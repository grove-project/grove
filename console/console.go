// Package console provides Grove's application-owned operational action model.
package console

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

var (
	// ErrActionNameRequired is returned when an action has no stable name.
	ErrActionNameRequired = errors.New("action name is required")
	// ErrActionLabelRequired is returned when an action has no human label.
	ErrActionLabelRequired = errors.New("action label is required")
	// ErrActionSectionRequired is returned when an action has no TUI section.
	ErrActionSectionRequired = errors.New("action section is required")
	// ErrActionHandlerRequired is returned when an action has no handler.
	ErrActionHandlerRequired = errors.New("action handler is required")
	// ErrActionDuplicate is returned when a stable action name is registered twice.
	ErrActionDuplicate = errors.New("action is already registered")
	// ErrActionUnknown is returned when an action name is not registered.
	ErrActionUnknown = errors.New("action is unknown")
)

// Handler performs one Grove or application-specific operational action.
// Arguments retain their structured-action command order; the result must be
// JSON-encodable when the action is exposed to another process.
type Handler func(context.Context, []string) (any, error)

// Action describes one operation shared by the application TUI and automation.
type Action struct {
	// Name is the stable machine-oriented action identifier.
	Name string
	// Label is the contextual human-facing TUI label.
	Label string
	// Section is the top-level TUI area containing the action.
	Section string
	// Description explains the action before a human invokes it.
	Description string
	// Handler performs the operation for both TUI and structured callers.
	Handler Handler
	// Hidden excludes the action from interactive TUI listings and keyboard
	// navigation while it remains invokable by name through Registry.Invoke
	// and structured automation.
	Hidden bool
}

// ActionError identifies the action involved in a registration or invocation
// failure.
type ActionError struct {
	// Name is the stable action identifier supplied by the caller.
	Name string
	// Err is the validation, registration, or lookup failure.
	Err error
}

func (e *ActionError) Error() string {
	if e.Name == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("action %q: %v", e.Name, e.Err)
}

func (e *ActionError) Unwrap() error {
	return e.Err
}

// Registry stores the operational actions compiled into one application. Its
// zero value is ready to use.
type Registry struct {
	mu      sync.RWMutex
	actions map[string]Action
}

// Register makes an action available to both the TUI and structured callers.
// It validates presentation metadata and never replaces an existing action.
func (r *Registry) Register(action Action) error {
	action.Name = strings.TrimSpace(action.Name)
	action.Label = strings.TrimSpace(action.Label)
	action.Section = strings.TrimSpace(action.Section)
	if action.Name == "" {
		return actionError("", ErrActionNameRequired)
	}
	if action.Label == "" {
		return actionError(action.Name, ErrActionLabelRequired)
	}
	if action.Section == "" {
		return actionError(action.Name, ErrActionSectionRequired)
	}
	if action.Handler == nil {
		return actionError(action.Name, ErrActionHandlerRequired)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.actions == nil {
		r.actions = make(map[string]Action)
	}
	if _, ok := r.actions[action.Name]; ok {
		return actionError(action.Name, ErrActionDuplicate)
	}
	r.actions[action.Name] = action
	return nil
}

// Invoke resolves a stable action name and runs the registered handler.
func (r *Registry) Invoke(ctx context.Context, name string, args []string) (any, error) {
	r.mu.RLock()
	action, ok := r.actions[name]
	r.mu.RUnlock()
	if !ok {
		return nil, actionError(name, ErrActionUnknown)
	}
	return action.Handler(ctx, slices.Clone(args))
}

// Actions returns registered action metadata ordered by section, label, and
// stable name. Handler values are retained so a TUI can invoke the same action.
func (r *Registry) Actions() []Action {
	r.mu.RLock()
	actions := make([]Action, 0, len(r.actions))
	for _, action := range r.actions {
		actions = append(actions, action)
	}
	r.mu.RUnlock()
	slices.SortFunc(actions, func(left, right Action) int {
		if order := strings.Compare(left.Section, right.Section); order != 0 {
			return order
		}
		if order := strings.Compare(left.Label, right.Label); order != 0 {
			return order
		}
		return strings.Compare(left.Name, right.Name)
	})
	return actions
}

func actionError(name string, err error) error {
	return &ActionError{Name: name, Err: err}
}
