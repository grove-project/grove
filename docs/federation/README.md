# Grove Federation

Status: Concept / evolving design  
Purpose: Define how independent Grove application clusters discover and communicate with each other without sharing their private cluster control plane.

## Core model

A Grove application binary owns its own Grove cluster. Multiple different application binaries may run side by side on the same hosts, VMs, Kubernetes cluster, or broader infrastructure while remaining completely independent Grove clusters.

```text
Host / Kubernetes cluster

├─ GroveShop cluster
│  ├─ GroveShop binary
│  └─ private Cluster NATS
│
├─ Billing cluster
│  ├─ Billing binary
│  └─ private Cluster NATS
│
└─ Fraud cluster
   ├─ Fraud binary
   └─ private Cluster NATS
```

The hierarchy is:

```text
binary → application cluster → optional federation of application clusters
```

Federation is additive. A Grove cluster must remain fully functional without participating in a federation.

## Separate NATS domains

Grove deliberately separates intra-cluster messaging from cross-cluster communication.

### Cluster NATS

Each Grove cluster has its own private embedded NATS deployment. It is internal infrastructure for that application cluster and may carry:

- Grove control-plane messages
- cluster membership and health
- placement and worker coordination
- replicated cluster state
- service RPC inside the application cluster
- application data-plane facilities owned by that cluster

Other Grove applications do not connect to this NATS domain.

### Federation NATS

Cross-cluster communication uses a separate shared NATS deployment: **Federation NATS**.

Federation NATS carries only traffic intentionally exposed across application boundaries, such as:

- cluster and application discovery
- exported service discovery
- cross-cluster RPC
- explicitly exported events or streams
- federation membership and routing metadata

The architectural rule is:

> **Cluster NATS is private infrastructure for one Grove application cluster. Federation NATS is the shared communication fabric between independent Grove application clusters.**

These domains remain separate even when applications happen to share the same machine or Kubernetes cluster.

## One Federation NATS node per cluster

A Grove application cluster may optionally contribute **at most one Federation NATS server node** to the shared federation.

Federation membership therefore scales with the number of participating application clusters, not with the number of Grovlets, workers, replicas, or machines inside any one cluster.

```text
GroveShop cluster                 Billing cluster
┌────────────────────┐           ┌────────────────────┐
│ private Cluster    │           │ private Cluster    │
│ NATS               │           │ NATS               │
│                    │           │                    │
│ ★ Federation node A├───────────┤★ Federation node B │
└────────────────────┘           └────────────────────┘
             \                         /
              \                       /
               └──── Federation ─────┘
                         │
               ┌─────────┴─────────┐
               │★ Federation node C│
               │ private Cluster   │
               │ NATS              │
               └───────────────────┘
                    Fraud cluster
```

This prevents application scale from distorting federation topology. A 30-node GroveShop cluster and a 3-node Billing cluster each contribute at most one Federation NATS member.

## Federation-node placement and failover

The Federation NATS member belongs logically to the application cluster but runs on one eligible Grove node at a time.

```text
GroveShop cluster

node 1 ─ workers
node 2 ─ workers + ★ federation member
node 3 ─ workers
node 4 ─ workers
```

If the hosting node fails, Grove relocates the federation member to another eligible node:

```text
node 1 ─ workers
node 3 ─ workers + ★ federation member
node 4 ─ workers
```

The federation member is independent from the cluster's application-worker count and should not be duplicated simply because the application scales horizontally.

## Discovery and exported services

Federation provides automatic discovery of participating Grove applications and only the services they intentionally export.

Conceptually:

```text
Cluster NATS                        Federation NATS

orders service ── export ────────► groveshop/orders
billing service ◄── discovery ──── groveshop/orders
```

A service existing inside a Grove cluster does not imply that it is visible outside that cluster.

Discovery also does not imply authorization. A cluster may discover an exported service but still require an explicit capability or policy before it may invoke that service.

This aligns Federation NATS with Grove's security model: application/cluster identities authenticate to the federation, and cross-cluster access can be capability-scoped without exposing private Cluster NATS traffic.

## Bootstrap

Federation should not require a separately managed control plane.

A possible bootstrap flow is:

```text
Cluster A starts
→ no federation discovered
→ contributes Federation NATS node A
→ federation = {A}

Cluster B starts
→ discovers federation
→ contributes Federation NATS node B
→ federation = {A, B}

Cluster C starts
→ joins and contributes node C
→ federation = {A, B, C}
```

The exact discovery mechanism is implementation-defined, but the resulting model should preserve the same invariant: one private Cluster NATS domain per Grove application cluster and zero or one contributed Federation NATS node per participating cluster.

## Failure behavior

Loss of one application cluster may remove that cluster's contributed federation member, but it must not collapse the private control planes of the remaining Grove clusters.

Federation availability and persistence should follow normal NATS quorum rules when JetStream or replicated federation state is used. Applications should not assume that all participating clusters are always reachable.

## Architectural invariants

- Different application binaries may run as independent Grove clusters side by side.
- Each Grove cluster owns a private embedded Cluster NATS domain.
- Cross-cluster communication never requires another application to join that private NATS domain.
- Federation NATS is a distinct shared communication domain.
- A Grove cluster may participate in zero or one federation.
- A participating Grove cluster contributes at most one Federation NATS server node.
- Federation-node count is independent of Grovlet, worker, replica, and machine count inside an application cluster.
- Grove may relocate a cluster's federation member when its hosting node fails.
- Only explicitly exported services/data become visible across the federation.
- Discovery and authorization remain separate concerns.
- A Grove cluster remains independently operable when federation is disabled or unavailable.
