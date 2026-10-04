package files

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// Recover publishes this node's locally committed files to a catalog that
// has no record of them, as after every node of the cluster stopped and the
// catalog was lost. The first node to recover a file becomes the source of
// the versions it can prove locally; a file the catalog already records is
// left to reconciliation, so stale disk state never overrides a live
// cluster.
func (n *Node) Recover(ctx context.Context) error {
	cluster, err := n.ensureCluster(ctx)
	if err != nil {
		return err
	}
	ids, err := n.disk.FileIDs()
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		if err := n.recoverFile(ctx, cluster, id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (n *Node) recoverFile(ctx context.Context, cluster, id string) error {
	file, ok, err := n.disk.File(id)
	if err != nil || !ok {
		return err
	}
	currentID, err := n.disk.Current(id)
	if err != nil || currentID == "" {
		return err
	}
	local, ok, err := n.disk.Version(id, currentID)
	if err != nil || !ok {
		return err
	}
	if local.ClusterID != cluster || local.FileID != id || FileIDFor(local.Path) != id {
		return fmt.Errorf("%w: local version %s of %s", ErrLineage, currentID, file.Path)
	}
	if _, _, err := n.cfg.Catalog.Get(ctx, id); !errors.Is(err, ErrNotFound) {
		return err
	}
	versions, err := n.disk.Versions(id)
	if err != nil {
		return err
	}
	record := Record{
		Format: FormatVersion, ClusterID: cluster, Path: file.Path, FileID: id,
		Replicas: file.Replicas, Durability: file.Durability,
		Generations: local.Version.Generation,
	}
	for _, version := range versions {
		record.Generations = max(record.Generations, version.Version.Generation)
		if version.Version.Generation < local.Version.Generation {
			record.History = append(record.History, version.Version)
		}
	}
	if len(record.History) > n.cfg.Retain {
		record.History = record.History[len(record.History)-n.cfg.Retain:]
	}
	current := local.Version
	record.Current = &current
	record.ReplicaSet = record.WithReplica(n.cfg.NodeID, current.ID, ReplicaVerified)
	if _, err := n.cfg.Catalog.Put(ctx, record, 0); err != nil {
		if errors.Is(err, ErrConflict) {
			return nil // another node recovered it first
		}
		return err
	}
	n.emit(Event{Kind: EventBootstrap, Path: file.Path, Version: current.ID})
	return nil
}

// Reconcile converges this node with the catalog: every file it stores
// reaches the current committed version, files whose replicas fell short are
// topped up by their repair leader, and superseded versions are pruned.
func (n *Node) Reconcile(ctx context.Context) error {
	cluster, err := n.ensureCluster(ctx)
	if err != nil {
		return err
	}
	records, err := n.cfg.Catalog.List(ctx)
	if err != nil {
		return err
	}
	stored, err := n.disk.FileIDs()
	if err != nil {
		return err
	}
	live, _ := n.live()
	now := n.cfg.Now()
	var errs []error
	for _, record := range records {
		if record.ClusterID != cluster {
			continue
		}
		n.observe(record, now)
		if record.Current == nil {
			continue
		}
		listed := slices.ContainsFunc(record.ReplicaSet, func(r ReplicaStatus) bool { return r.Node == n.cfg.NodeID })
		if !listed && !slices.Contains(stored, record.FileID) {
			continue
		}
		if err := n.converge(ctx, record); err != nil {
			errs = append(errs, err)
			continue
		}
		if RepairLeader(record, live) == n.cfg.NodeID {
			if err := n.repair(ctx, record, live); err != nil {
				errs = append(errs, err)
			}
		}
		if err := n.prune(record); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// converge brings this node's copy of record to its current version.
func (n *Node) converge(ctx context.Context, record Record) error {
	lock := n.fileLock(record.FileID)
	lock.Lock()
	defer lock.Unlock()
	if _, ok, err := n.disk.File(record.FileID); err != nil {
		return err
	} else if !ok {
		if err := n.disk.PutFile(localFile(record)); err != nil {
			return err
		}
	}
	before, _ := n.disk.Current(record.FileID)
	if err := n.ensureLocal(ctx, record); err != nil {
		return err
	}
	if before != record.Current.ID {
		n.emit(Event{Kind: EventReconciled, Path: record.Path, Version: record.Current.ID})
	}
	return nil
}

// repair replicates record's current version to live nodes until it has its
// configured replica count.
func (n *Node) repair(ctx context.Context, record Record, live []string) error {
	holders := record.Holders(record.Current.ID)
	healthy := slices.DeleteFunc(slices.Clone(holders), func(node string) bool { return !slices.Contains(live, node) })
	need := record.Replicas - len(healthy)
	if need <= 0 || n.cfg.Peers == nil {
		return nil
	}
	candidates := slices.DeleteFunc(slices.Clone(live), func(node string) bool { return slices.Contains(holders, node) })
	request := ReplicateRequest{
		ClusterID: record.ClusterID, File: localFile(record), Version: *record.Current,
		Source: n.cfg.NodeID, Committed: true,
	}
	var errs []error
	for _, target := range ChooseTargets(candidates, n.cfg.NodeID, record, need) {
		if err := n.cfg.Peers.Replicate(ctx, target, request); err != nil {
			errs = append(errs, fmt.Errorf("replicate %s to %s: %w", record.Path, target, err))
			continue
		}
		n.emit(Event{Kind: EventReplicaTransferred, Path: record.Path, Version: record.Current.ID, Peer: target})
		_, err := n.mutate(ctx, record.FileID, func(stored *Record, exists bool) error {
			if !exists || stored.Current == nil || stored.Current.ID != record.Current.ID {
				return errSuperseded
			}
			stored.ReplicaSet = stored.WithReplica(target, record.Current.ID, ReplicaVerified)
			return nil
		})
		if err != nil && !errors.Is(err, errSuperseded) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// prune removes local versions the catalog no longer retains. A version
// newer than any generation the catalog handed out belongs to a diverged
// history and is quarantined rather than deleted.
func (n *Node) prune(record Record) error {
	lock := n.fileLock(record.FileID)
	lock.Lock()
	defer lock.Unlock()
	keep := map[string]bool{record.Current.ID: true}
	for _, version := range record.History {
		keep[version.ID] = true
	}
	versions, err := n.disk.Versions(record.FileID)
	if err != nil {
		return err
	}
	for _, local := range versions {
		version := local.Version
		switch {
		case keep[version.ID]:
		case version.Generation > record.Generations:
			n.emit(Event{Kind: EventDiverged, Path: record.Path, Version: version.ID})
			if err := n.disk.QuarantineVersion(record.FileID, version.ID); err != nil {
				return err
			}
		default:
			if err := n.disk.RemoveVersion(record.FileID, version.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// FileStatus is what an operator sees of one file.
type FileStatus struct {
	Path            string
	FileID          string
	Current         *VersionMeta
	Owner           string
	Epoch           uint64
	DesiredReplicas int
	HealthyReplicas int
	Replicas        []ReplicaStatus
	// Local is the version this node holds as committed, or "".
	Local    string
	LastSync *SyncResult
}

// Status reports every file in the catalog as this node sees it.
func (n *Node) Status(ctx context.Context) ([]FileStatus, error) {
	records, err := n.cfg.Catalog.List(ctx)
	if err != nil {
		return nil, err
	}
	live, _ := n.live()
	statuses := make([]FileStatus, 0, len(records))
	for _, record := range records {
		status := FileStatus{
			Path: record.Path, FileID: record.FileID, Current: record.Current,
			DesiredReplicas: record.Replicas, Replicas: record.ReplicaSet,
		}
		if record.Owned() {
			status.Owner, status.Epoch = record.Lease.Holder, record.Lease.Epoch
		}
		if record.Current != nil {
			for _, node := range record.Holders(record.Current.ID) {
				if slices.Contains(live, node) {
					status.HealthyReplicas++
				}
			}
		}
		status.Local, _ = n.disk.Current(record.FileID)
		n.mu.Lock()
		if result, ok := n.lastSync[record.FileID]; ok {
			status.LastSync = &result
		}
		n.mu.Unlock()
		statuses = append(statuses, status)
	}
	return statuses, nil
}
