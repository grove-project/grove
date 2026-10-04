# Grove Files

Grove Files are cluster-managed local files: an application writes an
ordinary local file, and Grove versions, replicates, owns and recovers it.
The developer contract is in [sdk/FILES.md](../../sdk/FILES.md). This page
explains how the runtime keeps it.

## Where each part lives

| Concern | Owner | Notes |
|---|---|---|
| SDK contract: `grove.Files`, `File`, `OwnedFile`, `Durability` | root `grove` package | standard library only; names no NATS concept |
| Lifecycle: materialize, snapshot, sync, ownership, replicate, recover, reconcile, prune | `internal/files` (`Node`) | reaches metadata through the `Catalog` port and other nodes through the `Peers` port |
| Node disk layout and integrity | `internal/files` (`Disk`) | every write is temp file, fsync, rename |
| Ownership policy | `internal/placement` (`DecideClaim`, `LeaseHolds`) | the same fencing policy as exclusive handlers |
| Catalog store | `internal/systemnats` (`FilesCatalog`) | JetStream KV bucket `GROVE_FILES`, created on the first file write at the control-state replica count and grown with membership |
| Transfer | `internal/systemnats` (`ServeFiles`, `FilesPeers`) | NATS request/reply, at most 512 KiB per message |
| Application process access | `internal/systemnats` (`ServeLocalFiles`, `RemoteFileStore`) | the Grovlet holds handles; the app process gets the local path |
| Grovlet wiring | `runtime/files.go` | `<runtime-dir>/files`, liveness from the health view |
| In-process test cluster | `grovetest.FilesCluster` | production `internal/files` over an in-memory catalog and network |

Import guards in the root `boundary_test.go` keep the SDK on the standard
library, keep `internal/files` off NATS and on the placement lease policy.

## Records

The catalog holds one record per logical path (key `file.<file-id>`, where
the file ID is a hash of the path) and the cluster identity (key `cluster`).
Every record is versioned (`format: 1`); readers reject a newer format.

```text
Record   path, file_id, cluster_id, desired_replicas, durability_mode
         owner {holder, epoch, beat, released}   the fenced write lease
         generations                              last generation handed out
         current_version, staging_version, history[]
         replicas[] {node_id, version_id, state: verified | stale}
Version  id = <generation:016x>-<sha256 prefix>, generation, size, sha256,
         created_at, source_node
```

Every change is a compare-and-set of the whole record, so a commit and an
ownership change can never interleave.

## Sync

1. The owner's `Sync` copies the work file into `staging/`. Replication reads
   only that snapshot.
2. It hashes the snapshot and stages the next generation in the record,
   checking it still owns the file at its epoch.
3. It installs the snapshot as its own verified version.
4. It asks replica nodes to pull the version. Each writes to staging, checks
   the size and SHA-256, fsyncs and renames it into `versions/`.
5. Once the durability's copies are verified, a second compare-and-set
   checks the epoch again and commits: `current_version` moves, the old one
   joins bounded history, the staging slot clears.
6. Holders are told the version is committed before `Sync` returns. A late
   replica's copy is recorded in the background.

A failure before step 5 leaves the previous version current. A staged
version is abandoned when the next owner claims the file, and its generation
is never reused.

## Ownership

File ownership is a lease in the record, decided by `placement.DecideClaim`
and checked with `placement.LeaseHolds`. A claimant waits until it has seen
the lease unchanged for a full TTL on its own clock, then claims epoch + 1.
An owner stops acting once it has not renewed for a TTL, which is never
later than a claimant's takeover. Both commit steps check the epoch, so a
partitioned former owner cannot publish even if its clock is wrong.

A new owner must get a verified copy of the current version before it
materializes the file: from its own disk, or from a live holder. With no
reachable holder `Acquire` fails with `ErrNoEligibleReplica` and releases the
lease, so no node ever becomes owner from stale state.

## Node disk

```text
<runtime-dir>/files/
  catalog/cluster.json          the lineage of every local file
  objects/<file-id>/file.json   path and options
  objects/<file-id>/versions/   <version>.blob (read-only) and <version>.meta
  objects/<file-id>/current     newest version this node knows is committed
  objects/<file-id>/work/       the writable file and anything the app keeps beside it
  objects/<file-id>/staging/    snapshots and transfers; removed on start
  quarantine/                   corrupt, foreign, diverged or replaced state
```

On start a node rehashes every stored version and quarantines any that no
longer match. Before materializing or promoting a version it verifies it
again. Bytes received from a peer are never installed unless they match.

## Recovery and reconciliation

**Every node stopped, catalog lost.** A starting node with local files and no
catalog record of them publishes the newest version it knows is committed
(its `current` pointer), with its older versions as history. The first node
to do so is the recovery source. This is a recovery model, not a guarantee:
if that node missed later commits, those later versions are not recovered,
and copies of them on other nodes are quarantined as diverged.

**Lineage.** The catalog stores a cluster ID and every disk stores the one it
belongs to. A disk from another lineage is quarantined, never merged.

**Stale nodes.** Every second each node compares the files it stores with the
catalog, fetches a current version it lacks or holds corrupt, records itself
as a holder, and prunes versions outside the retained history. Restarting
never publishes a node's stale version over a live record.

**Repair.** The owner, or the first live holder when the owner is gone,
copies the current version to more live nodes until it has its configured
replica count.

## Observability

Each Grovlet emits `file_*` lifecycle events: `ownership_acquired`,
`ownership_promoted`, `ownership_lost`, `ownership_released`,
`sync_started`, `sync_committed`, `sync_failed`, `replica_transferred`,
`replica_corrupt`, `recovery_bootstrap`, `reconcile_converged`,
`reconcile_diverged` and `lineage_mismatch`. `files.Node.Status` reports per
file the current version, owner and epoch, desired and healthy replicas,
the local version and the last sync result.

## Tests

| Layer | What it proves |
|---|---|
| `internal/files` | Goals 1-8 one test each, on real disks with an in-memory catalog, network and clock: local round trip, immutable versions and failed syncs, replication, fenced ownership, failover, cold recovery, stale rejoin, corruption |
| `internal/systemnats` | the same over a real NATS server: KV compare-and-set races, chunked transfer, recovery against a fresh server, application-process handles |
| `grovetest` | `FilesCluster` failover and cold restart through `grove.Files(ctx)` |
| `runtime` | real Grovlet processes: sync, SIGKILL of the owner, takeover, every node killed and System NATS state deleted, catalog rebuilt from disks |
| `acceptance/sqlite` | the final acceptance scenario with SQLite, `PRAGMA integrity_check` after failover and cold recovery, and sync under concurrent writes |

## Remaining limits

- Transfers copy whole versions; there is no chunk resume across restarts or delta transfer.
- Retention keeps three previous versions; quarantine is never garbage-collected.
- File status is not yet shown in the console, TUI or inspection surface, and there are no metrics yet.
- Replica choice does not yet consider disk capacity, zones or nodes without persistent storage.
- There is no delete API.
- Encryption at rest is not provided.
