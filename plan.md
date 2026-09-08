# Plan: Implement local service invocation

## Goal

Complete Task 007 by introducing the application-facing generic `grove.Call`
primitive, routing local calls through a Client into the explicit Registry,
preserving lookup/handler failures, and changing Grove Shop Orders to invoke
Inventory through that visible Grove boundary while retaining direct ordinary-Go
construction for unit tests.

## Context

Tasks 001 through 006 are complete. Task 007 owns only local invocation. Task
008 owns Gob and invocation envelopes, Task 009 owns System NATS transport, and
Task 010 owns remote routing. The current Grovlet has only lifecycle behavior;
there is no application-hosting or inter-process request contract yet. The E2E
can gate a real Grovlet and exercise the complete local application path in the
test process, but moving application execution into that OS process would
require premature hosting or transport functionality.

### Key Files

- `tasks/007-local-service-invocation.md` — public call semantics, local path,
  Grove Shop reference call, E2E, and hard exclusions.
- `sdk/INVOCATION.md`, `sdk/SERVICE_MODEL.md`, and `sdk/EXAMPLE.md` — normative
  visible call boundary and same-API local/remote contract.
- `invocation.go` — Client, generic Call, local route, and typed failures.
- `invocation_test.go` — call-path tests and one-Grovlet Grove Shop E2E.
- `demo/groveshop/groveshop.go` — Orders construction and explicit
  Orders-to-Inventory Grove call.
- `tasks/008-invocation-serialization-envelope.md` through
  `tasks/010-cross-node-service-invocation.md` — serialization and transport
  boundaries preserved for later tasks.

### Decisions Made

- Add `NewClient(registry)` and generic
  `Call[Request, Response](ctx, client, serviceID, methodID, request)` matching
  the accepted developer-facing shape.
- Keep Client's local route internal. `Call` does not know whether execution is
  local or remote, so Task 010 can change routing underneath without changing
  application call sites.
- Return typed `InvocationError` values with explicit IDs and wrapped causes.
  Unknown service/method and concrete handler failures remain distinguishable
  through `errors.Is`; wrong response types have their own sentinel cause.
- Keep application request/response values opaque and in-process. No Gob, byte
  envelope, request ID, or transport concern enters before Task 008.
- Preserve `NewOrders` for direct ordinary-Go tests. Add `NewGroveOrders` for
  the runtime-shaped variant; its `Create` method visibly calls `grove.Call`
  for Inventory while Payment and Shipping remain direct in this increment.
- Build the Grovlet once in the root test package, start a real process, wait on
  its lifecycle protocol, execute the registered local Grove Shop flow, and
  stop it cleanly. Do not add a test-only Grovlet app mode or early IPC.

## Sub-Tasks

- [x] 1. Select and bound Task 007.
  **Context:** Read the task, invocation/service contracts, current Registry and
  Grove Shop APIs, process model, and Tasks 008-010.
  **Outcome:** Chose a generic public call plus internal local route and
  documented the pre-transport E2E limitation instead of expanding scope.

- [x] 2. Implement the local Client and Call path.
  **Context:** Add Client construction, context-preserving local resolution,
  generic response checking, and typed errors that wrap registry and handler
  causes.
  **Outcome:** Added Client construction and generic `Call`, which checks
  cancellation, resolves the Registry, invokes the local Handler, validates the
  application response type, and wraps every failure with explicit IDs.

- [x] 3. Route Orders to Inventory through Grove.
  **Context:** Add an explicit runtime-shaped Orders constructor and make
  `Orders.Create` visibly use the stable Inventory IDs at `grove.Call`, while
  preserving its direct constructor for business-only unit tests.
  **Outcome:** Added `NewGroveOrders`; its unchanged `Create` workflow visibly
  calls `grove.Call[ReserveRequest, Reservation]` with the application-owned
  Inventory IDs. The existing direct constructor keeps business-only unit tests
  independent of runtime setup.

- [x] 4. Prove local invocation and the one-Grovlet flow.
  **Context:** Test successful dispatch, ID lookup failures, handler failures,
  context propagation, response type mismatch, and the complete registered
  Grove Shop order workflow while a real Grovlet is ready.
  **Outcome:** Added a runnable Call example plus coverage for successful typed
  execution, context propagation/cancellation, nil setup, lookup failures,
  handler errors, and response mismatches. The E2E gates a real Grovlet through
  readiness and shutdown while the worker-equivalent test process completes a
  registered Grove Shop order through local invocation.

- [x] 5. Verify and close Task 007.
  **Context:** Review `go doc`, format, vet, run repeated and race tests, run the
  full repository suite, then mark Task 007 DONE after every check succeeds.
  **Outcome:** Reviewed the complete `go doc` surfaces; `gofmt -l` reports no
  files; `go vet ./...`, 50 repeated call tests, 20 repeated real-process local
  Grove Shop E2Es, `go test -race -count=1 ./...`, and `go test -count=1 ./...`
  pass. Task 007 is marked DONE.

## Log

- 2026-09-08: Tasks 001 through 006 completed with all focused, race, vet, and
  full-suite checks passing.
- 2026-09-08: Selected Task 007 and recorded the stable generic call shape,
  local routing, direct-test constructor, future remote boundary, and the
  lifecycle-only Grovlet limitation.
- 2026-09-08: Completed Task 007 after focused, repeated, race, vet, and full
  suite verification. The E2E does not move application code into the Grovlet
  supervisor process: worker hosting/supervision and IPC are later-task
  responsibilities, so the application executes in the test's local worker
  context while a real Grovlet is lifecycle-gated.
