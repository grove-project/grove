// Package consoleview holds the typed results of the operator console's
// read actions: the services drill-down, health diagnostics and the App
// screens. The runtime builds them; interactive frontends render them. Their
// JSON form is what structured callers of those actions receive.
package consoleview

import (
	"fmt"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/console"
)

// Placement states shown by the services view.
const (
	PlacementHealthy  = "healthy"
	PlacementLost     = "lost"
	PlacementStarting = "starting"
)

// ExecutionIsolated is the execution mode of a component running in a
// dedicated worker process.
const ExecutionIsolated = "isolated-process"

// Invocation is an action, with arguments, that a frontend may offer for a
// row of a view.
type Invocation struct {
	Name string
	Args []string
}

// Execution is the process a placement executes in. Placement says which
// node runs a handler; execution says which process on that node does, and
// several placements usually share the node's application runtime.
type Execution struct {
	ExecutionMode string `json:"execution_mode,omitempty"`
	ProcessID     string `json:"process_id,omitempty"`
	PID           int    `json:"pid,omitempty"`
}

// Label renders the execution process for tables, or "-" when unknown.
func (e Execution) Label() string {
	if e.ProcessID == "" {
		return "-"
	}
	label := e.ProcessID
	if e.PID != 0 {
		label += fmt.Sprintf(" (pid %d)", e.PID)
	}
	if e.ExecutionMode == ExecutionIsolated {
		label += " isolated"
	}
	return label
}

// Placement is one concrete placement of a handler on a node.
type Placement struct {
	ID     string `json:"id"`
	NodeID string `json:"node_id"`
	Status string `json:"status"`
	Owner  bool   `json:"owner,omitempty"`
	Execution
	// Debug attaches a debugger to this placement; empty when none can.
	Debug Invocation `json:"-"`
}

// Process is one execution process on a node and the services it runs.
type Process struct {
	Execution
	Services []string `json:"services"`
}

// Handler is one registered handler and where it runs.
type Handler struct {
	Method     grove.MethodID `json:"method"`
	Name       string         `json:"name"`
	Scaling    string         `json:"scaling"`
	Capability string         `json:"capability,omitempty"`
	Epoch      uint64         `json:"epoch,omitempty"`
	Owner      string         `json:"owner,omitempty"`
	Transfer   bool           `json:"transferring,omitempty"`
	Placements []Placement    `json:"placements"`
}

// Service is one application service and its handlers.
type Service struct {
	ServiceID grove.ServiceID `json:"service_id"`
	Name      string          `json:"name"`
	Status    string          `json:"status"`
	Handlers  []Handler       `json:"handlers"`
}

// PlacementCount is the number of placements across the service's handlers.
func (s Service) PlacementCount() int {
	count := 0
	for _, handler := range s.Handlers {
		count += len(handler.Placements)
	}
	return count
}

// Hosted is the inverse relation: a placement hosted by a node.
type Hosted struct {
	Service string `json:"service"`
	Handler string `json:"handler"`
	Scaling string `json:"scaling"`
	Status  string `json:"status"`
	Owner   bool   `json:"owner,omitempty"`
	Execution
}

// Node is one node, the placements it hosts and its processes.
type Node struct {
	NodeID    string    `json:"node_id"`
	Health    string    `json:"health"`
	Hosted    []Hosted  `json:"hosted"`
	Processes []Process `json:"processes"`
}

// Services is the App -> Services -> Handler -> Placement drill-down and its
// node -> placement inverse.
type Services struct {
	Services []Service `json:"services"`
	Nodes    []Node    `json:"nodes"`
	Error    string    `json:"error,omitempty"`
}

// Logs explains the cluster's health from application, cluster and System
// NATS diagnostics.
type Logs struct {
	Health        string                 `json:"health"`
	Causes        []string               `json:"causes"`
	Application   []string               `json:"application"`
	Cluster       []string               `json:"cluster"`
	SystemNATS    []string               `json:"system_nats"`
	DebugSessions []console.DebugSession `json:"debug_sessions"`
}

// Overview is the application's identity, runtime and deployment.
type Overview struct {
	Name          string    `json:"name"`
	ApplicationID string    `json:"application_id"`
	Version       string    `json:"version"`
	Build         string    `json:"build"`
	Built         string    `json:"built"`
	SDKVersion    string    `json:"sdk_version"`
	GoVersion     string    `json:"go_version"`
	Architecture  string    `json:"architecture"`
	Cluster       string    `json:"cluster"`
	Nodes         int       `json:"nodes"`
	StartedAt     time.Time `json:"started_at"`
}

// Config is the active embedded configuration, read-only.
type Config struct {
	Revision string `json:"revision"`
	Source   string `json:"source"`
	Status   string `json:"status"`
	YAML     string `json:"yaml"`
}

// Route is one registered ingress route.
type Route struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Service string `json:"service"`
}

// Ingress is where the application listens and its routes.
type Ingress struct {
	URL    string  `json:"url"`
	Routes []Route `json:"routes"`
}

// VersionGroup is the nodes running one build.
type VersionGroup struct {
	Version string   `json:"version"`
	Build   string   `json:"build"`
	Nodes   []string `json:"nodes"`
}

// Version is this binary's build and the build distribution across nodes.
type Version struct {
	Version      string         `json:"version"`
	Build        string         `json:"build"`
	Rollout      string         `json:"rollout,omitempty"`
	Distribution []VersionGroup `json:"distribution"`
}
