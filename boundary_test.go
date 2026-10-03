package grove_test

import (
	"bytes"
	"encoding/json"
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
		// Placement decisions are pure so production and grovetest share them
		// (docs/architecture/placement.md).
		Name: "placement decisions do not depend on storage, transport or runtime",
		From: []string{modulePath + "/internal/placement"},
		Forbidden: []string{
			"github.com/nats-io/...",
			modulePath + "/internal/systemnats",
			modulePath + "/internal/controlplane",
			modulePath + "/runtime/...",
		},
	},
	{
		// Grove Shop is a standalone application that consumes Grove.
		Name:      "Grove does not depend on Grove Shop",
		From:      []string{modulePath + "/..."},
		Forbidden: []string{"github.com/grove-project/groveshop/..."},
	},
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
