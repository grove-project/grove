package runtime

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// applicationCodeHost is the one file that hands application components a
// ComponentContext and registers them: the host shared by the node's
// application runtime and isolated workers.
const applicationCodeHost = "componenthost.go"

// applicationProcessEntrypoints are the process entrypoints that host
// application code. The Grovlet's entrypoint (run) is not one of them.
var applicationProcessEntrypoints = []string{"runApplicationRuntime", "runWorker"}

// TestGrovletNeverRunsApplicationCode proves the process model in
// docs/architecture/process-model.md: application components are registered
// only in application processes, never in the Grovlet. A ComponentContext is
// built only in the application-process host, and that host is created only
// by the application runtime and worker entrypoints.
func TestGrovletNeverRunsApplicationCode(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var contexts, hosts []string
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.CompositeLit:
					if identifier, ok := node.Type.(*ast.Ident); ok && identifier.Name == "ComponentContext" {
						contexts = append(contexts, path)
						if path != applicationCodeHost {
							t.Errorf("%s: %s builds a ComponentContext; only %s registers application code",
								fileSet.Position(node.Pos()), function.Name.Name, applicationCodeHost)
						}
					}
				case *ast.CallExpr:
					if identifier, ok := node.Fun.(*ast.Ident); ok && identifier.Name == "newApplicationProcess" {
						hosts = append(hosts, function.Name.Name)
						if !slices.Contains(applicationProcessEntrypoints, function.Name.Name) {
							t.Errorf("%s: %s creates an application-process host; only %v may",
								fileSet.Position(node.Pos()), function.Name.Name, applicationProcessEntrypoints)
						}
					}
				}
				return true
			})
		}
	}
	// A guard that matches nothing guards nothing.
	if !slices.Contains(contexts, applicationCodeHost) {
		t.Errorf("no ComponentContext is built in %s; update the guard", applicationCodeHost)
	}
	for _, entrypoint := range applicationProcessEntrypoints {
		if !slices.Contains(hosts, entrypoint) {
			t.Errorf("%s does not create an application-process host; update the guard", entrypoint)
		}
	}
}
