package rollout_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/controlplane"
	"github.com/grove-project/grove/internal/rollout"
)

var (
	digestA = "sha256:" + strings.Repeat("a", 64)
	digestB = "sha256:" + strings.Repeat("b", 64)
	errDown = errors.New("store unavailable")
)

// memoryStore is an in-memory Store that applies the same control-plane
// rules the System NATS adapter applies before each compare-and-set write.
// It logs every accepted write and can fail one call on demand.
type memoryStore struct {
	artifacts  map[string]controlplane.DeploymentArtifact
	desired    []controlplane.DesiredDeployment
	rollout    controlplane.Rollout
	hasRollout bool
	placements map[grove.ServiceID]controlplane.PlacementRecord
	healthy    map[string]string
	log        []string
	// failAt fails the call whose log entry would start with this prefix.
	failAt string
	// rejectDesired rejects this many desired writes before accepting one.
	rejectDesired int
}

func newMemoryStore(placements ...controlplane.PlacementRecord) *memoryStore {
	store := &memoryStore{
		artifacts:  make(map[string]controlplane.DeploymentArtifact),
		placements: make(map[grove.ServiceID]controlplane.PlacementRecord),
		healthy:    make(map[string]string),
	}
	for _, placement := range placements {
		store.placements[placement.ServiceID] = placement
	}
	return store
}

func (s *memoryStore) fail(entry string) error {
	if s.failAt != "" && strings.HasPrefix(entry, s.failAt) {
		s.failAt = ""
		return errDown
	}
	return nil
}

func (s *memoryStore) PutArtifact(_ context.Context, artifact controlplane.DeploymentArtifact) error {
	entry := "artifact " + artifact.ArtifactDigest[:8]
	if err := s.fail(entry); err != nil {
		return err
	}
	if err := controlplane.ValidateDeploymentArtifact(artifact); err != nil {
		return err
	}
	s.artifacts[artifact.ArtifactDigest] = artifact
	s.log = append(s.log, entry)
	return nil
}

func (s *memoryStore) PutDesired(_ context.Context, desired controlplane.DesiredDeployment) error {
	if s.rejectDesired > 0 {
		s.rejectDesired--
		return errDown
	}
	s.desired = append(s.desired, desired)
	s.log = append(s.log, "desired")
	return nil
}

func (s *memoryStore) PutRollout(_ context.Context, next controlplane.Rollout) error {
	entry := "rollout " + string(next.Phase)
	if err := s.fail(entry); err != nil {
		return err
	}
	next, err := controlplane.ValidateRollout(next)
	if err != nil {
		return err
	}
	if _, ok := s.artifacts[next.CurrentArtifactDigest]; !ok {
		return fmt.Errorf("current artifact %s not recorded", next.CurrentArtifactDigest)
	}
	write, err := controlplane.CheckRolloutCommit(s.rollout, s.hasRollout, next)
	if err != nil || !write {
		return err
	}
	s.rollout, s.hasRollout = next, true
	s.log = append(s.log, entry)
	return nil
}

func (s *memoryStore) ReplacePlacement(_ context.Context, current, replacement controlplane.PlacementRecord) error {
	entry := fmt.Sprintf("replace %d %s", replacement.ServiceID, replacement.NodeID)
	if err := s.fail(entry); err != nil {
		return err
	}
	write, err := controlplane.CheckPlacementReplacement(s.placements[current.ServiceID], current, replacement)
	if err != nil || !write {
		return err
	}
	s.placements[current.ServiceID] = replacement
	s.log = append(s.log, entry)
	return nil
}

func (s *memoryStore) WaitCandidateHealthy(_ context.Context, nodeID, artifactDigest string) error {
	entry := "healthy " + nodeID
	if err := s.fail(entry); err != nil {
		return err
	}
	if s.healthy[nodeID] != artifactDigest {
		return fmt.Errorf("%s is not healthy on %s", nodeID, artifactDigest)
	}
	s.log = append(s.log, entry)
	return nil
}

func artifact(digest, revision string) controlplane.DeploymentArtifact {
	return controlplane.DeploymentArtifact{
		ApplicationID: "grove-shop", CodeVersion: "v1", CodeDigest: "sha256:" + strings.Repeat("c", 64),
		ConfigRevision: revision, ConfigDigest: "sha256:" + strings.Repeat("d", 64),
		ArtifactDigest: digest, ClusterID: "production",
	}
}

func placement(service grove.ServiceID, nodeID, digest string) controlplane.PlacementRecord {
	return controlplane.PlacementRecord{
		ServiceID: service, NodeID: nodeID, ArtifactDigest: digest,
		InvocationSubject: fmt.Sprintf("_GROVE.test.%s.%d", nodeID, service),
	}
}

// pendingUpgrade activates digestA on node-1 and node-2 and proposes digestB,
// returning the store, the pending rollout and the routes moving services 1
// and 2 to candidate nodes.
func pendingUpgrade(t *testing.T) (*memoryStore, *rollout.Operator, controlplane.Rollout, []controlplane.UpgradeRoute) {
	t.Helper()
	routes := []controlplane.UpgradeRoute{
		{Current: placement(2, "node-2", digestA), Candidate: placement(2, "candidate-2", digestB)},
		{Current: placement(1, "node-1", digestA), Candidate: placement(1, "candidate-1", digestB)},
	}
	store := newMemoryStore(routes[0].Current, routes[1].Current)
	operator := rollout.New(store)
	nodes := []string{"node-1", "node-2"}
	if _, err := operator.Activate(t.Context(), artifact(digestA, "r1"), rollout.Generation{RolloutID: "shop-1", Generation: 1, NodeIDs: nodes}); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	pending, err := operator.Propose(t.Context(), digestA, artifact(digestB, "r2"), rollout.Generation{RolloutID: "shop-2", Generation: 2, NodeIDs: nodes})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	store.log = nil
	return store, operator, pending, routes
}

func TestActivateAndProposeRecordArtifactsBeforeRollouts(t *testing.T) {
	store := newMemoryStore()
	operator := rollout.New(store)
	nodes := []string{"node-1", "node-2", "node-3"}
	active, err := operator.Activate(t.Context(), artifact(digestA, "r1"), rollout.Generation{RolloutID: "shop-1", Generation: 1, NodeIDs: nodes})
	if err != nil {
		t.Fatal(err)
	}
	if active.Phase != controlplane.RolloutActive || active.CurrentArtifactDigest != digestA || active.CandidateArtifactDigest != "" ||
		active.ApplicationID != "grove-shop" || active.ClusterID != "production" || len(active.Nodes) != 3 {
		t.Errorf("active = %+v", active)
	}
	pending, err := operator.Propose(t.Context(), digestA, artifact(digestB, "r2"), rollout.Generation{RolloutID: "shop-2", Generation: 2, NodeIDs: nodes})
	if err != nil {
		t.Fatal(err)
	}
	if pending.Phase != controlplane.RolloutPending || pending.CurrentArtifactDigest != digestA || pending.CandidateArtifactDigest != digestB {
		t.Errorf("pending = %+v", pending)
	}
	for _, node := range pending.Nodes {
		if node.Phase != controlplane.RolloutPending || node.CandidateArtifactDigest != digestB {
			t.Errorf("pending node = %+v", node)
		}
	}
	want := []string{"artifact sha256:a", "rollout active", "artifact sha256:b", "rollout pending"}
	if !slices.Equal(store.log, want) {
		t.Errorf("writes = %q; want %q", store.log, want)
	}
}

func TestActivateFailsWithoutRecordingRolloutWhenArtifactWriteFails(t *testing.T) {
	store := newMemoryStore()
	store.failAt = "artifact"
	_, err := rollout.New(store).Activate(t.Context(), artifact(digestA, "r1"), rollout.Generation{RolloutID: "shop-1", Generation: 1, NodeIDs: []string{"node-1"}})
	if !errors.Is(err, errDown) {
		t.Fatalf("Activate error = %v; want %v", err, errDown)
	}
	if store.hasRollout {
		t.Errorf("rollout recorded after the artifact write failed: %+v", store.rollout)
	}
}

func TestCommitGatesOnCandidatesThenSwitchesInServiceOrder(t *testing.T) {
	store, operator, pending, routes := pendingUpgrade(t)
	store.healthy["candidate-1"] = digestB
	store.healthy["candidate-2"] = digestB
	active, err := operator.Commit(t.Context(), pending, routes)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"healthy candidate-1", "healthy candidate-2",
		"rollout candidate-healthy", "rollout switching",
		"replace 1 candidate-1", "replace 2 candidate-2",
		"rollout active",
	}
	if !slices.Equal(store.log, want) {
		t.Errorf("steps = %q; want %q", store.log, want)
	}
	if active.Phase != controlplane.RolloutActive || active.Generation != 5 ||
		active.CurrentArtifactDigest != digestB || active.CandidateArtifactDigest != "" {
		t.Errorf("committed = %+v", active)
	}
	if store.rollout.Generation != active.Generation {
		t.Errorf("stored generation %d; want %d", store.rollout.Generation, active.Generation)
	}
}

func TestCommitWritesNothingUntilEveryCandidateIsHealthy(t *testing.T) {
	store, operator, pending, routes := pendingUpgrade(t)
	store.healthy["candidate-1"] = digestB
	store.healthy["candidate-2"] = digestA // healthy, but on the wrong artifact
	if _, err := operator.Commit(t.Context(), pending, routes); err == nil {
		t.Fatal("Commit succeeded with a candidate on the wrong artifact")
	}
	if store.rollout.Phase != controlplane.RolloutPending || slices.ContainsFunc(store.log, func(entry string) bool {
		return strings.HasPrefix(entry, "rollout") || strings.HasPrefix(entry, "replace")
	}) {
		t.Errorf("writes before every candidate was healthy: %q (rollout %s)", store.log, store.rollout.Phase)
	}
}

func TestCommitRejectsInvalidUpgradeWithoutWriting(t *testing.T) {
	store, operator, pending, routes := pendingUpgrade(t)
	routes[0].Candidate.ArtifactDigest = digestA
	if _, err := operator.Commit(t.Context(), pending, routes); !errors.Is(err, controlplane.ErrUpgradeInvalid) {
		t.Errorf("Commit error = %v; want %v", err, controlplane.ErrUpgradeInvalid)
	}
	if len(store.log) != 0 {
		t.Errorf("writes = %q; want none", store.log)
	}
}

// TestFailedCommitRollsBackFromWhereItStopped fails Commit midway through
// switching; the stored switching rollout is then rolled back, which restores
// every service, including the one that had already moved.
func TestFailedCommitRollsBackFromWhereItStopped(t *testing.T) {
	store, operator, pending, routes := pendingUpgrade(t)
	store.healthy["candidate-1"] = digestB
	store.healthy["candidate-2"] = digestB
	store.failAt = "replace 2"
	if _, err := operator.Commit(t.Context(), pending, routes); !errors.Is(err, errDown) {
		t.Fatalf("Commit error = %v; want %v", err, errDown)
	}
	if store.rollout.Phase != controlplane.RolloutSwitching || store.placements[1].NodeID != "candidate-1" || store.placements[2].NodeID != "node-2" {
		t.Fatalf("after failure: rollout %s, placements %+v", store.rollout.Phase, store.placements)
	}
	failure := controlplane.RolloutFailure{Code: "switch_failed", Message: "placement store unavailable"}
	rolledBack, err := operator.Rollback(t.Context(), store.rollout, routes, failure)
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if rolledBack.Phase != controlplane.RolloutRolledBack || rolledBack.CurrentArtifactDigest != digestA {
		t.Errorf("rolled back = %+v", rolledBack)
	}
	for _, route := range routes {
		if store.placements[route.Current.ServiceID] != route.Current {
			t.Errorf("service %d placed %+v; want %+v", route.Current.ServiceID, store.placements[route.Current.ServiceID], route.Current)
		}
	}
}

func TestRollbackRestoresCurrentOwnershipWithCause(t *testing.T) {
	store, operator, pending, routes := pendingUpgrade(t)
	// One service had already moved when the candidate failed.
	store.placements[1] = routes[1].Candidate
	failure := controlplane.RolloutFailure{
		Code: "candidate_startup_failed", Component: "Inventory",
		Field: "inventory.reservation_buffer", Message: "must be zero or greater",
	}
	rolledBack, err := operator.Rollback(t.Context(), pending, routes, failure)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"rollout candidate-failed", "rollout rolling-back",
		"replace 1 node-1",
		"rollout rolled-back",
	}
	if !slices.Equal(store.log, want) {
		t.Errorf("steps = %q; want %q", store.log, want)
	}
	if rolledBack.Phase != controlplane.RolloutRolledBack || rolledBack.Generation != 5 ||
		rolledBack.CurrentArtifactDigest != digestA || rolledBack.Failure == nil || *rolledBack.Failure != failure {
		t.Errorf("rolled back = %+v", rolledBack)
	}
	for _, route := range routes {
		if store.placements[route.Current.ServiceID] != route.Current {
			t.Errorf("service %d placed %+v; want %+v", route.Current.ServiceID, store.placements[route.Current.ServiceID], route.Current)
		}
	}
}

func TestRollbackRequiresACause(t *testing.T) {
	store, operator, pending, routes := pendingUpgrade(t)
	if _, err := operator.Rollback(t.Context(), pending, routes, controlplane.RolloutFailure{}); !errors.Is(err, controlplane.ErrUpgradeInvalid) {
		t.Errorf("Rollback error = %v; want %v", err, controlplane.ErrUpgradeInvalid)
	}
	if len(store.log) != 0 {
		t.Errorf("writes = %q; want none", store.log)
	}
}

func TestRecordDesiredRetriesUntilAccepted(t *testing.T) {
	store := newMemoryStore()
	store.rejectDesired = 3
	desired := controlplane.DesiredDeployment{ApplicationID: "grove-shop", Version: "v1", ArtifactDigest: digestA}
	if err := rollout.New(store).RecordDesired(t.Context(), desired); err != nil {
		t.Fatal(err)
	}
	if len(store.desired) != 1 || store.rejectDesired != 0 {
		t.Errorf("desired writes = %d, rejections left %d", len(store.desired), store.rejectDesired)
	}
}

func TestRecordDesiredGivesUpWhenContextEnds(t *testing.T) {
	store := newMemoryStore()
	store.rejectDesired = 1 << 30
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := rollout.New(store).RecordDesired(ctx, controlplane.DesiredDeployment{})
	if !errors.Is(err, errDown) || !errors.Is(err, context.Canceled) {
		t.Errorf("RecordDesired error = %v; want %v and %v", err, errDown, context.Canceled)
	}
}
