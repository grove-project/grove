package inspect

import (
	"fmt"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/controlplane"
)

// Cluster is cluster membership with each node's hosted components.
type Cluster struct {
	// Ready reports whether the cluster's membership view has settled.
	Ready bool
	Nodes []Node
}

// Status is Grove's application-agnostic read model of one cluster. Its JSON
// form is stable for tools beyond the console.
type Status struct {
	// Health is "healthy" when the cluster is ready, every node is healthy
	// and every placed service is served by a healthy component; otherwise
	// "degraded". Readers that are not attached to a cluster may report
	// their own values, such as "not-deployed" or "starting".
	Health            string      `json:"health"`
	Ready             bool        `json:"ready"`
	Nodes             []Node      `json:"nodes"`
	Placements        []Placement `json:"placements"`
	ActiveArtifact    *Artifact   `json:"active_artifact,omitempty"`
	CandidateArtifact *Artifact   `json:"candidate_artifact,omitempty"`
	Rollout           *Rollout    `json:"rollout,omitempty"`
}

// Node is one cluster member and the components it hosts.
type Node struct {
	NodeID string `json:"node_id"`
	Health string `json:"health"`
	// Endpoint is the node's advertised endpoint.
	Endpoint   string      `json:"endpoint,omitempty"`
	Components []Component `json:"components"`
	// Error says why the node's components could not be read.
	Error string `json:"error,omitempty"`
}

// Component is one component hosted on a node.
type Component struct {
	ServiceID grove.ServiceID `json:"service_id"`
	Name      string          `json:"name"`
	// InvocationSubject is the node's endpoint for the component.
	InvocationSubject string `json:"invocation_subject,omitempty"`
	// Generation increments whenever the node starts the component again.
	Generation uint64 `json:"generation,omitempty"`
	// WorkerID identifies the component instance generation, not a process.
	WorkerID string `json:"worker_id"`
	// ExecutionMode, ProcessID and PID identify the process running the
	// component. Components in the node's shared application runtime report
	// the same process.
	ExecutionMode string `json:"execution_mode,omitempty"`
	ProcessID     string `json:"process_id,omitempty"`
	PID           int    `json:"pid,omitempty"`
	State         string `json:"state"`
	Error         string `json:"error,omitempty"`
}

// Healthy reports whether the component serves calls; a debugged component
// still does.
func (c Component) Healthy() bool {
	return ServingState(c.State)
}

// Placement is where one service is placed and how healthy it is there.
type Placement struct {
	ServiceID         grove.ServiceID `json:"service_id"`
	Name              string          `json:"name"`
	NodeID            string          `json:"node_id"`
	InvocationSubject string          `json:"invocation_subject"`
	ArtifactDigest    string          `json:"artifact_digest"`
	// Health is the placed component's state, or "unavailable" when the
	// node does not host it on the placed subject.
	Health string `json:"health"`
}

// Artifact identifies an immutable application build and configuration.
type Artifact struct {
	ApplicationID  string `json:"application_id"`
	CodeVersion    string `json:"code_version"`
	ArtifactDigest string `json:"artifact_digest"`
	ConfigRevision string `json:"config_revision"`
	ConfigDigest   string `json:"config_digest"`
}

// Rollout is the application's current rollout.
type Rollout struct {
	Generation uint64          `json:"generation"`
	Phase      string          `json:"phase"`
	Failure    *RolloutFailure `json:"failure,omitempty"`
}

// RolloutFailure is why a rollout's candidate was rejected.
type RolloutFailure struct {
	Code      string `json:"code"`
	Component string `json:"component,omitempty"`
	Field     string `json:"field,omitempty"`
	Message   string `json:"message"`
}

// ServingState reports whether a component in state serves calls.
func ServingState(state string) bool {
	return state == string(controlplane.ComponentHealthy) || state == string(controlplane.ComponentDebugging)
}

// build derives Status from control-plane views. It is pure.
func build(
	cluster Cluster,
	placement controlplane.PlacementView,
	deployments controlplane.DeploymentView,
	app Application,
) Status {
	active := app.Artifact
	status := Status{
		Health:         "healthy",
		Ready:          cluster.Ready && placement.Ready && deployments.Ready,
		Nodes:          cluster.Nodes,
		Placements:     make([]Placement, 0, len(placement.Placements)),
		ActiveArtifact: &active,
	}
	if status.Nodes == nil {
		status.Nodes = []Node{}
	}
	hosted := make(map[string][]Component, len(cluster.Nodes))
	for _, node := range cluster.Nodes {
		hosted[node.NodeID] = node.Components
		if node.Health != string(controlplane.HealthHealthy) {
			status.Health = "degraded"
		}
	}
	for _, record := range placement.Placements {
		placed := Placement{
			ServiceID:         record.ServiceID,
			Name:              serviceName(app, record.ServiceID),
			NodeID:            record.NodeID,
			InvocationSubject: record.InvocationSubject,
			ArtifactDigest:    record.ArtifactDigest,
			Health:            "unavailable",
		}
		for _, component := range hosted[record.NodeID] {
			if component.ServiceID == record.ServiceID && component.InvocationSubject == record.InvocationSubject {
				placed.Health = component.State
				break
			}
		}
		if !ServingState(placed.Health) {
			status.Health = "degraded"
		}
		status.Placements = append(status.Placements, placed)
	}
	if !status.Ready || len(status.Nodes) == 0 || len(status.Placements) == 0 {
		status.Health = "degraded"
	}

	artifacts := make(map[string]Artifact, len(deployments.Artifacts))
	for _, record := range deployments.Artifacts {
		artifacts[record.ArtifactDigest] = Artifact{
			ApplicationID:  record.ApplicationID,
			CodeVersion:    record.CodeVersion,
			ArtifactDigest: record.ArtifactDigest,
			ConfigRevision: record.ConfigRevision,
			ConfigDigest:   record.ConfigDigest,
		}
	}
	for _, rollout := range deployments.Rollouts {
		if rollout.ApplicationID != app.ID {
			continue
		}
		view := Rollout{Generation: rollout.Generation, Phase: string(rollout.Phase)}
		if rollout.Failure != nil {
			view.Failure = &RolloutFailure{
				Code:      rollout.Failure.Code,
				Component: rollout.Failure.Component,
				Field:     rollout.Failure.Field,
				Message:   rollout.Failure.Message,
			}
		}
		status.Rollout = &view
		if current, ok := artifacts[rollout.CurrentArtifactDigest]; ok {
			status.ActiveArtifact = &current
		}
		if candidate, ok := artifacts[rollout.CandidateArtifactDigest]; ok {
			status.CandidateArtifact = &candidate
		}
		break
	}
	return status
}

func serviceName(app Application, serviceID grove.ServiceID) string {
	if app.ServiceName != nil {
		if name := app.ServiceName(serviceID); name != "" {
			return name
		}
	}
	return fmt.Sprintf("Service %d", serviceID)
}
