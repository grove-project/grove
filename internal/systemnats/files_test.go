package systemnats_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/files"
	"github.com/grove-project/grove/internal/systemnats"
)

// filesNode is one node's file service on a real System NATS server: the
// catalog is JetStream KV and transfers are NATS request/reply.
type filesNode struct {
	id        string
	node      *files.Node
	transport *systemnats.Transport
	stop      context.CancelFunc
}

type filesCluster struct {
	t      *testing.T
	server *systemnats.Server
	dirs   map[string]string
	live   []string
}

func startFilesServer(t *testing.T) *systemnats.Server {
	t.Helper()
	server, err := systemnats.StartClusterServer(t.Context(), systemnats.ClusterConfig{
		Name: "files-1", Host: "127.0.0.1", RouteHost: "127.0.0.1", JetStreamStoreDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func newFilesCluster(t *testing.T, ids ...string) *filesCluster {
	t.Helper()
	cluster := &filesCluster{t: t, server: startFilesServer(t), dirs: make(map[string]string), live: ids}
	t.Cleanup(func() { cluster.server.Shutdown() })
	for _, id := range ids {
		cluster.dirs[id] = filepath.Join(t.TempDir(), id)
	}
	return cluster
}

func (c *filesCluster) start(id string) *filesNode {
	c.t.Helper()
	ctx, cancel := context.WithCancel(c.t.Context())
	transport, err := systemnats.Connect(ctx, c.server.URL())
	if err != nil {
		c.t.Fatal(err)
	}
	node, err := files.NewNode(files.Config{
		NodeID:   id,
		Dir:      c.dirs[id],
		Catalog:  transport.FilesCatalog(),
		Peers:    transport.FilesPeers(),
		Live:     func() ([]string, bool) { return c.live, true },
		LeaseTTL:       600 * time.Millisecond,
		Poll:           20 * time.Millisecond,
		ReconcileEvery: 100 * time.Millisecond,
		// Several chunks per test file, so transfers resume across messages.
		ChunkSize: 64 << 10,
	})
	if err != nil {
		c.t.Fatal(err)
	}
	if err := transport.ServeFiles(ctx, id, node); err != nil {
		c.t.Fatal(err)
	}
	if err := transport.ServeLocalFiles(ctx, id, node, 600*time.Millisecond); err != nil {
		c.t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = node.Run(ctx)
	}()
	n := &filesNode{id: id, node: node, transport: transport}
	n.stop = func() {
		cancel()
		<-done
		transport.Close()
	}
	c.t.Cleanup(n.stop)
	return n
}

func hexSum(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func TestFilesReplicateOverSystemNATS(t *testing.T) {
	cluster := newFilesCluster(t, "node-a", "node-b", "node-c")
	a, b, c := cluster.start("node-a"), cluster.start("node-b"), cluster.start("node-c")
	ctx := t.Context()

	// Racing owners: the KV compare-and-set lets exactly one win.
	var wg sync.WaitGroup
	won := make([]*files.Handle, 3)
	for i, n := range []*filesNode{a, b, c} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			won[i], _ = n.node.AcquireHandle(timeoutContext(t, 300*time.Millisecond), "state/db", grove.Replicas(3))
		}()
	}
	wg.Wait()
	var owner *files.Handle
	winners := 0
	for _, handle := range won {
		if handle != nil {
			owner = handle
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("%d nodes acquired state/db; want exactly 1", winners)
	}

	data := bytes.Repeat([]byte("0123456789abcdef"), 96<<10) // 1.5 MiB: many chunks
	if err := os.WriteFile(owner.LocalPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	version, err := owner.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version.SHA256 != hexSum(data) {
		t.Fatalf("version checksum %s; want %s", version.SHA256, hexSum(data))
	}
	if err := owner.WaitReplicated(ctx, version); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"node-a", "node-b", "node-c"} {
		blob := filepath.Join(cluster.dirs[id], "objects", files.FileIDFor("state/db"), "versions", version.ID+".blob")
		got, err := os.ReadFile(blob)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("%s holds %d bytes; want %d", id, len(got), len(data))
		}
	}
	record, _, err := a.transport.FilesCatalog().Get(ctx, files.FileIDFor("state/db"))
	if err != nil {
		t.Fatal(err)
	}
	if record.Current.ID != version.ID || len(record.Holders(version.ID)) != 3 {
		t.Fatalf("catalog current %s holders %v", record.Current.ID, record.Holders(version.ID))
	}
}

// Every process stops and the System NATS state is lost. One prior replica
// starts against a fresh server and rebuilds the catalog from its disk; the
// others join and reconcile.
func TestFilesColdRecoveryOverSystemNATS(t *testing.T) {
	cluster := newFilesCluster(t, "node-a", "node-b", "node-c")
	nodes := map[string]*filesNode{}
	for _, id := range cluster.live {
		nodes[id] = cluster.start(id)
	}
	ctx := t.Context()
	owner, err := nodes["node-a"].node.AcquireHandle(ctx, "state/orders.db", grove.Replicas(3), grove.WithDurability(grove.Quorum))
	if err != nil {
		t.Fatal(err)
	}
	var last grove.Version
	for _, content := range []string{"v1", "v2", "v3"} {
		if err := os.WriteFile(owner.LocalPath(), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if last, err = owner.Sync(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := owner.WaitReplicated(ctx, last); err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		n.stop()
	}
	cluster.server.Shutdown()

	cluster.server = startFilesServer(t) // no surviving JetStream state
	t.Cleanup(cluster.server.Shutdown)
	b := cluster.start("node-b")
	if err := b.node.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	record, _, err := b.transport.FilesCatalog().Get(ctx, files.FileIDFor("state/orders.db"))
	if err != nil {
		t.Fatalf("recovered catalog: %v", err)
	}
	if record.Current.Public() != last {
		t.Fatalf("recovered %+v; want %+v", record.Current.Public(), last)
	}
	opened, err := b.node.OpenHandle(ctx, "state/orders.db")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(opened.LocalPath()); string(got) != "v3" {
		t.Fatalf("recovered content %q", got)
	}
	for _, id := range []string{"node-a", "node-c"} {
		n := cluster.start(id)
		if err := n.node.Recover(ctx); err != nil {
			t.Fatal(err)
		}
		if err := n.node.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
	}
	record, _, err = b.transport.FilesCatalog().Get(ctx, files.FileIDFor("state/orders.db"))
	if err != nil {
		t.Fatal(err)
	}
	if record.Current.ID != last.ID || len(record.Holders(last.ID)) != 3 {
		t.Fatalf("after rejoin: current %s holders %v", record.Current.ID, record.Holders(last.ID))
	}
}

// An application process reaches its Grovlet's file service: it gets a local
// path, syncs, and its ownership ends when it stops renewing.
func TestApplicationProcessFilesThroughTheGrovlet(t *testing.T) {
	cluster := newFilesCluster(t, "node-a", "node-b")
	cluster.start("node-a")
	cluster.start("node-b")
	ctx := t.Context()

	app, err := systemnats.Connect(ctx, cluster.server.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	lifetime, stopApp := context.WithCancel(ctx)
	store := app.NewRemoteFileStore(lifetime, "node-a", 600*time.Millisecond)
	ctx = grove.WithFileStore(ctx, store)

	file, err := grove.Files(ctx).Acquire(ctx, "state/app.db", grove.Replicas(2))
	if err != nil {
		t.Fatal(err)
	}
	if !file.Held() || file.Owner() != "node-a" {
		t.Fatalf("held %t owner %q", file.Held(), file.Owner())
	}
	if err := os.WriteFile(file.LocalPath(), []byte("from the app"), 0o600); err != nil {
		t.Fatal(err)
	}
	version, err := file.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.WaitReplicated(ctx, version); err != nil {
		t.Fatal(err)
	}
	if file.CurrentVersion() != version {
		t.Fatalf("handle version %+v; want %+v", file.CurrentVersion(), version)
	}

	// A second application process on node-b cannot own it meanwhile.
	other := app.NewRemoteFileStore(ctx, "node-b", 600*time.Millisecond)
	if _, err := other.Acquire(timeoutContext(t, 200*time.Millisecond), "state/app.db"); !errors.Is(err, grove.ErrFileOwned) {
		t.Fatalf("second owner: %v; want ErrFileOwned", err)
	}
	// The first process dies without releasing: its Grovlet drops the handle
	// once renewals stop, and node-b takes over from the committed version.
	stopApp()
	owned, err := other.Acquire(timeoutContext(t, 10*time.Second), "state/app.db")
	if err != nil {
		t.Fatal(err)
	}
	if owned.CurrentVersion() != version {
		t.Fatalf("takeover at %+v; want %+v", owned.CurrentVersion(), version)
	}
	if got, _ := os.ReadFile(owned.LocalPath()); string(got) != "from the app" {
		t.Fatalf("takeover content %q", got)
	}
	if err := owned.Release(ctx); err != nil {
		t.Fatal(err)
	}
}

func timeoutContext(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), d)
	t.Cleanup(cancel)
	return ctx
}
