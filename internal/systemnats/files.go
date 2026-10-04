package systemnats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/grove-project/grove/internal/files"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// The System NATS adapter for Grove Files: the catalog is the FilesBucket KV
// bucket, and file bytes move node to node over request/reply in bounded
// chunks, never as KV values.

const (
	// FilesBucket is the authoritative Grove Files catalog bucket. It is
	// created on the first file write, so a cluster that uses no files has
	// none.
	FilesBucket = "GROVE_FILES"

	filesClusterKey       = "cluster"
	filesRecordKeyPrefix  = "file."
	filesReplicateRoot    = "_GROVE.system.files.replicate."
	filesBlobRoot         = "_GROVE.system.files.blob."
	filesErrorHeader      = "Grove-Files-Error"
	filesErrorCodeHeader  = "Grove-Files-Code"
	filesCodeEOF          = "eof"
	filesCodeIntegrity    = "integrity"
	filesCodeLineage      = "lineage"
	filesCodeNoReplica    = "no-replica"
	filesCodeOther        = "error"
	filesBlobChunkCeiling = 512 << 10
)

// FilesCatalog stores the Grove Files catalog in JetStream KV. Writes are
// compare-and-set on the KV revision.
type FilesCatalog struct {
	transport *Transport
	mu        sync.Mutex
	kv        jetstream.KeyValue
}

// FilesCatalog returns the Grove Files catalog on this transport.
func (t *Transport) FilesCatalog() *FilesCatalog { return &FilesCatalog{transport: t} }

// bucket opens the catalog bucket, creating it only when create is set. It
// returns nil without error when the bucket does not exist yet.
func (c *FilesCatalog) bucket(ctx context.Context, create bool) (jetstream.KeyValue, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.kv != nil {
		return c.kv, nil
	}
	js, err := jetstream.New(c.transport.connection)
	if err != nil {
		return nil, err
	}
	ctx, cancel := operationContext(ctx)
	defer cancel()
	kv, err := js.KeyValue(ctx, FilesBucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) && !create {
		return nil, nil
	}
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		kv, err = openOrCreateKeyValue(ctx, js, filesKeyValueConfig(controlStateBootstrapReplicas))
	}
	if err != nil {
		return nil, fmt.Errorf("open grove files catalog: %w", err)
	}
	c.kv = kv
	return kv, nil
}

func filesKeyValueConfig(replicas int) jetstream.KeyValueConfig {
	return jetstream.KeyValueConfig{
		Bucket:      FilesBucket,
		Description: "Authoritative Grove Files catalog",
		History:     1,
		Storage:     jetstream.FileStorage,
		Replicas:    replicas,
	}
}

func (c *FilesCatalog) get(ctx context.Context, key string, value any) (uint64, error) {
	kv, err := c.bucket(ctx, false)
	if err != nil {
		return 0, err
	}
	if kv == nil {
		return 0, files.ErrNotFound
	}
	ctx, cancel := operationContext(ctx)
	defer cancel()
	entry, err := kv.Get(ctx, key)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return 0, files.ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if err := json.Unmarshal(entry.Value(), value); err != nil {
		return 0, fmt.Errorf("decode grove files catalog %s: %w", key, err)
	}
	return entry.Revision(), nil
}

func (c *FilesCatalog) put(ctx context.Context, key string, value any, revision uint64) (uint64, error) {
	kv, err := c.bucket(ctx, true)
	if err != nil {
		return 0, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	ctx, cancel := operationContext(ctx)
	defer cancel()
	if revision == 0 {
		revision, err = kv.Create(ctx, key, encoded)
	} else {
		revision, err = kv.Update(ctx, key, encoded, revision)
	}
	if errors.Is(err, jetstream.ErrKeyExists) || errors.Is(err, jetstream.ErrKeyRevisionMismatch) {
		return 0, files.ErrConflict
	}
	return revision, err
}

// Cluster implements files.Catalog.
func (c *FilesCatalog) Cluster(ctx context.Context) (files.ClusterRecord, error) {
	var record files.ClusterRecord
	_, err := c.get(ctx, filesClusterKey, &record)
	return record, err
}

// CreateCluster implements files.Catalog.
func (c *FilesCatalog) CreateCluster(ctx context.Context, record files.ClusterRecord) error {
	_, err := c.put(ctx, filesClusterKey, record, 0)
	return err
}

// Get implements files.Catalog.
func (c *FilesCatalog) Get(ctx context.Context, fileID string) (files.Record, uint64, error) {
	var record files.Record
	revision, err := c.get(ctx, filesRecordKeyPrefix+fileID, &record)
	if err == nil && record.Format > files.FormatVersion {
		return files.Record{}, 0, fmt.Errorf("%w %d", files.ErrFormat, record.Format)
	}
	return record, revision, err
}

// Put implements files.Catalog.
func (c *FilesCatalog) Put(ctx context.Context, record files.Record, revision uint64) (uint64, error) {
	return c.put(ctx, filesRecordKeyPrefix+record.FileID, record, revision)
}

// List implements files.Catalog.
func (c *FilesCatalog) List(ctx context.Context) ([]files.Record, error) {
	kv, err := c.bucket(ctx, false)
	if err != nil || kv == nil {
		return nil, err
	}
	ctx, cancel := operationContext(ctx)
	defer cancel()
	keys, err := kv.ListKeys(ctx)
	if err != nil {
		return nil, err
	}
	var records []files.Record
	for key := range keys.Keys() {
		id, ok := strings.CutPrefix(key, filesRecordKeyPrefix)
		if !ok {
			continue
		}
		record, _, err := c.Get(ctx, id)
		if errors.Is(err, files.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// ReconcileReplicas grows the catalog bucket's replication with membership,
// as the membership watcher does for the buckets that exist when it
// changes. It does nothing until a file write created the bucket.
func (c *FilesCatalog) ReconcileReplicas(ctx context.Context, membership *Membership) error {
	if membership == nil {
		return nil
	}
	view := membership.Snapshot()
	if !view.Ready {
		return nil
	}
	kv, err := c.bucket(ctx, false)
	if err != nil || kv == nil {
		return err
	}
	js, err := jetstream.New(c.transport.connection)
	if err != nil {
		return err
	}
	records := make(map[string]MembershipRecord, len(view.Members))
	for _, member := range view.Members {
		records[member.NodeID] = member
	}
	_, err = reconcileControlStateReplicas(ctx, js, records, false)
	return err
}

// ServeFiles answers other nodes' replicate and blob requests for node.
// Each request runs in its own goroutine, so a long transfer does not hold
// up the next.
func (t *Transport) ServeFiles(ctx context.Context, nodeID string, node *files.Node) error {
	replicate := func(message *nats.Msg) {
		var request files.ReplicateRequest
		err := json.Unmarshal(message.Data, &request)
		if err == nil {
			err = node.HandleReplicate(ctx, request)
		}
		_ = message.RespondMsg(filesReply(nil, err))
	}
	blob := func(message *nats.Msg) {
		var request files.BlobRequest
		err := json.Unmarshal(message.Data, &request)
		var data []byte
		if err == nil {
			data, err = node.HandleReadBlob(request)
		}
		_ = message.RespondMsg(filesReply(data, err))
	}
	for subject, handle := range map[string]nats.MsgHandler{
		filesReplicateRoot + nodeID: replicate,
		filesBlobRoot + nodeID:      blob,
	} {
		subscription, err := t.connection.Subscribe(subject, func(message *nats.Msg) { go handle(message) })
		if err != nil {
			return &Error{Operation: "subscribe Grove files endpoint", Err: err}
		}
		go func() {
			<-ctx.Done()
			_ = subscription.Unsubscribe()
		}()
	}
	flushCtx, cancel := operationContext(ctx)
	defer cancel()
	if err := t.connection.FlushWithContext(flushCtx); err != nil {
		return &Error{Operation: "activate Grove files endpoint", Err: err}
	}
	return nil
}

// FilesPeers reaches other nodes' file endpoints.
func (t *Transport) FilesPeers() files.Peers { return filesPeers{transport: t} }

type filesPeers struct{ transport *Transport }

func (p filesPeers) Replicate(ctx context.Context, node string, request files.ReplicateRequest) error {
	encoded, err := json.Marshal(request)
	if err != nil {
		return err
	}
	ctx, cancel := operationContext(ctx)
	defer cancel()
	reply, err := p.transport.connection.RequestWithContext(ctx, filesReplicateRoot+node, encoded)
	if err != nil {
		return fmt.Errorf("replicate to %s: %w", node, err)
	}
	return filesReplyError(reply)
}

func (p filesPeers) ReadBlob(ctx context.Context, node string, request files.BlobRequest) ([]byte, error) {
	request.Length = min(request.Length, filesBlobChunkCeiling)
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	ctx, cancel := operationContext(ctx)
	defer cancel()
	reply, err := p.transport.connection.RequestWithContext(ctx, filesBlobRoot+node, encoded)
	if err != nil {
		return nil, fmt.Errorf("read blob from %s: %w", node, err)
	}
	if err := filesReplyError(reply); err != nil {
		return nil, err
	}
	return reply.Data, nil
}

func filesReply(data []byte, err error) *nats.Msg {
	reply := &nats.Msg{Data: data, Header: nats.Header{}}
	if err == nil {
		return reply
	}
	code := filesCodeOther
	switch {
	case errors.Is(err, io.EOF):
		code = filesCodeEOF
	case errors.Is(err, files.ErrIntegrity):
		code = filesCodeIntegrity
	case errors.Is(err, files.ErrLineage):
		code = filesCodeLineage
	case errors.Is(err, files.ErrNoEligibleReplica):
		code = filesCodeNoReplica
	}
	reply.Data = nil
	reply.Header.Set(filesErrorCodeHeader, code)
	reply.Header.Set(filesErrorHeader, err.Error())
	return reply
}

func filesReplyError(reply *nats.Msg) error {
	code := reply.Header.Get(filesErrorCodeHeader)
	message := reply.Header.Get(filesErrorHeader)
	switch code {
	case "":
		return nil
	case filesCodeEOF:
		return io.EOF
	case filesCodeIntegrity:
		return fmt.Errorf("%w: %s", files.ErrIntegrity, message)
	case filesCodeLineage:
		return fmt.Errorf("%w: %s", files.ErrLineage, message)
	case filesCodeNoReplica:
		return fmt.Errorf("%w: %s", files.ErrNoEligibleReplica, message)
	}
	return errors.New(message)
}
