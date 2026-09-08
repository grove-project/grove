// Package grove provides the explicit application-facing Grove SDK.
package grove

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrDuplicateRegistration is returned when a service method is already
	// registered.
	ErrDuplicateRegistration = errors.New("service method is already registered")
	// ErrNilHandler is returned when Register receives a nil Handler.
	ErrNilHandler = errors.New("handler is nil")
	// ErrUnknownService is returned when a service has no local registrations.
	ErrUnknownService = errors.New("unknown service")
	// ErrUnknownMethod is returned when a known service does not register a
	// requested method.
	ErrUnknownMethod = errors.New("unknown method")
)

// ServiceID is a stable application-owned service identifier.
type ServiceID uint32

// MethodID is a stable application-owned method identifier scoped to one
// ServiceID.
type MethodID uint32

// Handler executes one explicitly registered local service method across
// Grove's serialized application boundary.
type Handler func(context.Context, []byte) ([]byte, error)

// RegistryError identifies the service and method involved in a failed
// registration or resolution.
type RegistryError struct {
	// ServiceID identifies the requested service.
	ServiceID ServiceID
	// MethodID identifies the requested method.
	MethodID MethodID
	// Err is the registration or resolution failure.
	Err error
}

func (e *RegistryError) Error() string {
	return fmt.Sprintf("service %d method %d: %v", e.ServiceID, e.MethodID, e.Err)
}

func (e *RegistryError) Unwrap() error {
	return e.Err
}

// Registry stores the methods that the current process can execute. Its zero
// value is ready to use. Registry contents describe local capability, not
// cluster placement.
type Registry struct {
	mu       sync.RWMutex
	services map[ServiceID]map[MethodID]Handler
}

// Register associates handler with one explicit service and method pair. It
// rejects nil handlers and never replaces an existing registration.
func (r *Registry) Register(serviceID ServiceID, methodID MethodID, handler Handler) error {
	if handler == nil {
		return registryError(serviceID, methodID, ErrNilHandler)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.services == nil {
		r.services = make(map[ServiceID]map[MethodID]Handler)
	}
	methods, ok := r.services[serviceID]
	if !ok {
		methods = make(map[MethodID]Handler)
		r.services[serviceID] = methods
	}
	if _, exists := methods[methodID]; exists {
		return registryError(serviceID, methodID, ErrDuplicateRegistration)
	}
	methods[methodID] = handler
	return nil
}

// Resolve returns the Handler registered for a service and method pair. It
// distinguishes a service with no local registrations from an unregistered
// method on a known service.
func (r *Registry) Resolve(serviceID ServiceID, methodID MethodID) (Handler, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	methods, ok := r.services[serviceID]
	if !ok {
		return nil, registryError(serviceID, methodID, ErrUnknownService)
	}
	handler, ok := methods[methodID]
	if !ok {
		return nil, registryError(serviceID, methodID, ErrUnknownMethod)
	}
	return handler, nil
}

func registryError(serviceID ServiceID, methodID MethodID, err error) error {
	return &RegistryError{ServiceID: serviceID, MethodID: methodID, Err: err}
}
