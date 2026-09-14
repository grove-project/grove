# Rollouts

Status: Design / evolving

## Principle

Grove configuration is immutable at runtime. The application console can inspect configuration and can initiate, observe, diagnose, or roll back a deployment, but it does not edit the configuration of a running node.

A configuration change therefore means rolling out another application artifact.

## Basic workflow

Human workflow from the application binary:

```bash
./groveshop
```

```text
Deployments
  ├─ Current artifact
  ├─ New rollout
  ├─ Rollout history
  └─ Configuration
```

Starting a rollout is contextual:

```text
Deployments > New rollout > ./groveshop-next
```

Automation uses structured actions exposed by the same application binary:

```bash
./groveshop action config.inspect ./groveshop-next
./groveshop action rollout.start ./groveshop-next
./groveshop action rollout.status
./groveshop action rollout.inspect <rollout-id>
```

A rollback is also an artifact transition, not configuration mutation:

```bash
./groveshop action rollout.rollback <rollout-id>
```

Exact action names are evolving; the behavioral contract is the important part.

## Binary handoff

The existing Grove mesh distributes and bootstraps the successor artifact. On each target node the current Grovlet transfers/obtains and verifies the artifact, starts the successor beside itself, waits for it to join and become healthy, hands responsibility over, and exits only after successful handoff.

The old process remains authoritative while the candidate is proving itself. Candidate failure leaves the old process running.

## Rollout phases

The TUI should expose a stable state machine such as:

```text
pending -> transferring -> verifying -> starting -> joining -> handoff -> healthy
                                                           \-> failed
```

The Deployments view should provide a concise cluster-wide view:

```text
Deployments > 2026-09-09-001

Artifact: sha256:...
Target class: cloud
Status: progressing

NODE       OLD        NEW        STATE
cloud-01   v1/r42     v2/r43     healthy
cloud-02   v1/r42     v2/r43     handoff
cloud-03   v1/r42     -          pending

Progress: 1/3 complete, 1 handoff, 1 pending, 0 failed
```

For a mixed cluster, configured variants may be rolled independently:

```text
ARTIFACT          TARGET   READY   HANDOFF   FAILED
grove-v2-cloud    cloud    4/5     1         0
grove-v2-edge     edge     8/8     0         0
```

## Diagnostics

The rollout detail view should explain why progress stopped rather than merely expose a failed state. Examples include:

- artifact integrity/signature failure,
- cluster identity mismatch,
- node-class/zone mismatch,
- candidate startup failure,
- candidate failed to join the mesh,
- placement validation failure,
- application health/E2E validation failure,
- handoff timeout.

The operator should see state, change, cause, action, and result.

## Configuration inspection

Configuration inspection is read-only and should be reachable from the artifact/rollout context in the TUI.

For automation:

```bash
./groveshop action config.inspect ./groveshop-edge-v2
./groveshop action node.inspect edge-07
```

Useful output includes code digest, config revision/digest, final artifact digest, cluster identity, node class/zone, embedded config format, and integrity status.

There is intentionally no normal live `node.set-zone` or `config.set` operation. To change an immutable value, create another configured artifact and roll it out.

## Compatibility guard

A normal handoff preserves the node's configured class:

```text
cloud-v1 -> cloud-v2
edge-v1  -> edge-v2
```

A candidate whose embedded cluster identity or node class is incompatible with the target must be rejected before ownership handoff. Reclassifying a machine is explicit reprovisioning, not an accidental rollout side effect.

## Control state

Rollout state is durable Grove control-plane state. Any instance of the same application console connected to a healthy cluster node should observe the same rollout ID, artifact identities, target set, per-node phase, health, and failure reasons.
