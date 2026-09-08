package grove

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
)

var (
	// ErrRegistryRequired is returned when NewClient receives a nil Registry.
	ErrRegistryRequired = errors.New("registry is required")
	// ErrClientRequired is returned when Call receives a nil Client.
	ErrClientRequired = errors.New("client is required")
	// ErrRouterRequired is returned when NewRoutedClient receives a nil Router.
	ErrRouterRequired = errors.New("router is required")
	// ErrTransportFailure identifies a failure to reach or exchange a response
	// with a routed destination.
	ErrTransportFailure = errors.New("transport failure")
)

var requestSequence atomic.Uint64

// InvocationError identifies the service and method involved in a failed
// Grove call.
type InvocationError struct {
	// ServiceID identifies the requested service.
	ServiceID ServiceID
	// MethodID identifies the requested method.
	MethodID MethodID
	// Err is the lookup, handler, context, or response failure.
	Err error
}

func (e *InvocationError) Error() string {
	return fmt.Sprintf("call service %d method %d: %v", e.ServiceID, e.MethodID, e.Err)
}

func (e *InvocationError) Unwrap() error {
	return e.Err
}

// Router resolves an invocation envelope to a local or remote destination.
// Runtime packages implement Router; application services continue to use
// Call.
type Router interface {
	Route(context.Context, RequestEnvelope) (ResponseEnvelope, error)
}

// Client routes Grove calls without exposing the selected route to application
// code.
type Client struct {
	router Router
}

// NewClient creates a Client that resolves calls through registry.
func NewClient(registry *Registry) (*Client, error) {
	if registry == nil {
		return nil, ErrRegistryRequired
	}
	dispatcher, err := NewDispatcher(registry)
	if err != nil {
		return nil, err
	}
	return &Client{router: localRouter{dispatcher: dispatcher}}, nil
}

// NewRoutedClient creates a Client using router. It changes call routing, not
// the application-facing Call API.
func NewRoutedClient(router Router) (*Client, error) {
	if router == nil {
		return nil, ErrRouterRequired
	}
	return &Client{router: router}, nil
}

// Call invokes one explicit service and method through client and returns its
// application-owned response type. The same call shape is used for local and
// remote routing.
func Call[Request, Response any](
	ctx context.Context,
	client *Client,
	serviceID ServiceID,
	methodID MethodID,
	request Request,
) (Response, error) {
	var zero Response
	if client == nil {
		return zero, invocationError(serviceID, methodID, ErrClientRequired)
	}
	if err := ctx.Err(); err != nil {
		return zero, invocationError(serviceID, methodID, err)
	}

	payload, err := Encode(request)
	if err != nil {
		return zero, invocationError(serviceID, methodID, err)
	}
	requestID := "request-" + strconv.FormatUint(requestSequence.Add(1), 10)
	response, err := client.router.Route(ctx, RequestEnvelope{
		RequestID: requestID,
		ServiceID: serviceID,
		MethodID:  methodID,
		Payload:   payload,
	})
	if err != nil {
		return zero, invocationError(serviceID, methodID, err)
	}
	if response.RequestID != requestID {
		return zero, invocationError(serviceID, methodID, ErrRequestIDMismatch)
	}
	if response.Error != nil {
		return zero, invocationError(serviceID, methodID, response.Error)
	}
	if err := Decode(response.Payload, &zero); err != nil {
		return zero, invocationError(serviceID, methodID, err)
	}
	return zero, nil
}

// Dispatcher executes invocation envelopes against one local Registry.
type Dispatcher struct {
	registry *Registry
}

// NewDispatcher creates a local envelope dispatcher for registry.
func NewDispatcher(registry *Registry) (*Dispatcher, error) {
	if registry == nil {
		return nil, ErrRegistryRequired
	}
	return &Dispatcher{registry: registry}, nil
}

// Dispatch resolves and executes request and returns a correlated response with
// a structured dispatch, handler, or serialization failure.
func (d *Dispatcher) Dispatch(ctx context.Context, request RequestEnvelope) ResponseEnvelope {
	response := ResponseEnvelope{RequestID: request.RequestID}
	handler, err := d.registry.Resolve(request.ServiceID, request.MethodID)
	if err != nil {
		response.Error = responseError(ErrorDispatch, err)
		return response
	}
	response.Payload, err = handler(ctx, request.Payload)
	if err != nil {
		code := ErrorHandler
		var codecErr *CodecError
		var remoteErr *ResponseError
		if errors.As(err, &remoteErr) {
			code = remoteErr.Code
		} else if errors.Is(err, ErrTransportFailure) {
			code = ErrorTransport
		} else if errors.As(err, &codecErr) {
			code = ErrorSerialization
		}
		response.Error = responseError(code, err)
	}
	return response
}

type localRouter struct {
	dispatcher *Dispatcher
}

func (r localRouter) Route(ctx context.Context, request RequestEnvelope) (ResponseEnvelope, error) {
	return r.dispatcher.Dispatch(ctx, request), nil
}

func invocationError(serviceID ServiceID, methodID MethodID, err error) error {
	return &InvocationError{ServiceID: serviceID, MethodID: methodID, Err: err}
}
