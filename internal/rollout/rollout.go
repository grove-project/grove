// Package rollout is Grove's one owner of deployment intent: recording
// immutable artifacts and the desired deployment, activating an artifact,
// proposing a candidate, and the health-gated commit or rollback that moves
// service ownership between them.
//
// It sequences control-plane records whose rules live in
// internal/controlplane and writes them through a Store port, so the
// orchestration is tested without NATS. The System NATS adapter implements
// Store (systemnats.RolloutStore). The operator console, its scripted demos
// and the CLI end-to-end tests all roll out through an Operator; nothing
// else in production writes these records.
package rollout

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/grove-project/grove/internal/controlplane"
)

// desiredRetryInterval paces retries of a desired-deployment write while the
// control plane is not yet able to accept it.
const (
	desiredRetryInterval  = 25 * time.Millisecond
	desiredAttemptTimeout = 500 * time.Millisecond
)

// Store persists rollout records and service ownership. Each write applies
// the control-plane rules for its record.
type Store interface {
	PutArtifact(ctx context.Context, artifact controlplane.DeploymentArtifact) error
	PutDesired(ctx context.Context, desired controlplane.DesiredDeployment) error
	PutRollout(ctx context.Context, rollout controlplane.Rollout) error
	// ReplacePlacement hands one service from current to replacement with
	// compare-and-set semantics; repeating a completed handoff is a no-op.
	ReplacePlacement(ctx context.Context, current, replacement controlplane.PlacementRecord) error
	// WaitCandidateHealthy waits until nodeID's candidate runtime reports
	// healthy for exactly artifactDigest.
	WaitCandidateHealthy(ctx context.Context, nodeID, artifactDigest string) error
}

// Generation names one rollout generation and the nodes it tracks.
type Generation struct {
	RolloutID  string
	Generation uint64
	NodeIDs    []string
}

// Operator performs rollouts against a Store.
type Operator struct {
	store Store
}

// New returns an Operator writing through store.
func New(store Store) *Operator {
	return &Operator{store: store}
}

// RecordArtifact records immutable artifact metadata. Recording the same
// artifact again is a no-op.
func (o *Operator) RecordArtifact(ctx context.Context, artifact controlplane.DeploymentArtifact) error {
	if err := o.store.PutArtifact(ctx, artifact); err != nil {
		return fmt.Errorf("record artifact %s: %w", artifact.ArtifactDigest, err)
	}
	return nil
}

// RecordDesired records the desired deployment, retrying each bounded
// attempt until it is accepted or ctx ends.
func (o *Operator) RecordDesired(ctx context.Context, desired controlplane.DesiredDeployment) error {
	ticker := time.NewTicker(desiredRetryInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, desiredAttemptTimeout)
		err := o.store.PutDesired(attemptCtx, desired)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("record desired deployment: %w", errors.Join(lastErr, ctx.Err()))
		}
	}
}

// Activate records artifact and makes it the active artifact as generation
// g, with every node of g on it.
func (o *Operator) Activate(ctx context.Context, artifact controlplane.DeploymentArtifact, g Generation) (controlplane.Rollout, error) {
	if err := o.RecordArtifact(ctx, artifact); err != nil {
		return controlplane.Rollout{}, err
	}
	active := Record(artifact.ApplicationID, artifact.ClusterID, g, artifact.ArtifactDigest, "", controlplane.RolloutActive)
	if err := o.store.PutRollout(ctx, active); err != nil {
		return controlplane.Rollout{}, fmt.Errorf("activate %s: %w", artifact.ArtifactDigest, err)
	}
	return active, nil
}

// Propose records candidate and a pending rollout generation g from the
// active artifact currentDigest to it.
func (o *Operator) Propose(ctx context.Context, currentDigest string, candidate controlplane.DeploymentArtifact, g Generation) (controlplane.Rollout, error) {
	if err := o.RecordArtifact(ctx, candidate); err != nil {
		return controlplane.Rollout{}, err
	}
	pending := Record(candidate.ApplicationID, candidate.ClusterID, g, currentDigest, candidate.ArtifactDigest, controlplane.RolloutPending)
	if err := o.store.PutRollout(ctx, pending); err != nil {
		return controlplane.Rollout{}, fmt.Errorf("propose %s: %w", candidate.ArtifactDigest, err)
	}
	return pending, nil
}

// Commit gates on every candidate runtime, durably records candidate health
// and switching, hands each service to its candidate in stable service
// order, and finally commits the candidate as the active artifact. If a step
// fails, the stored rollout records how far it got; Rollback accepts a
// candidate-healthy or switching rollout and restores current ownership.
func (o *Operator) Commit(ctx context.Context, pending controlplane.Rollout, routes []controlplane.UpgradeRoute) (controlplane.Rollout, error) {
	pending, routes, err := controlplane.ValidateUpgrade(pending, routes)
	if err != nil {
		return controlplane.Rollout{}, fmt.Errorf("validate Grove upgrade: %w", err)
	}
	for _, nodeID := range controlplane.UpgradeCandidateNodes(routes) {
		if err := o.store.WaitCandidateHealthy(ctx, nodeID, pending.CandidateArtifactDigest); err != nil {
			return controlplane.Rollout{}, err
		}
	}
	healthy := controlplane.AdvanceUpgradeRollout(pending, controlplane.RolloutCandidateHealthy, pending.CurrentArtifactDigest, pending.CandidateArtifactDigest)
	if err := o.store.PutRollout(ctx, healthy); err != nil {
		return controlplane.Rollout{}, err
	}
	switching := controlplane.AdvanceUpgradeRollout(healthy, controlplane.RolloutSwitching, healthy.CurrentArtifactDigest, healthy.CandidateArtifactDigest)
	if err := o.store.PutRollout(ctx, switching); err != nil {
		return controlplane.Rollout{}, err
	}
	for _, route := range routes {
		if err := o.store.ReplacePlacement(ctx, route.Current, route.Candidate); err != nil {
			return controlplane.Rollout{}, err
		}
	}
	active := controlplane.AdvanceUpgradeRollout(switching, controlplane.RolloutActive, switching.CandidateArtifactDigest, "")
	if err := o.store.PutRollout(ctx, active); err != nil {
		return controlplane.Rollout{}, err
	}
	return active, nil
}

// Rollback durably rejects the candidate, records rollback intent, hands
// every service back to the retained current artifact, and commits the
// terminal rolled-back generation with its structured cause. Handoffs already
// back on the current artifact are no-ops.
func (o *Operator) Rollback(ctx context.Context, rollout controlplane.Rollout, routes []controlplane.UpgradeRoute, failure controlplane.RolloutFailure) (controlplane.Rollout, error) {
	rollout, routes, err := controlplane.ValidateRollback(rollout, routes, failure)
	if err != nil {
		return controlplane.Rollout{}, fmt.Errorf("validate Grove rollback: %w", err)
	}
	failed := controlplane.AdvanceFailedRollout(rollout, controlplane.RolloutCandidateFailed, failure)
	if err := o.store.PutRollout(ctx, failed); err != nil {
		return controlplane.Rollout{}, err
	}
	rollingBack := controlplane.AdvanceFailedRollout(failed, controlplane.RolloutRollingBack, failure)
	if err := o.store.PutRollout(ctx, rollingBack); err != nil {
		return controlplane.Rollout{}, err
	}
	for _, route := range routes {
		if err := o.store.ReplacePlacement(ctx, route.Candidate, route.Current); err != nil {
			return controlplane.Rollout{}, err
		}
	}
	rolledBack := controlplane.AdvanceFailedRollout(rollingBack, controlplane.RolloutRolledBack, failure)
	if err := o.store.PutRollout(ctx, rolledBack); err != nil {
		return controlplane.Rollout{}, err
	}
	return rolledBack, nil
}

// Record builds generation g of an application's rollout in phase, with
// every node of g at the same artifacts and phase.
func Record(applicationID, clusterID string, g Generation, currentDigest, candidateDigest string, phase controlplane.RolloutPhase) controlplane.Rollout {
	nodes := make([]controlplane.RolloutNodeProgress, len(g.NodeIDs))
	for i, nodeID := range g.NodeIDs {
		nodes[i] = controlplane.RolloutNodeProgress{
			NodeID: nodeID, CurrentArtifactDigest: currentDigest,
			CandidateArtifactDigest: candidateDigest, Phase: phase,
		}
	}
	return controlplane.Rollout{
		ApplicationID: applicationID, ClusterID: clusterID,
		RolloutID: g.RolloutID, Generation: g.Generation,
		CurrentArtifactDigest: currentDigest, CandidateArtifactDigest: candidateDigest,
		Phase: phase, Nodes: nodes,
	}
}
