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

	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/grovetest"
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
