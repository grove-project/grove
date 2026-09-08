# Plan: Implement the explicit service registry

## Goal

Complete Task 006 by adding Grove's transport-independent registry keyed by
application-owned `ServiceID` and `MethodID` values, with deterministic
resolution, duplicate rejection, distinct lookup failures, and handwritten
Grove Shop adapters that visibly call concrete business methods.

## Context

Tasks 001 through 005 are complete. Task 006 owns registration and resolution
only. Task 007 will introduce the application-facing invocation primitive, and
Task 008 will introduce Gob serialization. The registry therefore needs an
opaque in-process handler boundary now without adding routing, dispatch clients,
wire bytes, transport, placement, generated glue, or reflection.

### Key Files

- `tasks/006-service-registry.md` — registry behavior, Grove Shop integration,
  tests, and hard exclusions.
- `sdk/SERVICE_MODEL.md` and `sdk/EXAMPLE.md` — stable identifier and explicit
  registration contract.
- `registry.go` — root `grove` package identifiers, handler, registry, and
  typed lookup/registration failures.
- `registry_test.go` — registry workflow and runnable example.
- `demo/groveshop/registration.go` — application-owned IDs and handwritten
  adapters for the concrete Grove Shop services.
- `demo/groveshop/registration_test.go` — deterministic concrete dispatch
  proof without Grove invocation or serialization.
- `tasks/007-local-service-invocation.md` — boundary for the future call path.

### Decisions Made

- Put the SDK implementation in the module-root `grove` package so imports
  match the accepted examples (`github.com/grove-project/grove`).
- Define IDs as distinct unsigned integer types and never derive them from
  names, reflection metadata, or declaration order.
- Store `Handler func(context.Context, any) (any, error)`. This is an in-process
  registration contract, not a transport envelope; Task 008 will add explicit
  Gob bytes at the wire boundary.
- Make the zero value of `Registry` ready to use and protect its map so later
  runtime callers can safely resolve handlers while registration is quiescent.
- Return a typed `RegistryError` wrapping sentinel causes for duplicates, nil
  handlers, unknown services, and unknown methods. Duplicate attempts do not
  replace the original handler.
- Keep stable IDs in Grove Shop, where the application owns them. Implement
  separate explicit registration functions for Orders, Inventory, Payment, and
  Shipping; Web remains a presentation component rather than an invokable
  business service in this task.
- Handwritten adapters type-check the opaque request and call the concrete
  service directly. No code generation, reflection, or serialization is used.

## Sub-Tasks

- [x] 1. Select and bound Task 006.
  **Context:** Read the task, SDK service model/example, current Grove Shop API,
  and Task 007/008 boundaries.
  **Outcome:** Chose a root registry plus explicit application adapters, using
  opaque local values until serialization is introduced by its own task.

- [ ] 2. Implement stable IDs and deterministic registry resolution.
  **Context:** Add the identifier types, handler type, zero-value registry,
  registration, resolution, synchronization, and typed errors.
  **Outcome:** Pending.

- [ ] 3. Register concrete Grove Shop handlers.
  **Context:** Define application-owned constants and handwritten adapters for
  Create, Reserve, Charge, and Arrange using concrete request/result types.
  **Outcome:** Pending.

- [ ] 4. Prove registry and application adapter behavior.
  **Context:** Test successful registration/resolution, duplicate preservation,
  nil handler rejection, distinct unknown-service/method errors, invalid
  adapter inputs, and deterministic execution of each concrete service.
  **Outcome:** Pending.

- [ ] 5. Verify and close Task 006.
  **Context:** Review `go doc`, format, vet, run repeated and race tests, run the
  full repository suite, then mark Task 006 DONE after every check succeeds.
  **Outcome:** Pending.

## Log

- 2026-09-08: Tasks 001 through 005 completed with all focused, race, vet, and
  full-suite checks passing.
- 2026-09-08: Selected Task 006 and recorded registry ownership, opaque local
  handler semantics, typed failures, explicit Grove Shop glue, and the
  serialization/invocation boundaries.
