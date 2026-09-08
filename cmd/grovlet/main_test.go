package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/grove-project/grove/grovetest"
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
	binaryPath, err := grovetest.BuildGrovlet(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	node, err := grovetest.StartNode(binaryPath)
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

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}
