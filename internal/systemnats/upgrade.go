package systemnats

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/bootstrap"
	"github.com/nats-io/nats.go"
)

const (
	bootstrapReadinessSubjectRoot = "_GROVE.system.bootstrap.readiness."
	readinessRetryDelay           = 25 * time.Millisecond
	readinessAttemptTimeout       = 250 * time.Millisecond
)

var (
	// ErrUpgradeInvalid is returned when a health-gated upgrade does not match
	// its pending rollout or current/candidate placement identities.
	ErrUpgradeInvalid = errors.New("grove upgrade is invalid")
	// ErrCandidateReadinessMismatch is returned when a runtime reports health
	// for a different node or artifact than the candidate being handed ownership.
	ErrCandidateReadinessMismatch = errors.New("grove candidate readiness identity does not match")
)

// UpgradeRoute describes one compare-and-set service ownership handoff.
type UpgradeRoute struct {
	Current   PlacementRecord
	Candidate PlacementRecord
}

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
	if nodeID == "" || !validSHA256Digest(artifactDigest) {
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
	pending, routes, err := validateUpgrade(pending, routes)
	if err != nil {
		return Rollout{}, &Error{Operation: "validate Grove upgrade", Err: err}
	}
	readyNodes := make([]string, 0, len(routes))
	for _, route := range routes {
		if slices.Contains(readyNodes, route.Candidate.NodeID) {
			continue
		}
		if err := transport.WaitBootstrapHealthy(ctx, route.Candidate.NodeID, pending.CandidateArtifactDigest); err != nil {
			return Rollout{}, err
		}
		readyNodes = append(readyNodes, route.Candidate.NodeID)
	}

	healthy := advanceUpgradeRollout(pending, RolloutCandidateHealthy, pending.CurrentArtifactDigest, pending.CandidateArtifactDigest)
	if err := d.PutRollout(ctx, transport, healthy); err != nil {
		return Rollout{}, err
	}
	switching := advanceUpgradeRollout(healthy, RolloutSwitching, healthy.CurrentArtifactDigest, healthy.CandidateArtifactDigest)
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
	active := advanceUpgradeRollout(switching, RolloutActive, switching.CandidateArtifactDigest, "")
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
	rollout, err := validateRollout(rollout)
	if err != nil || !slices.Contains([]RolloutPhase{RolloutPending, RolloutCandidateHealthy, RolloutSwitching}, rollout.Phase) || !validRolloutFailure(&failure) {
		return Rollout{}, &Error{Operation: "validate Grove rollback", Err: ErrUpgradeInvalid}
	}
	routes, err = validateUpgradeRoutes(rollout, routes)
	if err != nil {
		return Rollout{}, &Error{Operation: "validate Grove rollback", Err: err}
	}
	failed := advanceFailedRollout(rollout, RolloutCandidateFailed, failure)
	if err := d.PutRollout(ctx, transport, failed); err != nil {
		return Rollout{}, err
	}
	rollingBack := advanceFailedRollout(failed, RolloutRollingBack, failure)
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
	rolledBack := advanceFailedRollout(rollingBack, RolloutRolledBack, failure)
	if err := d.PutRollout(ctx, transport, rolledBack); err != nil {
		return Rollout{}, err
	}
	return rolledBack, nil
}

func validateUpgrade(pending Rollout, routes []UpgradeRoute) (Rollout, []UpgradeRoute, error) {
	pending, err := validateRollout(pending)
	if err != nil || pending.Phase != RolloutPending || len(routes) == 0 {
		return Rollout{}, nil, ErrUpgradeInvalid
	}
	routes, err = validateUpgradeRoutes(pending, routes)
	if err != nil {
		return Rollout{}, nil, err
	}
	return pending, routes, nil
}

func validateUpgradeRoutes(rollout Rollout, routes []UpgradeRoute) ([]UpgradeRoute, error) {
	if len(routes) == 0 {
		return nil, ErrUpgradeInvalid
	}
	routes = append([]UpgradeRoute(nil), routes...)
	sort.Slice(routes, func(i, j int) bool { return routes[i].Current.ServiceID < routes[j].Current.ServiceID })
	seen := make(map[grove.ServiceID]struct{}, len(routes))
	for _, route := range routes {
		if !validPlacementRecord(route.Current) || !validPlacementRecord(route.Candidate) ||
			route.Current.ServiceID != route.Candidate.ServiceID ||
			route.Current.ArtifactDigest != rollout.CurrentArtifactDigest ||
			route.Candidate.ArtifactDigest != rollout.CandidateArtifactDigest {
			return nil, ErrUpgradeInvalid
		}
		if _, exists := seen[route.Current.ServiceID]; exists {
			return nil, ErrUpgradeInvalid
		}
		seen[route.Current.ServiceID] = struct{}{}
	}
	return routes, nil
}

func advanceUpgradeRollout(previous Rollout, phase RolloutPhase, currentArtifactDigest, candidateArtifactDigest string) Rollout {
	nodes := make([]RolloutNodeProgress, len(previous.Nodes))
	for i, node := range previous.Nodes {
		nodes[i] = RolloutNodeProgress{
			NodeID:                  node.NodeID,
			CurrentArtifactDigest:   currentArtifactDigest,
			CandidateArtifactDigest: candidateArtifactDigest,
			Phase:                   phase,
		}
	}
	return Rollout{
		ApplicationID:           previous.ApplicationID,
		ClusterID:               previous.ClusterID,
		RolloutID:               fmt.Sprintf("%s.%d.%s", previous.RolloutID, previous.Generation+1, phase),
		Generation:              previous.Generation + 1,
		CurrentArtifactDigest:   currentArtifactDigest,
		CandidateArtifactDigest: candidateArtifactDigest,
		Phase:                   phase,
		Nodes:                   nodes,
	}
}

func advanceFailedRollout(previous Rollout, phase RolloutPhase, failure RolloutFailure) Rollout {
	nodes := make([]RolloutNodeProgress, len(previous.Nodes))
	for i, node := range previous.Nodes {
		nodes[i] = RolloutNodeProgress{
			NodeID:                  node.NodeID,
			CurrentArtifactDigest:   previous.CurrentArtifactDigest,
			CandidateArtifactDigest: previous.CandidateArtifactDigest,
			Phase:                   phase,
			Failure:                 node.Failure,
		}
	}
	return Rollout{
		ApplicationID:           previous.ApplicationID,
		ClusterID:               previous.ClusterID,
		RolloutID:               fmt.Sprintf("%s.%d.%s", previous.RolloutID, previous.Generation+1, phase),
		Generation:              previous.Generation + 1,
		CurrentArtifactDigest:   previous.CurrentArtifactDigest,
		CandidateArtifactDigest: previous.CandidateArtifactDigest,
		Phase:                   phase,
		Nodes:                   nodes,
		Failure:                 cloneRolloutFailure(&failure),
	}
}
