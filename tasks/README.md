# Grove Project Status

> Canonical bird's-eye view of Grove work. Task files are the source of truth; this page is the human entry point.

## Current outcome

### Killer GroveShop Demo — in progress

The current outcome is defined in [outcomes/groveshop-demo.md](outcomes/groveshop-demo.md).

The original MVP implementation sequence (tasks 001–032) is complete. New demo-experience work now builds on that baseline, especially interactive cluster join/rollout, live cluster visualization, runtime-guided debugging, and the standalone load-test experience.

## NOW

Keep this list intentionally small: at most 3–5 active tasks across the whole project.

- Interactive same-binary node discovery and join experience
- Live shared cluster view in the application TUI
- Standalone GroveShop load tester with its own TUI
- Runtime-guided hot-path debugging experience

## NEXT

- New-binary rollout from the same application experience
- Preserve the same ingress endpoint throughout rollout
- Config changes use the same binary rollout path
- Visualize failure, recovery, and rollout progress consistently in every connected TUI

## LATER

Work that is useful but not required to prove the current outcome belongs here until promoted into NOW/NEXT.

## Status vocabulary

Tasks use exactly one of:

- `todo`
- `in-progress`
- `blocked`
- `done`

A blocked task must identify what blocks it.

## Task model

Every active task must connect upward:

```text
task -> workstream -> outcome
```

The workstream is represented by the task's directory. Status, outcome, and dependencies are represented in YAML front matter.

Example:

```yaml
---
id: TUI-001
status: in-progress
outcome: groveshop-demo
depends-on:
  - CLUSTER-003
---
```

See [TASK_MANAGEMENT.md](TASK_MANAGEMENT.md) for the complete convention.

## Legacy MVP tasks

The flat numbered files `001-...` through `032-...` are the completed MVP implementation history. Keep them in place so existing documentation links remain stable. New work uses workstream directories and structured metadata.

## Workstreams

New task specs live under:

- `runtime/`
- `cluster/`
- `networking/`
- `sdk/`
- `tui/`
- `debugging/`
- `rollout/`
- `resilience/`
- `security/`
- `demo/`
- `testing/`
- `docs/`

This README should eventually be generated from task metadata; do not turn it into a second source of truth.
