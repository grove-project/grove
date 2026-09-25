package runtime

import (
	"testing"

	"github.com/grove-project/grove/internal/systemnats"
	groveshop "github.com/grove-project/grove/internal/testapp"
)

func TestBuildGroveShopStatusUsesPlacedComponentHealth(t *testing.T) {
	const (
		currentDigest   = "sha256:current"
		candidateDigest = "sha256:candidate"
	)
	cluster := systemnats.ClusterView{Ready: true, Nodes: []systemnats.ClusterNode{
		{NodeID: "node-1", Health: systemnats.HealthHealthy},
		{NodeID: "node-2", Health: systemnats.HealthHealthy},
		{NodeID: "node-3", Health: systemnats.HealthHealthy},
	}}
	placement := systemnats.PlacementView{Ready: true, Placements: []systemnats.PlacementRecord{
		{ServiceID: groveshop.ServiceOrders, NodeID: "node-1", InvocationSubject: "orders", ArtifactDigest: currentDigest},
		{ServiceID: groveshop.ServiceInventory, NodeID: "node-2", InvocationSubject: "inventory", ArtifactDigest: currentDigest},
	}}
	components := map[string]systemnats.ComponentView{
		"node-1": {Components: []systemnats.ComponentStatus{
			{ServiceID: groveshop.ServiceOrders, Name: "Orders", InvocationSubject: "orders", State: systemnats.ComponentHealthy},
			{ServiceID: groveshop.ServiceInventory, Name: "Inventory", InvocationSubject: "fallback-inventory", State: systemnats.ComponentStopped},
		}},
		"node-2": {Components: []systemnats.ComponentStatus{
			{ServiceID: groveshop.ServiceOrders, Name: "Orders", InvocationSubject: "fallback-orders", State: systemnats.ComponentStopped},
			{ServiceID: groveshop.ServiceInventory, Name: "Inventory", InvocationSubject: "inventory", State: systemnats.ComponentHealthy},
		}},
		"node-3": {Components: []systemnats.ComponentStatus{
			{ServiceID: groveshop.ServiceOrders, Name: "Orders", InvocationSubject: "observer-orders", State: systemnats.ComponentStopped},
			{ServiceID: groveshop.ServiceInventory, Name: "Inventory", InvocationSubject: "observer-inventory", State: systemnats.ComponentStopped},
		}},
	}
	deployments := systemnats.DeploymentView{
		Ready: true,
		Artifacts: []systemnats.DeploymentArtifact{
			{ApplicationID: "grove-shop", CodeVersion: "v1", ArtifactDigest: currentDigest, ConfigRevision: "acme-r42", ConfigDigest: "sha256:config-a"},
			{ApplicationID: "grove-shop", CodeVersion: "v1", ArtifactDigest: candidateDigest, ConfigRevision: "acme-broken-r43", ConfigDigest: "sha256:config-b"},
		},
		Rollouts: []systemnats.Rollout{{
			ApplicationID: "grove-shop", Generation: 2, CurrentArtifactDigest: currentDigest,
			CandidateArtifactDigest: candidateDigest, Phase: systemnats.RolloutPending,
		}},
	}
	status := buildStatus(cluster, placement, deployments, components, nil, ArtifactStatus{ApplicationID: "grove-shop"})
	if !status.Ready || status.Health != "healthy" {
		t.Fatalf("status readiness = %t, health = %q; want ready and healthy: %#v", status.Ready, status.Health, status)
	}
	if len(status.Placements) != 2 || status.Placements[0].Health != "healthy" || status.Placements[1].Health != "healthy" {
		t.Errorf("placed component health = %#v", status.Placements)
	}
	if status.ActiveArtifact == nil || status.ActiveArtifact.ConfigRevision != "acme-r42" || status.CandidateArtifact == nil || status.CandidateArtifact.ConfigRevision != "acme-broken-r43" {
		t.Errorf("artifact identities = active %#v candidate %#v", status.ActiveArtifact, status.CandidateArtifact)
	}
	if status.Rollout == nil || status.Rollout.Phase != string(systemnats.RolloutPending) || status.Rollout.Generation != 2 {
		t.Errorf("rollout = %#v", status.Rollout)
	}
}
