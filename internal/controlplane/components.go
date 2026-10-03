package controlplane

import "github.com/grove-project/grove"

// ComponentState is the locally observed lifecycle of a hosted component.
type ComponentState string

const (
	// ComponentStarting means the worker is starting but not ready.
	ComponentStarting ComponentState = "starting"
	// ComponentHealthy means the worker is ready to receive calls.
	ComponentHealthy ComponentState = "healthy"
	// ComponentDebugging means an authorized debugger owns the live worker while
	// ordinary failure supervision remains active.
	ComponentDebugging ComponentState = "debugging"
	// ComponentStopping means the worker is shutting down.
	ComponentStopping ComponentState = "stopping"
	// ComponentStopped means the worker is not running after an explicit stop.
	ComponentStopped ComponentState = "stopped"
	// ComponentFailed means worker startup or execution ended unexpectedly.
	ComponentFailed ComponentState = "failed"
)

// ExecutionMode says inside which kind of process a hosted component runs.
// Placement decides where a component runs; the execution mode decides which
// process on that node runs it.
type ExecutionMode string

const (
	// ExecutionInProcess runs the component in the node's shared application
	// runtime process together with every other in-process component.
	ExecutionInProcess ExecutionMode = "in-process"
	// ExecutionIsolatedProcess runs the component in a dedicated worker
	// process of its own.
	ExecutionIsolatedProcess ExecutionMode = "isolated-process"
)

// ComponentStatus describes one component hosted by a Grovlet.
type ComponentStatus struct {
	// ServiceID is the component's stable application-owned service identifier.
	ServiceID grove.ServiceID `json:"service_id"`
	// Name is the component's application-owned display name.
	Name string `json:"name"`
	// InvocationSubject is this Grovlet's endpoint for the component.
	InvocationSubject string `json:"invocation_subject"`
	// Generation increments whenever this Grovlet starts the component again.
	Generation uint64 `json:"generation"`
	// WorkerID is the stable identity of this component instance generation.
	// It identifies the component, not the process running it: see ProcessID.
	WorkerID string `json:"worker_id,omitempty"`
	// ExecutionMode says whether the component shares the node's application
	// runtime process or runs in a dedicated worker process.
	ExecutionMode ExecutionMode `json:"execution_mode,omitempty"`
	// ProcessID identifies the execution process hosting the component. Every
	// in-process component on a node reports the same ProcessID.
	ProcessID string `json:"process_id,omitempty"`
	// PID is the operating-system process ID of that execution process while
	// it runs; components sharing a process share it.
	PID int `json:"pid,omitempty"`
	// ArtifactDigest identifies the immutable artifact executing this worker.
	ArtifactDigest string `json:"artifact_digest,omitempty"`
	// CodeVersion identifies the application build executing this worker.
	CodeVersion string `json:"code_version,omitempty"`
	// State is the component's current locally observed lifecycle state.
	State ComponentState `json:"state"`
	// Error describes the latest startup or unexpected-exit failure.
	Error string `json:"error,omitempty"`
}

// ComponentView is one Grovlet's machine-readable hosted component state.
type ComponentView struct {
	// Components contains hosted components sorted by service ID.
	Components []ComponentStatus `json:"components"`
}
