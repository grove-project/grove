package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/grove-project/grove/internal/systemnats"
)

func TestBuildClusterViewSummarizesHealthNodesAndServices(t *testing.T) {
	status := ClusterStatus{
		Health: "degraded",
		Nodes: []NodeStatus{
			{NodeID: "node-1", Health: string(systemnats.HealthHealthy), LastSeen: "2026-01-01T00:00:00Z"},
			{NodeID: "node-2", Health: string(systemnats.HealthUnavailable), Error: "component request timeout"},
		},
		Placements: []PlacementStatus{
			{NodeID: "node-1", Health: string(systemnats.ComponentHealthy)},
			{NodeID: "node-2", Health: "unavailable"},
		},
		ActiveArtifact: &ArtifactStatus{CodeVersion: "v0.2.0", ConfigRevision: "acme-r42"},
	}
	hosted := map[string][]applicationHostedRow{
		"node-1": {{Service: "Orders", Handler: "CreateOrder", Status: "healthy", Owner: true}},
	}
	startedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := startedAt.Add(time.Hour + 42*time.Minute + 18*time.Second)

	view := buildClusterView(status, hosted, startedAt, now)

	if view.Health != "degraded" || view.NodesHealthy != 1 || view.NodesTotal != 2 {
		t.Fatalf("cluster summary = %#v", view)
	}
	if view.ServicesHealthy != 1 || view.ServicesTotal != 2 {
		t.Errorf("service summary = %#v", view)
	}
	if view.Version != "v0.2.0" || view.ConfigRevision != "acme-r42" {
		t.Errorf("artifact identity = %#v", view)
	}
	if view.Uptime != "01:42:18" {
		t.Errorf("uptime = %q; want 01:42:18", view.Uptime)
	}
	if len(view.Nodes) != 2 || view.Nodes[0].NodeID != "node-1" || view.Nodes[1].NodeID != "node-2" {
		t.Fatalf("nodes = %#v; want sorted node-1, node-2", view.Nodes)
	}
	if view.Nodes[0].LastSeen != "2026-01-01T00:00:00Z" || len(view.Nodes[0].Hosted) != 1 {
		t.Errorf("node-1 = %#v; want its last-seen timestamp and hosted placement", view.Nodes[0])
	}
	if view.Nodes[1].Error != "component request timeout" {
		t.Errorf("node-2 = %#v; want its runtime error surfaced", view.Nodes[1])
	}
}

func TestBuildClusterViewDefaultsHealthWhenUnset(t *testing.T) {
	view := buildClusterView(ClusterStatus{}, nil, time.Time{}, time.Now())
	if view.Health != "unknown" || view.Uptime != "" || len(view.Nodes) != 0 {
		t.Errorf("empty cluster view = %#v", view)
	}
}

func TestFormatLastHeartbeat(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)
	seen := now.Add(-423 * time.Millisecond).Format(time.RFC3339Nano)
	if got := formatLastHeartbeat(seen, now); got != "423ms ago" {
		t.Errorf("formatLastHeartbeat(%q) = %q; want 423ms ago", seen, got)
	}
	if got := formatLastHeartbeat("", now); got != "-" {
		t.Errorf("formatLastHeartbeat(\"\") = %q; want -", got)
	}
	if got := formatLastHeartbeat("not-a-timestamp", now); got != "-" {
		t.Errorf("formatLastHeartbeat(garbage) = %q; want -", got)
	}
}

func TestRenderClusterNodeDetailExposesRuntimeStateAndHostedServices(t *testing.T) {
	cluster := applicationClusterView{Version: "v0.2.0", ConfigRevision: "acme-r42"}
	node := applicationClusterNodeRow{
		NodeID:   "node-2",
		Health:   string(systemnats.HealthHealthy),
		LastSeen: time.Now().Add(-423 * time.Millisecond).Format(time.RFC3339Nano),
		Hosted: []applicationHostedRow{
			{Service: "Orders", Handler: "CreateOrder", Status: "healthy", Owner: true},
		},
	}
	rendered := renderClusterNodeDetail(node, cluster, time.Now())
	for _, want := range []string{"STATUS", "HEALTHY", "VERSION", "v0.2.0", "CONFIG", "acme-r42", "SERVICES", "Orders", "CreateOrder", "owner", "RUNTIME", "Grovelet", "System NATS", "reachable", "Last heartbeat"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered node detail = %q; want %q", rendered, want)
		}
	}
}

func TestRenderClusterNodeDetailSurfacesUnreachableSystemNATS(t *testing.T) {
	node := applicationClusterNodeRow{NodeID: "node-3", Health: "unavailable", Error: "component request timeout"}
	rendered := renderClusterNodeDetail(node, applicationClusterView{}, time.Now())
	if !strings.Contains(rendered, "unreachable: component request timeout") {
		t.Errorf("rendered node detail = %q; want the unreachable System NATS reason", rendered)
	}
	if !strings.Contains(rendered, "No services hosted on this node.") {
		t.Errorf("rendered node detail = %q; want an empty-hosted placeholder", rendered)
	}
}
