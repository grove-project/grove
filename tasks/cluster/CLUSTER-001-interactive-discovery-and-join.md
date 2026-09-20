---
id: CLUSTER-001
status: todo
outcome: groveshop-demo
depends-on: []
---

# Interactive same-binary discovery and join

## Goal
Starting another copy of the same Grove application should naturally extend the existing cluster.

## TUI contract

First process, when no compatible cluster exists:

```text
┌─ GroveShop ─────────────────────────────────────────────┐
│ No GroveShop cluster discovered                        │
│                                                        │
│ › Start new cluster                                    │
│                                                        │
│ Application   GroveShop                                │
│ Build         a82f19c                                  │
│                                                        │
│ Enter select                                           │
└────────────────────────────────────────────────────────┘
```

Second/third process running the exact same build:

```text
┌─ GroveShop ─────────────────────────────────────────────┐
│ Grove cluster discovered                               │
│                                                        │
│ Cluster       groveshop-local                          │
│ Nodes         1                                        │
│ Build         a82f19c                                  │
│ Status        Healthy                                  │
│                                                        │
│ › Join cluster                                         │
│   Start a separate cluster…                            │
│                                                        │
│ Enter select   ↑↓ navigate                             │
└────────────────────────────────────────────────────────┘
```

After confirmation, transition directly into the normal GroveShop TUI with **Cluster** available. Do not expose NATS URLs, seed addresses, PIDs, or internal transport details.

## Scope
- Give a Grove application a stable application identity and a distinct build identity.
- Discover reachable clusters for the same application identity on startup.
- If none exists, bootstrap the first node.
- If a same-build cluster exists, make **Join cluster** the primary TUI action.
- Keep creating a second cluster for the same application as an explicit advanced action.
- Never require the operator to enter NATS addresses, PIDs, or Grove implementation details.

## Acceptance
Starting the same GroveShop binary in three terminals creates one three-node cluster with only TUI confirmation on nodes two and three. The screens above are the behavioral contract; exact spacing may adapt to terminal size.

## Tests
Deterministic E2E covering first-node bootstrap, two same-build joins, unrelated-application isolation, and shared membership convergence.
