---
id: groveshop-demo
status: in-progress
---

# Killer GroveShop Demo

## Outcome

A developer or operator can start GroveShop as a single application binary, grow it into a live multi-node Grove cluster, observe and manipulate that cluster from the built-in TUI, survive failures, roll out a newly built binary without changing the ingress endpoint, and debug distributed application execution through an experience that still feels like one application.

## Success criteria

- Start the first GroveShop process as the first cluster node.
- Start additional copies of the same binary and have them discover the existing cluster and suggest joining it.
- Show three nodes participating in one live cluster.
- Place GroveShop services across different nodes and make placement visible.
- Create an order successfully.
- Kill a node and visibly show affected services recovering on available nodes.
- Create another order successfully after recovery.
- Show the same live cluster state from TUIs attached to different nodes.
- Use runtime-observed application flow to suggest useful debugging/hot-path targets.
- Attach ordinary DAP/Delve debugging sessions to services on different nodes through Grove.
- Change GroveShop code, build a new binary, start it, detect that it is a new version of the same application, and offer a rollout.
- Show rollout progress consistently in the connected TUIs.
- Preserve the same ingress endpoint throughout rollout.
- Treat embedded configuration changes as another immutable-binary rollout through the same flow.
- Run a standalone load-test binary during the demo.
- Give the load tester its own TUI showing current load and visible effects while cluster actions occur.

## Proof

The canonical proof is the end-to-end GroveShop demo. Individual task acceptance tests should compose into this flow rather than creating isolated demonstrations.

## Scope discipline

Work that does not materially help prove this outcome should normally remain in NEXT/LATER or belong to another outcome.
