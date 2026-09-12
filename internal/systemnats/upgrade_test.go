package systemnats_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/grove-project/grove/internal/bootstrap"
	"github.com/grove-project/grove/internal/systemnats"
)

func TestBootstrapReadinessRequiresExactArtifact(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	server, err := systemnats.StartServer(ctx, "127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Shutdown)
	responder, err := systemnats.Connect(ctx, server.URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(responder.Close)
	requester, err := systemnats.Connect(ctx, server.URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(requester.Close)
	readiness := bootstrap.Readiness{NodeID: "candidate-a", ArtifactDigest: testDigest("b"), State: bootstrap.ReadinessHealthy}
	if err := responder.ServeBootstrapReadiness(ctx, readiness); err != nil {
		t.Fatal(err)
	}
	if err := requester.WaitBootstrapHealthy(ctx, readiness.NodeID, readiness.ArtifactDigest); err != nil {
		t.Fatal(err)
	}
	if err := requester.WaitBootstrapHealthy(ctx, readiness.NodeID, testDigest("c")); !errors.Is(err, systemnats.ErrCandidateReadinessMismatch) {
		t.Errorf("wrong-artifact readiness error = %v; want %v", err, systemnats.ErrCandidateReadinessMismatch)
	}
}

func TestHealthyUpgradeCommitsCandidateOwnership(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	_, transports := startPlacementCluster(t, ctx)
	currentArtifact := deploymentArtifact("a", "b", "c", "r42", "cloud")
	candidateArtifact := deploymentArtifact("a", "d", "e", "r43", "cloud")
	currentRoutes := []systemnats.PlacementRecord{
		{ServiceID: 1, NodeID: "node-1", InvocationSubject: "_GROVE.system.current.orders", ArtifactDigest: currentArtifact.ArtifactDigest},
		{ServiceID: 2, NodeID: "node-2", InvocationSubject: "_GROVE.system.current.inventory", ArtifactDigest: currentArtifact.ArtifactDigest},
	}
	candidateRoutes := []systemnats.PlacementRecord{
		{ServiceID: 1, NodeID: "candidate-1", InvocationSubject: "_GROVE.system.candidate.orders", ArtifactDigest: candidateArtifact.ArtifactDigest},
		{ServiceID: 2, NodeID: "candidate-2", InvocationSubject: "_GROVE.system.candidate.inventory", ArtifactDigest: candidateArtifact.ArtifactDigest},
	}
	placements, deployments := runUpgradeViews(t, ctx, transports, currentRoutes)
	if err := waitForDeployments(ctx, transports, []systemnats.DeploymentArtifact{}, []systemnats.Rollout{}); err != nil {
		t.Fatal(err)
	}
	if _, err := waitForPlacementViews(ctx, transports, []string{"node-1", "node-2", "node-3"}, currentRoutes); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range []systemnats.DeploymentArtifact{currentArtifact, candidateArtifact} {
		if err := deployments[0].PutArtifact(ctx, transports[0], artifact); err != nil {
			t.Fatal(err)
		}
	}
	active := upgradeRollout(1, "initial", systemnats.RolloutActive, currentArtifact.ArtifactDigest, "", "candidate-1", "candidate-2")
	if err := deployments[0].PutRollout(ctx, transports[0], active); err != nil {
		t.Fatal(err)
	}
	pending := upgradeRollout(2, "upgrade.pending", systemnats.RolloutPending, currentArtifact.ArtifactDigest, candidateArtifact.ArtifactDigest, "candidate-1", "candidate-2")
	if err := deployments[0].PutRollout(ctx, transports[0], pending); err != nil {
		t.Fatal(err)
	}
	for i, nodeID := range []string{"candidate-1", "candidate-2"} {
		if err := transports[i].ServeBootstrapReadiness(ctx, bootstrap.Readiness{
			NodeID: nodeID, ArtifactDigest: candidateArtifact.ArtifactDigest, State: bootstrap.ReadinessHealthy,
		}); err != nil {
			t.Fatal(err)
		}
	}
	routes := []systemnats.UpgradeRoute{
		{Current: currentRoutes[1], Candidate: candidateRoutes[1]},
		{Current: currentRoutes[0], Candidate: candidateRoutes[0]},
	}
	committed, err := deployments[2].CommitHealthyUpgrade(ctx, transports[2], pending, routes)
	if err != nil {
		t.Fatal(err)
	}
	if committed.Phase != systemnats.RolloutActive || committed.Generation != 5 || committed.CurrentArtifactDigest != candidateArtifact.ArtifactDigest || committed.CandidateArtifactDigest != "" {
		t.Errorf("committed rollout = %#v", committed)
	}
	if _, err := waitForPlacementViews(ctx, transports, []string{"node-1", "node-2", "node-3"}, candidateRoutes); err != nil {
		t.Fatal(err)
	}
	artifacts := []systemnats.DeploymentArtifact{currentArtifact, candidateArtifact}
	slices.SortFunc(artifacts, func(a, b systemnats.DeploymentArtifact) int {
		if a.ArtifactDigest < b.ArtifactDigest {
			return -1
		}
		if a.ArtifactDigest > b.ArtifactDigest {
			return 1
		}
		return 0
	})
	if err := waitForDeployments(ctx, transports, artifacts, []systemnats.Rollout{committed}); err != nil {
		t.Fatal(err)
	}
	for _, placement := range placements {
		for _, route := range candidateRoutes {
			got, err := placement.Lookup(route.ServiceID)
			if err != nil || got != route {
				t.Errorf("candidate route %d = %#v, %v; want %#v", route.ServiceID, got, err, route)
			}
		}
	}
}

func TestFailedUpgradeRestoresKnownGoodOwnership(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	_, transports := startPlacementCluster(t, ctx)
	currentArtifact := deploymentArtifact("a", "b", "c", "r42", "cloud")
	candidateArtifact := deploymentArtifact("a", "d", "e", "r43", "cloud")
	currentRoutes := []systemnats.PlacementRecord{
		{ServiceID: 1, NodeID: "node-1", InvocationSubject: "_GROVE.system.current.orders", ArtifactDigest: currentArtifact.ArtifactDigest},
		{ServiceID: 2, NodeID: "node-2", InvocationSubject: "_GROVE.system.current.inventory", ArtifactDigest: currentArtifact.ArtifactDigest},
	}
	candidateRoutes := []systemnats.PlacementRecord{
		{ServiceID: 1, NodeID: "candidate-1", InvocationSubject: "_GROVE.system.candidate.orders", ArtifactDigest: candidateArtifact.ArtifactDigest},
		{ServiceID: 2, NodeID: "candidate-2", InvocationSubject: "_GROVE.system.candidate.inventory", ArtifactDigest: candidateArtifact.ArtifactDigest},
	}
	placements, deployments := runUpgradeViews(t, ctx, transports, currentRoutes)
	if err := waitForDeployments(ctx, transports, []systemnats.DeploymentArtifact{}, []systemnats.Rollout{}); err != nil {
		t.Fatal(err)
	}
	if _, err := waitForPlacementViews(ctx, transports, []string{"node-1", "node-2", "node-3"}, currentRoutes); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range []systemnats.DeploymentArtifact{currentArtifact, candidateArtifact} {
		if err := deployments[0].PutArtifact(ctx, transports[0], artifact); err != nil {
			t.Fatal(err)
		}
	}
	active := upgradeRollout(1, "initial", systemnats.RolloutActive, currentArtifact.ArtifactDigest, "", "node-1", "node-2")
	if err := deployments[0].PutRollout(ctx, transports[0], active); err != nil {
		t.Fatal(err)
	}
	pending := upgradeRollout(2, "upgrade.pending", systemnats.RolloutPending, currentArtifact.ArtifactDigest, candidateArtifact.ArtifactDigest, "node-1", "node-2")
	if err := deployments[0].PutRollout(ctx, transports[0], pending); err != nil {
		t.Fatal(err)
	}
	routes := []systemnats.UpgradeRoute{
		{Current: currentRoutes[1], Candidate: candidateRoutes[1]},
		{Current: currentRoutes[0], Candidate: candidateRoutes[0]},
	}
	if _, err := deployments[2].RollbackFailedUpgrade(ctx, transports[2], pending, routes, systemnats.RolloutFailure{}); !errors.Is(err, systemnats.ErrUpgradeInvalid) {
		t.Errorf("empty rollback failure error = %v; want %v", err, systemnats.ErrUpgradeInvalid)
	}
	illegal := pending
	illegal.Generation++
	illegal.RolloutID = "upgrade.skipped-rollback"
	illegal.Phase = systemnats.RolloutRolledBack
	illegal.Failure = &systemnats.RolloutFailure{Code: "candidate_startup_failed", Message: "Inventory failed"}
	illegal.Nodes = append([]systemnats.RolloutNodeProgress(nil), pending.Nodes...)
	for i := range illegal.Nodes {
		illegal.Nodes[i].Phase = systemnats.RolloutRolledBack
	}
	if err := deployments[0].PutRollout(ctx, transports[0], illegal); !errors.Is(err, systemnats.ErrRolloutInvalid) {
		t.Errorf("skipped rollback phases error = %v; want %v", err, systemnats.ErrRolloutInvalid)
	}
	if _, err := placements[0].Replace(ctx, transports[0], currentRoutes[0], candidateRoutes[0]); err != nil {
		t.Fatal(err)
	}
	partiallySwitched := []systemnats.PlacementRecord{candidateRoutes[0], currentRoutes[1]}
	if _, err := waitForPlacementViews(ctx, transports, []string{"node-1", "node-2", "node-3"}, partiallySwitched); err != nil {
		t.Fatal(err)
	}
	failure := systemnats.RolloutFailure{
		Code: "candidate_startup_failed", Component: "Inventory",
		Field: "inventory.reservation_buffer", Message: "must be zero or greater",
	}
	rolledBack, err := deployments[2].RollbackFailedUpgrade(ctx, transports[2], pending, routes, failure)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Phase != systemnats.RolloutRolledBack || rolledBack.Generation != 5 || rolledBack.CurrentArtifactDigest != currentArtifact.ArtifactDigest || rolledBack.CandidateArtifactDigest != candidateArtifact.ArtifactDigest || rolledBack.Failure == nil || *rolledBack.Failure != failure {
		t.Errorf("rolled-back rollout = %#v", rolledBack)
	}
	if _, err := waitForPlacementViews(ctx, transports, []string{"node-1", "node-2", "node-3"}, currentRoutes); err != nil {
		t.Fatal(err)
	}
	artifacts := []systemnats.DeploymentArtifact{currentArtifact, candidateArtifact}
	slices.SortFunc(artifacts, func(a, b systemnats.DeploymentArtifact) int {
		if a.ArtifactDigest < b.ArtifactDigest {
			return -1
		}
		if a.ArtifactDigest > b.ArtifactDigest {
			return 1
		}
		return 0
	})
	if err := waitForDeployments(ctx, transports, artifacts, []systemnats.Rollout{rolledBack}); err != nil {
		t.Fatal(err)
	}
}

func runUpgradeViews(
	t *testing.T,
	ctx context.Context,
	transports []*systemnats.Transport,
	initial []systemnats.PlacementRecord,
) ([]*systemnats.Placement, []*systemnats.Deployments) {
	t.Helper()
	placements := make([]*systemnats.Placement, len(transports))
	deployments := make([]*systemnats.Deployments, len(transports))
	runCtx, cancelRun := context.WithCancel(t.Context())
	var runs sync.WaitGroup
	for i := range transports {
		var records []systemnats.PlacementRecord
		if i == 0 {
			records = initial
		}
		placement, err := systemnats.NewPlacement(records)
		if err != nil {
			t.Fatal(err)
		}
		placements[i] = placement
		deployments[i] = systemnats.NewDeployments()
		nodeID := fmt.Sprintf("node-%d", i+1)
		if err := transports[i].ServePlacement(ctx, nodeID, placement); err != nil {
			t.Fatal(err)
		}
		if err := transports[i].ServeDeployments(ctx, nodeID, deployments[i]); err != nil {
			t.Fatal(err)
		}
		runs.Add(2)
		go func() {
			defer runs.Done()
			if err := placement.Run(runCtx, transports[i]); err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("placement node-%d: %v", i+1, err)
			}
		}()
		go func() {
			defer runs.Done()
			if err := deployments[i].Run(runCtx, transports[i]); err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("deployments node-%d: %v", i+1, err)
			}
		}()
	}
	t.Cleanup(func() { cancelRun(); runs.Wait() })
	return placements, deployments
}

func upgradeRollout(generation uint64, rolloutID string, phase systemnats.RolloutPhase, current, candidate string, nodeIDs ...string) systemnats.Rollout {
	nodes := make([]systemnats.RolloutNodeProgress, len(nodeIDs))
	for i, nodeID := range nodeIDs {
		nodes[i] = systemnats.RolloutNodeProgress{
			NodeID: nodeID, CurrentArtifactDigest: current, CandidateArtifactDigest: candidate, Phase: phase,
		}
	}
	return systemnats.Rollout{
		ApplicationID: "grove-shop", ClusterID: "production", RolloutID: rolloutID,
		Generation: generation, CurrentArtifactDigest: current, CandidateArtifactDigest: candidate,
		Phase: phase, Nodes: nodes,
	}
}
