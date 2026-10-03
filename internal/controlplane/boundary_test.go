package controlplane_test

import (
	"os/exec"
	"strings"
	"testing"
)

const module = "github.com/grove-project/grove"

// allowedImports are the only non-standard-library packages the control-plane
// domain may depend on: the SDK's identifier types and the pure placement
// decision.
var allowedImports = map[string]bool{
	module:                         true,
	module + "/internal/placement": true,
}

// TestControlPlaneDomainHasNoInfrastructureDependencies proves the domain is
// independent of NATS: nothing in its transitive dependency graph is outside
// the standard library and the allowlist above, and in particular no nats-io
// package and not the System NATS adapter that depends on it.
func TestControlPlaneDomainHasNoInfrastructureDependencies(t *testing.T) {
	deps := goList(t, "{{join .Deps \"\\n\"}}", module+"/internal/controlplane")
	standard := make(map[string]bool)
	for _, pkg := range goList(t, "{{if .Standard}}{{.ImportPath}}{{end}}", "std") {
		standard[pkg] = true
	}
	checked := 0
	for _, dep := range deps {
		if standard[dep] {
			continue
		}
		checked++
		if !allowedImports[dep] {
			t.Errorf("internal/controlplane depends on %s; the control-plane domain may import only the standard library, %s and %s/internal/placement", dep, module, module)
		}
	}
	if checked == 0 {
		t.Fatal("no non-standard dependencies found; go list produced unexpected output")
	}
}

// TestSystemNATSDependsOnControlPlaneDomain proves the adapter direction: the
// System NATS adapter builds on the domain rather than defining it.
func TestSystemNATSDependsOnControlPlaneDomain(t *testing.T) {
	for _, imported := range goList(t, "{{join .Imports \"\\n\"}}", module+"/internal/systemnats") {
		if imported == module+"/internal/controlplane" {
			return
		}
	}
	t.Fatal("internal/systemnats does not import internal/controlplane; control-plane records and rules must be defined in the domain and adapted by systemnats")
}

func goList(t *testing.T, format string, packages ...string) []string {
	t.Helper()
	args := append([]string{"list", "-f", format}, packages...)
	out, err := exec.Command("go", args...).Output()
	if err != nil {
		t.Fatalf("go %s: %v", strings.Join(args, " "), err)
	}
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
