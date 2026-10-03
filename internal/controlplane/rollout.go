package controlplane

import (
	"errors"
	"net"
	"slices"
	"sort"
)

var (
	// ErrDeploymentArtifactInvalid is returned for incomplete or malformed
	// immutable artifact metadata.
	ErrDeploymentArtifactInvalid = errors.New("grove deployment artifact is invalid")
	// ErrDeploymentArtifactChanged is returned when an existing artifact digest
	// is associated with different metadata.
	ErrDeploymentArtifactChanged = errors.New("grove deployment artifact metadata changed")
	// ErrRolloutInvalid is returned for incomplete or inconsistent rollout
	// state.
	ErrRolloutInvalid = errors.New("grove rollout is invalid")
	// ErrIngressInvalid is returned for an ingress address that is not host:port.
	ErrIngressInvalid = errors.New("grove ingress address is invalid")
	// ErrIngressChanged is returned when the cluster already has a different
	// ingress address; the first claim wins for the life of the cluster.
	ErrIngressChanged = errors.New("grove ingress address changed")
	// ErrRolloutGeneration is returned when a write does not create the next
	// rollout generation.
	ErrRolloutGeneration = errors.New("grove rollout generation is not next")
	// ErrRolloutChanged is returned when another writer changes a rollout before
	// the requested generation is committed.
	ErrRolloutChanged = errors.New("grove rollout changed")
)

// RolloutPhase is the durable operator-facing phase of a rollout or node.
type RolloutPhase string

const (
	// RolloutActive means the current artifact is authoritative with no pending
	// successor.
	RolloutActive RolloutPhase = "active"
	// RolloutPending means a candidate is recorded but has not been launched or
	// handed ownership.
	RolloutPending RolloutPhase = "pending"
	// RolloutCandidateHealthy means every candidate runtime reported readiness
	// for the exact candidate artifact while the current artifact remains active.
	RolloutCandidateHealthy RolloutPhase = "candidate-healthy"
	// RolloutSwitching means candidate health passed and authoritative service
	// placement is being moved to the candidate artifact.
	RolloutSwitching RolloutPhase = "switching"
	// RolloutCandidateFailed means the candidate failed validation or health and
	// cannot take ownership.
	RolloutCandidateFailed RolloutPhase = "candidate-failed"
	// RolloutRollingBack means placement is being reconciled to the retained
	// current artifact.
	RolloutRollingBack RolloutPhase = "rolling-back"
	// RolloutRolledBack means the rejected candidate remains recorded while the
	// previous known-good artifact is authoritative.
	RolloutRolledBack RolloutPhase = "rolled-back"
)

// DeploymentArtifact is immutable identity metadata for one configured Grove
// application artifact. Configuration bytes remain embedded in the artifact.
type DeploymentArtifact struct {
	ApplicationID  string `json:"application_id"`
	CodeVersion    string `json:"code_version"`
	CodeDigest     string `json:"code_digest"`
	ConfigRevision string `json:"config_revision"`
	ConfigDigest   string `json:"config_digest"`
	ArtifactDigest string `json:"artifact_digest"`
	ClusterID      string `json:"cluster_id"`
	NodeClass      string `json:"node_class,omitempty"`
	NodeZone       string `json:"node_zone,omitempty"`
}

// RolloutNodeProgress records one node's immutable current/candidate identity
// and progress. Later handoff tasks advance the phase and failure fields.
type RolloutNodeProgress struct {
	NodeID                  string       `json:"node_id"`
	CurrentArtifactDigest   string       `json:"current_artifact_digest"`
	CandidateArtifactDigest string       `json:"candidate_artifact_digest,omitempty"`
	Phase                   RolloutPhase `json:"phase"`
	Failure                 string       `json:"failure,omitempty"`
}

// RolloutFailure is a stable machine-readable candidate rejection reason for
// CLI and status-UI consumers.
type RolloutFailure struct {
	Code      string `json:"code"`
	Component string `json:"component,omitempty"`
	Field     string `json:"field,omitempty"`
	Message   string `json:"message"`
}

// Rollout records one durable generation for an application and cluster.
type Rollout struct {
	ApplicationID           string                `json:"application_id"`
	ClusterID               string                `json:"cluster_id"`
	RolloutID               string                `json:"rollout_id"`
	Generation              uint64                `json:"generation"`
	CurrentArtifactDigest   string                `json:"current_artifact_digest"`
	CandidateArtifactDigest string                `json:"candidate_artifact_digest,omitempty"`
	Phase                   RolloutPhase          `json:"phase"`
	Nodes                   []RolloutNodeProgress `json:"nodes"`
	Failure                 *RolloutFailure       `json:"failure,omitempty"`
}

// DeploymentView is one Grovlet's watcher-derived view of artifact and rollout
// control state.
type DeploymentView struct {
	Ready     bool                 `json:"ready"`
	Artifacts []DeploymentArtifact `json:"artifacts"`
	Rollouts  []Rollout            `json:"rollouts"`
	// Ingress is the cluster-wide address of HTTP ingress components, chosen
	// when the first node starts; empty until then.
	Ingress string `json:"ingress,omitempty"`
	Error   string `json:"error,omitempty"`
}

// ValidIngressAddress reports whether address is a usable host:port.
func ValidIngressAddress(address string) bool {
	host, port, err := net.SplitHostPort(address)
	return err == nil && host != "" && port != ""
}

// ValidateDeploymentArtifact checks that artifact names an application, a code
// version, a config revision and a cluster, and carries three sha256 digests.
func ValidateDeploymentArtifact(artifact DeploymentArtifact) error {
	if !ValidApplicationID(artifact.ApplicationID) || artifact.CodeVersion == "" || artifact.ConfigRevision == "" || artifact.ClusterID == "" {
		return ErrDeploymentArtifactInvalid
	}
	for _, digest := range []string{artifact.CodeDigest, artifact.ConfigDigest, artifact.ArtifactDigest} {
		if !ValidSHA256Digest(digest) {
			return ErrDeploymentArtifactInvalid
		}
	}
	return nil
}

// ValidateRollout checks rollout's shape for its phase and returns its
// canonical form with nodes sorted by ID. Every node must mirror the rollout's
// artifacts and phase, failed phases need a failure, and the others must not
// have one.
func ValidateRollout(rollout Rollout) (Rollout, error) {
	if !ValidApplicationID(rollout.ApplicationID) || rollout.ClusterID == "" || rollout.RolloutID == "" || rollout.Generation == 0 || rollout.Phase == "" || len(rollout.Nodes) == 0 {
		return Rollout{}, ErrRolloutInvalid
	}
	if !ValidSHA256Digest(rollout.CurrentArtifactDigest) {
		return Rollout{}, ErrRolloutInvalid
	}
	if rollout.CandidateArtifactDigest != "" {
		if !ValidSHA256Digest(rollout.CandidateArtifactDigest) || rollout.CandidateArtifactDigest == rollout.CurrentArtifactDigest {
			return Rollout{}, ErrRolloutInvalid
		}
	}
	switch rollout.Phase {
	case RolloutActive:
		if rollout.CandidateArtifactDigest != "" || rollout.Failure != nil {
			return Rollout{}, ErrRolloutInvalid
		}
	case RolloutPending, RolloutCandidateHealthy, RolloutSwitching:
		if rollout.CandidateArtifactDigest == "" || rollout.Failure != nil {
			return Rollout{}, ErrRolloutInvalid
		}
	case RolloutCandidateFailed, RolloutRollingBack, RolloutRolledBack:
		if rollout.CandidateArtifactDigest == "" || !ValidRolloutFailure(rollout.Failure) {
			return Rollout{}, ErrRolloutInvalid
		}
	default:
		return Rollout{}, ErrRolloutInvalid
	}
	nodes := append([]RolloutNodeProgress(nil), rollout.Nodes...)
	seen := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if node.NodeID == "" || node.CurrentArtifactDigest != rollout.CurrentArtifactDigest || node.CandidateArtifactDigest != rollout.CandidateArtifactDigest || node.Phase != rollout.Phase {
			return Rollout{}, ErrRolloutInvalid
		}
		if _, exists := seen[node.NodeID]; exists {
			return Rollout{}, ErrRolloutInvalid
		}
		seen[node.NodeID] = struct{}{}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	rollout.Nodes = nodes
	return rollout, nil
}

// ValidRolloutFailure reports whether failure carries a code and a message.
func ValidRolloutFailure(failure *RolloutFailure) bool {
	return failure != nil && failure.Code != "" && failure.Message != ""
}

// ValidRolloutTransition is the rollout state machine. It reports whether next
// may follow current, keeping the targeted nodes and, except where a switch
// completes, the artifacts:
//
//	active -> pending -> candidate-healthy -> switching -> active
//	             \               \               \
//	              +--------------+---------------+-> candidate-failed -> rolling-back -> rolled-back
func ValidRolloutTransition(current, next Rollout) bool {
	sameArtifacts := next.CurrentArtifactDigest == current.CurrentArtifactDigest && next.CandidateArtifactDigest == current.CandidateArtifactDigest
	sameTargets := slices.EqualFunc(current.Nodes, next.Nodes, func(a, b RolloutNodeProgress) bool {
		return a.NodeID == b.NodeID
	})
	switch current.Phase {
	case RolloutActive:
		return next.Phase == RolloutPending && sameTargets && next.CurrentArtifactDigest == current.CurrentArtifactDigest && next.CandidateArtifactDigest != ""
	case RolloutPending:
		return (next.Phase == RolloutCandidateHealthy || next.Phase == RolloutCandidateFailed) && sameArtifacts && sameTargets
	case RolloutCandidateHealthy:
		return (next.Phase == RolloutSwitching || next.Phase == RolloutCandidateFailed) && sameArtifacts && sameTargets
	case RolloutSwitching:
		return (next.Phase == RolloutActive && sameTargets && next.CurrentArtifactDigest == current.CandidateArtifactDigest && next.CandidateArtifactDigest == "") ||
			(next.Phase == RolloutCandidateFailed && sameArtifacts && sameTargets)
	case RolloutCandidateFailed:
		return next.Phase == RolloutRollingBack && sameArtifacts && sameTargets && EqualRolloutFailure(current.Failure, next.Failure)
	case RolloutRollingBack:
		return next.Phase == RolloutRolledBack && sameArtifacts && sameTargets && EqualRolloutFailure(current.Failure, next.Failure)
	default:
		return false
	}
}

// CheckRolloutCommit decides whether next may be committed over the stored
// rollout (exists=false when none is stored yet). It reports write=false with
// no error for an identical retry. Otherwise the first generation must be 1,
// and each later one must be the next generation of the same cluster under a
// new rollout ID and a valid state transition.
func CheckRolloutCommit(stored Rollout, exists bool, next Rollout) (write bool, err error) {
	if !exists {
		if next.Generation != 1 {
			return false, ErrRolloutGeneration
		}
		return true, nil
	}
	if EqualRollout(stored, next) {
		return false, nil
	}
	if next.Generation != stored.Generation+1 {
		return false, ErrRolloutGeneration
	}
	if next.ClusterID != stored.ClusterID || next.RolloutID == stored.RolloutID || !ValidRolloutTransition(stored, next) {
		return false, ErrRolloutInvalid
	}
	return true, nil
}

// CheckRolloutArtifacts verifies that the artifacts a rollout references
// belong to its application and cluster. candidate is nil when the rollout has
// no candidate.
func CheckRolloutArtifacts(rollout Rollout, current DeploymentArtifact, candidate *DeploymentArtifact) error {
	if current.ApplicationID != rollout.ApplicationID || current.ClusterID != rollout.ClusterID {
		return ErrRolloutInvalid
	}
	if candidate == nil {
		return nil
	}
	if candidate.ApplicationID != rollout.ApplicationID || candidate.ClusterID != rollout.ClusterID {
		return ErrRolloutInvalid
	}
	return nil
}

// EqualRollout reports whether a and b are the same rollout generation.
func EqualRollout(a, b Rollout) bool {
	return a.ApplicationID == b.ApplicationID && a.ClusterID == b.ClusterID && a.RolloutID == b.RolloutID && a.Generation == b.Generation && a.CurrentArtifactDigest == b.CurrentArtifactDigest && a.CandidateArtifactDigest == b.CandidateArtifactDigest && a.Phase == b.Phase && slices.Equal(a.Nodes, b.Nodes) && EqualRolloutFailure(a.Failure, b.Failure)
}

// EqualRolloutFailure compares two optional failures by value.
func EqualRolloutFailure(a, b *RolloutFailure) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// CloneRollout returns a copy of rollout that shares no memory with it.
func CloneRollout(rollout Rollout) Rollout {
	rollout.Nodes = append([]RolloutNodeProgress(nil), rollout.Nodes...)
	rollout.Failure = CloneRolloutFailure(rollout.Failure)
	return rollout
}

// CloneRolloutFailure returns a copy of failure, or nil.
func CloneRolloutFailure(failure *RolloutFailure) *RolloutFailure {
	if failure == nil {
		return nil
	}
	cloned := *failure
	return &cloned
}
