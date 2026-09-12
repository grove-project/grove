package main

import (
	"context"
	"errors"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
)

const recoveryInterval = 50 * time.Millisecond

var errRecoveryComponentUnavailable = errors.New("replacement Grovlet cannot run placed component")

type serviceRecovery struct {
	nodeID     string
	health     *systemnats.Health
	placement  *systemnats.Placement
	components *componentManager
	transport  *systemnats.Transport
}

func newServiceRecovery(
	nodeID string,
	health *systemnats.Health,
	placement *systemnats.Placement,
	components *componentManager,
	transport *systemnats.Transport,
) *serviceRecovery {
	return &serviceRecovery{
		nodeID:     nodeID,
		health:     health,
		placement:  placement,
		components: components,
		transport:  transport,
	}
}

func (r *serviceRecovery) Run(ctx context.Context) error {
	ticker := time.NewTicker(recoveryInterval)
	defer ticker.Stop()
	for {
		_ = r.reconcile(ctx)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (r *serviceRecovery) reconcile(ctx context.Context) error {
	current, ok := selectRecovery(r.nodeID, r.health.Snapshot(), r.placement.Snapshot())
	if !ok {
		return nil
	}
	component, ok := findComponent(r.components.SnapshotComponents(), current.ServiceID)
	if !ok || component.InvocationSubject == "" {
		return errRecoveryComponentUnavailable
	}
	started := false
	switch component.State {
	case systemnats.ComponentStopped, systemnats.ComponentFailed:
		if err := r.components.StartComponent(ctx, current.ServiceID); err != nil {
			return err
		}
		started = true
		component, ok = findComponent(r.components.SnapshotComponents(), current.ServiceID)
		if !ok || component.State != systemnats.ComponentHealthy {
			return errRecoveryComponentUnavailable
		}
	case systemnats.ComponentHealthy:
	case systemnats.ComponentStarting, systemnats.ComponentStopping:
		return nil
	default:
		return errRecoveryComponentUnavailable
	}

	replacement := systemnats.PlacementRecord{
		ServiceID:         current.ServiceID,
		NodeID:            r.nodeID,
		InvocationSubject: component.InvocationSubject,
		ArtifactDigest:    current.ArtifactDigest,
	}
	observed, err := r.placement.Replace(ctx, r.transport, current, replacement)
	if err == nil {
		return nil
	}
	if started && observed.NodeID != r.nodeID {
		_ = r.components.StopComponent(ctx, current.ServiceID)
	}
	if errors.Is(err, systemnats.ErrPlacementChanged) {
		return nil
	}
	return err
}

func selectRecovery(
	nodeID string,
	cluster systemnats.ClusterView,
	placement systemnats.PlacementView,
) (systemnats.PlacementRecord, bool) {
	if !cluster.Ready || !placement.Ready {
		return systemnats.PlacementRecord{}, false
	}
	healthByNode := make(map[string]systemnats.ClusterNode, len(cluster.Nodes))
	coordinator := ""
	for _, node := range cluster.Nodes {
		healthByNode[node.NodeID] = node
		if node.Health == systemnats.HealthHealthy && (coordinator == "" || node.NodeID < coordinator) {
			coordinator = node.NodeID
		}
	}
	if coordinator == "" || coordinator != nodeID {
		return systemnats.PlacementRecord{}, false
	}
	for _, record := range placement.Placements {
		node, exists := healthByNode[record.NodeID]
		if exists && node.Health == systemnats.HealthUnavailable && node.LastSeen != "" {
			return record, true
		}
	}
	return systemnats.PlacementRecord{}, false
}

func findComponent(view systemnats.ComponentView, serviceID grove.ServiceID) (systemnats.ComponentStatus, bool) {
	for _, component := range view.Components {
		if component.ServiceID == serviceID {
			return component, true
		}
	}
	return systemnats.ComponentStatus{}, false
}
