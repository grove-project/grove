package runtime

import (
	"errors"
	"testing"

	"github.com/grove-project/grove/internal/artifact"
)

// A Definition declares components only: the deprecated Scenario placement
// fields are not required for it to validate.
func TestDefinitionValidatesWithoutScenarioPlacement(t *testing.T) {
	definition := testRuntimeDefinition()
	scenario := *definition.Scenario
	scenario.NodeCount, scenario.DebugNodeCount = 0, 0
	scenario.InitialPlacements, scenario.StartupComponents, scenario.DebugPlacements = nil, nil, nil
	scenario.RecoveryServiceID = 0
	definition.Scenario = &scenario
	if err := validateDefinition(definition); err != nil {
		t.Fatalf("validateDefinition() = %v", err)
	}
}

// The scripted demo actions report an error, rather than panicking, for an
// application that declares no demo nodes (grove#43).
func TestDemoClustersRequireScenarioNodes(t *testing.T) {
	previous := activeApplication
	t.Cleanup(func() { activeApplication = previous })
	definition := testRuntimeDefinition()
	scenario := *definition.Scenario
	scenario.NodeCount, scenario.DebugNodeCount = 0, 0
	scenario.InitialPlacements, scenario.DebugPlacements = nil, nil
	definition.Scenario = &scenario
	activeApplication = definition

	if _, err := startApplicationCluster(t.Context(), "unused", artifact.Inspection{}); !errors.Is(err, errApplicationScenarioNodes) {
		t.Errorf("startApplicationCluster() = %v; want %v", err, errApplicationScenarioNodes)
	}
	if _, err := startDebugApplicationCluster(t.Context(), "unused", "unused", artifact.Inspection{}); !errors.Is(err, errApplicationScenarioNodes) {
		t.Errorf("startDebugApplicationCluster() = %v; want %v", err, errApplicationScenarioNodes)
	}
}
