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

- [ ] 2. Implement authoritative placement state and routing.
  **Context:** Add the placement record/view types, validation, replicated KV
  writer/watcher, deterministic snapshot and lookup, per-node JSON API, and a
  Grove Router that selects the invocation subject from placement.
  **Acceptance:** Package tests prove three-replica KV configuration, identical
  sorted views on three observers, invalid-input behavior, and a call routed
  through the selected placement.

- [ ] 3. Wire explicit Grove Shop assignments into Grovlet.
  **Context:** Add an Orders placement flag, build local Orders/Inventory
  placement records for membership-enabled nodes, start/serve placement on
  every clustered node, construct Orders with a placement-backed client, and
  cancel/join placement before membership and transport shutdown.
  **Acceptance:** Config tests cover the new valid and invalid combinations;
  existing explicit-subject invocation and earlier cluster behavior remain
  unchanged.

- [ ] 4. Prove replicated placement and routed execution end to end.
  **Context:** Start three real mutually seeded Grovlets with Orders assigned to
  node A, Inventory assigned to node B, and node C as an observer. Condition-
  wait on each node's placement view, then invoke Orders and verify Inventory's
  cross-node result.
  **Acceptance:** Every node reports the same two records before the successful
  order flow; waits are bounded without fixed sleeps and failures dump all
  process logs.

- [ ] 5. Verify and close Task 015.
  **Context:** Review exported docs and state boundaries, format, vet, repeat
  focused placement/E2E tests, run race and full suites, then mark Task 015
  DONE.
  **Acceptance:** `gofmt -l` is empty; `git diff --check`, `go vet ./...`,
  focused repeated tests, `go test -race -count=1 ./...`, and
  `go test -count=1 ./...` pass without weakening prior coverage.

## Log

- 2026-09-09: Tasks 001 through 014 completed with focused, repeated, race,
  vet, and full-suite checks passing.
- 2026-09-09: Selected Task 015 and mapped its stale Workflow/Greeter names to
  the accepted Grove Shop Orders/Inventory reference flow.
