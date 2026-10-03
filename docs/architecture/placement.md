# Placement

Placement answers **where** work runs. Grove makes every placement decision
in one package, `internal/placement`. That package is pure: it has no storage,
transport or clock of its own. Production stores and transports its results
through `internal/controlplane` records and the `internal/systemnats` adapter,
and the `grovetest` TestCluster runs the same functions in memory.

Execution, meaning which process runs a component on a node, is a separate
decision. It is described in [process-model.md](process-model.md).

## Two levels of placement

| Level | Unit | Record | Decided by | Used for |
|---|---|---|---|---|
| **Handler placement** (primary) | one declared handler (`service.method`) | `controlplane.HandlerPlacement` in `GROVE_HANDLERS` | `placement.Place`, every reconcile tick | routing calls to declared handlers, exclusive ownership |
| **Service placement** | one component (service) | `controlplane.PlacementRecord` in `GROVE_PLACEMENT` | startup, then `placement.Recover` when a node fails | the HTTP ingress, and calls to handlers a component does not declare |

**Hosting** comes before both levels. By default every node runs every
component in its shared application runtime. An HTTP ingress component runs
on the node serving the cluster's ingress address. Every hosted component
publishes the handlers it declares, so handler placement places them on every
node that hosts them.

Service placement adds one more fact: which single node owns a component's
endpoint. Today that matters for the ingress, which can only listen on one
node, and for applications that do not declare handlers. The founding node
records every service as placed on itself. Joining nodes host the components
but claim nothing. Recovery moves a record only when its node fails.

## Who decides what

| Decision | Function | Production caller | Simulation caller |
|---|---|---|---|
| Nodes for each handler: every eligible node for automatic handlers, one stable owner for exclusive ones | `placement.Place` | `controlplane.PlanHandlerPlacements` | `grovetest.EverywherePolicy` |
| Fencing epoch of an exclusive handler | `placement.FencedEpoch` (`NextEpoch`) | `controlplane.PlanHandlerPlacements` | `TestCluster` reconcile |
| Which placement serves a call | `placement.Selector` | `systemnats` handler router | `TestCluster` node router |
| Which nodes are live, and which have failed | `placement.LiveNodes` and `placement.Member` | `runtime.clusterMembers` from the health view | `TestCluster` node state |
| Where a failed node's services move | `placement.Recover` (the `Coordinator`) | `runtime.selectRecoveries` | not modeled (see below) |
| Whether a node may claim an exclusive capability | `placement.DecideClaim` | `systemnats` `tryClaim` through `controlplane.DecideClaim` | `TestNode.AcquireExclusive` |
| Whether a held lease may still act | `placement.LeaseHolds` | `systemnats` `holds` through `controlplane.LeaseHolds` | `testLease.Held` |

`placement.Coordinator` is the lowest live node ID. `Place` uses the same rule
for a new exclusive owner, so when one node must act for the cluster, it is
the node that would also become a new exclusive owner.

## Flows

**Handler reconciliation.** Every node publishes the handlers its healthy
components declare. Each tick, every node runs
`controlplane.PlanHandlerPlacements` over the live nodes' registrations. That
calls `placement.Place` and `placement.FencedEpoch` and writes only the
records that differ, using compare-and-set. All nodes compute the same plan,
so a lost write race is harmless.

**Invocation.** A call to a declared handler resolves its handler placement,
restricted to live nodes, and `Selector` picks a target. Any other call
resolves the service placement record.

**Exclusive ownership.** Only the single placed owner may claim. A claim
waits until the previous holder's lease has gone unrenewed for the lease TTL,
as measured from when this node first saw it, unless that holder released it.
The claim is stored at the placement's epoch. The holder renews it every tick
and loses it as soon as the placement moves or another holder replaces it.
An owner that is cut off stops acting when its own TTL runs out, which is
before anyone else may claim.

**Node failure.** Health marks a node unavailable after it misses heartbeats.
A node that was seen healthy before then counts as failed. Handler
reconciliation drops the failed node from every handler placement. Separately,
`placement.Recover` assigns every service recorded on the failed node to the
coordinator. The coordinator starts those components locally before the
control plane has a leader, so the ingress comes back quickly. Once the
control plane is available, it hands each record over with compare-and-set.

**Rollout.** An upgrade rewrites service placement records to point at the
candidate artifact's endpoint. It does not choose nodes. The scripted demo
in `runtime/application.go` still names its candidate nodes itself (Goal 2).

## Simulation

`grovetest.TestCluster` runs `Place`, `FencedEpoch`, `Selector`,
`DecideClaim` and `LeaseHolds` from this package, so its handler placement,
epochs and lease semantics are production's. It simulates only the store,
failure detection and the clock. When production would block waiting out a
previous holder's lease, the TestCluster advances its clock to that moment.

`TestTestClusterMatchesProductionPlacement` (`grovetest/conformance_test.go`)
replays one scenario through both the TestCluster and
`controlplane.PlanHandlerPlacements`, and requires identical placements and
epochs after every step. The scenario covers start, owner crash, takeover,
restart, isolation, reconnect, and an exclusive handler deleted and recreated.

The TestCluster does not model service placement records or recovery yet.

## Guards

- `internal/placement/boundary_test.go`:
  - `TestPlacementIsPure` allows only the standard library and the SDK root.
  - `TestPlacementDecisionsHaveOneOwner` fails if `internal/controlplane`,
    `runtime` or `grovetest` stops delegating its decisions to this package.
- `internal/controlplane/boundary_test.go` keeps the control-plane domain free
  of NATS.
- `grovetest/conformance_test.go` keeps the simulation equal to production.
