# Architecture Overview

## Project Structure

```
cmd/main.go                              # Entrypoint, wires AWS client + controller + flags
api/v1alpha1/                            # CRD type definitions
internal/controller/
  distributiontenant_controller.go       # Reconciliation logic (incl. DNS management)
  change_detection.go                    # Three-way diff and drift policy
internal/aws/
  client.go                              # CloudFrontClient, DNSClient, ACMClient interfaces
  cloudfront.go                          # Real AWS CloudFront SDK implementation
  route53.go                             # Real AWS Route53 SDK implementation (DNSClient)
  acm.go                                 # Real AWS ACM SDK implementation (ACMClient)
  errors.go                              # Error classification (terminal vs retryable)
  mock.go                                # Mock clients for testing
internal/metrics/
  metrics.go                             # Prometheus metric definitions
  tenant_collector.go                    # prometheus.Collector for tenant counts
```

## Reconciliation Flow

The controller follows a multi-phase reconciliation loop:

1. **Create** (DNS-first when configured):
    1. Validate spec (name format, certificate coverage, required parameters).
    2. If DNS is configured and no managed cert: validate that the ACM certificate's SANs cover all tenant domains.
    3. Resolve CNAME target: the specified connection group's routing endpoint, or the account's default connection group endpoint if none is specified (multi-tenant distributions always route through a connection group).
    4. Upsert CNAME records in Route53 and store the change ID in status.
    5. Poll Route53 `GetChange` until the change reaches `INSYNC`.
    6. Call `CreateDistributionTenant`.
2. **Poll**: Requeue every 30 seconds until the AWS status transitions from `InProgress` to `Deployed`.
3. **Steady state**: A three-way diff compares the spec, observed generation, and AWS state:
    - **Spec change** (generation bumped): validate, upsert DNS for current domains, push update to AWS, clean up orphaned DNS records on next steady-state cycle.
    - **Drift** (generation matches, AWS differs): apply the configured [drift policy](drift-detection.md).
    - **No change**: update conditions, check [managed cert lifecycle](managed-certificates.md), clean up orphaned DNS records, requeue every 5 minutes.
4. **Managed cert**: Track `pending-validation` -> `issued` -> auto-attach ARN to spec -> push update to AWS.
5. **Delete**: Disable tenant -> wait for `Deployed` -> delete from AWS -> delete DNS records from Route53 -> remove finalizer.

## Error Handling

Errors from the AWS API are classified into categories that determine retry behavior:

| Category | Examples | Behavior |
|----------|----------|----------|
| **Terminal** | `CNAMEAlreadyExists`, `AccessDenied`, `InvalidArgument` | Set condition, stop retrying, requeue after 5 min |
| **Domain validation pending** | `InvalidArgument` when `validationTokenHost=cloudfront` | Set `DomainValidationPending` condition, retry every 5 min (see below) |
| **Terminal (DNS)** | `NoSuchHostedZone`, DNS `AccessDenied`, `InvalidChangeBatch`, `ConnectionGroupNotFound` | Set DNSReady=False condition, stop retrying |
| **Terminal (Cert)** | Certificate SAN mismatch (domains not covered) | Set Ready=False with `CertificateSANMismatch` reason |
| **Throttling** | `Throttling`, `TooManyRequests` | Requeue with 60s backoff |
| **Throttling (DNS)** | Route53 `Throttling`, `PriorRequestNotComplete` | Requeue with 60s backoff |
| **ETag mismatch** | `PreconditionFailed` | Re-fetch and retry via rate limiter (no error metric) |
| **Retryable** | 5xx errors, network errors | Return error for controller-runtime backoff |
| **Validation** | Name too short, missing certificate, missing parameters | Set condition with specific message, requeue after 5 min |

**Domain validation pending**: When `CreateDistributionTenant` fails with `InvalidArgument` and the tenant uses `managedCertificateRequest.validationTokenHost: "cloudfront"`, the error is treated as a transient DNS propagation delay instead of terminal. CloudFront requires the CNAME to be globally resolvable before it can serve the HTTP validation token, but Route53 `INSYNC` only guarantees authoritative nameserver updates -- global recursive resolver caches may lag by minutes to hours. This applies to both operator-managed DNS (`spec.dns` configured) and user-managed DNS. The operator sets `Ready=False` with reason `DomainValidationPending` and retries every 5 minutes until the create succeeds.

All AWS errors preserve the original error message from the API response, so users see the exact reason for failure in the status conditions.

## ResourceVersion Safety

The controller ensures **at most one Kubernetes API write** (spec update or status update) per reconcile loop. This prevents `resourceVersion` conflicts that occur when the in-memory object becomes stale after a write.

Operations that logically require multiple writes are split across reconcile cycles:

- **Certificate attachment**: Persists the certificate ARN to the spec (one write), then returns. The spec change triggers a watch event, and the next reconcile pushes the update to AWS.
- **Deletion**: Each step (disable, wait, delete, remove finalizer) is a separate reconcile with at most one write.

## DNS Record Management

When `spec.dns` is configured, the operator manages CNAME records in Route53 that point the tenant's domains to the CloudFront distribution or connection group endpoint.

### Conditions

| Condition | Status | Reason | Meaning |
|-----------|--------|--------|---------|
| `DNSReady` | `True` | `DNSReady` | CNAME records are propagated in Route53 |
| `DNSReady` | `True` | `DNSNotConfigured` | `spec.dns` is not set; DNS is not managed |
| `DNSReady` | `False` | `DNSRecordCreating` | CNAME records were submitted to Route53 |
| `DNSReady` | `False` | `DNSPropagating` | Waiting for Route53 change to reach INSYNC |
| `DNSReady` | `False` | `DNSError` | A terminal DNS error occurred (check message) |
| `Ready` | `False` | `CertificateSANMismatch` | The ACM certificate's SANs don't cover the tenant's domains |
| `Ready` | `False` | `DomainValidationPending` | CloudFront cannot verify domain ownership yet (DNS propagation delay); retrying |

### Cross-Account DNS

Use `spec.dns.assumeRoleArn` when the Route53 hosted zone is in a different AWS account. The operator uses STS `AssumeRole` to obtain temporary credentials for Route53 API calls only. CloudFront and ACM calls always use the operator's own credentials.

### Required IAM Permissions

The operator's IAM identity (or the assumed role for DNS) needs the following AWS permissions:

| Permission | Resource | Used for |
|-----------|----------|----------|
| `cloudfront:CreateDistributionTenant` | `*` | Creating tenants |
| `cloudfront:GetDistributionTenant` | `*` | Reading tenant state |
| `cloudfront:UpdateDistributionTenant` | `*` | Updating tenants |
| `cloudfront:DeleteDistributionTenant` | `*` | Deleting tenants |
| `cloudfront:GetDistribution` | `*` | Reading distribution config |
| `cloudfront:GetManagedCertificateDetails` | `*` | Managed certificate lifecycle |
| `cloudfront:GetConnectionGroup` | `*` | Resolving a connection group's routing endpoint |
| `cloudfront:ListConnectionGroups` | `*` | Finding the default connection group's routing endpoint |
| `acm:RequestCertificate` | `*` | Requesting managed ACM certificates |
| `acm:DescribeCertificate` | Certificate ARN | Validating certificate SANs cover tenant domains |
| `route53:ChangeResourceRecordSets` | Hosted zone ARN | Creating and deleting CNAME records (DNS only) |
| `route53:GetChange` | `*` | Polling for record propagation (DNS only) |
| `sts:AssumeRole` | Role ARN from `assumeRoleArn` | Cross-account Route53 access (only when configured) |

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--metrics-bind-address` | `0` | Address the metrics endpoint binds to (`:8443` for HTTPS, `:8080` for HTTP, `0` to disable) |
| `--health-probe-bind-address` | `:8081` | Address the health/ready probe endpoint binds to |
| `--leader-elect` | `false` | Enable leader election (required for HA deployments) |
| `--aws-region` | *(SDK default)* | AWS region for API calls; falls back to env vars / config file / IMDS |
| `--drift-policy` | `enforce` | How to handle external drift: `enforce`, `report`, or `suspend` |
| `--max-concurrent-reconciles` | `1` | Maximum number of concurrent reconcile loops |
| `--metrics-secure` | `true` | Serve metrics over HTTPS (`false` for HTTP) |
| `--metrics-cert-path` | *(empty)* | Directory containing TLS cert/key for the metrics server |
| `--webhook-cert-path` | *(empty)* | Directory containing TLS cert/key for the webhook server |
| `--enable-http2` | `false` | Enable HTTP/2 for metrics and webhook servers |

## Prometheus Metrics

### Custom metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `cloudfront_tenant_operator_reconcile_errors_total` | Counter | `error_type` | Incremented once per AWS API error classified by `handleAWSError`, `handleDNSError`, or `handleDomainValidationPending`. **Label values:** `error_type` is one of `domain_conflict`, `access_denied`, `invalid_spec`, `connection_group_not_found` (CloudFront terminal), `domain_validation_pending` (DNS propagation delay with cloudfront-hosted validation), `dns_zone_not_found`, `dns_access_denied`, `dns_invalid_input`, `dns_error` (DNS terminal), `throttling`, `dns_throttling` (rate limited), `retryable`, or `dns_retryable`. Not incremented for spec validation failures or Kubernetes API errors. |
| `cloudfront_tenant_operator_tenants_total` | Gauge | `namespace`, `status` | Current number of DistributionTenant resources per namespace. Implemented as a `prometheus.Collector` (kube-state-metrics / cert-manager pattern): the count is computed from the informer cache at Prometheus scrape time, not during reconciliation, so it is never stale and adds zero overhead to the reconcile loop. **Label values:** `status` is `Ready` (Ready condition is True) or `NotReady` (Ready condition is False or absent). |
| `cloudfront_tenant_operator_drift_detected_total` | Counter | *(none)* | Incremented once each time `handleDrift` fires, regardless of the configured drift policy (enforce, report, or suspend). Provides an aggregate signal for alerting on external AWS drift. Per-resource drift details are available via the `Synced` condition and `status.driftDetected` field. |
| `cloudfront_tenant_operator_aws_api_call_duration_seconds` | Histogram | `operation` | Wall-clock duration of each AWS SDK call. Recorded for both successful and failed calls. **Label values:** `operation` is one of `CreateDistributionTenant`, `GetDistributionTenant`, `UpdateDistributionTenant`, `DeleteDistributionTenant`, `GetDistribution`, `GetManagedCertificateDetails`, `GetConnectionGroup`, `ListConnectionGroups`, `Route53ChangeResourceRecordSets`, `Route53GetChange`, `ACMDescribeCertificate`. |

### Built-in controller-runtime metrics

The following metrics are provided automatically by controller-runtime and cover reconcile duration, counts, and error totals per controller:

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `controller_runtime_reconcile_total` | Counter | `controller`, `result` | Total number of reconciliations. `result` is `success`, `error`, `requeue`, or `requeue_after`. |
| `controller_runtime_reconcile_errors_total` | Counter | `controller` | Total reconciliation errors (when `Reconcile` returns a non-nil error). |
| `controller_runtime_reconcile_time_seconds` | Histogram | `controller` | Wall-clock duration of each reconciliation loop. |
| `controller_runtime_active_workers` | Gauge | `controller` | Number of currently active reconcile workers. |
