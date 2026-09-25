package console_test

import (
	"context"
	"strings"
	"testing"

	"github.com/grove-project/grove/console"
)

func TestTUIRendersStateAndUsesRegisteredAction(t *testing.T) {
	var selected string
	var registry console.Registry
	if err := registry.Register(console.Action{
		Name: "rollout.start", Label: "New rollout", Section: "Deployments",
		Handler: func(_ context.Context, args []string) (any, error) {
			selected = args[0]
			return "started", nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	tui, err := console.NewTUI(&registry, func(context.Context) (console.Model, error) {
		return console.Model{
			Application: "Grove Shop", Sections: []string{"Cluster", "Services", "Deployments", "Application"}, Health: "healthy",
			NodesHealthy: 3, NodesTotal: 3, ServicesHealthy: 5, ServicesTotal: 5,
			ActiveVersion: "v0.1.0", ConfigRevision: "acme-r42", CandidateRevision: "acme-r43", RolloutPhase: "pending",
			IngressURL: "http://127.0.0.1:8080",
			DebugSessions: []console.DebugSession{
				{ServiceName: "Orders", NodeID: "node-2", WorkerID: "orders-1", DAPEndpoint: "127.0.0.1:40000"},
			},
			LastEvent: "inventory recovered on node-1",
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := tui.Render(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Grove Grove Shop", "Cluster  healthy", "Nodes    3 / 3 healthy",
		"Services 5 / 5 healthy", "Version  v0.1.0", "Config   acme-r42",
		"Ingress  http://127.0.0.1:8080", "Debugger Orders node-2/orders-1 DAP 127.0.0.1:40000", "Candidate acme-r43", "Rollout  pending",
		"inventory recovered on node-1", "Services", "Deployments", "New rollout  [rollout.start]",
	} {
		if !strings.Contains(snapshot, want) {
			t.Errorf("Render() = %q; want %q", snapshot, want)
		}
	}
	selectedSnapshot, err := tui.RenderSelected(t.Context(), "rollout.start")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(selectedSnapshot, "> New rollout  [rollout.start]") {
		t.Errorf("RenderSelected() = %q; want selected rollout marker", selectedSnapshot)
	}
	result, err := tui.Select(t.Context(), "rollout.start", []string{"configs/acme.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if result != "started" || selected != "configs/acme.yaml" {
		t.Errorf("Select() = %q, selected = %q; want shared rollout handler", result, selected)
	}
}

func TestTUIExcludesHiddenActionsFromNavigationButKeepsThemInvokable(t *testing.T) {
	var registry console.Registry
	var invoked []string
	if err := registry.Register(console.Action{
		Name: "cluster.status", Label: "Status", Section: "Cluster",
		Handler: func(context.Context, []string) (any, error) { return "healthy", nil },
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(console.Action{
		Name: "cluster.restart", Label: "Restart cluster", Section: "Cluster", Hidden: true,
		Handler: func(context.Context, []string) (any, error) {
			invoked = append(invoked, "cluster.restart")
			return "restarted", nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	tui, err := console.NewTUI(&registry, func(context.Context) (console.Model, error) {
		return console.Model{Application: "Grove Test App", Sections: []string{"Cluster"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if actions := tui.Actions(); len(actions) != 1 || actions[0].Name != "cluster.status" {
		t.Errorf("Actions() = %#v; want only the visible cluster.status action", actions)
	}
	snapshot, err := tui.Render(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(snapshot, "Restart cluster") {
		t.Errorf("Render() = %q; must not list the hidden cluster.restart action", snapshot)
	}
	if !strings.Contains(snapshot, "Status  [cluster.status]") {
		t.Errorf("Render() = %q; want the visible cluster.status action", snapshot)
	}

	result, err := tui.Select(t.Context(), "cluster.restart", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != "restarted" || len(invoked) != 1 || invoked[0] != "cluster.restart" {
		t.Errorf("Select() on hidden action = %q, invoked = %v; want it to still run", result, invoked)
	}
}

func TestTUIRendersClusterStartupContracts(t *testing.T) {
	var registry console.Registry
	for _, action := range []console.Action{
		{Name: "cluster.start", Label: "Start new cluster", Section: "Cluster", Handler: func(context.Context, []string) (any, error) { return nil, nil }},
		{Name: "cluster.join", Label: "Join cluster", Section: "Cluster", Handler: func(context.Context, []string) (any, error) { return nil, nil }},
		{Name: "cluster.status", Label: "Status", Section: "Cluster", Handler: func(context.Context, []string) (any, error) { return nil, nil }},
	} {
		if err := registry.Register(action); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name  string
		model console.Model
		want  []string
		not   []string
	}{
		{
			name:  "new cluster",
			model: console.Model{Application: "GroveShop", StartupAction: "cluster.start", StartupBuild: "a82f19c"},
			want:  []string{"No GroveShop cluster discovered", "> Start new cluster", "Application  GroveShop", "Build        a82f19c", "Enter select"},
			not:   []string{"Join cluster", "Status  [cluster.status]"},
		},
		{
			name:  "join cluster",
			model: console.Model{Application: "GroveShop", StartupAction: "cluster.join", StartupCluster: "groveshop-local", StartupNodes: 1, StartupBuild: "a82f19c", StartupStatus: "Healthy"},
			want:  []string{"Grove cluster discovered", "Cluster  groveshop-local", "Nodes    1", "Build    a82f19c", "Status   Healthy", "> Join cluster", "Enter join   Esc cancel"},
			not:   []string{"Start new cluster", "Status  [cluster.status]"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tui, err := console.NewTUI(&registry, func(context.Context) (console.Model, error) { return test.model, nil })
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := tui.Render(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range test.want {
				if !strings.Contains(snapshot, want) {
					t.Errorf("startup Render() = %q; want %q", snapshot, want)
				}
			}
			for _, unwanted := range test.not {
				if strings.Contains(snapshot, unwanted) {
					t.Errorf("startup Render() = %q; must not contain %q", snapshot, unwanted)
				}
			}
		})
	}
}
