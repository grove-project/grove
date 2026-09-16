package console

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrRegistryRequired is returned when a TUI has no action registry.
	ErrRegistryRequired = errors.New("action registry is required")
	// ErrModelReaderRequired is returned when a TUI cannot read application state.
	ErrModelReaderRequired = errors.New("console model reader is required")
)

// Model is the application-first state rendered by a Grove TUI. Runtime
// adapters derive it from the same authoritative view exposed to automation.
type Model struct {
	// Application is the application name shown in the TUI title.
	Application string
	// Sections are the application-first top-level navigation areas.
	Sections []string
	// Health is the current cluster health.
	Health string
	// NodesHealthy is the number of currently healthy Grovlets.
	NodesHealthy int
	// NodesTotal is the number of Grovlets in the current view.
	NodesTotal int
	// ServicesHealthy is the number of currently healthy services.
	ServicesHealthy int
	// ServicesTotal is the number of services in the current view.
	ServicesTotal int
	// ActiveVersion is the active application code version.
	ActiveVersion string
	// ConfigRevision is the active embedded configuration revision.
	ConfigRevision string
	// LastEvent explains the latest operationally relevant state change.
	LastEvent string
}

// ModelReader returns the latest application-first console model.
type ModelReader func(context.Context) (Model, error)

// TUI renders application state and dispatches selections through one Registry.
type TUI struct {
	registry  *Registry
	readModel ModelReader
}

// NewTUI creates a TUI over the action registry and authoritative model reader
// shared with non-interactive callers.
func NewTUI(registry *Registry, readModel ModelReader) (*TUI, error) {
	if registry == nil {
		return nil, ErrRegistryRequired
	}
	if readModel == nil {
		return nil, ErrModelReaderRequired
	}
	return &TUI{registry: registry, readModel: readModel}, nil
}

// Render returns a deterministic terminal snapshot of current application
// state and all registered contextual actions.
func (t *TUI) Render(ctx context.Context) (string, error) {
	model, err := t.readModel(ctx)
	if err != nil {
		return "", fmt.Errorf("read console model: %w", err)
	}
	var output strings.Builder
	fmt.Fprintf(&output, "GroveShop %s\n", model.Application)
	fmt.Fprintf(&output, "Cluster  %s\n", displayValue(model.Health))
	fmt.Fprintf(&output, "Nodes    %d / %d healthy\n", model.NodesHealthy, model.NodesTotal)
	fmt.Fprintf(&output, "Services %d / %d healthy\n", model.ServicesHealthy, model.ServicesTotal)
	fmt.Fprintf(&output, "Version  %s\n", displayValue(model.ActiveVersion))
	fmt.Fprintf(&output, "Config   %s\n", displayValue(model.ConfigRevision))
	if model.LastEvent != "" {
		fmt.Fprintf(&output, "Last event\n  %s\n", model.LastEvent)
	}

	actions := t.registry.Actions()
	sections := append([]string(nil), model.Sections...)
	for _, action := range actions {
		if !containsSection(sections, action.Section) {
			sections = append(sections, action.Section)
		}
	}
	for _, section := range sections {
		fmt.Fprintf(&output, "\n%s\n", section)
		for _, action := range actions {
			if action.Section == section {
				fmt.Fprintf(&output, "  %s  [%s]\n", action.Label, action.Name)
			}
		}
	}
	return output.String(), nil
}

// Select dispatches one human TUI selection through the same registered
// handler used by structured automation.
func (t *TUI) Select(ctx context.Context, name string, args []string) (any, error) {
	return t.registry.Invoke(ctx, name, args)
}

func displayValue(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func containsSection(sections []string, want string) bool {
	for _, section := range sections {
		if section == want {
			return true
		}
	}
	return false
}
