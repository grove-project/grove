# Grove Files

**Use an ordinary local file. Tell Grove when it is safe. Grove keeps it across nodes.**

```go
file, err := grove.Files(ctx).Acquire(ctx, "state/orders.db",
    grove.Replicas(3),
    grove.WithDurability(grove.Quorum),
)
if err != nil {
    return err
}
defer file.Release(ctx)

db, err := sql.Open("sqlite", file.LocalPath()) // any library, any format

// ... the application writes normally ...

v, err := file.Sync(ctx) // publish this consistent state
```

`LocalPath` is a real file on the node. Grove never replicates writes as they happen and never reads the file while you write it. `Sync` snapshots the file, gives the snapshot an immutable version with a SHA-256 checksum, and copies it to other nodes. It returns only once the version is committed.

## What Sync promises

| Durability | `Sync` returns once | Use for | Risk |
|---|---|---|---|
| `grove.Replicated` (default) | every one of `Replicas(n)` nodes, the writer included, persisted and verified the version | general durable files | a sync fails while fewer than n nodes are up |
| `grove.Quorum` | a majority of the n nodes persisted and verified it | state whose acknowledged loss must be rare | higher sync latency than Local |
| `grove.Local` | the writer persisted and verified it | caches and rebuildable state | losing the writer's disk loses the latest version |

When `Sync` cannot reach its durability it returns `grove.ErrDurability`, and the previous committed version stays current. A failed or interrupted sync is never visible: readers, new owners and recovery see the previous version or the new one, never a partial one.

`WaitReplicated(ctx, v)` waits for the rest: every configured replica holds `v`. With `Quorum` or `Local`, Grove keeps copying after `Sync` returns.

## Ownership and failover

`Acquire` gives one node write ownership. Another node's `Acquire` waits until that ownership ends: released, or its node silent for a full lease. The new owner materializes the **last committed version**:

```text
node-a  Acquire state/orders.db      epoch 1
node-a  Sync                          v7 committed on node-a, node-b
node-a  writes more, then dies        (unsynced writes are lost)
node-b  Acquire state/orders.db      epoch 2, materializes v7
node-a  comes back, calls Sync        ErrOwnershipLost: epoch 1 is fenced
```

Ownership follows your handler placement. Acquire the file in an exclusive handler and Grove moves both together.

`Open` reads a file without ownership. `Sync` through an opened file works only while nobody owns the file and nobody committed since you opened it (`ErrFileChanged`). Use it for replace-style files.

## Your safe point

Grove treats contents as opaque bytes, so the application decides when the file is consistent. For SQLite, checkpoint the write-ahead log and hold off writers while Grove snapshots the file. [`acceptance/sqlite`](../acceptance/sqlite/sqlitefiles.go) does this in about 40 lines:

```go
v, err := sqlitefiles.Sync(ctx, db, file)
```

Failover resumes from the last synced database, not from every SQLite write.

## Test it

Business code takes the store from its context, so tests attach one. `grovetest.FilesCluster` runs real disks on in-process nodes that you can kill and restart:

```go
cluster := grovetest.NewFilesCluster(t, "node-a", "node-b", "node-c")
v, _ := saveOrders(cluster.Context("node-a"), "1,2,3")

cluster.Kill("node-a")
owned, _ := cluster.Store("node-b").Acquire(ctx, "state/orders.txt") // v

cluster.StopAll()
cluster.LoseMetadata()   // no live cluster metadata survives
cluster.Start("node-c")  // rebuilds the catalog from its disk
```

Without a Grove runtime in the context, every call returns `grove.ErrFilesUnavailable`.

## Not in V1

Grove Files is not a distributed filesystem: there is no shared file handle across nodes, no block-level replication, no multi-writer merge and no encryption at rest. Transfers copy whole versions. See [the architecture](../docs/architecture/files.md) for recovery rules and remaining limits.
