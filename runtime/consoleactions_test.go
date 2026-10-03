package runtime

import (
	"slices"
	"strings"
	"testing"

	"github.com/grove-project/grove/console"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/consoleview"
	"github.com/grove-project/grove/internal/controlplane"
	"github.com/grove-project/grove/internal/systemnats"
)

// registeredConsoleActions registers the console's actions with both startup
// actions available, as a process that may start or join a cluster does.
func registeredConsoleActions(t *testing.T) (*applicationController, map[string]console.Action) {
	t.Helper()
	controller := newApplicationController("", "")
	controller.startAvailable, controller.joinAvailable = true, true
	controller.startup.Config = &artifact.ConfigMetadata{Facts: map[string]string{"cluster.name": "acme"}}
	var registry console.Registry
	if err := registerApplicationConsoleActions(&registry, controller); err != nil {
		t.Fatal(err)
	}
	actions := map[string]console.Action{}
	for _, action := range registry.Actions() {
		actions[action.Name] = action
	}
	return controller, actions
}

// The runtime, not the TUI, declares each action's key, screen and inputs.
func TestConsoleActionsDeclareTheirPresentation(t *testing.T) {
	_, actions := registeredConsoleActions(t)
	keys := map[rune]string{}
	for _, action := range actions {
		if action.Key != 0 {
			if other, taken := keys[action.Key]; taken {
				t.Errorf("key %q is declared by both %s and %s", action.Key, other, action.Name)
			}
			keys[action.Key] = action.Name
		}
	}
	wantKeys := map[rune]string{
		's': "cluster.status", 'a': "debug.attach", 'l': "logs.view",
		'o': "app.overview", 'p': "services.view", 'n': "cluster.nodes",
	}
	for key, name := range wantKeys {
		if keys[key] != name {
			t.Errorf("key %q = %q; want %s", key, keys[key], name)
		}
	}
	if len(keys) != len(wantKeys) {
		t.Errorf("keys = %v; want %v", keys, wantKeys)
	}
	views := map[string]console.View{
		"logs.view": console.ViewLogs, "services.view": console.ViewServices, "cluster.nodes": console.ViewNodes,
		"app.overview": console.ViewApp, "app.config": console.ViewApp, "app.ingress": console.ViewApp, "app.version": console.ViewApp,
	}
	for name, action := range actions {
		if action.View != views[name] {
			t.Errorf("%s view = %q; want %q", name, action.View, views[name])
		}
	}

	// Each form's defaults are arguments its own handler accepts.
	defaults := func(name string) []string {
		t.Helper()
		action := actions[name]
		values := make([]string, len(action.Inputs))
		for i, input := range action.Inputs {
			value, err := input.Default()
			if err != nil {
				t.Fatalf("%s input %q default: %v", name, input.Label, err)
			}
			values[i] = value
		}
		return console.Args(action.Inputs, values)
	}
	if path, err := parseRolloutArguments(defaults("rollout.start")); err != nil || path != "configs/acme.yaml" {
		t.Errorf("rollout.start defaults parse as (%q, %v)", path, err)
	}
	start := defaults("cluster.start")
	if address, nodes, err := parseStartArguments(start); err != nil || nodes != systemnats.MinClusterNodes || !strings.HasPrefix(address, "127.0.0.1:") {
		t.Errorf("cluster.start defaults %q parse as (%q, %d, %v)", start, address, nodes, err)
	}
	if nodes, err := parseJoinArguments(defaults("cluster.join")); err != nil || nodes != 1 {
		t.Errorf("cluster.join defaults parse as (%d, %v)", nodes, err)
	}
	if service, listen, _, err := parseDebugAttachArguments(defaults("debug.attach")); err != nil || service != "orders" || listen != "127.0.0.1:40000" {
		t.Errorf("debug.attach defaults parse as (%q, %q, %v)", service, listen, err)
	}
}

// Before this process enters a cluster only its startup action is offered;
// afterwards every visible action except the startup actions is.
func TestOfferedActionsGateStartup(t *testing.T) {
	controller, actions := registeredConsoleActions(t)
	registered := make([]console.Action, 0, len(actions))
	for _, action := range actions {
		registered = append(registered, action)
	}
	if got := offeredActions(registered, "cluster.start"); !slices.Equal(got, []string{"cluster.start"}) {
		t.Errorf("startup offer = %v; want cluster.start only", got)
	}
	normal := offeredActions(registered, "")
	for _, name := range normal {
		if name == "cluster.start" || name == "cluster.join" || actions[name].Hidden {
			t.Errorf("normal offer includes %s", name)
		}
	}
	if !slices.Contains(normal, "cluster.status") || !slices.Contains(normal, "logs.view") {
		t.Errorf("normal offer = %v; want the visible actions", normal)
	}

	model, err := controller.readModel(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if model.StartupAction != "cluster.join" || !model.StartupDiscovered || !slices.Equal(model.Actions, []string{"cluster.join"}) {
		t.Errorf("join model = %q discovered=%t actions=%v; want only cluster.join", model.StartupAction, model.StartupDiscovered, model.Actions)
	}
	controller.startAvailable, controller.joinAvailable = false, false
	if model, err = controller.readModel(t.Context()); err != nil {
		t.Fatal(err)
	}
	if model.StartupAction != "" || slices.Contains(model.Actions, "cluster.join") || !slices.Contains(model.Actions, "cluster.status") {
		t.Errorf("attached model actions = %v; want the normal offer", model.Actions)
	}
}

// Results describe themselves for interactive frontends, in the words the
// console has always used.
func TestConsoleResultsSummarizeThemselves(t *testing.T) {
	previous := activeApplication
	t.Cleanup(func() { activeApplication = previous })
	activeApplication = Definition{}
	for _, test := range []struct {
		result console.Summarizer
		want   string
	}{
		{rolloutActionResult{State: "active", WebURL: "http://127.0.0.1:8080"}, "Rollout: active\nWeb UI: http://127.0.0.1:8080"},
		{debugDemoActionResult{State: "ready", WebURL: "http://w"}, "Debug demo: ready\nWeb UI: http://w"},
		{debugAttachResult{ServiceName: "Orders", NodeID: "node-2", WorkerID: "orders-1", DAPEndpoint: "127.0.0.1:40000"}, "Debugger ready: Orders on node-2/orders-1\nDAP: 127.0.0.1:40000"},
		{applicationJoinResult{State: "joined", NodeIDs: []string{"node-4", "node-5"}}, "Cluster: joined\nNodes: node-4, node-5"},
		{ClusterStatus{Health: "healthy", Nodes: make([]NodeStatus, 3), Placements: make([]PlacementStatus, 4)}, "Cluster: healthy\nNodes: 3\nServices: 4"},
		{resilienceActionResult{FailedNodeID: "node-1", RecoveredNodeID: "node-2", Result: "ok"}, "Service recovered: node-1 -> node-2\nResult: ok"},
	} {
		if got := test.result.Summary(); got != test.want {
			t.Errorf("%T summary = %q; want %q", test.result, got, test.want)
		}
	}
}

// The console view model names the isolated execution mode the control
// plane reports.
func TestConsoleViewExecutionModeMatchesControlPlane(t *testing.T) {
	if consoleview.ExecutionIsolated != string(controlplane.ExecutionIsolatedProcess) {
		t.Fatalf("consoleview.ExecutionIsolated = %q; controlplane says %q", consoleview.ExecutionIsolated, controlplane.ExecutionIsolatedProcess)
	}
}
