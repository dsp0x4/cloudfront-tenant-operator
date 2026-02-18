# Architecture Overview

## Project Structure

```
cmd/main.go                              # Entrypoint, wires AWS client + controller + flags
api/v1alpha1/                            # CRD type definitions
internal/controller/
  distributiontenant_controller.go       # Reconciliation logic
  change_detection.go                    # Three-way diff and drift policy
internal/aws/
  client.go                              # CloudFrontClient interface
  cloudfront.go                          # Real AWS SDK implementation
  errors.go                              # Error classification (terminal vs retryable)
  mock.go                                # Mock for testing
internal/metrics/
  metrics.go                             # Prometheus metric definitions
```

## Reconciliation Flow

The controller follows a multi-phase reconciliation loop:

1. **Create**: Validate spec (name format, certificate coverage, required parameters), then call `CreateDistributionTenant`.
2. **Poll**: Requeue every 30 seconds until the AWS status transitions from `InProgress` to `Deployed`.
3. **Steady state**: A three-way diff compares the spec, observed generation, and AWS state:
    - **Spec change** (generation bumped): validate and push update to AWS.
    - **Drift** (generation matches, AWS differs): apply the configured [drift policy](drift-detection.md).
    - **No change**: update conditions, check [managed cert lifecycle](managed-certificates.md), requeue every 5 minutes.
4. **Managed cert**: Track `pending-validation` -> `issued` -> auto-attach ARN to spec -> push update to AWS.
5. **Delete**: Disable tenant -> wait for `Deployed` -> delete from AWS -> remove finalizer.

## Error Handling

Errors from the AWS API are classified into categories that determine retry behavior:

| Category | Examples | Behavior |
|----------|----------|----------|
| **Terminal** | `CNAMEAlreadyExists`, `AccessDenied`, `InvalidArgument` | Set condition, stop retrying, requeue after 5 min |
| **Throttling** | `Throttling`, `TooManyRequests` | Requeue with 60s backoff |
| **ETag mismatch** | `PreconditionFailed` | Re-fetch and retry via rate limiter (no error metric) |
| **Retryable** | 5xx errors, network errors | Return error for controller-runtime backoff |
| **Validation** | Name too short, missing certificate, missing parameters | Set condition with specific message, requeue after 5 min |

All AWS errors preserve the original error message from the API response, so users see the exact reason for failure in the status conditions.

## ResourceVersion Safety

The controller ensures **at most one Kubernetes API write** (spec update or status update) per reconcile loop. This prevents `resourceVersion` conflicts that occur when the in-memory object becomes stale after a write.

Operations that logically require multiple writes are split across reconcile cycles:

- **Certificate attachment**: Persists the certificate ARN to the spec (one write), then returns. The spec change triggers a watch event, and the next reconcile pushes the update to AWS.
- **Deletion**: Each step (disable, wait, delete, remove finalizer) is a separate reconcile with at most one write.

## Prometheus Metrics

### Custom metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `cloudfront_tenant_operator_reconcile_errors_total` | Counter | `error_type` | Incremented once per AWS API error classified by `handleAWSError`. **Label values:** `error_type` is one of `domain_conflict`, `access_denied`, `invalid_spec` (terminal), `throttling`, or `retryable`. Not incremented for spec validation failures or Kubernetes API errors. Per-resource errors are available via status conditions and events. |
| `cloudfront_tenant_operator_tenants_total` | Gauge | `namespace`, `status` | Current number of DistributionTenant resources per namespace. Implemented as a `prometheus.Collector` (kube-state-metrics / cert-manager pattern): the count is computed from the informer cache at Prometheus scrape time, not during reconciliation, so it is never stale and adds zero overhead to the reconcile loop. **Label values:** `status` is `Ready` (Ready condition is True) or `NotReady` (Ready condition is False or absent). |
| `cloudfront_tenant_operator_drift_detected_total` | Counter | *(none)* | Incremented once each time `handleDrift` fires, regardless of the configured drift policy (enforce, report, or suspend). Provides an aggregate signal for alerting on external AWS drift. Per-resource drift details are available via the `Synced` condition and `status.driftDetected` field. |
| `cloudfront_tenant_operator_aws_api_call_duration_seconds` | Histogram | `operation` | Wall-clock duration of each AWS CloudFront SDK call made by `RealCloudFrontClient`. Recorded for both successful and failed calls. **Label values:** `operation` is one of `CreateDistributionTenant`, `GetDistributionTenant`, `UpdateDistributionTenant`, `DeleteDistributionTenant`, `GetDistribution`, `GetManagedCertificateDetails`. |

### Built-in controller-runtime metrics

The following metrics are provided automatically by controller-runtime and cover reconcile duration, counts, and error totals per controller:

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `controller_runtime_reconcile_total` | Counter | `controller`, `result` | Total number of reconciliations. `result` is `success`, `error`, `requeue`, or `requeue_after`. |
| `controller_runtime_reconcile_errors_total` | Counter | `controller` | Total reconciliation errors (when `Reconcile` returns a non-nil error). |
| `controller_runtime_reconcile_time_seconds` | Histogram | `controller` | Wall-clock duration of each reconciliation loop. |
| `controller_runtime_active_workers` | Gauge | `controller` | Number of currently active reconcile workers. |
