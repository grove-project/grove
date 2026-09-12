package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/systemnats"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// A config-only N to N+1 rollout remains metadata-only in Task 025 while its
// exact current/candidate identities converge through replicated control state.
func TestConfiguredArtifactRolloutIdentityConverges(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	directory := t.TempDir()
	currentConfig := directory + "/current.yaml"
	candidateConfig := directory + "/candidate.yaml"
	currentPath := directory + "/grove-shop-current"
	candidatePath := directory + "/grove-shop-candidate"
	writeConfigFile(t, currentConfig, "acme-r42", "cloud", 2)
	writeConfigFile(t, candidateConfig, "acme-r43", "cloud", 3)
	if output, err := runGroveCommand(ctx, "config", "embed", "--binary", grovletPath, "--config", currentConfig, "--output", currentPath); err != nil {
		t.Fatalf("embed current artifact: %v; output=%q", err, output)
	}
	if output, err := runGroveCommand(ctx, "config", "embed", "--binary", grovletPath, "--config", candidateConfig, "--output", candidatePath); err != nil {
		t.Fatalf("embed candidate artifact: %v; output=%q", err, output)
	}
	currentInspection, err := artifact.InspectFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	candidateInspection, err := artifact.InspectFile(candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	if currentInspection.CodeDigest != candidateInspection.CodeDigest || currentInspection.Config.Digest == candidateInspection.Config.Digest || currentInspection.ArtifactDigest == candidateInspection.ArtifactDigest {
		t.Fatalf("config-only artifact identities = current %#v, candidate %#v", currentInspection, candidateInspection)
	}

	nodes, systemNATSURL := startGrovletsFromArtifact(t, ctx, currentPath)
	defer stopGrovlets(t, nodes)
	runningInspection, err := artifact.InspectFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if runningInspection.ArtifactDigest != currentInspection.ArtifactDigest {
		t.Fatalf("current artifact changed during launch: before=%s after=%s", currentInspection.ArtifactDigest, runningInspection.ArtifactDigest)
	}
	transport, err := systemnats.Connect(ctx, systemNATSURL)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	if err := waitForDeploymentControlViews(ctx, transport, []systemnats.DeploymentArtifact{}, []systemnats.Rollout{}); err != nil {
		t.Fatalf("wait for empty deployment state: %v\n%s", err, grovletLogs(nodes))
	}
	if err := waitForDesiredDeployments(ctx, transport, "node-3", []systemnats.DesiredDeployment{}); err != nil {
		t.Fatalf("wait for empty desired state: %v\n%s", err, grovletLogs(nodes))
	}
	if err := waitForObservedServices(ctx, transport, "node-3", groveshop.ServiceOrders, groveshop.ServiceInventory); err != nil {
		t.Fatalf("wait for current placement: %v\n%s", err, grovletLogs(nodes))
	}
	placement, err := transport.RequestPlacement(ctx, "node-3")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range placement.Placements {
		if record.ArtifactDigest != currentInspection.ArtifactDigest {
			t.Errorf("service %d placement artifact = %q; want current %q\n%s", record.ServiceID, record.ArtifactDigest, currentInspection.ArtifactDigest, grovletLogs(nodes))
		}
	}

	desired := systemnats.DesiredDeployment{
		ApplicationID:  currentInspection.Manifest.ApplicationID,
		Version:        currentInspection.Manifest.CodeVersion,
		ArtifactDigest: currentInspection.ArtifactDigest,
		Components: []systemnats.DesiredComponent{
			{ServiceID: groveshop.ServiceOrders, NodeID: "node-1"},
			{ServiceID: groveshop.ServiceInventory, NodeID: "node-2"},
		},
	}
	if err := transport.PutDesired(ctx, "node-1", desired); err != nil {
		t.Fatal(err)
	}
	if err := waitForDesiredDeployments(ctx, transport, "node-3", []systemnats.DesiredDeployment{desired}); err != nil {
		t.Fatalf("wait for desired artifact identity: %v\n%s", err, grovletLogs(nodes))
	}

	current := artifactControlRecord(currentInspection)
	candidate := artifactControlRecord(candidateInspection)
	if err := transport.PutDeploymentArtifact(ctx, "node-1", current); err != nil {
		t.Fatal(err)
	}
	first := rolloutRecord(1, current.ArtifactDigest, "", systemnats.RolloutActive)
	if err := transport.PutRollout(ctx, "node-2", first); err != nil {
		t.Fatal(err)
	}
	if err := waitForDeploymentControlViews(ctx, transport, []systemnats.DeploymentArtifact{current}, []systemnats.Rollout{first}); err != nil {
		t.Fatalf("wait for current rollout: %v\n%s", err, grovletLogs(nodes))
	}
	if err := transport.PutDeploymentArtifact(ctx, "node-2", candidate); err != nil {
		t.Fatal(err)
	}
	second := rolloutRecord(2, current.ArtifactDigest, candidate.ArtifactDigest, systemnats.RolloutPending)
	if err := transport.PutRollout(ctx, "node-3", second); err != nil {
		t.Fatal(err)
	}
	wantArtifacts := []systemnats.DeploymentArtifact{current, candidate}
	slices.SortFunc(wantArtifacts, func(a, b systemnats.DeploymentArtifact) int {
		return strings.Compare(a.ArtifactDigest, b.ArtifactDigest)
	})
	if err := waitForDeploymentControlViews(ctx, transport, wantArtifacts, []systemnats.Rollout{second}); err != nil {
		t.Fatalf("wait for candidate rollout: %v\n%s", err, grovletLogs(nodes))
	}

	connection, err := nats.Connect(systemNATSURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
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
		t.Errorf("deployment state replicas = %d; want %d", status.Config().Replicas, systemnats.DeploymentReplicas)
	}
	for _, record := range wantArtifacts {
		key, err := systemnats.DeploymentArtifactKey(record.ArtifactDigest)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := kv.Get(ctx, key); err != nil {
			t.Errorf("read persisted artifact %q: %v", record.ArtifactDigest, err)
		}
	}
	rolloutKey, err := systemnats.DeploymentRolloutKey("grove-shop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Get(ctx, rolloutKey); err != nil {
		t.Errorf("read persisted rollout: %v", err)
	}
}

func artifactControlRecord(inspection artifact.Inspection) systemnats.DeploymentArtifact {
	return systemnats.DeploymentArtifact{
		ApplicationID:  inspection.Manifest.ApplicationID,
		CodeVersion:    inspection.Manifest.CodeVersion,
		CodeDigest:     inspection.CodeDigest,
		ConfigRevision: inspection.Config.Revision,
		ConfigDigest:   inspection.Config.Digest,
		ArtifactDigest: inspection.ArtifactDigest,
		ClusterID:      inspection.Config.Facts["cluster.name"],
		NodeZone:       inspection.Config.Facts["node.zone"],
	}
}

func rolloutRecord(generation uint64, current, candidate string, phase systemnats.RolloutPhase) systemnats.Rollout {
	nodes := make([]systemnats.RolloutNodeProgress, 3)
	for i := range nodes {
		nodes[i] = systemnats.RolloutNodeProgress{
			NodeID:                  fmt.Sprintf("node-%d", i+1),
			CurrentArtifactDigest:   current,
			CandidateArtifactDigest: candidate,
			Phase:                   phase,
		}
	}
	return systemnats.Rollout{
		ApplicationID:           "grove-shop",
		ClusterID:               "production",
		RolloutID:               fmt.Sprintf("grove-shop-%d", generation),
		Generation:              generation,
		CurrentArtifactDigest:   current,
		CandidateArtifactDigest: candidate,
		Phase:                   phase,
		Nodes:                   nodes,
	}
}

func waitForDeploymentControlViews(ctx context.Context, transport *systemnats.Transport, artifacts []systemnats.DeploymentArtifact, rollouts []systemnats.Rollout) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	views := make([]systemnats.DeploymentView, 3)
	var lastErr error
	for {
		converged := true
		for i := range views {
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
			return fmt.Errorf("deployment control views did not converge: views=%#v: %w", views, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func waitForDesiredDeployments(ctx context.Context, transport *systemnats.Transport, nodeID string, want []systemnats.DesiredDeployment) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var last systemnats.DesiredView
	var lastErr error
	for {
		requestCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		view, err := transport.RequestDesired(requestCtx, nodeID)
		cancel()
		if err == nil {
			last = view
			if view.Ready && reflect.DeepEqual(view.Deployments, want) {
				return nil
			}
		} else {
			lastErr = err
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("desired artifact did not converge: view=%#v: %w", last, errors.Join(lastErr, ctx.Err()))
		}
	}
}
