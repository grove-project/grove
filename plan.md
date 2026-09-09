# Plan: Route Grove Shop through replicated service placement

## Goal

Complete Task 015 by recording explicit service-to-Grovlet assignments in a
three-replica System NATS JetStream/KV bucket, maintaining the same watched
placement view on every Grovlet, and resolving the Grove Shop Orders to
Inventory call from that view. Prove the records and cross-node flow with
three real Grovlet processes without adding scheduling, lifecycle
reconciliation, ownership, or recovery.

## Context

Tasks 001 through 014 are complete. Task 013 established the replicated
JetStream/KV membership pattern and Task 014 layered ephemeral health on top.
Task 015 adds authoritative placement metadata but does not react to health;
Tasks 016 through 018 own managed component lifecycle and recovery.

The Task 015 text still names the pre-reference-app Workflow and Greeter
services. The accepted `IMPLEMENTATION_PLAN.md`, `sdk/EXAMPLE.md`, Task 010,
and `demo/` contracts replaced that example with Grove Shop. This task uses
the equivalent canonical split: Orders on node A and Inventory on node B.

### Key Files

- `tasks/015-explicit-service-placement.md` — placement state, observation,
  routing, and real-process acceptance requirements.
- `sdk/SERVICE_MODEL.md` and `sdk/INVOCATION.md` — registration/placement
  separation and the unchanged explicit Grove call path.
- `demo/ARCHITECTURE.md` and `demo/IMPLEMENTATION_GUIDE.md` — canonical Grove
  Shop component topology and incremental task boundary.
- `internal/systemnats/membership.go` — established replicated KV observer
  pattern for durable cluster metadata.
- `internal/systemnats/placement.go` — planned authoritative placement model,
  observer, read API, and placement-backed router.
- `cmd/grovlet/main.go` — explicit local Grove Shop assignments and placement
  observer lifecycle.
- `cmd/grovlet/main_test.go` — three-process placement and cross-node flow E2E.

### Decisions Made

- Store one JSON record per service under `services.<service-id>` in a
  file-backed `GROVE_PLACEMENT` bucket with three replicas and history one.
- Keep the minimum routing-complete record: stable service ID, selected node
  ID, and that node's System NATS invocation subject. Registration remains a
  separate local capability as required by the SDK contract.
- Treat the Grovlet's existing explicit Grove Shop flags as placement input.
  Add a direct Orders flag; when membership is enabled, locally hosted Orders
  and Inventory records are written to authoritative placement state.
- Start an empty placement observer on every membership-enabled Grovlet so
  nodes without application services still expose the same watched view.
- Resolve each call from the latest watcher-derived placement snapshot. Missing
  or initializing placement fails explicitly; no retry, fallback, failover, or
  health-based relocation is introduced.
- Preserve Task 010's explicit Inventory-subject mode for non-cluster tests and
  compatibility. Task 015's Orders mode uses the placement-backed router.
- Expose a per-node JSON placement-view subject rather than merging placement
  into the Task 014 health view. Later structured status work can compose these
  read models without changing their authoritative sources.

## Sub-Tasks

- [x] 1. Select and bound Task 015.
  **Context:** Read Task 015, Task 016, accepted SDK/demo contracts, current
  membership, transport, Grovlet wiring, and existing cross-node E2E.
  **Outcome:** Chose canonical Orders/Inventory placement, a routing-complete
  KV record, watched per-node views, explicit assignment flags, and no
  scheduling, lifecycle management, recovery, ownership arbitration, or
  health-triggered mutation.

- [x] 2. Implement authoritative placement state and routing.
  **Context:** Add the placement record/view types, validation, replicated KV
  writer/watcher, deterministic snapshot and lookup, per-node JSON API, and a
  Grove Router that selects the invocation subject from placement.
  **Outcome:** Added validated service/node/subject records, a three-replica
  file-backed KV bucket, retrying watchers, sorted snapshots and lookup, a
  per-node JSON query endpoint, and a placement-backed Grove Router. Package
  tests prove raw KV state, replica count, three-observer convergence, explicit
  missing/unready errors, and routed invocation.

- [x] 3. Wire explicit Grove Shop assignments into Grovlet.
  **Context:** Add an Orders placement flag, build local Orders/Inventory
  placement records for membership-enabled nodes, start/serve placement on
  every clustered node, construct Orders with a placement-backed client, and
  cancel/join placement before membership and transport shutdown.
  **Outcome:** Added the clustered `--grove-shop-orders` assignment, translated
  local Orders/Inventory hosting into placement records, started a placement
  observer/API on every membership-enabled Grovlet, and constructed Orders
  with the placement-backed client. Shutdown joins health, placement, and
  membership in dependency order. Config and prior cluster tests pass.

- [x] 4. Prove replicated placement and routed execution end to end.
  **Context:** Start three real mutually seeded Grovlets with Orders assigned to
  node A, Inventory assigned to node B, and node C as an observer. Condition-
  wait on each node's placement view, then invoke Orders and verify Inventory's
  cross-node result.
  **Outcome:** Added a three-process E2E that assigns Orders to node 1,
  Inventory to node 2, and leaves node 3 observation-only. It condition-waits
  for identical placement views on all three, then invokes Orders from node 3
  and verifies the cross-node reservation and completed order. Timeout failures
  include every process log.

- [x] 5. Verify and close Task 015.
  **Context:** Review exported docs and state boundaries, format, vet, repeat
  focused placement/E2E tests, run race and full suites, then mark Task 015
  DONE.
  **Outcome:** Reviewed exported docs and the complete diff; `gofmt -l .` and
  `git diff --check` are clean. `go vet ./...`, five repeated placement package
  scenarios, three repeated real-process placement flows,
  `go test -race -count=1 ./...`, and `go test -count=1 ./...` pass. Task 015 is
  marked DONE.

## Log

- 2026-09-09: Tasks 001 through 014 completed with focused, repeated, race,
  vet, and full-suite checks passing.
- 2026-09-09: Selected Task 015 and mapped its stale Workflow/Greeter names to
  the accepted Grove Shop Orders/Inventory reference flow.
- 2026-09-09: Added replicated placement records, watcher-derived views, the
  machine-readable API, and a placement-backed Grove invocation router.
- 2026-09-09: Wired explicit Orders/Inventory assignments into Grovlet and
  passed repeated three-process placement convergence and application flows.
- 2026-09-09: Completed Task 015 after vet, race, formatting, repeated focused,
  and full-suite verification; no architectural issues found.
