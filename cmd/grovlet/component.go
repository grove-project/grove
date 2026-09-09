package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
)

var (
	errComponentNotHosted  = errors.New("component is not hosted by this Grovlet")
	errComponentTransition = errors.New("component lifecycle transition is invalid")
)

type componentSpec struct {
	serviceID grove.ServiceID
	name      string
	kind      string
	subject   string
}

type componentProcess interface {
	Stop(context.Context) error
	Done() <-chan struct{}
	Err() error
}

type componentStarter func(context.Context, componentSpec) (componentProcess, error)

type managedComponent struct {
	mu         sync.Mutex
	spec       componentSpec
	state      systemnats.ComponentState
	err        string
	process    componentProcess
	generation uint64
}

type componentManager struct {
	components map[grove.ServiceID]*managedComponent
	starter    componentStarter
}

func newComponentManager(specs []componentSpec, starter componentStarter) *componentManager {
	components := make(map[grove.ServiceID]*managedComponent, len(specs))
	for _, spec := range specs {
		components[spec.serviceID] = &managedComponent{spec: spec, state: systemnats.ComponentStopped}
	}
	return &componentManager{components: components, starter: starter}
}

func (m *componentManager) startAll(ctx context.Context) error {
	serviceIDs := m.serviceIDs()
	for _, serviceID := range serviceIDs {
		if err := m.StartComponent(ctx, serviceID); err != nil {
			_ = m.stopAll(ctx)
			return fmt.Errorf("start component %d: %w", serviceID, err)
		}
	}
	return nil
}

func (m *componentManager) stopAll(ctx context.Context) error {
	var stopErrors []error
	for _, serviceID := range m.serviceIDs() {
		component := m.components[serviceID]
		component.mu.Lock()
		state := component.state
		component.mu.Unlock()
		if state != systemnats.ComponentHealthy {
			continue
		}
		if err := m.StopComponent(ctx, serviceID); err != nil {
			stopErrors = append(stopErrors, fmt.Errorf("stop component %d: %w", serviceID, err))
		}
	}
	return errors.Join(stopErrors...)
}

func (m *componentManager) serviceIDs() []grove.ServiceID {
	serviceIDs := make([]grove.ServiceID, 0, len(m.components))
	for serviceID := range m.components {
		serviceIDs = append(serviceIDs, serviceID)
	}
	sort.Slice(serviceIDs, func(i, j int) bool { return serviceIDs[i] < serviceIDs[j] })
	return serviceIDs
}

func (m *componentManager) SnapshotComponents() systemnats.ComponentView {
	components := make([]systemnats.ComponentStatus, 0, len(m.components))
	for _, serviceID := range m.serviceIDs() {
		component := m.components[serviceID]
		component.mu.Lock()
		components = append(components, systemnats.ComponentStatus{
			ServiceID: component.spec.serviceID,
			Name:      component.spec.name,
			State:     component.state,
			Error:     component.err,
		})
		component.mu.Unlock()
	}
	return systemnats.ComponentView{Components: components}
}

func (m *componentManager) StartComponent(ctx context.Context, serviceID grove.ServiceID) error {
	component, ok := m.components[serviceID]
	if !ok {
		return errComponentNotHosted
	}
	component.mu.Lock()
	if component.state != systemnats.ComponentStopped && component.state != systemnats.ComponentFailed {
		component.mu.Unlock()
		return errComponentTransition
	}
	component.state = systemnats.ComponentStarting
	component.err = ""
	component.generation++
	generation := component.generation
	spec := component.spec
	component.mu.Unlock()

	process, err := m.starter(ctx, spec)
	component.mu.Lock()
	if err != nil {
		component.state = systemnats.ComponentFailed
		component.err = err.Error()
		component.mu.Unlock()
		return err
	}
	component.process = process
	component.state = systemnats.ComponentHealthy
	component.mu.Unlock()
	go m.watch(component, generation, process)
	return nil
}

func (m *componentManager) watch(component *managedComponent, generation uint64, process componentProcess) {
	<-process.Done()
	component.mu.Lock()
	defer component.mu.Unlock()
	if component.generation != generation || component.state != systemnats.ComponentHealthy {
		return
	}
	component.state = systemnats.ComponentFailed
	component.process = nil
	if err := process.Err(); err != nil {
		component.err = err.Error()
	} else {
		component.err = "worker exited unexpectedly"
	}
}

func (m *componentManager) StopComponent(ctx context.Context, serviceID grove.ServiceID) error {
	component, ok := m.components[serviceID]
	if !ok {
		return errComponentNotHosted
	}
	component.mu.Lock()
	if component.state != systemnats.ComponentHealthy {
		component.mu.Unlock()
		return errComponentTransition
	}
	component.state = systemnats.ComponentStopping
	process := component.process
	component.mu.Unlock()

	err := process.Stop(ctx)
	component.mu.Lock()
	defer component.mu.Unlock()
	component.generation++
	component.process = nil
	if err != nil {
		component.state = systemnats.ComponentFailed
		component.err = err.Error()
		return err
	}
	component.state = systemnats.ComponentStopped
	component.err = ""
	return nil
}
