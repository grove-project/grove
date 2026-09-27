package runtime

import (
	"testing"

	"github.com/grove-project/grove/internal/systemnats"
)

const recoveryArtifactDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestSelectRecoveryUsesFirstHealthyNode(t *testing.T) {
	cluster := systemnats.ClusterView{
		Ready: true,
		Nodes: []systemnats.ClusterNode{
			{NodeID: "node-a", Health: systemnats.HealthHealthy, LastSeen: "now"},
			{NodeID: "node-b", Health: systemnats.HealthUnavailable, LastSeen: "before"},
			{NodeID: "node-c", Health: systemnats.HealthHealthy, LastSeen: "now"},
		},
	}
	want := systemnats.PlacementRecord{ServiceID: 2, NodeID: "node-b", InvocationSubject: "inventory.node-b", ArtifactDigest: recoveryArtifactDigest}
	placement := systemnats.PlacementView{Ready: true, Placements: []systemnats.PlacementRecord{
		{ServiceID: 1, NodeID: "node-a", InvocationSubject: "orders.node-a", ArtifactDigest: recoveryArtifactDigest},
		want,
	}}

	got, ok := selectRecovery("node-a", cluster, placement)
	if !ok || got != want {
		t.Fatalf("selectRecovery() = %#v, %t; want %#v, true", got, ok, want)
	}
	if got, ok := selectRecovery("node-c", cluster, placement); ok {
		t.Errorf("non-coordinator selection = %#v, true; want no decision", got)
	}
}

func TestSelectRecoveryWaitsForPriorHealth(t *testing.T) {
	cluster := systemnats.ClusterView{
		Ready: true,
		Nodes: []systemnats.ClusterNode{
			{NodeID: "node-a", Health: systemnats.HealthHealthy, LastSeen: "now"},
			{NodeID: "node-b", Health: systemnats.HealthUnavailable},
		},
	}
	placement := systemnats.PlacementView{Ready: true, Placements: []systemnats.PlacementRecord{{
		ServiceID: 2, NodeID: "node-b", InvocationSubject: "inventory.node-b", ArtifactDigest: recoveryArtifactDigest,
	}}}
	if got, ok := selectRecovery("node-a", cluster, placement); ok {
		t.Errorf("startup selection = %#v, true; want no decision before node-b was healthy", got)
	}
	cluster.Nodes[1].LastSeen = "before"
	if _, ok := selectRecovery("node-a", cluster, placement); !ok {
		t.Error("selection after observed failure = false; want true")
	}
}

// Recovery must be able to start components after a node loss even while the
// control plane has no leader: selection uses the last-known placement records.
func TestSelectRecoveriesUsesLastKnownPlacementWithoutLeader(t *testing.T) {
	cluster := systemnats.ClusterView{
		Ready: true,
		Nodes: []systemnats.ClusterNode{
			{NodeID: "node-a", Health: systemnats.HealthUnavailable, LastSeen: "before"},
			{NodeID: "node-b", Health: systemnats.HealthHealthy, LastSeen: "now"},
			{NodeID: "node-c", Health: systemnats.HealthHealthy, LastSeen: "now"},
		},
	}
	lost1 := systemnats.PlacementRecord{ServiceID: 1, NodeID: "node-a", InvocationSubject: "orders.node-a", ArtifactDigest: recoveryArtifactDigest}
	lost2 := systemnats.PlacementRecord{ServiceID: 5, NodeID: "node-a", InvocationSubject: "web.node-a", ArtifactDigest: recoveryArtifactDigest}
	kept := systemnats.PlacementRecord{ServiceID: 2, NodeID: "node-c", InvocationSubject: "inventory.node-c", ArtifactDigest: recoveryArtifactDigest}
	// The placement view lost its leader: not ready, records are last-known.
	placement := systemnats.PlacementView{Ready: false, Placements: []systemnats.PlacementRecord{lost1, kept, lost2}}

	got := selectRecoveries("node-b", cluster, placement)
	if len(got) != 2 || got[0] != lost1 || got[1] != lost2 {
		t.Fatalf("selectRecoveries(node-b) = %#v; want both records of lost node-a", got)
	}
	if got := selectRecoveries("node-c", cluster, placement); len(got) != 0 {
		t.Errorf("non-coordinator selection = %#v; want none", got)
	}
	cluster.Ready = false
	if got := selectRecoveries("node-b", cluster, placement); len(got) != 0 {
		t.Errorf("selection before membership is ready = %#v; want none", got)
	}
}
