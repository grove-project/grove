package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDebugDemoDeploysFiveDiscoverableWorkers(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), debugE2ETimeout)
	defer cancel()
	statePath := filepath.Join(t.TempDir(), "debug-demo.json")
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller information is unavailable")
	}
	configPath := filepath.Clean(filepath.Join(filepath.Dir(testFile), "../../configs/acme.yaml"))
	command := exec.Command(
		grovePath,
		"deploy",
		"--binary", debugGrovletPath,
		"--config", configPath,
		"--debug-demo",
	)
	var output synchronizedBuffer
	command.Stdout = &output
	command.Stderr = &output
	command.Env = append(os.Environ(),
		localStateEnvironment+"="+statePath,
		"PATH="+filepath.Dir(delvePath)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	defer func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
	}()
	if err := waitForText(ctx, &output, "Grove Shop debug demo ready", "Orders   node-2", "Payment  node-4"); err != nil {
		t.Fatalf("wait for deploy: %v\n%s", err, output.String())
	}
	state, err := readLocalConnectionAt(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.NodeID != "node-1" || state.SystemNATSURL == "" || state.WebURL == "" {
		t.Errorf("local connection = %#v", state)
	}
	status, err := runDiscoveredGroveCommand(ctx, statePath, "status")
	if err != nil || status != "Cluster     healthy\nNodes       5 / 5 healthy\nComponents  5 / 5 healthy\n" {
		t.Fatalf("discovered status = %q, %v\n%s", status, err, output.String())
	}
	components, err := runDiscoveredGroveCommand(ctx, statePath, "components")
	if err != nil {
		t.Fatalf("discovered components: %v; output=%q", err, components)
	}
	for _, row := range []string{
		"node-1  5        Web        healthy",
		"node-2  1        Orders     healthy",
		"node-3  2        Inventory  healthy",
		"node-4  3        Payment    healthy",
		"node-5  4        Shipping   healthy",
	} {
		if !strings.Contains(components, row) {
			t.Errorf("components missing %q:\n%s", row, components)
		}
	}
	order, err := createMVPOrder(ctx, strings.TrimPrefix(state.WebURL, "http://"), "deployed-debug-order")
	if err != nil || order.Status != "Completed" {
		t.Fatalf("deployed distributed order = %#v, %v", order, err)
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stop deploy command: %v\n%s", err, output.String())
		}
	case <-ctx.Done():
		t.Fatalf("deploy command did not stop: %v\n%s", ctx.Err(), output.String())
	}
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("local state remains after shutdown: %v", err)
	}
}

func waitForText(ctx context.Context, output *synchronizedBuffer, fragments ...string) error {
	ticker := time.NewTicker(debugDemoConditionInterval)
	defer ticker.Stop()
	for {
		text := output.String()
		ready := true
		for _, fragment := range fragments {
			ready = ready && strings.Contains(text, fragment)
		}
		if ready {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func readLocalConnectionAt(path string) (localConnection, error) {
	previous, existed := os.LookupEnv(localStateEnvironment)
	if err := os.Setenv(localStateEnvironment, path); err != nil {
		return localConnection{}, err
	}
	defer func() {
		if existed {
			_ = os.Setenv(localStateEnvironment, previous)
		} else {
			_ = os.Unsetenv(localStateEnvironment)
		}
	}()
	return readLocalConnection()
}

func runDiscoveredGroveCommand(ctx context.Context, statePath string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, grovePath, args...)
	command.Env = append(os.Environ(), localStateEnvironment+"="+statePath)
	output, err := command.CombinedOutput()
	return string(output), err
}
