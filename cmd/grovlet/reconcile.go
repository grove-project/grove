package main

import (
	"context"
	"sort"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
)

const desiredReconcileInterval = 50 * time.Millisecond

type desiredReconciler struct {
	nodeID     string
	desired    *systemnats.Desired
	components *componentManager
}

func (r *desiredReconciler) Run(ctx context.Context) error {
	ticker := time.NewTicker(desiredReconcileInterval)
	defer ticker.Stop()
	for {
		for _, serviceID := range desiredStarts(r.nodeID, r.desired.Snapshot(), r.components.SnapshotComponents()) {
			_ = r.components.StartComponent(ctx, serviceID)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func desiredStarts(nodeID string, desired systemnats.DesiredView, observed systemnats.ComponentView) []grove.ServiceID {
	if !desired.Ready || len(desired.Deployments) == 0 {
		return nil
	}
	states := make(map[grove.ServiceID]systemnats.ComponentState, len(observed.Components))
	for _, component := range observed.Components {
		states[component.ServiceID] = component.State
	}
	starts := make(map[grove.ServiceID]struct{})
	for _, deployment := range desired.Deployments {
		for _, component := range deployment.Components {
			state, available := states[component.ServiceID]
			if component.NodeID == nodeID && available && (state == systemnats.ComponentStopped || state == systemnats.ComponentFailed) {
				starts[component.ServiceID] = struct{}{}
			}
		}
	}
	serviceIDs := make([]grove.ServiceID, 0, len(starts))
	for serviceID := range starts {
		serviceIDs = append(serviceIDs, serviceID)
	}
	sort.Slice(serviceIDs, func(i, j int) bool { return serviceIDs[i] < serviceIDs[j] })
	return serviceIDs
}
