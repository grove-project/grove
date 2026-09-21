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
	// CandidateRevision is the candidate embedded configuration revision.
	CandidateRevision string
	// RolloutPhase is the current durable deployment phase.
	RolloutPhase string
	// IngressURL is the application endpoint exposed by the active deployment.
	IngressURL string
	// DebugSessions are the active service-aware Delve/DAP tunnels.
	DebugSessions []DebugSession
	// LastEvent explains the latest operationally relevant state change.
	LastEvent string
	// StartupAction is the only cluster action offered before this process has
	// entered the normal application TUI. Supported values are cluster.start
	// and cluster.join.
	StartupAction string
	// StartupCluster is the discovered embedded cluster identity.
	StartupCluster string
	// StartupBuild is the short immutable build identity shown at startup.
	StartupBuild string
	// StartupNodes is the discovered cluster size before joining.
	StartupNodes int
	// StartupStatus is the discovered cluster health shown before joining.
	StartupStatus string
}

// DebugSession identifies one active local DAP tunnel and its remote worker.
type DebugSession struct {
	ServiceName string `json:"service_name"`
	NodeID      string `json:"node_id"`
	WorkerID    string `json:"worker_id"`
	DAPEndpoint string `json:"dap_endpoint"`
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
	return t.render(ctx, "")
}

// RenderSelected returns a terminal snapshot with one action marked as the
// current keyboard selection.
func (t *TUI) RenderSelected(ctx context.Context, selectedAction string) (string, error) {
	return t.render(ctx, selectedAction)
}

func (t *TUI) render(ctx context.Context, selectedAction string) (string, error) {
	model, err := t.readModel(ctx)
	if err != nil {
		return "", fmt.Errorf("read console model: %w", err)
	}
	if model.StartupAction != "" {
		return t.renderStartup(model, selectedAction), nil
	}
	var output strings.Builder
	fmt.Fprintf(&output, "GroveShop %s\n", model.Application)
	fmt.Fprintf(&output, "Cluster  %s\n", displayValue(model.Health))
	fmt.Fprintf(&output, "Nodes    %d / %d healthy\n", model.NodesHealthy, model.NodesTotal)
	fmt.Fprintf(&output, "Services %d / %d healthy\n", model.ServicesHealthy, model.ServicesTotal)
	fmt.Fprintf(&output, "Version  %s\n", displayValue(model.ActiveVersion))
	fmt.Fprintf(&output, "Config   %s\n", displayValue(model.ConfigRevision))
	fmt.Fprintf(&output, "Ingress  %s\n", displayValue(model.IngressURL))
	for _, session := range model.DebugSessions {
		fmt.Fprintf(
			&output,
			"Debugger %s %s/%s DAP %s\n",
			displayValue(session.ServiceName),
			displayValue(session.NodeID),
			displayValue(session.WorkerID),
			displayValue(session.DAPEndpoint),
		)
	}
	if model.CandidateRevision != "" {
		fmt.Fprintf(&output, "Candidate %s\n", model.CandidateRevision)
	}
	if model.RolloutPhase != "" {
		fmt.Fprintf(&output, "Rollout  %s\n", model.RolloutPhase)
	}
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
				marker := "  "
				if action.Name == selectedAction {
					marker = "> "
				}
				fmt.Fprintf(&output, "%s%s  [%s]\n", marker, action.Label, action.Name)
			}
		}
	}
	return output.String(), nil
}

func (t *TUI) renderStartup(model Model, selectedAction string) string {
	var output strings.Builder
	fmt.Fprintf(&output, "GroveShop %s\n", model.Application)
	if model.StartupAction == "cluster.start" {
		fmt.Fprintf(&output, "No %s cluster discovered\n\n", model.Application)
		fmt.Fprintf(&output, "%sStart new cluster  [cluster.start]\n", startupMarker(selectedAction, "cluster.start"))
		fmt.Fprintf(&output, "\nApplication  %s\n", model.Application)
		fmt.Fprintf(&output, "Build        %s\n", displayValue(model.StartupBuild))
		fmt.Fprintln(&output, "\nEnter select")
		return output.String()
	}
	fmt.Fprintln(&output, "Grove cluster discovered")
	fmt.Fprintf(&output, "\nCluster  %s\n", displayValue(model.StartupCluster))
	fmt.Fprintf(&output, "Nodes    %d\n", model.StartupNodes)
	fmt.Fprintf(&output, "Build    %s\n", displayValue(model.StartupBuild))
	fmt.Fprintf(&output, "Status   %s\n", displayValue(model.StartupStatus))
	fmt.Fprintf(&output, "\n%sJoin cluster  [cluster.join]\n", startupMarker(selectedAction, "cluster.join"))
	fmt.Fprintln(&output, "\nEnter join   Esc cancel")
	return output.String()
}

func startupMarker(selectedAction, action string) string {
	if selectedAction == "" || selectedAction == action {
		return "> "
	}
	return "  "
}

// Actions returns the ordered actions available for keyboard navigation.
func (t *TUI) Actions() []Action {
	return t.registry.Actions()
}

// ReadModel returns the latest application state for interactive frontends.
func (t *TUI) ReadModel(ctx context.Context) (Model, error) {
	return t.readModel(ctx)
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
