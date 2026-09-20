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

## TUI contract

Stable cluster:

```text
 GROVESHOP / CLUSTER                                  HEALTHY ●

 Nodes                         Services
 ───────────────────────       ───────────────────────────────
 node-1  ●  a82f19c            web          node-1
 node-2  ●  a82f19c            orders       node-2
 node-3  ●  a82f19c            inventory    node-3
                               payment      node-2
                               shipping     node-3

 Build
 ─────────────────────────────────────────────────────────────
 Active       a82f19c

 Activity
 ─────────────────────────────────────────────────────────────
 10:42:03  node-3 joined
 10:42:04  shipping → node-3
 10:42:04  cluster healthy

 [n] Nodes  [s] Services  [d] Deployments  [q] Back
```

During recovery, the transition should be understandable without reading logs:

```text
 GROVESHOP / CLUSTER                               RECOVERING ◐

 node-2  ×  unavailable

 orders       node-2  ───────>  node-1   STARTING
 payment      node-2  ───────>  node-3   HEALTHY

 Activity
 10:47:11  node-2 missed health threshold
 10:47:11  payment relocating → node-3
 10:47:12  payment healthy on node-3
 10:47:12  orders relocating → node-1
```

During rollout, every connected terminal renders the same shared rollout:

```text
 GROVESHOP / CLUSTER                              ROLLING OUT ◐

 Active       a82f19c
 Candidate    c41de72

 Services     3 / 5 on candidate
 ████████████████████████░░░░░░░░░░░░  60%

 web          c41de72   HEALTHY
 orders       c41de72   HEALTHY
 inventory    c41de72   HEALTHY
 payment      a82f19c   ACTIVE
 shipping     a82f19c   ACTIVE
```

## Scope
Expose a first-class TUI Cluster view backed only by authoritative shared control-plane state. Show cluster health, active/candidate build identities, membership, per-node build, service placement, rollout/rollback state, and recent cluster activity.

## Acceptance
Open Cluster in three terminals. Join, failure, recovery, placement movement, rollout, and rollback appear consistently in all surviving views without manual refresh. Exact layout may adapt to terminal size, but the information hierarchy and transitions above are required.

## Tests
Run multiple independent read-model observers and verify they converge on the same transitions.
