package console_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/grove-project/grove/console"
)

// This example registers an application operation once for interactive and
// scripted invocation.
func ExampleRegistry() {
	var registry console.Registry
	err := registry.Register(console.Action{
		Name: "app.orders.verify", Label: "Run integrity check", Section: "Application",
		Handler: func(_ context.Context, args []string) (any, error) {
			return "verified " + args[0], nil
		},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	result, err := registry.Invoke(context.Background(), "app.orders.verify", []string{"orders"})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(result)
	// Output:
	// verified orders
}

func TestRegistryRegisterInvokeAndList(t *testing.T) {
	var registry console.Registry
	first := console.Action{
		Name: "rollout.start", Label: "New rollout", Section: "Deployments",
		Handler: func(_ context.Context, args []string) (any, error) {
			args[0] = "handler-owned-copy"
			return len(args), nil
		},
	}
	second := console.Action{
		Name: "cluster.status", Label: "Status", Section: "Cluster",
		Handler: func(_ context.Context, _ []string) (any, error) { return "healthy", nil },
	}
	if err := registry.Register(first); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(second); err != nil {
		t.Fatal(err)
	}
	args := []string{"configs/acme.yaml"}
	result, err := registry.Invoke(t.Context(), first.Name, args)
	if err != nil {
		t.Fatal(err)
	}
	if result != 1 || args[0] != "configs/acme.yaml" {
		t.Errorf("Invoke() = %v, args = %q; want 1 and caller-owned arguments", result, args)
	}
	actions := registry.Actions()
	if len(actions) != 2 || actions[0].Name != second.Name || actions[1].Name != first.Name {
		t.Errorf("Actions() = %#v; want actions ordered by section", actions)
	}

	if err := registry.Register(first); !errors.Is(err, console.ErrActionDuplicate) {
		t.Errorf("duplicate Register() error = %v; want %v", err, console.ErrActionDuplicate)
	}
	if _, err := registry.Invoke(t.Context(), "missing", nil); !errors.Is(err, console.ErrActionUnknown) {
		t.Errorf("unknown Invoke() error = %v; want %v", err, console.ErrActionUnknown)
	}
}

func TestRegistryRejectsIncompleteActions(t *testing.T) {
	var registry console.Registry
	cases := []struct {
		action console.Action
		want   error
	}{
		{action: console.Action{}, want: console.ErrActionNameRequired},
		{action: console.Action{Name: "cluster.status"}, want: console.ErrActionLabelRequired},
		{action: console.Action{Name: "cluster.status", Label: "Status"}, want: console.ErrActionSectionRequired},
		{action: console.Action{Name: "cluster.status", Label: "Status", Section: "Cluster"}, want: console.ErrActionHandlerRequired},
	}
	for _, test := range cases {
		if err := registry.Register(test.action); !errors.Is(err, test.want) {
			t.Errorf("Register(%#v) error = %v; want %v", test.action, err, test.want)
		}
	}
}
