# Task 005 — Grove Shop reference application

Status: TODO
Depends on: 001

## Goal
Create the deterministic Grove Shop application domain reused by all later E2Es.

## Required reading
- `IMPLEMENTATION_PLAN.md`
- `sdk/DESIGN_PRINCIPLES.md`
- `sdk/EXAMPLE.md`
- `demo/README.md`
- `demo/ARCHITECTURE.md`

## Scope
Create the logical application components:
- `Web`
- `Orders`
- `Inventory`
- `Payment`
- `Shipping`

At this task, keep them as normal Go application code without distributed runtime behavior.

The minimum business flow is:

```text
Created -> Reserved -> Paid -> Shipping -> Completed
```

`Orders` coordinates Inventory, Payment, and Shipping through ordinary Go code. Keep the concrete implementations directly navigable and unit-testable. Do not introduce generated stubs, required service interfaces, reflection-driven dispatch, or Grove-specific abstractions into the business methods.

The application must be deterministic and intentionally small.

Define the Web-facing business model/API shape needed to create and inspect orders, but do not implement Grove status polling or distributed serving yet unless required by the smallest clean local app structure.

## Out of scope
- Grove service registry.
- Grove invocation API.
- Remote invocation.
- NATS transport.
- Cluster membership or placement.
- Deployment artifact creation.
- Embedded customer configuration.
- Upgrade/rollback behavior.
- Live Grove cluster-status polling.
- Debugging/DAP.

## Tests
Unit-test Grove Shop independently of Grove:
- successful order progression,
- deterministic Inventory reservation,
- deterministic Payment success,
- deterministic Shipping result,
- clear propagation of component errors.

## Done
- Grove Shop is the permanent reference app.
- Business services are ordinary Go types/methods and directly unit-testable without Grove.
- No generated code or required framework interfaces are introduced.
- `go test ./...` passes.