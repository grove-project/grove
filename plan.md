# Plan: Expose the Grove Shop E2E through `grove test`

## Goal

Complete Task 029 with a real `grove test --binary <artifact>` command that
starts an isolated three-Grovlet cluster through the Go harness, waits for
production-shaped health and placement, executes the reference application
flow, cleans up all processes, and returns deterministic success or failure.
Do not inject failures, define SLAs, generate tests, or add a scenario matrix.

## Context

Earlier tasks prove the same Grove Shop flow from package tests. Task 029 makes
that experience a developer-facing CLI operation while retaining Go tests and
`grovetest` as the automation foundation. Task 030 will reuse the runner to add
one node-kill scenario.

### Key Files

- `tasks/029-grove-test-command-foundation.md` — current acceptance contract.
- `docs/developer-experience/testing.md` and `local-development.md` — test the
  shipped artifact through the real runtime model.
- `cmd/grove/main.go` — `test` command parsing and dispatch.
- `cmd/grove/test.go` — isolated topology, bounded readiness, functional flow,
  diagnostics, shutdown, and deterministic output.
- `cmd/grove/test_test.go` — parser, real command success, and failure exit
  coverage.
- `grovetest/grovetest.go` — existing real-process lifecycle primitive used by
  the command.

### Decisions Made

- Require `--binary` so the command tests the exact immutable artifact supplied
  by the developer rather than rebuilding an implicit source tree.
- Launch Orders, Inventory, and an observer as distinct Grovlet processes with
  isolated runtime directories and an explicit embedded System NATS route
  topology. Reuse `grovetest.StartNode`; do not add shell or Docker
  orchestration.
- Wait for node/component health and Orders/Inventory placement with bounded
  polling. On timeout, include every child process log and last observed state.
- Execute one deterministic order through the observer's placement-routed Grove
  client. A completed reservation proves the real Orders-to-Inventory path.
- Print the fixed success transcript only after graceful stop and cleanup
  succeed. Any setup, health, business-flow, or cleanup error returns non-zero
  through the existing CLI main function.

## Sub-Tasks

- [x] 1. Parse and dispatch `grove test`.
  **Context:** Add the command/required binary flag, reject extra or missing
  arguments, and preserve existing command behavior.

- [x] 2. Implement the isolated reference-flow runner.
  **Context:** Start real Grovlets, wait for health/placement, run the order
  flow, emit diagnostics on failure, and guarantee bounded child cleanup.

- [x] 3. Exercise the real command and exit contract.
  **Context:** Run the built CLI with the built Grovlet artifact, assert exact
  success output, and prove an unusable artifact returns non-zero.

- [ ] 4. Verify and close Task 029.
  **Context:** Run formatting, diff checks, focused repetitions, vet, full
  uncached tests, and full race tests; mark DONE only after all prior E2Es pass,
  then rebase and push directly to `main`.

## Log

- 2026-09-12: Task 028 passed focused, full uncached, and race gates and was
  pushed to `main` at `dab3dcf`.
- 2026-09-12: Kept node failure injection and post-recovery reruns out of this
  command slice; Task 030 owns the single resilience action.
- 2026-09-12: The command launches three isolated Grovlets through `grovetest`,
  observes cluster/component/placement readiness without fixed sleeps, and
  completes the cross-process Grove Shop order flow before cleanup.
- 2026-09-12: The built `grove test` command passed three real-process
  repetitions and focused race coverage; a missing artifact returned non-zero
  with a deterministic diagnostic.
