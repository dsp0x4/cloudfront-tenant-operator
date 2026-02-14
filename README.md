# cloudfront-tenant-operator

A Kubernetes operator for managing [CloudFront Distribution Tenants](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/distribution-tenants.html) -- the new AWS CloudFront SaaS Manager feature for multi-tenant content delivery.

## What It Does

The operator manages `DistributionTenant` custom resources that map 1:1 to AWS CloudFront distribution tenants. It handles:

- **Full lifecycle management**: Create, update, and delete distribution tenants via the AWS API
- **Disable-before-delete**: Automatically disables tenants before deletion (required by AWS)
- **ETag-based concurrency**: Fetches current state before every update to prevent conflicts
- **Error classification**: Distinguishes terminal errors (domain conflicts, permission issues) from retryable ones (throttling, network errors)
- **Status conditions**: Reports `Ready` and `Synced` conditions following Kubernetes conventions
- **Finalizer-based cleanup**: Ensures AWS resources are properly deleted before the K8s object is removed

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
        "cloudfront:DeleteDistributionTenant"
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

```yaml
apiVersion: cloudfront.cloudfront-tenant-operator.io/v1alpha1
kind: DistributionTenant
metadata:
  name: my-tenant
spec:
  distributionId: "E1XNX8R2GOAABC"     # Your multi-tenant distribution ID
  name: "my-tenant"
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
| `Synced=True` | K8s spec matches AWS state |

### Uninstall

```sh
kubectl delete -k config/samples/   # Delete CRs
make uninstall                       # Delete CRDs
make undeploy                        # Remove operator
```

## CRD Reference

See the full [CRD spec](api/v1alpha1/distributiontenant_types.go) and [example CRs](config/samples/) for all available fields.

### Spec Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `distributionId` | string | Yes | ID of the parent multi-tenant distribution |
| `name` | string | Yes | Friendly name (immutable after creation) |
| `domains` | array | Yes | List of domains to associate |
| `enabled` | bool | No | Whether to serve traffic (default: true) |
| `connectionGroupId` | string | No | Connection group ID |
| `parameters` | array | No | Key-value parameters for the distribution template |
| `customizations` | object | No | WAF, certificate, and geo restriction overrides |
| `managedCertificateRequest` | object | No | CloudFront-managed ACM certificate configuration |
| `tags` | array | No | AWS resource tags |

## Architecture

```
cmd/main.go                          # Entrypoint, wires AWS client + controller
api/v1alpha1/                        # CRD type definitions
internal/controller/                 # Reconciliation logic
internal/aws/
  client.go                          # CloudFrontClient interface
  cloudfront.go                      # Real AWS SDK implementation
  errors.go                          # Error classification (terminal vs retryable)
  mock.go                            # Mock for testing
```

### Reconciliation Flow

1. **Create**: If no AWS ID in status, call `CreateDistributionTenant`
2. **Wait**: Requeue until AWS status transitions from `InProgress` to `Deployed`
3. **Steady state**: Compare spec vs AWS state, update if changed
4. **Delete**: Disable tenant -> wait for `Deployed` -> delete from AWS -> remove finalizer

### Error Handling

- **Terminal errors** (`CNAMEAlreadyExists`, `AccessDenied`, `InvalidArgument`): Set condition, stop retrying
- **Retryable errors** (`Throttling`, `PreconditionFailed`, 5xx): Requeue with backoff

## Development

```sh
# Run tests
make test

# Run linter
make lint

# Regenerate CRD manifests and deep copy
make manifests generate
```

## Roadmap

- **Phase 2**: Certificate readiness tracking, drift detection, Prometheus metrics
- **Phase 3**: Helm chart, validation webhooks, CI/CD, GoReleaser
- **Phase 4**: External tenant sources (database), automatic DNS management, dry-run mode

## License

Copyright 2026. Licensed under the Apache License, Version 2.0.
