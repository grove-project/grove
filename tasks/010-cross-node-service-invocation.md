# Task 010 — Cross-node service invocation

Status: TODO
Depends on: 007, 009

## Goal
Make the same Grove Shop application-facing invocation API work when the callee is on another Grovlet.

## Required reading
- `sdk/INVOCATION.md`
- `sdk/SERVICE_MODEL.md`
- `sdk/SERIALIZATION.md`
- `sdk/EXAMPLE.md`

## Scope
- Use `Orders -> Inventory` as the reference call.
- Allow an explicit destination Grovlet for this stage before automatic placement/discovery exists.
- Route the existing Grove invocation through serialization, System NATS transport, remote registry dispatch, and response/error propagation.
- Keep the application call site unchanged from Task 007 except for routing/destination information supplied through Grove.

## Architectural constraints
- Do not introduce a separate remote client API.
- Do not generate RPC stubs.
- Do not make application code publish directly to NATS.
- Local and remote paths must remain two routing outcomes of the same Grove invocation model.
- Remote registry dispatch remains explicit `(ServiceID, MethodID)` dispatch, not reflection.

## Out of scope
Automatic placement/discovery, retries, failover, load balancing, custom consensus.

## E2E
Start 2 real Grovlets; place Orders on A and Inventory on B; invoke the same Orders flow used for local invocation and assert the expected cross-process result.

## Done
The application-facing call model matches `sdk/INVOCATION.md`, the cross-node path uses System NATS underneath, and `go test ./...` passes.