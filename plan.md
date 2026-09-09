# Plan: Recover services after hosting-node failure

## Goal

Complete Task 018 by detecting when a placed Grove Shop service loses its
hosting Grovlet, deterministically starting the service on a healthy survivor,
and replacing the authoritative JetStream/KV placement so Orders resumes its
normal Grove call path.

## Context

Tasks 001 through 017 are complete. Membership and placement are replicated KV
views, health is observer-derived from System NATS heartbeats, and Grovlets can
start and stop supervised service workers through System NATS commands. Task
018 composes those capabilities into stateless recovery; Task 019 owns general
desired deployment state and reconciliation.

### Key Files

- `tasks/018-service-recovery-after-node-failure.md` — recovery acceptance
  contract and scope boundary.
- `internal/systemnats/placement.go` — replicated placement records and the new
  compare-and-swap ownership change.
- `internal/systemnats/component.go` — component capabilities exposed to a
  recovery coordinator.
- `cmd/grovlet/component.go` — local worker catalog and lifecycle control.
- `cmd/grovlet/recovery.go` — deterministic health-to-placement recovery loop.
- `cmd/grovlet/main_test.go` — real multi-process recovery scenario.

### Decisions Made

- Enable recovery explicitly for Task 018 clusters. This preserves Task 017's
  permanent diagnosis-without-recovery behavior and avoids introducing Task
  019's general desired-state semantics early.
- Every recovery-enabled Grovlet knows the two currently executable Grove Shop
  component specifications, but starts only components explicitly assigned at
  boot. Component views expose each component's node-local invocation subject.
- Derive coordination and replacement from the sorted healthy-node view: the
  first healthy node acts, and the first healthy node is the replacement. This
  is deterministic and adds no separate leader election or scheduling system.
- Start the replacement worker, then compare-and-swap the old placement record
  in JetStream/KV. If the placement changed concurrently, stop the unplaced
  worker and accept the authoritative winner.
- Recover only placements whose current node is unavailable. Do not add desired
  deployments, component-crash reconciliation, persistence, retries hidden in
  the SDK, or advanced placement policy.

## Sub-Tasks

- [x] 1. Add authoritative placement replacement.
  **Context:** Add a validated JetStream/KV compare-and-swap operation and prove
  all placement watchers observe the new record while stale writers cannot
  overwrite it.
  **Outcome:** Added a validated revision-based placement replacement that
  returns the authoritative winner on conflict. Three replicated observers see
  the update, and a stale replacement is rejected without overwriting it.

- [x] 2. Expose dormant recovery capabilities.
  **Context:** Include invocation subjects in component views, let a manager
  start selected catalog entries, and configure recovery-enabled Grovlets with
  the Grove Shop Orders and Inventory worker catalog while preserving explicit
  initial placement.
  **Outcome:** Recovery-enabled Grovlets advertise node-local Orders and
  Inventory subjects while starting only their explicit boot placements.

- [x] 3. Reconcile unavailable placements.
  **Context:** Add a bounded System NATS recovery loop that waits for ready
  health and placement views, selects one deterministic coordinator and target,
  starts the target component, and commits the placement change through KV.
  **Outcome:** Added the smallest-healthy-node coordinator, prior-health guard,
  local replacement start, KV compare-and-swap, and losing-worker cleanup.

- [x] 4. Prove real-process recovery and close Task 018.
  **Context:** Start three recovery-enabled Grovlets, complete an order, kill
  Inventory's node, condition-wait for unavailability and replacement placement,
  then complete another order. Repeat the scenario and run formatting, vet,
  race, and full suites before marking Task 018 DONE.
  **Outcome:** Three repeated recovery scenarios pass. Formatting, diff checks,
  vet, the complete race suite, and `go test -count=1 ./...` pass. Task 018 is
  DONE.

## Log

- 2026-09-09: Tasks 001 through 017 completed with focused, repeated, race,
  vet, and full-suite checks passing.
- 2026-09-09: Selected opt-in, deterministic stateless recovery so Task 017
  remains stable and Task 019's desired-state model stays out of scope.
- 2026-09-09: Completed Task 018 with replicated placement replacement,
  deterministic real-process Inventory recovery, and all historical race/full
  tests passing.
