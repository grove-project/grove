package systemnats_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grove-project/grove/internal/systemnats"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func TestDeploymentStateConvergesThroughControlAPI(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	servers, transports := startPlacementCluster(t, ctx)
	deployments := make([]*systemnats.Deployments, len(transports))
	runCtx, cancelRun := context.WithCancel(t.Context())
	var runs sync.WaitGroup
	for i := range transports {
		deployments[i] = systemnats.NewDeployments()
		nodeID := fmt.Sprintf("node-%d", i+1)
		if err := transports[i].ServeDeployments(ctx, nodeID, deployments[i]); err != nil {
			t.Fatal(err)
		}
		runs.Add(1)
		go func() {
			defer runs.Done()
			if err := deployments[i].Run(runCtx, transports[i]); err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("deployment observer: %v", err)
			}
		}()
	}
	t.Cleanup(func() { cancelRun(); runs.Wait() })
	if err := waitForDeployments(ctx, transports, []systemnats.DeploymentArtifact{}, []systemnats.Rollout{}); err != nil {
		t.Fatal(err)
	}

	current := deploymentArtifact("a", "b", "c", "r42", "cloud")
	candidate := deploymentArtifact("a", "d", "e", "r43", "edge")
	if err := transports[2].PutDeploymentArtifact(ctx, "node-1", current); err != nil {
		t.Fatal(err)
	}
	if err := transports[0].PutDeploymentArtifact(ctx, "node-2", candidate); err != nil {
		t.Fatal(err)
	}
	if err := transports[1].PutDeploymentArtifact(ctx, "node-3", current); err != nil {
		t.Fatalf("idempotent artifact write: %v", err)
	}
	changed := current
	changed.CodeVersion = "changed"
	if err := deployments[0].PutArtifact(ctx, transports[0], changed); !errors.Is(err, systemnats.ErrDeploymentArtifactChanged) {
		t.Errorf("changed artifact metadata error = %v; want %v", err, systemnats.ErrDeploymentArtifactChanged)
	}

	first := systemnats.Rollout{
		ApplicationID:         "grove-shop",
		ClusterID:             "production",
		RolloutID:             "grove-shop-1",
		Generation:            1,
		CurrentArtifactDigest: current.ArtifactDigest,
		Phase:                 systemnats.RolloutActive,
		Nodes: []systemnats.RolloutNodeProgress{
			{NodeID: "node-1", CurrentArtifactDigest: current.ArtifactDigest, Phase: systemnats.RolloutActive},
			{NodeID: "node-2", CurrentArtifactDigest: current.ArtifactDigest, Phase: systemnats.RolloutActive},
			{NodeID: "node-3", CurrentArtifactDigest: current.ArtifactDigest, Phase: systemnats.RolloutActive},
		},
	}
	if err := transports[2].PutRollout(ctx, "node-1", first); err != nil {
		t.Fatal(err)
	}
	second := systemnats.Rollout{
		ApplicationID:           "grove-shop",
		ClusterID:               "production",
		RolloutID:               "grove-shop-2",
		Generation:              2,
		CurrentArtifactDigest:   current.ArtifactDigest,
		CandidateArtifactDigest: candidate.ArtifactDigest,
		Phase:                   systemnats.RolloutPending,
		Nodes: []systemnats.RolloutNodeProgress{
			{NodeID: "node-3", CurrentArtifactDigest: current.ArtifactDigest, CandidateArtifactDigest: candidate.ArtifactDigest, Phase: systemnats.RolloutPending},
			{NodeID: "node-1", CurrentArtifactDigest: current.ArtifactDigest, CandidateArtifactDigest: candidate.ArtifactDigest, Phase: systemnats.RolloutPending},
			{NodeID: "node-2", CurrentArtifactDigest: current.ArtifactDigest, CandidateArtifactDigest: candidate.ArtifactDigest, Phase: systemnats.RolloutPending},
		},
	}
	if err := transports[0].PutRollout(ctx, "node-2", second); err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(second.Nodes, func(a, b systemnats.RolloutNodeProgress) int { return strings.Compare(a.NodeID, b.NodeID) })
	artifacts := []systemnats.DeploymentArtifact{current, candidate}
	slices.SortFunc(artifacts, func(a, b systemnats.DeploymentArtifact) int {
		return strings.Compare(a.ArtifactDigest, b.ArtifactDigest)
	})
	if err := waitForDeployments(ctx, transports, artifacts, []systemnats.Rollout{second}); err != nil {
		t.Fatal(err)
	}
	skippedHealth := second
	skippedHealth.Generation = 3
	skippedHealth.RolloutID = "grove-shop-skipped-health"
	skippedHealth.CurrentArtifactDigest = candidate.ArtifactDigest
	skippedHealth.CandidateArtifactDigest = ""
	skippedHealth.Phase = systemnats.RolloutActive
	skippedHealth.Nodes = append([]systemnats.RolloutNodeProgress(nil), second.Nodes...)
	for i := range skippedHealth.Nodes {
		skippedHealth.Nodes[i].CurrentArtifactDigest = candidate.ArtifactDigest
		skippedHealth.Nodes[i].CandidateArtifactDigest = ""
		skippedHealth.Nodes[i].Phase = systemnats.RolloutActive
	}
	if err := deployments[0].PutRollout(ctx, transports[0], skippedHealth); !errors.Is(err, systemnats.ErrRolloutInvalid) {
		t.Errorf("skipped candidate health error = %v; want %v", err, systemnats.ErrRolloutInvalid)
	}

	stale := second
	stale.RolloutID = "stale"
	if err := deployments[0].PutRollout(ctx, transports[0], stale); !errors.Is(err, systemnats.ErrRolloutGeneration) {
		t.Errorf("stale rollout error = %v; want %v", err, systemnats.ErrRolloutGeneration)
	}
	changedCluster := second
	changedCluster.Generation = 3
	changedCluster.RolloutID = "grove-shop-3"
	changedCluster.ClusterID = "other"
	if err := deployments[0].PutRollout(ctx, transports[0], changedCluster); !errors.Is(err, systemnats.ErrRolloutInvalid) {
		t.Errorf("changed rollout cluster error = %v; want %v", err, systemnats.ErrRolloutInvalid)
	}
	connection, err := nats.Connect(servers[0].URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	kv, err := js.KeyValue(ctx, systemnats.DeploymentBucket)
	if err != nil {
		t.Fatal(err)
	}
	status, err := kv.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Config().Replicas != systemnats.DeploymentReplicas {
		t.Errorf("deployment replicas = %d; want %d", status.Config().Replicas, systemnats.DeploymentReplicas)
	}
}

func TestDeploymentStateRejectsInvalidRecords(t *testing.T) {
	invalidArtifacts := []systemnats.DeploymentArtifact{
		{},
		deploymentArtifact("not-a-digest", "b", "c", "r1", "cloud"),
	}
	for _, artifact := range invalidArtifacts {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := systemnats.NewDeployments().PutArtifact(ctx, nil, artifact); !errors.Is(err, systemnats.ErrDeploymentArtifactInvalid) {
			t.Errorf("PutArtifact(%#v) error = %v; want %v", artifact, err, systemnats.ErrDeploymentArtifactInvalid)
		}
	}
	invalidRollout := systemnats.Rollout{ApplicationID: "grove-shop", ClusterID: "production", RolloutID: "r1", Generation: 1, CurrentArtifactDigest: testDigest("a"), Phase: systemnats.RolloutActive}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := systemnats.NewDeployments().PutRollout(ctx, nil, invalidRollout); !errors.Is(err, systemnats.ErrRolloutInvalid) {
		t.Errorf("PutRollout(%#v) error = %v; want %v", invalidRollout, err, systemnats.ErrRolloutInvalid)
	}
}

func deploymentArtifact(code, config, artifact, revision, zone string) systemnats.DeploymentArtifact {
	codeDigest := code
	if len(code) == 1 {
		codeDigest = testDigest(code)
	}
	return systemnats.DeploymentArtifact{
		ApplicationID:  "grove-shop",
		CodeVersion:    "v1",
		CodeDigest:     codeDigest,
		ConfigRevision: revision,
		ConfigDigest:   testDigest(config),
		ArtifactDigest: testDigest(artifact),
		ClusterID:      "production",
		NodeZone:       zone,
	}
}

func testDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}

func waitForDeployments(ctx context.Context, transports []*systemnats.Transport, artifacts []systemnats.DeploymentArtifact, rollouts []systemnats.Rollout) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	views := make([]systemnats.DeploymentView, len(transports))
	var lastErr error
	for {
		converged := true
		for i, transport := range transports {
			requestCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
			view, err := transport.RequestDeployments(requestCtx, fmt.Sprintf("node-%d", i+1))
			cancel()
			if err != nil {
				lastErr = err
				converged = false
				continue
			}
			views[i] = view
			if !view.Ready || !reflect.DeepEqual(view.Artifacts, artifacts) || !reflect.DeepEqual(view.Rollouts, rollouts) {
				converged = false
			}
		}
		if converged {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("deployment views did not converge: views=%#v: %w", views, errors.Join(lastErr, ctx.Err()))
		}
	}
}
