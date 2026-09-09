# Plan: Diagnose abrupt hosting-node loss

## Goal

Complete Task 017 with permanent real-process coverage for abrupt loss of the
Grovlet hosting Grove Shop Inventory. Prove both survivors detect the node as
unavailable, retain authoritative Inventory placement pointing at the failed
node for diagnosis, and expose call failure without recovering the service.

## Context

Tasks 001 through 016 are complete. Health is an ephemeral per-observer view,
placement is authoritative replicated KV state, and hosted services run in
Grovlet-supervised workers. Task 017 composes those existing capabilities in a
failure E2E only. Task 018 owns placement mutation and recovery.

### Key Files

- `tasks/017-node-failure-detection-e2e.md` — abrupt-loss acceptance contract.
- `tasks/018-service-recovery-after-node-failure.md` — hard recovery boundary.
- `cmd/grovlet/main_test.go` — real multi-Grovlet health, placement, component,
  and application-flow tests plus bounded wait helpers.

### Decisions Made

- Reuse the canonical Orders-on-node-1 and Inventory-on-node-2 topology with
  node 3 as a second survivor/observer.
- Establish a successful order before failure, SIGKILL node 2, then require
  both survivors to report node 2 unavailable.
- Treat the unchanged Inventory placement record targeting node 2 as the
  structured diagnostic link between failed node and affected service.
- Verify a new order fails after the kill. Do not move placement, start a
  replacement worker, retry, or add any recovery policy.

## Sub-Tasks

- [x] 1. Select and bound Task 017.
  **Context:** Read Tasks 017 and 018 and inspect existing health, placement,
  worker lifecycle, application flow, and diagnostic helpers.
  **Outcome:** Chose a test-only composition of existing production behavior
  with two survivor observations and an explicit no-recovery assertion.

- [x] 2. Add the abrupt node-loss E2E.
  **Context:** Start three real placed Grovlets, prove an initial order, kill
  Inventory's Grovlet, condition-wait on both survivor health views, verify the
  retained placement identifies Inventory as affected, and prove calls fail.
  **Outcome:** Added a real three-process scenario proving the initial flow,
  SIGKILLing Inventory's node, waiting on both survivors, correlating retained
  placement to the unavailable node, and receiving a transport-classified call
  failure without replacement.

- [x] 3. Verify and close Task 017.
  **Context:** Repeat the failure E2E, run formatting, vet, race, and full suites,
  then mark Task 017 DONE.
  **Outcome:** Three repeated failure scenarios, formatting, diff checks, vet,
  the complete race suite, and `go test -count=1 ./...` pass. Task 017 is DONE.

## Log

- 2026-09-09: Tasks 001 through 016 completed with focused, repeated, race,
  vet, and full-suite checks passing.
- 2026-09-09: Selected Task 017 as permanent abrupt-loss regression coverage;
  Task 018 remains the first recovery increment.
- 2026-09-09: Completed Task 017 with repeated abrupt-loss coverage and all
  historical race/full tests passing; no recovery behavior was introduced.
