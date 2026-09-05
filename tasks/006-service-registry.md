# Task 006 — Service registry

Status: TODO
Depends on: 005

## Goal
Introduce Grove's explicit service/method registry using stable application-owned IDs.

## Required reading
- `sdk/README.md`
- `sdk/DESIGN_PRINCIPLES.md`
- `sdk/SERVICE_MODEL.md`
- `sdk/EXAMPLE.md`

## Scope
- Define `ServiceID` and `MethodID` as explicit stable identifiers.
- Add explicit handler registration and deterministic resolution by `(ServiceID, MethodID)`.
- Reject duplicate registrations.
- Distinguish unknown service from unknown method.
- Keep the registry transport-independent.
- Register concrete Grove Shop handlers explicitly.

## Architectural constraints
- No generated code.
- No required Go interfaces for business services.
- No reflection-driven method discovery or dispatch.
- IDs must not be derived from package/type/function names or declaration order.
- Registration means what the local process can execute; do not conflate it with cluster placement state.

## Out of scope
Serialization, transport, placement, automatic discovery, routing, generated typed clients.

## Tests
- registration success
- duplicate service/method rejection
- unknown service
- unknown method
- deterministic dispatch to the expected concrete Grove Shop handler

## Done
The registry matches `sdk/SERVICE_MODEL.md` and `go test ./...` passes.