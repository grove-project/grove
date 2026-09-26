package runtime

import (
	"context"
	"sort"
	"time"

	"github.com/grove-project/grove/internal/systemnats"
)

// applicationClusterNodeRow is one node as shown by the Cluster flow: its
// derived liveness, the generic hosted-placement view it already exposes to
// the Services flow, and (when unreachable) the reason its own runtime state
// could not be read.
type applicationClusterNodeRow struct {
	NodeID   string                 `json:"node_id"`
	Health   string                 `json:"health"`
	LastSeen string                 `json:"last_seen,omitempty"`
	Hosted   []applicationHostedRow `json:"hosted"`
	Error    string                 `json:"error,omitempty"`
}

// applicationClusterView is the Cluster flow's application-agnostic
// infrastructure/runtime read model: where and how the application is
// running, independent of the Services flow's service/handler drill-down.
type applicationClusterView struct {
	Health          string                      `json:"health"`
	NodesHealthy    int                         `json:"nodes_healthy"`
	NodesTotal      int                         `json:"nodes_total"`
	ServicesHealthy int                         `json:"services_healthy"`
	ServicesTotal   int                         `json:"services_total"`
	Version         string                      `json:"version"`
	ConfigRevision  string                      `json:"config_revision"`
	Uptime          string                      `json:"uptime,omitempty"`
	Nodes           []applicationClusterNodeRow `json:"nodes"`
	Error           string                      `json:"error,omitempty"`
}

// appCluster reads the current control-plane status and the same resolved
// handler-placement view the Services flow uses, then reshapes both into the
// node-first Cluster read model. It introduces no new authoritative state, so
// it always agrees with cluster.status and services.view.
func (c *applicationController) appCluster(ctx context.Context, args []string) (any, error) {
	if len(args) != 0 {
		return nil, errConsoleArguments
	}
	status, statusErr := c.status(ctx)
	c.mu.RLock()
	cluster := c.cluster
	startedAt := c.startedAt
	c.mu.RUnlock()
	var handlers systemnats.HandlerPlacementView
	if cluster != nil {
		view, err := readHandlerPlacement(ctx, cluster.systemNATSURL, status)
		handlers = view
		if err != nil && applicationDeclaresHandlers() && statusErr == nil {
			statusErr = err
		}
	}
	hosted := make(map[string][]applicationHostedRow, len(status.Nodes))
	for _, node := range buildServicesView(handlers, status).Nodes {
		hosted[node.NodeID] = node.Hosted
	}
	result := buildClusterView(status, hosted, startedAt, time.Now())
	if statusErr != nil {
		result.Error = statusErr.Error()
	}
	return result, nil
}

// buildClusterView derives the Cluster summary and per-node rows from the
// generic control-plane status, without adding Groveshop-specific knowledge.
func buildClusterView(
	status ClusterStatus,
	hosted map[string][]applicationHostedRow,
	startedAt time.Time,
	now time.Time,
) applicationClusterView {
	view := applicationClusterView{
		Health:        status.Health,
		NodesTotal:    len(status.Nodes),
		ServicesTotal: len(status.Placements),
		Nodes:         make([]applicationClusterNodeRow, 0, len(status.Nodes)),
	}
	if view.Health == "" {
		view.Health = "unknown"
	}
	for _, node := range status.Nodes {
		if node.Health == string(systemnats.HealthHealthy) {
			view.NodesHealthy++
		}
		view.Nodes = append(view.Nodes, applicationClusterNodeRow{
			NodeID:   node.NodeID,
			Health:   node.Health,
			LastSeen: node.LastSeen,
			Hosted:   hosted[node.NodeID],
			Error:    node.Error,
		})
	}
	sort.Slice(view.Nodes, func(i, j int) bool { return view.Nodes[i].NodeID < view.Nodes[j].NodeID })
	for _, placement := range status.Placements {
		if placement.Health == string(systemnats.ComponentHealthy) || placement.Health == string(systemnats.ComponentDebugging) {
			view.ServicesHealthy++
		}
	}
	if status.ActiveArtifact != nil {
		view.Version = status.ActiveArtifact.CodeVersion
		view.ConfigRevision = status.ActiveArtifact.ConfigRevision
	}
	if !startedAt.IsZero() {
		view.Uptime = formatApplicationDuration(now.Sub(startedAt))
	}
	return view
}
