---
id: DEBUG-001
status: todo
outcome: groveshop-demo
depends-on: []
---

# Runtime Grove call-flow capture

## Goal
Let an operator understand which meaningful application operations actually participated in a request.

## Scope
Record request-scoped execution of registered Grove operations and `grove.Call` edges. Preserve semantic service/operation identity and current placement so the TUI can reconstruct the last real application flow. Do not use profiling heuristics for the primary flow.

## Acceptance
After one GroveShop order, Grove can return the executed application-level operation graph with the node/worker placement that handled each operation.

## Tests
Create a deterministic order and assert the captured semantic call graph and placement correspond to the execution.
