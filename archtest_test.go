package grove_test

// Helpers shared by the architecture rules in boundary_test.go. They read
// the module through one go list call and parse non-test sources only.

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

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

// standardPackages returns the import paths of the standard library.
func standardPackages(t *testing.T) map[string]bool {
	t.Helper()
	out, err := exec.Command("go", "list", "std").Output()
	if err != nil {
		t.Fatalf("go list std: %v", err)
	}
	standard := make(map[string]bool)
	for _, pkg := range strings.Fields(string(out)) {
		standard[pkg] = true
	}
	return standard
}

// qualifiedUses returns every package-level identifier the Go file at path
// references through an import, as "import/path.Name".
func qualifiedUses(t *testing.T, path string) map[string]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	imports := make(map[string]string, len(file.Imports))
	for _, spec := range file.Imports {
		importPath, _ := strconv.Unquote(spec.Path.Value)
		local := importPath[strings.LastIndex(importPath, "/")+1:]
		if spec.Name != nil {
			local = spec.Name.Name
		}
		imports[local] = importPath
	}
	uses := make(map[string]bool)
	ast.Inspect(file, func(node ast.Node) bool {
		if selector, ok := node.(*ast.SelectorExpr); ok {
			if ident, ok := selector.X.(*ast.Ident); ok {
				if importPath, ok := imports[ident.Name]; ok {
					uses[importPath+"."+selector.Sel.Name] = true
				}
			}
		}
		return true
	})
	return uses
}
