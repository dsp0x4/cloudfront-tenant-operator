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

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `cloudfront_tenant_operator_reconcile_duration_seconds` | Histogram | `namespace`, `name`, `result` | Duration of reconciliation loops |
| `cloudfront_tenant_operator_reconcile_errors_total` | Counter | `namespace`, `name`, `error_type` | Total reconciliation errors by type |
| `cloudfront_tenant_operator_tenants_total` | Gauge | `namespace`, `status` | Number of managed tenants by status |
| `cloudfront_tenant_operator_drift_detected_total` | Counter | `namespace`, `name` | Total drift detections |
| `cloudfront_tenant_operator_aws_api_call_duration_seconds` | Histogram | `operation` | Duration of AWS API calls |
