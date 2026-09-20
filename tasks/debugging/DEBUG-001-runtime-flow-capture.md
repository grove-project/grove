---
id: DEBUG-001
status: todo
outcome: groveshop-demo
depends-on: []
---

# Runtime Grove call-flow capture

## Goal
Let an operator understand which meaningful application operations actually participated in a request, using Grove's own semantic knowledge rather than profiling heuristics.

## Scope
Record request-scoped execution of registered Grove operations and `grove.Call` edges. Preserve semantic service/operation identity, trace/request identity, timing, current node/worker placement, and active artifact/build identity so the TUI can reconstruct a real application flow and navigate every operation back to the exact source that produced it.

The primary graph is made from registered Grove operations. Raw Go profiling may later enrich the experience, but it must not be required to discover the main application flow.

The captured model must support both directions required by the debugger:
- observed flow -> registered operation -> source/debug target;
- paused source/debug target -> originating observed flow.

## Read-model shape

A captured order should be representable as a semantic graph similar to:

```text
POST /orders

web.PlaceOrder                 node-1 / worker-1
      |
      v
orders.CreateOrder             node-2 / worker-3
      |
      +----> inventory.Reserve node-1 / worker-2
      |
      +----> payments.Charge   node-3 / worker-7
```

Node/worker identities are observations, not stable breakpoint identities.

## Acceptance
After one GroveShop order, Grove can return the executed application-level operation graph with:
- registered service + operation identity;
- parent/child `grove.Call` edges;
- node and worker that executed each operation;
- active artifact/build identity;
- enough source/symbol identity for DEBUG-002 to open the exact implementation;
- a stable flow/trace identity that DEBUG-003 can return to while paused.

A subsequent execution after service relocation must show the new placement without changing semantic operation identities.

## Tests
Create deterministic orders and assert:
- the captured semantic call graph corresponds to execution;
- placement corresponds to the workers that actually handled the calls;
- the same semantic operations survive placement changes;
- source/debug-target metadata resolves against the active artifact;
- no profiling heuristic is needed to identify the registered Grove operations.
