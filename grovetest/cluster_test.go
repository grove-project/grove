package grovetest_test

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/grove-project/grove/grovetest"
)

// Three independent processes must retain isolated resources and failure
// domains even though they share one host.
func TestNewCluster(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	cluster, err := grovetest.NewCluster(grovletPath, 3)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cluster.Cleanup(); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	if got := cluster.NodeCount(); got != 3 {
		t.Fatalf("node count = %d; want 3", got)
	}

	ids := make(map[string]bool)
	ports := make(map[int]bool)
	runtimeDirs := make(map[string]bool)
	for i := range cluster.NodeCount() {
		node := cluster.Node(i)
		if node.ID() == "" || ids[node.ID()] {
			t.Errorf("node %d ID = %q; want a unique non-empty ID", i, node.ID())
		}
		if node.Port() == 0 || ports[node.Port()] {
			t.Errorf("node %d port = %d; want a unique non-zero port", i, node.Port())
		}
		if node.TempDir() == "" || runtimeDirs[node.TempDir()] {
			t.Errorf("node %d runtime directory = %q; want a unique directory", i, node.TempDir())
		}
		ids[node.ID()] = true
		ports[node.Port()] = true
		runtimeDirs[node.TempDir()] = true
	}

	if err := cluster.Start(); err != nil {
		t.Fatal(err)
	}
	if err := cluster.WaitAllReady(ctx); err != nil {
		t.Fatal(err)
	}

	failedNode := cluster.Node(1)
	if err := failedNode.Kill(ctx); err != nil {
		t.Fatal(err)
	}
	if err := failedNode.WaitReady(ctx); err == nil {
		t.Error("killed node remained ready")
	}
	for _, i := range []int{0, 2} {
		if err := cluster.Node(i).WaitReady(ctx); err != nil {
			t.Errorf("surviving node %d: %v", i, err)
		}
	}

	diagnostics := cluster.DumpDiagnostics()
	for id := range ids {
		if !strings.Contains(diagnostics, "node="+id) {
			t.Errorf("diagnostics do not include %s:\n%s", id, diagnostics)
		}
	}
	if err := cluster.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, 2} {
		if got := cluster.Node(i).Logs(); !strings.Contains(got, `{"event":"stopped"}`) {
			t.Errorf("surviving node %d logs = %q; want stopped event", i, got)
		}
	}

	if err := cluster.Cleanup(); err != nil {
		t.Fatal(err)
	}
	for runtimeDir := range runtimeDirs {
		if _, err := os.Stat(runtimeDir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("runtime directory %q after cleanup: %v; want not exist", runtimeDir, err)
		}
	}
	for port := range ports {
		listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			t.Errorf("listen on released port %d: %v", port, err)
			continue
		}
		if err := listener.Close(); err != nil {
			t.Errorf("close listener on released port %d: %v", port, err)
		}
	}
}

func TestNewClusterRejectsInvalidNodeCount(t *testing.T) {
	_, err := grovetest.NewCluster(grovletPath, 0)
	if !errors.Is(err, grovetest.ErrInvalidNodeCount) {
		t.Errorf("NewCluster node count error = %v; want %v", err, grovetest.ErrInvalidNodeCount)
	}
}
