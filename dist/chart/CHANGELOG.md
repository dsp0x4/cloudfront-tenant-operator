# Changelog

## 0.3.0

### Improvements

- **TenantSource backend abstraction** -- Extracted a provider-neutral `Backend` interface so additional external sources (PostgreSQL, MongoDB/DocumentDB, Redis) can be added as drop-in packages without controller changes. Behavior-preserving refactor: no CRD or API changes; DynamoDB remains the only registered backend.
- Upgraded Go version.
- Upgraded Go dependencies.

## 0.2.1

### Improvements

- Upgraded Go version.
- Upgraded Go dependencies.

## 0.2.0

### Features

- **TenantSource controller (DynamoDB)** -- The `TenantSource` CRD is now fully functional. The operator polls a DynamoDB table and automatically creates, updates, and deletes `DistributionTenant` resources to match the external state. Supports template-based defaults with per-item DynamoDB overrides for all fields including DNS, parameters, tags, WebACL, geo restrictions, and managed certificates. Requires `dynamodb:Scan` IAM permission on the target table (see [IAM Permissions](https://cloudfront-tenant-operator.io/getting-started/installation/#tenantsource-dynamodb-permissions-optional)).
- **Tenant template** -- A new `spec.template` field on `TenantSource` defines baseline values applied to every discovered tenant. DynamoDB items can override individual fields via configurable attribute mappings. Precedence: DynamoDB item > template > K8s default.
- **Optimistic tenant deletion** -- After disabling a tenant, the controller immediately attempts deletion instead of polling for the `Deployed` status. If AWS reports the tenant is not yet disabled, the controller requeues with a 30-second backoff, significantly reducing deletion time in practice.

### CRD Changes

- **`TenantSource` spec**: Added `spec.template` (`TenantTemplate`) with fields for `enabled`, `connectionGroupId`, `parameters`, `customizations`, `managedCertificateRequest`, `tags`, and `dns` -- defines baseline values for all tenants created by this source.
- **`TenantSource` DynamoDB config** (breaking): Removed `domainAttribute` (default `"domain"`), replaced by `domainsAttribute` (default `"domains"`) with support for String (S) and StringSet (SS). Added 16 new attribute mappings: `enabledAttribute`, `connectionGroupIdAttribute`, `certificateArnAttribute`, `validationTokenHostAttribute`, `primaryDomainNameAttribute`, `certificateTransparencyLoggingPreferenceAttribute`, `dnsProviderAttribute`, `hostedZoneIdAttribute`, `dnsTTLAttribute`, `dnsAssumeRoleArnAttribute`, `webAclActionAttribute`, `webAclArnAttribute`, `geoRestrictionTypeAttribute`, `geoLocationsAttribute`, `parametersAttribute`, `tagsAttribute`.
- **`TenantSource` status**: Added `tenantsDeleted` field. Changed `tenantsDiscovered`, `tenantsCreated`, and `tenantsUpdated` from `omitempty` to always serialized (zero value included in JSON).
- **`DistributionTenant` spec**: `managedCertificateRequest.primaryDomainName` changed from required to optional (defaults to the first domain in `spec.domains`).

### Improvements

- Upgraded Go dependencies.
- Skip generation increment on status-only updates to reduce unnecessary reconciliation cycles.

## 0.1.0

### Improvements

- Upgraded Go version.

## 0.1.0

Initial release.
