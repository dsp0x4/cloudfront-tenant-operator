# Managed Certificates

When a `managedCertificateRequest` is configured in the spec, CloudFront requests and manages an ACM certificate automatically. The operator tracks the certificate through its lifecycle and attaches it to the tenant when it is issued.

## Lifecycle

```
                    ┌──────────────────┐
                    │  Create Tenant   │
                    │  with MCR in     │
                    │  spec            │
                    └────────┬─────────┘
                             │
                             ▼
                    ┌──────────────────┐
                    │  pending-        │  CertificateReady=False
                    │  validation      │  reason=PendingValidation
                    │  (poll every     │
                    │  30s)            │
                    └────────┬─────────┘
                             │ DNS validated
                             ▼
                    ┌──────────────────┐
                    │  issued          │  CertificateReady=False
                    │  (auto-attach    │  reason=Attaching
                    │  triggered)      │
                    └────────┬─────────┘
                             │ ARN persisted to spec
                             │ Update pushed to AWS
                             ▼
                    ┌──────────────────┐
                    │  attached        │  CertificateReady=True
                    │  (all domains    │  reason=Validated
                    │  active)         │
                    └──────────────────┘
```

## Auto-Attachment

When the operator detects that a managed certificate has been issued (`status: issued`) but not all domains are active, it:

1. Persists the certificate ARN to `spec.customizations.certificate.arn`.
2. Returns immediately (one K8s API write per reconcile).
3. The spec change bumps the generation and triggers a new reconcile.
4. The three-way diff detects a spec change and pushes the update to AWS via the normal update path.

This two-step approach avoids `resourceVersion` conflicts and reuses the existing update logic (including ETag handling and validation).

!!! note
    The auto-attach runs even when the AWS tenant status is `InProgress`. The certificate can be issued while a previous deployment is still propagating, and the operator persists the ARN immediately rather than waiting.

## Validation Token Host

The `validationTokenHost` field controls how CloudFront validates domain ownership:

| Value | Requirement |
|-------|-------------|
| `cloudfront` | The domain must already have a DNS CNAME pointing to CloudFront. Use this when the domain is already serving traffic through CloudFront. |
| `self-hosted` | You manage the validation token yourself. Use this for domains not yet pointed to CloudFront. |

## Failure Handling

If the certificate validation fails or times out, the operator sets `CertificateReady=False` with reason `CertificateFailed` and includes the failure status in the condition message (e.g., `validation-timed-out`, `revoked`, `failed`).

The operator continues to poll in steady state (every 5 minutes), so if the underlying issue is resolved, the status will update on the next reconcile.
