---
id: CLUSTER-002
status: done
outcome: groveshop-demo
depends-on:
  - CLUSTER-001
---

# Elastic control-plane replication

## Goal

A Grove cluster remains usable while it grows from one node to three, and a
graceful `q` shrinks the JetStream replica set before the departing embedded
NATS server exits.

## Human contract

```text
Start node-1        1 member   R1 control state   5/5 services healthy
Join node-2         2 members  R2 control state   ready
Join node-3         3 members  R3 control state   ready
Press q on node-3   2 members  R2 control state   ready
```

An abruptly killed node does not authorize a replica reduction. Grove keeps
the previous replica set and reports degraded/unavailable state rather than
risking split brain or discarding authoritative state.

## Scope

- Create new authoritative JetStream/KV buckets with one bootstrap replica.
- Keep a second embedded JetStream metadata peer with the bootstrap Grove node
  because a clustered NATS metadata group cannot operate with one peer;
  subsequent Grove nodes add one metadata peer each.
- Give the bootstrap pair one logical-node uniqueness tag so replicated control
  state cannot place two copies on the bootstrap Grove node.
- Start all five Grove Shop services on the bootstrap node so a one-node
  cluster can serve orders before any other node joins.
- Reconcile each control-state bucket to `min(active members, 3)` replicas.
- Count a gracefully leaving member as inactive for replica reconciliation.
- Complete replica reconciliation while the leaving node is still running.
- Remove graceful NATS metadata peers through the system-account API and wait
  for control streams to be current elsewhere before their servers stop.
- Preserve the configured replica set after abrupt process or network loss.

## Out of scope

- Automatic quorum recovery after abrupt failure or partition.
- Operator-driven force removal of failed NATS peers.
- Replica counts above three and topology-aware replica placement.

## Acceptance

Starting the configured Grove Shop artifact reaches an available control plane
and serves an order with all five services on one node. It remains available
through `node-1 -> node-1,node-2 -> node-1 -> node-1,node-3`, reaches three
replicas after a third live node joins, and returns to two replicas before a
graceful third-node exit completes.

## Tests

Add deterministic System NATS integration coverage for R1 -> R2 -> R3 -> R2
membership transitions, prove a fresh single logical node has a usable
JetStream metadata leader, and extend the real-process configured-artifact E2E
to cover one-node service, second-node replacement, and subsequent R3 -> R2.
