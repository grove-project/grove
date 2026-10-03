package controlplane

import (
	"errors"
	"strings"
	"testing"
)

var (
	digestA = "sha256:" + strings.Repeat("a", 64)
	digestB = "sha256:" + strings.Repeat("b", 64)
	digestC = "sha256:" + strings.Repeat("c", 64)
)

func artifact(digest string) DeploymentArtifact {
	return DeploymentArtifact{
		ApplicationID: "shop", CodeVersion: "v1", CodeDigest: digestC,
		ConfigRevision: "r1", ConfigDigest: digestC, ArtifactDigest: digest, ClusterID: "c1",
	}
}

// rollout builds a valid rollout in phase over nodes, with failure set for the
// failed phases.
func rollout(phase RolloutPhase, generation uint64, current, candidate string, nodes ...string) Rollout {
	r := Rollout{
		ApplicationID: "shop", ClusterID: "c1", RolloutID: "r", Generation: generation,
		CurrentArtifactDigest: current, CandidateArtifactDigest: candidate, Phase: phase,
	}
	switch phase {
	case RolloutCandidateFailed, RolloutRollingBack, RolloutRolledBack:
		r.Failure = &RolloutFailure{Code: "unhealthy", Message: "candidate did not become ready"}
	}
	for _, id := range nodes {
		r.Nodes = append(r.Nodes, RolloutNodeProgress{NodeID: id, CurrentArtifactDigest: current, CandidateArtifactDigest: candidate, Phase: phase})
	}
	return r
}

func TestSHA256DigestValidation(t *testing.T) {
	for digest, want := range map[string]bool{
		digestA:                             true,
		"sha256:" + strings.Repeat("A", 64): false, // uppercase
		"sha256:" + strings.Repeat("a", 63): false,
		"sha256:" + strings.Repeat("g", 64): false, // not hex
		"sha512:" + strings.Repeat("a", 64): false,
		strings.Repeat("a", 64):             false,
		"":                                  false,
	} {
		if got := ValidSHA256Digest(digest); got != want {
			t.Errorf("ValidSHA256Digest(%q) = %v; want %v", digest, got, want)
		}
	}
}

func TestValidateDeploymentArtifact(t *testing.T) {
	if err := ValidateDeploymentArtifact(artifact(digestA)); err != nil {
		t.Fatalf("valid artifact: %v", err)
	}
	for name, mutate := range map[string]func(*DeploymentArtifact){
		"dotted application": func(a *DeploymentArtifact) { a.ApplicationID = "shop.v2" },
		"no code version":    func(a *DeploymentArtifact) { a.CodeVersion = "" },
		"no config revision": func(a *DeploymentArtifact) { a.ConfigRevision = "" },
		"no cluster":         func(a *DeploymentArtifact) { a.ClusterID = "" },
		"bad code digest":    func(a *DeploymentArtifact) { a.CodeDigest = "sha256:x" },
		"bad config digest":  func(a *DeploymentArtifact) { a.ConfigDigest = "" },
		"bad digest":         func(a *DeploymentArtifact) { a.ArtifactDigest = "abc" },
	} {
		a := artifact(digestA)
		mutate(&a)
		if err := ValidateDeploymentArtifact(a); !errors.Is(err, ErrDeploymentArtifactInvalid) {
			t.Errorf("%s: err = %v; want ErrDeploymentArtifactInvalid", name, err)
		}
	}
}

func TestValidIngressAddress(t *testing.T) {
	for address, want := range map[string]bool{
		"127.0.0.1:8080": true,
		"shop.local:80":  true,
		"[::1]:443":      true,
		":8080":          false,
		"127.0.0.1:":     false,
		"127.0.0.1":      false,
		"":               false,
	} {
		if got := ValidIngressAddress(address); got != want {
			t.Errorf("ValidIngressAddress(%q) = %v; want %v", address, got, want)
		}
	}
}

func TestValidateRolloutSortsNodesAndChecksPhaseShape(t *testing.T) {
	r := rollout(RolloutPending, 2, digestA, digestB, "node-3", "node-1", "node-2")
	got, err := ValidateRollout(r)
	if err != nil {
		t.Fatalf("ValidateRollout: %v", err)
	}
	if got.Nodes[0].NodeID != "node-1" || got.Nodes[2].NodeID != "node-3" {
		t.Errorf("nodes not sorted: %+v", got.Nodes)
	}
	if r.Nodes[0].NodeID != "node-3" {
		t.Error("ValidateRollout reordered the caller's nodes")
	}

	invalid := map[string]Rollout{
		"active with candidate":     rollout(RolloutActive, 1, digestA, digestB, "n1"),
		"pending without candidate": rollout(RolloutPending, 1, digestA, "", "n1"),
		"candidate equals current":  rollout(RolloutPending, 1, digestA, digestA, "n1"),
		"failed without failure": func() Rollout {
			r := rollout(RolloutCandidateFailed, 1, digestA, digestB, "n1")
			r.Failure = nil
			return r
		}(),
		"healthy with failure": func() Rollout {
			r := rollout(RolloutCandidateHealthy, 1, digestA, digestB, "n1")
			r.Failure = &RolloutFailure{Code: "x", Message: "y"}
			return r
		}(),
		"failure without message": func() Rollout {
			r := rollout(RolloutRolledBack, 1, digestA, digestB, "n1")
			r.Failure.Message = ""
			return r
		}(),
		"unknown phase":   rollout("paused", 1, digestA, "", "n1"),
		"zero generation": rollout(RolloutActive, 0, digestA, "", "n1"),
		"no nodes":        rollout(RolloutActive, 1, digestA, ""),
		"duplicate node":  rollout(RolloutActive, 1, digestA, "", "n1", "n1"),
		"node phase differs": func() Rollout {
			r := rollout(RolloutActive, 1, digestA, "", "n1")
			r.Nodes[0].Phase = RolloutPending
			return r
		}(),
		"node artifact differs": func() Rollout {
			r := rollout(RolloutActive, 1, digestA, "", "n1")
			r.Nodes[0].CurrentArtifactDigest = digestB
			return r
		}(),
		"bad current digest": rollout(RolloutActive, 1, "sha256:short", "", "n1"),
		"no rollout ID":      func() Rollout { r := rollout(RolloutActive, 1, digestA, "", "n1"); r.RolloutID = ""; return r }(),
	}
	for name, r := range invalid {
		if _, err := ValidateRollout(r); !errors.Is(err, ErrRolloutInvalid) {
			t.Errorf("%s: err = %v; want ErrRolloutInvalid", name, err)
		}
	}
}

// TestRolloutStateMachine checks every phase pair against the documented
// transitions, so adding or dropping an edge fails here.
func TestRolloutStateMachine(t *testing.T) {
	phases := []RolloutPhase{RolloutActive, RolloutPending, RolloutCandidateHealthy, RolloutSwitching, RolloutCandidateFailed, RolloutRollingBack, RolloutRolledBack}
	allowed := map[[2]RolloutPhase]bool{
		{RolloutActive, RolloutPending}:                   true,
		{RolloutPending, RolloutCandidateHealthy}:         true,
		{RolloutPending, RolloutCandidateFailed}:          true,
		{RolloutCandidateHealthy, RolloutSwitching}:       true,
		{RolloutCandidateHealthy, RolloutCandidateFailed}: true,
		{RolloutSwitching, RolloutActive}:                 true,
		{RolloutSwitching, RolloutCandidateFailed}:        true,
		{RolloutCandidateFailed, RolloutRollingBack}:      true,
		{RolloutRollingBack, RolloutRolledBack}:           true,
	}
	shape := func(phase RolloutPhase, from RolloutPhase) Rollout {
		// The switch to active promotes the candidate; every other step keeps
		// the artifacts.
		if phase == RolloutActive && from == RolloutSwitching {
			return rollout(phase, 2, digestB, "", "n1", "n2")
		}
		if phase == RolloutActive {
			return rollout(phase, 2, digestA, "", "n1", "n2")
		}
		return rollout(phase, 2, digestA, digestB, "n1", "n2")
	}
	for _, from := range phases {
		for _, to := range phases {
			current := shape(from, "")
			next := shape(to, from)
			if got, want := ValidRolloutTransition(current, next), allowed[[2]RolloutPhase{from, to}]; got != want {
				t.Errorf("%s -> %s allowed = %v; want %v", from, to, got, want)
			}
		}
	}
}

func TestRolloutTransitionKeepsTargetsArtifactsAndFailure(t *testing.T) {
	pending := rollout(RolloutPending, 2, digestA, digestB, "n1", "n2")
	if ValidRolloutTransition(pending, rollout(RolloutCandidateHealthy, 3, digestA, digestB, "n1")) {
		t.Error("transition dropped a target node")
	}
	if ValidRolloutTransition(pending, rollout(RolloutCandidateHealthy, 3, digestA, digestC, "n1", "n2")) {
		t.Error("transition changed the candidate artifact")
	}
	if ValidRolloutTransition(rollout(RolloutSwitching, 2, digestA, digestB, "n1"), rollout(RolloutActive, 3, digestA, "", "n1")) {
		t.Error("switching -> active kept the old artifact instead of promoting the candidate")
	}
	failed := rollout(RolloutCandidateFailed, 2, digestA, digestB, "n1")
	back := rollout(RolloutRollingBack, 3, digestA, digestB, "n1")
	back.Failure = &RolloutFailure{Code: "other", Message: "rewritten cause"}
	if ValidRolloutTransition(failed, back) {
		t.Error("rollback rewrote the recorded failure")
	}
}

func TestCheckRolloutCommit(t *testing.T) {
	active := rollout(RolloutActive, 1, digestA, "", "n1")
	pending := rollout(RolloutPending, 2, digestA, digestB, "n1")
	pending.RolloutID = "r2"

	cases := []struct {
		name   string
		stored Rollout
		exists bool
		next   Rollout
		write  bool
		err    error
	}{
		{"first generation", Rollout{}, false, active, true, nil},
		{"first must be generation 1", Rollout{}, false, pending, false, ErrRolloutGeneration},
		{"identical retry", active, true, active, false, nil},
		{"next generation", active, true, pending, true, nil},
		{"skipped generation", active, true, func() Rollout { r := pending; r.Generation = 3; return r }(), false, ErrRolloutGeneration},
		{"stale generation", pending, true, active, false, ErrRolloutGeneration},
		{"other cluster", active, true, func() Rollout { r := pending; r.ClusterID = "c2"; return r }(), false, ErrRolloutInvalid},
		{"reused rollout ID", active, true, func() Rollout { r := pending; r.RolloutID = active.RolloutID; return r }(), false, ErrRolloutInvalid},
		{"invalid transition", active, true, func() Rollout {
			r := rollout(RolloutSwitching, 2, digestA, digestB, "n1")
			r.RolloutID = "r2"
			return r
		}(), false, ErrRolloutInvalid},
	}
	for _, tc := range cases {
		write, err := CheckRolloutCommit(tc.stored, tc.exists, tc.next)
		if write != tc.write || !errors.Is(err, tc.err) || (tc.err == nil && err != nil) {
			t.Errorf("%s: CheckRolloutCommit = (%v, %v); want (%v, %v)", tc.name, write, err, tc.write, tc.err)
		}
	}
}

func TestCheckRolloutArtifacts(t *testing.T) {
	pending := rollout(RolloutPending, 2, digestA, digestB, "n1")
	current, candidate := artifact(digestA), artifact(digestB)
	if err := CheckRolloutArtifacts(pending, current, &candidate); err != nil {
		t.Fatalf("matching artifacts: %v", err)
	}
	if err := CheckRolloutArtifacts(rollout(RolloutActive, 1, digestA, "", "n1"), current, nil); err != nil {
		t.Fatalf("active rollout without candidate: %v", err)
	}
	foreign := artifact(digestB)
	foreign.ClusterID = "c2"
	if err := CheckRolloutArtifacts(pending, current, &foreign); !errors.Is(err, ErrRolloutInvalid) {
		t.Errorf("candidate from another cluster: err = %v", err)
	}
	other := artifact(digestA)
	other.ApplicationID = "billing"
	if err := CheckRolloutArtifacts(pending, other, &candidate); !errors.Is(err, ErrRolloutInvalid) {
		t.Errorf("current from another application: err = %v", err)
	}
}

func TestCloneRolloutSharesNoMemory(t *testing.T) {
	r := rollout(RolloutRolledBack, 3, digestA, digestB, "n1")
	clone := CloneRollout(r)
	clone.Nodes[0].NodeID = "changed"
	clone.Failure.Code = "changed"
	if r.Nodes[0].NodeID != "n1" || r.Failure.Code != "unhealthy" {
		t.Errorf("clone shares memory with original: %+v", r)
	}
}
