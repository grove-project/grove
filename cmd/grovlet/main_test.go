package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/grovetest"
	"github.com/grove-project/grove/internal/systemnats"
)

var grovletPath string

// A Grovlet announces when it is ready and when a graceful shutdown completes.
func Example() {
	runtimeDir, err := os.MkdirTemp("", "grovlet-example-")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.RemoveAll(runtimeDir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var stdout bytes.Buffer
	if err := run(ctx, []string{"--runtime-dir", runtimeDir}, &stdout, io.Discard); err != nil {
		fmt.Println(err)
		return
	}

	fmt.Print(stdout.String())
	// Output:
	// {"event":"ready"}
	// {"event":"stopped"}
}

func TestParseConfig(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	cfg, err := parseConfig([]string{
		"--runtime-dir", runtimeDir,
		"--system-nats-url", "nats://127.0.0.1:4222",
		"--system-nats-subject", "_GROVE.system.invoke.node-a",
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.runtimeDir != runtimeDir {
		t.Errorf("runtime directory = %q; want %q", cfg.runtimeDir, runtimeDir)
	}
	if cfg.systemNATSURL != "nats://127.0.0.1:4222" {
		t.Errorf("System NATS URL = %q; want nats://127.0.0.1:4222", cfg.systemNATSURL)
	}
	if cfg.systemNATSSubject != "_GROVE.system.invoke.node-a" {
		t.Errorf("System NATS subject = %q; want _GROVE.system.invoke.node-a", cfg.systemNATSSubject)
	}

	if _, err := parseConfig(nil, io.Discard); !errors.Is(err, errRuntimeDirRequired) {
		t.Errorf("error = %v; want %v", err, errRuntimeDirRequired)
	}
	if _, err := parseConfig([]string{
		"--runtime-dir", runtimeDir,
		"--system-nats-listen", "127.0.0.1:4222",
		"--system-nats-url", "nats://127.0.0.1:4222",
	}, io.Discard); !errors.Is(err, errSystemNATSConflict) {
		t.Errorf("listen and URL error = %v; want %v", err, errSystemNATSConflict)
	}
	if _, err := parseConfig([]string{
		"--runtime-dir", runtimeDir,
		"--system-nats-subject", "_GROVE.system.invoke.node-a",
	}, io.Discard); !errors.Is(err, errSystemNATSRequired) {
		t.Errorf("endpoint without connection error = %v; want %v", err, errSystemNATSRequired)
	}
}

func TestPrepareRuntimeDir(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	if err := prepareRuntimeDir(runtimeDir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Errorf("runtime path %q is not a directory", runtimeDir)
	}

	runtimeFile := filepath.Join(t.TempDir(), "runtime-file")
	if err := os.WriteFile(runtimeFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err = prepareRuntimeDir(runtimeFile)
	var runtimeErr runtimeDirError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("error type = %T; want runtimeDirError", err)
	}
	if runtimeErr.path != runtimeFile {
		t.Errorf("error path = %q; want %q", runtimeErr.path, runtimeFile)
	}
}

func TestRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		var stdout bytes.Buffer
		done := make(chan error, 1)
		go func() {
			done <- run(ctx, []string{"--runtime-dir", t.TempDir()}, &stdout, io.Discard)
		}()

		synctest.Wait()
		const readyOutput = "{\"event\":\"ready\"}\n"
		if got := stdout.String(); got != readyOutput {
			t.Errorf("output before shutdown = %q; want %q", got, readyOutput)
		}
		select {
		case err := <-done:
			t.Fatalf("run returned before shutdown: %v", err)
		default:
		}

		cancel()
		synctest.Wait()
		if err := <-done; err != nil {
			t.Fatal(err)
		}

		const stoppedOutput = readyOutput + "{\"event\":\"stopped\"}\n"
		if got := stdout.String(); got != stoppedOutput {
			t.Errorf("output after shutdown = %q; want %q", got, stoppedOutput)
		}
	})

	wantErr := errors.New("write failed")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := run(ctx, []string{"--runtime-dir", t.TempDir()}, errorWriter{err: wantErr}, io.Discard); !errors.Is(err, wantErr) {
		t.Errorf("error = %v; want %v", err, wantErr)
	}
}

func TestGrovletProcess(t *testing.T) {
	node, err := grovetest.StartNode(grovletPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := node.Cleanup(); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})

	waitCtx, cancelWait := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelWait()
	if err := node.WaitReady(waitCtx); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(node.TempDir()); err != nil || !info.IsDir() {
		t.Fatalf("runtime directory is not ready: %v; logs: %q", err, node.Logs())
	}
	if err := node.Stop(waitCtx); err != nil {
		t.Fatal(err)
	}
}

// Two Grovlets must exchange the transport-independent envelope through the
// embedded System NATS process rather than a same-process shortcut.
func TestGrovletSystemNATSTransport(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	const (
		hostSubject = "_GROVE.system.invoke.host"
		peerSubject = "_GROVE.system.invoke.peer"
	)

	host, err := grovetest.StartNode(
		grovletPath,
		"--system-nats-listen", "127.0.0.1:0",
		"--system-nats-subject", hostSubject,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Cleanup(); err != nil {
			t.Errorf("cleanup host: %v", err)
		}
	})
	if err := host.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	serverURL := systemNATSURLFromLogs(t, host.Logs())

	peer, err := grovetest.StartNode(
		grovletPath,
		"--system-nats-url", serverURL,
		"--system-nats-subject", peerSubject,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := peer.Cleanup(); err != nil {
			t.Errorf("cleanup peer: %v", err)
		}
	})
	if err := peer.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}

	transport, err := systemnats.Connect(ctx, serverURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.Close)
	payload, err := grove.Encode("inventory request")
	if err != nil {
		t.Fatal(err)
	}
	request := grove.RequestEnvelope{
		RequestID: "request-between-grovlets",
		ServiceID: 2,
		MethodID:  1,
		Payload:   payload,
	}
	for _, subject := range []string{hostSubject, peerSubject} {
		response, err := transport.Request(ctx, subject, request)
		if err != nil {
			t.Fatal(err)
		}
		var got string
		if err := grove.Decode(response.Payload, &got); err != nil {
			t.Fatal(err)
		}
		if got != "inventory request" {
			t.Errorf("response through %s = %q; want inventory request", subject, got)
		}
	}

	if err := peer.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := host.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func systemNATSURLFromLogs(t *testing.T, logs string) string {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(logs))
	for {
		var event lifecycleEvent
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatal(err)
		}
		if event.Event == "ready" && event.SystemNATSURL != "" {
			return event.SystemNATSURL
		}
	}
	t.Fatalf("Grovlet logs do not contain a ready System NATS URL: %q", logs)
	return ""
}

func TestMain(m *testing.M) {
	buildDir, err := os.MkdirTemp("", "grovlet-command-build-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	path, buildErr := grovetest.BuildGrovlet(ctx, buildDir)
	cancel()
	if buildErr != nil {
		fmt.Fprintln(os.Stderr, buildErr)
		_ = os.RemoveAll(buildDir)
		os.Exit(1)
	}
	grovletPath = path

	code := m.Run()
	if err := os.RemoveAll(buildDir); err != nil && code == 0 {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}
