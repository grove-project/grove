package localcluster

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
)

const (
	conditionInterval = 25 * time.Millisecond
	attemptTimeout    = time.Second
)

// Serving describes when a started cluster serves its application.
type Serving struct {
	// Observer is the node whose cluster and placement views are read.
	Observer string
	// Nodes is the number of nodes that must be members and healthy.
	Nodes int
	// Placements lists every service that must be placed, with the node it
	// must be placed on, or "" for any node.
	Placements map[grove.ServiceID]string
	// ArtifactDigest is the artifact every placement must run.
	ArtifactDigest string
	// RequireWorker also requires each placed component to report the
	// worker running it, which a debugger attaches to.
	RequireWorker bool
	// NodeIDs, when set, must each report ready cluster and placement views
	// of their own, not just the observer.
	NodeIDs []string
}

// WaitServing waits until the cluster reached through transport matches
// want: every node is healthy, exactly the wanted services are placed on the
// wanted artifact, and each placed component is healthy on its node.
func WaitServing(ctx context.Context, transport *systemnats.Transport, want Serving) error {
	ticker := time.NewTicker(conditionInterval)
	defer ticker.Stop()
	var lastCluster systemnats.ClusterView
	var lastPlacement systemnats.PlacementView
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		ready, attemptErr := servingAttempt(attemptCtx, transport, want, &lastCluster, &lastPlacement)
		cancel()
		if ready {
			return nil
		}
		lastErr = attemptErr
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("cluster=%#v placement=%#v: %w", lastCluster, lastPlacement, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func servingAttempt(
	ctx context.Context,
	transport *systemnats.Transport,
	want Serving,
	lastCluster *systemnats.ClusterView,
	lastPlacement *systemnats.PlacementView,
) (bool, error) {
	cluster, clusterErr := transport.RequestClusterView(ctx, want.Observer)
	placement, placementErr := transport.RequestPlacement(ctx, want.Observer)
	if clusterErr == nil {
		*lastCluster = cluster
	}
	if placementErr == nil {
		*lastPlacement = placement
	}
	attemptErr := errors.Join(clusterErr, placementErr)
	if clusterErr != nil || !cluster.Ready || len(cluster.Nodes) != want.Nodes ||
		placementErr != nil || !placement.Ready || len(placement.Placements) != len(want.Placements) {
		return false, attemptErr
	}
	for _, node := range cluster.Nodes {
		if node.Health != systemnats.HealthHealthy {
			return false, attemptErr
		}
	}
	seen := make(map[grove.ServiceID]bool, len(placement.Placements))
	for _, record := range placement.Placements {
		wantNode, expected := want.Placements[record.ServiceID]
		if !expected || seen[record.ServiceID] || (wantNode != "" && record.NodeID != wantNode) ||
			record.ArtifactDigest != want.ArtifactDigest {
			return false, attemptErr
		}
		seen[record.ServiceID] = true
		healthy, err := componentServing(ctx, transport, record, want.RequireWorker)
		if !healthy {
			return false, errors.Join(attemptErr, err)
		}
	}
	// Every node gates its own views on its own control-plane state, so a
	// cluster can serve through the observer before another node does.
	for _, nodeID := range want.NodeIDs {
		nodeCluster, err := transport.RequestClusterView(ctx, nodeID)
		if err != nil || !nodeCluster.Ready {
			return false, errors.Join(attemptErr, err)
		}
		nodePlacement, err := transport.RequestPlacement(ctx, nodeID)
		if err != nil || !nodePlacement.Ready {
			return false, errors.Join(attemptErr, err)
		}
	}
	return true, nil
}

// componentServing reports whether record's node runs its component healthy
// on the placed invocation subject.
func componentServing(ctx context.Context, transport *systemnats.Transport, record systemnats.PlacementRecord, requireWorker bool) (bool, error) {
	view, err := transport.RequestComponents(ctx, record.NodeID)
	if err != nil {
		return false, err
	}
	for _, component := range view.Components {
		if component.ServiceID == record.ServiceID && component.InvocationSubject == record.InvocationSubject &&
			component.State == systemnats.ComponentHealthy && (!requireWorker || component.WorkerID != "") {
			return true, nil
		}
	}
	return false, nil
}

// Recovery describes a service that must recover after its node failed.
type Recovery struct {
	// Observer is the node whose cluster and placement views are read.
	Observer string
	// Nodes is the number of members, including the failed one.
	Nodes          int
	FailedNodeID   string
	ServiceID      grove.ServiceID
	ArtifactDigest string
}

// WaitRecovery waits until the cluster has detected want.FailedNodeID as
// unavailable, every other node is healthy, each of them observes the
// service placed on a healthy node, and that node runs the component healthy.
// It returns the node the service recovered on.
func WaitRecovery(ctx context.Context, transport *systemnats.Transport, want Recovery) (string, error) {
	ticker := time.NewTicker(conditionInterval)
	defer ticker.Stop()
	var lastCluster systemnats.ClusterView
	var lastPlacement systemnats.PlacementView
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		recoveredNodeID, attemptErr := recoveryAttempt(attemptCtx, transport, want, &lastCluster, &lastPlacement)
		cancel()
		if recoveredNodeID != "" {
			return recoveredNodeID, nil
		}
		lastErr = attemptErr
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return "", fmt.Errorf("cluster=%#v placement=%#v: %w", lastCluster, lastPlacement, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func recoveryAttempt(
	ctx context.Context,
	transport *systemnats.Transport,
	want Recovery,
	lastCluster *systemnats.ClusterView,
	lastPlacement *systemnats.PlacementView,
) (string, error) {
	cluster, clusterErr := transport.RequestClusterView(ctx, want.Observer)
	placement, placementErr := transport.RequestPlacement(ctx, want.Observer)
	if clusterErr == nil {
		*lastCluster = cluster
	}
	if placementErr == nil {
		*lastPlacement = placement
	}
	attemptErr := errors.Join(clusterErr, placementErr)
	if clusterErr != nil || !cluster.Ready || len(cluster.Nodes) != want.Nodes || placementErr != nil || !placement.Ready {
		return "", attemptErr
	}
	failedObserved := false
	healthyNodes := make(map[string]bool, len(cluster.Nodes))
	for _, node := range cluster.Nodes {
		if node.NodeID == want.FailedNodeID {
			failedObserved = node.Health == systemnats.HealthUnavailable
			continue
		}
		if node.Health != systemnats.HealthHealthy {
			return "", attemptErr
		}
		healthyNodes[node.NodeID] = true
	}
	if !failedObserved {
		return "", attemptErr
	}
	var recovered systemnats.PlacementRecord
	for _, record := range placement.Placements {
		if record.ServiceID == want.ServiceID && record.NodeID != want.FailedNodeID &&
			record.ArtifactDigest == want.ArtifactDigest && healthyNodes[record.NodeID] {
			recovered = record
			break
		}
	}
	if recovered.NodeID == "" {
		return "", attemptErr
	}
	for nodeID := range healthyNodes {
		view, err := transport.RequestPlacement(ctx, nodeID)
		if err != nil || !view.Ready {
			return "", errors.Join(attemptErr, err)
		}
		observed := false
		for _, record := range view.Placements {
			if record == recovered {
				observed = true
				break
			}
		}
		if !observed {
			return "", attemptErr
		}
	}
	healthy, err := componentServing(ctx, transport, recovered, false)
	if !healthy {
		return "", errors.Join(attemptErr, err)
	}
	return recovered.NodeID, nil
}
