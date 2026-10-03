package controlplane

import (
	"sort"
	"time"

	"github.com/grove-project/grove/internal/placement"
)

// HealthState is one node's derived liveness observation for a member.
type HealthState string

const (
	// HealthHealthy means a heartbeat arrived within the configured deadline.
	HealthHealthy HealthState = "healthy"
	// HealthUnavailable means no heartbeat arrived within the configured
	// deadline.
	HealthUnavailable HealthState = "unavailable"
)

// ClusterNode combines authoritative membership identity with locally observed
// ephemeral health.
type ClusterNode struct {
	// NodeID is the node's stable logical identity.
	NodeID string `json:"node_id"`
	// AdvertisedEndpoint is the node's Grove transport endpoint.
	AdvertisedEndpoint string `json:"advertised_endpoint"`
	// Health is this observer's current derived liveness state.
	Health HealthState `json:"health"`
	// LastSeen is the receiver-side RFC3339 timestamp of the latest heartbeat.
	LastSeen string `json:"last_seen,omitempty"`
}

// ClusterView is one node's machine-readable membership and health view.
type ClusterView struct {
	// Ready reports whether authoritative membership has initialized.
	Ready bool `json:"ready"`
	// Nodes contains membership-scoped observations sorted by node ID.
	Nodes []ClusterNode `json:"nodes"`
	// Error describes the latest membership initialization failure.
	Error string `json:"error,omitempty"`
}

// EvaluateHealth derives the cluster view from membership and the
// receiver-side time each member's latest heartbeat arrived. A member is
// healthy when it is not leaving and was heard from within unavailableAfter of
// now; only members appear, whatever heartbeats arrived.
func EvaluateHealth(membership MembershipView, lastSeen map[string]time.Time, now time.Time, unavailableAfter time.Duration) ClusterView {
	nodes := make([]ClusterNode, 0, len(membership.Members))
	for _, member := range membership.Members {
		seen := lastSeen[member.NodeID]
		state := HealthUnavailable
		if !member.Leaving && !seen.IsZero() && now.Sub(seen) <= unavailableAfter {
			state = HealthHealthy
		}
		node := ClusterNode{
			NodeID:             member.NodeID,
			AdvertisedEndpoint: member.AdvertisedEndpoint,
			Health:             state,
		}
		if !seen.IsZero() {
			node.LastSeen = seen.Format(time.RFC3339Nano)
		}
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].NodeID < nodes[j].NodeID
	})
	return ClusterView{
		Ready: membership.Ready,
		Nodes: nodes,
		Error: membership.Error,
	}
}

// GateClusterView applies the serving gates to a ready view: below minNodes
// the cluster is still forming, and settled (when set) can hold it not-ready
// with its own reason. The view is returned unchanged otherwise.
func GateClusterView(view ClusterView, minNodes int, settled func(nodes int) error) ClusterView {
	if !view.Ready {
		return view
	}
	if len(view.Nodes) < minNodes {
		view.Ready = false
		view.Error = ClusterFormingError(len(view.Nodes), minNodes).Error()
	} else if settled != nil {
		if err := settled(len(view.Nodes)); err != nil {
			view.Ready = false
			view.Error = err.Error()
		}
	}
	return view
}

// Members is the cluster view as placement decides over it. A member is live
// while healthy, and has failed once it was heard from and is unavailable
// now; a member never heard from is still joining, not failed.
func Members(view ClusterView) []placement.Member {
	members := make([]placement.Member, 0, len(view.Nodes))
	for _, node := range view.Nodes {
		members = append(members, placement.Member{
			ID:     node.NodeID,
			Live:   node.Health == HealthHealthy,
			Failed: node.Health == HealthUnavailable && node.LastSeen != "",
		})
	}
	return members
}
