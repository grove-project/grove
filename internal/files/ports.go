package files

import (
	"context"
	"maps"
	"slices"
	"sync"
)

// Catalog is the authoritative cluster metadata Grove Files coordinates
// through. Writes are compare-and-set on a record's revision. The System NATS
// adapter stores it in JetStream KV; MemoryCatalog serves tests.
type Catalog interface {
	// Cluster returns the cluster identity, or ErrNotFound.
	Cluster(ctx context.Context) (ClusterRecord, error)
	// CreateCluster stores the cluster identity, or fails with ErrConflict
	// when one exists.
	CreateCluster(ctx context.Context, record ClusterRecord) error
	// Get returns a file record and its revision, or ErrNotFound.
	Get(ctx context.Context, fileID string) (Record, uint64, error)
	// Put stores record if the stored revision is still revision; zero
	// creates it. It fails with ErrConflict otherwise.
	Put(ctx context.Context, record Record, revision uint64) (uint64, error)
	// List returns every file record.
	List(ctx context.Context) ([]Record, error)
}

// Peers reaches the file service of other nodes. The System NATS adapter
// carries it over request/reply; Network connects in-process nodes.
type Peers interface {
	// Replicate asks node to hold a version: it pulls the bytes from the
	// request's source unless it has them, verifies and persists them.
	Replicate(ctx context.Context, node string, request ReplicateRequest) error
	// ReadBlob reads part of a verified version that node stores. It
	// returns io.EOF once the offset reaches the end.
	ReadBlob(ctx context.Context, node string, request BlobRequest) ([]byte, error)
}

// ReplicateRequest asks a node to persist one version of a file.
type ReplicateRequest struct {
	ClusterID  string      `json:"cluster_id"`
	File       LocalFile   `json:"file"`
	Version    VersionMeta `json:"version"`
	Source     string      `json:"source_node"`
	Committed  bool        `json:"committed,omitempty"`
}

// BlobRequest reads Length bytes of a version from Offset.
type BlobRequest struct {
	FileID    string `json:"file_id"`
	VersionID string `json:"version_id"`
	Offset    int64  `json:"offset"`
	Length    int    `json:"length"`
}

// MemoryCatalog is an in-memory Catalog. Fail makes it unreachable, as a
// partitioned node would find the real catalog.
type MemoryCatalog struct {
	mu        sync.Mutex
	cluster   *ClusterRecord
	records   map[string]Record
	revisions map[string]uint64
	next      uint64
}

// NewMemoryCatalog returns an empty catalog.
func NewMemoryCatalog() *MemoryCatalog {
	return &MemoryCatalog{records: make(map[string]Record), revisions: make(map[string]uint64)}
}

// Cluster implements Catalog.
func (c *MemoryCatalog) Cluster(context.Context) (ClusterRecord, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cluster == nil {
		return ClusterRecord{}, ErrNotFound
	}
	return *c.cluster, nil
}

// CreateCluster implements Catalog.
func (c *MemoryCatalog) CreateCluster(_ context.Context, record ClusterRecord) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cluster != nil {
		return ErrConflict
	}
	c.cluster = &record
	return nil
}

// Get implements Catalog.
func (c *MemoryCatalog) Get(_ context.Context, fileID string) (Record, uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	record, ok := c.records[fileID]
	if !ok {
		return Record{}, 0, ErrNotFound
	}
	return cloneRecord(record), c.revisions[fileID], nil
}

// Put implements Catalog.
func (c *MemoryCatalog) Put(_ context.Context, record Record, revision uint64) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.revisions[record.FileID] != revision {
		return 0, ErrConflict
	}
	c.next++
	c.records[record.FileID] = cloneRecord(record)
	c.revisions[record.FileID] = c.next
	return c.next, nil
}

// List implements Catalog.
func (c *MemoryCatalog) List(context.Context) ([]Record, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	records := make([]Record, 0, len(c.records))
	for _, id := range slices.Sorted(maps.Keys(c.records)) {
		records = append(records, cloneRecord(c.records[id]))
	}
	return records, nil
}

func cloneRecord(record Record) Record {
	if record.Current != nil {
		current := *record.Current
		record.Current = &current
	}
	if record.Staging != nil {
		staging := *record.Staging
		record.Staging = &staging
	}
	record.History = slices.Clone(record.History)
	record.ReplicaSet = slices.Clone(record.ReplicaSet)
	return record
}
