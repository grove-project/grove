package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

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
	cfg, err := parseConfig([]string{"--runtime-dir", runtimeDir}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.runtimeDir != runtimeDir {
		t.Errorf("runtime directory = %q; want %q", cfg.runtimeDir, runtimeDir)
	}

	if _, err := parseConfig(nil, io.Discard); !errors.Is(err, errRuntimeDirRequired) {
		t.Errorf("error = %v; want %v", err, errRuntimeDirRequired)
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
	binaryPath := buildGrovlet(t)
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	cmd := exec.Command(binaryPath, "--runtime-dir", runtimeDir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()
	exited := false
	finish := func() error {
		if exited {
			return nil
		}
		_ = cmd.Process.Kill()
		exited = true
		return <-waitDone
	}
	defer finish()

	events := decodeLifecycleEvents(stdout)
	waitCtx, cancelWait := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelWait()

	event, err := receiveLifecycleEvent(waitCtx, events)
	if err != nil {
		waitErr := finish()
		t.Fatalf("wait for readiness: %v; process: %v; stderr: %q", err, waitErr, stderr.String())
	}
	if want := (lifecycleEvent{Event: "ready"}); event != want {
		waitErr := finish()
		t.Fatalf("ready event = %#v; want %#v; process: %v; stderr: %q", event, want, waitErr, stderr.String())
	}
	if info, err := os.Stat(runtimeDir); err != nil || !info.IsDir() {
		waitErr := finish()
		t.Fatalf("runtime directory is not ready: %v; process: %v; stderr: %q", err, waitErr, stderr.String())
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		waitErr := finish()
		t.Fatalf("signal SIGTERM: %v; process: %v; stderr: %q", err, waitErr, stderr.String())
	}
	event, err = receiveLifecycleEvent(waitCtx, events)
	if err != nil {
		waitErr := finish()
		t.Fatalf("wait for shutdown: %v; process: %v; stderr: %q", err, waitErr, stderr.String())
	}
	if want := (lifecycleEvent{Event: "stopped"}); event != want {
		waitErr := finish()
		t.Fatalf("stopped event = %#v; want %#v; process: %v; stderr: %q", event, want, waitErr, stderr.String())
	}

	select {
	case err := <-waitDone:
		exited = true
		if err != nil {
			t.Fatalf("process exit: %v; stderr: %q", err, stderr.String())
		}
	case <-waitCtx.Done():
		waitErr := finish()
		t.Fatalf("wait for process exit: %v; process: %v; stderr: %q", waitCtx.Err(), waitErr, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q; want empty output", stderr.String())
	}

	runtimeFile := filepath.Join(t.TempDir(), "runtime-file")
	if err := os.WriteFile(runtimeFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	failureCtx, cancelFailure := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelFailure()
	failureOutput, err := exec.CommandContext(failureCtx, binaryPath, "--runtime-dir", runtimeFile).CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("startup error = %v; want process exit error; output: %q", err, failureOutput)
	}
	if exitErr.ExitCode() == 0 {
		t.Errorf("startup exit code = 0; want non-zero")
	}
	if got := string(failureOutput); !strings.Contains(got, "runtime directory") || !strings.Contains(got, runtimeFile) {
		t.Errorf("startup output = %q; want runtime-directory diagnostics for %q", got, runtimeFile)
	}
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type eventResult struct {
	event lifecycleEvent
	err   error
}

func buildGrovlet(t *testing.T) string {
	t.Helper()

	binaryPath := filepath.Join(t.TempDir(), "grovlet")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "go", "build", "-o", binaryPath, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("build grovlet: %v; output: %s", err, output)
	}
	return binaryPath
}

func decodeLifecycleEvents(reader io.Reader) <-chan eventResult {
	results := make(chan eventResult, 1)
	go func() {
		defer close(results)
		decoder := json.NewDecoder(reader)
		for {
			var event lifecycleEvent
			if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
				return
			} else if err != nil {
				results <- eventResult{err: err}
				return
			}
			results <- eventResult{event: event}
		}
	}()
	return results
}

func receiveLifecycleEvent(ctx context.Context, results <-chan eventResult) (lifecycleEvent, error) {
	select {
	case <-ctx.Done():
		return lifecycleEvent{}, ctx.Err()
	case result, ok := <-results:
		if !ok {
			return lifecycleEvent{}, io.EOF
		}
		return result.event, result.err
	}
}
