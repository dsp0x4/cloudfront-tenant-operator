# Contributing

## Development Setup

Clone the repository and ensure you have the [prerequisites](getting-started/installation.md#prerequisites) installed.

```sh
git clone https://github.com/paolo-desantis/cloudfront-tenant-operator.git
cd cloudfront-tenant-operator
```

## Common Commands

```sh
# Run unit tests (uses envtest: real K8s API server + etcd, mock AWS client)
make test

# Run linter
make lint

# Auto-fix lint issues
make lint-fix

# Regenerate CRD manifests and DeepCopy methods after editing *_types.go
make manifests generate

# Run the operator locally against your current kubeconfig
make run

# Build and push the container image
make docker-build docker-push IMG=<registry>/cloudfront-tenant-operator:tag
```

## Test Structure

Tests use **Ginkgo + Gomega** (BDD style):

- `internal/controller/distributiontenant_controller_test.go` -- Integration tests using envtest (real K8s API) with a mock AWS client.
- `internal/controller/change_detection_test.go` -- Unit tests for the three-way diff and drift policy logic.
- `internal/aws/errors_test.go` -- Unit tests for AWS error classification.
- `test/e2e/e2e_test.go` -- End-to-end tests designed to run against an isolated Kind cluster.

!!! warning
    The e2e tests require a dedicated [Kind](https://kind.sigs.k8s.io/) cluster. Do not run them against a real cluster.

## After Editing Types

If you modify any `*_types.go` file or kubebuilder markers, always run:

```sh
make manifests generate
```

This regenerates:

- `config/crd/bases/*.yaml` -- CRD manifests
- `config/rbac/role.yaml` -- RBAC rules from `+kubebuilder:rbac` markers
- `**/zz_generated.deepcopy.go` -- DeepCopy methods

Do **not** edit these generated files manually.

## Code Style

- Use `log := log.FromContext(ctx)` for structured logging.
- Keep reconciliation idempotent: safe to run multiple times.
- At most one K8s API write per reconcile loop (see [ResourceVersion Safety](architecture/overview.md#resourceversion-safety)).
- Use owner references for automatic garbage collection.
- Wrap errors with context: `fmt.Errorf("failed to X: %w", err)`.
