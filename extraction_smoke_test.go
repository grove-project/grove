package grove_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGroveShopExtractsAsExternalModule proves the GroveShop application
// shape can be built as a genuinely external Go module that consumes Grove
// only through its public packages. It assembles a temporary module from
// copies of demo/groveshop, demo/groveshopapp, and the groveshop command,
// rewriting only the import paths that would change when those directories
// move to their own repository. The build fails if extraction would require
// importing Grove-internal packages or anything GroveShop does not own,
// because Go's internal-package visibility is keyed on import path, not on
// module identity, and a foreign module path cannot see into
// github.com/grove-project/grove/internal/....
func TestGroveShopExtractsAsExternalModule(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	const modulePath = "example.com/groveshop-extraction-smoke"
	moduleDir := t.TempDir()

	copyPackageSource(t, filepath.Join(repoRoot, "demo", "groveshop"), filepath.Join(moduleDir, "groveshop"), nil)
	if err := os.CopyFS(
		filepath.Join(moduleDir, "groveshop", "web"),
		os.DirFS(filepath.Join(repoRoot, "demo", "groveshop", "web")),
	); err != nil {
		t.Fatalf("copy embedded web assets: %v", err)
	}
	copyPackageSource(t, filepath.Join(repoRoot, "demo", "groveshopapp"), filepath.Join(moduleDir, "groveshopapp"), map[string]string{
		"github.com/grove-project/grove/demo/groveshop": modulePath + "/groveshop",
	})
	copyPackageSource(t, filepath.Join(repoRoot, "demo", "groveshop", "cmd", "groveshop"), filepath.Join(moduleDir, "cmd", "groveshop"), map[string]string{
		"github.com/grove-project/grove/demo/groveshopapp": modulePath + "/groveshopapp",
	})

	goMod := fmt.Sprintf(`module %s

go 1.26.0

require (
	github.com/grove-project/grove v0.0.0-00010101000000-000000000000
	go.yaml.in/yaml/v3 v3.0.5
)

replace github.com/grove-project/grove => %s
`, modulePath, repoRoot)
	if err := os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	runGo(t, moduleDir, "mod", "tidy")
	// A successful build is the proof: Go's internal-package visibility is
	// keyed on import path, so the copied groveshop/groveshopapp/cmd code
	// would fail to compile here if it ever imported an internal Grove
	// package or anything else GroveShop does not itself own, regardless of
	// the local replace directive pointing at this checkout.
	runGo(t, moduleDir, "build", "./...")
}

// copyPackageSource copies the non-test .go files of one package directory
// (non-recursively) into dst, rewriting any quoted import path found as a key
// in rewrites to its corresponding value.
func copyPackageSource(t *testing.T, src, dst string, rewrites map[string]string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		rewritten := string(content)
		for from, to := range rewrites {
			rewritten = strings.ReplaceAll(rewritten, `"`+from+`"`, `"`+to+`"`)
		}
		if err := os.WriteFile(filepath.Join(dst, name), []byte(rewritten), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func runGo(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, output.String())
	}
	return output.String()
}
