// Package scenario is the protocol between the grove CLI and an application
// binary for application-owned scenarios.
//
// The grove CLI never links application code. `grove test` and
// `grove deploy --debug-demo` ask the application binary to describe its
// scenario (Command DescribeCommand) and, for `grove test`, to run its
// end-to-end check against a running cluster (Command CheckCommand). The
// application side is implemented by package runtime from Definition.Scenario.
package scenario

import (
	"errors"

	"github.com/grove-project/grove"
)

const (
	// Command is the application binary's first argument for scenario
	// subcommands.
	Command = "scenario"
	// DescribeCommand prints the application's Description as JSON.
	DescribeCommand = "describe"
	// CheckCommand runs the application's end-to-end check against a running
	// cluster and prints a CheckResult as JSON.
	CheckCommand = "check"
	// ProtocolVersion identifies the Description and CheckResult encoding.
	ProtocolVersion = 1
)

// ErrNoCheck reports an application that declares no end-to-end check.
var ErrNoCheck = errors.New("application declares no end-to-end check")

// Description is an application's scenario as the grove CLI sees it.
type Description struct {
	ProtocolVersion int    `json:"protocol_version"`
	Name            string `json:"name"`
	ApplicationID   string `json:"application_id"`
	// CheckComponents are pinned one per node, in order, before the check
	// runs; one more node hosts no component and runs the check. Empty when
	// the application declares no check.
	CheckComponents []Component `json:"check_components,omitempty"`
	// RecoveryServiceID is the service a resilience run kills by default.
	RecoveryServiceID grove.ServiceID `json:"recovery_service_id,omitempty"`
	// DebugNodeCount and DebugPlacements describe the fixed debug-demo
	// topology; DebugNodeCount is zero when the application has none.
	DebugNodeCount  int         `json:"debug_node_count,omitempty"`
	DebugPlacements []Component `json:"debug_placements,omitempty"`
}

// Component is one application component placed on a scenario node.
type Component struct {
	ServiceID grove.ServiceID `json:"service_id"`
	Name      string          `json:"name"`
	Kind      string          `json:"kind"`
	NodeID    string          `json:"node_id,omitempty"`
	Options   []string        `json:"options,omitempty"`
	// Ingress marks an HTTP ingress component, which needs a listen address.
	Ingress bool `json:"ingress,omitempty"`
}

// CheckResult reports a passed end-to-end check.
type CheckResult struct {
	ProtocolVersion int `json:"protocol_version"`
	// Summary is a short line saying what the check proved.
	Summary string `json:"summary"`
}
