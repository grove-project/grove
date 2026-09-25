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

// groveShopCommit pins the grove-project/groveshop commit this test proves
// still builds against this checkout of Grove's public packages. Update it
// whenever GroveShop intentionally adopts a new Grove capability.
const groveShopCommit = "728623b0e64aaf22e1e6ca75a887fb3e7921ceed"

// TestGroveShopExternalModuleBuilds proves the real, external
// grove-project/groveshop application builds against this checkout of Grove
// using only Grove's public packages. It fetches the pinned GroveShop commit
// as an ordinary Go module dependency and replaces only Grove's own module
// path with this checkout, so GroveShop is built exactly as any other
// external consumer would build it — no source is copied or rewritten here.
// Go's internal-package visibility is keyed on import path, not module
// identity, so this build would fail if GroveShop ever required a
// Grove-internal package or anything else it does not itself own.
func TestGroveShopExternalModuleBuilds(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	moduleDir := t.TempDir()
	goMod := fmt.Sprintf(`module example.com/groveshop-boundary-smoke

go 1.26.0

require github.com/grove-project/grove v0.0.0-00010101000000-000000000000

replace github.com/grove-project/grove => %s
`, repoRoot)
	if err := os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	runGo(t, moduleDir, "get", "github.com/grove-project/groveshop@"+groveShopCommit)
	runGo(t, moduleDir, "mod", "tidy")
	runGo(t, moduleDir, "build", "github.com/grove-project/groveshop/...")
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
