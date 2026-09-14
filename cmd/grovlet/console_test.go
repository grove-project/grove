package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

func createApplicationOrder(ctx context.Context, webAddress, orderID string) (groveshop.Order, error) {
	requestBody, err := json.Marshal(groveshop.CreateOrderRequest{
		OrderID: orderID, SKU: "coffee-beans", Quantity: 1,
		AmountCents: 1200, ShippingAddress: "31 Grove Lane",
	})
	if err != nil {
		return groveshop.Order{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+webAddress+"/api/orders", bytes.NewReader(requestBody))
	if err != nil {
		return groveshop.Order{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return groveshop.Order{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		return groveshop.Order{}, fmt.Errorf("POST /api/orders: %s: %s", response.Status, body)
	}
	var order groveshop.Order
	if err := json.NewDecoder(response.Body).Decode(&order); err != nil {
		return groveshop.Order{}, err
	}
	return order, nil
}

func applicationOrderCompleted(order groveshop.Order) bool {
	return order.Status == groveshop.OrderCompleted && slices.Equal(order.History, []groveshop.OrderStatus{
		groveshop.OrderCreated,
		groveshop.OrderReserved,
		groveshop.OrderPaid,
		groveshop.OrderShipping,
		groveshop.OrderCompleted,
	})
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
