package grovetest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestTestClusterRunsProductionRules keeps the simulator from drifting back
// to private copies of Grove's rules: its reconciliation, health evaluation
// and leasing must call the production functions. Only the store, network,
// processes and clock are simulated.
func TestTestClusterRunsProductionRules(t *testing.T) {
	required := []string{
		"controlplane.PlanHandlerPlacementsWith", // reconciliation, epochs, deletes
		"controlplane.EvaluateHealth",            // failure detection
		"controlplane.Members",                   // liveness from health
		"placement.LiveNodes",
		"placement.DecideClaim", // exclusive claims
		"placement.LeaseHolds",  // fencing
		"placement.Selector",    // call target selection
	}
	file, err := parser.ParseFile(token.NewFileSet(), "testcluster.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	used := make(map[string]bool)
	ast.Inspect(file, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok {
				used[id.Name+"."+sel.Sel.Name] = true
			}
		}
		return true
	})
	for _, name := range required {
		if !used[name] {
			t.Errorf("testcluster.go does not use %s; the TestCluster must run production's rule, not its own copy", name)
		}
	}
}
