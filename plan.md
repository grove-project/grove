# Plan: Supervise hosted Grove Shop components

## Goal

Complete Task 016 by moving hosted Grove Shop execution behind a
Grovlet-owned worker-process lifecycle, reporting starting, healthy, stopping,
stopped, and failed component states, and accepting explicit start/stop
commands. Prove Inventory can be stopped and restarted while its authoritative
placement remains unchanged and the Orders flow succeeds again, without adding
automatic recovery or durable lifecycle state.

## Context

Tasks 001 through 015 are complete. Task 015 stores authoritative service
placement in JetStream/KV and routes Orders to Inventory through the watched
placement view. Task 016 adds local execution supervision for those placed
services. Placement remains authoritative cluster metadata; component state is
an observed, process-local read model. Task 017 will cover abrupt Grovlet loss,
and Task 018 will introduce cluster-driven recovery.

ADR-002 requires application code to execute outside the Grovlet supervisor.
The Grovlet executable will therefore launch an internal worker mode rather
than merely toggling handlers inside the control-plane process. One worker per
independently controlled component is justified by this task's independent
start/stop requirement; worker consolidation and optimization remain outside
the task.

### Key Files

- `tasks/016-managed-component-lifecycle.md` — state and start/stop/restart E2E
  requirements.
- `tasks/017-node-failure-detection-e2e.md` and
  `tasks/018-service-recovery-after-node-failure.md` — boundaries excluding
  failure recovery from this increment.
- `docs/adr/002-grovlet-worker-process-boundary.md` — accepted supervisor and
  application-worker isolation decision.
- `sdk/SERVICE_MODEL.md` and `sdk/INVOCATION.md` — explicit worker registration
  and unchanged Grove call semantics.
- `internal/systemnats/component.go` — planned machine-readable component view
  and start/stop command transport.
- `cmd/grovlet/component.go` — planned local process supervisor and component
  state machine.
- `cmd/grovlet/worker.go` — planned internal worker mode for concrete Grove Shop
  registration and dispatch.
- `cmd/grovlet/main.go` — supervisor construction, worker startup, and ordered
  shutdown.
- `cmd/grovlet/main_test.go` — real-process stop/restart/application-flow E2E.

### Decisions Made

- Represent each hosted service as one independently supervised worker process
  because Task 016 requires component-specific stop and restart. Do not add a
  general worker packing policy.
- Launch the same Grovlet artifact in an internal worker mode. The worker owns
  the concrete Grove Shop registry and System NATS invocation subscription;
  the parent Grovlet retains membership, health, placement, and lifecycle APIs.
- Derive a service-specific invocation subject from the configured node subject
  and service ID so independently controlled workers never compete for the same
  NATS request subscription.
- Have Orders workers resolve Inventory by querying their parent Grovlet's
  watched placement endpoint before each Grove call. This keeps routing driven
  by Task 015 state without making workers JetStream control-plane members.
- Keep component state local and ephemeral. Transitions are starting to
  healthy, healthy to stopping to stopped, and any startup or unexpected exit
  to failed. Start is permitted from stopped or failed; no automatic restart.
- Expose per-node JSON snapshot and start/stop command subjects on System NATS.
  Commands are explicit test/control operations and do not mutate placement.
- Give each worker a parent-owned pipe. EOF cancels the worker if its Grovlet
  exits abruptly, preventing orphaned application execution before Task 017.
- Stop and join workers before health, placement, membership, transport, and
  embedded NATS shutdown.

## Sub-Tasks

- [x] 1. Select and bound Task 016.
  **Context:** Read Task 016 through Task 018, the worker-boundary and failure
  ADRs, current registry/routing code, process harness, and placement wiring.
  **Outcome:** Chose real worker-process supervision, ephemeral local component
  state, explicit System NATS lifecycle commands, service-specific subjects,
  parent-death cleanup, and no recovery, persistence, upgrade, or ownership
  behavior.

- [x] 2. Expose component lifecycle state and commands.
  **Context:** Define component states/status/view, a controller contract, and
  per-node System NATS snapshot/start/stop request-reply operations with JSON
  responses and explicit errors.
  **Outcome:** Added component state/status/view contracts plus per-node JSON
  snapshot and start/stop endpoints. Package tests prove command dispatch,
  returned state, remote controller failures, stable subjects, and nil
  controller rejection.

- [x] 3. Implement Grovlet worker supervision.
  **Context:** Add a concurrency-safe component manager with injectable process
  startup for unit tests, all required transitions, unexpected-exit failure
  reporting, restart from stopped/failed, and bounded graceful shutdown.
  **Outcome:** Added a concurrency-safe manager with injectable process startup,
  deterministic sorted views, explicit transitions, unexpected-exit failure
  capture, restart from stopped/failed, and ordered shutdown. Repeated unit
  tests exercise all five states and invalid transitions.

- [x] 4. Move placed Grove Shop services into workers.
  **Context:** Add internal worker mode, explicit Orders/Inventory registration,
  placement-view routing for Orders, service-specific placement subjects,
  parent-death cancellation, and Grovlet startup/shutdown integration.
  **Outcome:** Added internal worker mode to the Grovlet artifact. Orders and
  Inventory now register and serve in child processes on service-specific
  subjects; Orders queries its parent placement view for Inventory routing.
  Parent-owned pipe EOF stops orphaned workers, and prior placement/cross-node
  tests pass.

- [x] 5. Prove stop and restart end to end.
  **Context:** Start the three-Grovlet Orders/Inventory placement, wait for
  healthy component views, stop Inventory, wait for stopped, start it again,
  wait for healthy, and execute a successful order.
  **Outcome:** Added a real three-Grovlet E2E that waits for healthy Inventory,
  stops it remotely, waits for stopped, verifies placement is unchanged,
  restarts it, waits for healthy, and completes the Orders flow. All waits are
  bounded and failures include cluster logs.

- [x] 6. Verify and close Task 016.
  **Context:** Review docs and task boundaries, format, vet, repeat focused
  manager/E2E tests, run race and full suites, then mark Task 016 DONE.
  **Outcome:** Reviewed exported docs and task boundaries; `gofmt -l .` and
  `git diff --check` are clean. `go vet ./...`, ten repeated manager tests,
  three repeated worker lifecycle E2Es, focused race tests,
  `go test -race -count=1 ./...`, and `go test -count=1 ./...` pass. Task 016 is
  marked DONE.

## Log

- 2026-09-09: Tasks 001 through 015 completed with focused, repeated, race,
  vet, and full-suite checks passing.
- 2026-09-09: Selected Task 016 and retained the accepted Grovlet/worker process
  boundary while excluding all automatic recovery and durable lifecycle state.
- 2026-09-09: Added component state/control APIs, real worker supervision,
  service-specific routing, and parent-death cleanup.
- 2026-09-09: Passed repeated real-process stop/restart flows plus vet, race,
  formatting, and the complete historical test suite; no scope deviations or
  architectural issues found.
