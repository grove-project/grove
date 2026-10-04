package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/grove-project/grove/internal/systemnats"
)

func (g *grovlet) gracefulLeave(ctx context.Context, nodeID string) error {
	if g.membership == nil || g.transport == nil {
		return nil
	}
	membership := g.membership.Snapshot()
	if !membership.Ready {
		return nil
	}
	if len(membership.Members) >= systemnats.MinClusterNodes && len(membership.Members)-1 < systemnats.MinClusterNodes {
		return fmt.Errorf("%w: leaving would drop the cluster below %d nodes; add a replacement node first",
			errClusterTooSmallToLeave, systemnats.MinClusterNodes)
	}
	if err := g.membership.BeginLeave(ctx, g.transport); err != nil {
		return err
	}
	g.reconcileLoop.stop()
	g.recoveryLoop.stop()
	if g.components != nil {
		if err := g.components.stopAll(ctx); err != nil {
			return fmt.Errorf("stop components before node retirement: %w", err)
		}
	}
	g.healthLoop.stop()
	peerIDs := make([]string, 0, len(membership.Members)-1)
	for _, member := range membership.Members {
		if member.NodeID != nodeID {
			peerIDs = append(peerIDs, member.NodeID)
		}
	}
	if g.placement != nil && len(peerIDs) != 0 {
		shouldRetire, err := g.waitForPlacementRetirement(ctx, nodeID, peerIDs, membership.Members)
		if err != nil {
			return err
		}
		if !shouldRetire {
			return nil
		}
	}
	if g.server != nil {
		if g.server.HasPeer() && len(peerIDs) != 0 {
			var transferErr error
			for _, peerID := range peerIDs {
				if err := g.transport.RequestPeer(ctx, peerID); err == nil {
					transferErr = nil
					break
				} else {
					transferErr = errors.Join(transferErr, err)
				}
			}
			if transferErr != nil {
				return fmt.Errorf("transfer System NATS metadata witness: %w", transferErr)
			}
		}
		if err := g.server.PrepareRetire(ctx); err != nil {
			return err
		}
	}
	if err := g.membership.Leave(ctx, g.transport); err != nil {
		return err
	}
	if len(peerIDs) != 0 {
		if err := g.waitForMembershipRetirement(ctx, nodeID, peerIDs); err != nil {
			return err
		}
	}
	if g.server != nil {
		if err := g.server.Retire(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (g *grovlet) waitForMembershipRetirement(
	ctx context.Context,
	nodeID string,
	peerIDs []string,
) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var last systemnats.MembershipView
	var lastErr error
	for {
		for _, peerID := range peerIDs {
			requestCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
			view, err := g.transport.RequestMembership(requestCtx, peerID)
			cancel()
			if err != nil {
				lastErr = err
				continue
			}
			last = view
			retired := view.Ready
			for _, member := range view.Members {
				if member.NodeID == nodeID {
					retired = false
					break
				}
			}
			if retired {
				return nil
			}
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("wait for membership retirement of %s: membership=%#v: %w", nodeID, last, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func (g *grovlet) waitForPlacementRetirement(
	ctx context.Context,
	nodeID string,
	peerIDs []string,
	members []systemnats.MembershipRecord,
) (bool, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var last systemnats.PlacementView
	var lastErr error
	for {
		allLeaving, err := g.membership.AllLeaving(ctx, g.transport, members)
		if err == nil && allLeaving {
			return false, nil
		}
		if err != nil {
			lastErr = err
		}
		for _, peerID := range peerIDs {
			requestCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
			view, err := g.transport.RequestPlacement(requestCtx, peerID)
			cancel()
			if err != nil {
				lastErr = err
				continue
			}
			last = view
			if view.Ready && !placementReferencesNode(view, nodeID) {
				return true, nil
			}
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return false, fmt.Errorf("wait for service relocation from %s: placement=%#v: %w", nodeID, last, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func placementReferencesNode(view systemnats.PlacementView, nodeID string) bool {
	for _, placement := range view.Placements {
		if placement.NodeID == nodeID {
			return true
		}
	}
	return false
}
