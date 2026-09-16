package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/internal/systemnats"
)

func TestParseDebugDemoArguments(t *testing.T) {
	path, err := parseDebugDemoArguments(nil)
	if err != nil {
		t.Fatal(err)
	}
	if path != "configs/acme.yaml" {
		t.Errorf("default config = %q; want configs/acme.yaml", path)
	}
	path, err = parseDebugDemoArguments([]string{"--config", "customer.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if path != "customer.yaml" {
		t.Errorf("selected config = %q; want customer.yaml", path)
	}
	if _, err := parseDebugDemoArguments([]string{"unexpected"}); !errors.Is(err, errConsoleArguments) {
		t.Errorf("unexpected argument error = %v; want %v", err, errConsoleArguments)
	}
}

func TestDebugApplicationStatusHealthy(t *testing.T) {
	placements := debugApplicationPlacements()
	status := groveshop.ClusterStatusView{
		Ready: true, Health: "healthy",
		ActiveArtifact: &groveshop.ArtifactStatusView{ArtifactDigest: "sha256:debug"},
	}
	for i := 1; i <= debugApplicationNodeCount; i++ {
		status.Nodes = append(status.Nodes, groveshop.NodeStatusView{NodeID: "node-" + string(rune('0'+i)), Health: string(systemnats.HealthHealthy)})
	}
	for serviceID, nodeID := range placements {
		status.Placements = append(status.Placements, groveshop.PlacementStatusView{
			ServiceID: serviceID, NodeID: nodeID, Health: string(systemnats.ComponentHealthy),
		})
	}
	if !debugApplicationStatusHealthy(status, "sha256:debug") {
		t.Fatalf("healthy debug application rejected: %#v", status)
	}
	status.Placements[0].NodeID = "wrong-node"
	if debugApplicationStatusHealthy(status, "sha256:debug") {
		t.Fatal("misplaced debug application reported healthy")
	}
}

// The Grove Shop artifact itself owns the topology that subsequent debugger
// actions inspect; no external deployment command prepares the cluster.
func TestApplicationControllerStartsFiveNodeDebugDemo(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	statePath := filepath.Join(t.TempDir(), "console.json")
	consoleCommand := exec.CommandContext(ctx, debugGrovletPath)
	consoleCommand.Env = append(os.Environ(),
		consoleStateEnvironment+"="+statePath,
		"PATH="+filepath.Dir(delvePath)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	input, err := consoleCommand.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var consoleOutput bytes.Buffer
	consoleCommand.Stdout = &consoleOutput
	consoleCommand.Stderr = &consoleOutput
	if err := consoleCommand.Start(); err != nil {
		t.Fatal(err)
	}
	waitForConsoleState(t, statePath)
	stopped := false
	t.Cleanup(func() {
		if stopped {
			return
		}
		_, _ = io.WriteString(input, "q\n")
		_ = consoleCommand.Wait()
	})

	configPath := filepath.Join("..", "..", "configs", "acme.yaml")
	output := runDebugApplicationAction(t, ctx, statePath, "debug.demo.start", "--config", configPath)
	var result debugDemoActionResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("decode debug demo result %q: %v", output, err)
	}
	if result.State != "active" || result.WebURL == "" || result.Status.ActiveArtifact == nil ||
		!debugApplicationStatusHealthy(result.Status, result.Status.ActiveArtifact.ArtifactDigest) {
		t.Fatalf("debug demo result = %#v; console=%s", result, consoleOutput.String())
	}
	if applicationPlacementNodeFromStatus(result.Status, groveshop.ServiceOrders) != "node-2" || applicationPlacementNodeFromStatus(result.Status, groveshop.ServicePayment) != "node-4" {
		t.Errorf("debug demo placement = %#v", result.Status.Placements)
	}
	if applicationWorkerFromStatus(result.Status, groveshop.ServiceOrders) != "orders-1" || applicationWorkerFromStatus(result.Status, groveshop.ServicePayment) != "payment-1" {
		t.Errorf("debug demo workers = %#v", result.Status.Nodes)
	}
	if _, err := io.WriteString(input, "q\n"); err != nil {
		t.Fatal(err)
	}
	if err := consoleCommand.Wait(); err != nil {
		t.Fatalf("stop debug demo console: %v; output=%s", err, consoleOutput.String())
	}
	stopped = true
	if output := consoleOutput.String(); !strings.Contains(output, "Debug demo > Start") {
		t.Errorf("debug demo TUI action missing: %s", output)
	}
}

func applicationWorkerFromStatus(status groveshop.ClusterStatusView, serviceID grove.ServiceID) string {
	for _, node := range status.Nodes {
		for _, component := range node.Components {
			if component.ServiceID == serviceID {
				return component.WorkerID
			}
		}
	}
	return ""
}

func runDebugApplicationAction(t *testing.T, ctx context.Context, statePath string, args ...string) []byte {
	t.Helper()
	command := exec.CommandContext(ctx, debugGrovletPath, append([]string{"action"}, args...)...)
	command.Env = append(os.Environ(), consoleStateEnvironment+"="+statePath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("debug Grove Shop action %q: %v; output=%q", args, err, output)
	}
	return output
}
