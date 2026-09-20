---
id: CLUSTER-001
status: todo
outcome: groveshop-demo
depends-on: []
---

# Interactive same-binary discovery and join

## Goal
Starting another copy of the same Grove application should naturally extend the existing cluster.

## Scope
- Give a Grove application a stable application identity and a distinct build identity.
- Discover reachable clusters for the same application identity on startup.
- If none exists, bootstrap the first node.
- If a same-build cluster exists, make **Join** the primary TUI action.
- Keep creating a second cluster for the same application as an explicit advanced action.
- Never require the operator to enter NATS addresses, PIDs, or Grove implementation details.

## Acceptance
Starting the same GroveShop binary in three terminals creates one three-node cluster with only TUI confirmation on nodes two and three.

## Tests
Deterministic E2E covering first-node bootstrap, two same-build joins, unrelated-application isolation, and shared membership convergence.
