# Task 005 — Grove Shop reference application

Status: TODO
Depends on: 001

## Goal
Create the deterministic Grove Shop application domain reused by all later E2Es.

## Required reading
- `IMPLEMENTATION_PLAN.md`
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

`Orders` coordinates Inventory, Payment, and Shipping through plain Go-callable abstractions that can later be wired to Grove invocation without changing the business semantics.

The application must be deterministic and intentionally small.

Define the Web-facing business model/API shape needed to create and inspect orders, but do not implement Grove status polling or distributed serving yet unless required by the smallest clean local app structure.

## Out of scope
- Grove service registry.
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
- Grove Shop replaces Greeter/Workflow as the permanent reference app.
- Business logic is usable independently of Grove runtime plumbing.
- `go test ./...` passes.