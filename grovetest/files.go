package grovetest

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/files"
)

// FilesLeaseTTL is how long a FilesCluster owner may go without renewing
// before another node can take its files over.
const FilesLeaseTTL = 300 * time.Millisecond

// FilesCluster runs Grove Files on in-process nodes. Each node has its own
// real directory and runs production Grove Files code; only the catalog
// store and the network are in-process. Nodes can be killed, restarted over
// the same directory, and the whole cluster restarted with or without its
// metadata, so recovery is tested by restarting, not by inspecting memory.
//
//	cluster := grovetest.NewFilesCluster(t, "node-a", "node-b", "node-c")
//	file, _ := grove.Files(cluster.Context("node-a")).Acquire(ctx, "state/app.db")
//	os.WriteFile(file.LocalPath(), data, 0o600)
//	file.Sync(ctx)
//	cluster.Kill("node-a")
//	owned, _ := cluster.Store("node-b").Acquire(ctx, "state/app.db") // last committed version
type FilesCluster struct {
	t       testing.TB
	mu      sync.Mutex
	catalog *files.MemoryCatalog
	network *files.Network
	ids     []string
	nodes   map[string]*filesClusterNode
	dirs    map[string]string
}

type filesClusterNode struct {
	node *files.Node
	cut  *atomic.Bool
	stop context.CancelFunc
	done chan struct{}
}

// NewFilesCluster starts the named nodes. They are stopped when the test
// ends.
func NewFilesCluster(t testing.TB, nodes ...string) *FilesCluster {
	t.Helper()
	c := &FilesCluster{
		t:       t,
		catalog: files.NewMemoryCatalog(),
		network: files.NewNetwork(),
		ids:     slices.Clone(nodes),
		nodes:   make(map[string]*filesClusterNode),
		dirs:    make(map[string]string),
	}
	root := t.TempDir()
	for _, id := range nodes {
		c.dirs[id] = filepath.Join(root, id)
	}
	t.Cleanup(c.StopAll)
	for _, id := range nodes {
		c.Start(id)
	}
	return c
}

// Start starts node, or restarts it over its directory after Kill. A
// restarted node verifies its stored versions, recovers them into the
// catalog when it has no record of them, and reconciles with the cluster.
func (c *FilesCluster) Start(node string) {
	c.t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if running, ok := c.nodes[node]; ok && running.stop != nil {
		return
	}
	dir, ok := c.dirs[node]
	if !ok {
		c.t.Fatalf("grovetest: unknown files node %q", node)
	}
	cut := &atomic.Bool{}
	n, err := files.NewNode(files.Config{
		NodeID:         node,
		Dir:            dir,
		Catalog:        cutCatalog{Catalog: c.catalog, cut: cut},
		Peers:          c.network.Peers(node),
		Live:           c.network.Live,
		LeaseTTL:       FilesLeaseTTL,
		Poll:           10 * time.Millisecond,
		ReconcileEvery: 50 * time.Millisecond,
	})
	if err != nil {
		c.t.Fatalf("grovetest: start files node %s: %v", node, err)
	}
	c.network.Add(n)
	ctx, cancel := context.WithCancel(context.Background())
	running := &filesClusterNode{node: n, cut: cut, stop: cancel, done: make(chan struct{})}
	go func() {
		defer close(running.done)
		_ = n.Run(ctx)
	}()
	c.nodes[node] = running
}

// Kill stops node abruptly: it stops renewing ownership and reconciling, and
// it can no longer reach the catalog or its peers. Its directory stays.
func (c *FilesCluster) Kill(node string) {
	c.mu.Lock()
	running, ok := c.nodes[node]
	c.mu.Unlock()
	if !ok || running.stop == nil {
		return
	}
	running.cut.Store(true)
	c.network.SetDown(node, true)
	running.stop()
	<-running.done
	running.stop = nil
}

// StopAll kills every node.
func (c *FilesCluster) StopAll() {
	for _, id := range c.ids {
		c.Kill(id)
	}
}

// LoseMetadata discards the cluster catalog, as when every node stopped and
// no live cluster metadata survived. Call it with every node stopped.
func (c *FilesCluster) LoseMetadata() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.catalog = files.NewMemoryCatalog()
	c.network = files.NewNetwork()
	c.nodes = make(map[string]*filesClusterNode)
}

// Store is node's file store.
func (c *FilesCluster) Store(node string) grove.FileStore {
	return c.running(node).node
}

// Context returns the test's context carrying node's file store, as a
// handler running on node would see it.
func (c *FilesCluster) Context(node string) context.Context {
	return grove.WithFileStore(c.t.Context(), c.Store(node))
}

// Dir is node's Grove Files directory.
func (c *FilesCluster) Dir(node string) string { return c.dirs[node] }

// Current is the committed version of path, or the zero Version.
func (c *FilesCluster) Current(path string) grove.Version {
	record, ok := c.record(path)
	if !ok || record.Current == nil {
		return grove.Version{}
	}
	return record.Current.Public()
}

// Holders lists the nodes the catalog records as holding a verified copy of
// path's committed version.
func (c *FilesCluster) Holders(path string) []string {
	record, ok := c.record(path)
	if !ok || record.Current == nil {
		return nil
	}
	return record.Holders(record.Current.ID)
}

// WaitHolders waits until want nodes hold path's committed version.
func (c *FilesCluster) WaitHolders(path string, want int) {
	c.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for len(c.Holders(path)) < want {
		if time.Now().After(deadline) {
			c.t.Fatalf("grovetest: %s has holders %v; want %d", path, c.Holders(path), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (c *FilesCluster) record(path string) (files.Record, bool) {
	clean, err := files.CleanPath(path)
	if err != nil {
		return files.Record{}, false
	}
	c.mu.Lock()
	catalog := c.catalog
	c.mu.Unlock()
	record, _, err := catalog.Get(context.Background(), files.FileIDFor(clean))
	return record, err == nil
}

func (c *FilesCluster) running(node string) *filesClusterNode {
	c.t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	running, ok := c.nodes[node]
	if !ok {
		c.t.Fatalf("grovetest: files node %q is not running", node)
	}
	return running
}

var errCut = errors.New("grovetest: node is cut off from the catalog")

// cutCatalog is one node's view of the shared catalog; a killed node's view
// fails as a lost connection would.
type cutCatalog struct {
	files.Catalog
	cut *atomic.Bool
}

func (c cutCatalog) Cluster(ctx context.Context) (files.ClusterRecord, error) {
	if c.cut.Load() {
		return files.ClusterRecord{}, errCut
	}
	return c.Catalog.Cluster(ctx)
}

func (c cutCatalog) CreateCluster(ctx context.Context, record files.ClusterRecord) error {
	if c.cut.Load() {
		return errCut
	}
	return c.Catalog.CreateCluster(ctx, record)
}

func (c cutCatalog) Get(ctx context.Context, id string) (files.Record, uint64, error) {
	if c.cut.Load() {
		return files.Record{}, 0, errCut
	}
	return c.Catalog.Get(ctx, id)
}

func (c cutCatalog) Put(ctx context.Context, record files.Record, revision uint64) (uint64, error) {
	if c.cut.Load() {
		return 0, errCut
	}
	return c.Catalog.Put(ctx, record, revision)
}

func (c cutCatalog) List(ctx context.Context) ([]files.Record, error) {
	if c.cut.Load() {
		return nil, errCut
	}
	return c.Catalog.List(ctx)
}
