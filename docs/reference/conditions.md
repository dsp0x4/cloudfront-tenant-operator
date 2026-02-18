# Conditions & Reasons

The operator sets standard Kubernetes conditions on the `DistributionTenant` status to communicate the resource's state.

## Condition Types

### Ready

Indicates whether the tenant is fully deployed and serving traffic.

| Status | Reason | Meaning |
|--------|--------|---------|
| `True` | `Deployed` | Tenant is deployed and serving traffic |
| `False` | `Creating` | AWS resource creation is in progress |
| `False` | `Deploying` | AWS deployment is in progress (after create or update) |
| `False` | `Disabling` | Tenant is being disabled before deletion |
| `False` | `Deleting` | Waiting for deployment to complete before deletion |
| `False` | `DomainConflict` | Domain is already used by another distribution (terminal) |
| `False` | `AccessDenied` | Insufficient IAM permissions (terminal) |
| `False` | `InvalidSpec` | AWS rejected the request as invalid (terminal) |
| `False` | `AWSError` | Other AWS API error (terminal) |
| `False` | `ValidationFailed` | Spec failed pre-flight validation |

### Synced

Indicates whether the K8s spec matches the AWS state.

| Status | Reason | Meaning |
|--------|--------|---------|
| `True` | `InSync` | Spec is in sync with AWS |
| `False` | `DriftDetected` | AWS state was modified outside the operator |
| `False` | `UpdatePending` | A spec change has been submitted but not yet deployed |

### CertificateReady

Indicates the status of a CloudFront-managed ACM certificate. Only set when `managedCertificateRequest` is configured in the spec.

| Status | Reason | Meaning |
|--------|--------|---------|
| `True` | `Validated` | Certificate is validated and attached to the tenant |
| `False` | `PendingValidation` | Certificate is awaiting DNS validation |
| `False` | `Attaching` | Certificate has been issued but is being attached to the tenant |
| `False` | `CertificateFailed` | Certificate validation failed or timed out |
| `False` | `NotConfigured` | No managed certificate request is configured |
