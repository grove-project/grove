package grove_test

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const modulePath = "github.com/grove-project/grove"

// importRule forbids packages matching From (minus Except) from depending,
// directly or transitively, on packages matching Forbidden. Patterns are an
// import path or a prefix ending in "/...", which also matches the prefix
// itself. Only non-test code is checked: tests may use test infrastructure.
type importRule struct {
	Name      string
	From      []string
	Except    []string
	Forbidden []string
}

// importRules is Grove's architecture boundary table. Add a row to enforce a
// new boundary.
var importRules = []importRule{
	{
		// grovetest builds on production abstractions, never the reverse.
		Name:      "production does not depend on test infrastructure",
		From:      []string{modulePath + "/..."},
		Except:    []string{modulePath + "/grovetest/...", modulePath + "/internal/testapp/..."},
		Forbidden: []string{modulePath + "/grovetest/..."},
	},
	{
		// Applications reach the grove CLI only through the scenario protocol
		// (internal/scenario), so the CLI and runtime never link one.
		Name:      "Grove does not depend on its test application",
		From:      []string{modulePath + "/..."},
		Except:    []string{modulePath + "/internal/testapp/..."},
		Forbidden: []string{modulePath + "/internal/testapp/..."},
	},
	{
		// Rollout orchestration sequences control-plane records through a
		// Store port; the System NATS adapter implements it, so the
		// orchestration is tested without a server.
		Name:      "rollout orchestration does not depend on NATS",
		From:      []string{modulePath + "/internal/rollout"},
		Forbidden: []string{modulePath + "/internal/systemnats", "github.com/nats-io/..."},
	},
	{
		// Inspection reads control-plane state through a read-only port
		// that the System NATS adapter implements.
		Name:      "inspection does not depend on NATS",
		From:      []string{modulePath + "/internal/inspect"},
		Forbidden: []string{modulePath + "/internal/systemnats", "github.com/nats-io/..."},
	},
	{
		// Grove Shop is a standalone application that consumes Grove.
		Name:      "Grove does not depend on Grove Shop",
		From:      []string{modulePath + "/..."},
		Forbidden: []string{"github.com/grove-project/groveshop/..."},
	},
}

// callRule allows calls to the functions or methods named Names only from
// packages matching Owners. A name qualified as "pkg.Func" matches calls
// through that package identifier; a bare name matches any method call of
// that name. Only non-test code is checked: tests may set up state directly.
type callRule struct {
	Name   string
	Names  []string
	Owners []string
}

// callRules give each orchestration responsibility one production owner.
var callRules = []callRule{
	{
		// Every artifact, desired-deployment and rollout record is written by
		// the rollout owner, through the System NATS adapter that stores it.
		Name:   "deployment intent is written only by the rollout owner",
		Names:  []string{"PutRollout", "PutDeploymentArtifact", "PutDesired", "PutArtifact"},
		Owners: []string{modulePath + "/internal/rollout", modulePath + "/internal/systemnats"},
	},
	{
		// Local node processes are launched by the local cluster owner;
		// grovetest wraps the same process abstraction for tests.
		Name:   "local node processes are launched only by the local cluster owner",
		Names:  []string{"nodeproc.Start", "nodeproc.New"},
		Owners: []string{modulePath + "/internal/localcluster", modulePath + "/grovetest"},
	},
}

// TestOrchestrationOwners enforces callRules over every non-test source file
// in the module. A rule whose owners never make the call fails too, so a
// renamed function cannot leave a rule silently guarding nothing.
func TestOrchestrationOwners(t *testing.T) {
	packages := listModulePackages(t)
	for _, rule := range callRules {
		t.Run(rule.Name, func(t *testing.T) {
			ownerCalls := 0
			for _, pkg := range packages {
				owner := matchesAny(pkg.ImportPath, rule.Owners)
				for _, file := range pkg.GoFiles {
					path := filepath.Join(pkg.Dir, file)
					for _, call := range namedCalls(t, path, rule.Names) {
						if owner {
							ownerCalls++
							continue
						}
						t.Errorf("%s: only %s may call %s", call, rule.Owners, rule.Names)
					}
				}
			}
			if ownerCalls == 0 {
				t.Errorf("no owner calls %s; the rule guards nothing", rule.Names)
			}
		})
	}
}

// namedCalls returns the positions of calls in the Go file at path to any
// of names.
func namedCalls(t *testing.T, path string, names []string) []string {
	t.Helper()
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var calls []string
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		qualified := selector.Sel.Name
		if ident, ok := selector.X.(*ast.Ident); ok {
			qualified = ident.Name + "." + selector.Sel.Name
		}
		for _, name := range names {
			if name == selector.Sel.Name && !strings.Contains(name, ".") || name == qualified {
				calls = append(calls, fileSet.Position(call.Pos()).String()+" "+qualified)
			}
		}
		return true
	})
	return calls
}

// presentationFiles build what the console, its TUI and the grove CLI show.
// They read cluster state only through internal/inspect.
var presentationFiles = []string{
	"runtime/application.go",
	"runtime/appinfo.go",
	"runtime/appservices.go",
	"runtime/applicationlogs.go",
	"runtime/console.go",
	"runtime/k9s_tui.go",
	"runtime/k9s_services.go",
	"cmd/grove/main.go",
}

// controlPlaneViewReads are the raw control-plane view requests that
// inspect.Source wraps.
var controlPlaneViewReads = []string{
	"RequestClusterView", "RequestPlacement", "RequestDeployments", "RequestComponents", "RequestHandlerPlacement",
}

// TestPresentationReadsThroughInspection proves the console, TUI and CLI read
// cluster state through the inspection surface: their files make no raw
// control-plane view request, and no production code reads Grove status from
// an application's ingress, which only the application itself serves.
func TestPresentationReadsThroughInspection(t *testing.T) {
	for _, path := range presentationFiles {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("presentation file %s: %v; update presentationFiles", path, err)
			continue
		}
		for _, call := range namedCalls(t, path, controlPlaneViewReads) {
			t.Errorf("%s: presentation code reads control-plane views through internal/inspect", call)
		}
	}
	for _, pkg := range listModulePackages(t) {
		if matches(pkg.ImportPath, modulePath+"/internal/testapp/...") {
			continue
		}
		for _, file := range pkg.GoFiles {
			path := filepath.Join(pkg.Dir, file)
			for _, literal := range stringLiterals(t, path) {
				if strings.Contains(literal, "/grove/status") {
					t.Errorf("%s reads Grove status from the application's ingress; read it through internal/inspect", literal)
				}
			}
		}
	}
}

// stringLiterals returns the positions and values of string literals in the
// Go file at path.
func stringLiterals(t *testing.T, path string) []string {
	t.Helper()
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var literals []string
	ast.Inspect(file, func(node ast.Node) bool {
		if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
			literals = append(literals, fileSet.Position(literal.Pos()).String()+" "+literal.Value)
		}
		return true
	})
	return literals
}

type listedPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Deps       []string
}

// TestArchitectureBoundaries enforces importRules over every package in the
// module. A rule that matches no package, or forbids an in-module package
// that does not exist, fails too, so a rule cannot silently stop guarding.
func TestArchitectureBoundaries(t *testing.T) {
	packages := listModulePackages(t)
	known := make(map[string]bool, len(packages))
	for _, pkg := range packages {
		known[pkg.ImportPath] = true
	}
	for _, rule := range importRules {
		t.Run(rule.Name, func(t *testing.T) {
			for _, forbidden := range rule.Forbidden {
				if matches(forbidden, modulePath+"/...") && !matchesAnyKnown(forbidden, known) {
					t.Errorf("forbidden pattern %q matches no package in the module; the rule guards nothing", forbidden)
				}
			}
			checked := 0
			for _, pkg := range packages {
				if !matchesAny(pkg.ImportPath, rule.From) || matchesAny(pkg.ImportPath, rule.Except) {
					continue
				}
				checked++
				for _, dep := range pkg.Deps {
					if matchesAny(dep, rule.Forbidden) {
						t.Errorf("%s depends on %s", pkg.ImportPath, dep)
					}
				}
			}
			if checked == 0 {
				t.Error("rule matches no package; it guards nothing")
			}
		})
	}
}

func TestImportPatternMatching(t *testing.T) {
	tests := []struct {
		path    string
		pattern string
		want    bool
	}{
		{modulePath + "/grovetest", modulePath + "/grovetest/...", true},
		{modulePath + "/grovetest/sub", modulePath + "/grovetest/...", true},
		{modulePath + "/grovetestx", modulePath + "/grovetest/...", false},
		{modulePath + "/runtime", modulePath + "/runtime", true},
		{modulePath + "/runtime/sub", modulePath + "/runtime", false},
	}
	for _, test := range tests {
		if got := matches(test.path, test.pattern); got != test.want {
			t.Errorf("matches(%q, %q) = %t; want %t", test.path, test.pattern, got, test.want)
		}
	}
}

func listModulePackages(t *testing.T) []listedPackage {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("go", "list", "-json=ImportPath,Dir,GoFiles,Deps", "./...")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list ./...: %v\n%s", err, stderr.String())
	}
	var packages []listedPackage
	decoder := json.NewDecoder(&stdout)
	for decoder.More() {
		var pkg listedPackage
		if err := decoder.Decode(&pkg); err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		packages = append(packages, pkg)
		// go test caches results by the files the test itself touches, not
		// the ones go list reads. Stat every source so editing an import
		// re-runs the guard instead of replaying a cached pass.
		_, _ = os.Stat(pkg.Dir)
		for _, file := range pkg.GoFiles {
			_, _ = os.Stat(filepath.Join(pkg.Dir, file))
		}
	}
	if len(packages) == 0 {
		t.Fatal("go list produced no packages")
	}
	return packages
}

func matchesAnyKnown(pattern string, known map[string]bool) bool {
	for path := range known {
		if matches(path, pattern) {
			return true
		}
	}
	return false
}

func matchesAny(path string, patterns []string) bool {
	for _, pattern := range patterns {
		if matches(path, pattern) {
			return true
		}
	}
	return false
}

func matches(path, pattern string) bool {
	if prefix, ok := strings.CutSuffix(pattern, "/..."); ok {
		return path == prefix || strings.HasPrefix(path, prefix+"/")
	}
	return path == pattern
}
