// Package nodeproc supervises one local Grovlet node process.
//
// A Process starts an application binary as a Grovlet with its own runtime
// directory, follows the node's lifecycle event stream (see Event) and
// captures its output. internal/localcluster builds local clusters from
// Processes for the operator console and the grove CLI, and grovetest wraps
// them for tests.
package nodeproc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
)

var (
	// ErrCleaned is returned when an operation targets a cleaned Process.
	ErrCleaned = errors.New("node has been cleaned up")
	// ErrRunning is returned when Start or Restart targets a running Process.
	ErrRunning = errors.New("node is still running")
	// ErrRuntimeDirArgument is returned when node arguments try to replace
	// the runtime directory the Process owns.
	ErrRuntimeDirArgument = errors.New("runtime directory argument is owned by the node process supervisor")
)

var (
	errExitedBeforeReady   = errors.New("process exited before reporting readiness")
	errExitedBeforeStopped = errors.New("process exited before reporting graceful shutdown")
)

// Error reports a failed process operation together with the process output
// captured before the failure.
type Error struct {
	// Operation identifies the operation that failed.
	Operation string
	// Err is the underlying process, protocol, or context error.
	Err error
	// Logs contains the combined Grovlet stdout and stderr captured so far.
	Logs string
}

func (e *Error) Error() string {
	message := fmt.Sprintf("%s: %v", e.Operation, e.Err)
	if e.Logs == "" {
		return message
	}
	return message + "\nprocess logs:\n" + e.Logs
}

func (e *Error) Unwrap() error {
	return e.Err
}

// Process controls one Grovlet node process and the runtime directory it
// keeps across restarts. Its lifecycle methods are intended for sequential use
// by one goroutine; Logs may be called while the process is running.
type Process struct {
	binaryPath string
	args       []string
	runtimeDir string
	logs       lockedBuffer
	current    *execution
	cleaned    bool
}

// Start creates a Process for binaryPath with a new runtime directory and
// starts it. Arguments must not set --runtime-dir. Call Cleanup when done.
func Start(binaryPath string, args ...string) (*Process, error) {
	process, err := New(binaryPath, args...)
	if err != nil {
		return nil, err
	}
	if err := process.Start(); err != nil {
		_ = process.Cleanup()
		return nil, err
	}
	return process, nil
}

// New creates a Process and its runtime directory without starting it.
// Arguments must not set --runtime-dir.
func New(binaryPath string, args ...string) (*Process, error) {
	for _, arg := range args {
		if arg == "--runtime-dir" || strings.HasPrefix(arg, "--runtime-dir=") {
			return nil, &Error{Operation: "configure node", Err: ErrRuntimeDirArgument}
		}
	}
	runtimeDir, err := os.MkdirTemp("", "grove-node-")
	if err != nil {
		return nil, &Error{Operation: "create node runtime directory", Err: err}
	}
	return &Process{binaryPath: binaryPath, args: append([]string(nil), args...), runtimeDir: runtimeDir}, nil
}

// Start starts the node process. The previous process, if any, must already
// have exited; the new one reuses the runtime directory. Call WaitReady to
// wait for it to finish starting.
func (p *Process) Start() error {
	if p.cleaned {
		return p.failure("start node", ErrCleaned)
	}
	if p.Running() {
		return p.failure("start node", ErrRunning)
	}

	stdout := newLifecycleWriter(&p.logs)
	args := append([]string{"--runtime-dir", p.runtimeDir}, p.args...)
	cmd := exec.Command(p.binaryPath, args...)
	cmd.Stdout = stdout
	cmd.Stderr = &p.logs
	if err := cmd.Start(); err != nil {
		return p.failure("start node", err)
	}

	current := &execution{
		cmd:    cmd,
		stdout: stdout,
		done:   make(chan struct{}),
	}
	p.current = current
	go func() {
		current.err = cmd.Wait()
		stdout.close()
		close(current.done)
	}()
	return nil
}

// WaitReady waits until the current process reports its ready event. The
// returned error includes captured logs when the context ends, the lifecycle
// stream is invalid, or the process exits first.
func (p *Process) WaitReady(ctx context.Context) error {
	current, err := p.currentExecution("wait for readiness")
	if err != nil {
		return err
	}

	select {
	case <-current.stdout.ready:
		select {
		case <-current.done:
			if current.err != nil {
				return p.failure("wait for readiness", current.err)
			}
		default:
		}
		return nil
	case protocolErr := <-current.stdout.protocolErrors:
		return p.failure("wait for readiness", protocolErr)
	case <-current.done:
		if current.err != nil {
			return p.failure("wait for readiness", current.err)
		}
		if channelClosed(current.stdout.ready) {
			return nil
		}
		return p.failure("wait for readiness", current.exitCause(errExitedBeforeReady))
	case <-ctx.Done():
		return p.failure("wait for readiness", ctx.Err())
	}
}

// Stop sends SIGTERM to the current process and waits for its stopped event and
// successful exit.
func (p *Process) Stop(ctx context.Context) error {
	current, err := p.currentExecution("stop node")
	if err != nil {
		return err
	}
	if !current.running() {
		if current.err == nil && channelClosed(current.stdout.stopped) {
			return nil
		}
		return p.failure("stop node", current.exitCause(errExitedBeforeStopped))
	}
	if err := current.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return p.failure("signal node", err)
	}

	if err := p.waitStopped(ctx, current); err != nil {
		return err
	}
	select {
	case <-current.done:
		if current.err != nil {
			return p.failure("stop node", current.err)
		}
		return nil
	case <-ctx.Done():
		return p.failure("stop node", ctx.Err())
	}
}

func (p *Process) waitStopped(ctx context.Context, current *execution) error {
	select {
	case <-current.stdout.stopped:
		return nil
	case protocolErr := <-current.stdout.protocolErrors:
		return p.failure("wait for graceful shutdown", protocolErr)
	case <-current.done:
		if channelClosed(current.stdout.stopped) {
			return nil
		}
		return p.failure("wait for graceful shutdown", current.exitCause(errExitedBeforeStopped))
	case <-ctx.Done():
		return p.failure("wait for graceful shutdown", ctx.Err())
	}
}

// Kill forcibly terminates the current process and waits for it to exit.
func (p *Process) Kill(ctx context.Context) error {
	current, err := p.currentExecution("kill node")
	if err != nil {
		return err
	}
	if !current.running() {
		return nil
	}
	if err := current.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return p.failure("kill node", err)
	}
	select {
	case <-current.done:
		return nil
	case <-ctx.Done():
		return p.failure("kill node", ctx.Err())
	}
}

// Restart starts a new process that reuses the runtime directory. The
// previous process must already have exited; call WaitReady to wait for the
// new process to finish starting.
func (p *Process) Restart() error {
	if p.cleaned {
		return p.failure("restart node", ErrCleaned)
	}
	if p.Running() {
		return p.failure("restart node", ErrRunning)
	}
	return p.Start()
}

// Logs returns all stdout and stderr captured across the process lifetimes.
func (p *Process) Logs() string {
	return p.logs.String()
}

// RuntimeDir returns the runtime directory reused across restarts.
func (p *Process) RuntimeDir() string {
	return p.runtimeDir
}

// Running reports whether the current process has started and not exited.
func (p *Process) Running() bool {
	return p.current != nil && p.current.running()
}

// State describes the process for diagnostics: "not started", "running",
// "exited with error", "stopped" or "cleaned".
func (p *Process) State() string {
	if p.cleaned {
		return "cleaned"
	}
	if p.current == nil {
		return "not started"
	}
	if p.current.running() {
		return "running"
	}
	if p.current.err != nil {
		return "exited with error"
	}
	return "stopped"
}

// Cleanup forcibly stops a live process and removes the runtime directory.
// It is safe to call Cleanup more than once.
func (p *Process) Cleanup() error {
	if p.cleaned {
		return nil
	}
	if p.Running() {
		if err := p.current.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return p.failure("clean up node", err)
		}
		<-p.current.done
	}
	if err := os.RemoveAll(p.runtimeDir); err != nil {
		return p.failure("remove node runtime directory", err)
	}
	p.cleaned = true
	return nil
}

func (p *Process) currentExecution(operation string) (*execution, error) {
	if p.cleaned {
		return nil, p.failure(operation, ErrCleaned)
	}
	if p.current == nil {
		return nil, p.failure(operation, errors.New("node has not been started"))
	}
	return p.current, nil
}

func (p *Process) failure(operation string, err error) error {
	return &Error{Operation: operation, Err: err, Logs: p.Logs()}
}

// execution is one lifetime of the node process.
type execution struct {
	cmd    *exec.Cmd
	stdout *lifecycleWriter
	done   chan struct{}
	err    error
}

func (e *execution) running() bool {
	select {
	case <-e.done:
		return false
	default:
		return true
	}
}

func (e *execution) exitCause(fallback error) error {
	if e.err != nil {
		return e.err
	}
	return fallback
}

// lifecycleWriter captures stdout and turns its lifecycle events into
// readiness and shutdown signals.
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
	event, err := parseEvent(line)
	if err != nil {
		w.reportProtocolError(fmt.Errorf("unmarshal lifecycle event: %w", err))
		return
	}
	switch event.Event {
	case EventReady:
		w.readyOnce.Do(func() { close(w.ready) })
	case EventStopped:
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
