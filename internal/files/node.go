package files

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/placement"
)

const (
	defaultLeaseTTL        = 3 * time.Second
	defaultPoll            = 100 * time.Millisecond
	defaultReconcileEvery  = time.Second
	defaultRetain          = 3
	defaultChunkSize       = 256 << 10
	defaultTransferTimeout = 2 * time.Minute
	maxCatalogAttempts     = 16
)

// Config configures one node's file service.
type Config struct {
	// NodeID is this node's logical identity.
	NodeID string
	// Dir is this node's durable Grove Files directory.
	Dir string
	// Catalog is the authoritative cluster metadata.
	Catalog Catalog
	// Peers reaches other nodes. Nil means this node has no peers.
	Peers Peers
	// Live reports the live nodes that store files. Nil means only this
	// node; not ready means liveness is unknown and only this node counts.
	Live func() ([]string, bool)
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	// LeaseTTL is how long an owner may go without renewing before another
	// node can take ownership. An owner stops publishing before then.
	LeaseTTL time.Duration
	// Poll is how often waits re-check the catalog.
	Poll time.Duration
	// ReconcileEvery is how often Run reconciles local state.
	ReconcileEvery time.Duration
	// Retain is how many previous committed versions each file keeps.
	Retain int
	// ChunkSize is the largest transfer message.
	ChunkSize int
	// TransferTimeout bounds one replica transfer.
	TransferTimeout time.Duration
	// Events receives structured lifecycle events. Nil discards them.
	Events func(Event)
}

// Node is one node's Grove Files service: it materializes files locally,
// publishes synced versions, holds fenced ownership, serves and stores
// replicas, and recovers and reconciles local state against the catalog.
type Node struct {
	cfg  Config
	disk *Disk

	mu        sync.Mutex
	clusterID string
	observed  map[string]placement.ObservedLease
	held      map[string]*ownership
	locks     map[string]*sync.Mutex
	lastSync  map[string]SyncResult

	background sync.WaitGroup
}

// ownership is this node's fenced claim on one file.
type ownership struct {
	lease placement.HeldLease
	// owner is the record's lease as this node last stored or read it.
	owner placement.Ownership
}

// SyncResult is the outcome of a file's latest Sync on this node.
type SyncResult struct {
	Version VersionMeta
	At      time.Time
	Err     string
}

// NewNode opens the node's directory, verifies every stored version and
// quarantines any that fail their checksum.
func NewNode(cfg Config) (*Node, error) {
	if cfg.NodeID == "" {
		return nil, errors.New("grove files node identity is required")
	}
	if cfg.Catalog == nil {
		return nil, errors.New("grove files catalog is required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = defaultLeaseTTL
	}
	if cfg.Poll <= 0 {
		cfg.Poll = defaultPoll
	}
	if cfg.ReconcileEvery <= 0 {
		cfg.ReconcileEvery = defaultReconcileEvery
	}
	if cfg.Retain <= 0 {
		cfg.Retain = defaultRetain
	}
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = defaultChunkSize
	}
	if cfg.TransferTimeout <= 0 {
		cfg.TransferTimeout = defaultTransferTimeout
	}
	disk, err := OpenDisk(cfg.Dir)
	if err != nil {
		return nil, err
	}
	n := &Node{
		cfg: cfg, disk: disk,
		observed: make(map[string]placement.ObservedLease),
		held:     make(map[string]*ownership),
		locks:    make(map[string]*sync.Mutex),
		lastSync: make(map[string]SyncResult),
	}
	if err := n.verifyDisk(); err != nil {
		return nil, err
	}
	return n, nil
}

// ID is the node's identity.
func (n *Node) ID() string { return n.cfg.NodeID }

// Disk is the node's durable state.
func (n *Node) Disk() *Disk { return n.disk }

// Wait waits for background replication started by Sync to finish.
func (n *Node) Wait() { n.background.Wait() }

// verifyDisk rehashes every stored version and quarantines those whose
// bytes no longer match their checksum.
func (n *Node) verifyDisk() error {
	ids, err := n.disk.FileIDs()
	if err != nil {
		return err
	}
	for _, id := range ids {
		versions, err := n.disk.Versions(id)
		if err != nil {
			return err
		}
		for _, version := range versions {
			if err := n.disk.Verify(id, version.Version); err != nil {
				n.emit(Event{Kind: EventCorrupt, Path: version.Path, Version: version.Version.ID, Err: err.Error()})
				if err := n.disk.QuarantineVersion(id, version.Version.ID); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// Run recovers local state into the catalog, then renews held ownership and
// reconciles local replicas until ctx ends.
func (n *Node) Run(ctx context.Context) error {
	renew := time.NewTicker(n.cfg.LeaseTTL / 6)
	defer renew.Stop()
	reconcile := time.NewTicker(n.cfg.ReconcileEvery)
	defer reconcile.Stop()
	recovered := n.Recover(ctx) == nil
	for {
		select {
		case <-ctx.Done():
			n.background.Wait()
			return ctx.Err()
		case <-renew.C:
			n.Renew(ctx)
		case <-reconcile.C:
			if !recovered {
				recovered = n.Recover(ctx) == nil
				continue
			}
			_ = n.Reconcile(ctx)
		}
	}
}

// Open implements grove.FileStore.
func (n *Node) Open(ctx context.Context, logical string, opts ...grove.FileOption) (grove.File, error) {
	return n.OpenHandle(ctx, logical, opts...)
}

// Acquire implements grove.FileStore.
func (n *Node) Acquire(ctx context.Context, logical string, opts ...grove.FileOption) (grove.OwnedFile, error) {
	return n.AcquireHandle(ctx, logical, opts...)
}

// OpenHandle materializes the latest committed version of logical.
func (n *Node) OpenHandle(ctx context.Context, logical string, opts ...grove.FileOption) (*Handle, error) {
	logical, err := CleanPath(logical)
	if err != nil {
		return nil, err
	}
	cluster, err := n.ensureCluster(ctx)
	if err != nil {
		return nil, err
	}
	id := FileIDFor(logical)
	record, _, err := n.cfg.Catalog.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		record, err = newRecord(cluster, logical, grove.ApplyFileOptions(opts...)), nil
	}
	if err != nil {
		return nil, err
	}
	if record.ClusterID != cluster {
		return nil, fmt.Errorf("%w: file %s", ErrLineage, logical)
	}
	if err := n.materialize(ctx, record); err != nil {
		return nil, err
	}
	handle := n.newHandle(record, nil)
	handle.options = opts
	return handle, nil
}

// AcquireHandle takes ownership of logical, waiting while another node's
// ownership may still be live, and materializes its committed version.
func (n *Node) AcquireHandle(ctx context.Context, logical string, opts ...grove.FileOption) (*Handle, error) {
	ticker := time.NewTicker(n.cfg.Poll)
	defer ticker.Stop()
	for {
		handle, done, err := n.TryAcquire(ctx, logical, opts...)
		if done {
			return handle, err
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			holder := ""
			if logical, err := CleanPath(logical); err == nil {
				n.mu.Lock()
				holder = n.observed[FileIDFor(logical)].Lease.Holder
				n.mu.Unlock()
			}
			return nil, fmt.Errorf("%w: %s holds %s: %w", grove.ErrFileOwned, holder, logical, ctx.Err())
		}
	}
}

// TryAcquire makes one ownership claim on logical. It reports done=false
// while another owner's lease may still be live. Ownership follows the
// placement lease policy: a new owner fences the previous one with the next
// epoch, and only once that owner has gone a full lease TTL without
// renewing.
func (n *Node) TryAcquire(ctx context.Context, logical string, opts ...grove.FileOption) (*Handle, bool, error) {
	logical, err := CleanPath(logical)
	if err != nil {
		return nil, true, err
	}
	cluster, err := n.ensureCluster(ctx)
	if err != nil {
		return nil, true, err
	}
	id := FileIDFor(logical)
	n.mu.Lock()
	if held, ok := n.held[id]; ok && held.lease.Active() {
		n.mu.Unlock()
		return nil, true, fmt.Errorf("%w: another handle on %s holds %s", grove.ErrFileOwned, n.cfg.NodeID, logical)
	}
	n.mu.Unlock()
	record, revision, err := n.cfg.Catalog.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		record, revision, err = newRecord(cluster, logical, grove.ApplyFileOptions(opts...)), 0, nil
	}
	if err != nil {
		return nil, false, err
	}
	if record.ClusterID != cluster {
		return nil, true, fmt.Errorf("%w: file %s", ErrLineage, logical)
	}
	now := n.cfg.Now()
	observed := n.observe(record, now)
	claim := placement.DecideClaim(placement.ClaimRequest{
		NodeID:   n.cfg.NodeID,
		Owner:    placement.Ownership{Nodes: []string{n.cfg.NodeID}, Epoch: record.Lease.Epoch + 1},
		Placed:   true,
		Observed: observed,
		Seen:     record.Owned(),
		Now:      now,
		TTL:      n.cfg.LeaseTTL,
	})
	if claim.Decision != placement.ClaimWrite {
		return nil, false, nil
	}
	previous := record.Lease
	record.Lease = claim.Lease
	// A version a previous owner staged but never committed is abandoned:
	// it is never promoted.
	record.Staging = nil
	if _, err := n.cfg.Catalog.Put(ctx, record, revision); err != nil {
		if errors.Is(err, ErrConflict) {
			return nil, false, nil
		}
		return nil, false, err
	}
	held := &ownership{
		lease: placement.HeldLease{Capability: logical, Epoch: claim.Lease.Epoch, Beat: claim.Lease.Beat, RenewedAt: now},
		owner: placement.Ownership{Nodes: []string{n.cfg.NodeID}, Epoch: claim.Lease.Epoch},
	}
	n.mu.Lock()
	n.held[id] = held
	n.mu.Unlock()
	if err := n.materialize(ctx, record); err != nil {
		_ = n.release(context.WithoutCancel(ctx), id, held)
		return nil, true, err
	}
	kind := EventOwnershipAcquired
	if previous.Holder != "" && previous.Holder != n.cfg.NodeID && !previous.Released {
		kind = EventPromoted
	}
	event := Event{Kind: kind, Path: logical, Epoch: claim.Lease.Epoch, Peer: previous.Holder}
	if record.Current != nil {
		event.Version = record.Current.ID
	}
	n.emit(event)
	handle := n.newHandle(record, held)
	handle.options = opts
	return handle, true, nil
}

// Renew extends every lease this node holds by one beat. A lease whose
// record names another owner or epoch is lost.
func (n *Node) Renew(ctx context.Context) {
	n.mu.Lock()
	held := make(map[string]*ownership, len(n.held))
	for id, h := range n.held {
		if h.lease.Active() {
			held[id] = h
		}
	}
	n.mu.Unlock()
	for id, h := range held {
		sent := n.cfg.Now()
		record, err := n.mutate(ctx, id, func(record *Record, exists bool) error {
			if !exists || !record.OwnedBy(n.cfg.NodeID, h.lease.Epoch) {
				return grove.ErrOwnershipLost
			}
			record.Lease.Beat++
			return nil
		})
		n.mu.Lock()
		switch {
		case errors.Is(err, grove.ErrOwnershipLost):
			h.lease.Lost = true
		case err == nil:
			h.lease.Beat, h.lease.RenewedAt = record.Lease.Beat, sent
			h.owner = placement.Ownership{Nodes: []string{record.Lease.Holder}, Epoch: record.Lease.Epoch}
		}
		n.mu.Unlock()
		if errors.Is(err, grove.ErrOwnershipLost) {
			n.emit(Event{Kind: EventOwnershipLost, Path: h.lease.Capability, Epoch: h.lease.Epoch})
		}
	}
}

// holds reports whether h may still act as the owner: active, renewed
// within the lease TTL, and still the record's owner at its epoch.
func (n *Node) holds(h *ownership) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return placement.LeaseHolds(h.lease, h.owner, n.cfg.NodeID, n.cfg.Now(), n.cfg.LeaseTTL)
}

func (n *Node) release(ctx context.Context, id string, h *ownership) error {
	n.mu.Lock()
	h.lease.Released = true
	if n.held[id] == h {
		delete(n.held, id)
	}
	n.mu.Unlock()
	_, err := n.mutate(ctx, id, func(record *Record, exists bool) error {
		if !exists || !record.OwnedBy(n.cfg.NodeID, h.lease.Epoch) {
			return grove.ErrOwnershipLost
		}
		record.Lease.Released = true
		return nil
	})
	if errors.Is(err, grove.ErrOwnershipLost) {
		err = nil
	}
	if err == nil {
		n.emit(Event{Kind: EventOwnershipReleased, Path: h.lease.Capability, Epoch: h.lease.Epoch})
	}
	return err
}

// materialize makes record's committed version local and verified, then
// points the work file at it. A work file that already builds on that
// version keeps its unsynced local changes.
func (n *Node) materialize(ctx context.Context, record Record) error {
	if err := n.disk.PutFile(localFile(record)); err != nil {
		return err
	}
	want := ""
	if record.Current != nil {
		if err := n.ensureLocal(ctx, record); err != nil {
			return err
		}
		want = record.Current.ID
	}
	base, exists, err := n.disk.WorkBase(record.FileID, record.Path)
	if err != nil {
		return err
	}
	if exists && base == want {
		return nil
	}
	return n.disk.Materialize(record.FileID, record.Path, want)
}

// ensureLocal makes this node hold a verified copy of record's current
// version, fetching it from a verified holder when the local copy is
// missing or corrupt, and records this node as a holder.
func (n *Node) ensureLocal(ctx context.Context, record Record) error {
	current := *record.Current
	_, has, err := n.disk.Version(record.FileID, current.ID)
	if err != nil {
		return err
	}
	if has {
		if err := n.disk.Verify(record.FileID, current); err != nil {
			n.emit(Event{Kind: EventCorrupt, Path: record.Path, Version: current.ID, Err: err.Error()})
			if err := n.disk.QuarantineVersion(record.FileID, current.ID); err != nil {
				return err
			}
			has = false
		}
	}
	if !has {
		if err := n.fetch(ctx, record, current); err != nil {
			return err
		}
	}
	if local, _ := n.disk.Current(record.FileID); local != current.ID {
		if err := n.disk.SetCurrent(record.FileID, current.ID); err != nil {
			return err
		}
	}
	if slices.Contains(record.Holders(current.ID), n.cfg.NodeID) {
		return nil
	}
	_, err = n.mutate(ctx, record.FileID, func(stored *Record, exists bool) error {
		if !exists || stored.Current == nil || stored.Current.ID != current.ID {
			return errSuperseded
		}
		stored.ReplicaSet = stored.WithReplica(n.cfg.NodeID, current.ID, ReplicaVerified)
		return nil
	})
	if errors.Is(err, errSuperseded) {
		return nil
	}
	return err
}

var errSuperseded = errors.New("version superseded")

// fetch copies version from the first live verified holder that serves it.
func (n *Node) fetch(ctx context.Context, record Record, version VersionMeta) error {
	if n.cfg.Peers == nil {
		return fmt.Errorf("%w: %s %s", ErrNoEligibleReplica, record.Path, version.ID)
	}
	live, _ := n.live()
	var errs []error
	for _, source := range record.Holders(version.ID) {
		if source == n.cfg.NodeID || !slices.Contains(live, source) {
			continue
		}
		err := n.receive(ctx, source, LocalVersion{
			ClusterID: record.ClusterID, Path: record.Path, FileID: record.FileID, Version: version,
		})
		if err == nil {
			n.emit(Event{Kind: EventReplicaTransferred, Path: record.Path, Version: version.ID, Peer: source})
			return nil
		}
		if errors.Is(err, ErrIntegrity) {
			n.emit(Event{Kind: EventCorrupt, Path: record.Path, Version: version.ID, Peer: source, Err: err.Error()})
		}
		errs = append(errs, fmt.Errorf("from %s: %w", source, err))
	}
	return fmt.Errorf("%w: %s %s: %w", ErrNoEligibleReplica, record.Path, version.ID, errors.Join(errs...))
}

func (n *Node) receive(ctx context.Context, source string, local LocalVersion) error {
	ctx, cancel := context.WithTimeout(ctx, n.cfg.TransferTimeout)
	defer cancel()
	return n.disk.Receive(local, func(offset int64) ([]byte, error) {
		if offset >= local.Version.Size {
			return nil, io.EOF
		}
		return n.cfg.Peers.ReadBlob(ctx, source, BlobRequest{
			FileID: local.FileID, VersionID: local.Version.ID, Offset: offset, Length: n.cfg.ChunkSize,
		})
	})
}

func (n *Node) newHandle(record Record, held *ownership) *Handle {
	h := &Handle{
		node: n, path: record.Path, id: record.FileID, held: held,
		local: n.disk.WorkPath(record.FileID, record.Path),
	}
	if record.Current != nil {
		current := *record.Current
		h.base = &current
	}
	return h
}

// sync publishes the work file of h as a new committed version.
func (n *Node) sync(ctx context.Context, h *Handle) (VersionMeta, error) {
	lock := n.fileLock(h.id)
	lock.Lock()
	defer lock.Unlock()
	version, err := n.publish(ctx, h)
	result := SyncResult{Version: version, At: n.cfg.Now()}
	if err != nil {
		result.Err = err.Error()
		n.emit(Event{Kind: EventSyncFailed, Path: h.path, Version: version.ID, Err: err.Error()})
	}
	n.mu.Lock()
	n.lastSync[h.id] = result
	n.mu.Unlock()
	return version, err
}

func (n *Node) publish(ctx context.Context, h *Handle) (VersionMeta, error) {
	if h.held != nil && !n.holds(h.held) {
		return VersionMeta{}, grove.ErrOwnershipLost
	}
	cluster, err := n.ensureCluster(ctx)
	if err != nil {
		return VersionMeta{}, err
	}
	n.emit(Event{Kind: EventSyncStarted, Path: h.path})
	staged, size, sum, err := n.disk.Snapshot(h.id, h.path)
	if err != nil {
		return VersionMeta{}, err
	}
	defer os.Remove(staged)
	var version VersionMeta
	record, err := n.mutate(ctx, h.id, func(record *Record, exists bool) error {
		if !exists {
			*record = newRecord(cluster, h.path, grove.ApplyFileOptions(h.options...))
		}
		if err := n.fence(h, *record); err != nil {
			return err
		}
		record.Generations++
		version = VersionMeta{
			ID: VersionIDFor(record.Generations, sum), Generation: record.Generations,
			Size: size, SHA256: sum, CreatedAt: n.cfg.Now().UTC(), Source: n.cfg.NodeID,
		}
		record.Staging = &version
		return nil
	})
	if err != nil {
		return VersionMeta{}, n.lost(h, err)
	}
	if err := n.disk.PutFile(localFile(record)); err != nil {
		return version, err
	}
	if err := n.disk.Install(LocalVersion{ClusterID: cluster, Path: h.path, FileID: h.id, Version: version}, staged); err != nil {
		return version, err
	}

	required := RequiredCopies(record.Durability, record.Replicas)
	live, _ := n.live()
	targets := ChooseTargets(live, n.cfg.NodeID, record, record.Replicas-1)
	if n.cfg.Peers == nil {
		targets = nil
	}
	results := make(chan replicaResult, len(targets))
	transferCtx, cancelTransfers := context.WithTimeout(context.WithoutCancel(ctx), n.cfg.TransferTimeout)
	request := ReplicateRequest{ClusterID: cluster, File: localFile(record), Version: version, Source: n.cfg.NodeID}
	for _, target := range targets {
		go func() {
			results <- replicaResult{node: target, err: n.cfg.Peers.Replicate(transferCtx, target, request)}
		}()
	}
	holders := []string{n.cfg.NodeID}
	received := 0
	var failures []error
wait:
	for len(holders) < required && received < len(targets) {
		select {
		case result := <-results:
			received++
			if result.err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", result.node, result.err))
				continue
			}
			holders = append(holders, result.node)
			n.emit(Event{Kind: EventReplicaTransferred, Path: h.path, Version: version.ID, Peer: result.node})
		case <-ctx.Done():
			failures = append(failures, ctx.Err())
			break wait
		}
	}
	pending := len(targets) - received
	if len(holders) < required {
		n.drain(results, pending, cancelTransfers)
		err := fmt.Errorf("%w: %s %s has %d of %d required copies", grove.ErrDurability,
			h.path, version.ID, len(holders), required)
		if len(failures) != 0 {
			err = fmt.Errorf("%w: %w", err, errors.Join(failures...))
		} else if len(targets) == 0 {
			err = fmt.Errorf("%w: no live peer can hold a replica", err)
		}
		return version, err
	}

	record, err = n.mutate(ctx, h.id, func(record *Record, exists bool) error {
		if !exists {
			return grove.ErrOwnershipLost
		}
		if err := n.fence(h, *record); err != nil {
			return err
		}
		if record.Staging == nil || record.Staging.ID != version.ID {
			return grove.ErrOwnershipLost
		}
		*record = record.Commit(version, holders, n.cfg.Retain)
		return nil
	})
	if err != nil {
		cancelTransfers()
		return version, n.lost(h, err)
	}
	if err := n.disk.SetCurrent(h.id, version.ID); err != nil {
		cancelTransfers()
		return version, err
	}
	if err := n.disk.SetWorkBase(h.id, version.ID); err != nil {
		cancelTransfers()
		return version, err
	}
	h.setBase(version)
	n.emit(Event{Kind: EventSyncCommitted, Path: h.path, Version: version.ID, Epoch: record.Lease.Epoch})
	// Holders learn of the commit before Sync returns, so a cold restart of
	// any of them recovers this version. It is best effort: reconciliation
	// catches up a holder that misses it.
	n.notifyCommitted(transferCtx, record, version, holders)
	n.finishReplication(record, version, results, pending, cancelTransfers)
	return version, nil
}

// notifyCommitted tells holders other than this node that version is
// committed.
func (n *Node) notifyCommitted(ctx context.Context, record Record, version VersionMeta, holders []string) {
	request := ReplicateRequest{
		ClusterID: record.ClusterID, File: localFile(record), Version: version,
		Source: n.cfg.NodeID, Committed: true,
	}
	var wg sync.WaitGroup
	for _, holder := range holders {
		if holder == n.cfg.NodeID {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = n.cfg.Peers.Replicate(ctx, holder, request)
		}()
	}
	wg.Wait()
}

type replicaResult struct {
	node string
	err  error
}

// finishReplication records the transfers still running after a commit and
// tells their nodes the version is committed, in the background.
func (n *Node) finishReplication(record Record, version VersionMeta,
	results chan replicaResult, pending int, cancel context.CancelFunc) {
	if pending == 0 {
		cancel()
		return
	}
	n.background.Add(1)
	go func() {
		defer n.background.Done()
		defer cancel()
		ctx, done := context.WithTimeout(context.Background(), n.cfg.TransferTimeout)
		defer done()
		var late []string
		for ; pending > 0; pending-- {
			result := <-results
			if result.err != nil {
				continue
			}
			late = append(late, result.node)
			n.emit(Event{Kind: EventReplicaTransferred, Path: record.Path, Version: version.ID, Peer: result.node})
			_, _ = n.mutate(ctx, record.FileID, func(stored *Record, exists bool) error {
				if !exists || stored.Current == nil || stored.Current.ID != version.ID {
					return errSuperseded
				}
				stored.ReplicaSet = stored.WithReplica(result.node, version.ID, ReplicaVerified)
				return nil
			})
		}
		n.notifyCommitted(ctx, record, version, late)
	}()
}

// drain lets transfers of a version that will not commit finish in the
// background so their goroutines end.
func (n *Node) drain(results chan replicaResult, pending int, cancel context.CancelFunc) {
	n.background.Add(1)
	go func() {
		defer n.background.Done()
		defer cancel()
		for ; pending > 0; pending-- {
			<-results
		}
	}()
}

// fence rejects a publish h may not make: an owned handle needs the record to
// name it the owner at its epoch, and an unowned one needs the file unowned
// and unchanged since it was opened.
func (n *Node) fence(h *Handle, record Record) error {
	if h.held != nil {
		if !record.OwnedBy(n.cfg.NodeID, h.held.lease.Epoch) || !n.holds(h.held) {
			return grove.ErrOwnershipLost
		}
		return nil
	}
	if record.Owned() {
		return fmt.Errorf("%w: %s owns %s", grove.ErrFileOwned, record.Lease.Holder, record.Path)
	}
	base := h.Base()
	if (record.Current == nil) != base.IsZero() || (record.Current != nil && record.Current.ID != base.ID) {
		return fmt.Errorf("%w: %s", grove.ErrFileChanged, record.Path)
	}
	return nil
}

// lost marks h's ownership lost when err says it was fenced.
func (n *Node) lost(h *Handle, err error) error {
	if h.held == nil || !errors.Is(err, grove.ErrOwnershipLost) {
		return err
	}
	n.mu.Lock()
	already := h.held.lease.Lost
	h.held.lease.Lost = true
	n.mu.Unlock()
	if !already {
		n.emit(Event{Kind: EventOwnershipLost, Path: h.path, Epoch: h.held.lease.Epoch})
	}
	return err
}

// waitReplicated waits until every configured replica holds version or a
// newer version supersedes it.
func (n *Node) waitReplicated(ctx context.Context, id string, version grove.Version) error {
	ticker := time.NewTicker(n.cfg.Poll)
	defer ticker.Stop()
	for {
		record, _, err := n.cfg.Catalog.Get(ctx, id)
		if err == nil && record.Current != nil {
			if record.Current.Generation > version.Generation ||
				len(record.Holders(version.ID)) >= record.Replicas {
				return nil
			}
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("wait for %s to replicate: %w", version.ID, ctx.Err())
		}
	}
}

// HandleReplicate serves a peer's ReplicateRequest: it pulls and verifies the
// version unless this node holds it, and records a committed version as this
// node's current one. It trusts no peer metadata it cannot check.
func (n *Node) HandleReplicate(ctx context.Context, request ReplicateRequest) error {
	cluster, err := n.ensureCluster(ctx)
	if err != nil {
		return err
	}
	if request.ClusterID != cluster {
		return fmt.Errorf("%w: peer cluster %s; this node is in %s", ErrLineage, request.ClusterID, cluster)
	}
	logical, err := CleanPath(request.File.Path)
	if err != nil {
		return err
	}
	if FileIDFor(logical) != request.File.FileID || !validVersionID(request.Version.ID) ||
		VersionIDFor(request.Version.Generation, request.Version.SHA256) != request.Version.ID {
		return fmt.Errorf("%w: replicate request does not describe a valid version", ErrIntegrity)
	}
	id := request.File.FileID
	lock := n.fileLock(id)
	lock.Lock()
	defer lock.Unlock()
	if _, ok, err := n.disk.File(id); err != nil {
		return err
	} else if !ok {
		if err := n.disk.PutFile(request.File); err != nil {
			return err
		}
	}
	if _, has, err := n.disk.Version(id, request.Version.ID); err != nil {
		return err
	} else if !has {
		if n.cfg.Peers == nil {
			return ErrNoEligibleReplica
		}
		local := LocalVersion{ClusterID: cluster, Path: logical, FileID: id, Version: request.Version}
		if err := n.receive(ctx, request.Source, local); err != nil {
			if errors.Is(err, ErrIntegrity) {
				n.emit(Event{Kind: EventCorrupt, Path: logical, Version: request.Version.ID, Peer: request.Source, Err: err.Error()})
			}
			return err
		}
	}
	if !request.Committed {
		return nil
	}
	return n.advanceCurrent(id, request.Version)
}

// advanceCurrent points this node's committed pointer at version unless it
// already points at a newer one.
func (n *Node) advanceCurrent(id string, version VersionMeta) error {
	current, err := n.disk.Current(id)
	if err != nil {
		return err
	}
	if current != "" {
		if local, ok, _ := n.disk.Version(id, current); ok && local.Version.Generation >= version.Generation {
			return nil
		}
	}
	return n.disk.SetCurrent(id, version.ID)
}

// HandleReadBlob serves part of a verified version to a peer.
func (n *Node) HandleReadBlob(request BlobRequest) ([]byte, error) {
	if !validFileID(request.FileID) || !validVersionID(request.VersionID) || request.Offset < 0 {
		return nil, fmt.Errorf("%w: invalid blob request", ErrIntegrity)
	}
	length := min(max(request.Length, 1), n.cfg.ChunkSize)
	return n.disk.ReadAt(request.FileID, request.VersionID, request.Offset, length)
}

var (
	fileIDPattern    = regexp.MustCompile(`^[0-9a-f]{32}$`)
	versionIDPattern = regexp.MustCompile(`^[0-9a-f]{16}-[0-9a-f]{1,12}$`)
)

func validFileID(id string) bool    { return fileIDPattern.MatchString(id) }
func validVersionID(id string) bool { return versionIDPattern.MatchString(id) }

// ensureCluster returns the cluster lineage, establishing it on first use.
// With no identity in the catalog, this node's disk lineage bootstraps it, or
// a new one does. A disk of another lineage is quarantined, never merged.
func (n *Node) ensureCluster(ctx context.Context) (string, error) {
	n.mu.Lock()
	cached := n.clusterID
	n.mu.Unlock()
	if cached != "" {
		return cached, nil
	}
	local, err := n.disk.ClusterID()
	if err != nil {
		return "", err
	}
	for attempt := 0; attempt < 2; attempt++ {
		record, err := n.cfg.Catalog.Cluster(ctx)
		if errors.Is(err, ErrNotFound) {
			id := local
			if id == "" {
				id = newClusterID()
			}
			err = n.cfg.Catalog.CreateCluster(ctx, ClusterRecord{
				Format: FormatVersion, ClusterID: id, CreatedAt: n.cfg.Now().UTC(),
			})
			if errors.Is(err, ErrConflict) {
				continue
			}
			if err != nil {
				return "", err
			}
			record.ClusterID = id
		} else if err != nil {
			return "", err
		}
		if local != "" && local != record.ClusterID {
			n.emit(Event{Kind: EventLineage, Err: fmt.Sprintf("disk lineage %s; cluster %s", local, record.ClusterID)})
			if err := n.disk.QuarantineAll(); err != nil {
				return "", err
			}
		}
		if local != record.ClusterID {
			if err := n.disk.SetClusterID(record.ClusterID, n.cfg.Now().UTC()); err != nil {
				return "", err
			}
		}
		n.mu.Lock()
		n.clusterID = record.ClusterID
		n.mu.Unlock()
		return record.ClusterID, nil
	}
	return "", fmt.Errorf("establish grove files cluster identity: %w", ErrConflict)
}

func newClusterID() string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

// mutate applies change to the stored record with compare-and-set, retrying
// on conflicts. change sees exists=false for a missing record and may
// create it; an error from change aborts without writing.
func (n *Node) mutate(ctx context.Context, id string, change func(record *Record, exists bool) error) (Record, error) {
	for attempt := 0; attempt < maxCatalogAttempts; attempt++ {
		record, revision, err := n.cfg.Catalog.Get(ctx, id)
		exists := err == nil
		if err != nil && !errors.Is(err, ErrNotFound) {
			return Record{}, err
		}
		if !exists {
			revision = 0
		}
		if err := change(&record, exists); err != nil {
			return record, err
		}
		if _, err := n.cfg.Catalog.Put(ctx, record, revision); err == nil {
			return record, nil
		} else if !errors.Is(err, ErrConflict) {
			return Record{}, err
		}
		if err := ctx.Err(); err != nil {
			return Record{}, err
		}
	}
	return Record{}, fmt.Errorf("update grove file record: %w", ErrConflict)
}

// observe tracks when this node first saw record's current lease, on its own
// clock, so lease expiry never depends on clocks agreeing across nodes.
func (n *Node) observe(record Record, now time.Time) placement.ObservedLease {
	n.mu.Lock()
	defer n.mu.Unlock()
	observed, ok := n.observed[record.FileID]
	if !ok || observed.Lease != record.Lease {
		observed = placement.ObservedLease{Lease: record.Lease, FirstSeen: now}
		n.observed[record.FileID] = observed
	}
	return observed
}

func (n *Node) live() ([]string, bool) {
	if n.cfg.Live == nil {
		return []string{n.cfg.NodeID}, true
	}
	nodes, ready := n.cfg.Live()
	if !ready {
		return []string{n.cfg.NodeID}, false
	}
	if !slices.Contains(nodes, n.cfg.NodeID) {
		nodes = append(slices.Clone(nodes), n.cfg.NodeID)
	}
	return nodes, true
}

func (n *Node) fileLock(id string) *sync.Mutex {
	n.mu.Lock()
	defer n.mu.Unlock()
	lock, ok := n.locks[id]
	if !ok {
		lock = &sync.Mutex{}
		n.locks[id] = lock
	}
	return lock
}

func (n *Node) emit(event Event) {
	if n.cfg.Events == nil {
		return
	}
	event.Node = n.cfg.NodeID
	event.At = n.cfg.Now()
	n.cfg.Events(event)
}

func newRecord(cluster, logical string, options grove.FileOptions) Record {
	return Record{
		Format: FormatVersion, ClusterID: cluster, Path: logical, FileID: FileIDFor(logical),
		Replicas: options.Replicas, Durability: options.Durability,
	}
}

func localFile(record Record) LocalFile {
	return LocalFile{
		Format: FormatVersion, Path: record.Path, FileID: record.FileID,
		Replicas: record.Replicas, Durability: record.Durability,
	}
}
