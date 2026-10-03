package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/grove-project/grove/internal/scenario"
	"github.com/grove-project/grove/internal/systemnats"
)

var errScenarioCommand = errors.New("scenario command must be describe or check")

// runScenarioCommand serves the grove CLI's application-scenario protocol
// (internal/scenario), so the CLI can run an application's own scenario
// without linking application code.
func runScenarioCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errScenarioCommand
	}
	switch args[0] {
	case scenario.DescribeCommand:
		if len(args) != 1 {
			return fmt.Errorf("scenario describe: unexpected arguments %q", args[1:])
		}
		description, err := describeScenario(activeApplication)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(description)
	case scenario.CheckCommand:
		return runScenarioCheck(ctx, args[1:], stdout, stderr)
	default:
		return errScenarioCommand
	}
}

func describeScenario(definition Definition) (scenario.Description, error) {
	description := scenario.Description{
		ProtocolVersion: scenario.ProtocolVersion,
		Name:            definition.Name,
		ApplicationID:   definition.ApplicationID,
	}
	if definition.Scenario == nil {
		return description, nil
	}
	description.RecoveryServiceID = definition.Scenario.RecoveryServiceID
	for _, kind := range definition.Scenario.CheckComponents {
		component, ok := definition.componentByKind(kind)
		if !ok {
			return scenario.Description{}, fmt.Errorf("%w: scenario check component %q is unknown", ErrComponentUnknown, kind)
		}
		description.CheckComponents = append(description.CheckComponents, scenarioComponent(component, "", nil))
	}
	description.DebugNodeCount = definition.scenarioDebugNodeCount()
	for _, placement := range definition.scenarioDebugPlacements() {
		component, ok := definition.componentByID(placement.ServiceID)
		if !ok {
			return scenario.Description{}, fmt.Errorf("%w: debug placement service %d", ErrComponentUnknown, placement.ServiceID)
		}
		description.DebugPlacements = append(description.DebugPlacements, scenarioComponent(component, placement.NodeID, placement.Options))
	}
	return description, nil
}

func scenarioComponent(component Component, nodeID string, options []string) scenario.Component {
	return scenario.Component{
		ServiceID: component.ServiceID,
		Name:      component.Name,
		Kind:      component.Kind,
		NodeID:    nodeID,
		Options:   append([]string(nil), options...),
		Ingress:   component.HTTPHandler != nil,
	}
}

func runScenarioCheck(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("scenario check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	systemNATSURL := flags.String("system-nats-url", "", "System NATS client URL of the cluster under test")
	nodeID := flags.String("node-id", "", "Grovlet node whose placement view routes the check's calls")
	runID := flags.String("run-id", "", "unique identifier of this check run")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse scenario check flags: %w", err)
	}
	if flags.NArg() != 0 || *systemNATSURL == "" || *nodeID == "" || *runID == "" {
		return errors.New("scenario check requires --system-nats-url, --node-id and --run-id")
	}
	if activeApplication.Scenario == nil || activeApplication.Scenario.Check == nil {
		return scenario.ErrNoCheck
	}
	transport, err := systemnats.Connect(ctx, *systemNATSURL)
	if err != nil {
		return fmt.Errorf("connect to cluster: %w", err)
	}
	defer transport.Close()
	client, err := transport.ObservedPlacementClient(*nodeID)
	if err != nil {
		return fmt.Errorf("create check client: %w", err)
	}
	summary, err := activeApplication.Scenario.Check(ctx, client, *runID)
	if err != nil {
		return fmt.Errorf("scenario check: %w", err)
	}
	return json.NewEncoder(stdout).Encode(scenario.CheckResult{ProtocolVersion: scenario.ProtocolVersion, Summary: summary})
}
