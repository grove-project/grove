# Grove Agent Instructions

Read `AGENTS.md`, `IMPLEMENTATION_PLAN.md`, and the selected `tasks/NNN-*.md` before implementation.

## One task at a time
Implement exactly one task. Do not implement future functionality unless explicitly required. Prefer the smallest implementation satisfying the current task. If a task requires an unplanned architectural change, stop and report it rather than silently redesigning Grove.

## Testing is architecture
Every capability must be fully testable through `go test`. Distributed E2E tests must use the Grove Go testing harness, spawn real Grovlet OS processes, run multiple Grovlets on the same host, use production-shaped inter-process transport, isolate state/ports, clean up all children, and emit useful diagnostics.

No manual setup, Docker, shell-script orchestration, or human verification may be required for acceptance.

## No same-host shortcuts
A Grovlet is a logical node, not a host. Multi-process tests may share a machine, but Grovlets must communicate as if they were on different hosts. Node identity must be independent of host identity.

## Synchronization
Do not use fixed sleeps for distributed state transitions. Use bounded condition waits such as `WaitReady`, `WaitForClusterSize`, `WaitForNodeState`, `WaitForPlacement`, `WaitForServiceHealthy`, and `Eventually`. Timeouts must dump diagnostics.

## Completion rule
A task is complete only when its unit tests, integration tests, new E2E, all earlier E2Es, and `go test ./...` pass. Never weaken/delete an existing test to make a task pass.

Treat each task's `Out of scope` section as a hard constraint.

When done, report changes, tests added, commands/results, deviations, and architectural issues. Do not proceed automatically to the next task.