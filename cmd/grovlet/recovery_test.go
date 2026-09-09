package main

import (
	"testing"

	"github.com/grove-project/grove/internal/systemnats"
)

func TestSelectRecoveryUsesFirstHealthyNode(t *testing.T) {
	cluster := systemnats.ClusterView{
		Ready: true,
		Nodes: []systemnats.ClusterNode{
			{NodeID: "node-a", Health: systemnats.HealthHealthy, LastSeen: "now"},
			{NodeID: "node-b", Health: systemnats.HealthUnavailable, LastSeen: "before"},
			{NodeID: "node-c", Health: systemnats.HealthHealthy, LastSeen: "now"},
		},
	}
	want := systemnats.PlacementRecord{ServiceID: 2, NodeID: "node-b", InvocationSubject: "inventory.node-b"}
	placement := systemnats.PlacementView{Ready: true, Placements: []systemnats.PlacementRecord{
		{ServiceID: 1, NodeID: "node-a", InvocationSubject: "orders.node-a"},
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
		ServiceID: 2, NodeID: "node-b", InvocationSubject: "inventory.node-b",
	}}}
	if got, ok := selectRecovery("node-a", cluster, placement); ok {
		t.Errorf("startup selection = %#v, true; want no decision before node-b was healthy", got)
	}
	cluster.Nodes[1].LastSeen = "before"
	if _, ok := selectRecovery("node-a", cluster, placement); !ok {
		t.Error("selection after observed failure = false; want true")
	}
}
