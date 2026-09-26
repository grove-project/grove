package runtime

import (
	"context"
	"sync"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
)

const handlerRegistrationSyncInterval = 50 * time.Millisecond

// applicationDeclaresHandlers reports whether any component opts into
// handler-level placement.
func applicationDeclaresHandlers() bool {
	for _, component := range activeApplication.Components {
		if len(component.Handlers) != 0 {
			return true
		}
	}
	return false
}

// applicationManagesHandler reports whether a component declares the method
// for handler-level placement.
func applicationManagesHandler(service grove.ServiceID, method grove.MethodID) bool {
	component, ok := activeApplication.componentByID(service)
	if !ok {
		return false
	}
	for _, handler := range component.Handlers {
		if handler.Method == method {
			return true
		}
	}
	return false
}

// liveNodesFromHealth adapts Health to the handler placement live-node view.
func liveNodesFromHealth(health *systemnats.Health) func() ([]string, bool) {
	return func() ([]string, bool) {
		view := health.Snapshot()
		if !view.Ready {
			return nil, false
		}
		live := make([]string, 0, len(view.Nodes))
		for _, node := range view.Nodes {
			if node.Health == systemnats.HealthHealthy {
				live = append(live, node.NodeID)
			}
		}
		return live, true
	}
}

// hostedHandlerRegistrations lists the declared handlers of components whose
// workers are healthy here, each pointing at that component's endpoint.
func hostedHandlerRegistrations(components *componentManager) []systemnats.HandlerRegistration {
	var registrations []systemnats.HandlerRegistration
	for _, status := range components.SnapshotComponents().Components {
		if status.State != systemnats.ComponentHealthy && status.State != systemnats.ComponentDebugging {
			continue
		}
		component, ok := activeApplication.componentByID(status.ServiceID)
		if !ok {
			continue
		}
		for _, handler := range component.Handlers {
			registrations = append(registrations, systemnats.HandlerRegistration{
				Service:           component.ServiceID,
				Method:            handler.Method,
				Exclusive:         handler.Exclusive,
				Capability:        handler.Capability,
				InvocationSubject: status.InvocationSubject,
			})
		}
	}
	return registrations
}

// startHandlerPlacement runs handler-level placement for this Grovlet and
// serves the resolved view and exclusive-lease proxy its workers use.
func (r *systemNATSRuntime) startHandlerPlacement(ctx context.Context, cfg config, health *systemnats.Health) error {
	placements, err := systemnats.NewHandlerPlacements(systemnats.HandlerPlacementConfig{
		NodeID:            cfg.nodeID,
		InvocationSubject: cfg.systemNATSSubject,
		Live:              liveNodesFromHealth(health),
		Membership:        r.membership,
	})
	if err != nil {
		return err
	}
	if err := r.transport.ServeHandlerPlacement(ctx, cfg.nodeID, placements); err != nil {
		return err
	}
	if err := r.transport.ServeExclusiveLeases(ctx, cfg.nodeID, placements); err != nil {
		return err
	}
	handlerCtx, cancel := context.WithCancel(ctx)
	r.handlersCancel = cancel
	r.handlersDone = make(chan struct{})
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		_ = placements.Run(handlerCtx, r.transport)
	}()
	go func() {
		defer group.Done()
		ticker := time.NewTicker(handlerRegistrationSyncInterval)
		defer ticker.Stop()
		for {
			_ = placements.SetHandlers(hostedHandlerRegistrations(r.components))
			select {
			case <-ticker.C:
			case <-handlerCtx.Done():
				return
			}
		}
	}()
	go func() {
		group.Wait()
		close(r.handlersDone)
	}()
	r.handlers = placements
	return nil
}

func (r *systemNATSRuntime) stopHandlerPlacement() {
	if r.handlersCancel != nil {
		r.handlersCancel()
		<-r.handlersDone
	}
}
