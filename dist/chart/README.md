# cloudfront-tenant-operator

A Kubernetes operator for managing [CloudFront Distribution Tenants](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/distribution-tenants.html) — the AWS CloudFront multi-tenant content delivery feature.

> **Work in Progress** — This project is under active development. APIs and behavior may change without notice.

## Installation

```bash
helm install cloudfront-tenant-operator \
  oci://ghcr.io/dsp0x4/cloudfront-tenant-operator/charts/cloudfront-tenant-operator \
  --namespace cloudfront-tenant-operator-system \
  --create-namespace
```

## Prerequisites

- Kubernetes 1.28+
- Helm 3.x
- AWS credentials with CloudFront permissions (see [IAM Permissions](#iam-permissions))

## Configuration

The following table lists the main configurable parameters. See [values.yaml](values.yaml) for the full list.

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of controller replicas | `1` |
| `image.repository` | Controller image repository | `ghcr.io/dsp0x4/cloudfront-tenant-operator` |
| `image.tag` | Controller image tag (defaults to appVersion) | `""` |
| `aws.region` | AWS region for API calls | `us-east-1` |
| `controller.driftPolicy` | How to handle external drift: `enforce`, `report`, or `suspend` | `enforce` |
| `controller.maxConcurrentReconciles` | Maximum concurrent reconcile loops | `1` |
| `leaderElection.enabled` | Enable leader election for HA | `true` |
| `metrics.bindAddress` | Bind address for the metrics endpoint | `:8443` |
| `metrics.secure` | Serve metrics over HTTPS | `true` |
| `serviceAccount.create` | Create a service account | `true` |
| `serviceAccount.annotations` | Service account annotations (e.g., for IRSA) | `{}` |
| `resources.limits.cpu` | CPU limit | `500m` |
| `resources.limits.memory` | Memory limit | `128Mi` |
| `resources.requests.cpu` | CPU request | `10m` |
| `resources.requests.memory` | Memory request | `64Mi` |
| `extraEnv` | Additional environment variables for the controller | `[]` |

## IAM Permissions

The controller requires the following AWS IAM permissions:

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
        "cloudfront:GetManagedCertificateDetails",
        "cloudfront:GetConnectionGroup",
        "cloudfront:ListConnectionGroups",
        "acm:RequestCertificate",
        "acm:DescribeCertificate"
      ],
      "Resource": "*"
    }
  ]
}
```

If you use `spec.dns` for automatic Route53 record management, also add:

```json
{
  "Effect": "Allow",
  "Action": [
    "route53:ChangeResourceRecordSets",
    "route53:GetChange"
  ],
  "Resource": "*"
}
```

## Upgrading

Helm does **not** automatically upgrade CRDs. After upgrading the chart, apply the latest CRDs manually:

```bash
kubectl apply -f https://raw.githubusercontent.com/dsp0x4/cloudfront-tenant-operator/v<version>/dist/chart/crds/cloudfront-tenant-operator.io_distributiontenants.yaml
kubectl apply -f https://raw.githubusercontent.com/dsp0x4/cloudfront-tenant-operator/v<version>/dist/chart/crds/cloudfront-tenant-operator.io_tenantsources.yaml
```

## Uninstalling

```bash
helm uninstall cloudfront-tenant-operator -n cloudfront-tenant-operator-system
```

> **Note:** Uninstalling the chart does not remove CRDs or custom resources. Delete custom resources before uninstalling to ensure AWS resources are cleaned up by the controller.

## Documentation

- [Full Documentation](https://cloudfront-tenant-operator.io)
- [Quickstart](https://cloudfront-tenant-operator.io/getting-started/quickstart)
- [CRD Reference](https://cloudfront-tenant-operator.io/reference/crd)
- [Architecture](https://cloudfront-tenant-operator.io/architecture/overview)

## Disclaimer

This project is not affiliated with, endorsed by, or sponsored by Amazon Web Services (AWS). All AWS service names and trademarks are the property of Amazon.com, Inc. or its affiliates.
