# Installation

## Prerequisites

- Go 1.24+
- Docker 17.03+
- kubectl v1.28+
- Access to a Kubernetes v1.28+ cluster
- AWS credentials with CloudFront permissions

## Required AWS IAM Permissions

The operator needs the following IAM permissions:

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

| Permission | Used For |
|------------|----------|
| `CreateDistributionTenant` | Creating new tenants |
| `GetDistributionTenant` | Fetching current state for drift detection and status updates |
| `UpdateDistributionTenant` | Pushing spec changes and disabling before deletion |
| `DeleteDistributionTenant` | Removing tenants from AWS |
| `GetDistribution` | Pre-flight validation (certificate coverage, required parameters) |
| `GetManagedCertificateDetails` | Tracking managed certificate lifecycle |

## Install CRDs

```sh
make install
```

## Run Locally (Development)

```sh
# Uses your local AWS credentials and kubeconfig
make run
```

## Deploy to Cluster

**Build and push the operator image:**

```sh
make docker-build docker-push IMG=<your-registry>/cloudfront-tenant-operator:latest
```

**Deploy the operator:**

```sh
make deploy IMG=<your-registry>/cloudfront-tenant-operator:latest
```

## Configure AWS Credentials

The operator pod needs AWS credentials to call the CloudFront API. Recommended approaches:

- [IAM Roles for Service Accounts (IRSA)](https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html) on EKS (recommended)
- [Pod Identity](https://docs.aws.amazon.com/eks/latest/userguide/pod-identities.html) on EKS
- Kubernetes secrets mounted as environment variables

## Uninstall

```sh
kubectl delete -k config/samples/   # Delete CRs
make uninstall                       # Delete CRDs
make undeploy                        # Remove operator
```
