---
id: TUI-001
status: todo
outcome: groveshop-demo
depends-on:
  - CLUSTER-001
---

# Shared live Cluster view

## Goal
Every GroveShop terminal should feel like a live window into the same cluster.

## Scope
Expose a first-class TUI Cluster view backed only by authoritative shared control-plane state. Show cluster health, active/candidate build identities, membership, per-node build, service placement, rollout/rollback state, and recent cluster activity.

## Acceptance
Open Cluster in three terminals. Join, failure, recovery, placement movement, rollout, and rollback appear consistently in all surviving views without manual refresh.

## Tests
Run multiple independent read-model observers and verify they converge on the same transitions.
