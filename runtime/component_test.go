package runtime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
)

// The supervisor publishes each transition while start and stop work remains
// in progress, retains failures, and permits an explicitly requested restart.
func TestComponentManagerLifecycle(t *testing.T) {
	started := make(chan struct{})
	allowStart := make(chan struct{})
	process := newFakeComponentProcess()
	manager := newComponentManager([]componentSpec{{serviceID: 2, name: "Inventory", subject: "inventory.node-a"}}, func(context.Context, componentSpec) (componentProcess, error) {
		close(started)
		<-allowStart
		return process, nil
	})
	startDone := make(chan error, 1)
	go func() { startDone <- manager.StartComponent(t.Context(), 2) }()
	<-started
	assertComponentState(t, manager, systemnats.ComponentStarting)
	close(allowStart)
	if err := <-startDone; err != nil {
		t.Fatal(err)
	}
	assertComponentState(t, manager, systemnats.ComponentHealthy)
	if subject := manager.SnapshotComponents().Components[0].InvocationSubject; subject != "inventory.node-a" {
		t.Errorf("component invocation subject = %q; want inventory.node-a", subject)
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- manager.StopComponent(t.Context(), 2) }()
	<-process.stopCalled
	assertComponentState(t, manager, systemnats.ComponentStopping)
	close(process.allowStop)
	if err := <-stopDone; err != nil {
		t.Fatal(err)
	}
	assertComponentState(t, manager, systemnats.ComponentStopped)

	restarted := newFakeComponentProcess()
	manager.starter = func(context.Context, componentSpec) (componentProcess, error) { return restarted, nil }
	if err := manager.StartComponent(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	restarted.exit(errors.New("worker crashed"))
	failed := waitComponentState(t, manager, systemnats.ComponentFailed)
	if failed.Error == "" {
		t.Error("failed component has no diagnostic error")
	}

	final := newFakeComponentProcess()
	manager.starter = func(context.Context, componentSpec) (componentProcess, error) { return final, nil }
	if err := manager.StartComponent(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	assertComponentState(t, manager, systemnats.ComponentHealthy)
	close(final.allowStop)
	if err := manager.stopAll(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func waitComponentState(t *testing.T, manager *componentManager, want systemnats.ComponentState) systemnats.ComponentStatus {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		view := manager.SnapshotComponents()
		if len(view.Components) == 1 && view.Components[0].State == want {
			return view.Components[0]
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("component did not reach %s: %#v", want, view)
		}
	}
}

func assertComponentState(t *testing.T, manager *componentManager, want systemnats.ComponentState) {
	t.Helper()
	view := manager.SnapshotComponents()
	if len(view.Components) != 1 || view.Components[0].State != want {
		t.Fatalf("component view = %#v; want one %s component", view, want)
	}
}

type fakeComponentProcess struct {
	stopCalled chan struct{}
	allowStop  chan struct{}
	done       chan struct{}
	stopOnce   sync.Once
	doneOnce   sync.Once
	mu         sync.Mutex
	err        error
	// execution defaults to a dedicated process; tests sharing one process
	// between components set it.
	execution processExecution
}

func newFakeComponentProcess() *fakeComponentProcess {
	return &fakeComponentProcess{
		stopCalled: make(chan struct{}),
		allowStop:  make(chan struct{}),
		done:       make(chan struct{}),
	}
}

func (p *fakeComponentProcess) Stop(ctx context.Context) error {
	p.stopOnce.Do(func() { close(p.stopCalled) })
	select {
	case <-p.allowStop:
		p.doneOnce.Do(func() { close(p.done) })
		return p.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *fakeComponentProcess) Kill(context.Context) error {
	p.exit(errors.New("killed"))
	return nil
}

func (p *fakeComponentProcess) Done() <-chan struct{} { return p.done }

func (p *fakeComponentProcess) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

func (p *fakeComponentProcess) Execution() processExecution {
	if p.execution.processID == "" {
		return processExecution{mode: systemnats.ExecutionIsolatedProcess, processID: "fake-worker", pid: 42}
	}
	return p.execution
}

func (p *fakeComponentProcess) exit(err error) {
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
	p.doneOnce.Do(func() { close(p.done) })
}

func TestComponentManagerRejectsInvalidTransitions(t *testing.T) {
	manager := newComponentManager(nil, func(context.Context, componentSpec) (componentProcess, error) {
		return nil, errors.New("start failed")
	})
	if err := manager.StartComponent(t.Context(), grove.ServiceID(9)); !errors.Is(err, errComponentNotHosted) {
		t.Errorf("unknown start error = %v; want %v", err, errComponentNotHosted)
	}
	if err := manager.StopComponent(t.Context(), grove.ServiceID(9)); !errors.Is(err, errComponentNotHosted) {
		t.Errorf("unknown stop error = %v; want %v", err, errComponentNotHosted)
	}
	manager = newComponentManager([]componentSpec{{serviceID: 2, name: "Inventory"}}, manager.starter)
	if err := manager.StartComponent(t.Context(), 2); err == nil {
		t.Fatal("failed starter returned nil error")
	}
	assertComponentState(t, manager, systemnats.ComponentFailed)
}

func TestComponentManagerTracksExactDebuggedWorkerGeneration(t *testing.T) {
	process := newFakeComponentProcess()
	manager := newComponentManager([]componentSpec{{
		serviceID: 3, name: "Payment", kind: "payment",
		artifactDigest: "sha256:artifact", codeVersion: "v32",
	}}, func(context.Context, componentSpec) (componentProcess, error) {
		return process, nil
	})
	if err := manager.StartComponent(t.Context(), 3); err != nil {
		t.Fatal(err)
	}
	target, err := manager.beginDebug(3)
	if err != nil {
		t.Fatal(err)
	}
	if target.workerID != "payment-1" || target.processID != 42 || target.artifactDigest != "sha256:artifact" || target.codeVersion != "v32" {
		t.Errorf("debug target = %#v", target)
	}
	assertComponentState(t, manager, systemnats.ComponentDebugging)
	if _, err := manager.beginDebug(3); !errors.Is(err, errComponentTransition) {
		t.Errorf("second debug session error = %v; want %v", err, errComponentTransition)
	}
	manager.endDebug(3, target.generation+1)
	assertComponentState(t, manager, systemnats.ComponentDebugging)
	manager.endDebug(3, target.generation)
	assertComponentState(t, manager, systemnats.ComponentHealthy)

	target, err = manager.beginDebug(3)
	if err != nil {
		t.Fatal(err)
	}
	process.exit(errors.New("worker exited while debugging"))
	failed := waitComponentState(t, manager, systemnats.ComponentFailed)
	manager.endDebug(3, target.generation)
	if failed.Error != "worker exited while debugging" {
		t.Errorf("debugged worker failure = %#v", failed)
	}
}

// A debugger attaches to a whole process, so components sharing the node's
// application runtime take turns being debugged.
func TestComponentManagerSerializesDebuggingOfOneSharedProcess(t *testing.T) {
	shared := processExecution{mode: systemnats.ExecutionInProcess, processID: "app-runtime-1", pid: 7}
	processes := map[grove.ServiceID]*fakeComponentProcess{2: newFakeComponentProcess(), 3: newFakeComponentProcess()}
	for _, process := range processes {
		process.execution = shared
	}
	manager := newComponentManager([]componentSpec{
		{serviceID: 2, name: "Inventory", kind: "inventory", mode: systemnats.ExecutionInProcess},
		{serviceID: 3, name: "Payment", kind: "payment", mode: systemnats.ExecutionInProcess},
	}, func(_ context.Context, spec componentSpec) (componentProcess, error) {
		return processes[spec.serviceID], nil
	})
	if err := manager.start(t.Context(), []grove.ServiceID{2, 3}); err != nil {
		t.Fatal(err)
	}
	for _, component := range manager.SnapshotComponents().Components {
		if component.ExecutionMode != systemnats.ExecutionInProcess || component.ProcessID != "app-runtime-1" || component.PID != 7 {
			t.Errorf("%s execution = %q %q pid %d; want the shared runtime", component.Name, component.ExecutionMode, component.ProcessID, component.PID)
		}
	}
	target, err := manager.beginDebug(2)
	if err != nil {
		t.Fatal(err)
	}
	if target.processID != 7 || target.execution != shared {
		t.Errorf("debug target = pid %d %#v; want the shared runtime", target.processID, target.execution)
	}
	if _, err := manager.beginDebug(3); !errors.Is(err, errComponentTransition) || !strings.Contains(err.Error(), "Inventory") {
		t.Errorf("debugging Payment in the debugged process = %v; want it refused naming Inventory", err)
	}
	manager.endDebug(2, target.generation)
	if _, err := manager.beginDebug(3); err != nil {
		t.Errorf("debug Payment after the session ended: %v", err)
	}
}
