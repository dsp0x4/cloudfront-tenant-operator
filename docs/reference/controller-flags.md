# Controller Flags

The controller manager accepts the following command-line flags.

## Operator-Specific Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--drift-policy` | `enforce` | How to handle external drift on AWS resources. See [Drift Detection](../architecture/drift-detection.md) for details. |
| `--aws-region` | *(SDK default)* | AWS region for CloudFront API calls. If not set, uses AWS SDK default resolution (environment variables, config file, IMDS). |

### Drift Policy Values

| Value | Behavior |
|-------|----------|
| `enforce` | Overwrite the AWS state with the K8s spec. The spec is treated as the single source of truth. |
| `report` | Log the drift and set status conditions, but do not modify the AWS state. |
| `suspend` | Skip drift detection entirely. Useful during planned maintenance windows when AWS resources are modified manually. |

## Infrastructure Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--metrics-bind-address` | `0` | Address for the metrics endpoint. Use `:8443` for HTTPS or `:8080` for HTTP. `0` disables metrics. |
| `--health-probe-bind-address` | `:8081` | Address for health and readiness probes. |
| `--leader-elect` | `false` | Enable leader election for controller manager. Required for HA deployments with multiple replicas. |
| `--metrics-secure` | `true` | Serve the metrics endpoint over HTTPS. Set to `false` for HTTP. |
| `--enable-http2` | `false` | Enable HTTP/2 for metrics and webhook servers. |
