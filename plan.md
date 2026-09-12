# Plan: Commit a health-gated artifact upgrade

## Goal

Complete Task 027 by advancing a side-by-side Grove Shop candidate through a
small durable rollout state machine, proving the exact candidate artifact is
healthy over the stable bootstrap boundary, switching service placement with
JetStream/KV compare-and-set writes, and retiring current workers only after
N+1 is durably active. Do not add rollback, canaries, migration, or rollout
policy.

## Context

Task 026 can run and explicitly invoke N and N+1 at the same time. Task 027
turns that coexistence into the simplest deterministic ownership handoff. The
authoritative rollout generation and every placement change remain replicated
JetStream/KV facts; candidate readiness is ephemeral System NATS messaging.

### Key Files

- `tasks/027-traffic-switch-and-upgrade.md` — current acceptance contract.
- `docs/architecture/bootstrap-and-binary-handoff.md` — stable readiness and
  ownership-handoff constraints.
- `internal/bootstrap/bootstrap.go` — versioned readiness wire message below
  the application SDK.
- `internal/systemnats/deployment.go` — durable rollout phases and validated
  generation transitions.
- `internal/systemnats/upgrade.go` — exact-artifact readiness probe and
  deterministic placement/active-artifact commit sequence.
- `cmd/grovlet/main.go` — publishes candidate runtime readiness only after its
  invocation endpoint is active.
- `cmd/grove/upgrade_test.go` — real N to N+1 handoff and post-cutover request
  proof.

### Decisions Made

- Extend the stable bootstrap JSON envelope with a minimal readiness message:
  node ID, exact artifact digest, and healthy state. Unknown optional fields
  remain ignorable; no SDK/runtime structs enter this wire contract.
- Persist four operator-visible rollout phases: `pending`,
  `candidate-healthy`, `switching`, and `active`. Each transition is a new
  rollout generation in the existing R3 deployment KV bucket.
- Gate handoff by requesting readiness from every candidate route and matching
  the returned node and artifact identities. A missing, unhealthy, or wrong
  artifact cannot advance durable state.
- Switch placement records in stable service-ID order with existing KV
  compare-and-set semantics, then commit N+1 as the active artifact. The
  `switching` generation makes the unavoidable multi-key MVP handoff window an
  explicit durable state rather than hidden process memory.
- Stop N's application workers only after the final active rollout generation
  commits. Keep Grovlet/System NATS processes alive because they remain quorum
  members; binary process replacement is represented by retiring their N
  application ownership in this MVP topology.

## Sub-Tasks

- [ ] 1. Add and test stable candidate readiness messaging.
  **Context:** Define the versioned bootstrap payload, System NATS endpoint,
  exact identity validation, and bounded health observation without fixed
  sleeps.

- [ ] 2. Implement and test durable upgrade transitions.
  **Context:** Validate routes and phases, reject unhealthy/wrong candidates,
  commit candidate-health and switching generations, replace placements in
  deterministic order, and finish with N+1 active.

- [ ] 3. Expose readiness from running candidate Grovlets.
  **Context:** Register readiness after the candidate invocation subscription
  is active so health implies the process can receive application traffic.

- [ ] 4. Prove N to N+1 ownership handoff end to end.
  **Context:** Run real current and candidate artifacts, verify pre-cutover N
  behavior, commit the upgrade, retire N workers after commit, verify durable
  active/placement state, and prove post-cutover requests reach N+1.

- [ ] 5. Verify and close Task 027.
  **Context:** Run formatting, diff checks, focused repetitions, vet, full
  uncached tests, and full race tests; mark DONE only after all prior E2Es pass,
  then rebase and push directly to `main`.

## Log

- 2026-09-12: Task 026 passed focused, full uncached, and race gates and was
  pushed to `main` at `40185ab`.
- 2026-09-12: Kept failed-candidate rejection and restoring prior placement out
  of this task; Task 028 owns rollback and structured rollback reasons.
