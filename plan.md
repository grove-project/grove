# Plan: Reconcile authoritative desired deployments

## Goal

Complete Task 019 by storing a minimum desired Grove deployment in replicated
JetStream/KV, exposing it through System NATS control APIs, and restarting a
killed worker when observed local runtime state diverges from that intent.

## Context

Tasks 001 through 018 are complete. Placement describes current routing,
component views describe observed worker lifecycle, and opt-in recovery can
move a service after node loss. Desired state must now describe intent
separately. Task 020 owns persistence across a complete cluster restart, and
Task 025 owns simultaneous N/N+1 version records.

### Key Files

- `tasks/019-desired-state-in-jetstream-kv.md` — desired-state acceptance
  contract.
- `internal/systemnats/desired.go` — replicated desired deployment model,
  watcher, and System NATS read/write API.
- `internal/systemnats/desired_test.go` — model validation, KV encoding, and
  multi-observer convergence.
- `cmd/grovlet/component.go` — observed worker generation and explicit kill.
- `cmd/grovlet/reconcile.go` — local desired-versus-observed decisions.
- `cmd/grovlet/main_test.go` — real-process desired-state reconciliation E2E.

### Decisions Made

- Store one record per application under `GROVE_DESIRED`. The Task 019 record
  contains application ID, one current version label, and unique service/node
  assignments. Task 025 will extend this to distinct concurrent versions.
- Use a System NATS request/reply endpoint for writes; only JetStream/KV is
  authoritative. Every Grovlet maintains an independent watcher-derived view.
- Reconcile only components assigned to the local node and only when a desired
  deployment exists. Empty desired state does not stop Task 018 boot placements.
- Expose worker generation in observed component status and add an explicit
  kill control command so the E2E proves a new OS worker is started, rather
  than treating a graceful stop as a crash.
- Keep placement mutation, node-failure recovery, durable restart, artifacts,
  and rollout policy outside this increment unless required by the existing
  Task 018 behavior.

## Sub-Tasks

- [x] 1. Add replicated desired deployment state.
  **Context:** Define and validate the minimum model, create the three-replica
  file-backed KV bucket, maintain sorted watcher views, and expose read/write
  request endpoints.
  **Outcome:** Added validated, sorted application/version/component records,
  three-replica KV observation, and System NATS read/write endpoints.

- [x] 2. Represent and inject worker loss.
  **Context:** Add observed worker generation and an explicit component kill
  command without changing normal start/stop semantics.
  **Outcome:** Component views now report worker generation, and the control
  endpoint can abruptly kill a worker while retaining failed observed state.

- [x] 3. Reconcile desired local components.
  **Context:** Compute deterministic start decisions from desired assignments
  and observed component states, then run a bounded local reconciliation loop
  on desired-state-enabled Grovlets.
  **Outcome:** Added deterministic local start decisions and a bounded loop
  that creates a new worker generation for stopped or failed assigned services.

- [x] 4. Prove convergence and close Task 019.
  **Context:** Write Grove Shop desired state through the control API, observe
  it from all three Grovlets, kill Inventory's worker, wait for a later healthy
  generation, and verify Orders still completes. Repeat and run formatting,
  vet, race, and full suites before marking Task 019 DONE.
  **Outcome:** Replicated desired-state integration coverage and repeated real
  process reconciliation pass; vet, the full race suite, and uncached full
  suite pass. Task 019 is DONE.

## Log

- 2026-09-09: Tasks 001 through 018 completed with focused, repeated, race,
  vet, and full-suite checks passing.
- 2026-09-09: Selected a single-current-version desired record and local worker
  reconciliation, preserving Tasks 020 and 025 boundaries.
- 2026-09-09: Completed Task 019 with authoritative desired state, observable
  worker generations, and automated reconstruction after worker loss.
