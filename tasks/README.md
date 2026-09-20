# Grove Project Status

> Canonical bird's-eye view. Task specs are the source of truth.

## Current outcome

### Killer GroveShop Demo — in progress

See [outcomes/groveshop-demo.md](outcomes/groveshop-demo.md).

The original MVP sequence (tasks 001–032) is complete. The current outcome is represented by the structured tasks below.

## NOW

Keep active work intentionally small: at most 3–5 tasks.

| Task | Workstream | Status | Depends on |
| --- | --- | --- | --- |
| [CLUSTER-001](cluster/CLUSTER-001-interactive-discovery-and-join.md) Interactive discovery and join | cluster | todo | — |
| [DEBUG-001](debugging/DEBUG-001-runtime-flow-capture.md) Runtime Grove call-flow capture | debugging | todo | — |
| [DEMO-001](demo/DEMO-001-standalone-load-generator.md) Standalone load generator | demo | todo | NET-001 |

DEMO-001 is part of the immediate demo focus but is dependency-gated until stable ingress is available.

## NEXT

| Task | Workstream | Status | Depends on |
| --- | --- | --- | --- |
| [TUI-001](tui/TUI-001-shared-cluster-view.md) Shared live Cluster view | tui | todo | CLUSTER-001 |
| [ROLLOUT-001](rollout/ROLLOUT-001-startup-candidate-detection.md) Startup candidate detection | rollout | todo | CLUSTER-001 |
| [DEBUG-002](debugging/DEBUG-002-runtime-guided-debug-tui.md) Runtime-guided Debug TUI | debugging | todo | DEBUG-001 |
| [NET-001](networking/NET-001-stable-ingress-rollout.md) Stable ingress through rollout | networking | todo | ROLLOUT-001 |

## LATER / dependency-gated

| Task | Workstream | Status | Depends on |
| --- | --- | --- | --- |
| [ROLLOUT-002](rollout/ROLLOUT-002-config-as-artifact-rollout.md) Config as artifact rollout | rollout | todo | ROLLOUT-001, NET-001 |
| [DEBUG-003](debugging/DEBUG-003-native-paused-debugger.md) Native paused debugger | debugging | todo | DEBUG-002 |
| [TEST-001](testing/TEST-001-killer-demo-e2e.md) Killer demo E2E | testing | todo | TUI-001, ROLLOUT-002, DEBUG-003, DEMO-001 |

## Dependency shape

```text
CLUSTER-001 ──┬─> TUI-001
              └─> ROLLOUT-001 ─> NET-001 ─┬─> ROLLOUT-002
                                          └─> DEMO-001

DEBUG-001 ─> DEBUG-002 ─> DEBUG-003

TUI-001 ───────────────┐
ROLLOUT-002 ───────────┤
DEBUG-003 ─────────────┼─> TEST-001
DEMO-001 ──────────────┘
```

## Status vocabulary

`todo -> in-progress -> blocked -> done`

A blocked task must identify its blocker. See [TASK_MANAGEMENT.md](TASK_MANAGEMENT.md).

## Legacy MVP tasks

The flat numbered files `001-...` through `032-...` are completed MVP implementation history and remain at their existing paths so documentation links stay stable.

## Workstreams

New work lives under `runtime/`, `cluster/`, `networking/`, `sdk/`, `tui/`, `debugging/`, `rollout/`, `resilience/`, `security/`, `demo/`, `testing/`, and `docs/`.

This dashboard should eventually be generated from task metadata rather than manually maintained.
