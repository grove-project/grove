package grove_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// forbiddenGroveShopImport is the GroveShop demo application package. Grove
// runtime, SDK, CLI/TUI, and test infrastructure must never depend on it;
// only demo-owned packages may.
const forbiddenGroveShopImport = "github.com/grove-project/grove/demo/groveshop"

type listedPackage struct {
	ImportPath string
	Deps       []string
}

// TestGroveDoesNotDependOnGroveShop proves the one-way dependency boundary:
// GroveShop may import Grove's public packages, but no non-demo Grove
// package or command may import back into the GroveShop demo application.
// This keeps moving GroveShop to its own repository a mechanical extraction
// rather than an architectural change.
func TestGroveDoesNotDependOnGroveShop(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list ./...: %v\n%s", err, stderr.String())
	}

	decoder := json.NewDecoder(&stdout)
	checked := 0
	for decoder.More() {
		var pkg listedPackage
		if err := decoder.Decode(&pkg); err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		if strings.HasPrefix(pkg.ImportPath, "github.com/grove-project/grove/demo/") {
			continue
		}
		checked++
		for _, dep := range pkg.Deps {
			if dep == forbiddenGroveShopImport {
				t.Errorf("%s imports %s; Grove must not depend on the GroveShop demo application", pkg.ImportPath, forbiddenGroveShopImport)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no packages were checked; go list produced no output")
	}
}
