package files_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grove-project/grove/internal/files"
)

// clock is a test clock shared by every node of a testCluster.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// partitionable is one node's view of the shared catalog, which a test can
// cut as a partition would.
type partitionable struct {
	files.Catalog
	cut *atomic.Bool
}

func (p partitionable) Cluster(ctx context.Context) (files.ClusterRecord, error) {
	if p.cut.Load() {
		return files.ClusterRecord{}, files.ErrUnreachable
	}
	return p.Catalog.Cluster(ctx)
}

func (p partitionable) CreateCluster(ctx context.Context, record files.ClusterRecord) error {
	if p.cut.Load() {
		return files.ErrUnreachable
	}
	return p.Catalog.CreateCluster(ctx, record)
}

func (p partitionable) Get(ctx context.Context, id string) (files.Record, uint64, error) {
	if p.cut.Load() {
		return files.Record{}, 0, files.ErrUnreachable
	}
	return p.Catalog.Get(ctx, id)
}

func (p partitionable) Put(ctx context.Context, record files.Record, revision uint64) (uint64, error) {
	if p.cut.Load() {
		return 0, files.ErrUnreachable
	}
	return p.Catalog.Put(ctx, record, revision)
}

func (p partitionable) List(ctx context.Context) ([]files.Record, error) {
	if p.cut.Load() {
		return nil, files.ErrUnreachable
	}
	return p.Catalog.List(ctx)
}

// testCluster runs Grove Files nodes in one process: a shared catalog, an
// in-process network, per-node durable directories and one clock. A node
// "restarts" by building a new Node over the same directory.
type testCluster struct {
	t       *testing.T
	clock   *clock
	catalog *files.MemoryCatalog
	network *files.Network
	dirs    map[string]string
	cuts    map[string]*atomic.Bool
	nodes   map[string]*files.Node
	events  []files.Event
	mu      sync.Mutex
}

const testLeaseTTL = 3 * time.Second

func newTestCluster(t *testing.T) *testCluster {
	t.Helper()
	return &testCluster{
		t:       t,
		clock:   &clock{now: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)},
		catalog: files.NewMemoryCatalog(),
		network: files.NewNetwork(),
		dirs:    make(map[string]string),
		cuts:    make(map[string]*atomic.Bool),
		nodes:   make(map[string]*files.Node),
	}
}

// start creates or restarts node id over its durable directory.
func (c *testCluster) start(id string) *files.Node {
	c.t.Helper()
	dir, ok := c.dirs[id]
	if !ok {
		dir = filepath.Join(c.t.TempDir(), id)
		c.dirs[id] = dir
		c.cuts[id] = &atomic.Bool{}
	}
	c.cuts[id].Store(false)
	node, err := files.NewNode(files.Config{
		NodeID:   id,
		Dir:      dir,
		Catalog:  partitionable{Catalog: c.catalog, cut: c.cuts[id]},
		Peers:    c.network.Peers(id),
		Live:     c.network.Live,
		Now:      c.clock.Now,
		LeaseTTL: testLeaseTTL,
		Poll:     time.Millisecond,
		Events: func(event files.Event) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.events = append(c.events, event)
		},
	})
	if err != nil {
		c.t.Fatalf("start %s: %v", id, err)
	}
	c.network.Add(node)
	c.nodes[id] = node
	c.t.Cleanup(node.Wait)
	return node
}

// stop takes node id down: unreachable to peers and cut off from the
// catalog. Its directory stays for a restart.
func (c *testCluster) stop(id string) {
	c.nodes[id].Wait()
	c.network.SetDown(id, true)
	c.cuts[id].Store(true)
}

// partition cuts node id off from the catalog and its peers while it keeps
// running.
func (c *testCluster) partition(id string, cut bool) {
	c.network.SetDown(id, cut)
	c.cuts[id].Store(cut)
}

// wipeCatalog replaces the catalog with an empty one, as when every node
// stopped and no live cluster metadata survived.
func (c *testCluster) wipeCatalog() {
	c.catalog = files.NewMemoryCatalog()
	c.network = files.NewNetwork()
	c.nodes = make(map[string]*files.Node)
}

func (c *testCluster) record(path string) files.Record {
	c.t.Helper()
	record, _, err := c.catalog.Get(context.Background(), files.FileIDFor(path))
	if err != nil {
		c.t.Fatalf("catalog record %s: %v", path, err)
	}
	return record
}

func (c *testCluster) countEvents(kind files.EventKind) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, event := range c.events {
		if event.Kind == kind {
			count++
		}
	}
	return count
}

func sum(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func writeLocal(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readLocal(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// replicaBytes reads the bytes node holds for version of path straight from
// its directory, the way an operator would after the writer is gone.
func (c *testCluster) replicaBytes(node, path, version string) []byte {
	c.t.Helper()
	data, err := os.ReadFile(filepath.Join(c.dirs[node], "objects", files.FileIDFor(path), "versions", version+".blob"))
	if err != nil {
		c.t.Fatalf("read %s replica of %s %s: %v", node, path, version, err)
	}
	return data
}

// takeOver makes node the owner of path after its owner stopped: node first
// observes the silent lease, then claims it once the lease aged a full TTL.
func (c *testCluster) takeOver(node *files.Node, path string) *files.Handle {
	c.t.Helper()
	for attempt := 0; attempt < 3; attempt++ {
		handle, done, err := node.TryAcquire(c.t.Context(), path)
		if done {
			if err != nil {
				c.t.Fatalf("%s take over %s: %v", node.ID(), path, err)
			}
			return handle
		}
		c.clock.Advance(testLeaseTTL)
	}
	c.t.Fatalf("%s could not take over %s", node.ID(), path)
	return nil
}
