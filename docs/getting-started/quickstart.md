# Quickstart

This guide walks you through creating your first CloudFront distribution tenant.

## Prerequisites

Make sure you have [installed](installation.md) the operator and have a multi-tenant CloudFront distribution ready.

## Create a Distribution Tenant

The Kubernetes resource name (`metadata.name`) is used as the CloudFront tenant name. It must be 3-128 characters, start and end with a lowercase alphanumeric, and contain only lowercase alphanumerics, dots, and hyphens.

### Minimal Example

> **Note:** This minimal example does not specify a certificate. It only works if the domain is already covered by the default certificate of the parent distribution. Otherwise, add a `customizations.certificate` or a `managedCertificateRequest` to the spec.

```yaml
apiVersion: cloudfront-tenant-operator.io/v1alpha1
kind: DistributionTenant
metadata:
  name: my-tenant-minimal
spec:
  distributionId: "E1XNX8R2GOAABC"
  domains:
    - domain: "minimal.example.com"
```

Apply the minimal sample:

```sh
kubectl apply -f config/samples/cloudfront_v1alpha1_distributiontenant.yaml
```

### Full Example

The full example shows all available spec fields. See `config/samples/cloudfront_v1alpha1_distributiontenant_full.yaml`:

```yaml
apiVersion: cloudfront-tenant-operator.io/v1alpha1
kind: DistributionTenant
metadata:
  name: my-tenant-full
spec:
  distributionId: "E1XNX8R2GOAABC"
  domains:
    - domain: "full.example.com"
    - domain: "www.full.example.com"

  enabled: true
  connectionGroupId: "cg_2whCJoXMYCjHcxaLGrkllvyABC"

  parameters:
    - name: "tenantName"
      value: "full-tenant"
    - name: "originPath"
      value: "/tenants/full"

  customizations:
    webAcl:
      action: "override"
      arn: "arn:aws:wafv2:us-east-1:123456789012:global/webacl/my-tenant-waf/abc123"
    certificate:
      arn: "arn:aws:acm:us-east-1:123456789012:certificate/abc12345-1234-1234-1234-abc123456789"
    geoRestrictions:
      restrictionType: "whitelist"
      locations: ["US", "CA", "GB", "DE", "FR"]

  tags:
    - key: "env"
      value: "production"
    - key: "team"
      value: "platform"
```

Additional samples are available for [DNS management](https://github.com/dsp0x4/cloudfront-tenant-operator/tree/main/config/samples/cloudfront_v1alpha1_distributiontenant_with_dns.yaml) and [managed certificates](https://github.com/dsp0x4/cloudfront-tenant-operator/tree/main/config/samples/cloudfront_v1alpha1_distributiontenant_managed_cert.yaml).

## Monitor Status

```sh
# Watch tenant status
kubectl get distributiontenants -w

# Detailed status
kubectl describe distributiontenant my-tenant
```

The `STATUS` column shows the AWS deployment state, and the `READY` and `SYNCED` columns show the Kubernetes conditions:

```
NAME        STATUS      READY   SYNCED   ID                    AGE
my-tenant   InProgress  False   True     dt-abc123def456       10s
my-tenant   Deployed    True    True     dt-abc123def456       45s
```

## Update a Tenant

Edit the spec and re-apply. The operator detects the change via a three-way diff and pushes the update to AWS:

```sh
kubectl edit distributiontenant my-tenant
```

## Delete a Tenant

```sh
kubectl delete distributiontenant my-tenant
```

The operator will automatically disable the tenant, wait for the disable to propagate, then delete the AWS resource and remove the finalizer.
