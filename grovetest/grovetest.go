// Package grovetest builds and controls real Grovlet processes in Go tests.
//
// The package owns one isolated runtime directory per Node and can compose
// nodes into a local Cluster. It does not model membership, services, or
// network faults.
package grovetest

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
	"runtime"
	"sync"
	"syscall"
)

var (
	// ErrNodeCleaned is returned when an operation targets a cleaned Node.
	ErrNodeCleaned = errors.New("node has been cleaned up")
	// ErrNodeRunning is returned when Restart targets a running Node.
	ErrNodeRunning = errors.New("node is still running")
)

var (
	errExitedBeforeReady   = errors.New("process exited before reporting readiness")
	errExitedBeforeStopped = errors.New("process exited before reporting graceful shutdown")
)

// ProcessError reports a failed harness operation together with the process
// output captured before the failure.
type ProcessError struct {
	// Operation identifies the harness operation that failed.
	Operation string
	// Err is the underlying build, process, protocol, or context error.
	Err error
	// Logs contains the combined Grovlet stdout and stderr captured so far.
	Logs string
}

func (e *ProcessError) Error() string {
	message := fmt.Sprintf("%s: %v", e.Operation, e.Err)
	if e.Logs == "" {
		return message
	}
	return message + "\nprocess logs:\n" + e.Logs
}

func (e *ProcessError) Unwrap() error {
	return e.Err
}

// BuildGrovlet builds the Grovlet command associated with this grovetest
// package into outputDir and returns the executable path. The caller owns
// outputDir and should call BuildGrovlet once and reuse the result within a
// test-package invocation.
func BuildGrovlet(ctx context.Context, outputDir string) (string, error) {
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		return "", &ProcessError{Operation: "create build directory", Err: err}
	}

	moduleDir, err := groveModuleDir()
	if err != nil {
		return "", &ProcessError{Operation: "locate Grove module", Err: err}
	}
	binaryPath := filepath.Join(outputDir, "grovlet")
	cmd := exec.CommandContext(ctx, "go", "build", "-o", binaryPath, "./cmd/grovlet")
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

// Node controls one real Grovlet process. Its lifecycle methods are intended
// for sequential use by one test goroutine; Logs may be called while the
// process is running.
type Node struct {
	binaryPath string
	id         string
	port       int
	tempDir    string
	logs       lockedBuffer
	process    *nodeProcess
	cleaned    bool
}

// StartNode starts binaryPath as a Grovlet with a new isolated runtime
// directory. Call Cleanup when the test finishes.
func StartNode(binaryPath string) (*Node, error) {
	node, err := newNode(binaryPath)
	if err != nil {
		return nil, err
	}
	if err := node.start(); err != nil {
		_ = node.Cleanup()
		return nil, err
	}
	return node, nil
}

func newNode(binaryPath string) (*Node, error) {
	tempDir, err := os.MkdirTemp("", "grovetest-node-")
	if err != nil {
		return nil, &ProcessError{Operation: "create node runtime directory", Err: err}
	}
	return &Node{binaryPath: binaryPath, tempDir: tempDir}, nil
}

func (n *Node) start() error {
	if n.cleaned {
		return n.failure("start node", ErrNodeCleaned)
	}
	if n.process != nil && n.process.running() {
		return n.failure("start node", ErrNodeRunning)
	}

	stdout := newLifecycleWriter(&n.logs)
	cmd := exec.Command(n.binaryPath, "--runtime-dir", n.tempDir)
	cmd.Stdout = stdout
	cmd.Stderr = &n.logs
	if err := cmd.Start(); err != nil {
		return n.failure("start node", err)
	}

	process := &nodeProcess{
		cmd:    cmd,
		stdout: stdout,
		done:   make(chan struct{}),
	}
	n.process = process
	go func() {
		process.err = cmd.Wait()
		stdout.close()
		close(process.done)
	}()
	return nil
}

// WaitReady waits until the current process reports its machine-readable
// ready event. The returned error includes captured logs when the context ends,
// the lifecycle stream is invalid, or the process exits first.
func (n *Node) WaitReady(ctx context.Context) error {
	process, err := n.currentProcess("wait for readiness")
	if err != nil {
		return err
	}

	select {
	case <-process.stdout.ready:
		select {
		case <-process.done:
			if process.err != nil {
				return n.failure("wait for readiness", process.err)
			}
		default:
		}
		return nil
	case protocolErr := <-process.stdout.protocolErrors:
		return n.failure("wait for readiness", protocolErr)
	case <-process.done:
		if process.err != nil {
			return n.failure("wait for readiness", process.err)
		}
		if channelClosed(process.stdout.ready) {
			return nil
		}
		return n.failure("wait for readiness", process.exitCause(errExitedBeforeReady))
	case <-ctx.Done():
		return n.failure("wait for readiness", ctx.Err())
	}
}

// Stop sends SIGTERM to the current process and waits for its stopped event and
// successful exit.
func (n *Node) Stop(ctx context.Context) error {
	process, err := n.currentProcess("stop node")
	if err != nil {
		return err
	}
	if !process.running() {
		if process.err == nil && channelClosed(process.stdout.stopped) {
			return nil
		}
		return n.failure("stop node", process.exitCause(errExitedBeforeStopped))
	}
	if err := process.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return n.failure("signal node", err)
	}

	if err := n.waitStopped(ctx, process); err != nil {
		return err
	}
	select {
	case <-process.done:
		if process.err != nil {
			return n.failure("stop node", process.err)
		}
		return nil
	case <-ctx.Done():
		return n.failure("stop node", ctx.Err())
	}
}

func (n *Node) waitStopped(ctx context.Context, process *nodeProcess) error {
	select {
	case <-process.stdout.stopped:
		return nil
	case protocolErr := <-process.stdout.protocolErrors:
		return n.failure("wait for graceful shutdown", protocolErr)
	case <-process.done:
		if channelClosed(process.stdout.stopped) {
			return nil
		}
		return n.failure("wait for graceful shutdown", process.exitCause(errExitedBeforeStopped))
	case <-ctx.Done():
		return n.failure("wait for graceful shutdown", ctx.Err())
	}
}

// Kill forcibly terminates the current process and waits for it to exit.
func (n *Node) Kill(ctx context.Context) error {
	process, err := n.currentProcess("kill node")
	if err != nil {
		return err
	}
	if !process.running() {
		return nil
	}
	if err := process.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return n.failure("kill node", err)
	}
	select {
	case <-process.done:
		return nil
	case <-ctx.Done():
		return n.failure("kill node", ctx.Err())
	}
}

// Restart starts a new Grovlet process that reuses the Node's runtime
// directory. The previous process must already have exited; call WaitReady to
// wait for the new process to finish starting.
func (n *Node) Restart() error {
	if n.cleaned {
		return n.failure("restart node", ErrNodeCleaned)
	}
	if n.process != nil && n.process.running() {
		return n.failure("restart node", ErrNodeRunning)
	}
	return n.start()
}

// Logs returns all stdout and stderr captured across the Node's process
// lifetimes.
func (n *Node) Logs() string {
	return n.logs.String()
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
	return n.tempDir
}

// Cleanup forcibly stops a live process and removes the Node's runtime
// directory. It is safe to call Cleanup more than once.
func (n *Node) Cleanup() error {
	if n.cleaned {
		return nil
	}
	if n.process != nil && n.process.running() {
		if err := n.process.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return n.failure("clean up node", err)
		}
		<-n.process.done
	}
	if err := os.RemoveAll(n.tempDir); err != nil {
		return n.failure("remove node runtime directory", err)
	}
	n.cleaned = true
	return nil
}

func (n *Node) currentProcess(operation string) (*nodeProcess, error) {
	if n.cleaned {
		return nil, n.failure(operation, ErrNodeCleaned)
	}
	if n.process == nil {
		return nil, n.failure(operation, errors.New("node has not been started"))
	}
	return n.process, nil
}

func (n *Node) failure(operation string, err error) error {
	return &ProcessError{Operation: operation, Err: err, Logs: n.Logs()}
}

type nodeProcess struct {
	cmd    *exec.Cmd
	stdout *lifecycleWriter
	done   chan struct{}
	err    error
}

func (p *nodeProcess) running() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (p *nodeProcess) exitCause(fallback error) error {
	if p.err != nil {
		return p.err
	}
	return fallback
}

type lifecycleEvent struct {
	Event string `json:"event"`
}

type lifecycleWriter struct {
	logs           *lockedBuffer
	pending        []byte
	ready          chan struct{}
	stopped        chan struct{}
	protocolErrors chan error
	readyOnce      sync.Once
	stoppedOnce    sync.Once
}

func newLifecycleWriter(logs *lockedBuffer) *lifecycleWriter {
	return &lifecycleWriter{
		logs:           logs,
		ready:          make(chan struct{}),
		stopped:        make(chan struct{}),
		protocolErrors: make(chan error, 1),
	}
}

func (w *lifecycleWriter) Write(data []byte) (int, error) {
	_, _ = w.logs.Write(data)
	w.pending = append(w.pending, data...)
	for {
		newline := bytes.IndexByte(w.pending, '\n')
		if newline < 0 {
			return len(data), nil
		}
		line := w.pending[:newline]
		w.pending = w.pending[newline+1:]
		w.consume(line)
	}
}

func (w *lifecycleWriter) consume(line []byte) {
	var event lifecycleEvent
	if err := json.Unmarshal(line, &event); err != nil {
		w.reportProtocolError(fmt.Errorf("unmarshal lifecycle event: %w", err))
		return
	}
	switch event.Event {
	case "ready":
		w.readyOnce.Do(func() { close(w.ready) })
	case "stopped":
		w.stoppedOnce.Do(func() { close(w.stopped) })
	}
}

func (w *lifecycleWriter) close() {
	if len(bytes.TrimSpace(w.pending)) != 0 {
		w.reportProtocolError(io.ErrUnexpectedEOF)
	}
}

func (w *lifecycleWriter) reportProtocolError(err error) {
	select {
	case w.protocolErrors <- err:
	default:
	}
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func channelClosed(channel <-chan struct{}) bool {
	select {
	case <-channel:
		return true
	default:
		return false
	}
}
