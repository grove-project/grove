package runtime

import (
	"errors"
	"strings"
	"testing"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/console"
)

func TestBuildApplicationLogsViewExplainsUnhealthyCluster(t *testing.T) {
	status := ClusterStatus{
		Health: "degraded",
		Ready:  true,
		Nodes: []NodeStatus{
			{
				NodeID: "node-2", Health: "unavailable", Error: "component request timeout",
				Components: []ComponentStatus{
					{ServiceID: grove.ServiceID(2), Name: "Inventory", WorkerID: "inventory-1", State: "failed", Error: "inventory.reservation_buffer must be zero or greater"},
				},
			},
		},
		Placements: []PlacementStatus{
			{ServiceID: grove.ServiceID(2), Name: "Inventory", NodeID: "node-2", Health: "unavailable", InvocationSubject: "_GROVE.inventory"},
		},
		Rollout: &RolloutStatus{
			Generation: 2,
			Phase:      "rolled-back",
			Failure: &RolloutFailure{
				Code: "candidate_startup_failed", Component: "Inventory",
				Field: "inventory.reservation_buffer", Message: "must be zero or greater",
			},
		},
	}
	nodes := []applicationNodeLogs{{
		NodeID: "node-2",
		Output: strings.Join([]string{
			`{"event":"ready","node_id":"node-2","system_nats_url":"nats://127.0.0.1:4222","system_nats_route_url":"nats-route://127.0.0.1:6222"}`,
			"grovlet: start component 2: inventory.reservation_buffer must be zero or greater",
			"JetStream placement watch unavailable",
		}, "\n"),
	}}
	view := buildApplicationLogsView(status, nil, nodes, "nats://127.0.0.1:4222", []console.DebugSession{
		{ServiceName: "Orders", NodeID: "node-2", WorkerID: "orders-1", DAPEndpoint: "127.0.0.1:40000"},
	}, "")

	for _, want := range []string{
		"node-2 is unavailable: component request timeout",
		"Inventory on node-2 is failed: inventory.reservation_buffer must be zero or greater",
		"Inventory placement on node-2 is unavailable",
		"rollout candidate_startup_failed component=Inventory field=inventory.reservation_buffer: must be zero or greater",
	} {
		if !containsApplicationLogLine(view.Causes, want) {
			t.Errorf("causes = %#v; want %q", view.Causes, want)
		}
	}
	if !containsApplicationLogLine(view.Application, "node-2 grovlet: start component 2") {
		t.Errorf("application logs = %#v; want worker failure", view.Application)
	}
	if !containsApplicationLogLine(view.Cluster, "node=node-2 event=ready") {
		t.Errorf("cluster logs = %#v; want lifecycle event", view.Cluster)
	}
	for _, want := range []string{"client-endpoint=nats://127.0.0.1:4222", "JetStream placement watch unavailable", "subject=_GROVE.inventory"} {
		if !containsApplicationLogLine(view.SystemNATS, want) {
			t.Errorf("System NATS logs = %#v; want %q", view.SystemNATS, want)
		}
	}

	rendered := renderApplicationLogs(view)
	for _, want := range []string{"WHY NOT HEALTHY", "ACTIVE DELVE / DAP SESSIONS", "127.0.0.1:40000", "APPLICATION", "CLUSTER", "SYSTEM NATS / CONTROL PLANE", "reservation_buffer"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered logs = %q; want %q", rendered, want)
		}
	}
}

func TestBuildApplicationLogsViewSurvivesStatusFailure(t *testing.T) {
	view := buildApplicationLogsView(
		ClusterStatus{},
		errors.New("Web status endpoint refused the connection"),
		[]applicationNodeLogs{{NodeID: "node-1", Output: "System NATS route failed"}},
		"nats://127.0.0.1:4222",
		nil,
		"",
	)
	if view.Health != "unknown" || !containsApplicationLogLine(view.Causes, "cluster status unavailable") {
		t.Fatalf("logs view = %#v; want status failure diagnosis", view)
	}
	if !containsApplicationLogLine(view.SystemNATS, "System NATS route failed") {
		t.Fatalf("System NATS logs = %#v; want raw diagnostics despite status failure", view.SystemNATS)
	}
}

func TestBuildApplicationLogsViewUsesLifecycleNodeIdentity(t *testing.T) {
	view := buildApplicationLogsView(
		ClusterStatus{Health: "healthy", Ready: true},
		nil,
		[]applicationNodeLogs{{
			NodeID: "node-1",
			Output: strings.Join([]string{
				`{"event":"ready","node_id":"node-4","system_nats_url":"nats://127.0.0.1:4222"}`,
				"worker lifecycle complete",
			}, "\n"),
		}},
		"nats://127.0.0.1:4222",
		nil,
		"",
	)
	if !containsApplicationLogLine(view.Cluster, "node=node-4 event=ready") ||
		!containsApplicationLogLine(view.SystemNATS, "node=node-4 client=") ||
		!containsApplicationLogLine(view.Application, "node-4 worker lifecycle complete") {
		t.Fatalf("logs view = %#v; want lifecycle identity node-4", view)
	}
	for _, lines := range [][]string{view.Cluster, view.SystemNATS, view.Application} {
		if containsApplicationLogLine(lines, "node=node-1") || containsApplicationLogLine(lines, "node-1 ") {
			t.Fatalf("logs view = %#v; stale slice identity node-1 must not be displayed", view)
		}
	}
}

func TestBuildApplicationLogsViewIgnoresStoppedUnplacedComponents(t *testing.T) {
	status := ClusterStatus{
		Health: "healthy",
		Ready:  true,
		Nodes: []NodeStatus{
			{NodeID: "node-1", Health: "healthy"},
			{
				NodeID: "node-3", Health: "healthy",
				Components: []ComponentStatus{
					{ServiceID: grove.ServiceID(1), Name: "Orders", State: "stopped"},
					{ServiceID: grove.ServiceID(2), Name: "Inventory", State: "stopped"},
					{ServiceID: grove.ServiceID(3), Name: "Payment", WorkerID: "payment-1", State: "healthy"},
					{ServiceID: grove.ServiceID(4), Name: "Shipping", State: "stopped"},
					{ServiceID: grove.ServiceID(5), Name: "Web", State: "stopped"},
				},
			},
		},
		Placements: []PlacementStatus{
			{ServiceID: grove.ServiceID(3), Name: "Payment", NodeID: "node-3", Health: "healthy"},
		},
	}
	view := buildApplicationLogsView(status, nil, nil, "nats://127.0.0.1:4222", nil, "")
	if view.Health != "healthy" || len(view.Causes) != 0 {
		t.Fatalf("logs view = %#v; stopped components without authoritative placement must not degrade health", view)
	}
	for _, component := range []string{"Orders", "Inventory", "Shipping", "Web"} {
		if !containsApplicationLogLine(view.Application, "service="+component+" worker= state=stopped") {
			t.Errorf("application diagnostics = %#v; want stopped %s visible", view.Application, component)
		}
	}
}

func TestBuildApplicationLogsViewNodeFilterPreservesScopeAcrossSections(t *testing.T) {
	status := ClusterStatus{
		Health: "degraded",
		Ready:  true,
		Nodes: []NodeStatus{
			{NodeID: "node-1", Health: "healthy", Components: []ComponentStatus{
				{ServiceID: grove.ServiceID(1), Name: "Orders", WorkerID: "orders-1", State: "healthy"},
			}},
			{NodeID: "node-2", Health: "unavailable", Error: "component request timeout", Components: []ComponentStatus{
				{ServiceID: grove.ServiceID(2), Name: "Inventory", WorkerID: "inventory-1", State: "failed"},
			}},
		},
		Placements: []PlacementStatus{
			{ServiceID: grove.ServiceID(1), Name: "Orders", NodeID: "node-1", Health: "healthy", InvocationSubject: "_GROVE.orders"},
			{ServiceID: grove.ServiceID(2), Name: "Inventory", NodeID: "node-2", Health: "unavailable", InvocationSubject: "_GROVE.inventory"},
		},
	}
	nodes := []applicationNodeLogs{
		{NodeID: "node-1", Output: "grovlet: node-1 steady state"},
		{NodeID: "node-2", Output: "grovlet: node-2 steady state"},
	}
	view := buildApplicationLogsView(status, nil, nodes, "nats://127.0.0.1:4222", nil, "node-1")

	if view.NodeFilter != "node-1" {
		t.Fatalf("NodeFilter = %q; want node-1", view.NodeFilter)
	}
	for _, lines := range [][]string{view.Cluster, view.Application, view.SystemNATS} {
		if containsApplicationLogLine(lines, "node-2") || containsApplicationLogLine(lines, "node=node-2") {
			t.Fatalf("scoped logs = %#v; must not include node-2 lines", lines)
		}
	}
	if !containsApplicationLogLine(view.Cluster, "node=node-1") || !containsApplicationLogLine(view.Application, "node=node-1") ||
		!containsApplicationLogLine(view.SystemNATS, "node=node-1") || !containsApplicationLogLine(view.Application, "node-1 grovlet: node-1 steady state") {
		t.Fatalf("scoped logs = cluster:%#v application:%#v systemNATS:%#v; want node-1 lines in every section",
			view.Cluster, view.Application, view.SystemNATS)
	}
	// Causes reason globally so a scoped view still explains a health banner
	// it shares with the unscoped view, even when the cause is another node.
	if !containsApplicationLogLine(view.Causes, "node-2 is unavailable") {
		t.Fatalf("causes = %#v; want the global node-2 failure even while scoped to node-1", view.Causes)
	}

	rendered := renderApplicationLogs(view)
	if !strings.Contains(rendered, "SCOPE") || !strings.Contains(rendered, "node-1") {
		t.Errorf("rendered logs = %q; want a node-1 scope banner", rendered)
	}
}

func containsApplicationLogLine(lines []string, fragment string) bool {
	for _, line := range lines {
		if strings.Contains(line, fragment) {
			return true
		}
	}
	return false
}
