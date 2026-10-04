package sqlitefiles_test

// The Grove Files final acceptance scenario with SQLite as the local file
// consumer: populate a database on one node, sync a safe point, lose the
// owner, continue on another node, stop the whole cluster with its metadata,
// recover from one replica, and prove no partial or corrupt version became
// current.

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grove-project/grove"
	sqlitefiles "github.com/grove-project/grove/acceptance/sqlite"
	"github.com/grove-project/grove/grovetest"
)

const dbPath = "state/demo.db"

func TestSQLiteFinalAcceptance(t *testing.T) {
	// 1. Start a 3-node Grove application.
	cluster := grovetest.NewFilesCluster(t, "node-a", "node-b", "node-c")

	// 2. Acquire state/demo.db on node A and populate it through SQLite.
	file, db := acquireDB(t, cluster, "node-a")
	mustExec(t, db, "CREATE TABLE orders (id INTEGER PRIMARY KEY, item TEXT NOT NULL)")
	insertOrders(t, db, 1, 100)

	// 3. Create a safe checkpoint and sync with quorum durability.
	synced, err := sqlitefiles.Sync(ctx(cluster, "node-a"), db, file)
	if err != nil {
		t.Fatal(err)
	}
	// Rows written after the safe point are not part of any version.
	insertOrders(t, db, 101, 110)

	// 4. Node B and node C hold valid committed replicas.
	cluster.WaitHolders(dbPath, 3)
	for _, node := range []string{"node-b", "node-c"} {
		replica := replicaCopy(t, cluster, node, synced)
		assertDatabase(t, replica, 100)
	}

	// 5. Kill A. Node B takes over from exactly the last committed version.
	cluster.Kill("node-a")
	db.Close()
	file, db = acquireDB(t, cluster, "node-b")
	if file.CurrentVersion() != synced {
		t.Fatalf("node-b resumed at %+v; want %+v", file.CurrentVersion(), synced)
	}
	assertDatabase(t, db, 100)

	// 6. More changes, synced on the new owner.
	insertOrders(t, db, 101, 250)
	latest, err := sqlitefiles.Sync(ctx(cluster, "node-b"), db, file)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Generation <= synced.Generation {
		t.Fatalf("latest %+v does not follow %+v", latest, synced)
	}
	waitFor(t, "two replicas of the latest version", func() bool { return len(cluster.Holders(dbPath)) >= 2 })
	holders := cluster.Holders(dbPath)
	db.Close()

	// 7. Stop the entire cluster; no live metadata survives.
	cluster.StopAll()
	cluster.LoseMetadata()

	// 8-9. Start one node that holds the latest replica. It reconstructs the
	// catalog and exposes the same committed data.
	first := holders[0]
	cluster.Start(first)
	waitFor(t, "recovered catalog", func() bool { return cluster.Current(dbPath) == latest })
	recovered := openDB(t, cluster, first)
	assertDatabase(t, recovered, 250)
	recovered.Close()

	// 10. Start the other nodes; they reconcile automatically.
	for _, node := range []string{"node-a", "node-b", "node-c"} {
		if node != first {
			cluster.Start(node)
		}
	}
	waitFor(t, "all nodes reconciled", func() bool { return len(cluster.Holders(dbPath)) == 3 })

	// 11. Every node holds the same verified database, and no partial or
	// corrupt version became current.
	if current := cluster.Current(dbPath); current != latest {
		t.Fatalf("current %+v; want %+v", current, latest)
	}
	for _, node := range []string{"node-a", "node-b", "node-c"} {
		assertDatabase(t, replicaCopy(t, cluster, node, latest), 250)
		db := openDB(t, cluster, node)
		assertDatabase(t, db, 250)
		db.Close()
	}
}

// Writers keep inserting while the application syncs repeatedly and the
// owner is killed at an arbitrary point. The surviving database is always a
// consistent prefix of the writes as of one sync.
func TestSQLiteSyncUnderConcurrentWritesAndFailover(t *testing.T) {
	cluster := grovetest.NewFilesCluster(t, "node-a", "node-b", "node-c")
	file, db := acquireDB(t, cluster, "node-a")
	mustExec(t, db, "CREATE TABLE orders (id INTEGER PRIMARY KEY, item TEXT NOT NULL)")

	var written atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for id := int64(1); ; id++ {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := db.Exec("INSERT INTO orders (id, item) VALUES (?, ?)", id, item(int(id))); err != nil {
				return
			}
			written.Store(id)
		}
	}()
	var floors []int64
	for range 5 {
		time.Sleep(20 * time.Millisecond)
		floor := written.Load()
		if _, err := sqlitefiles.Sync(ctx(cluster, "node-a"), db, file); err != nil {
			t.Fatal(err)
		}
		floors = append(floors, floor)
	}
	ceiling := written.Load()
	cluster.Kill("node-a")
	close(stop)
	wg.Wait()
	db.Close()

	file, db = acquireDB(t, cluster, "node-b")
	defer db.Close()
	assertIntegrity(t, db)
	var count, maxID int64
	if err := db.QueryRow("SELECT count(*), coalesce(max(id), 0) FROM orders").Scan(&count, &maxID); err != nil {
		t.Fatal(err)
	}
	if count != maxID {
		t.Fatalf("recovered %d rows up to id %d; want a contiguous prefix", count, maxID)
	}
	if count < floors[len(floors)-1] || count > ceiling {
		t.Fatalf("recovered %d rows; the last sync covered at least %d and at most %d", count, floors[len(floors)-1], ceiling)
	}
}

func ctx(cluster *grovetest.FilesCluster, node string) context.Context {
	return cluster.Context(node)
}

func acquireDB(t *testing.T, cluster *grovetest.FilesCluster, node string) (grove.OwnedFile, *sql.DB) {
	t.Helper()
	c, cancel := context.WithTimeout(cluster.Context(node), 20*time.Second)
	defer cancel()
	file, err := grove.Files(c).Acquire(c, dbPath, grove.Replicas(3), grove.WithDurability(grove.Quorum))
	if err != nil {
		t.Fatalf("%s acquire %s: %v", node, dbPath, err)
	}
	db, err := sqlitefiles.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	return file, db
}

func openDB(t *testing.T, cluster *grovetest.FilesCluster, node string) *sql.DB {
	t.Helper()
	c := cluster.Context(node)
	file, err := grove.Files(c).Open(c, dbPath)
	if err != nil {
		t.Fatalf("%s open %s: %v", node, dbPath, err)
	}
	db, err := sqlitefiles.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// replicaCopy opens a copy of the committed blob node stores for version,
// straight from its disk.
func replicaCopy(t *testing.T, cluster *grovetest.FilesCluster, node string, version grove.Version) *sql.DB {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(cluster.Dir(node), "objects", "*", "versions", version.ID+".blob"))
	if len(matches) != 1 {
		t.Fatalf("%s holds %d blobs of %s", node, len(matches), version.ID)
	}
	source, err := os.Open(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	copyPath := filepath.Join(t.TempDir(), "replica.db")
	target, err := os.Create(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(target, source); err != nil {
		t.Fatal(err)
	}
	target.Close()
	db, err := sql.Open("sqlite", "file:"+copyPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func assertDatabase(t *testing.T, db *sql.DB, rows int) {
	t.Helper()
	assertIntegrity(t, db)
	var ids []int
	result, err := db.Query("SELECT id, item FROM orders ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	for result.Next() {
		var id int
		var got string
		if err := result.Scan(&id, &got); err != nil {
			t.Fatal(err)
		}
		if got != item(id) {
			t.Fatalf("order %d = %q; want %q", id, got, item(id))
		}
		ids = append(ids, id)
	}
	if len(ids) != rows || !slices.IsSorted(ids) || (rows > 0 && ids[rows-1] != rows) {
		t.Fatalf("database has %d orders; want exactly 1..%d", len(ids), rows)
	}
}

func assertIntegrity(t *testing.T, db *sql.DB) {
	t.Helper()
	var check string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil {
		t.Fatal(err)
	}
	if check != "ok" {
		t.Fatalf("integrity_check = %q", check)
	}
}

func insertOrders(t *testing.T, db *sql.DB, from, to int) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for id := from; id <= to; id++ {
		if _, err := tx.Exec("INSERT INTO orders (id, item) VALUES (?, ?)", id, item(id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func item(id int) string { return "coffee-" + string(rune('a'+id%26)) }

func mustExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
