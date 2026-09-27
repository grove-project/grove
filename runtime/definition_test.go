package runtime

import "testing"

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
