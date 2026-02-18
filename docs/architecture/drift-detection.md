# Drift Detection

The operator uses a **three-way diff** to distinguish between user-initiated spec changes and external modifications to AWS resources.

## How It Works

The three-way diff compares three pieces of information:

1. **`metadata.generation`** -- Kubernetes increments this automatically whenever the spec changes.
2. **`status.observedGeneration`** -- The operator sets this to the current generation after every successful reconciliation.
3. **Spec vs AWS comparison** -- A field-by-field comparison of the CRD spec against the live AWS state.

The logic:

| Generation Match? | Spec Matches AWS? | Result |
|-------------------|-------------------|--------|
| `generation == observedGeneration` | Yes | **No change** -- steady state |
| `generation != observedGeneration` | No | **Spec change** -- user modified the CR |
| `generation != observedGeneration` | Yes | **No-op spec change** -- user changed and reverted, or changed a field the operator doesn't track |
| `generation == observedGeneration` | No | **Drift** -- something outside the operator modified AWS |

## Drift Policies

The `--drift-policy` flag controls how the operator responds when drift is detected:

### `enforce` (default)

The spec is the single source of truth. When drift is detected, the operator pushes the K8s spec to AWS, overwriting whatever was changed externally.

- Sets `Synced=False` with reason `DriftDetected` briefly during the update.
- After the update succeeds, sets `Synced=True`.

### `report`

The operator logs the drift and sets status conditions, but does **not** modify the AWS state.

- Sets `Synced=False` with reason `DriftDetected`.
- Emits a `DriftDetected` Kubernetes event.
- The drift persists until the user either updates the spec to match AWS or switches to `enforce`.

### `suspend`

The operator skips drift detection entirely. The AWS state is not compared to the spec.

- Sets `driftDetected: true` in the status as an acknowledgment.
- Does **not** degrade the `Synced` condition.
- Useful during planned maintenance windows when AWS resources are intentionally modified outside the operator.

## What Fields Are Compared

The spec-vs-AWS comparison covers:

- `enabled` state
- `distributionId`
- `connectionGroupId`
- Domain list (order-independent)
- Parameters (by name and value)
- Customizations: Web ACL (action + ARN), certificate ARN, geo restrictions (type + locations, order-independent)
