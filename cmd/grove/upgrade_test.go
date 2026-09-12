package main

import (
	"context"
	"errors"
	"fmt"
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

// N remains authoritative while N+1 starts and proves healthy. The durable
// active-artifact and placement switch commits before N's workers retire.
func TestHealthyCandidateTakesOwnership(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	directory := t.TempDir()
	currentConfig := directory + "/current.yaml"
	candidateConfig := directory + "/candidate.yaml"
	currentPath := directory + "/grove-shop-current"
	candidatePath := directory + "/grove-shop-candidate"
	writeConfigFile(t, currentConfig, "acme-r42", "cloud", 3)
	writeConfigFile(t, candidateConfig, "acme-r43", "cloud", 1)
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
	if _, err := negotiateBootstrapTargets(ctx, currentPath, candidatePath); err != nil {
		t.Fatal(err)
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
	if len(currentRoutes) != 2 {
		t.Fatalf("current routes = %#v; want Orders and Inventory", currentRoutes)
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
		t.Fatalf("wait for pending rollout: %v\n%s", err, grovletLogs(currentNodes))
	}

	suffix := strings.TrimPrefix(candidate.ArtifactDigest, "sha256:")[:12]
	candidateInventorySubject := "_GROVE.system.upgrade." + suffix + ".inventory"
	candidateOrdersSubject := "_GROVE.system.upgrade." + suffix + ".orders"
	candidateInventory := startUpgradeCandidate(t, ctx, candidatePath,
		"node-2-candidate", systemNATSURL, candidateInventorySubject,
		"--grove-shop-inventory",
	)
	candidateOrders := startUpgradeCandidate(t, ctx, candidatePath,
		"node-1-candidate", systemNATSURL, candidateOrdersSubject,
		"--grove-shop-orders-inventory-subject", candidateInventorySubject,
	)
	defer stopCandidateNodes(t, candidateOrders, candidateInventory)

	client, err := transport.ObservedPlacementClient("node-3")
	if err != nil {
		t.Fatal(err)
	}
	before, err := grove.Call[groveshop.CreateOrderRequest, groveshop.Order](ctx, client, groveshop.ServiceOrders, groveshop.MethodCreateOrder, groveshop.CreateOrderRequest{
		OrderID: "order-before-upgrade", SKU: "coffee-beans", Quantity: 2, AmountCents: 2400, ShippingAddress: "27 Grove Lane",
	})
	if err != nil || before.Status != groveshop.OrderCompleted {
		t.Fatalf("invoke N before cutover = %#v, %v\n%s", before, err, grovletLogs(currentNodes))
	}

	candidateRoutes := []systemnats.PlacementRecord{
		{ServiceID: groveshop.ServiceOrders, NodeID: "node-1-candidate", InvocationSubject: candidateOrdersSubject, ArtifactDigest: candidate.ArtifactDigest},
		{ServiceID: groveshop.ServiceInventory, NodeID: "node-2-candidate", InvocationSubject: candidateInventorySubject, ArtifactDigest: candidate.ArtifactDigest},
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
	committed, err := systemnats.NewDeployments().CommitHealthyUpgrade(ctx, transport, pending, routes)
	if err != nil {
		t.Fatalf("commit healthy upgrade: %v; candidate Orders=%q Inventory=%q\n%s", err, candidateOrders.Logs(), candidateInventory.Logs(), grovletLogs(currentNodes))
	}
	if committed.Phase != systemnats.RolloutActive || committed.CurrentArtifactDigest != candidate.ArtifactDigest || committed.CandidateArtifactDigest != "" {
		t.Fatalf("committed rollout = %#v", committed)
	}
	if err := waitForDeploymentControlViews(ctx, transport, wantArtifacts, []systemnats.Rollout{committed}); err != nil {
		t.Fatalf("wait for active candidate: %v\n%s", err, grovletLogs(currentNodes))
	}
	if err := waitForPlacementRecords(ctx, transport, "node-3", candidateRoutes); err != nil {
		t.Fatalf("wait for candidate placement: %v\n%s", err, grovletLogs(currentNodes))
	}

	if view, err := transport.RequestStopComponent(ctx, "node-1", groveshop.ServiceOrders); err != nil || componentState(view, groveshop.ServiceOrders) != systemnats.ComponentStopped {
		t.Fatalf("retire current Orders = %#v, %v", view, err)
	}
	if view, err := transport.RequestStopComponent(ctx, "node-2", groveshop.ServiceInventory); err != nil || componentState(view, groveshop.ServiceInventory) != systemnats.ComponentStopped {
		t.Fatalf("retire current Inventory = %#v, %v", view, err)
	}

	_, err = grove.Call[groveshop.CreateOrderRequest, groveshop.Order](ctx, client, groveshop.ServiceOrders, groveshop.MethodCreateOrder, groveshop.CreateOrderRequest{
		OrderID: "order-after-upgrade-over-buffer", SKU: "coffee-beans", Quantity: 2, AmountCents: 2400, ShippingAddress: "27 Grove Lane",
	})
	if err == nil || !strings.Contains(err.Error(), groveshop.ErrReservationBufferExceeded.Error()) {
		t.Errorf("N+1 over-buffer error = %v; want %v", err, groveshop.ErrReservationBufferExceeded)
	}
	after, err := grove.Call[groveshop.CreateOrderRequest, groveshop.Order](ctx, client, groveshop.ServiceOrders, groveshop.MethodCreateOrder, groveshop.CreateOrderRequest{
		OrderID: "order-after-upgrade", SKU: "coffee-beans", Quantity: 1, AmountCents: 1200, ShippingAddress: "27 Grove Lane",
	})
	if err != nil || after.Status != groveshop.OrderCompleted {
		t.Fatalf("invoke N+1 after cutover = %#v, %v; candidate Orders=%q Inventory=%q", after, err, candidateOrders.Logs(), candidateInventory.Logs())
	}
}

func startUpgradeCandidate(t *testing.T, ctx context.Context, path, nodeID, systemNATSURL, subject string, args ...string) *grovetest.Node {
	t.Helper()
	nodeArgs := []string{
		"--node-id", nodeID,
		"--advertise-endpoint", "nats-subject://system/" + nodeID,
		"--system-nats-url", systemNATSURL,
		"--system-nats-subject", subject,
	}
	node, err := grovetest.StartNode(path, append(nodeArgs, args...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Cleanup() })
	if err := node.WaitReady(ctx); err != nil {
		t.Fatalf("wait for candidate %s: %v; logs=%q", nodeID, err, node.Logs())
	}
	return node
}

func waitForPlacementRecords(ctx context.Context, transport *systemnats.Transport, nodeID string, want []systemnats.PlacementRecord) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var last systemnats.PlacementView
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		view, err := transport.RequestPlacement(attemptCtx, nodeID)
		cancel()
		if err == nil {
			last = view
			if view.Ready && slices.Equal(view.Placements, want) {
				return nil
			}
		} else {
			lastErr = err
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("placement did not converge: view=%#v: %w", last, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func componentState(view systemnats.ComponentView, serviceID grove.ServiceID) systemnats.ComponentState {
	for _, component := range view.Components {
		if component.ServiceID == serviceID {
			return component.State
		}
	}
	return ""
}
