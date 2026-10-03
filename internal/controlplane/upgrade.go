package controlplane

import (
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/grove-project/grove"
)

// ErrUpgradeInvalid is returned when a health-gated upgrade does not match its
// pending rollout or current/candidate placement identities.
var ErrUpgradeInvalid = errors.New("grove upgrade is invalid")

// UpgradeRoute describes one compare-and-set service ownership handoff.
type UpgradeRoute struct {
	Current   PlacementRecord
	Candidate PlacementRecord
}

// ValidateUpgrade checks that pending is a valid pending rollout and that its
// routes move services from its current to its candidate artifact. It returns
// both in canonical order.
func ValidateUpgrade(pending Rollout, routes []UpgradeRoute) (Rollout, []UpgradeRoute, error) {
	pending, err := ValidateRollout(pending)
	if err != nil || pending.Phase != RolloutPending || len(routes) == 0 {
		return Rollout{}, nil, ErrUpgradeInvalid
	}
	routes, err = ValidateUpgradeRoutes(pending, routes)
	if err != nil {
		return Rollout{}, nil, err
	}
	return pending, routes, nil
}

// ValidateRollback checks that rollout is still in flight (pending,
// candidate-healthy or switching), that failure explains the rejection, and
// that routes match the rollout's artifacts. It returns rollout and routes in
// canonical order.
func ValidateRollback(rollout Rollout, routes []UpgradeRoute, failure RolloutFailure) (Rollout, []UpgradeRoute, error) {
	rollout, err := ValidateRollout(rollout)
	if err != nil || !slices.Contains([]RolloutPhase{RolloutPending, RolloutCandidateHealthy, RolloutSwitching}, rollout.Phase) || !ValidRolloutFailure(&failure) {
		return Rollout{}, nil, ErrUpgradeInvalid
	}
	routes, err = ValidateUpgradeRoutes(rollout, routes)
	if err != nil {
		return Rollout{}, nil, err
	}
	return rollout, routes, nil
}

// ValidateUpgradeRoutes checks that each route hands one service from the
// rollout's current artifact to its candidate, with at most one route per
// service, and returns the routes sorted by service ID.
func ValidateUpgradeRoutes(rollout Rollout, routes []UpgradeRoute) ([]UpgradeRoute, error) {
	if len(routes) == 0 {
		return nil, ErrUpgradeInvalid
	}
	routes = append([]UpgradeRoute(nil), routes...)
	sort.Slice(routes, func(i, j int) bool { return routes[i].Current.ServiceID < routes[j].Current.ServiceID })
	seen := make(map[grove.ServiceID]struct{}, len(routes))
	for _, route := range routes {
		if !ValidPlacementRecord(route.Current) || !ValidPlacementRecord(route.Candidate) ||
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

// UpgradeCandidateNodes lists, in route order and without repeats, the nodes
// whose candidate runtime must report healthy before ownership moves.
func UpgradeCandidateNodes(routes []UpgradeRoute) []string {
	nodes := make([]string, 0, len(routes))
	for _, route := range routes {
		if !slices.Contains(nodes, route.Candidate.NodeID) {
			nodes = append(nodes, route.Candidate.NodeID)
		}
	}
	return nodes
}

// AdvanceUpgradeRollout returns the next generation of previous in phase with
// the given artifacts, clearing node failures. Its rollout ID derives from the
// previous one, so retries of the same step produce the same record.
func AdvanceUpgradeRollout(previous Rollout, phase RolloutPhase, currentArtifactDigest, candidateArtifactDigest string) Rollout {
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
		RolloutID:               nextRolloutID(previous, phase),
		Generation:              previous.Generation + 1,
		CurrentArtifactDigest:   currentArtifactDigest,
		CandidateArtifactDigest: candidateArtifactDigest,
		Phase:                   phase,
		Nodes:                   nodes,
	}
}

// AdvanceFailedRollout returns the next generation of previous in a failed
// phase, keeping its artifacts and node failures and recording failure.
func AdvanceFailedRollout(previous Rollout, phase RolloutPhase, failure RolloutFailure) Rollout {
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
		RolloutID:               nextRolloutID(previous, phase),
		Generation:              previous.Generation + 1,
		CurrentArtifactDigest:   previous.CurrentArtifactDigest,
		CandidateArtifactDigest: previous.CandidateArtifactDigest,
		Phase:                   phase,
		Nodes:                   nodes,
		Failure:                 CloneRolloutFailure(&failure),
	}
}

func nextRolloutID(previous Rollout, phase RolloutPhase) string {
	return fmt.Sprintf("%s.%d.%s", previous.RolloutID, previous.Generation+1, phase)
}
