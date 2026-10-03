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
