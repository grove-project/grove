// Package grovetest builds and controls real Grovlet processes in Go tests.
// Node wraps the production node-process supervisor, internal/nodeproc.
//
// The package owns one isolated runtime directory per Node and can compose
// nodes into a local Cluster. It does not model membership, services, or
// network faults.
package grovetest

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/grove-project/grove/internal/nodeproc"
)

var (
	// ErrNodeCleaned is returned when an operation targets a cleaned Node.
	ErrNodeCleaned = nodeproc.ErrCleaned
	// ErrNodeRunning is returned when Restart targets a running Node.
	ErrNodeRunning = nodeproc.ErrRunning
	// ErrRuntimeDirArgument is returned when StartNode arguments try to replace
	// the harness-owned runtime directory.
	ErrRuntimeDirArgument = nodeproc.ErrRuntimeDirArgument
)

// ProcessError reports a failed harness operation together with the process
// output captured before the failure.
type ProcessError = nodeproc.Error

// BuildGrovlet builds applicationPackage into outputDir and returns the
// executable path. The package is explicit so external applications and
// Grove's own test fixtures use the same harness boundary.
func BuildGrovlet(ctx context.Context, outputDir, applicationPackage string) (string, error) {
	return buildGrovlet(ctx, outputDir, applicationPackage, false)
}

// BuildDebugGrovlet builds the Grovlet command with compiler optimizations and
// inlining disabled so source breakpoints bind reliably.
func BuildDebugGrovlet(ctx context.Context, outputDir, applicationPackage string) (string, error) {
	return buildGrovlet(ctx, outputDir, applicationPackage, true)
}

func buildGrovlet(ctx context.Context, outputDir, applicationPackage string, debug bool) (string, error) {
	if strings.TrimSpace(applicationPackage) == "" {
		return "", &ProcessError{Operation: "build Grovlet", Err: errors.New("application package is required")}
	}
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		return "", &ProcessError{Operation: "create build directory", Err: err}
	}

	moduleDir, err := groveModuleDir()
	if err != nil {
		return "", &ProcessError{Operation: "locate Grove module", Err: err}
	}
	binaryName := "grovlet"
	arguments := []string{"build"}
	if debug {
		binaryName = "grovlet-debug"
		arguments = append(arguments, "-gcflags=all=-N -l")
	}
	binaryPath := filepath.Join(outputDir, binaryName)
	arguments = append(arguments, "-o", binaryPath, applicationPackage)
	cmd := exec.CommandContext(ctx, "go", arguments...)
	cmd.Dir = moduleDir
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return "", &ProcessError{
			Operation: "build Grovlet",
			Err:       err,
			Logs:      output.String(),
		}
	}
	return binaryPath, nil
}

func groveModuleDir() (string, error) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("runtime caller information is unavailable")
	}
	return filepath.Dir(filepath.Dir(filename)), nil
}

// Node controls one real Grovlet process. It is the production node-process
// supervisor (internal/nodeproc) plus the identity a Cluster assigns. Its
// lifecycle methods are intended for sequential use by one test goroutine;
// Logs may be called while the process is running.
type Node struct {
	process *nodeproc.Process
	id      string
	port    int
}

// StartNode starts binaryPath as a Grovlet with a new isolated runtime
// directory and additional command arguments. Arguments must not set
// --runtime-dir. Call Cleanup when the test finishes.
func StartNode(binaryPath string, args ...string) (*Node, error) {
	process, err := nodeproc.Start(binaryPath, args...)
	if err != nil {
		return nil, err
	}
	return &Node{process: process}, nil
}

func newNode(binaryPath string) (*Node, error) {
	process, err := nodeproc.New(binaryPath)
	if err != nil {
		return nil, err
	}
	return &Node{process: process}, nil
}

// WaitReady waits until the current process reports its machine-readable
// ready event. The returned error includes captured logs when the context ends,
// the lifecycle stream is invalid, or the process exits first.
func (n *Node) WaitReady(ctx context.Context) error {
	return n.process.WaitReady(ctx)
}

// Stop sends SIGTERM to the current process and waits for its stopped event and
// successful exit.
func (n *Node) Stop(ctx context.Context) error {
	return n.process.Stop(ctx)
}

// Kill forcibly terminates the current process and waits for it to exit.
func (n *Node) Kill(ctx context.Context) error {
	return n.process.Kill(ctx)
}

// Restart starts a new Grovlet process that reuses the Node's runtime
// directory. The previous process must already have exited; call WaitReady to
// wait for the new process to finish starting.
func (n *Node) Restart() error {
	return n.process.Restart()
}

// Logs returns all stdout and stderr captured across the Node's process
// lifetimes.
func (n *Node) Logs() string {
	return n.process.Logs()
}

// ID returns the Node's cluster-local harness identity. A Node created by
// StartNode outside a Cluster has no ID and returns an empty string.
func (n *Node) ID() string {
	return n.id
}

// Port returns the loopback TCP port reserved for this Node by its Cluster.
// The lifecycle-only Grovlet does not bind this port yet. A Node created by
// StartNode outside a Cluster returns zero.
func (n *Node) Port() int {
	return n.port
}

// TempDir returns the isolated runtime directory reused by the Node across
// restarts.
func (n *Node) TempDir() string {
	return n.process.RuntimeDir()
}

// Cleanup forcibly stops a live process and removes the Node's runtime
// directory. It is safe to call Cleanup more than once.
func (n *Node) Cleanup() error {
	return n.process.Cleanup()
}
