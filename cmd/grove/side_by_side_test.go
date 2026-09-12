package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/grovetest"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/bootstrap"
	"github.com/grove-project/grove/internal/systemnats"
)

// Current placement remains authoritative while separately addressed
// candidate processes join the same mesh and serve the N+1 artifact.
func TestCurrentAndCandidateRunSideBySide(t *testing.T) {
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
	negotiation, err := negotiateBootstrapTargets(ctx, currentPath, candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	if negotiation.BootstrapVersion != bootstrap.BootstrapVersion || negotiation.Current.ArtifactDigest != currentInspection.ArtifactDigest || negotiation.Candidate.ArtifactDigest != candidateInspection.ArtifactDigest {
		t.Fatalf("bootstrap negotiation = %#v", negotiation)
	}
	if negotiation.Current.CodeDigest != negotiation.Candidate.CodeDigest || negotiation.Current.ConfigDigest == negotiation.Candidate.ConfigDigest || negotiation.Current.ArtifactDigest == negotiation.Candidate.ArtifactDigest {
		t.Fatalf("side-by-side identities = current %#v, candidate %#v", negotiation.Current, negotiation.Candidate)
	}

	currentNodes, systemNATSURL := startGrovletsFromArtifact(t, ctx, currentPath)
	defer stopGrovlets(t, currentNodes)
	transport, err := systemnats.Connect(ctx, systemNATSURL)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	if err := waitForObservedServices(ctx, transport, "node-3", groveshop.ServiceOrders, groveshop.ServiceInventory); err != nil {
		t.Fatalf("wait for current placement: %v\n%s", err, grovletLogs(currentNodes))
	}

	suffix := strings.TrimPrefix(candidateInspection.ArtifactDigest, "sha256:")[:12]
	candidateInventorySubject := "_GROVE.system.candidate." + suffix + ".inventory"
	candidateOrdersSubject := "_GROVE.system.candidate." + suffix + ".orders"
	candidateInventory, err := grovetest.StartNode(
		candidatePath,
		"--node-id", "node-2-candidate",
		"--advertise-endpoint", "nats-subject://system/node-2-candidate",
		"--system-nats-url", systemNATSURL,
		"--system-nats-subject", candidateInventorySubject,
		"--grove-shop-inventory",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = candidateInventory.Cleanup() })
	if err := candidateInventory.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	candidateOrders, err := grovetest.StartNode(
		candidatePath,
		"--node-id", "node-1-candidate",
		"--advertise-endpoint", "nats-subject://system/node-1-candidate",
		"--system-nats-url", systemNATSURL,
		"--system-nats-subject", candidateOrdersSubject,
		"--grove-shop-orders-inventory-subject", candidateInventorySubject,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = candidateOrders.Cleanup() })
	if err := candidateOrders.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	defer stopCandidateNodes(t, candidateOrders, candidateInventory)
	for name, logs := range map[string]string{"Orders": candidateOrders.Logs(), "Inventory": candidateInventory.Logs()} {
		if !strings.Contains(logs, `"config_revision":"acme-r43"`) || !strings.Contains(logs, `"artifact_digest":"`+candidateInspection.ArtifactDigest+`"`) {
			t.Errorf("candidate %s readiness = %q", name, logs)
		}
	}

	currentClient, err := transport.ObservedPlacementClient("node-3")
	if err != nil {
		t.Fatal(err)
	}
	currentOrder, err := grove.Call[groveshop.CreateOrderRequest, groveshop.Order](ctx, currentClient, groveshop.ServiceOrders, groveshop.MethodCreateOrder, groveshop.CreateOrderRequest{
		OrderID: "order-current", SKU: "coffee-beans", Quantity: 2, AmountCents: 2400, ShippingAddress: "26 Grove Lane",
	})
	if err != nil || currentOrder.Status != groveshop.OrderCompleted {
		t.Fatalf("invoke current artifact = %#v, %v\n%s", currentOrder, err, grovletLogs(currentNodes))
	}
	candidateClient, err := transport.RoutedClient(candidateOrdersSubject)
	if err != nil {
		t.Fatal(err)
	}
	_, err = grove.Call[groveshop.CreateOrderRequest, groveshop.Order](ctx, candidateClient, groveshop.ServiceOrders, groveshop.MethodCreateOrder, groveshop.CreateOrderRequest{
		OrderID: "order-candidate-over-buffer", SKU: "coffee-beans", Quantity: 2, AmountCents: 2400, ShippingAddress: "26 Grove Lane",
	})
	if err == nil || !strings.Contains(err.Error(), groveshop.ErrReservationBufferExceeded.Error()) {
		t.Errorf("candidate over-buffer error = %v; want %v", err, groveshop.ErrReservationBufferExceeded)
	}
	candidateOrder, err := grove.Call[groveshop.CreateOrderRequest, groveshop.Order](ctx, candidateClient, groveshop.ServiceOrders, groveshop.MethodCreateOrder, groveshop.CreateOrderRequest{
		OrderID: "order-candidate", SKU: "coffee-beans", Quantity: 1, AmountCents: 1200, ShippingAddress: "26 Grove Lane",
	})
	if err != nil || candidateOrder.Status != groveshop.OrderCompleted {
		t.Fatalf("invoke candidate artifact = %#v, %v; Orders logs=%q; Inventory logs=%q", candidateOrder, err, candidateOrders.Logs(), candidateInventory.Logs())
	}
	placement, err := transport.RequestPlacement(ctx, "node-3")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range placement.Placements {
		if record.ArtifactDigest != currentInspection.ArtifactDigest || strings.Contains(record.InvocationSubject, suffix) {
			t.Errorf("candidate changed authoritative placement: %#v", record)
		}
	}
}

func stopCandidateNodes(t *testing.T, nodes ...*grovetest.Node) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, node := range nodes {
		if err := node.Stop(ctx); err != nil {
			t.Errorf("stop candidate: %v; logs=%q", err, node.Logs())
		}
	}
}
