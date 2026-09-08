# Plan: Implement the Grove Shop reference application

## Goal

Complete Task 005 by adding the deterministic Grove Shop business domain used
by later runtime and E2E tasks. The application must expose ordinary Go types
for Web, Orders, Inventory, Payment, and Shipping; complete the canonical order
progression; support Web-facing create and inspection calls; and remain fully
unit-testable without Grove.

## Context

Tasks 001 through 004 are complete. Task 005 introduces only application-domain
code. Task 006 will add explicit Grove registration, so this task must not add
service IDs, handlers, serialization, invocation clients, transport, or any
other runtime abstraction. The normative SDK and demo contracts require
concrete, directly navigable business implementations and the progression
`Created -> Reserved -> Paid -> Shipping -> Completed`.

### Key Files

- `tasks/005-reference-application.md` — required components, business flow,
  unit coverage, and hard scope boundary.
- `sdk/DESIGN_PRINCIPLES.md` and `sdk/EXAMPLE.md` — accepted ordinary-Go service
  model and canonical Inventory shape.
- `demo/README.md`, `demo/ARCHITECTURE.md`, and `demo/UI.md` — permanent Grove
  Shop topology and the Web-facing create/inspect model.
- `demo/groveshop/groveshop.go` — complete small business domain.
- `demo/groveshop/groveshop_test.go` — application workflow, deterministic
  component behavior, error propagation, and runnable usage example.

### Decisions Made

- Use package `demo/groveshop` so the reference application remains alongside
  its normative demo contracts without turning those contracts into runtime
  implementation.
- Keep Orders dependencies concrete: `*Inventory`, `*Payment`, and `*Shipping`.
  No application interfaces or Grove-specific types are introduced.
- Let callers own explicit order IDs. Component result IDs derive solely from
  the order ID, making every business result deterministic and test-friendly.
- Represent progress with a current `OrderStatus` and immutable snapshots of
  the full status history. Successful orders are retained in creation order for
  Web-facing get/list inspection.
- Validate each component's own inputs with exported sentinel errors. Orders
  wraps component failures while preserving them for `errors.Is` checks.
- Keep Web as a thin ordinary-Go facade over Orders. HTTP serving, UI assets,
  and Grove status polling remain outside Task 005.

## Sub-Tasks

- [x] 1. Select and bound Task 005.
  **Context:** Read the task, normative SDK example/principles, demo overview,
  architecture, UI contract, and Task 006 boundary.
  **Outcome:** Chose one small standalone Go package with concrete services,
  deterministic values, in-memory inspection, and no Grove runtime imports.

- [x] 2. Implement deterministic component methods.
  **Context:** Add Inventory reservation, Payment charging, and Shipping
  arrangement request/result types, validation, and stable outputs.
  **Outcome:** Added ordinary Inventory, Payment, and Shipping types with
  request/result models, context handling, exported validation errors, and
  repeatable result IDs derived from the application-owned order ID.

- [x] 3. Implement the Orders workflow and Web facade.
  **Context:** Coordinate all three concrete components, record the canonical
  progression, retain successful order snapshots, reject duplicate IDs, and
  expose create/get/list calls suitable for a later HTTP layer.
  **Outcome:** Added concrete service composition, the complete five-stage
  workflow, duplicate detection, isolated retained snapshots in creation order,
  and thin Web create/get/list methods without HTTP or Grove dependencies.

- [x] 4. Prove the standalone application contract.
  **Context:** Unit-test the successful flow, every deterministic component,
  preserved component errors, order inspection, snapshot isolation, and a
  representative runnable Web-to-order example.
  **Outcome:** Added direct deterministic component tests, successful workflow
  and inspection coverage, duplicate/missing-order behavior, component error
  propagation, constructor invariants, snapshot isolation, and a runnable
  ordinary-Go example.

- [x] 5. Verify and close Task 005.
  **Context:** Review `go doc`, format, vet, run repeated and race tests, run the
  full repository suite, then mark Task 005 DONE after every check succeeds.
  **Outcome:** Reviewed the complete `go doc` surface; `gofmt -l` reports no
  files; `go vet ./...`, 25 repeated domain runs, `go test -race -count=1
  ./...`, and `go test -count=1 ./...` pass. Task 005 is marked DONE.

## Log

- 2026-09-08: Tasks 001 through 004 completed with all focused, race, vet, and
  full-suite checks passing.
- 2026-09-08: Selected Task 005 and recorded the concrete service composition,
  deterministic model, Web facade, persistence boundary, and Task 006 cutoff.
- 2026-09-08: Completed Task 005 after focused, repeated, race, vet, and full
  suite verification; no scope deviations or architectural issues found.
