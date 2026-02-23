# CloudFront Tenant Operator

A Kubernetes operator for managing [CloudFront Distribution Tenants](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/distribution-tenants.html) -- the AWS CloudFront multi-tenant content delivery feature.

!!! warning "Work in Progress"
    This project is under active development. APIs, CRD schemas, and controller behavior may change without notice. It is not yet recommended for production use.

## Overview

The operator manages `DistributionTenant` custom resources that map 1:1 to AWS CloudFront distribution tenants. It handles:

- **Full lifecycle management** -- Create, update, and delete distribution tenants via the AWS API
- **Disable-before-delete** -- Automatically disables tenants before deletion (required by AWS)
- **ETag-based concurrency** -- Uses optimistic concurrency control on every update to prevent conflicts
- **Drift detection** -- Three-way diff (spec vs observed generation vs AWS state) distinguishes user-initiated changes from external drift, with configurable policy
- **Managed certificate lifecycle** -- Tracks CloudFront-managed ACM certificates through validation, issuance, and automatic attachment
- **Pre-flight validation** -- Validates the resource name, certificate coverage, and required parameters against the parent distribution before calling AWS
- **Error classification** -- Distinguishes terminal errors (domain conflicts, permission issues) from retryable ones (throttling, network errors) with detailed AWS error messages
- **DNS record management** -- Automatically creates and cleans up Route53 CNAME records, with cross-account support via STS
- **Status conditions** -- Reports `Ready`, `Synced`, `CertificateReady`, and `DNSReady` conditions following Kubernetes conventions
- **Prometheus metrics** -- Exposes reconciliation duration, error counts, drift detections, and AWS API call latency
- **Finalizer-based cleanup** -- Ensures AWS resources are properly disabled and deleted before the K8s object is removed

## Roadmap

Planned features:

1. **TenantSource controller** -- Automatically discover and reconcile `DistributionTenant` resources from an external database (PostgreSQL or DynamoDB). The CRD is already defined (`TenantSource`); controller logic is not yet implemented. This enables SaaS platforms to manage tenants from their existing data store without manually creating Kubernetes resources.
2. **Webhook validation** -- Admission webhooks for deeper validation at create/update time (e.g., cross-field consistency, DNS reachability checks).
3. **Multi-provider DNS** -- Support for DNS providers beyond Route53 (e.g., Cloudflare, Azure DNS).

## Quick Links

- [Installation](getting-started/installation.md) -- Set up the operator in your cluster
- [Quickstart](getting-started/quickstart.md) -- Create your first distribution tenant
- [CRD Reference](reference/crd.md) -- Full spec and status field documentation
- [Architecture](architecture/overview.md) -- How the reconciliation loop works
