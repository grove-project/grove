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
	"github.com/grove-project/grove/grovetest"
	"github.com/grove-project/grove/internal/systemnats"
)

func TestApplicationConsoleDispatchesStructuredActions(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "console.json")
	t.Setenv(consoleStateEnvironment, statePath)
	input, writeInput := io.Pipe()
	var tuiOutput bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runApplicationConsole(t.Context(), nil, input, &tuiOutput)
	}()
	waitForConsoleState(t, statePath)

	var actionOutput bytes.Buffer
	if err := runApplicationAction(t.Context(), []string{groveshop.ActionVerifyOrders}, &actionOutput); err != nil {
		t.Fatal(err)
	}
	var result groveshop.IntegrityResult
	if err := json.NewDecoder(&actionOutput).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Service != "orders" || result.Status != "healthy" || len(result.Workflow) != 5 {
		t.Errorf("application action result = %#v", result)
	}
	if _, err := io.WriteString(writeInput, "q\n"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_ = writeInput.Close()
	_ = input.Close()
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("console state after exit error = %v; want not exist", err)
	}
	if output := tuiOutput.String(); !strings.Contains(output, "GroveShop Grove Shop") || !strings.Contains(output, "Run integrity check") {
		t.Errorf("TUI output = %q", output)
	}
}

func TestGroveShopBinaryRunsConsoleActionsAndNodeRuntime(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	statePath := filepath.Join(t.TempDir(), "console.json")
	consoleCommand := exec.CommandContext(ctx, grovletPath)
	consoleCommand.Env = append(os.Environ(), consoleStateEnvironment+"="+statePath)
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

	actionCommand := exec.CommandContext(ctx, grovletPath, "action", groveshop.ActionVerifyOrders)
	actionCommand.Env = append(os.Environ(), consoleStateEnvironment+"="+statePath)
	actionOutput, err := actionCommand.CombinedOutput()
	if err != nil {
		t.Fatalf("run structured action: %v; output=%q; console=%q", err, actionOutput, consoleOutput.String())
	}
	var result groveshop.IntegrityResult
	if err := json.Unmarshal(actionOutput, &result); err != nil {
		t.Fatalf("decode structured action output %q: %v", actionOutput, err)
	}
	if result.Status != "healthy" {
		t.Errorf("structured action result = %#v", result)
	}
	if _, err := io.WriteString(input, "q\n"); err != nil {
		t.Fatal(err)
	}
	if err := consoleCommand.Wait(); err != nil {
		t.Fatalf("stop application console: %v; output=%q", err, consoleOutput.String())
	}
	if !strings.Contains(consoleOutput.String(), "Application") {
		t.Errorf("application console output = %q", consoleOutput.String())
	}

	node, err := grovetest.StartNode(grovletPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Cleanup() })
	if err := node.WaitReady(ctx); err != nil {
		t.Fatalf("same binary node runtime: %v", err)
	}
}

// Application-native actions drive the known-good deployment and retain it
// after an invalid Inventory candidate is rejected.
func TestGroveShopBinaryRollsBackBrokenConfiguration(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	statePath := filepath.Join(t.TempDir(), "console.json")
	consoleCommand := exec.CommandContext(ctx, grovletPath)
	consoleCommand.Env = append(os.Environ(), consoleStateEnvironment+"="+statePath)
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

	goodConfig := filepath.Join("..", "..", "configs", "acme.yaml")
	activeOutput := runGroveShopAction(t, ctx, statePath, "rollout.start", "--config", goodConfig)
	var active rolloutActionResult
	if err := json.Unmarshal(activeOutput, &active); err != nil {
		t.Fatalf("decode active rollout %q: %v", activeOutput, err)
	}
	if active.State != "active" || active.WebURL == "" || !applicationStatusHealthy(active.Status, active.Status.ActiveArtifact.ArtifactDigest) {
		t.Fatalf("active rollout = %#v; console=%q", active, consoleOutput.String())
	}
	if active.Status.ActiveArtifact.ConfigRevision != "acme-r42" || active.Status.Rollout == nil || active.Status.Rollout.Phase != string(systemnats.RolloutActive) {
		t.Errorf("active artifact status = %#v rollout=%#v", active.Status.ActiveArtifact, active.Status.Rollout)
	}
	before, err := createApplicationOrder(ctx, strings.TrimPrefix(active.WebURL, "http://"), "console-before-rollback")
	if err != nil || !applicationOrderCompleted(before) {
		t.Fatalf("order before rollback = %#v, %v", before, err)
	}

	statusOutput := runGroveShopAction(t, ctx, statePath, "cluster.status")
	var actionStatus groveshop.ClusterStatusView
	if err := json.Unmarshal(statusOutput, &actionStatus); err != nil {
		t.Fatalf("decode cluster.status %q: %v", statusOutput, err)
	}
	if !applicationStatusHealthy(actionStatus, active.Status.ActiveArtifact.ArtifactDigest) {
		t.Errorf("cluster.status = %#v", actionStatus)
	}
	resilienceOutput := runGroveShopAction(t, ctx, statePath, "resilience.run")
	var resilience resilienceActionResult
	if err := json.Unmarshal(resilienceOutput, &resilience); err != nil {
		t.Fatalf("decode resilience result %q: %v", resilienceOutput, err)
	}
	if resilience.FailedNodeID != "node-2" || resilience.RecoveredNodeID != "node-1" || !applicationOrderCompleted(resilience.Order) {
		t.Errorf("resilience result = %#v", resilience)
	}
	if resilience.Status.Health != "degraded" {
		t.Errorf("resilience status health = %q; want degraded while node-2 is unavailable", resilience.Status.Health)
	}
	restartOutput := runGroveShopAction(t, ctx, statePath, "cluster.restart")
	var restarted groveshop.ClusterStatusView
	if err := json.Unmarshal(restartOutput, &restarted); err != nil {
		t.Fatalf("decode restart status %q: %v", restartOutput, err)
	}
	if !applicationStatusHealthy(restarted, active.Status.ActiveArtifact.ArtifactDigest) || applicationPlacementNodeFromStatus(restarted, groveshop.ServiceInventory) != "node-1" {
		t.Errorf("restarted status = %#v", restarted)
	}
	afterRestart, err := createApplicationOrder(ctx, strings.TrimPrefix(active.WebURL, "http://"), "console-after-restart")
	if err != nil || !applicationOrderCompleted(afterRestart) {
		t.Fatalf("order after restart = %#v, %v", afterRestart, err)
	}

	brokenConfig := filepath.Join("..", "..", "configs", "acme-broken.yaml")
	rollbackOutput := runGroveShopAction(t, ctx, statePath, "rollout.start", "--config", brokenConfig)
	var rollback rolloutActionResult
	if err := json.Unmarshal(rollbackOutput, &rollback); err != nil {
		t.Fatalf("decode rollback %q: %v", rollbackOutput, err)
	}
	if rollback.State != "rolled-back" || len(rollback.Transitions) != 2 {
		t.Fatalf("rollback result = %#v", rollback)
	}
	if pending := rollback.Transitions[0]; pending.Rollout == nil || pending.Rollout.Phase != string(systemnats.RolloutPending) || pending.CandidateArtifact == nil || pending.CandidateArtifact.ConfigRevision != "acme-broken-r43" {
		t.Errorf("pending transition = %#v", pending)
	}
	if final := rollback.Status; !applicationStatusHealthy(final, active.Status.ActiveArtifact.ArtifactDigest) || final.Rollout == nil || final.Rollout.Phase != string(systemnats.RolloutRolledBack) || final.Rollout.Failure == nil || final.Rollout.Failure.Field != "inventory.reservation_buffer" {
		t.Errorf("rolled-back status = %#v", final)
	}
	after, err := createApplicationOrder(ctx, strings.TrimPrefix(active.WebURL, "http://"), "console-after-rollback")
	if err != nil || !applicationOrderCompleted(after) {
		t.Fatalf("order after rollback = %#v, %v", after, err)
	}

	if _, err := io.WriteString(input, "q\n"); err != nil {
		t.Fatal(err)
	}
	if err := consoleCommand.Wait(); err != nil {
		t.Fatalf("stop application console: %v; output=%q", err, consoleOutput.String())
	}
	stopped = true
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("console state after lifecycle error = %v; want not exist", err)
	}
}

func runGroveShopAction(t *testing.T, ctx context.Context, statePath string, args ...string) []byte {
	t.Helper()
	command := exec.CommandContext(ctx, grovletPath, append([]string{"action"}, args...)...)
	command.Env = append(os.Environ(), consoleStateEnvironment+"="+statePath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("groveshop action %q: %v; output=%q", args, err, output)
	}
	return output
}

func applicationPlacementNodeFromStatus(status groveshop.ClusterStatusView, serviceID grove.ServiceID) string {
	for _, placement := range status.Placements {
		if placement.ServiceID == serviceID {
			return placement.NodeID
		}
	}
	return ""
}

func waitForConsoleState(t *testing.T, path string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("wait for application console state %q: %v", path, ctx.Err())
		}
	}
}
