package systemnats

import "context"

// RolloutStore persists rollout records over System NATS for the rollout
// owner (internal/rollout implements the orchestration; this is its store).
// Artifact, desired-deployment and rollout writes go through the
// deployment and desired command endpoints that nodeID serves, which apply
// the control-plane rules and retry transient control-state errors;
// placement handoffs compare-and-set the authoritative placement bucket.
type RolloutStore struct {
	transport *Transport
	nodeID    string
	placement *Placement
}

// NewRolloutStore returns a RolloutStore writing through nodeID.
func NewRolloutStore(transport *Transport, nodeID string) (*RolloutStore, error) {
	placement, err := NewPlacement(nil)
	if err != nil {
		return nil, err
	}
	return &RolloutStore{transport: transport, nodeID: nodeID, placement: placement}, nil
}

// PutArtifact records immutable artifact metadata.
func (s *RolloutStore) PutArtifact(ctx context.Context, artifact DeploymentArtifact) error {
	return s.transport.PutDeploymentArtifact(ctx, s.nodeID, artifact)
}

// PutDesired records the desired deployment.
func (s *RolloutStore) PutDesired(ctx context.Context, desired DesiredDeployment) error {
	return s.transport.PutDesired(ctx, s.nodeID, desired)
}

// PutRollout commits a rollout generation.
func (s *RolloutStore) PutRollout(ctx context.Context, rollout Rollout) error {
	return s.transport.PutRollout(ctx, s.nodeID, rollout)
}

// ReplacePlacement hands one service from current to replacement.
func (s *RolloutStore) ReplacePlacement(ctx context.Context, current, replacement PlacementRecord) error {
	_, err := s.placement.Replace(ctx, s.transport, current, replacement)
	return err
}

// WaitCandidateHealthy waits for nodeID's candidate runtime to report
// healthy for exactly artifactDigest.
func (s *RolloutStore) WaitCandidateHealthy(ctx context.Context, nodeID, artifactDigest string) error {
	return s.transport.WaitBootstrapHealthy(ctx, nodeID, artifactDigest)
}
