# Task 007 — Local service invocation

Status: TODO
Depends on: 006

## Goal
Allow Grove Shop services to invoke one another through Grove while both caller and callee run in one Grovlet.

## Required reading
- `sdk/INVOCATION.md`
- `sdk/SERVICE_MODEL.md`
- `sdk/EXAMPLE.md`

## Scope
- Introduce the application-facing Grove invocation primitive.
- Make service and method IDs explicit at the call boundary.
- Resolve local calls through the Grove routing/invocation layer into the local registry.
- Execute the registered concrete handler and propagate errors.
- Use Grove Shop `Orders -> Inventory` as the reference call.

## Architectural constraints
- The application call site must visibly be a Grove call; do not disguise distribution as a plain method call.
- This API must be suitable for reuse unchanged when the destination becomes remote in Task 010.
- Do not introduce generated clients, required service interfaces, or transparent proxies.
- Do not require NATS for a local call.

## Out of scope
Network transport, wire envelope, placement, automatic discovery, retries, failover.

## E2E
Start one real Grovlet via `grovetest`, run Grove Shop, invoke an Orders flow that calls Inventory through Grove, and assert the expected result.

## Done
Local invocation follows `sdk/INVOCATION.md` and `go test ./...` passes.