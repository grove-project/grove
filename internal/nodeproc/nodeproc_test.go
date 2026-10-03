package nodeproc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeNodeEnvironment makes the test binary act as a Grovlet that speaks the
// lifecycle protocol, so Process is tested without building an application.
const fakeNodeEnvironment = "NODEPROC_FAKE_NODE"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeNodeEnvironment); mode != "" {
		os.Exit(runFakeNode(mode))
	}
	os.Exit(m.Run())
}

func runFakeNode(mode string) int {
	runtimeDir := ""
	for i, arg := range os.Args {
		if arg == "--runtime-dir" && i+1 < len(os.Args) {
			runtimeDir = os.Args[i+1]
		}
	}
	if info, err := os.Stat(runtimeDir); err != nil || !info.IsDir() {
		fmt.Fprintln(os.Stderr, "runtime directory missing")
		return 2
	}
	switch mode {
	case "exit":
		fmt.Fprintln(os.Stderr, "failed before ready")
		return 3
	case "garbage":
		fmt.Println("not a lifecycle event")
		time.Sleep(time.Minute)
		return 0
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	encoder := json.NewEncoder(os.Stdout)
	_ = encoder.Encode(Event{Event: EventReady, NodeID: "node-1", SystemNATSURL: "nats://127.0.0.1:4222"})
	fmt.Fprintln(os.Stderr, "serving")
	<-signals
	_ = encoder.Encode(Event{Event: EventStopped})
	return 0
}

func startFake(t *testing.T, mode string, args ...string) *Process {
	t.Helper()
	t.Setenv(fakeNodeEnvironment, mode)
	process, err := Start(os.Args[0], args...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := process.Cleanup(); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	return process
}

func TestProcessLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	process := startFake(t, "serve")
	if err := process.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	if process.State() != "running" {
		t.Errorf("State() = %q; want running", process.State())
	}
	if err := process.Restart(); !errors.Is(err, ErrRunning) {
		t.Errorf("Restart() while running = %v; want %v", err, ErrRunning)
	}
	if err := process.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if process.State() != "stopped" {
		t.Errorf("State() after Stop = %q; want stopped", process.State())
	}
	runtimeDir := process.RuntimeDir()
	if err := process.Restart(); err != nil {
		t.Fatal(err)
	}
	if err := process.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(ctx); err != nil {
		t.Fatal(err)
	}
	if process.RuntimeDir() != runtimeDir {
		t.Errorf("RuntimeDir() changed across restart: %q -> %q", runtimeDir, process.RuntimeDir())
	}
	if got := strings.Count(process.Logs(), `"event":"ready"`); got != 2 {
		t.Errorf("logs hold %d ready events across two lifetimes; want 2:\n%s", got, process.Logs())
	}
	ready, err := ReadyEvent(process.Logs())
	if err != nil || ready.NodeID != "node-1" {
		t.Errorf("ReadyEvent(logs) = %#v, %v", ready, err)
	}
	if err := process.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runtimeDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("runtime directory survived Cleanup: %v", err)
	}
	if err := process.WaitReady(ctx); !errors.Is(err, ErrCleaned) {
		t.Errorf("WaitReady() after Cleanup = %v; want %v", err, ErrCleaned)
	}
}

func TestProcessReportsExitBeforeReady(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	process := startFake(t, "exit")
	err := process.WaitReady(ctx)
	var processErr *Error
	if !errors.As(err, &processErr) || !strings.Contains(processErr.Logs, "failed before ready") {
		t.Errorf("WaitReady() = %v; want an Error carrying the process logs", err)
	}
}

func TestProcessReportsLifecycleProtocolErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	process := startFake(t, "garbage")
	if err := process.WaitReady(ctx); err == nil || !strings.Contains(err.Error(), "unmarshal lifecycle event") {
		t.Errorf("WaitReady() = %v; want a lifecycle protocol error", err)
	}
}

func TestProcessOwnsRuntimeDirectory(t *testing.T) {
	for _, args := range [][]string{{"--runtime-dir", "elsewhere"}, {"--runtime-dir=elsewhere"}} {
		if _, err := Start(os.Args[0], args...); !errors.Is(err, ErrRuntimeDirArgument) {
			t.Errorf("Start(%q) = %v; want %v", args, err, ErrRuntimeDirArgument)
		}
	}
}

func TestReadyEventUsesLatestLifetime(t *testing.T) {
	output := strings.Join([]string{
		`{"event":"ready","node_id":"node-1","system_nats_url":"nats://first"}`,
		`stderr noise`,
		`{"event":"stopped"}`,
		`{"event":"ready","node_id":"node-1"}`,
		`{"event":"ready","node_id":"node-1","system_nats_url":"nats://second","system_nats_route_url":"nats-route://second"}`,
		`{"event":"cluster_formed","detail":"3 of 3 nodes joined"}`,
	}, "\n")
	event, err := ReadyEvent(output)
	if err != nil {
		t.Fatal(err)
	}
	if event.SystemNATSURL != "nats://second" || event.SystemNATSRouteURL != "nats-route://second" {
		t.Errorf("ReadyEvent() = %#v; want the latest ready event", event)
	}
	if _, err := ReadyEvent(`{"event":"stopped"}`); !errors.Is(err, ErrReadyEventMissing) {
		t.Errorf("ReadyEvent(no ready) = %v; want %v", err, ErrReadyEventMissing)
	}
}
