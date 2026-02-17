# CRD Specification

Full reference for the `DistributionTenant` custom resource.

See the [Go type definitions](https://github.com/paolo-desantis/cloudfront-tenant-operator/blob/main/api/v1alpha1/distributiontenant_types.go) and [example CRs](https://github.com/paolo-desantis/cloudfront-tenant-operator/tree/main/config/samples/) for additional detail.

## Spec Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `distributionId` | string | Yes | ID of the parent multi-tenant distribution |
| `domains` | array of `DomainSpec` | Yes | List of domains to associate (min 1) |
| `enabled` | bool | No | Whether to serve traffic (default: `true`) |
| `connectionGroupId` | string | No | Connection group ID |
| `parameters` | array of `Parameter` | No | Key-value parameters for the distribution template |
| `customizations` | `Customizations` | No | WAF, certificate, and geo restriction overrides |
| `managedCertificateRequest` | `ManagedCertificateRequest` | No | CloudFront-managed ACM certificate configuration |
| `tags` | array of `Tag` | No | AWS resource tags |
| `dns` | `DNSConfig` | No | DNS record management config (not yet implemented) |

### DomainSpec

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `domain` | string | Yes | Fully qualified domain name |

### Parameter

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Parameter name (must match a parameter defined in the distribution) |
| `value` | string | Yes | Parameter value |

### Customizations

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `webAcl` | `WebAclCustomization` | No | WAF Web ACL override |
| `certificate` | `CertificateCustomization` | No | ACM certificate override |
| `geoRestrictions` | `GeoRestrictionCustomization` | No | Geographic restriction override |

### ManagedCertificateRequest

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `validationTokenHost` | string | Yes | Validation method: `"cloudfront"` or `"self-hosted"` |
| `primaryDomainName` | string | No | Primary domain for the certificate |
| `certificateTransparencyLoggingPreference` | string | No | `"enabled"` or `"disabled"` |

## Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | AWS-assigned distribution tenant ID |
| `arn` | string | Amazon Resource Name |
| `eTag` | string | Version identifier for optimistic concurrency |
| `distributionTenantStatus` | string | AWS deployment status (`InProgress`, `Deployed`) |
| `observedGeneration` | int64 | Last generation successfully reconciled (used for drift detection) |
| `certificateArn` | string | ARN of the associated ACM certificate |
| `managedCertificateStatus` | string | Managed cert lifecycle status (`pending-validation`, `issued`, etc.) |
| `driftDetected` | bool | Whether external drift was detected |
| `lastDriftCheckTime` | timestamp | Timestamp of the last drift check |
| `domainResults` | array of `DomainResult` | Per-domain status from AWS |
| `conditions` | array of `Condition` | Standard Kubernetes conditions |

### DomainResult

| Field | Type | Description |
|-------|------|-------------|
| `domain` | string | Fully qualified domain name |
| `status` | string | AWS-reported domain status (`active` or `inactive`) |

## Naming Constraints

The Kubernetes resource name (`metadata.name`) is used as the CloudFront tenant name in AWS. It must satisfy both Kubernetes naming rules and CloudFront's constraints:

- 3-128 characters
- Start and end with a lowercase alphanumeric character
- Contain only lowercase alphanumerics, dots (`.`), and hyphens (`-`)
