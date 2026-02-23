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

### DNSReady

Indicates whether DNS records have been created and propagated in Route53. Only relevant when `spec.dns` is configured.

| Status | Reason | Meaning |
|--------|--------|---------|
| `True` | `DNSReady` | CNAME records are propagated in Route53 |
| `True` | `DNSNotConfigured` | `spec.dns` is not set; DNS is not managed by the operator |
| `False` | `DNSRecordCreating` | CNAME records were submitted to Route53 |
| `False` | `DNSPropagating` | Waiting for Route53 change to reach INSYNC |
| `False` | `DNSError` | A terminal DNS error occurred (check condition message) |

## Additional Ready Reasons

These reasons are set on the `Ready` condition in specific scenarios:

| Status | Reason | Meaning |
|--------|--------|---------|
| `False` | `CertificateSANMismatch` | The ACM certificate's SANs don't cover the tenant's domains |
| `False` | `DomainValidationPending` | CloudFront cannot verify domain ownership yet (DNS propagation delay); retries every 5 minutes |
| `False` | `MissingParameters` | Required distribution parameters are missing from the spec |
| `False` | `MissingCertificate` | No certificate configured for a distribution that requires one |
