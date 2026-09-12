# Plan: Rerun `grove test` after one controlled node loss

## Goal

Complete Task 030 by extending `grove test` with one bounded resilience mode:
run the normal Grove Shop flow, kill the node currently hosting a selected
stateless service, wait for production recovery to move that service, and run
the same functional flow again. Do not add a scenario matrix, SLA enforcement,
or any other fault type.

## Context

Task 029 launches the exact artifact in an isolated three-Grovlet cluster and
executes a cross-process order flow. Existing Grovlet recovery already detects
node loss and compare-and-set moves affected placement. Task 030 activates that
production path from the command and observes it through the same System NATS
control APIs.

### Key Files

- `tasks/030-resilience-scenario-execution.md` — current acceptance contract.
- `docs/developer-experience/testing.md` — reuse the developer's functional
  flow instead of authoring a separate chaos suite.
- `cmd/grove/main.go` — `--resilience` and selected service parsing.
- `cmd/grove/test.go` — baseline/recovery flow reuse, node kill, survivor
  reconnect, bounded recovery observation, diagnostics, and output.
- `cmd/grove/test_test.go` — real command resilience proof.
- `cmd/grovlet/recovery.go` — existing production recovery behavior invoked by
  the test cluster.

### Decisions Made

- Add `--resilience` and optional `--service-id`; default to Grove Shop
  Inventory. Baseline `grove test` output and behavior remain unchanged.
- Enable `--system-nats-recovery` on the isolated Grovlets only for resilience
  mode. Determine the target process from authoritative placement rather than
  assuming the initial topology.
- Kill the complete hosting Grovlet through `grovetest.Node.Kill`, then reconnect
  to System NATS through a surviving process so the command works even when the
  selected service was on the original client node.
- Wait until the failed node is unavailable, selected service placement names a
  healthy survivor, and the replacement component reports healthy. Do not use
  fixed sleeps.
- Invoke the same order helper before and after failure with distinct order IDs.
  Print the failed and recovered node IDs so the deterministic transcript shows
  the action and result.

## Sub-Tasks

- [ ] 1. Parse the bounded resilience mode.
  **Context:** Add the flag/default service, validate the service ID, and keep
  baseline invocation compatibility.

- [ ] 2. Execute node loss and observe production recovery.
  **Context:** Enable recovery, resolve and kill the hosting node, reconnect via
  a survivor, wait for health/placement/component convergence, rerun the flow,
  and clean up the already-dead child safely.

- [ ] 3. Prove the real resilience command.
  **Context:** Assert exact baseline, injection, recovery, rerun, and PASS output
  from the built CLI with real Grovlet processes.

- [ ] 4. Verify and close Task 030.
  **Context:** Run formatting, diff checks, focused repetitions, vet, full
  uncached tests, and full race tests; mark DONE only after all prior E2Es pass,
  then rebase and push directly to `main`.

## Log

- 2026-09-12: Task 029 passed focused, full uncached, and race gates and was
  pushed to `main` at `9272bab`.
- 2026-09-12: Kept the action to one hosting-node kill. Network, disk, CPU,
  latency, scenario generation, and SLA evaluation remain out of scope.
