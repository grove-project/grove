package systemnats

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/grove-project/grove"
	"github.com/nats-io/nats.go"
)

const (
	componentSubjectRoot        = "_GROVE.system.components."
	componentCommandSubjectRoot = "_GROVE.system.component_commands."
)

var (
	// ErrComponentControllerRequired is returned when a component endpoint has
	// no local lifecycle controller.
	ErrComponentControllerRequired = errors.New("component controller is required")
	// ErrComponentCommandFailed is returned when a remote lifecycle command
	// cannot complete.
	ErrComponentCommandFailed = errors.New("component command failed")
)

// ComponentState is the locally observed lifecycle of a hosted component.
type ComponentState string

const (
	// ComponentStarting means the worker is starting but not ready.
	ComponentStarting ComponentState = "starting"
	// ComponentHealthy means the worker is ready to receive calls.
	ComponentHealthy ComponentState = "healthy"
	// ComponentStopping means the worker is shutting down.
	ComponentStopping ComponentState = "stopping"
	// ComponentStopped means the worker is not running after an explicit stop.
	ComponentStopped ComponentState = "stopped"
	// ComponentFailed means worker startup or execution ended unexpectedly.
	ComponentFailed ComponentState = "failed"
)

// ComponentStatus describes one component hosted by a Grovlet.
type ComponentStatus struct {
	// ServiceID is the component's stable application-owned service identifier.
	ServiceID grove.ServiceID `json:"service_id"`
	// Name is the component's application-owned display name.
	Name string `json:"name"`
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

// ComponentController owns local hosted component lifecycle.
type ComponentController interface {
	// SnapshotComponents returns the current local hosted-component view.
	SnapshotComponents() ComponentView
	// StartComponent explicitly starts a locally hosted service.
	StartComponent(context.Context, grove.ServiceID) error
	// StopComponent explicitly stops a locally hosted service.
	StopComponent(context.Context, grove.ServiceID) error
}

type componentCommand struct {
	Operation string          `json:"operation"`
	ServiceID grove.ServiceID `json:"service_id"`
}

type componentCommandResponse struct {
	View  ComponentView `json:"view"`
	Error string        `json:"error,omitempty"`
}

// ComponentSubject returns the System NATS query subject for nodeID's local
// component view.
func ComponentSubject(nodeID string) string {
	return componentSubjectRoot + nodeID
}

// ServeComponents registers nodeID's component view and lifecycle command
// endpoints and waits until both subscriptions are active.
func (t *Transport) ServeComponents(ctx context.Context, nodeID string, controller ComponentController) error {
	if controller == nil {
		return &Error{Operation: "serve Grove components", Err: ErrComponentControllerRequired}
	}
	if _, err := t.connection.Subscribe(ComponentSubject(nodeID), func(message *nats.Msg) {
		respondJSON(message, controller.SnapshotComponents())
	}); err != nil {
		return &Error{Operation: "subscribe Grove component view", Err: err}
	}
	if _, err := t.connection.Subscribe(componentCommandSubjectRoot+nodeID, func(message *nats.Msg) {
		var command componentCommand
		if err := json.Unmarshal(message.Data, &command); err != nil {
			respondJSON(message, componentCommandResponse{Error: err.Error()})
			return
		}
		var err error
		switch command.Operation {
		case "start":
			err = controller.StartComponent(ctx, command.ServiceID)
		case "stop":
			err = controller.StopComponent(ctx, command.ServiceID)
		default:
			err = ErrComponentCommandFailed
		}
		response := componentCommandResponse{View: controller.SnapshotComponents()}
		if err != nil {
			response.Error = err.Error()
		}
		respondJSON(message, response)
	}); err != nil {
		return &Error{Operation: "subscribe Grove component commands", Err: err}
	}
	flushCtx, cancel := operationContext(ctx)
	defer cancel()
	if err := t.connection.FlushWithContext(flushCtx); err != nil {
		return &Error{Operation: "activate Grove component endpoints", Err: err}
	}
	return nil
}

func respondJSON(message *nats.Msg, value any) {
	encoded, err := json.Marshal(value)
	if err == nil {
		_ = message.Respond(encoded)
	}
}

// RequestComponents requests nodeID's machine-readable local component view.
func (t *Transport) RequestComponents(ctx context.Context, nodeID string) (ComponentView, error) {
	message, err := t.connection.RequestWithContext(ctx, ComponentSubject(nodeID), nil)
	if err != nil {
		return ComponentView{}, &Error{Operation: "request Grove components", Err: err}
	}
	var view ComponentView
	if err := json.Unmarshal(message.Data, &view); err != nil {
		return ComponentView{}, &Error{Operation: "decode Grove components", Err: err}
	}
	return view, nil
}

// RequestStartComponent asks nodeID to start its hosted serviceID component.
func (t *Transport) RequestStartComponent(ctx context.Context, nodeID string, serviceID grove.ServiceID) (ComponentView, error) {
	return t.requestComponentCommand(ctx, nodeID, componentCommand{Operation: "start", ServiceID: serviceID})
}

// RequestStopComponent asks nodeID to stop its hosted serviceID component.
func (t *Transport) RequestStopComponent(ctx context.Context, nodeID string, serviceID grove.ServiceID) (ComponentView, error) {
	return t.requestComponentCommand(ctx, nodeID, componentCommand{Operation: "stop", ServiceID: serviceID})
}

func (t *Transport) requestComponentCommand(ctx context.Context, nodeID string, command componentCommand) (ComponentView, error) {
	encoded, err := json.Marshal(command)
	if err != nil {
		return ComponentView{}, &Error{Operation: "encode Grove component command", Err: err}
	}
	message, err := t.connection.RequestWithContext(ctx, componentCommandSubjectRoot+nodeID, encoded)
	if err != nil {
		return ComponentView{}, &Error{Operation: "request Grove component command", Err: err}
	}
	var response componentCommandResponse
	if err := json.Unmarshal(message.Data, &response); err != nil {
		return ComponentView{}, &Error{Operation: "decode Grove component command", Err: err}
	}
	if response.Error != "" {
		return response.View, &Error{Operation: "run Grove component command", Err: errors.Join(ErrComponentCommandFailed, errors.New(response.Error))}
	}
	return response.View, nil
}
