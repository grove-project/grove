package controlplane

import (
	"errors"
	"slices"
	"testing"
)

func route(service uint32, currentNode, candidateNode string) UpgradeRoute {
	return UpgradeRoute{
		Current:   PlacementRecord{ServiceID: serviceID(service), NodeID: currentNode, InvocationSubject: "current." + currentNode, ArtifactDigest: digestA},
		Candidate: PlacementRecord{ServiceID: serviceID(service), NodeID: candidateNode, InvocationSubject: "candidate." + candidateNode, ArtifactDigest: digestB},
	}
}

// commit applies next over stored exactly as the adapter does before its
// compare-and-set write, failing the test when the domain would reject it.
func commit(t *testing.T, stored, next Rollout) Rollout {
	t.Helper()
	next, err := ValidateRollout(next)
	if err != nil {
		t.Fatalf("%s generation %d: ValidateRollout: %v", next.Phase, next.Generation, err)
	}
	write, err := CheckRolloutCommit(stored, true, next)
	if err != nil || !write {
		t.Fatalf("%s -> %s: CheckRolloutCommit = (%v, %v)", stored.Phase, next.Phase, write, err)
	}
	return next
}

// TestHealthyUpgradeStepsCommit walks CommitHealthyUpgrade's records through
// the domain rules: pending -> candidate-healthy -> switching -> active.
func TestHealthyUpgradeStepsCommit(t *testing.T) {
	pending := rollout(RolloutPending, 2, digestA, digestB, "n1", "n2")
	routes := []UpgradeRoute{route(2, "n1", "n2"), route(1, "n1", "n2"), route(3, "n2", "n1")}
	pending, routes, err := ValidateUpgrade(pending, routes)
	if err != nil {
		t.Fatalf("ValidateUpgrade: %v", err)
	}
	if routes[0].Current.ServiceID != 1 || routes[2].Current.ServiceID != 3 {
		t.Errorf("routes not in stable service order: %+v", routes)
	}
	if got := UpgradeCandidateNodes(routes); !slices.Equal(got, []string{"n2", "n1"}) {
		t.Errorf("UpgradeCandidateNodes = %v; want [n2 n1]", got)
	}

	healthy := commit(t, pending, AdvanceUpgradeRollout(pending, RolloutCandidateHealthy, pending.CurrentArtifactDigest, pending.CandidateArtifactDigest))
	switching := commit(t, healthy, AdvanceUpgradeRollout(healthy, RolloutSwitching, healthy.CurrentArtifactDigest, healthy.CandidateArtifactDigest))
	active := commit(t, switching, AdvanceUpgradeRollout(switching, RolloutActive, switching.CandidateArtifactDigest, ""))
	if active.Generation != 5 || active.CurrentArtifactDigest != digestB || active.CandidateArtifactDigest != "" {
		t.Errorf("active = %+v; want generation 5 running the candidate", active)
	}
	if active.RolloutID != "r.3.candidate-healthy.4.switching.5.active" {
		t.Errorf("RolloutID = %q; want one derived from each step", active.RolloutID)
	}
	// A retried step rebuilds the same record, which commits as a no-op.
	again := AdvanceUpgradeRollout(switching, RolloutActive, switching.CandidateArtifactDigest, "")
	if write, err := CheckRolloutCommit(active, true, again); write || err != nil {
		t.Errorf("retried step = (%v, %v); want an idempotent no-op", write, err)
	}
}

// TestRollbackStepsCommit walks RollbackFailedUpgrade's records from each
// in-flight phase: candidate-failed -> rolling-back -> rolled-back.
func TestRollbackStepsCommit(t *testing.T) {
	failure := RolloutFailure{Code: "unhealthy", Message: "candidate did not become ready"}
	for _, phase := range []RolloutPhase{RolloutPending, RolloutCandidateHealthy, RolloutSwitching} {
		inFlight := rollout(phase, 3, digestA, digestB, "n1")
		inFlight, _, err := ValidateRollback(inFlight, []UpgradeRoute{route(1, "n1", "n1")}, failure)
		if err != nil {
			t.Fatalf("%s: ValidateRollback: %v", phase, err)
		}
		failed := commit(t, inFlight, AdvanceFailedRollout(inFlight, RolloutCandidateFailed, failure))
		rollingBack := commit(t, failed, AdvanceFailedRollout(failed, RolloutRollingBack, failure))
		rolledBack := commit(t, rollingBack, AdvanceFailedRollout(rollingBack, RolloutRolledBack, failure))
		if rolledBack.CurrentArtifactDigest != digestA || rolledBack.CandidateArtifactDigest != digestB || *rolledBack.Failure != failure {
			t.Errorf("%s: rolled back = %+v; want the current artifact kept and the candidate recorded with its cause", phase, rolledBack)
		}
	}
}

func TestUpgradeValidationRejectsMismatchedRoutes(t *testing.T) {
	pending := rollout(RolloutPending, 2, digestA, digestB, "n1")
	failure := RolloutFailure{Code: "c", Message: "m"}
	cases := map[string]struct {
		rollout Rollout
		routes  []UpgradeRoute
	}{
		"no routes":         {pending, nil},
		"not pending":       {rollout(RolloutCandidateHealthy, 2, digestA, digestB, "n1"), []UpgradeRoute{route(1, "n1", "n1")}},
		"duplicate service": {pending, []UpgradeRoute{route(1, "n1", "n1"), route(1, "n2", "n2")}},
		"service changes":   {pending, []UpgradeRoute{func() UpgradeRoute { r := route(1, "n1", "n1"); r.Candidate.ServiceID = 2; return r }()}},
		"wrong current":     {pending, []UpgradeRoute{func() UpgradeRoute { r := route(1, "n1", "n1"); r.Current.ArtifactDigest = digestC; return r }()}},
		"wrong candidate":   {pending, []UpgradeRoute{func() UpgradeRoute { r := route(1, "n1", "n1"); r.Candidate.ArtifactDigest = digestA; return r }()}},
		"incomplete record": {pending, []UpgradeRoute{func() UpgradeRoute { r := route(1, "n1", "n1"); r.Candidate.InvocationSubject = ""; return r }()}},
	}
	for name, tc := range cases {
		if _, _, err := ValidateUpgrade(tc.rollout, tc.routes); !errors.Is(err, ErrUpgradeInvalid) {
			t.Errorf("upgrade %s: err = %v; want ErrUpgradeInvalid", name, err)
		}
	}
	if _, _, err := ValidateRollback(rollout(RolloutActive, 1, digestA, "", "n1"), []UpgradeRoute{route(1, "n1", "n1")}, failure); !errors.Is(err, ErrUpgradeInvalid) {
		t.Errorf("rollback of an active rollout: err = %v", err)
	}
	if _, _, err := ValidateRollback(pending, []UpgradeRoute{route(1, "n1", "n1")}, RolloutFailure{Code: "c"}); !errors.Is(err, ErrUpgradeInvalid) {
		t.Errorf("rollback without a failure message: err = %v", err)
	}
}
