// Package inspect is Grove's read-only inspection surface: what is running,
// on which nodes, where each service is placed, which artifact and rollout
// are active, and what is unhealthy.
//
// An Inspector reads control-plane state through a Source, a port with read
// requests only, and turns it into one typed model. It never writes state,
// starts or stops anything, or depends on the application: it reads the
// control plane directly, not the application's ingress, and works for any
// cluster whose System NATS it can reach, whichever process launched the
// nodes. The Grovlet (for ComponentContext.ReadStatus), the operator console
// and the grove CLI all read through it.
//
// The dependency direction is guarded by the module's boundary_test.go:
// inspect builds on the control-plane domain and never on the System NATS
// adapter, which implements Source.
package inspect

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/controlplane"
)

// requestTimeout bounds each control-plane read so one unresponsive node
// cannot stall an inspection.
const requestTimeout = 500 * time.Millisecond

var (
	// ErrNoNodes is returned when an Inspector has no node to read through.
	ErrNoNodes = errors.New("no node to inspect the cluster through")
	// ErrNoHandlerPlacement is returned when no healthy node reports a
	// resolved handler placement.
	ErrNoHandlerPlacement = errors.New("no healthy node reported handler placement")
)

// Source reads control-plane views from one named node. It has read
// requests only; the System NATS transport implements it.
type Source interface {
	RequestClusterView(context.Context, string) (controlplane.ClusterView, error)
	RequestPlacement(context.Context, string) (controlplane.PlacementView, error)
	RequestDeployments(context.Context, string) (controlplane.DeploymentView, error)
	RequestComponents(context.Context, string) (controlplane.ComponentView, error)
	RequestHandlerPlacement(context.Context, string) (controlplane.HandlerPlacementView, error)
}

// Application identifies the application being inspected.
type Application struct {
	// ID selects this application's rollout among the cluster's rollouts.
	ID string
	// Artifact is reported as active until the cluster records a rollout
	// naming a known artifact.
	Artifact Artifact
	// ServiceName names a placed service. Nil, or an empty result, names it
	// "Service <id>".
	ServiceName func(grove.ServiceID) string
}

// Inspector reads one cluster. It is safe for concurrent use when its
// Source is.
type Inspector struct {
	source Source
	app    Application
	via    []string
}

// New returns an Inspector reading through the first of via that answers.
// Hosted components are read from every member node.
func New(source Source, app Application, via ...string) *Inspector {
	return &Inspector{source: source, app: app, via: via}
}

// Nodes reads cluster membership and each node's hosted components. A node
// whose components cannot be read is reported with Error set.
func (i *Inspector) Nodes(ctx context.Context) (Cluster, error) {
	cluster, _, err := i.nodes(ctx)
	return cluster, err
}

// Status reads the whole cluster: membership, hosted components, service
// placement, artifacts and this application's rollout.
func (i *Inspector) Status(ctx context.Context) (Status, error) {
	cluster, nodeID, err := i.nodes(ctx)
	if err != nil {
		return Status{}, err
	}
	placementCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	placement, err := i.source.RequestPlacement(placementCtx, nodeID)
	cancel()
	if err != nil {
		return Status{}, fmt.Errorf("request placement view: %w", err)
	}
	deploymentCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	deployments, err := i.source.RequestDeployments(deploymentCtx, nodeID)
	cancel()
	if err != nil {
		return Status{}, fmt.Errorf("request deployment view: %w", err)
	}
	return build(cluster, placement, deployments, i.app), nil
}

// Handlers asks status's healthy nodes in turn for their resolved handler
// placement and returns the first ready view.
func (i *Inspector) Handlers(ctx context.Context, status Status) (controlplane.HandlerPlacementView, error) {
	lastErr := ErrNoHandlerPlacement
	for _, node := range status.Nodes {
		if node.Health != string(controlplane.HealthHealthy) {
			continue
		}
		requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		view, err := i.source.RequestHandlerPlacement(requestCtx, node.NodeID)
		cancel()
		if err == nil && view.Ready {
			return view, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	return controlplane.HandlerPlacementView{}, lastErr
}

// nodes returns the cluster and the node that answered for it.
func (i *Inspector) nodes(ctx context.Context) (Cluster, string, error) {
	if len(i.via) == 0 {
		return Cluster{}, "", ErrNoNodes
	}
	var view controlplane.ClusterView
	var nodeID string
	var errs []error
	for _, candidate := range i.via {
		requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		answer, err := i.source.RequestClusterView(requestCtx, candidate)
		cancel()
		if err == nil {
			view, nodeID = answer, candidate
			break
		}
		errs = append(errs, fmt.Errorf("%s: %w", candidate, err))
		if ctx.Err() != nil {
			break
		}
	}
	if nodeID == "" {
		return Cluster{}, "", fmt.Errorf("request cluster view: %w", errors.Join(errs...))
	}
	cluster := Cluster{Ready: view.Ready, Nodes: make([]Node, 0, len(view.Nodes))}
	for _, member := range view.Nodes {
		node := Node{
			NodeID:     member.NodeID,
			Health:     string(member.Health),
			Endpoint:   member.AdvertisedEndpoint,
			Components: []Component{},
		}
		componentCtx, cancel := context.WithTimeout(ctx, requestTimeout)
		components, err := i.source.RequestComponents(componentCtx, member.NodeID)
		cancel()
		if err != nil {
			node.Error = err.Error()
		}
		for _, hosted := range components.Components {
			node.Components = append(node.Components, component(hosted))
		}
		cluster.Nodes = append(cluster.Nodes, node)
	}
	return cluster, nodeID, nil
}

func component(hosted controlplane.ComponentStatus) Component {
	return Component{
		ServiceID:         hosted.ServiceID,
		Name:              hosted.Name,
		InvocationSubject: hosted.InvocationSubject,
		Generation:        hosted.Generation,
		WorkerID:          hosted.WorkerID,
		ExecutionMode:     string(hosted.ExecutionMode),
		ProcessID:         hosted.ProcessID,
		PID:               hosted.PID,
		State:             string(hosted.State),
		Error:             hosted.Error,
	}
}
