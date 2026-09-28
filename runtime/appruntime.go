package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/grove-project/grove/internal/systemnats"
)

// The node application runtime is one OS process per Grovlet that hosts every
// locally placed component whose execution mode is in-process. The Grovlet
// drives it over two pipes: commands on fd 3 (whose EOF also means the
// Grovlet is gone) and events on fd 4. Application stdout never carries the
// protocol, so application output cannot corrupt it.
const (
	applicationRuntimeCommand = "application-runtime"
	applicationRuntimeStartup = 5 * time.Second
	applicationRuntimeStop    = 5 * time.Second
)

var (
	errApplicationRuntimeExited = errors.New("application runtime exited")
	errComponentKilled          = errors.New("component killed")
)

type runtimeCommand struct {
	ID     uint64          `json:"id"`
	Op     string          `json:"op"` // start, stop, kill
	Launch componentLaunch `json:"launch"`
}

type runtimeEvent struct {
	Event     string `json:"event,omitempty"` // ready, exited
	Reply     uint64 `json:"reply,omitempty"`
	Component string `json:"component,omitempty"`
	Error     string `json:"error,omitempty"`
}

// processExecution identifies the process executing a component. Several
// components report the same processExecution when they share the node's
// application runtime.
type processExecution struct {
	mode      systemnats.ExecutionMode
	processID string
	pid       int
}

// newExecutionStarter starts each component in the execution process its
// spec selects: the node's shared application runtime by default, or a
// dedicated worker when the component is isolated.
func newExecutionStarter(systemNATSURL, placementNodeID string) componentStarter {
	shared := &applicationRuntimeHost{systemNATSURL: systemNATSURL, nodeID: placementNodeID}
	return func(ctx context.Context, spec componentSpec) (componentProcess, error) {
		if spec.mode == systemnats.ExecutionIsolatedProcess {
			return startWorkerProcess(ctx, systemNATSURL, placementNodeID, spec)
		}
		return shared.Start(ctx, spec)
	}
}

// applicationRuntimeHost owns the node's shared application runtime process.
// It launches the process when the first in-process component starts and
// stops it once no component is hosted in it; a runtime that exited is
// replaced by a new generation on the next start.
type applicationRuntimeHost struct {
	systemNATSURL string
	nodeID        string

	mu         sync.Mutex
	current    *applicationRuntimeProcess
	generation uint64
}

func (h *applicationRuntimeHost) Start(ctx context.Context, spec componentSpec) (componentProcess, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	runtime := h.current
	if runtime == nil || runtime.exited() {
		h.generation++
		launched, err := launchApplicationRuntime(ctx, "app-runtime-"+strconv.FormatUint(h.generation, 10), h.systemNATSURL, h.nodeID)
		if err != nil {
			return nil, err
		}
		runtime = launched
		h.current = runtime
	}
	component := &inProcessComponent{host: h, runtime: runtime, kind: spec.kind, done: make(chan struct{})}
	if err := runtime.adopt(component); err != nil {
		return nil, err
	}
	err := runtime.call(ctx, runtimeCommand{Op: "start", Launch: componentLaunch{
		Kind:          spec.kind,
		Subject:       spec.subject,
		ListenAddress: spec.listenAddress,
		Options:       spec.options,
	}})
	if err != nil {
		runtime.release(component)
		h.retireIfIdleLocked(runtime)
		return nil, fmt.Errorf("start %s in %s: %w", spec.name, runtime.id, err)
	}
	return component, nil
}

func (h *applicationRuntimeHost) release(runtime *applicationRuntimeProcess, component *inProcessComponent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	runtime.release(component)
	h.retireIfIdleLocked(runtime)
}

// retireIfIdleLocked stops runtime when it hosts nothing, so a node with no
// in-process components runs no application runtime.
func (h *applicationRuntimeHost) retireIfIdleLocked(runtime *applicationRuntimeProcess) {
	if runtime.hostedCount() != 0 {
		return
	}
	if h.current == runtime {
		h.current = nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), applicationRuntimeStop)
	defer cancel()
	runtime.shutdown(ctx)
}

// applicationRuntimeProcess is one generation of the node application
// runtime as seen from the Grovlet.
type applicationRuntimeProcess struct {
	id       string
	cmd      *exec.Cmd
	commands *os.File

	writeMu sync.Mutex
	encoder *json.Encoder

	mu      sync.Mutex
	nextID  uint64
	pending map[uint64]chan string
	hosted  map[string]*inProcessComponent
	done    chan struct{}
	err     error
}

func launchApplicationRuntime(ctx context.Context, id, systemNATSURL, nodeID string) (*applicationRuntimeProcess, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate Grovlet executable: %w", err)
	}
	commandRead, commandWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create application runtime command pipe: %w", err)
	}
	eventRead, eventWrite, err := os.Pipe()
	if err != nil {
		commandRead.Close()
		commandWrite.Close()
		return nil, fmt.Errorf("create application runtime event pipe: %w", err)
	}
	cmd := exec.Command(executable, applicationRuntimeCommand,
		"--system-nats-url", systemNATSURL,
		"--placement-node-id", nodeID,
		"--command-fd", "3",
		"--event-fd", "4",
	)
	// The Grovlet's stdout is its own lifecycle event stream; application
	// output goes to its diagnostics stream instead.
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{commandRead, eventWrite}
	if err := cmd.Start(); err != nil {
		commandRead.Close()
		commandWrite.Close()
		eventRead.Close()
		eventWrite.Close()
		return nil, fmt.Errorf("start application runtime: %w", err)
	}
	commandRead.Close()
	eventWrite.Close()
	runtime := &applicationRuntimeProcess{
		id:       id,
		cmd:      cmd,
		commands: commandWrite,
		encoder:  json.NewEncoder(commandWrite),
		pending:  make(map[uint64]chan string),
		hosted:   make(map[string]*inProcessComponent),
		done:     make(chan struct{}),
	}
	ready := make(chan struct{})
	go runtime.readEvents(eventRead, ready)

	startCtx, cancel := context.WithTimeout(ctx, applicationRuntimeStartup)
	defer cancel()
	select {
	case <-ready:
		return runtime, nil
	case <-runtime.done:
		return nil, fmt.Errorf("application runtime exited before readiness: %w", runtime.exitErr())
	case <-startCtx.Done():
		runtime.shutdown(context.Background())
		return nil, fmt.Errorf("wait for application runtime readiness: %w", startCtx.Err())
	}
}

func (p *applicationRuntimeProcess) readEvents(events *os.File, ready chan<- struct{}) {
	decoder := json.NewDecoder(events)
	readyOnce := sync.Once{}
	for {
		var event runtimeEvent
		if err := decoder.Decode(&event); err != nil {
			break
		}
		switch {
		case event.Event == "ready":
			readyOnce.Do(func() { close(ready) })
		case event.Event == "exited":
			p.mu.Lock()
			component := p.hosted[event.Component]
			delete(p.hosted, event.Component)
			p.mu.Unlock()
			if component != nil {
				component.finish(errors.New(event.Error))
			}
		case event.Reply != 0:
			p.mu.Lock()
			reply := p.pending[event.Reply]
			delete(p.pending, event.Reply)
			p.mu.Unlock()
			if reply != nil {
				reply <- event.Error
			}
		}
	}
	events.Close()
	waitErr := p.cmd.Wait()
	exitErr := fmt.Errorf("%w: %s", errApplicationRuntimeExited, p.id)
	if waitErr != nil {
		exitErr = fmt.Errorf("%w: %s: %w", errApplicationRuntimeExited, p.id, waitErr)
	}
	p.mu.Lock()
	p.err = exitErr
	hosted := p.hosted
	p.hosted = map[string]*inProcessComponent{}
	pending := p.pending
	p.pending = map[uint64]chan string{}
	p.mu.Unlock()
	close(p.done)
	// A shared runtime failure is the failure of every component it hosts.
	for _, component := range hosted {
		component.finish(exitErr)
	}
	for _, reply := range pending {
		reply <- exitErr.Error()
	}
}

func (p *applicationRuntimeProcess) exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func (p *applicationRuntimeProcess) exitErr() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

func (p *applicationRuntimeProcess) adopt(component *inProcessComponent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	if _, exists := p.hosted[component.kind]; exists {
		return fmt.Errorf("%w: %s already hosts %s", errComponentTransition, p.id, component.kind)
	}
	p.hosted[component.kind] = component
	return nil
}

func (p *applicationRuntimeProcess) release(component *inProcessComponent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hosted[component.kind] == component {
		delete(p.hosted, component.kind)
	}
}

func (p *applicationRuntimeProcess) hostedCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.hosted)
}

// call sends command and waits for the runtime's reply.
func (p *applicationRuntimeProcess) call(ctx context.Context, command runtimeCommand) error {
	reply := make(chan string, 1)
	p.mu.Lock()
	if p.err != nil {
		p.mu.Unlock()
		return p.err
	}
	p.nextID++
	command.ID = p.nextID
	p.pending[command.ID] = reply
	p.mu.Unlock()

	p.writeMu.Lock()
	err := p.encoder.Encode(command)
	p.writeMu.Unlock()
	if err != nil {
		p.mu.Lock()
		delete(p.pending, command.ID)
		p.mu.Unlock()
		return fmt.Errorf("send %s command to %s: %w", command.Op, p.id, err)
	}
	select {
	case message := <-reply:
		if message != "" {
			return errors.New(message)
		}
		return nil
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.pending, command.ID)
		p.mu.Unlock()
		return ctx.Err()
	}
}

// shutdown closes the command pipe, which asks the runtime to stop every
// component and exit, and kills it if it does not exit in time.
func (p *applicationRuntimeProcess) shutdown(ctx context.Context) {
	p.writeMu.Lock()
	_ = p.commands.Close()
	p.writeMu.Unlock()
	select {
	case <-p.done:
	case <-ctx.Done():
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

func (p *applicationRuntimeProcess) pid() int {
	return p.cmd.Process.Pid
}

// inProcessComponent is a component hosted by the node application runtime.
// Stopping or killing it affects only that component; its Done channel also
// closes when the whole runtime process exits.
type inProcessComponent struct {
	host    *applicationRuntimeHost
	runtime *applicationRuntimeProcess
	kind    string

	once sync.Once
	done chan struct{}
	mu   sync.Mutex
	err  error
}

func (c *inProcessComponent) finish(err error) {
	c.once.Do(func() {
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
		close(c.done)
	})
}

func (c *inProcessComponent) Stop(ctx context.Context) error {
	err := c.runtime.call(ctx, runtimeCommand{Op: "stop", Launch: componentLaunch{Kind: c.kind}})
	c.finish(nil)
	c.host.release(c.runtime, c)
	if err != nil && !errors.Is(err, errApplicationRuntimeExited) {
		return err
	}
	return nil
}

func (c *inProcessComponent) Kill(ctx context.Context) error {
	err := c.runtime.call(ctx, runtimeCommand{Op: "kill", Launch: componentLaunch{Kind: c.kind}})
	c.finish(errComponentKilled)
	c.host.release(c.runtime, c)
	if err != nil && !errors.Is(err, errApplicationRuntimeExited) {
		return err
	}
	return nil
}

func (c *inProcessComponent) Done() <-chan struct{} {
	return c.done
}

func (c *inProcessComponent) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *inProcessComponent) Execution() processExecution {
	return processExecution{mode: systemnats.ExecutionInProcess, processID: c.runtime.id, pid: c.runtime.pid()}
}

type applicationRuntimeConfig struct {
	systemNATSURL   string
	placementNodeID string
	commandFD       int
	eventFD         int
}

// runApplicationRuntime is the node application runtime process: it hosts
// the components the Grovlet asks for, each started and stopped on its own,
// and exits when the Grovlet closes the command pipe.
func runApplicationRuntime(ctx context.Context, args []string, _ io.Writer, stderr io.Writer) error {
	var cfg applicationRuntimeConfig
	flags := flag.NewFlagSet("grovlet "+applicationRuntimeCommand, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&cfg.systemNATSURL, "system-nats-url", "", "parent Grovlet System NATS URL")
	flags.StringVar(&cfg.placementNodeID, "placement-node-id", "", "parent placement observer node ID")
	flags.IntVar(&cfg.commandFD, "command-fd", 0, "parent command pipe file descriptor")
	flags.IntVar(&cfg.eventFD, "event-fd", 0, "parent event pipe file descriptor")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse application runtime flags: %w", err)
	}
	if cfg.systemNATSURL == "" || cfg.placementNodeID == "" || cfg.commandFD < 3 || cfg.eventFD < 3 {
		return errors.New("application runtime configuration is incomplete")
	}
	commands := os.NewFile(uintptr(cfg.commandFD), "grovlet-commands")
	events := os.NewFile(uintptr(cfg.eventFD), "grovlet-events")
	if commands == nil || events == nil {
		return errors.New("application runtime parent pipes are unavailable")
	}
	defer commands.Close()
	defer events.Close()

	runtimeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	process, err := newApplicationProcess(runtimeCtx, cfg.systemNATSURL, cfg.placementNodeID)
	if err != nil {
		return err
	}
	defer process.Close()

	var emitMu sync.Mutex
	encoder := json.NewEncoder(events)
	emit := func(event runtimeEvent) {
		emitMu.Lock()
		defer emitMu.Unlock()
		_ = encoder.Encode(event)
	}
	incoming := make(chan runtimeCommand)
	go func() {
		defer cancel()
		decoder := json.NewDecoder(commands)
		for {
			var command runtimeCommand
			if err := decoder.Decode(&command); err != nil {
				return
			}
			select {
			case incoming <- command:
			case <-runtimeCtx.Done():
				return
			}
		}
	}()

	type hostedEntry struct {
		running *runningComponent
		serial  uint64
		// removed ends the entry's failure watch once it leaves hosted.
		removed chan struct{}
	}
	var serials atomic.Uint64
	hosted := make(map[string]hostedEntry)
	failures := make(chan struct {
		kind   string
		serial uint64
	})
	emit(runtimeEvent{Event: "ready"})
	for {
		select {
		case <-runtimeCtx.Done():
			stopCtx, stopCancel := context.WithTimeout(context.Background(), applicationRuntimeStop)
			var errs []error
			for _, entry := range hosted {
				errs = append(errs, entry.running.stop(stopCtx))
			}
			stopCancel()
			return errors.Join(errs...)
		case failure := <-failures:
			entry, ok := hosted[failure.kind]
			if !ok || entry.serial != failure.serial {
				continue
			}
			delete(hosted, failure.kind)
			close(entry.removed)
			entry.running.kill()
			emit(runtimeEvent{Event: "exited", Component: failure.kind, Error: entry.running.failure.Error()})
		case command := <-incoming:
			var err error
			switch command.Op {
			case "start":
				if _, exists := hosted[command.Launch.Kind]; exists {
					err = fmt.Errorf("component %s is already hosted", command.Launch.Kind)
					break
				}
				var running *runningComponent
				running, err = process.host(command.Launch)
				if err != nil {
					break
				}
				entry := hostedEntry{running: running, serial: serials.Add(1), removed: make(chan struct{})}
				hosted[command.Launch.Kind] = entry
				go func(kind string, entry hostedEntry) {
					select {
					case <-entry.running.failed:
						select {
						case failures <- struct {
							kind   string
							serial uint64
						}{kind, entry.serial}:
						case <-entry.removed:
						case <-runtimeCtx.Done():
						}
					case <-entry.removed:
					case <-runtimeCtx.Done():
					}
				}(command.Launch.Kind, entry)
			case "stop", "kill":
				entry, ok := hosted[command.Launch.Kind]
				if !ok {
					break
				}
				delete(hosted, command.Launch.Kind)
				close(entry.removed)
				if command.Op == "kill" {
					entry.running.kill()
					break
				}
				stopCtx, stopCancel := context.WithTimeout(runtimeCtx, applicationRuntimeStop)
				err = entry.running.stop(stopCtx)
				stopCancel()
			default:
				err = fmt.Errorf("unknown application runtime command %q", command.Op)
			}
			reply := runtimeEvent{Reply: command.ID}
			if err != nil {
				reply.Error = err.Error()
			}
			emit(reply)
		}
	}
}
