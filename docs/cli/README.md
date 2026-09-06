# Grove CLI

**One interface for developing the application and understanding the running system.**

The CLI is the bridge between Grove's developer and operator experiences. Commands should answer a human question first; implementation details come second.

> The examples below describe the intended CLI experience. Commands are being implemented incrementally.

## Run locally

```bash
$ grove run .

Building shop...
Starting Grove cluster...

✓ api
✓ orders
✓ inventory

Cluster ready at http://localhost:8080
```

Local development should preserve the runtime semantics that matter in production without requiring Kubernetes or a hand-built infrastructure stack.

## See what is running

```bash
$ grove status

Cluster     healthy
Version     v0.8.2
Nodes       3 / 3 healthy
Services    3 / 3 healthy
Config      production-42
```

A healthy system should be boring. When it is not healthy, the same command should immediately point toward the reason:

```bash
$ grove status

Cluster     degraded
Services    2 / 3 healthy

orders      degraded
  node-2 restarted 3 times in 42s

Last change
  config production-43 deployed 48s ago

Suggested
  grove inspect orders
```

## Drill into a service

```bash
$ grove inspect orders

orders

Instances
  node-1    healthy
  node-2    crash loop
  node-3    healthy

Cause
  configuration production-43
  max_connections: -1

Current action
  restoring production-42
```

The goal is not to replace raw logs, metrics, or traces. It is to make the runtime's own state and decisions understandable before the operator has to correlate those lower-level signals.

## Test failures deliberately

```bash
$ grove test --resilience

Running application E2E suite...
✓ baseline

Injecting node failure...
✓ failure detected
✓ orders recovered on node-3
✓ E2E suite remained healthy

PASS
```

Resilience belongs in the normal development loop rather than a separate production-only discipline. See [Testing with Grove](../sdk/testing.md).

## Debug where the code actually runs

```bash
$ grove debug orders

orders is running on node-2
DAP endpoint ready at localhost:4711
Waiting for IDE...
```

Grove resolves placement; the developer debugs the application. See the [debugging guide](debugging.md).

## Change configuration safely

```bash
$ grove config embed production.yaml ./shop
Embedded configuration production-43

$ grove deploy ./shop
Deploying shop v0.8.3

✓ node-1 updated
✗ orders failed health check

Rollout stopped
Restoring production-42...
✓ cluster healthy
```

The important output is not that an operation failed. It is **what changed, what Grove observed, what Grove did, and whether the system recovered**.

See [Deployments and Recovery](../operations/deployments.md) for the operational model.

## CLI design rule

Every operational command should help answer one of these questions:

- What is running?
- What changed?
- What is unhealthy?
- Why did it happen?
- What is Grove doing about it?
- Did recovery succeed?

Raw runtime detail should remain available for deeper investigation, but it should not be the first thing a human must decode.