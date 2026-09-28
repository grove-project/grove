package runtime

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
	serviceID      grove.ServiceID
	name           string
	kind           string
	subject        string
	artifactDigest string
	codeVersion    string
	listenAddress  string
	options        []string
	// mode selects the execution process. Placement decided that this node
	// hosts the component; mode decides which process on the node runs it.
	mode systemnats.ExecutionMode
}

type componentProcess interface {
	Stop(context.Context) error
	Kill(context.Context) error
	Done() <-chan struct{}
	Err() error
	// Execution identifies the process running the component, which other
	// components may share.
	Execution() processExecution
}

type componentDebugTarget struct {
	serviceID      grove.ServiceID
	serviceName    string
	workerID       string
	artifactDigest string
	codeVersion    string
	processID      int
	execution      processExecution
	generation     uint64
	process        componentProcess
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

func (m *componentManager) start(ctx context.Context, serviceIDs []grove.ServiceID) error {
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
		if state != systemnats.ComponentHealthy && state != systemnats.ComponentDebugging {
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
		status := systemnats.ComponentStatus{
			ServiceID:         component.spec.serviceID,
			Name:              component.spec.name,
			InvocationSubject: component.spec.subject,
			Generation:        component.generation,
			WorkerID:          componentWorkerID(component.spec, component.generation),
			ArtifactDigest:    component.spec.artifactDigest,
			CodeVersion:       component.spec.codeVersion,
			ExecutionMode:     component.spec.mode,
			State:             component.state,
			Error:             component.err,
		}
		if component.process != nil {
			execution := component.process.Execution()
			status.ExecutionMode = execution.mode
			status.ProcessID = execution.processID
			status.PID = execution.pid
		}
		components = append(components, status)
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
	if component.generation != generation || (component.state != systemnats.ComponentHealthy && component.state != systemnats.ComponentDebugging) {
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
	if component.state != systemnats.ComponentHealthy && component.state != systemnats.ComponentDebugging {
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

func (m *componentManager) KillComponent(ctx context.Context, serviceID grove.ServiceID) error {
	component, ok := m.components[serviceID]
	if !ok {
		return errComponentNotHosted
	}
	component.mu.Lock()
	if component.state != systemnats.ComponentHealthy && component.state != systemnats.ComponentDebugging {
		component.mu.Unlock()
		return errComponentTransition
	}
	component.state = systemnats.ComponentStopping
	process := component.process
	component.mu.Unlock()

	err := process.Kill(ctx)
	component.mu.Lock()
	defer component.mu.Unlock()
	component.process = nil
	component.state = systemnats.ComponentFailed
	component.err = "worker killed"
	if err != nil {
		component.err = err.Error()
	}
	return err
}

func (m *componentManager) beginDebug(serviceID grove.ServiceID) (componentDebugTarget, error) {
	component, ok := m.components[serviceID]
	if !ok {
		return componentDebugTarget{}, errComponentNotHosted
	}
	component.mu.Lock()
	if component.state != systemnats.ComponentHealthy || component.process == nil {
		component.mu.Unlock()
		return componentDebugTarget{}, errComponentTransition
	}
	execution := component.process.Execution()
	component.mu.Unlock()
	// A debugger attaches to a whole process. Components sharing the node's
	// application runtime therefore share one debug session at a time.
	for _, otherID := range m.serviceIDs() {
		other := m.components[otherID]
		if other == component {
			continue
		}
		other.mu.Lock()
		busy := other.state == systemnats.ComponentDebugging && other.process != nil &&
			other.process.Execution().processID == execution.processID
		name := other.spec.name
		other.mu.Unlock()
		if busy {
			return componentDebugTarget{}, fmt.Errorf("%w: process %s is already being debugged through %s", errComponentTransition, execution.processID, name)
		}
	}
	component.mu.Lock()
	defer component.mu.Unlock()
	if component.state != systemnats.ComponentHealthy || component.process == nil {
		return componentDebugTarget{}, errComponentTransition
	}
	component.state = systemnats.ComponentDebugging
	return componentDebugTarget{
		serviceID:      component.spec.serviceID,
		serviceName:    component.spec.name,
		workerID:       componentWorkerID(component.spec, component.generation),
		artifactDigest: component.spec.artifactDigest,
		codeVersion:    component.spec.codeVersion,
		processID:      execution.pid,
		execution:      execution,
		generation:     component.generation,
		process:        component.process,
	}, nil
}

func (m *componentManager) endDebug(serviceID grove.ServiceID, generation uint64) {
	component, ok := m.components[serviceID]
	if !ok {
		return
	}
	component.mu.Lock()
	defer component.mu.Unlock()
	if component.generation != generation || component.state != systemnats.ComponentDebugging || component.process == nil {
		return
	}
	select {
	case <-component.process.Done():
		return
	default:
		component.state = systemnats.ComponentHealthy
		component.err = ""
	}
}

func componentWorkerID(spec componentSpec, generation uint64) string {
	if generation == 0 {
		return ""
	}
	return fmt.Sprintf("%s-%d", spec.kind, generation)
}
