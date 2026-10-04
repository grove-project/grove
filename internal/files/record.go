// Package files implements Grove Files: cluster-managed local files with
// immutable committed versions, replication, fenced write ownership, and
// recovery from node disks (docs/architecture/files.md).
//
// The package owns the lifecycle. It reaches cluster metadata through the
// Catalog port and other nodes through the Peers port, so it does not depend
// on NATS; internal/systemnats adapts both.
package files

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/placement"
)

// FormatVersion is the version of every record Grove Files persists, in the
// catalog and on disk. Readers reject a newer format instead of guessing.
const FormatVersion = 1

var (
	// ErrNotFound is returned by a Catalog for a missing record.
	ErrNotFound = errors.New("not found")
	// ErrConflict is returned by a Catalog when a write's revision is stale.
	ErrConflict = errors.New("revision conflict")
	// ErrLineage is returned when local or peer state belongs to another
	// cluster.
	ErrLineage = errors.New("cluster lineage mismatch")
	// ErrNoEligibleReplica is returned when no reachable node holds a
	// verified copy of the current committed version.
	ErrNoEligibleReplica = errors.New("no eligible replica holds the committed version")
	// ErrIntegrity is returned when bytes do not match their recorded size
	// and checksum.
	ErrIntegrity = errors.New("integrity check failed")
	// ErrFormat is returned for a persisted record of an unknown format.
	ErrFormat = errors.New("unsupported grove files format")
)

// VersionMeta describes one immutable version of a file.
type VersionMeta struct {
	ID         string    `json:"id"`
	Generation uint64    `json:"generation"`
	Size       int64     `json:"size"`
	SHA256     string    `json:"sha256"`
	CreatedAt  time.Time `json:"created_at"`
	Source     string    `json:"source_node"`
}

// Public is v as the SDK reports it.
func (v VersionMeta) Public() grove.Version {
	return grove.Version{
		ID: v.ID, Generation: v.Generation, Size: v.Size, SHA256: v.SHA256,
		CreatedAt: v.CreatedAt, Source: v.Source,
	}
}

// ReplicaState is what the catalog knows about one node's copy.
type ReplicaState string

const (
	// ReplicaVerified means the node persisted the version and verified its
	// checksum.
	ReplicaVerified ReplicaState = "verified"
	// ReplicaStale means the node's newest verified copy is an older version.
	ReplicaStale ReplicaState = "stale"
)

// ReplicaStatus is one node's copy of a file.
type ReplicaStatus struct {
	Node      string       `json:"node_id"`
	VersionID string       `json:"version_id"`
	State     ReplicaState `json:"state"`
}

// Record is the authoritative catalog entry for one logical file. Its Lease
// is the fenced write ownership: every commit is a compare-and-set of the
// whole record that checks the lease, so a stale owner cannot publish.
type Record struct {
	Format     int              `json:"format"`
	ClusterID  string           `json:"cluster_id"`
	Path       string           `json:"path"`
	FileID     string           `json:"file_id"`
	Replicas   int              `json:"desired_replicas"`
	Durability grove.Durability `json:"durability_mode"`
	Lease      placement.Lease  `json:"owner"`
	// Generations is the last generation handed out. Every staged version
	// takes the next one, so abandoned versions never reuse a generation.
	Generations uint64          `json:"generations"`
	Current     *VersionMeta    `json:"current_version,omitempty"`
	Staging     *VersionMeta    `json:"staging_version,omitempty"`
	History     []VersionMeta   `json:"history,omitempty"`
	ReplicaSet  []ReplicaStatus `json:"replicas,omitempty"`
}

// ClusterRecord is the catalog's cluster identity. Files of one cluster
// lineage are never mixed with another's.
type ClusterRecord struct {
	Format    int       `json:"format"`
	ClusterID string    `json:"cluster_id"`
	CreatedAt time.Time `json:"created_at"`
}

// Owned reports whether the record names an unreleased owner.
func (r Record) Owned() bool { return r.Lease.Holder != "" && !r.Lease.Released }

// OwnedBy reports whether nodeID owns the record at epoch.
func (r Record) OwnedBy(nodeID string, epoch uint64) bool {
	return r.Owned() && r.Lease.Holder == nodeID && r.Lease.Epoch == epoch
}

// Holders lists the nodes holding a verified copy of versionID, sorted.
func (r Record) Holders(versionID string) []string {
	var nodes []string
	for _, replica := range r.ReplicaSet {
		if replica.VersionID == versionID && replica.State == ReplicaVerified {
			nodes = append(nodes, replica.Node)
		}
	}
	slices.Sort(nodes)
	return nodes
}

// WithReplica returns the replica set with node's entry set to versionID and
// state.
func (r Record) WithReplica(node, versionID string, state ReplicaState) []ReplicaStatus {
	set := slices.DeleteFunc(slices.Clone(r.ReplicaSet), func(s ReplicaStatus) bool { return s.Node == node })
	set = append(set, ReplicaStatus{Node: node, VersionID: versionID, State: state})
	slices.SortFunc(set, func(a, b ReplicaStatus) int { return strings.Compare(a.Node, b.Node) })
	return set
}

// Commit returns the record with version committed as current: the previous
// current moves to bounded history, the staging slot clears, and every other
// node's copy is marked stale until it reports the new version.
func (r Record) Commit(version VersionMeta, holders []string, retain int) Record {
	if r.Current != nil {
		r.History = append(slices.Clone(r.History), *r.Current)
		if retain > 0 && len(r.History) > retain {
			r.History = r.History[len(r.History)-retain:]
		}
	}
	current := version
	r.Current, r.Staging = &current, nil
	set := make([]ReplicaStatus, 0, len(r.ReplicaSet)+len(holders))
	for _, replica := range r.ReplicaSet {
		if !slices.Contains(holders, replica.Node) {
			replica.State = ReplicaStale
			set = append(set, replica)
		}
	}
	r.ReplicaSet = set
	for _, node := range holders {
		r.ReplicaSet = r.WithReplica(node, version.ID, ReplicaVerified)
	}
	return r
}

// RequiredCopies is how many verified copies, the writer's included, commit
// a version under durability with replicas configured.
func RequiredCopies(durability grove.Durability, replicas int) int {
	switch durability {
	case grove.Local:
		return 1
	case grove.Quorum:
		return replicas/2 + 1
	}
	return replicas
}

// ChooseTargets picks up to want nodes other than self from live to receive a
// new version. Nodes that already hold a copy of the file come first, as
// they are its configured replicas; the rest follow in node order.
func ChooseTargets(live []string, self string, record Record, want int) []string {
	known := make(map[string]bool)
	for _, replica := range record.ReplicaSet {
		known[replica.Node] = true
	}
	candidates := slices.DeleteFunc(slices.Clone(live), func(node string) bool { return node == self })
	slices.Sort(candidates)
	candidates = slices.Compact(candidates)
	slices.SortStableFunc(candidates, func(a, b string) int {
		switch {
		case known[a] && !known[b]:
			return -1
		case known[b] && !known[a]:
			return 1
		}
		return 0
	})
	if want < 0 {
		want = 0
	}
	if len(candidates) > want {
		candidates = candidates[:want]
	}
	return candidates
}

// RepairLeader is the one live node that tops up a file's replicas: its
// owner while the owner is live, otherwise the first live node holding the
// current version. It returns "" when no live node can.
func RepairLeader(record Record, live []string) string {
	if record.Current == nil {
		return ""
	}
	if record.Owned() && slices.Contains(live, record.Lease.Holder) &&
		slices.Contains(record.Holders(record.Current.ID), record.Lease.Holder) {
		return record.Lease.Holder
	}
	for _, node := range record.Holders(record.Current.ID) {
		if slices.Contains(live, node) {
			return node
		}
	}
	return ""
}

// VersionIDFor names the version at generation with checksum sum. Generation
// comes first, zero-padded, so IDs sort in commit order.
func VersionIDFor(generation uint64, sum string) string {
	if len(sum) > 12 {
		sum = sum[:12]
	}
	return fmt.Sprintf("%016x-%s", generation, sum)
}

// FileIDFor is the stable identifier of a logical path, safe as a directory
// name and a catalog key.
func FileIDFor(logical string) string {
	sum := sha256.Sum256([]byte(logical))
	return hex.EncodeToString(sum[:16])
}

// CleanPath validates a logical path and returns its canonical form. A
// logical path is relative, slash-separated, and stays inside the
// Grove-managed root.
func CleanPath(logical string) (string, error) {
	if logical == "" || strings.ContainsRune(logical, 0) || strings.Contains(logical, `\`) ||
		path.IsAbs(logical) {
		return "", fmt.Errorf("%w: %q", grove.ErrInvalidPath, logical)
	}
	cleaned := path.Clean(logical)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%w: %q", grove.ErrInvalidPath, logical)
	}
	return cleaned, nil
}
