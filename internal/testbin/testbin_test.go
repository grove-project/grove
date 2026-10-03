package testbin_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestRealProcessBuildsGoThroughTestbin keeps -short fast and binaries built
// once: test code may build a Grovlet or run `go build` only inside a
// testbin.New artifact (a package-level var, or a function passed to it),
// never directly in TestMain or a test body.
func TestRealProcessBuildsGoThroughTestbin(t *testing.T) {
	root := filepath.Join("..", "..")
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		checked++
		builders := make(map[string]bool) // functions passed to testbin.New
		ast.Inspect(file, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok && isSelector(call.Fun, "testbin", "New") && len(call.Args) == 2 {
				if id, ok := call.Args[1].(*ast.Ident); ok {
					builders[id.Name] = true
				}
			}
			return true
		})
		for _, decl := range file.Decls {
			if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.VAR {
				continue // artifact declarations
			}
			if fn, ok := decl.(*ast.FuncDecl); ok && builders[fn.Name.Name] {
				continue
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if isSelector(call.Fun, "grovetest", "BuildGrovlet") || isSelector(call.Fun, "grovetest", "BuildDebugGrovlet") || runsGoBuild(call) {
					t.Errorf("%s builds a binary directly; declare a testbin.New artifact and get it with Get(t) so -short skips it and it is built once", path)
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no test files checked")
	}
}

func isSelector(expr ast.Expr, pkg, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

// runsGoBuild reports exec.Command(Context)(..., "go", "build", ...).
func runsGoBuild(call *ast.CallExpr) bool {
	if !isSelector(call.Fun, "exec", "Command") && !isSelector(call.Fun, "exec", "CommandContext") {
		return false
	}
	var args []string
	for _, arg := range call.Args {
		if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			value, _ := strconv.Unquote(lit.Value)
			args = append(args, value)
		}
	}
	return len(args) >= 2 && args[0] == "go" && args[1] == "build"
}
