# Plan: Reject an unhealthy candidate and retain the known-good artifact

## Goal

Complete Task 028 by observing the Grove Shop candidate Inventory fail from its
negative embedded reservation buffer, durably recording structured rejection
and rollback progress, reconciling all routes to the retained current artifact,
and proving Artifact A still completes orders. Do not mutate either artifact or
add migration, rollout policy, or historical rollback chains.

## Context

Task 027 commits healthy candidates through durable rollout generations and
placement compare-and-set writes. Task 028 adds the failure branch. The
previous artifact and workers remain alive until candidate acceptance, so a
pre-handoff failure normally requires no process restart; placement
reconciliation still repairs any route already moved during an interrupted
handoff.

### Key Files

- `tasks/028-rollback.md` — current acceptance contract.
- `demo/CONFIGURATION.md`, `demo/DEMO_FLOW.md`, and `demo/UI.md` — broken
  Inventory configuration, lifecycle, and structured observability contract.
- `internal/systemnats/deployment.go` — durable failure/rollback phases and
  structured reason.
- `internal/systemnats/upgrade.go` — idempotent route restoration and final
  known-good ownership commit.
- `cmd/grove/rollback_test.go` — immutable Artifact A/B process-level rollback
  proof.

### Decisions Made

- Add `candidate-failed`, `rolling-back`, and `rolled-back` rollout phases.
  Each transition is a new generation in the existing R3 deployment bucket.
- Store a structured rollout failure with stable code plus optional component
  and field and a human-readable message. Keep the rejected candidate digest
  in the terminal rollout so CLI/UI readers can show cause and result while the
  current digest continues to identify active Artifact A.
- Reconcile every candidate route back to its corresponding current route in
  stable service-ID order. Existing placement compare-and-set behavior treats
  an already-current route as an idempotent success, covering both pre-cutover
  rejection and interrupted partial handoff.
- Verify reverse bootstrap negotiation before attempting the candidate so the
  retained artifact remains a compatible recovery anchor.
- Build the intentionally broken E2E artifact through the generic immutable
  artifact envelope with target-owned Gob bytes. Normal `grove config embed`
  continues to reject invalid operator configuration; this test fixture is
  deliberately integrity-valid but semantically rejected by the target at
  startup, which exercises runtime rollback without weakening Task 024.

## Sub-Tasks

- [x] 1. Define and test durable rollback state.
  **Context:** Add structured failure validation, legal failure/rollback
  transitions, terminal rejected-candidate retention, and malformed/illegal
  transition coverage.

- [x] 2. Implement and test idempotent rollback reconciliation.
  **Context:** Record failure and rollback intent, restore candidate routes to
  current placement, commit terminal rolled-back state, and prove retries are
  safe when routes never switched or switched only partially.

- [x] 3. Prove broken-config rollback end to end.
  **Context:** Run healthy A, record B pending, verify bidirectional bootstrap
  compatibility, start B Inventory with `reservation_buffer < 0`, observe its
  startup failure, roll back through replicated state, verify A's artifact and
  config identities plus placement, and complete another order on A.

- [x] 4. Verify and close Task 028.
  **Context:** Run formatting, diff checks, focused repetitions, vet, full
  uncached tests, and full race tests; mark DONE only after all prior E2Es pass,
  then rebase and push directly to `main`.

## Log

- 2026-09-12: Task 027 passed focused, full uncached, and race gates and was
  pushed to `main` at `b3ed2ce`.
- 2026-09-12: Kept normal invalid-config CLI rejection intact. The broken
  runtime artifact is a deliberate E2E fixture so rollback is exercised after
  immutable artifact creation rather than by weakening earlier validation.
- 2026-09-12: Structured rollback state, legal transition enforcement, and
  current/partially-switched placement reconciliation passed three focused R3
  JetStream/KV integration repetitions.
- 2026-09-12: Broken Inventory rollback passed three real-process repetitions
  plus focused race coverage. Bootstrap compatibility succeeded forward and
  backward without decoding application config; candidate startup then failed
  on `inventory.reservation_buffer`, and Artifact A completed an order after
  the durable rollback.
- 2026-09-12: Final verification passed `go vet ./...`,
  `go test -count=1 ./...`, and `go test -race -count=1 ./...`.
