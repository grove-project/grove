package placement_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	module        = "github.com/grove-project/grove"
	placementPath = module + "/internal/placement"
)

// TestPlacementIsPure proves the placement subsystem has no infrastructure
// dependency: outside the standard library it may import only the SDK root,
// never NATS, the control-plane records or a runtime package.
func TestPlacementIsPure(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", `{{range .Deps}}{{.}}{{"\n"}}{{end}}`, placementPath).Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	std, err := exec.Command("go", "list", "std").Output()
	if err != nil {
		t.Fatalf("go list std: %v", err)
	}
	standard := make(map[string]bool)
	for _, pkg := range strings.Fields(string(std)) {
		standard[pkg] = true
	}
	for _, dep := range strings.Fields(string(out)) {
		if !standard[dep] && dep != module {
			t.Errorf("internal/placement depends on %s; placement decisions must stay pure (standard library and %s only)", dep, module)
		}
	}
}

// TestPlacementDecisionsHaveOneOwner proves every consumer that decides
// placement delegates to internal/placement rather than keeping its own
// rule: production reconciliation and leasing (internal/controlplane), node
// recovery and liveness (runtime), and the simulator (grovetest), which must
// run the same rules as production.
func TestPlacementDecisionsHaveOneOwner(t *testing.T) {
	required := map[string][]string{
		"../controlplane": {"Place", "FencedEpoch", "DecideClaim", "LeaseHolds"},
		"../../runtime":   {"Recover", "LiveNodes"},
		"../../grovetest": {"Place", "FencedEpoch", "DecideClaim", "LeaseHolds", "Selector"},
	}
	for dir, functions := range required {
		used := placementUses(t, dir)
		for _, name := range functions {
			if !used[name] {
				t.Errorf("%s does not use placement.%s; it must delegate that decision to internal/placement instead of reimplementing it", filepath.Clean(dir), name)
			}
		}
	}
}

// placementUses returns the internal/placement identifiers referenced by the
// non-test Go files of the package in dir.
func placementUses(t *testing.T, dir string) map[string]bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	used := make(map[string]bool)
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		local := ""
		for _, spec := range file.Imports {
			if p, _ := strconv.Unquote(spec.Path.Value); p == placementPath {
				local = "placement"
				if spec.Name != nil {
					local = spec.Name.Name
				}
			}
		}
		if local == "" {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == local {
					used[sel.Sel.Name] = true
				}
			}
			return true
		})
	}
	if len(used) == 0 {
		t.Fatalf("%s does not import %s", dir, placementPath)
	}
	return used
}
