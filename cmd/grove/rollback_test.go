package main

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/grovetest"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/systemnats"
)

// An integrity-valid candidate with semantically invalid Inventory
// configuration is rejected at startup. Its failure remains observable while
// the retained complete current artifact continues serving.
func TestBrokenInventoryCandidateRollsBack(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	directory := t.TempDir()
	currentConfig := directory + "/current.yaml"
	currentPath := directory + "/grove-shop-current"
	candidatePath := directory + "/grove-shop-broken"
	writeConfigFile(t, currentConfig, "acme-r42", "cloud", 3)
	if output, err := runGroveCommand(ctx, "config", "embed", "--binary", grovletPath, "--config", currentConfig, "--output", currentPath); err != nil {
		t.Fatalf("embed current artifact: %v; output=%q", err, output)
	}
	currentInspection, err := artifact.InspectFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	candidateInspection := embedBrokenInventoryArtifact(t, candidatePath)
	if currentInspection.CodeDigest != candidateInspection.CodeDigest || currentInspection.Config.Digest == candidateInspection.Config.Digest || currentInspection.ArtifactDigest == candidateInspection.ArtifactDigest {
		t.Fatalf("rollback artifact identities = current %#v, candidate %#v", currentInspection, candidateInspection)
	}
	if _, err := negotiateBootstrapTargets(ctx, currentPath, candidatePath); err != nil {
		t.Fatalf("forward bootstrap compatibility: %v", err)
	}
	if _, err := negotiateBootstrapTargets(ctx, candidatePath, currentPath); err != nil {
		t.Fatalf("reverse bootstrap compatibility: %v", err)
	}

	currentNodes, systemNATSURL := startGrovletsFromArtifact(t, ctx, currentPath)
	defer stopGrovlets(t, currentNodes)
	transport, err := systemnats.Connect(ctx, systemNATSURL)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	if err := waitForDeploymentControlViews(ctx, transport, []systemnats.DeploymentArtifact{}, []systemnats.Rollout{}); err != nil {
		t.Fatalf("wait for empty deployment state: %v\n%s", err, grovletLogs(currentNodes))
	}
	if err := waitForObservedServices(ctx, transport, "node-3", groveshop.ServiceOrders, groveshop.ServiceInventory); err != nil {
		t.Fatalf("wait for current placement: %v\n%s", err, grovletLogs(currentNodes))
	}
	placement, err := transport.RequestPlacement(ctx, "node-3")
	if err != nil {
		t.Fatal(err)
	}
	currentRoutes := placement.Placements
	client, err := transport.ObservedPlacementClient("node-3")
	if err != nil {
		t.Fatal(err)
	}
	before, err := createRollbackOrder(ctx, client, "order-before-rollback")
	if err != nil || before.Status != groveshop.OrderCompleted {
		t.Fatalf("order before rollback = %#v, %v\n%s", before, err, grovletLogs(currentNodes))
	}

	current := artifactControlRecord(currentInspection)
	candidate := artifactControlRecord(candidateInspection)
	for _, record := range []systemnats.DeploymentArtifact{current, candidate} {
		if err := transport.PutDeploymentArtifact(ctx, "node-1", record); err != nil {
			t.Fatal(err)
		}
	}
	active := rolloutRecord(1, current.ArtifactDigest, "", systemnats.RolloutActive)
	if err := transport.PutRollout(ctx, "node-2", active); err != nil {
		t.Fatal(err)
	}
	pending := rolloutRecord(2, current.ArtifactDigest, candidate.ArtifactDigest, systemnats.RolloutPending)
	if err := transport.PutRollout(ctx, "node-3", pending); err != nil {
		t.Fatal(err)
	}
	wantArtifacts := []systemnats.DeploymentArtifact{current, candidate}
	slices.SortFunc(wantArtifacts, func(a, b systemnats.DeploymentArtifact) int {
		return strings.Compare(a.ArtifactDigest, b.ArtifactDigest)
	})
	if err := waitForDeploymentControlViews(ctx, transport, wantArtifacts, []systemnats.Rollout{pending}); err != nil {
		t.Fatalf("wait for broken candidate: %v\n%s", err, grovletLogs(currentNodes))
	}

	candidateNode, err := grovetest.StartNode(
		candidatePath,
		"--node-id", "node-2-candidate",
		"--advertise-endpoint", "nats-subject://system/node-2-candidate",
		"--system-nats-url", systemNATSURL,
		"--system-nats-subject", "_GROVE.system.rollback.candidate.inventory",
		"--grove-shop-inventory",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = candidateNode.Cleanup() })
	failureCtx, failureCancel := context.WithTimeout(ctx, 5*time.Second)
	err = candidateNode.WaitReady(failureCtx)
	failureCancel()
	if err == nil {
		t.Fatal("broken Inventory candidate became ready")
	}
	candidateLogs := candidateNode.Logs()
	if !strings.Contains(candidateLogs, groveshop.ErrConfigurationInvalid.Error()) || !strings.Contains(candidateLogs, "inventory.reservation_buffer") || !strings.Contains(candidateLogs, "must be zero or greater") {
		t.Fatalf("broken Inventory failure = %v; logs=%q", err, candidateLogs)
	}

	candidateRoutes := []systemnats.PlacementRecord{
		{ServiceID: groveshop.ServiceOrders, NodeID: "node-1-candidate", InvocationSubject: "_GROVE.system.rollback.candidate.orders", ArtifactDigest: candidate.ArtifactDigest},
		{ServiceID: groveshop.ServiceInventory, NodeID: "node-2-candidate", InvocationSubject: "_GROVE.system.rollback.candidate.inventory", ArtifactDigest: candidate.ArtifactDigest},
	}
	routes := make([]systemnats.UpgradeRoute, len(currentRoutes))
	for i, currentRoute := range currentRoutes {
		candidateIndex := slices.IndexFunc(candidateRoutes, func(route systemnats.PlacementRecord) bool {
			return route.ServiceID == currentRoute.ServiceID
		})
		if candidateIndex < 0 {
			t.Fatalf("candidate route missing for service %d", currentRoute.ServiceID)
		}
		routes[i] = systemnats.UpgradeRoute{Current: currentRoute, Candidate: candidateRoutes[candidateIndex]}
	}
	failure := systemnats.RolloutFailure{
		Code:      "candidate_startup_failed",
		Component: "Inventory",
		Field:     "inventory.reservation_buffer",
		Message:   "must be zero or greater",
	}
	rolledBack, err := systemnats.NewDeployments().RollbackFailedUpgrade(ctx, transport, pending, routes, failure)
	if err != nil {
		t.Fatalf("rollback broken candidate: %v\n%s", err, grovletLogs(currentNodes))
	}
	if rolledBack.Phase != systemnats.RolloutRolledBack || rolledBack.CurrentArtifactDigest != current.ArtifactDigest || rolledBack.CandidateArtifactDigest != candidate.ArtifactDigest || rolledBack.Failure == nil || *rolledBack.Failure != failure {
		t.Fatalf("rolled-back control state = %#v", rolledBack)
	}
	if err := waitForDeploymentControlViews(ctx, transport, wantArtifacts, []systemnats.Rollout{rolledBack}); err != nil {
		t.Fatalf("wait for durable rollback: %v\n%s", err, grovletLogs(currentNodes))
	}
	if err := waitForPlacementRecords(ctx, transport, "node-3", currentRoutes); err != nil {
		t.Fatalf("wait for known-good placement: %v\n%s", err, grovletLogs(currentNodes))
	}
	if current.ConfigRevision != "acme-r42" || current.ConfigDigest != currentInspection.Config.Digest || rolledBack.CurrentArtifactDigest != currentInspection.ArtifactDigest {
		t.Errorf("active known-good config identity = artifact %s config %s/%s", rolledBack.CurrentArtifactDigest, current.ConfigRevision, current.ConfigDigest)
	}
	after, err := createRollbackOrder(ctx, client, "order-after-rollback")
	if err != nil || after.Status != groveshop.OrderCompleted {
		t.Fatalf("order after rollback = %#v, %v\n%s", after, err, grovletLogs(currentNodes))
	}
}

func embedBrokenInventoryArtifact(t *testing.T, output string) artifact.Inspection {
	t.Helper()
	configuration := groveshop.DefaultConfiguration()
	configuration.Revision = "acme-broken-r43"
	configuration.Customer.Name = "Acme Retail"
	configuration.Cluster.Name = "production"
	configuration.Node.Zone = "cloud"
	configuration.Inventory.ReservationBuffer = -1
	payload, err := grove.Encode(configuration)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := artifact.EmbedFile(grovletPath, output, artifact.Compilation{
		ProtocolVersion: artifact.CompilerProtocolVersion,
		Revision:        configuration.Revision,
		Encoding:        "gob",
		Payload:         payload,
		CanonicalYAML:   []byte("revision: acme-broken-r43\ncustomer:\n  name: Acme Retail\ncluster:\n  name: production\nnode:\n  zone: cloud\ninventory:\n  reservation_buffer: -1\n"),
		Facts: map[string]string{
			"cluster.name": configuration.Cluster.Name,
			"node.zone":    configuration.Node.Zone,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return inspection
}

func createRollbackOrder(ctx context.Context, client *grove.Client, orderID string) (groveshop.Order, error) {
	return grove.Call[groveshop.CreateOrderRequest, groveshop.Order](ctx, client, groveshop.ServiceOrders, groveshop.MethodCreateOrder, groveshop.CreateOrderRequest{
		OrderID: orderID, SKU: "coffee-beans", Quantity: 2, AmountCents: 2400, ShippingAddress: "28 Grove Lane",
	})
}
