# Installation

## Prerequisites

- kubectl v1.28+
- Access to a Kubernetes v1.28+ cluster
- AWS credentials with CloudFront permissions
- Helm 3.x (for Helm-based installation)

For development, you additionally need:

- Go 1.25+
- Docker 17.03+

## Required AWS IAM Permissions

### Core permissions (required)

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
        "cloudfront:ListConnectionGroups"
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
| `GetConnectionGroup` | Resolving a connection group's routing endpoint for DNS |
| `ListConnectionGroups` | Finding the default connection group's routing endpoint |

### DNS management permissions (optional)

Required only if you configure `spec.dns` on your tenants:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "route53:ChangeResourceRecordSets",
        "route53:GetChange"
      ],
      "Resource": "*"
    },
    {
      "Effect": "Allow",
      "Action": "acm:DescribeCertificate",
      "Resource": "*"
    }
  ]
}
```

If you use `spec.dns.assumeRoleArn` for cross-account DNS, the operator also needs `sts:AssumeRole` on the target role.

## Install with Helm (Recommended)

The chart is published as an OCI artifact. CRDs are bundled in the chart's `crds/` directory and are automatically installed on first `helm install`.

### Install from OCI registry

```sh
helm install cloudfront-tenant-operator \
  oci://ghcr.io/dsp0x4/charts/cloudfront-tenant-operator \
  --version <version> \
  --namespace cloudfront-tenant-operator-system \
  --create-namespace
```

> **Private registries:** If the chart is hosted in a private OCI registry, log in first:
> ```sh
> helm registry login ghcr.io -u <username>
> ```

### Install from local source

If you cloned the repository, you can install directly from the chart directory:

```sh
helm install cloudfront-tenant-operator dist/chart/ \
  --namespace cloudfront-tenant-operator-system \
  --create-namespace
```

### Configuration

Override defaults via `--set` or a custom values file:

```sh
helm install cloudfront-tenant-operator \
  oci://ghcr.io/dsp0x4/charts/cloudfront-tenant-operator \
  --version <version> \
  --namespace cloudfront-tenant-operator-system \
  --create-namespace \
  --set aws.region=us-east-1 \
  --set controller.driftPolicy=report \
  --set controller.maxConcurrentReconciles=3
```

Or with a values file:

```sh
helm install cloudfront-tenant-operator \
  oci://ghcr.io/dsp0x4/charts/cloudfront-tenant-operator \
  --version <version> \
  --namespace cloudfront-tenant-operator-system \
  --create-namespace \
  -f my-values.yaml
```

See `dist/chart/values.yaml` for all available options.

### Upgrade

Helm does **not** update CRDs on upgrade (by design, to prevent accidental data loss). When upgrading to a version with CRD changes, apply the CRDs manually **before** running `helm upgrade`.

**From OCI registry:**

```sh
# 1. Pull the chart to extract updated CRDs
helm pull oci://ghcr.io/dsp0x4/charts/cloudfront-tenant-operator \
  --version <new-version> --untar --untardir /tmp

# 2. Apply updated CRDs
kubectl apply -f /tmp/cloudfront-tenant-operator/crds/

# 3. Upgrade the operator
helm upgrade cloudfront-tenant-operator \
  oci://ghcr.io/dsp0x4/charts/cloudfront-tenant-operator \
  --version <new-version> \
  --namespace cloudfront-tenant-operator-system
```

**From local source:**

```sh
# 1. Apply updated CRDs
kubectl apply -f dist/chart/crds/

# 2. Upgrade the operator
helm upgrade cloudfront-tenant-operator dist/chart/ \
  --namespace cloudfront-tenant-operator-system
```

### Uninstall

```sh
# Remove the operator (CRDs and CRs are preserved)
helm uninstall cloudfront-tenant-operator \
  --namespace cloudfront-tenant-operator-system

# If you also want to remove CRDs (this deletes ALL DistributionTenant resources):
kubectl delete -f dist/chart/crds/
```

## Install with Kustomize

### Install CRDs

```sh
make install
```

### Deploy the operator

**Build and push the operator image:**

```sh
make docker-build docker-push IMG=<your-registry>/cloudfront-tenant-operator:latest
```

**Deploy:**

```sh
make deploy IMG=<your-registry>/cloudfront-tenant-operator:latest
```

### Uninstall

```sh
kubectl delete -k config/samples/   # Delete CRs
make undeploy                        # Remove operator
make uninstall                       # Delete CRDs
```

## Run Locally (Development)

```sh
# Uses your local AWS credentials and kubeconfig
make run
```

## Configure AWS Credentials

The operator pod needs AWS credentials to call the CloudFront and Route53 APIs. On EKS, use one of the native IAM integration methods below. On non-EKS clusters, fall back to environment variables or mounted secrets.

### EKS Pod Identity (recommended)

[EKS Pod Identity](https://docs.aws.amazon.com/eks/latest/userguide/pod-identities.html) is the recommended approach. It requires no service account annotations and works at the cluster level.

**1. Install the Pod Identity Agent add-on** (one-time per cluster):

```sh
aws eks create-addon \
  --cluster-name <cluster> \
  --addon-name eks-pod-identity-agent
```

**2. Create an IAM role** with a trust policy for Pod Identity:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": { "Service": "pods.eks.amazonaws.com" },
      "Action": ["sts:AssumeRole", "sts:TagSession"]
    }
  ]
}
```

Attach the [required IAM permissions](#required-aws-iam-permissions) to this role.

**3. Create the Pod Identity association:**

```sh
aws eks create-pod-identity-association \
  --cluster-name <cluster> \
  --namespace cloudfront-tenant-operator-system \
  --service-account <service-account-name> \
  --role-arn arn:aws:iam::123456789012:role/my-operator-role
```

Replace `<service-account-name>` with the service account created by the Helm chart (defaults to the release name).

No Helm value changes are needed -- the Pod Identity Agent injects credentials automatically.

### IAM Roles for Service Accounts (IRSA)

[IRSA](https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html) works by annotating the service account with an IAM role ARN. It requires an OIDC provider configured on the cluster.

With Helm:

```sh
helm install cloudfront-tenant-operator dist/chart/ \
  --namespace cloudfront-tenant-operator-system \
  --create-namespace \
  --set serviceAccount.annotations."eks\.amazonaws\.com/role-arn"=arn:aws:iam::123456789012:role/my-operator-role
```

### Static credentials (non-EKS)

For non-EKS clusters, inject AWS credentials via environment variables using the `extraEnv` chart value:

```yaml
extraEnv:
  - name: AWS_ACCESS_KEY_ID
    valueFrom:
      secretKeyRef:
        name: aws-credentials
        key: access-key-id
  - name: AWS_SECRET_ACCESS_KEY
    valueFrom:
      secretKeyRef:
        name: aws-credentials
        key: secret-access-key
  - name: AWS_REGION
    value: us-east-1
```
