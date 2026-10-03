package systemnats

import (
	"context"
	"errors"
	"time"

	"github.com/grove-project/grove/internal/bootstrap"
	"github.com/grove-project/grove/internal/controlplane"
	"github.com/nats-io/nats.go"
)

const (
	bootstrapReadinessSubjectRoot = "_GROVE.system.bootstrap.readiness."
	readinessRetryDelay           = 25 * time.Millisecond
	readinessAttemptTimeout       = 250 * time.Millisecond
)

var (
	// ErrCandidateReadinessMismatch is returned when a runtime reports health
	// for a different node or artifact than the candidate being handed ownership.
	ErrCandidateReadinessMismatch = errors.New("grove candidate readiness identity does not match")
)

// BootstrapReadinessSubject returns the ephemeral readiness endpoint for a
// candidate runtime node.
func BootstrapReadinessSubject(nodeID string) string {
	return bootstrapReadinessSubjectRoot + nodeID
}

// ServeBootstrapReadiness exposes exact-artifact runtime health over System
// NATS using the stable bootstrap wire envelope.
func (t *Transport) ServeBootstrapReadiness(ctx context.Context, readiness bootstrap.Readiness) error {
	encoded, err := bootstrap.MarshalReadiness(readiness)
	if err != nil {
		return &Error{Operation: "serve Grove bootstrap readiness", Err: err}
	}
	if _, err := t.connection.Subscribe(BootstrapReadinessSubject(readiness.NodeID), func(message *nats.Msg) {
		_ = message.Respond(encoded)
	}); err != nil {
		return &Error{Operation: "subscribe Grove bootstrap readiness", Err: err}
	}
	flushCtx, cancel := operationContext(ctx)
	defer cancel()
	if err := t.connection.FlushWithContext(flushCtx); err != nil {
		return &Error{Operation: "activate Grove bootstrap readiness", Err: err}
	}
	return nil
}

// RequestBootstrapReadiness requests one candidate runtime's stable readiness
// message.
func (t *Transport) RequestBootstrapReadiness(ctx context.Context, nodeID string) (bootstrap.Readiness, error) {
	if nodeID == "" {
		return bootstrap.Readiness{}, &Error{Operation: "request Grove bootstrap readiness", Err: ErrCandidateReadinessMismatch}
	}
	message, err := t.connection.RequestWithContext(ctx, BootstrapReadinessSubject(nodeID), nil)
	if err != nil {
		return bootstrap.Readiness{}, &Error{Operation: "request Grove bootstrap readiness", Err: err}
	}
	readiness, err := bootstrap.UnmarshalReadiness(message.Data)
	if err != nil {
		return bootstrap.Readiness{}, &Error{Operation: "decode Grove bootstrap readiness", Err: err}
	}
	return readiness, nil
}

// WaitBootstrapHealthy waits until nodeID reports healthy for the exact
// artifact digest. A response for a different identity fails immediately.
func (t *Transport) WaitBootstrapHealthy(ctx context.Context, nodeID, artifactDigest string) error {
	if nodeID == "" || !controlplane.ValidSHA256Digest(artifactDigest) {
		return &Error{Operation: "wait for Grove bootstrap readiness", Err: ErrCandidateReadinessMismatch}
	}
	ticker := time.NewTicker(readinessRetryDelay)
	defer ticker.Stop()
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, readinessAttemptTimeout)
		readiness, err := t.RequestBootstrapReadiness(attemptCtx, nodeID)
		cancel()
		if err == nil {
			if readiness.NodeID != nodeID || readiness.ArtifactDigest != artifactDigest || readiness.State != bootstrap.ReadinessHealthy {
				return &Error{Operation: "verify Grove bootstrap readiness", Err: ErrCandidateReadinessMismatch}
			}
			return nil
		}
		lastErr = err
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return &Error{Operation: "wait for Grove bootstrap readiness", Err: errors.Join(lastErr, ctx.Err())}
		}
	}
}

// CommitHealthyUpgrade gates on every candidate runtime, durably records
// candidate health and switching, changes placement in stable service order,
// and finally commits the candidate as the active artifact.
func (d *Deployments) CommitHealthyUpgrade(
	ctx context.Context,
	transport *Transport,
	pending Rollout,
	routes []UpgradeRoute,
) (Rollout, error) {
	pending, routes, err := controlplane.ValidateUpgrade(pending, routes)
	if err != nil {
		return Rollout{}, &Error{Operation: "validate Grove upgrade", Err: err}
	}
	for _, nodeID := range controlplane.UpgradeCandidateNodes(routes) {
		if err := transport.WaitBootstrapHealthy(ctx, nodeID, pending.CandidateArtifactDigest); err != nil {
			return Rollout{}, err
		}
	}

	healthy := controlplane.AdvanceUpgradeRollout(pending, RolloutCandidateHealthy, pending.CurrentArtifactDigest, pending.CandidateArtifactDigest)
	if err := d.PutRollout(ctx, transport, healthy); err != nil {
		return Rollout{}, err
	}
	switching := controlplane.AdvanceUpgradeRollout(healthy, RolloutSwitching, healthy.CurrentArtifactDigest, healthy.CandidateArtifactDigest)
	if err := d.PutRollout(ctx, transport, switching); err != nil {
		return Rollout{}, err
	}
	placement, err := NewPlacement(nil)
	if err != nil {
		return Rollout{}, err
	}
	for _, route := range routes {
		if _, err := placement.Replace(ctx, transport, route.Current, route.Candidate); err != nil {
			return Rollout{}, err
		}
	}
	active := controlplane.AdvanceUpgradeRollout(switching, RolloutActive, switching.CandidateArtifactDigest, "")
	if err := d.PutRollout(ctx, transport, active); err != nil {
		return Rollout{}, err
	}
	return active, nil
}

// RollbackFailedUpgrade durably rejects a candidate, records rollback intent,
// reconciles every route to the retained current artifact, and commits the
// terminal rolled-back generation with its structured cause.
func (d *Deployments) RollbackFailedUpgrade(
	ctx context.Context,
	transport *Transport,
	rollout Rollout,
	routes []UpgradeRoute,
	failure RolloutFailure,
) (Rollout, error) {
	rollout, routes, err := controlplane.ValidateRollback(rollout, routes, failure)
	if err != nil {
		return Rollout{}, &Error{Operation: "validate Grove rollback", Err: err}
	}
	failed := controlplane.AdvanceFailedRollout(rollout, RolloutCandidateFailed, failure)
	if err := d.PutRollout(ctx, transport, failed); err != nil {
		return Rollout{}, err
	}
	rollingBack := controlplane.AdvanceFailedRollout(failed, RolloutRollingBack, failure)
	if err := d.PutRollout(ctx, transport, rollingBack); err != nil {
		return Rollout{}, err
	}
	placement, err := NewPlacement(nil)
	if err != nil {
		return Rollout{}, err
	}
	for _, route := range routes {
		if _, err := placement.Replace(ctx, transport, route.Candidate, route.Current); err != nil {
			return Rollout{}, err
		}
	}
	rolledBack := controlplane.AdvanceFailedRollout(rollingBack, RolloutRolledBack, failure)
	if err := d.PutRollout(ctx, transport, rolledBack); err != nil {
		return Rollout{}, err
	}
	return rolledBack, nil
}
