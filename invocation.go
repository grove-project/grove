package grove

import (
	"context"
	"errors"
	"fmt"
)

var (
	// ErrRegistryRequired is returned when NewClient receives a nil Registry.
	ErrRegistryRequired = errors.New("registry is required")
	// ErrClientRequired is returned when Call receives a nil Client.
	ErrClientRequired = errors.New("client is required")
	// ErrResponseType is returned when a Handler returns a value that does not
	// match the response type requested by Call.
	ErrResponseType = errors.New("unexpected handler response type")
)

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

// Client routes Grove calls. NewClient creates a Client whose destinations are
// methods registered in the current process.
type Client struct {
	registry *Registry
}

// NewClient creates a Client that resolves calls through registry.
func NewClient(registry *Registry) (*Client, error) {
	if registry == nil {
		return nil, ErrRegistryRequired
	}
	return &Client{registry: registry}, nil
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

	handler, err := client.registry.Resolve(serviceID, methodID)
	if err != nil {
		return zero, invocationError(serviceID, methodID, err)
	}
	response, err := handler(ctx, request)
	if err != nil {
		return zero, invocationError(serviceID, methodID, err)
	}
	typed, ok := response.(Response)
	if !ok {
		return zero, invocationError(serviceID, methodID, ErrResponseType)
	}
	return typed, nil
}

func invocationError(serviceID ServiceID, methodID MethodID, err error) error {
	return &InvocationError{ServiceID: serviceID, MethodID: methodID, Err: err}
}
