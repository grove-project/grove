package files_test

// One test per outcome of the Grove Files delivery plan (Goals 1-8). Each
// runs real disks and production code; only the catalog store, the network
// and the clock are in-process.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/files"
)

// Goal 1: an application gets an ordinary local path, mutates it with
// standard file APIs, and closes and reopens it on the same node.
func TestGoal1LocalFileRoundTrip(t *testing.T) {
	cluster := newTestCluster(t)
	node := cluster.start("node-a")
	ctx := t.Context()

	var store grove.FileStore = node
	file, err := store.Open(ctx, "state/notes.txt", grove.WithDurability(grove.Local), grove.Replicas(1))
	if err != nil {
		t.Fatal(err)
	}
	if !file.CurrentVersion().IsZero() {
		t.Fatalf("new file has version %+v", file.CurrentVersion())
	}
	if got := readLocal(t, file.LocalPath()); len(got) != 0 {
		t.Fatalf("new file has %d bytes", len(got))
	}
	writeLocal(t, file.LocalPath(), []byte("first"))
	version, err := file.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version.Generation != 1 || version.SHA256 != sum([]byte("first")) || version.Size != 5 {
		t.Fatalf("synced version %+v", version)
	}
	// Unsynced local edits survive close and reopen on the same node.
	writeLocal(t, file.LocalPath(), []byte("first, then more"))
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Sync(ctx); !errors.Is(err, grove.ErrFileClosed) {
		t.Fatalf("sync after close: %v; want ErrFileClosed", err)
	}
	reopened, err := store.Open(ctx, "state/notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if reopened.CurrentVersion().ID != version.ID {
		t.Fatalf("reopened at %s; want %s", reopened.CurrentVersion().ID, version.ID)
	}
	if got := string(readLocal(t, reopened.LocalPath())); got != "first, then more" {
		t.Fatalf("reopened local file = %q", got)
	}
	if filepath.Base(reopened.LocalPath()) != "notes.txt" {
		t.Fatalf("local path %s does not keep the file name", reopened.LocalPath())
	}
}

func TestPathsStayInsideTheManagedRoot(t *testing.T) {
	node := newTestCluster(t).start("node-a")
	for _, path := range []string{"", "/etc/passwd", "../escape", "a/../../escape", ".", `a\b`, "a\x00b"} {
		if _, err := node.Open(t.Context(), path); !errors.Is(err, grove.ErrInvalidPath) {
			t.Errorf("Open(%q) = %v; want ErrInvalidPath", path, err)
		}
	}
	if clean, err := files.CleanPath("state//./db/../app.db"); err != nil || clean != "state/app.db" {
		t.Errorf("CleanPath = %q, %v", clean, err)
	}
}

func TestFilesWithoutARuntimeAreUnavailable(t *testing.T) {
	if _, err := grove.Files(context.Background()).Open(context.Background(), "a"); !errors.Is(err, grove.ErrFilesUnavailable) {
		t.Fatalf("Open without a runtime: %v", err)
	}
	node := newTestCluster(t).start("node-a")
	ctx := grove.WithFileStore(context.Background(), node)
	if _, err := grove.Files(ctx).Open(ctx, "a", grove.WithDurability(grove.Local)); err != nil {
		t.Fatalf("Open through the context store: %v", err)
	}
}

// Goal 2: Sync produces an immutable, checksummed version, and a failed sync
// never replaces the previous committed version.
func TestGoal2CommittedVersionsAreImmutable(t *testing.T) {
	cluster := newTestCluster(t)
	a := cluster.start("node-a")
	ctx := t.Context()
	file, err := a.AcquireHandle(ctx, "state/db", grove.WithDurability(grove.Local), grove.Replicas(1))
	if err != nil {
		t.Fatal(err)
	}
	writeLocal(t, file.LocalPath(), []byte("v1"))
	v1, err := file.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	writeLocal(t, file.LocalPath(), []byte("v2 is longer"))
	if got := cluster.replicaBytes("node-a", "state/db", v1.ID); string(got) != "v1" {
		t.Fatalf("committed v1 changed in place: %q", got)
	}
	v2, err := file.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v2.Generation <= v1.Generation || v2.ID == v1.ID {
		t.Fatalf("v2 %+v does not follow v1 %+v", v2, v1)
	}
	record := cluster.record("state/db")
	if record.Current.ID != v2.ID || len(record.History) != 1 || record.History[0].ID != v1.ID {
		t.Fatalf("catalog current %v history %v", record.Current, record.History)
	}
}

// Goal 2: faults before commit (a snapshot that cannot be taken, replicas that
// cannot be reached, a writer killed mid-transfer) leave the previous version
// current, and recovery never observes a partial version.
func TestGoal2FailedSyncKeepsThePreviousVersion(t *testing.T) {
	cluster := newTestCluster(t)
	a, b, _ := cluster.start("node-a"), cluster.start("node-b"), cluster.start("node-c")
	ctx := t.Context()
	file, err := a.AcquireHandle(ctx, "state/db", grove.Replicas(3), grove.WithDurability(grove.Replicated))
	if err != nil {
		t.Fatal(err)
	}
	writeLocal(t, file.LocalPath(), []byte("committed"))
	v1, err := file.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Before snapshot: the local file is gone.
	if err := os.Remove(file.LocalPath()); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Sync(ctx); err == nil {
		t.Fatal("sync without a local file succeeded")
	}
	// During transfer: one replica is unreachable, so Replicated(3) fails.
	writeLocal(t, file.LocalPath(), []byte("never committed"))
	cluster.stop("node-c")
	if _, err := file.Sync(ctx); !errors.Is(err, grove.ErrDurability) {
		t.Fatalf("sync with a replica down: %v; want ErrDurability", err)
	}
	record := cluster.record("state/db")
	if record.Current.ID != v1.ID {
		t.Fatalf("current = %s after failed syncs; want %s", record.Current.ID, v1.ID)
	}
	if record.Staging == nil || record.Staging.ID == v1.ID {
		t.Fatalf("failed version not left staging: %+v", record.Staging)
	}
	staged := *record.Staging

	// A new owner never promotes the staged version.
	cluster.stop("node-a")
	owned := cluster.takeOver(b, "state/db")
	if owned.CurrentVersion().ID != v1.ID || string(readLocal(t, owned.LocalPath())) != "committed" {
		t.Fatalf("new owner materialized %s %q", owned.CurrentVersion().ID, readLocal(t, owned.LocalPath()))
	}
	if record := cluster.record("state/db"); record.Staging != nil {
		t.Fatalf("staging %s survived the ownership change", record.Staging.ID)
	}
	// The next version takes a fresh generation: the abandoned one is never reused.
	writeLocal(t, owned.LocalPath(), []byte("after failover"))
	v2, err := owned.Sync(ctx)
	if !errors.Is(err, grove.ErrDurability) {
		// node-a and node-c are down, so Replicated(3) cannot commit.
		t.Fatalf("sync with two nodes down: %v", err)
	}
	cluster.start("node-a")
	cluster.start("node-c")
	v2, err = owned.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v2.Generation <= staged.Generation {
		t.Fatalf("v2 generation %d reuses abandoned generation %d", v2.Generation, staged.Generation)
	}
}

// Goal 2: bytes cut off mid-transfer never become a version.
func TestGoal2PartialTransferIsNeverInstalled(t *testing.T) {
	dir := t.TempDir()
	disk, err := files.OpenDisk(dir)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("0123456789")
	local := files.LocalVersion{
		Path: "x", FileID: files.FileIDFor("x"),
		Version: files.VersionMeta{ID: files.VersionIDFor(1, sum(data)), Generation: 1, Size: 10, SHA256: sum(data)},
	}
	calls := 0
	err = disk.Receive(local, func(offset int64) ([]byte, error) {
		calls++
		if calls == 1 {
			return data[:4], nil
		}
		return nil, errors.New("writer killed")
	})
	if err == nil {
		t.Fatal("interrupted transfer installed")
	}
	if _, ok, _ := disk.Version(local.FileID, local.Version.ID); ok {
		t.Fatal("partial version is visible")
	}
	// Bytes that do not match the checksum are rejected the same way.
	tampered := bytes.Clone(data)
	tampered[0] = 'X'
	sent := false
	err = disk.Receive(local, func(int64) ([]byte, error) {
		if sent {
			return nil, io.EOF
		}
		sent = true
		return tampered, nil
	})
	if !errors.Is(err, files.ErrIntegrity) {
		t.Fatalf("tampered transfer: %v; want ErrIntegrity", err)
	}
	if _, ok, _ := disk.Version(local.FileID, local.Version.ID); ok {
		t.Fatal("tampered version is visible")
	}
	// A restart removes whatever staging a crash left behind.
	if err := os.WriteFile(filepath.Join(dir, "objects", local.FileID, "staging", "transfer-crash"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := files.OpenDisk(dir); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "objects", local.FileID, "staging")); len(entries) != 0 {
		t.Fatalf("staging left after restart: %v", entries)
	}
}

// Goal 3: a synced version is persisted and verified on peer nodes, so the
// bytes survive the writer.
func TestGoal3SyncReplicatesToPeers(t *testing.T) {
	cluster := newTestCluster(t)
	a := cluster.start("node-a")
	cluster.start("node-b")
	cluster.start("node-c")
	ctx := t.Context()
	file, err := a.AcquireHandle(ctx, "models/weights.bin", grove.Replicas(3))
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("grove"), 200_000) // 1 MB: several transfer chunks
	writeLocal(t, file.LocalPath(), data)
	version, err := file.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.WaitReplicated(ctx, version); err != nil {
		t.Fatal(err)
	}
	cluster.stop("node-a")
	for _, node := range []string{"node-b", "node-c"} {
		got := cluster.replicaBytes(node, "models/weights.bin", version.ID)
		if sum(got) != version.SHA256 || int64(len(got)) != version.Size {
			t.Fatalf("%s holds %d bytes sha256 %s; want %d bytes %s", node, len(got), sum(got), version.Size, version.SHA256)
		}
	}
	record := cluster.record("models/weights.bin")
	if holders := record.Holders(version.ID); len(holders) != 3 {
		t.Fatalf("catalog holders %v; want all three nodes", holders)
	}
}

// Goal 3: Quorum commits on a majority and replicates the rest afterwards;
// WaitReplicated reports when every replica holds the version.
func TestGoal3QuorumCommitsOnAMajority(t *testing.T) {
	cluster := newTestCluster(t)
	a := cluster.start("node-a")
	cluster.start("node-b")
	cluster.start("node-c")
	ctx := t.Context()
	file, err := a.AcquireHandle(ctx, "state/db", grove.Replicas(3), grove.WithDurability(grove.Quorum))
	if err != nil {
		t.Fatal(err)
	}
	cluster.stop("node-c")
	writeLocal(t, file.LocalPath(), []byte("quorum"))
	version, err := file.Sync(ctx)
	if err != nil {
		t.Fatalf("quorum sync with one of three down: %v", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := file.WaitReplicated(waitCtx, version); err == nil {
		t.Fatal("WaitReplicated returned with a replica down")
	}
	cluster.start("node-c")
	if err := a.Reconcile(ctx); err != nil { // the owner tops replicas up
		t.Fatal(err)
	}
	if err := file.WaitReplicated(ctx, version); err != nil {
		t.Fatal(err)
	}
}

// Goal 4: only one node can own a file, and ownership is fenced by epoch so
// a partitioned former owner cannot publish after another node took over.
func TestGoal4ExclusiveFencedOwnership(t *testing.T) {
	cluster := newTestCluster(t)
	a, b := cluster.start("node-a"), cluster.start("node-b")
	ctx := t.Context()
	if _, err := a.Open(ctx, "state/db"); err != nil { // establishes the cluster identity
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	handles := make([]*files.Handle, 2)
	for i, node := range []*files.Node{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handles[i], _, _ = node.TryAcquire(ctx, "state/db", grove.Replicas(2), grove.WithDurability(grove.Quorum))
		}()
	}
	wg.Wait()
	if (handles[0] == nil) == (handles[1] == nil) {
		t.Fatalf("racing Acquire: node-a won=%t node-b won=%t; want exactly one", handles[0] != nil, handles[1] != nil)
	}
	owner, other := handles[0], b
	if owner == nil {
		owner, other = handles[1], a
	}
	ownerID := owner.Owner()
	if _, err := other.AcquireHandle(timeout(t, 20*time.Millisecond), "state/db"); !errors.Is(err, grove.ErrFileOwned) {
		t.Fatalf("second Acquire: %v; want ErrFileOwned", err)
	}
	writeLocal(t, owner.LocalPath(), []byte("v1"))
	v1, err := owner.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// An unowned writer is fenced too.
	opened, err := other.OpenHandle(ctx, "state/db")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := opened.Sync(ctx); !errors.Is(err, grove.ErrFileOwned) {
		t.Fatalf("Open+Sync on an owned file: %v; want ErrFileOwned", err)
	}

	// The owner renews and the other node observes the renewal. Then the
	// owner is partitioned: it stops acting before the other may take over.
	cluster.nodes[ownerID].Renew(ctx)
	if _, done, _ := other.TryAcquire(ctx, "state/db"); done {
		t.Fatal("took over a renewed lease")
	}
	cluster.partition(ownerID, true)
	cluster.clock.Advance(testLeaseTTL / 2)
	cluster.nodes[ownerID].Renew(ctx) // fails: the catalog is unreachable
	if !owner.Held() {
		t.Fatal("owner gave up within its TTL")
	}
	if _, done, _ := other.TryAcquire(ctx, "state/db"); done {
		t.Fatal("took over before the silent lease aged a full TTL")
	}
	cluster.clock.Advance(testLeaseTTL / 2)
	if owner.Held() {
		t.Fatal("partitioned owner still believes it holds the file a full TTL after its last renewal")
	}
	promoted, err := other.AcquireHandle(ctx, "state/db")
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Epoch() != owner.Epoch()+1 {
		t.Fatalf("promoted epoch %d; want %d", promoted.Epoch(), owner.Epoch()+1)
	}
	if promoted.CurrentVersion().ID != v1.ID {
		t.Fatalf("promoted at %s; want %s", promoted.CurrentVersion().ID, v1.ID)
	}

	// Heal: the stale owner's Sync is rejected even if it renews its clock view.
	cluster.partition(ownerID, false)
	writeLocal(t, owner.LocalPath(), []byte("stale owner"))
	if _, err := owner.Sync(ctx); !errors.Is(err, grove.ErrOwnershipLost) {
		t.Fatalf("stale owner Sync: %v; want ErrOwnershipLost", err)
	}
	// Renewal cannot resurrect it either.
	cluster.nodes[ownerID].Renew(ctx)
	if _, err := owner.Sync(ctx); !errors.Is(err, grove.ErrOwnershipLost) {
		t.Fatalf("stale owner Sync after renew: %v; want ErrOwnershipLost", err)
	}
	if record := cluster.record("state/db"); record.Current.ID != v1.ID || record.Lease.Holder != other.ID() {
		t.Fatalf("catalog after stale sync: current %s owner %s", record.Current.ID, record.Lease.Holder)
	}
	if cluster.countEvents(files.EventPromoted) != 1 || cluster.countEvents(files.EventOwnershipLost) == 0 {
		t.Fatalf("events: %d promotions, %d losses", cluster.countEvents(files.EventPromoted), cluster.countEvents(files.EventOwnershipLost))
	}
}

func TestReleasedOwnershipIsImmediatelyAvailable(t *testing.T) {
	cluster := newTestCluster(t)
	a, b := cluster.start("node-a"), cluster.start("node-b")
	ctx := t.Context()
	owned, err := a.AcquireHandle(ctx, "state/db", grove.Replicas(2))
	if err != nil {
		t.Fatal(err)
	}
	if err := owned.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.AcquireHandle(timeout(t, time.Second), "state/db"); err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
}

// Goal 5: after the owner is lost, the node the handler moves to becomes
// owner from the last committed version, fetching it if it has no copy.
func TestGoal5OwnerFailover(t *testing.T) {
	cluster := newTestCluster(t)
	a := cluster.start("node-a")
	cluster.start("node-b")
	d := cluster.start("node-d") // holds no replica: Replicas(2) picks node-b
	ctx := t.Context()
	file, err := a.AcquireHandle(ctx, "state/orders.db", grove.Replicas(2), grove.WithDurability(grove.Replicated))
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("orders: 1, 2, 3")
	writeLocal(t, file.LocalPath(), data)
	committed, err := file.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	writeLocal(t, file.LocalPath(), []byte("orders: 1, 2, 3, 4 (never synced)"))
	cluster.stop("node-a")
	owned := cluster.takeOver(d, "state/orders.db")
	if owned.CurrentVersion() != committed {
		t.Fatalf("failover version %+v; want %+v", owned.CurrentVersion(), committed)
	}
	if got := readLocal(t, owned.LocalPath()); !bytes.Equal(got, data) {
		t.Fatalf("failover content %q; want the last committed %q", got, data)
	}
	// The new owner continues: it syncs on top of the committed version.
	writeLocal(t, owned.LocalPath(), []byte("orders: 1, 2, 3, 5"))
	next, err := owned.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if next.Generation != committed.Generation+1 || next.Source != "node-d" {
		t.Fatalf("next version %+v", next)
	}
}

// Goal 5: a node that cannot get a verified copy of the committed version
// never becomes owner, so it can never publish from stale or empty state.
func TestGoal5NoPromotionWithoutTheCommittedVersion(t *testing.T) {
	cluster := newTestCluster(t)
	a := cluster.start("node-a")
	cluster.start("node-b")
	d := cluster.start("node-d")
	ctx := t.Context()
	file, err := a.AcquireHandle(ctx, "state/db", grove.Replicas(2))
	if err != nil {
		t.Fatal(err)
	}
	writeLocal(t, file.LocalPath(), []byte("only on a and b"))
	if _, err := file.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	cluster.stop("node-a")
	cluster.stop("node-b")
	d.TryAcquire(ctx, "state/db") // observes node-a's lease
	cluster.clock.Advance(testLeaseTTL)
	if _, err := d.AcquireHandle(ctx, "state/db"); !errors.Is(err, files.ErrNoEligibleReplica) {
		t.Fatalf("promotion without a reachable replica: %v; want ErrNoEligibleReplica", err)
	}
	if record := cluster.record("state/db"); record.Owned() {
		t.Fatalf("failed promotion left %s owning the file", record.Lease.Holder)
	}
}

// Goal 6: after every process stops and the catalog is lost, one prior
// replica restarts alone and reconstructs the exact committed versions.
func TestGoal6FullClusterColdRecovery(t *testing.T) {
	cluster := newTestCluster(t)
	a := cluster.start("node-a")
	cluster.start("node-b")
	cluster.start("node-c")
	ctx := t.Context()
	want := map[string]grove.Version{}
	contents := map[string][]byte{}
	for i, path := range []string{"state/orders.db", "state/inventory.db"} {
		file, err := a.AcquireHandle(ctx, path, grove.Replicas(3))
		if err != nil {
			t.Fatal(err)
		}
		for v := 1; v <= 3+i; v++ {
			contents[path] = []byte(fmt.Sprintf("%s version %d", path, v))
			writeLocal(t, file.LocalPath(), contents[path])
			if want[path], err = file.Sync(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if err := file.WaitReplicated(ctx, want[path]); err != nil {
			t.Fatal(err)
		}
	}
	for _, node := range []string{"node-a", "node-b", "node-c"} {
		cluster.stop(node)
	}
	cluster.wipeCatalog()

	b := cluster.start("node-b")
	if err := b.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	for path, version := range want {
		record := cluster.record(path)
		if record.Current == nil || record.Current.Public() != version {
			t.Fatalf("%s recovered as %+v; want %+v", path, record.Current, version)
		}
		if len(record.History) == 0 {
			t.Fatalf("%s recovered without its retained history", path)
		}
		opened, err := b.OpenHandle(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		if got := readLocal(t, opened.LocalPath()); !bytes.Equal(got, contents[path]) || sum(got) != version.SHA256 {
			t.Fatalf("%s recovered content %q", path, got)
		}
	}
	if cluster.countEvents(files.EventBootstrap) != 2 {
		t.Fatalf("%d bootstrap events; want 2", cluster.countEvents(files.EventBootstrap))
	}

	// The others join and reconcile; they never replace what node-b recovered.
	for _, id := range []string{"node-a", "node-c"} {
		node := cluster.start(id)
		if err := node.Recover(ctx); err != nil {
			t.Fatal(err)
		}
		if err := node.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for path, version := range want {
		if holders := cluster.record(path).Holders(version.ID); len(holders) != 3 {
			t.Fatalf("%s holders after rejoin: %v", path, holders)
		}
	}
}

// Goal 6: a disk from another cluster lineage never bootstraps or joins.
func TestGoal6ForeignLineageIsQuarantined(t *testing.T) {
	cluster := newTestCluster(t)
	a := cluster.start("node-a")
	ctx := t.Context()
	file, err := a.OpenHandle(ctx, "state/db", grove.WithDurability(grove.Local), grove.Replicas(1))
	if err != nil {
		t.Fatal(err)
	}
	writeLocal(t, file.LocalPath(), []byte("old cluster"))
	if _, err := file.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	cluster.stop("node-a")
	cluster.wipeCatalog()
	// A different cluster forms first.
	if err := cluster.catalog.CreateCluster(ctx, files.ClusterRecord{Format: 1, ClusterID: "another-cluster"}); err != nil {
		t.Fatal(err)
	}
	a = cluster.start("node-a")
	if err := a.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if records, _ := cluster.catalog.List(ctx); len(records) != 0 {
		t.Fatalf("foreign disk published %d records", len(records))
	}
	if quarantined, _ := a.Disk().Quarantined(); len(quarantined) == 0 {
		t.Fatal("foreign files were not quarantined")
	}
	if cluster.countEvents(files.EventLineage) != 1 {
		t.Fatal("no lineage mismatch event")
	}
}

// Goal 7: a node that missed updates rejoins, converges to the current
// version, and never publishes its stale one.
func TestGoal7StaleNodeReconciles(t *testing.T) {
	cluster := newTestCluster(t)
	a := cluster.start("node-a")
	cluster.start("node-b")
	cluster.start("node-c")
	ctx := t.Context()
	file, err := a.AcquireHandle(ctx, "state/db", grove.Replicas(3), grove.WithDurability(grove.Quorum))
	if err != nil {
		t.Fatal(err)
	}
	var versions []grove.Version
	syncVersion := func(i int) {
		writeLocal(t, file.LocalPath(), []byte(fmt.Sprintf("v%d", i)))
		version, err := file.Sync(ctx)
		if err != nil {
			t.Fatal(err)
		}
		versions = append(versions, version)
	}
	for i := 1; i <= 5; i++ {
		syncVersion(i)
	}
	if err := file.WaitReplicated(ctx, versions[4]); err != nil {
		t.Fatal(err)
	}
	cluster.stop("node-c")
	for i := 6; i <= 10; i++ {
		syncVersion(i)
	}
	v5, v10 := versions[4], versions[9]

	c := cluster.start("node-c")
	if err := c.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if record := cluster.record("state/db"); record.Current.ID != v10.ID {
		t.Fatalf("node-c's restart published %s; current must stay %s", record.Current.ID, v10.ID)
	}
	if local, _ := c.Disk().Current(files.FileIDFor("state/db")); local != v5.ID {
		t.Fatalf("node-c restarted at %s; want stale %s", local, v5.ID)
	}
	if err := c.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if local, _ := c.Disk().Current(files.FileIDFor("state/db")); local != v10.ID {
		t.Fatalf("node-c converged to %s; want %s", local, v10.ID)
	}
	if got := cluster.replicaBytes("node-c", "state/db", v10.ID); string(got) != "v10" {
		t.Fatalf("node-c v10 content %q", got)
	}
	if record := cluster.record("state/db"); !contains(record.Holders(v10.ID), "node-c") {
		t.Fatalf("node-c is not a verified holder: %v", record.ReplicaSet)
	}
	// Superseded versions beyond the retained history are pruned.
	local, _ := c.Disk().Versions(files.FileIDFor("state/db"))
	if len(local) > 4 {
		t.Fatalf("node-c keeps %d versions; want current plus at most 3", len(local))
	}
}

// Goal 8: a replica corrupted on disk is detected, quarantined, never
// promoted, and repaired from a healthy replica.
func TestGoal8CorruptReplicaIsQuarantinedAndRepaired(t *testing.T) {
	cluster := newTestCluster(t)
	a := cluster.start("node-a")
	cluster.start("node-b")
	cluster.start("node-c")
	ctx := t.Context()
	file, err := a.AcquireHandle(ctx, "state/db", grove.Replicas(3))
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("healthy bytes")
	writeLocal(t, file.LocalPath(), data)
	version, err := file.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.WaitReplicated(ctx, version); err != nil {
		t.Fatal(err)
	}
	blob := filepath.Join(cluster.dirs["node-b"], "objects", files.FileIDFor("state/db"), "versions", version.ID+".blob")
	corrupt(t, blob, []byte("rotten bytes!"))

	cluster.stop("node-b")
	b := cluster.start("node-b") // restart verifies every stored version
	if _, ok, _ := b.Disk().Version(files.FileIDFor("state/db"), version.ID); ok {
		t.Fatal("corrupt version still listed after restart")
	}
	if cluster.countEvents(files.EventCorrupt) == 0 {
		t.Fatal("no corruption event")
	}
	if err := b.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if got := cluster.replicaBytes("node-b", "state/db", version.ID); !bytes.Equal(got, data) {
		t.Fatalf("repaired replica %q; want %q", got, data)
	}

	// Corrupted after startup: promotion verifies before materializing and
	// refetches instead of serving rotten bytes.
	corrupt(t, blob, []byte("rotten again"))
	cluster.stop("node-a")
	owned := cluster.takeOver(b, "state/db")
	if got := readLocal(t, owned.LocalPath()); !bytes.Equal(got, data) {
		t.Fatalf("promoted with %q; want %q", got, data)
	}
}

// Goal 8: corrupt bytes served by a peer are rejected by the receiver.
func TestGoal8CorruptSourceIsRejected(t *testing.T) {
	cluster := newTestCluster(t)
	a := cluster.start("node-a")
	cluster.start("node-b")
	ctx := t.Context()
	file, err := a.AcquireHandle(ctx, "state/db", grove.Replicas(1), grove.WithDurability(grove.Local))
	if err != nil {
		t.Fatal(err)
	}
	writeLocal(t, file.LocalPath(), []byte("source"))
	version, err := file.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	corrupt(t, filepath.Join(cluster.dirs["node-a"], "objects", files.FileIDFor("state/db"), "versions", version.ID+".blob"), []byte("SOURCE"))
	record := cluster.record("state/db")
	err = cluster.nodes["node-b"].HandleReplicate(ctx, files.ReplicateRequest{
		ClusterID: record.ClusterID, Version: *record.Current, Source: "node-a",
		File: files.LocalFile{Path: "state/db", FileID: record.FileID, Replicas: 1},
	})
	if !errors.Is(err, files.ErrIntegrity) {
		t.Fatalf("replicate from a corrupt source: %v; want ErrIntegrity", err)
	}
	if _, ok, _ := cluster.nodes["node-b"].Disk().Version(record.FileID, version.ID); ok {
		t.Fatal("corrupt bytes were installed")
	}
}

func corrupt(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func timeout(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), d)
	t.Cleanup(cancel)
	return ctx
}

func contains(nodes []string, node string) bool {
	for _, n := range nodes {
		if n == node {
			return true
		}
	}
	return false
}
