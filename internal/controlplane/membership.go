package controlplane

import (
	"errors"
	"fmt"
)

// MinClusterNodes is the number of logical nodes a Grove cluster needs before
// it serves. With one control-plane voter per node, three nodes keep a quorum
// (and so a metadata leader) through the loss of any single node.
const MinClusterNodes = 3

// MaxControlStateReplicas is the most replicas Grove keeps of each piece of
// authoritative control state.
const MaxControlStateReplicas = 3

// MinControlStateReplicas is the replica count of control state before any
// node is active, as when the first node bootstraps the cluster.
const MinControlStateReplicas = 1

var (
	// ErrMembershipRecordInvalid is returned for a membership record without a
	// node identity or advertised endpoint.
	ErrMembershipRecordInvalid = errors.New("grove membership record is invalid")
	// ErrClusterForming is returned while the cluster has fewer than
	// MinClusterNodes registered nodes.
	ErrClusterForming = errors.New("grove cluster is forming")
	// ErrClusterSettling is returned while the bootstrap metadata witness has
	// not been released yet, so the control plane still has more voters than
	// nodes.
	ErrClusterSettling = errors.New("grove cluster is settling")
	// ErrControlPlaneUnavailable is returned when the replicated control state
	// has no leader, so placement and cluster operations cannot be confirmed.
	ErrControlPlaneUnavailable = errors.New("grove control plane has no leader")
)

// ClusterFormingError describes a cluster that has joined nodes of needed.
func ClusterFormingError(joined, needed int) error {
	return fmt.Errorf("%w: %d of %d nodes joined", ErrClusterForming, joined, needed)
}

// ClusterSettlingError describes a control plane with more voters than nodes.
func ClusterSettlingError(voters, nodes int) error {
	return fmt.Errorf("%w: %d control-plane voters for %d nodes (bootstrap witness not yet released)", ErrClusterSettling, voters, nodes)
}

// MembershipRecord is one logical node's authoritative cluster membership.
type MembershipRecord struct {
	// NodeID is the node's stable logical identity.
	NodeID string `json:"node_id"`
	// AdvertisedEndpoint is the node's Grove transport endpoint.
	AdvertisedEndpoint string `json:"advertised_endpoint"`
	// Leaving marks a node that is shutting down gracefully. It stays a member
	// until it retires, but no longer counts toward control-state replicas.
	Leaving bool `json:"leaving,omitempty"`
}

// MembershipView is one node's observation of authoritative membership.
type MembershipView struct {
	// Ready reports whether the initial membership snapshot completed.
	Ready bool `json:"ready"`
	// Members contains records sorted by node ID.
	Members []MembershipRecord `json:"members"`
	// Error describes the latest initialization or watch failure.
	Error string `json:"error,omitempty"`
}

// ValidMembershipRecord reports whether record names a node and its endpoint.
func ValidMembershipRecord(record MembershipRecord) bool {
	return record.NodeID != "" && record.AdvertisedEndpoint != ""
}

// ControlStateReplicas is the replica policy for authoritative control state:
// one replica per active (not leaving) member, between MinControlStateReplicas
// and MaxControlStateReplicas.
func ControlStateReplicas(records map[string]MembershipRecord) int {
	active := 0
	for _, record := range records {
		if !record.Leaving {
			active++
		}
	}
	return min(max(active, MinControlStateReplicas), MaxControlStateReplicas)
}
