package main

import (
	"context"
	"errors"
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
	manager := newComponentManager([]componentSpec{{serviceID: 2, name: "Inventory"}}, func(context.Context, componentSpec) (componentProcess, error) {
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

func (p *fakeComponentProcess) Done() <-chan struct{} { return p.done }

func (p *fakeComponentProcess) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
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
