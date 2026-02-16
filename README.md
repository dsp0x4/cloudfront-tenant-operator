# cloudfront-tenant-operator

A Kubernetes operator for managing [CloudFront Distribution Tenants](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/distribution-tenants.html) -- the AWS CloudFront multi-tenant content delivery feature.

## What It Does

The operator manages `DistributionTenant` custom resources that map 1:1 to AWS CloudFront distribution tenants. It handles:

- **Full lifecycle management**: Create, update, and delete distribution tenants via the AWS API
- **Disable-before-delete**: Automatically disables tenants before deletion (required by AWS)
- **ETag-based concurrency**: Uses optimistic concurrency control on every update to prevent conflicts
- **Drift detection**: Three-way diff (spec vs observed generation vs AWS state) distinguishes user-initiated changes from external drift, with configurable policy (`enforce`, `report`, `suspend`)
- **Managed certificate lifecycle**: Tracks CloudFront-managed ACM certificates through validation, issuance, and automatic attachment to the tenant
- **Pre-flight validation**: Validates the resource name, certificate coverage, and required parameters against the parent distribution before calling AWS
- **Error classification**: Distinguishes terminal errors (domain conflicts, permission issues) from retryable ones (throttling, network errors) with detailed AWS error messages
- **Status conditions**: Reports `Ready`, `Synced`, and `CertificateReady` conditions following Kubernetes conventions
- **Prometheus metrics**: Exposes reconciliation duration, error counts, drift detections, and AWS API call latency
- **Finalizer-based cleanup**: Ensures AWS resources are properly disabled and deleted before the K8s object is removed

## Prerequisites

- Go 1.24+
- Docker 17.03+
- kubectl v1.28+
- Access to a Kubernetes v1.28+ cluster
- AWS credentials with CloudFront permissions

### Required AWS IAM Permissions

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "cloudfront:CreateDistributionTenant",
        "cloudfront:GetDistributionTenant",
        "cloudfront:UpdateDistributionTenant",
        "cloudfront:DeleteDistributionTenant",
        "cloudfront:GetDistribution",
        "cloudfront:GetManagedCertificateDetails"
      ],
      "Resource": "*"
    }
  ]
}
```

## Getting Started

### Install CRDs

```sh
make install
```

### Run Locally (Development)

```sh
# Uses your local AWS credentials and kubeconfig
make run
```

### Deploy to Cluster

**Build and push the operator image:**

```sh
make docker-build docker-push IMG=<your-registry>/cloudfront-tenant-operator:latest
```

**Deploy the operator:**

```sh
make deploy IMG=<your-registry>/cloudfront-tenant-operator:latest
```

**Configure AWS credentials** for the operator pod using one of:
- [IAM Roles for Service Accounts (IRSA)](https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html) on EKS (recommended)
- [Pod Identity](https://docs.aws.amazon.com/eks/latest/userguide/pod-identities.html) on EKS
- Kubernetes secrets mounted as environment variables

### Create a Distribution Tenant

The Kubernetes resource name (`metadata.name`) is used as the CloudFront tenant name. It must be 3-128 characters, start and end with a lowercase alphanumeric, and contain only lowercase alphanumerics, dots, and hyphens.

```yaml
apiVersion: cloudfront-tenant-operator.io/v1alpha1
kind: DistributionTenant
metadata:
  name: my-tenant
spec:
  distributionId: "E1XNX8R2GOAABC"     # Your multi-tenant distribution ID
  domains:
    - domain: "my-tenant.example.com"
```

```sh
kubectl apply -f config/samples/cloudfront_v1alpha1_distributiontenant.yaml
```

### Monitor Status

```sh
# Watch tenant status
kubectl get distributiontenants -w

# Detailed status
kubectl describe distributiontenant my-tenant
```

The operator reports standard Kubernetes conditions:

| Condition | Meaning |
|-----------|---------|
| `Ready=True` | Tenant is deployed and serving traffic |
| `Ready=False, reason=Deploying` | AWS deployment in progress |
| `Ready=False, reason=DomainConflict` | Domain already used by another distribution (terminal) |
| `Ready=False, reason=ValidationFailed` | Spec failed pre-flight validation (name, cert, parameters) |
| `Synced=True` | K8s spec matches AWS state |
| `Synced=False, reason=DriftDetected` | AWS state was modified outside the operator |
| `CertificateReady=True` | Managed certificate validated and attached |
| `CertificateReady=False, reason=PendingValidation` | Certificate is awaiting DNS validation |

## Controller Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--drift-policy` | `enforce` | How to handle external drift: `enforce` (overwrite AWS), `report` (log + set conditions), `suspend` (skip drift checks) |
| `--aws-region` | *(SDK default)* | AWS region for CloudFront API calls |
| `--metrics-bind-address` | `0` | Address for the metrics endpoint (`0` = disabled) |
| `--health-probe-bind-address` | `:8081` | Address for health probes |
| `--leader-elect` | `false` | Enable leader election for HA deployments |

## CRD Reference

See the full [CRD spec](api/v1alpha1/distributiontenant_types.go) and [example CRs](config/samples/) for all available fields.

### Spec Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `distributionId` | string | Yes | ID of the parent multi-tenant distribution |
| `domains` | array | Yes | List of domains to associate |
| `enabled` | bool | No | Whether to serve traffic (default: true) |
| `connectionGroupId` | string | No | Connection group ID |
| `parameters` | array | No | Key-value parameters for the distribution template |
| `customizations` | object | No | WAF, certificate, and geo restriction overrides |
| `managedCertificateRequest` | object | No | CloudFront-managed ACM certificate configuration |
| `tags` | array | No | AWS resource tags |
| `dns` | object | No | DNS record management config (Phase 4, not yet implemented) |

### Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | AWS-assigned distribution tenant ID |
| `arn` | string | Amazon Resource Name |
| `eTag` | string | Version identifier for optimistic concurrency |
| `distributionTenantStatus` | string | AWS deployment status (`InProgress`, `Deployed`) |
| `observedGeneration` | int64 | Last generation successfully reconciled (for drift detection) |
| `certificateArn` | string | ARN of the associated ACM certificate |
| `managedCertificateStatus` | string | Managed cert lifecycle status (`pending-validation`, `issued`, etc.) |
| `driftDetected` | bool | Whether external drift was detected |
| `lastDriftCheckTime` | time | Timestamp of the last drift check |
| `domainResults` | array | Per-domain status from AWS (`active`/`inactive`) |
| `conditions` | array | Standard Kubernetes conditions (`Ready`, `Synced`, `CertificateReady`) |

## Architecture

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

### Reconciliation Flow

1. **Create**: Validate spec (name, cert coverage, required params) -> call `CreateDistributionTenant`
2. **Poll**: Requeue every 30s until AWS status transitions from `InProgress` to `Deployed`
3. **Steady state**: Three-way diff compares spec, observed generation, and AWS state
   - **Spec change** (generation bumped): validate and push update to AWS
   - **Drift** (generation matches, AWS differs): apply configured drift policy
   - **No change**: update conditions, check managed cert lifecycle, requeue every 5min
4. **Managed cert**: Track `pending-validation` -> `issued` -> auto-attach ARN to spec -> push update
5. **Delete**: Disable tenant -> wait for `Deployed` -> delete from AWS -> remove finalizer

### Error Handling

- **Terminal errors** (`CNAMEAlreadyExists`, `AccessDenied`, `InvalidArgument`): Set condition, don't retry, requeue after 5min
- **Retryable errors** (`Throttling`, `PreconditionFailed`, 5xx): Requeue with backoff
- **ETag mismatch**: Re-fetch and retry via rate limiter (no error metric increment)
- **Validation errors**: Set condition with specific message, requeue after 5min (user must fix spec)

### ResourceVersion Safety

The controller ensures at most one Kubernetes API write (spec update or status update) per reconcile loop to prevent `resourceVersion` conflicts. Operations that require multiple writes (e.g., cert attachment followed by status update) are split across reconcile cycles using watch events and short requeues.

## Development

```sh
# Run tests (unit + envtest)
make test

# Run linter
make lint

# Auto-fix lint issues
make lint-fix

# Regenerate CRD manifests and deep copy
make manifests generate
```

## Roadmap

- Validation webhooks for immediate feedback on invalid specs
- Helm chart distribution
- External tenant sources (database-driven tenant provisioning)
- Automatic DNS record management (Route53 CNAME creation)
- Dry-run mode for safe change previews

## License

Copyright 2026. Licensed under the Apache License, Version 2.0.
