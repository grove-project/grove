# Storage

Grove storage is part of the application runtime, not a separate storage system bolted onto it.

The goal is not to build a smaller Ceph or compete with dedicated storage hardware on storage hardware alone. Grove's advantage is that the runtime already knows the application, service placement, cluster topology, workload lifecycle, available compute, memory, disks, and edge/cloud topology. Storage can use that context to make decisions a standalone storage system cannot.

> **Storage that understands the application because it runs inside the application runtime.**

## Use the hardware already in the cluster

Grovelets may already have local NVMe and spare memory. Grove can pool those resources into distributed storage instead of requiring a second fleet of dedicated storage nodes for every workload.

This changes the cost model. The savings are not primarily from cheaper SSDs; they come from reducing duplicated infrastructure: separate storage servers, control planes, networking, deployment, monitoring, upgrades, and operational tooling.

Dedicated storage remains appropriate when a workload requires properties Grove does not provide, such as extremely predictable fsync latency, specialized data services, regulatory storage features, or extreme IOPS. Grove applications should be able to mix Grove storage and external databases, object stores, SANs, and customer-provided storage naturally.

## Application-aware storage intent

A dedicated storage system mostly observes reads, writes, blocks, and objects. Grove can know what the data means operationally.

The SDK should allow developers to express intent rather than configure low-level storage mechanisms directly. Possible intents include:

- `Durable`: strong durability is required.
- `LatencySensitive`: optimize for fast local access.
- `Immutable`: data does not change after creation and is a good candidate for efficient coding or tiering.
- `Reconstructible`: the application can regenerate the data, allowing reduced or zero redundant durable copies.
- `Ephemeral`: data has a bounded useful lifetime.
- `EdgeLocal`: keep hot data near an edge workload while satisfying durability elsewhere.

Grove translates those requirements into placement, replication, caching, and coding decisions.

## Adaptive durability

Not every byte needs the same redundancy strategy.

Hot mutable data may use full replication. Warm data may use fewer replicas. Cold or immutable data may use erasure coding. Reconstructible data may need little or no redundant durable storage.

For example, three-way replication consumes 3x raw capacity. A `6+3` erasure-coded layout divides data into six data fragments and three recovery fragments. Any six fragments can reconstruct the original data, consuming roughly 1.5x raw capacity while tolerating loss of three fragments.

Erasure coding trades disk capacity for CPU, network traffic, and reconstruction complexity, so Grove should apply it selectively. A useful lifecycle is to keep newly written hot data replicated for low latency and convert cold immutable data to erasure coding later.

## Co-schedule compute and data

The same runtime controls service placement and data placement. Grove should optimize them together.

Data should preferentially live on, or be cached near, the Grovelets running the services that use it. When Grove moves a service it can prefetch hot data, establish a local cache or replica, preserve required durability elsewhere, and then activate the service.

Storage placement must respect the same topology and eligibility constraints as service placement. Locality is an optimization, never a reason to weaken durability or failure-domain guarantees.

## KV locality

Key-value storage can take locality further than placing whole storage shards near applications.

Grove can observe which services produce and consume individual keys or key ranges and use those access patterns as placement signals. Hot keys can be placed, replicated, or cached near their dominant producers and consumers.

For example:

```text
Service A on G1  --writes-->  customer/42
Service B on G2  --reads---->  customer/42

Observed access pattern:
  G1: heavy writes
  G2: heavy reads

Possible Grove decision:
  authoritative placement near G1
  hot read replica/cache near G2
  durability replicas in independent failure domains
```

This makes KV placement a joint scheduling problem rather than a fixed hash-partitioning problem. Grove may choose among moving compute toward data, moving data toward compute, or creating an additional local replica/cache depending on cost and constraints.

Placement should be adaptive but stable. Grove should use sustained access patterns, migration cost, minimum thresholds, and hysteresis so temporary traffic spikes do not cause keys to constantly move around the cluster.

The same mechanism is especially useful across cloud and edge boundaries. Keys heavily consumed by an edge-local service can remain hot at that edge while Grove maintains the required durable copies elsewhere.

## Distributed memory as an adaptive cache

Unused Grovelet RAM can participate in a cluster-wide adaptive cache. The preferred access path can become:

```text
local Grovelet RAM
        ↓ miss
local NVMe
        ↓ miss
remote Grove NVMe
        ↓
cold / erasure-coded storage
```

Because Grove controls scheduling, it can preferentially put cached data on the machine executing the relevant service rather than relying on a large remote storage cache.

## Edge storage

Dedicated enterprise storage at every customer edge is expensive and operationally difficult. A Grovelet can use ordinary local NVMe for low-latency edge access while Grove establishes the required durability across other eligible nodes or cloud infrastructure through outbound connectivity.

This follows Grove's general edge model: edge nodes remain part of the same logical cluster without requiring inbound Internet ports.

## Workload-aware background work

Encoding, repair, compaction, migration, and rebalancing consume CPU and network resources. Because Grove also schedules application compute, it can coordinate this background work with actual application pressure.

When application load is high, Grove can defer or throttle background storage work. When resources are idle, it can encode cold objects, reconstruct missing fragments, compact data, rebalance placement, or convert replicated data to a more capacity-efficient representation.

Spare compute capacity can therefore be traded for lower storage-capacity requirements.

## Failure and repair

When a disk or Grovelet fails, Grove knows both the affected data and the current application pressure across the cluster.

Repair decisions can account for disk capacity, CPU load, network load, service placement, topology, and failure domains. A node with plenty of free disk but a latency-sensitive workload may deliberately be avoided during reconstruction.

## Competitive position

Dedicated storage optimizes storage independently. Grove can optimize the application runtime as a whole:

```text
compute placement
       +
memory locality
       +
data placement
       +
durability
       +
caching
       +
network topology
       +
application lifecycle
```

Grove does not compete through better SSDs. It competes by co-scheduling compute, memory, storage, and data placement and by turning resources already present in the application cluster into reliable distributed storage.

For suitable workloads this can reduce hardware duplication, raw storage overhead, network traffic, and operational complexity while improving locality and common-case latency.
