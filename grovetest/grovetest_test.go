package grovetest_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/grove-project/grove/grovetest"
	"github.com/grove-project/grove/internal/testbin"
)

var grovletBinary = testbin.New("test Grovlet", func(ctx context.Context, dir string) (string, error) {
	return grovetest.BuildGrovlet(ctx, dir, "./internal/testapp/cmd/testapp")
})

// grovletPath builds the test Grovlet on first use and skips tb under -short.
func grovletPath(tb testing.TB) string { return grovletBinary.Get(tb) }

// A Node follows the same readiness and graceful-shutdown lifecycle as the
// Grovlet executable used in production.
func ExampleNode() {
	node, err := grovetest.StartNode(grovletBinary.ForExample())
	if err != nil {
		fmt.Println(err)
		return
	}
	defer node.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := node.WaitReady(ctx); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("ready")
	if err := node.Stop(ctx); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("stopped")
	// Output:
	// ready
	// stopped
}

// The harness workflow deliberately exercises every process transition in one
// test so every restart uses the same binary and runtime state.
func TestNode(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := grovetest.StartNode(grovletPath(t), "--runtime-dir=elsewhere"); !errors.Is(err, grovetest.ErrRuntimeDirArgument) {
		t.Errorf("StartNode() runtime argument error = %v; want %v", err, grovetest.ErrRuntimeDirArgument)
	}

	node, err := grovetest.StartNode(grovletPath(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := node.Cleanup(); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	runtimeDir := node.TempDir()

	if err := node.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	if got := node.Logs(); !strings.Contains(got, `{"event":"ready"}`) {
		t.Errorf("logs after readiness = %q; want ready event", got)
	}
	if err := node.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if got := node.Logs(); !strings.Contains(got, `{"event":"stopped"}`) {
		t.Errorf("logs after stop = %q; want stopped event", got)
	}

	if err := node.Restart(); err != nil {
		t.Fatal(err)
	}
	if err := node.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	if node.TempDir() != runtimeDir {
		t.Errorf("runtime directory after restart = %q; want %q", node.TempDir(), runtimeDir)
	}
	if err := node.Kill(ctx); err != nil {
		t.Fatal(err)
	}

	if err := node.Restart(); err != nil {
		t.Fatal(err)
	}
	if err := node.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	if err := node.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	if err := os.RemoveAll(runtimeDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimeDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := node.Restart(); err != nil {
		t.Fatal(err)
	}
	err = node.WaitReady(ctx)
	var processErr *grovetest.ProcessError
	if !errors.As(err, &processErr) {
		t.Fatalf("startup error type = %T; want *grovetest.ProcessError", err)
	}
	if !strings.Contains(processErr.Logs, "runtime directory") || !strings.Contains(processErr.Logs, runtimeDir) {
		t.Errorf("startup diagnostics = %q; want runtime-directory failure for %q", processErr.Logs, runtimeDir)
	}

	if err := node.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runtimeDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("runtime directory after cleanup: %v; want not exist", err)
	}
	if err := node.Restart(); !errors.Is(err, grovetest.ErrNodeCleaned) {
		t.Errorf("restart after cleanup = %v; want %v", err, grovetest.ErrNodeCleaned)
	}

	activeNode, err := grovetest.StartNode(grovletPath(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := activeNode.Cleanup(); err != nil {
			t.Errorf("cleanup active node: %v", err)
		}
	})
	if err := activeNode.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	activeRuntimeDir := activeNode.TempDir()
	if err := activeNode.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(activeRuntimeDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("active runtime directory after cleanup: %v; want not exist", err)
	}
}

func TestMain(m *testing.M) {
	testbin.SkipExamplesUnderShort("ExampleNode")
	code := m.Run()
	if err := testbin.Cleanup(); err != nil && code == 0 {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}
