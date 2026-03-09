# CRD Specification

Full reference for the `DistributionTenant` and `TenantSource` custom resources.

See the [Go type definitions](https://github.com/dsp0x4/cloudfront-tenant-operator/blob/main/api/v1alpha1/distributiontenant_types.go) and [example CRs](https://github.com/dsp0x4/cloudfront-tenant-operator/tree/main/config/samples/) for additional detail.

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
| `dns` | `DNSConfig` | No | DNS record management config (see below) |

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

> **Note:** ACM certificates used with CloudFront must be created in the **us-east-1** region. This is an AWS requirement.

### ManagedCertificateRequest

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `validationTokenHost` | string | Yes | Validation method: `"cloudfront"` or `"self-hosted"` |
| `primaryDomainName` | string | No | Primary domain for the certificate (defaults to the first domain in `spec.domains`) |
| `certificateTransparencyLoggingPreference` | string | No | `"enabled"` or `"disabled"` |

### DNSConfig

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `provider` | string | Yes | DNS provider (`"route53"`) |
| `hostedZoneId` | string | No | Route53 hosted zone ID where records will be managed |
| `ttl` | int64 | No | TTL for CNAME records in seconds (60-172800, default: `300`) |
| `assumeRoleArn` | string | No | IAM role ARN to assume for Route53 calls (cross-account DNS) |

## Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | AWS-assigned distribution tenant ID |
| `arn` | string | Amazon Resource Name |
| `eTag` | string | Version identifier for optimistic concurrency |
| `distributionTenantStatus` | string | AWS deployment status (`InProgress`, `Deployed`) |
| `observedGeneration` | int64 | Last generation successfully reconciled (used for drift detection) |
| `createdTime` | timestamp | When the distribution tenant was created in AWS |
| `lastModifiedTime` | timestamp | When the distribution tenant was last modified in AWS |
| `certificateArn` | string | ARN of the associated ACM certificate |
| `managedCertificateStatus` | string | Managed cert lifecycle status (see values below) |
| `driftDetected` | bool | Whether external drift was detected |
| `lastDriftCheckTime` | timestamp | Timestamp of the last drift check |
| `dnsChangeId` | string | Route53 change ID for a pending DNS record change |
| `dnsTarget` | string | CNAME target (CloudFront endpoint) used for DNS records |
| `domainResults` | array of `DomainResult` | Per-domain status from AWS |
| `conditions` | array of `Condition` | Standard Kubernetes conditions |

### managedCertificateStatus Values

| Value | Meaning |
|-------|---------|
| `pending-validation` | Certificate is awaiting DNS validation |
| `issued` | Certificate is validated and issued |
| `inactive` | Certificate is inactive |
| `expired` | Certificate has expired |
| `validation-timed-out` | DNS validation timed out |
| `revoked` | Certificate was revoked |
| `failed` | Certificate issuance failed |

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

---

## TenantSource

A `TenantSource` points to an external data source (currently DynamoDB) and automatically creates, updates, and deletes `DistributionTenant` CRs to match the external state.

See the [Go type definitions](https://github.com/dsp0x4/cloudfront-tenant-operator/blob/main/api/v1alpha1/tenantsource_types.go) for additional detail.

### Spec Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `provider` | string | Yes | External source type (`"dynamodb"`) |
| `pollInterval` | duration | No | How often to poll the source (default: `5m`) |
| `distributionId` | string | Yes | Default parent multi-tenant distribution ID for discovered tenants |
| `targetNamespace` | string | No | Namespace for created DistributionTenants (default: TenantSource's namespace) |
| `template` | `TenantTemplate` | No | Baseline values applied to every tenant (see below) |
| `dryRun` | bool | No | When `true`, calculates changes without mutating CRs (default: `false`) |
| `dynamodb` | `DynamoDBSourceConfig` | Conditional | DynamoDB source config (required when `provider` is `"dynamodb"`) |
| `postgres` | `PostgresSourceConfig` | Conditional | PostgreSQL source config (not yet implemented) |

### TenantTemplate

Defines default values applied to every `DistributionTenant` created by the source. All fields are optional. DynamoDB items can override individual fields when the corresponding attribute mapping is configured.

Precedence: **DynamoDB item value > template value > K8s default**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `enabled` | bool | No | Whether tenants serve traffic (default: `true`) |
| `connectionGroupId` | string | No | Default connection group for all tenants |
| `parameters` | array of `Parameter` | No | Default key-value parameters |
| `customizations` | `Customizations` | No | Default WAF, certificate, and geo restriction settings |
| `managedCertificateRequest` | `ManagedCertificateRequest` | No | Managed certificate config (same type as DistributionTenant; `primaryDomainName` defaults to the tenant's first domain). If a DynamoDB item provides a `certificateArn`, it takes precedence. Individual fields can be overridden per tenant via DynamoDB attribute mappings. |
| `tags` | array of `Tag` | No | Default AWS resource tags |
| `dns` | `DNSConfig` | No | Default DNS record management config |

### DynamoDBSourceConfig

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `tableName` | string | Yes | DynamoDB table to scan |
| `region` | string | No | AWS region for the table (default: operator's default region) |
| `nameAttribute` | string | No | DynamoDB attribute for tenant name (default: `"name"`) |
| `domainsAttribute` | string | No | DynamoDB attribute for tenant domains — accepts String (S) for a single domain or StringSet (SS) for multiple domains (default: `"domains"`) |
| `enabledAttribute` | string | No | DynamoDB attribute (boolean) for `spec.enabled` |
| `connectionGroupIdAttribute` | string | No | DynamoDB attribute for `spec.connectionGroupId` |
| `certificateArnAttribute` | string | No | DynamoDB attribute for `spec.customizations.certificate.arn` — overrides template managed cert |
| `validationTokenHostAttribute` | string | No | DynamoDB attribute to override `template.managedCertificateRequest.validationTokenHost` |
| `primaryDomainNameAttribute` | string | No | DynamoDB attribute to override the managed certificate primary domain |
| `certificateTransparencyLoggingPreferenceAttribute` | string | No | DynamoDB attribute to override CT logging preference |
| `dnsProviderAttribute` | string | No | DynamoDB attribute to override `template.dns.provider` |
| `hostedZoneIdAttribute` | string | No | DynamoDB attribute to override `template.dns.hostedZoneId` |
| `dnsTTLAttribute` | string | No | DynamoDB attribute (number) to override `template.dns.ttl` |
| `dnsAssumeRoleArnAttribute` | string | No | DynamoDB attribute to override `template.dns.assumeRoleArn` |
| `webAclActionAttribute` | string | No | DynamoDB attribute to override `template.customizations.webAcl.action` |
| `webAclArnAttribute` | string | No | DynamoDB attribute to override `template.customizations.webAcl.arn` |
| `geoRestrictionTypeAttribute` | string | No | DynamoDB attribute to override `template.customizations.geoRestrictions.restrictionType` |
| `geoLocationsAttribute` | string | No | DynamoDB attribute (StringSet) to override `template.customizations.geoRestrictions.locations` |
| `parametersAttribute` | string | No | DynamoDB attribute (Map) to override `template.parameters` (replaces template entirely) |
| `tagsAttribute` | string | No | DynamoDB attribute (Map) to override `template.tags` (replaces template entirely) |

Each DynamoDB item produces one `DistributionTenant` CR. The `nameAttribute` value becomes the CR's `metadata.name`, and the `domainsAttribute` value becomes the `spec.domains` list.

### TenantSource Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `lastPollTime` | timestamp | When the last successful poll occurred |
| `tenantsDiscovered` | int | Number of items found in the last poll |
| `tenantsCreated` | int | Number of CRs created in the last poll cycle |
| `tenantsUpdated` | int | Number of CRs updated in the last poll cycle |
| `tenantsDeleted` | int | Number of CRs deleted in the last poll cycle |
| `pendingChanges` | array of `PendingChange` | Changes that would be made (only populated when `dryRun` is `true`) |
| `conditions` | array of `Condition` | Standard Kubernetes conditions |

### PendingChange

| Field | Type | Description |
|-------|------|-------------|
| `action` | string | `"create"`, `"update"`, or `"delete"` |
| `tenantName` | string | Name of the DistributionTenant that would be affected |
| `description` | string | Human-readable description of the change |

### Ownership and Labeling

Managed `DistributionTenant` CRs receive:

- An **owner reference** pointing to the `TenantSource`, enabling Kubernetes garbage collection and efficient listing.
- A **label** `cloudfront-tenant-operator.io/source: <tenantsource-name>` for easy `kubectl` filtering.

The controller never modifies `DistributionTenant` resources that it does not own. If a user-created tenant has the same name as a DynamoDB item, the controller reports a conflict in its status conditions and skips that item.
