package grove_test

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
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
		// Test-binary builds serve tests only (internal/testbin).
		Name:      "production does not depend on test binary builds",
		From:      []string{modulePath + "/..."},
		Except:    []string{modulePath + "/internal/testbin"},
		Forbidden: []string{modulePath + "/internal/testbin"},
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

// pureRule allows Package to depend, directly or transitively, only on the
// standard library and Allowed. It is an allowlist: a new dependency fails
// until someone decides it belongs.
type pureRule struct {
	Name    string
	Package string
	Allowed []string
}

// pureRules keep Grove's decision logic free of storage, transport and
// runtime, so production and the grovetest TestCluster run the same rules.
var pureRules = []pureRule{
	{
		// Placement decisions (docs/architecture/placement.md).
		Name:    "placement decisions are pure",
		Package: modulePath + "/internal/placement",
		Allowed: []string{modulePath},
	},
	{
		// Control-plane records and rules; internal/systemnats adapts them.
		Name:    "the control-plane domain is independent of NATS",
		Package: modulePath + "/internal/controlplane",
		Allowed: []string{modulePath, modulePath + "/internal/placement"},
	},
}

// delegationRule requires the non-test code of every package matching
// Packages to use each of Uses, written "import/path.Name". It keeps a
// consumer delegating a decision to its owner instead of growing a copy.
type delegationRule struct {
	Name     string
	Packages []string
	Uses     []string
}

const (
	placementPath    = modulePath + "/internal/placement"
	controlPlanePath = modulePath + "/internal/controlplane"
)

// delegationRules name, for each decision, the consumers that must call its
// owner.
var delegationRules = []delegationRule{
	{
		Name:     "control-plane reconciliation and leasing delegate to placement",
		Packages: []string{controlPlanePath},
		Uses: []string{
			placementPath + ".Place", placementPath + ".FencedEpoch",
			placementPath + ".DecideClaim", placementPath + ".LeaseHolds",
		},
	},
	{
		Name:     "the System NATS adapter applies control-plane lease rules",
		Packages: []string{modulePath + "/internal/systemnats"},
		Uses:     []string{controlPlanePath + ".DecideClaim", controlPlanePath + ".LeaseHolds"},
	},
	{
		Name:     "Grovlet recovery and liveness delegate to placement and health",
		Packages: []string{modulePath + "/runtime"},
		Uses: []string{
			placementPath + ".Recover", placementPath + ".LiveNodes", controlPlanePath + ".Members",
		},
	},
	{
		// The TestCluster simulates only store, network, processes and clock
		// (docs/architecture/testing.md).
		Name:     "the TestCluster runs production rules",
		Packages: []string{modulePath + "/grovetest"},
		Uses: []string{
			controlPlanePath + ".PlanHandlerPlacementsWith", controlPlanePath + ".EvaluateHealth",
			controlPlanePath + ".Members", placementPath + ".LiveNodes", placementPath + ".DecideClaim",
			placementPath + ".LeaseHolds", placementPath + ".Selector",
		},
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

// TestPureRules enforces pureRules.
func TestPureRules(t *testing.T) {
	packages := listModulePackages(t)
	standard := standardPackages(t)
	for _, rule := range pureRules {
		t.Run(rule.Name, func(t *testing.T) {
			pkg, ok := findPackage(packages, rule.Package)
			if !ok {
				t.Fatalf("package %s not found; the rule guards nothing", rule.Package)
			}
			for _, dep := range pkg.Deps {
				if !standard[dep] && !slices.Contains(rule.Allowed, dep) {
					t.Errorf("%s depends on %s; it may depend only on the standard library and %s", rule.Package, dep, rule.Allowed)
				}
			}
		})
	}
}

// TestDelegationRules enforces delegationRules.
func TestDelegationRules(t *testing.T) {
	packages := listModulePackages(t)
	for _, rule := range delegationRules {
		t.Run(rule.Name, func(t *testing.T) {
			checked := 0
			for _, pkg := range packages {
				if !matchesAny(pkg.ImportPath, rule.Packages) {
					continue
				}
				checked++
				used := make(map[string]bool)
				for _, file := range pkg.GoFiles {
					maps.Copy(used, qualifiedUses(t, filepath.Join(pkg.Dir, file)))
				}
				for _, name := range rule.Uses {
					if !used[name] {
						t.Errorf("%s does not use %s; it must delegate that decision instead of keeping its own copy", pkg.ImportPath, name)
					}
				}
			}
			if checked == 0 {
				t.Error("rule matches no package; it guards nothing")
			}
		})
	}
}

func findPackage(packages []listedPackage, importPath string) (listedPackage, bool) {
	for _, pkg := range packages {
		if pkg.ImportPath == importPath {
			return pkg, true
		}
	}
	return listedPackage{}, false
}
