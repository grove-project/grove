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
			Application: "Grove Shop", Health: "healthy",
			NodesHealthy: 3, NodesTotal: 3, ServicesHealthy: 5, ServicesTotal: 5,
			ActiveVersion: "v0.1.0", ConfigRevision: "acme-r42",
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
		"GroveShop Grove Shop", "Cluster  healthy", "Nodes    3 / 3 healthy",
		"Services 5 / 5 healthy", "Version  v0.1.0", "Config   acme-r42",
		"inventory recovered on node-1", "Deployments", "New rollout  [rollout.start]",
	} {
		if !strings.Contains(snapshot, want) {
			t.Errorf("Render() = %q; want %q", snapshot, want)
		}
	}
	result, err := tui.Select(t.Context(), "rollout.start", []string{"configs/acme.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if result != "started" || selected != "configs/acme.yaml" {
		t.Errorf("Select() = %q, selected = %q; want shared rollout handler", result, selected)
	}
}
