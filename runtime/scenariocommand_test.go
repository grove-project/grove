package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/scenario"
	"github.com/grove-project/grove/internal/testapp"
)

func TestScenarioDescribeReportsApplicationOwnedScenario(t *testing.T) {
	definition := testRuntimeDefinition()
	definition.Scenario.CheckComponents = []string{"orders", "inventory"}
	definition.Scenario.Check = func(context.Context, *grove.Client, string) (string, error) { return "ok", nil }
	previous := activeApplication
	t.Cleanup(func() { activeApplication = previous })
	if err := configureApplication(definition); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := runScenarioCommand(t.Context(), nil, &stdout, io.Discard); err == nil {
		t.Fatal("scenario without a subcommand succeeded")
	}
	if err := runScenarioCommand(t.Context(), []string{scenario.DescribeCommand}, &stdout, io.Discard); err != nil {
		t.Fatal(err)
	}
	var got scenario.Description
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := scenario.Description{
		ProtocolVersion: scenario.ProtocolVersion,
		Name:            "Grove Test App",
		ApplicationID:   testapp.ApplicationID,
		CheckComponents: []scenario.Component{
			{ServiceID: testapp.ServiceOrders, Name: "Orders", Kind: "orders"},
			{ServiceID: testapp.ServiceInventory, Name: "Inventory", Kind: "inventory"},
		},
		RecoveryServiceID: testapp.ServiceInventory,
		DebugNodeCount:    5,
		DebugPlacements: []scenario.Component{
			{ServiceID: testapp.ServiceWeb, Name: "Web", Kind: "web", NodeID: "node-1", Ingress: true},
			{ServiceID: testapp.ServiceOrders, Name: "Orders", Kind: "orders", NodeID: "node-2", Options: []string{"distributed"}},
			{ServiceID: testapp.ServiceInventory, Name: "Inventory", Kind: "inventory", NodeID: "node-3"},
			{ServiceID: testapp.ServicePayment, Name: "Payment", Kind: "payment", NodeID: "node-4"},
			{ServiceID: testapp.ServiceShipping, Name: "Shipping", Kind: "shipping", NodeID: "node-5"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scenario describe = %#v\nwant %#v", got, want)
	}
}

func TestScenarioCheckRequiresApplicationCheck(t *testing.T) {
	previous := activeApplication
	t.Cleanup(func() { activeApplication = previous })
	if err := configureApplication(testRuntimeDefinition()); err != nil {
		t.Fatal(err)
	}
	err := runScenarioCommand(t.Context(), []string{
		scenario.CheckCommand, "--system-nats-url", "nats://127.0.0.1:1", "--node-id", "node-1", "--run-id", "r",
	}, io.Discard, io.Discard)
	if !errors.Is(err, scenario.ErrNoCheck) {
		t.Errorf("scenario check without Check = %v; want %v", err, scenario.ErrNoCheck)
	}
}

func TestDefinitionRejectsIncompleteScenarioCheck(t *testing.T) {
	check := func(context.Context, *grove.Client, string) (string, error) { return "", nil }
	tests := map[string]func(*Scenario){
		"check without components": func(s *Scenario) { s.Check = check },
		"components without check": func(s *Scenario) { s.CheckComponents = []string{"orders"} },
		"unknown component": func(s *Scenario) {
			s.Check = check
			s.CheckComponents = []string{"bakery"}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			definition := testRuntimeDefinition()
			mutate(definition.Scenario)
			if err := validateDefinition(definition); !errors.Is(err, ErrApplicationDefinitionInvalid) {
				t.Errorf("validateDefinition() = %v; want %v", err, ErrApplicationDefinitionInvalid)
			}
		})
	}
}
