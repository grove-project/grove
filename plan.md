# Plan: Prove the complete Grove Shop MVP lifecycle

## Goal

Complete Task 031 with one automated, production-shaped Grove Shop lifecycle:
build and configure immutable artifacts, run the embedded Web experience on a
real three-Grovlet cluster, prove distributed orders, node recovery and durable
restart, observe a broken candidate and rollback through the Web status
contract, rerun the application and resilience flow, and clean up every child.

## Context

Tasks 001–030 already implement each underlying runtime capability. Task 031
connects the existing System NATS cluster, placement, component, deployment,
rollout, artifact, recovery, and desired-state APIs into the Web observer and a
single regression scenario. It does not introduce another control store,
rollout engine, scheduler, or deployment daemon.

### Key Files

- `tasks/031-final-mvp-lifecycle-e2e.md` — final acceptance contract.
- `demo/groveshop/web.go` and `demo/groveshop/web/index.html` — embedded order
  API/UI and structured polling contract.
- `cmd/grovlet/worker.go` and a small status adapter under `cmd/grovlet/` — map
  production System NATS views into the Grove Shop Web read model.
- `cmd/grovlet/main.go` — retain the configured Web component when recovery is
  enabled.
- `cmd/grove/mvp_test.go` — one complete real-process MVP lifecycle proof.

### Decisions Made

- Keep Grove Shop free of control-plane dependencies. It owns public JSON view
  types and HTTP handlers; the Grovlet worker adapts `internal/systemnats`
  views into those types.
- Serve `POST /api/orders`, `GET /api/orders`, `/grove/config`, and
  `/grove/status` from the embedded Web component. The browser polls status
  every 750 ms and remains an observer only.
- Derive cluster health from authoritative membership plus the components
  selected by placement. Recovery-only fallback slots that are intentionally
  stopped do not make the application appear degraded.
- Build Artifact A through the existing target-owned config compiler/embed
  path, record its desired deployment and active rollout, and place Web with
  Orders while Inventory runs on another Grovlet so the HTTP order proof must
  cross a Grovlet boundary.
- Kill Inventory's hosting Grovlet, wait for the existing recovery coordinator,
  update durable desired intent to the recovered placement, restart the full
  cluster from the same runtime directories, and verify reconstruction.
- Build Artifact B from the same code with the documented negative Inventory
  buffer. Observe pending and rolled-back states through repeated HTTP status
  reads, while candidate startup failure and rollback remain runtime/control
  plane responsibilities.
- Invoke the Task 030 `grove test --resilience` workflow against Artifact A at
  the end. The final E2E composes existing lower-level APIs because a
  long-lived local deployment daemon and state discovery contract for the
  headline `grove deploy --config ...` syntax were not defined by Tasks
  021–030; Task 031 will not invent that architecture implicitly.

## Sub-Tasks

- [ ] 1. Expose the live Grove Shop Web contract.
  **Context:** Add typed status/order HTTP endpoints, embedded UI interaction
  and 750 ms polling, unit tests, and the Grovlet adapter over real control
  views.

- [ ] 2. Compose the final real-process lifecycle E2E.
  **Context:** Prove Artifact A, Web/status/config, cross-node order history,
  Inventory node recovery, durable full-cluster restart, Artifact B rejection,
  structured rollback observation, post-rollback order, and Task 030
  resilience execution without sleeps.

- [ ] 3. Verify and close the Grove MVP.
  **Context:** Run formatting, diff checks, focused repetitions, vet, the full
  uncached suite, and the full race suite. Mark Task 031 DONE only after all
  earlier E2Es remain green, then rebase and push directly to `main`.

## Log

- 2026-09-13: Task 030 passed all focused, uncached, and race gates and was
  pushed to `main` at `4145e12`.
- 2026-09-13: Confirmed that all lifecycle mechanisms exist independently;
  Task 031 will add the missing Web read-model adapter and one composed proof,
  not parallel runtime implementations.
