package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/grovetest"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/systemnats"
)

const mvpTestTimeout = 4 * time.Minute

type mvpCluster struct {
	nodes      []*grovetest.Node
	subjects   []string
	webAddress string
}

// One real-process regression proves the complete Grove Shop MVP lifecycle
// through the same artifact, control-plane, Web, recovery, and rollback paths
// used by the focused acceptance tests.
func TestGroveShopMVPLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), mvpTestTimeout)
	defer cancel()
	directory := t.TempDir()
	currentConfig := directory + "/acme.yaml"
	currentPath := directory + "/grove-shop-acme"
	candidatePath := directory + "/grove-shop-acme-broken"
	writeConfigFile(t, currentConfig, "acme-r42", "cloud", 100)
	if output, err := runGroveCommand(ctx, "config", "embed", "--binary", grovletPath, "--config", currentConfig, "--output", currentPath); err != nil {
		t.Fatalf("produce Artifact A: %v; output=%q", err, output)
	}
	currentInspection, err := artifact.InspectFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	candidateInspection := embedBrokenInventoryArtifact(t, candidatePath)
	if currentInspection.CodeDigest != candidateInspection.CodeDigest || currentInspection.Config.Digest == candidateInspection.Config.Digest || currentInspection.ArtifactDigest == candidateInspection.ArtifactDigest {
		t.Fatalf("MVP artifact identities = current %#v candidate %#v", currentInspection, candidateInspection)
	}
	ui, err := groveshop.WebAsset("index.html")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(binary, ui) {
		t.Fatal("Artifact A does not contain the embedded Grove Shop UI")
	}

	cluster, systemNATSURL := startMVPCluster(t, ctx, currentPath)
	transport, err := systemnats.Connect(ctx, systemNATSURL)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	if err := waitForDeploymentControlViews(ctx, transport, []systemnats.DeploymentArtifact{}, []systemnats.Rollout{}); err != nil {
		t.Fatalf("wait for deployment read models: %v\n%s", err, grovletLogs(cluster.nodes))
	}

	currentRoutes := []systemnats.PlacementRecord{
		{ServiceID: groveshop.ServiceOrders, NodeID: "node-1", InvocationSubject: cluster.subjects[0] + ".service.1", ArtifactDigest: currentInspection.ArtifactDigest},
		{ServiceID: groveshop.ServiceInventory, NodeID: "node-2", InvocationSubject: cluster.subjects[1] + ".service.2", ArtifactDigest: currentInspection.ArtifactDigest},
		{ServiceID: groveshop.ServiceWeb, NodeID: "node-1", InvocationSubject: cluster.subjects[0] + ".service.5", ArtifactDigest: currentInspection.ArtifactDigest},
	}
	if err := waitForPlacementRecords(ctx, transport, "node-3", currentRoutes); err != nil {
		t.Fatalf("wait for Artifact A placement: %v\n%s", err, grovletLogs(cluster.nodes))
	}
	desired := mvpDesiredDeployment(currentInspection, "node-2")
	if err := putMVPDesiredEventually(ctx, transport, "node-1", desired); err != nil {
		t.Fatal(err)
	}
	if err := waitForDesiredDeployments(ctx, transport, "node-3", []systemnats.DesiredDeployment{desired}); err != nil {
		t.Fatalf("wait for Artifact A desired state: %v\n%s", err, grovletLogs(cluster.nodes))
	}
	currentArtifact := artifactControlRecord(currentInspection)
	if err := transport.PutDeploymentArtifact(ctx, "node-1", currentArtifact); err != nil {
		t.Fatal(err)
	}
	active := rolloutRecord(1, currentArtifact.ArtifactDigest, "", systemnats.RolloutActive)
	if err := transport.PutRollout(ctx, "node-2", active); err != nil {
		t.Fatal(err)
	}
	if err := waitForDeploymentControlViews(ctx, transport, []systemnats.DeploymentArtifact{currentArtifact}, []systemnats.Rollout{active}); err != nil {
		t.Fatalf("wait for active Artifact A: %v\n%s", err, grovletLogs(cluster.nodes))
	}

	page, err := waitForMVPBody(ctx, "http://"+cluster.webAddress+"/")
	if err != nil {
		t.Fatalf("read Grove Shop UI: %v\n%s", err, grovletLogs(cluster.nodes))
	}
	if !bytes.Equal(page, ui) {
		t.Error("deployed Web component did not serve the UI embedded in Artifact A")
	}
	configuration, err := readMVPConfiguration(ctx, cluster.webAddress)
	if err != nil {
		t.Fatalf("read running config: %v\n%s", err, grovletLogs(cluster.nodes))
	}
	if configuration.Revision != "acme-r42" || configuration.ConfigDigest != currentInspection.Config.Digest || configuration.CustomerName != "Acme Retail" || configuration.ReservationBuffer != 100 {
		t.Errorf("running Artifact A config = %#v", configuration)
	}
	initialStatus, err := waitForMVPStatus(ctx, cluster.webAddress, func(status groveshop.ClusterStatusView) bool {
		return mvpStatusHealthy(status, currentInspection.ArtifactDigest, "acme-r42", currentInspection.Config.Digest)
	})
	if err != nil {
		t.Fatalf("wait for healthy Web status: %v\n%s", err, grovletLogs(cluster.nodes))
	}
	if !mvpCrossesGrovlets(initialStatus) {
		t.Fatalf("initial placement does not cross Grovlets: %#v", initialStatus.Placements)
	}
	assertMVPOrder(t, ctx, cluster.webAddress, "mvp-before-failure")

	transport.Close()
	if err := cluster.nodes[1].Kill(ctx); err != nil {
		t.Fatal(err)
	}
	systemNATSURL = latestMVPSystemNATSURL(t, cluster.nodes[0].Logs())
	transport, err = systemnats.Connect(ctx, systemNATSURL)
	if err != nil {
		t.Fatal(err)
	}
	recoveredNodeID, err := waitForCommandTestRecovery(
		ctx,
		transport,
		"node-2",
		groveshop.ServiceInventory,
		currentInspection.ArtifactDigest,
	)
	if err != nil {
		t.Fatalf("recover Inventory after node loss: %v\n%s", err, grovletLogs(cluster.nodes))
	}
	if recoveredNodeID != "node-1" {
		t.Fatalf("recovered Inventory node = %q; want node-1", recoveredNodeID)
	}
	assertMVPOrder(t, ctx, cluster.webAddress, "mvp-after-recovery")
	desired = mvpDesiredDeployment(currentInspection, recoveredNodeID)
	if err := putMVPDesiredEventually(ctx, transport, "node-1", desired); err != nil {
		t.Fatal(err)
	}
	if err := waitForDesiredDeployments(ctx, transport, "node-3", []systemnats.DesiredDeployment{desired}); err != nil {
		t.Fatalf("persist recovered desired state: %v\n%s", err, grovletLogs(cluster.nodes))
	}

	transport.Close()
	restartMVPCluster(t, ctx, cluster, 1)
	systemNATSURL = latestMVPSystemNATSURL(t, cluster.nodes[0].Logs())
	transport, err = systemnats.Connect(ctx, systemNATSURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := waitForDesiredDeployments(ctx, transport, "node-3", []systemnats.DesiredDeployment{desired}); err != nil {
		t.Fatalf("restore desired deployment: %v\n%s", err, grovletLogs(cluster.nodes))
	}
	restartedStatus, err := waitForMVPStatus(ctx, cluster.webAddress, func(status groveshop.ClusterStatusView) bool {
		return mvpStatusHealthy(status, currentInspection.ArtifactDigest, "acme-r42", currentInspection.Config.Digest) && mvpPlacementNode(status, groveshop.ServiceInventory) == recoveredNodeID
	})
	if err != nil {
		t.Fatalf("wait for restarted Grove Shop: %v\n%s", err, grovletLogs(cluster.nodes))
	}
	if restartedStatus.Rollout == nil || restartedStatus.Rollout.Phase != string(systemnats.RolloutActive) {
		t.Errorf("restored rollout = %#v", restartedStatus.Rollout)
	}
	assertMVPOrder(t, ctx, cluster.webAddress, "mvp-after-restart")

	candidateArtifact := artifactControlRecord(candidateInspection)
	if err := transport.PutDeploymentArtifact(ctx, "node-1", candidateArtifact); err != nil {
		t.Fatal(err)
	}
	pending := rolloutRecord(2, currentArtifact.ArtifactDigest, candidateArtifact.ArtifactDigest, systemnats.RolloutPending)
	if err := transport.PutRollout(ctx, "node-3", pending); err != nil {
		t.Fatal(err)
	}
	wantArtifacts := []systemnats.DeploymentArtifact{currentArtifact, candidateArtifact}
	slices.SortFunc(wantArtifacts, func(a, b systemnats.DeploymentArtifact) int {
		return strings.Compare(a.ArtifactDigest, b.ArtifactDigest)
	})
	if _, err := waitForMVPStatus(ctx, cluster.webAddress, func(status groveshop.ClusterStatusView) bool {
		return status.Rollout != nil && status.Rollout.Phase == string(systemnats.RolloutPending) &&
			status.ActiveArtifact != nil && status.ActiveArtifact.ArtifactDigest == currentArtifact.ArtifactDigest &&
			status.CandidateArtifact != nil && status.CandidateArtifact.ArtifactDigest == candidateArtifact.ArtifactDigest
	}); err != nil {
		t.Fatalf("observe pending candidate through Web status: %v\n%s", err, grovletLogs(cluster.nodes))
	}

	candidateInventorySubject := "_GROVE.system.mvp.candidate.inventory"
	candidateNode, err := grovetest.StartNode(
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
	t.Cleanup(func() { _ = candidateNode.Cleanup() })
	candidateCtx, candidateCancel := context.WithTimeout(ctx, 5*time.Second)
	err = candidateNode.WaitReady(candidateCtx)
	candidateCancel()
	if err == nil {
		t.Fatal("broken Inventory candidate became ready")
	}
	if logs := candidateNode.Logs(); !strings.Contains(logs, "inventory.reservation_buffer") || !strings.Contains(logs, "must be zero or greater") {
		t.Fatalf("broken candidate failure = %v; logs=%q", err, logs)
	}

	currentPlacement, err := transport.RequestPlacement(ctx, "node-3")
	if err != nil {
		t.Fatal(err)
	}
	routes := make([]systemnats.UpgradeRoute, len(currentPlacement.Placements))
	for i, currentRoute := range currentPlacement.Placements {
		candidateNodeID := "node-1-candidate"
		candidateSubject := "_GROVE.system.mvp.candidate." + strconv.FormatUint(uint64(currentRoute.ServiceID), 10)
		if currentRoute.ServiceID == groveshop.ServiceInventory {
			candidateNodeID = "node-2-candidate"
			candidateSubject = candidateInventorySubject
		}
		routes[i] = systemnats.UpgradeRoute{
			Current: currentRoute,
			Candidate: systemnats.PlacementRecord{
				ServiceID: currentRoute.ServiceID, NodeID: candidateNodeID,
				InvocationSubject: candidateSubject, ArtifactDigest: candidateArtifact.ArtifactDigest,
			},
		}
	}
	failure := systemnats.RolloutFailure{
		Code: "candidate_startup_failed", Component: "Inventory",
		Field: "inventory.reservation_buffer", Message: "must be zero or greater",
	}
	rolledBack, err := systemnats.NewDeployments().RollbackFailedUpgrade(ctx, transport, pending, routes, failure)
	if err != nil {
		t.Fatalf("rollback broken candidate: %v\n%s", err, grovletLogs(cluster.nodes))
	}
	if err := waitForDeploymentControlViews(ctx, transport, wantArtifacts, []systemnats.Rollout{rolledBack}); err != nil {
		t.Fatalf("wait for rolled-back control state: %v\n%s", err, grovletLogs(cluster.nodes))
	}
	rolledBackStatus, err := waitForMVPStatus(ctx, cluster.webAddress, func(status groveshop.ClusterStatusView) bool {
		return mvpStatusHealthy(status, currentArtifact.ArtifactDigest, "acme-r42", currentInspection.Config.Digest) &&
			status.Rollout != nil && status.Rollout.Phase == string(systemnats.RolloutRolledBack) &&
			status.Rollout.Failure != nil && status.Rollout.Failure.Code == failure.Code
	})
	if err != nil {
		t.Fatalf("observe rollback through Web status: %v\n%s", err, grovletLogs(cluster.nodes))
	}
	if rolledBackStatus.CandidateArtifact == nil || rolledBackStatus.CandidateArtifact.ConfigRevision != "acme-broken-r43" || rolledBackStatus.ActiveArtifact.ConfigDigest != currentInspection.Config.Digest {
		t.Errorf("rolled-back artifact/config status = %#v", rolledBackStatus)
	}
	assertMVPOrder(t, ctx, cluster.webAddress, "mvp-after-rollback")

	resilienceOutput, err := runGroveCommand(ctx, "test", "--binary", currentPath, "--resilience")
	wantResilienceOutput := "Grove Shop E2E\n✓ baseline\nKilled service 2 host node-2\n✓ failure detected\n✓ service 2 recovered on node-1\n✓ flow after recovery\nPASS\n"
	if err != nil || resilienceOutput != wantResilienceOutput {
		t.Fatalf("MVP resilience workflow = %v; output=%q", err, resilienceOutput)
	}
	if _, err := waitForMVPStatus(ctx, cluster.webAddress, func(status groveshop.ClusterStatusView) bool {
		return mvpStatusHealthy(status, currentArtifact.ArtifactDigest, "acme-r42", currentInspection.Config.Digest) && status.Rollout != nil && status.Rollout.Phase == string(systemnats.RolloutRolledBack)
	}); err != nil {
		t.Fatalf("final cluster health: %v\n%s", err, grovletLogs(cluster.nodes))
	}
	transport.Close()
	stopMVPCluster(t, cluster.nodes)
}

func startMVPCluster(t *testing.T, ctx context.Context, artifactPath string) (*mvpCluster, string) {
	t.Helper()
	ports := reserveRoutePorts(t, 3)
	webPort := reserveRoutePorts(t, 1)[0]
	cluster := &mvpCluster{
		subjects:   []string{"_GROVE.system.mvp.node-1", "_GROVE.system.mvp.node-2", "_GROVE.system.mvp.node-3"},
		webAddress: "127.0.0.1:" + strconv.Itoa(webPort),
	}
	extra := [][]string{
		{"--grove-shop-orders", "--grove-shop-web", "--grove-shop-web-listen", cluster.webAddress},
		{"--grove-shop-inventory"},
		{},
	}
	for i := range cluster.subjects {
		seed := 0
		if i == 0 {
			seed = 1
		}
		args := []string{
			"--node-id", fmt.Sprintf("node-%d", i+1),
			"--advertise-endpoint", fmt.Sprintf("nats-subject://system/node-%d", i+1),
			"--system-nats-listen", "127.0.0.1:0",
			"--system-nats-route-listen", "127.0.0.1:" + strconv.Itoa(ports[i]),
			"--system-nats-seed", "nats-route://127.0.0.1:" + strconv.Itoa(ports[seed]),
			"--system-nats-membership",
			"--system-nats-recovery",
			"--system-nats-subject", cluster.subjects[i],
		}
		node, err := grovetest.StartNode(artifactPath, append(args, extra[i]...)...)
		if err != nil {
			t.Fatal(err)
		}
		cluster.nodes = append(cluster.nodes, node)
		t.Cleanup(func() {
			if err := node.Cleanup(); err != nil {
				t.Errorf("cleanup MVP node-%d: %v", i+1, err)
			}
		})
	}
	for _, node := range cluster.nodes {
		if err := node.WaitReady(ctx); err != nil {
			t.Fatalf("wait for MVP cluster: %v\n%s", err, grovletLogs(cluster.nodes))
		}
	}
	return cluster, latestMVPSystemNATSURL(t, cluster.nodes[0].Logs())
}

func restartMVPCluster(t *testing.T, ctx context.Context, cluster *mvpCluster, alreadyStopped int) {
	t.Helper()
	for i := len(cluster.nodes) - 1; i >= 0; i-- {
		if i == alreadyStopped {
			continue
		}
		if err := cluster.nodes[i].Stop(ctx); err != nil {
			t.Fatalf("stop node-%d for restart: %v\n%s", i+1, err, grovletLogs(cluster.nodes))
		}
	}
	for i, node := range cluster.nodes {
		if err := node.Restart(); err != nil {
			t.Fatalf("restart node-%d: %v\n%s", i+1, err, grovletLogs(cluster.nodes))
		}
	}
	for i, node := range cluster.nodes {
		if err := node.WaitReady(ctx); err != nil {
			t.Fatalf("wait for restarted node-%d: %v\n%s", i+1, err, grovletLogs(cluster.nodes))
		}
	}
}

func stopMVPCluster(t *testing.T, nodes []*grovetest.Node) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	errorsByNode := make([]error, len(nodes))
	var wait sync.WaitGroup
	for i, node := range nodes {
		wait.Go(func() {
			if err := node.Stop(ctx); err != nil {
				errorsByNode[i] = fmt.Errorf("node-%d: %w", i+1, err)
			}
		})
	}
	wait.Wait()
	if err := errors.Join(errorsByNode...); err != nil {
		t.Errorf("stop final MVP cluster: %v\n%s", err, grovletLogs(nodes))
	}
}

func latestMVPSystemNATSURL(t *testing.T, logs string) string {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(logs))
	url := ""
	for {
		var event struct {
			Event         string `json:"event"`
			SystemNATSURL string `json:"system_nats_url"`
		}
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode MVP Grovlet events: %v; logs=%q", err, logs)
		}
		if event.Event == "ready" && event.SystemNATSURL != "" {
			url = event.SystemNATSURL
		}
	}
	if url == "" {
		t.Fatalf("MVP Grovlet did not report a System NATS URL: %q", logs)
	}
	return url
}

func mvpDesiredDeployment(inspection artifact.Inspection, inventoryNodeID string) systemnats.DesiredDeployment {
	return systemnats.DesiredDeployment{
		ApplicationID:  inspection.Manifest.ApplicationID,
		Version:        inspection.Manifest.CodeVersion,
		ArtifactDigest: inspection.ArtifactDigest,
		Components: []systemnats.DesiredComponent{
			{ServiceID: groveshop.ServiceOrders, NodeID: "node-1"},
			{ServiceID: groveshop.ServiceInventory, NodeID: inventoryNodeID},
			{ServiceID: groveshop.ServiceWeb, NodeID: "node-1"},
		},
	}
}

func putMVPDesiredEventually(
	ctx context.Context,
	transport *systemnats.Transport,
	nodeID string,
	desired systemnats.DesiredDeployment,
) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		err := transport.PutDesired(attemptCtx, nodeID, desired)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("write desired deployment: %w", errors.Join(lastErr, ctx.Err()))
		}
	}
}

func readMVPConfiguration(ctx context.Context, webAddress string) (groveshop.RuntimeConfigurationView, error) {
	var view groveshop.RuntimeConfigurationView
	err := readMVPJSON(ctx, "http://"+webAddress+"/grove/config", &view)
	return view, err
}

func waitForMVPBody(ctx context.Context, url string) ([]byte, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		requestCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, url, nil)
		if err == nil {
			var response *http.Response
			response, err = http.DefaultClient.Do(request)
			if err == nil {
				body, readErr := io.ReadAll(response.Body)
				closeErr := response.Body.Close()
				err = errors.Join(readErr, closeErr)
				if err == nil && response.StatusCode == http.StatusOK {
					cancel()
					return body, nil
				}
				if err == nil {
					err = fmt.Errorf("HTTP status %s", response.Status)
				}
			}
		}
		cancel()
		lastErr = err
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil, fmt.Errorf("HTTP endpoint %s did not become ready: %w", url, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func waitForMVPStatus(
	ctx context.Context,
	webAddress string,
	accept func(groveshop.ClusterStatusView) bool,
) (groveshop.ClusterStatusView, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var last groveshop.ClusterStatusView
	var lastErr error
	for {
		requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := readMVPJSON(requestCtx, "http://"+webAddress+"/grove/status", &last)
		cancel()
		if err == nil && accept(last) {
			return last, nil
		}
		lastErr = err
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return last, fmt.Errorf("Web status did not converge: status=%#v: %w", last, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func readMVPJSON(ctx context.Context, url string, output any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		return fmt.Errorf("GET %s: %s: %s", url, response.Status, body)
	}
	return json.NewDecoder(response.Body).Decode(output)
}

func createMVPOrder(ctx context.Context, webAddress, orderID string) (groveshop.Order, error) {
	requestBody, err := json.Marshal(groveshop.CreateOrderRequest{
		OrderID: orderID, SKU: "coffee-beans", Quantity: 1,
		AmountCents: 1200, ShippingAddress: "31 Grove Lane",
	})
	if err != nil {
		return groveshop.Order{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+webAddress+"/api/orders", bytes.NewReader(requestBody))
	if err != nil {
		return groveshop.Order{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return groveshop.Order{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		return groveshop.Order{}, fmt.Errorf("POST /api/orders: %s: %s", response.Status, body)
	}
	var order groveshop.Order
	if err := json.NewDecoder(response.Body).Decode(&order); err != nil {
		return groveshop.Order{}, err
	}
	return order, nil
}

func assertMVPOrder(t *testing.T, ctx context.Context, webAddress, orderID string) {
	t.Helper()
	order, err := createMVPOrder(ctx, webAddress, orderID)
	wantHistory := []groveshop.OrderStatus{
		groveshop.OrderCreated,
		groveshop.OrderReserved,
		groveshop.OrderPaid,
		groveshop.OrderShipping,
		groveshop.OrderCompleted,
	}
	if err != nil || order.ID != orderID || order.Status != groveshop.OrderCompleted || !slices.Equal(order.History, wantHistory) {
		t.Fatalf("order %q = %#v, %v; want complete history %v", orderID, order, err, wantHistory)
	}
}

func mvpStatusHealthy(status groveshop.ClusterStatusView, artifactDigest, configRevision, configDigest string) bool {
	if !status.Ready || status.Health != "healthy" || len(status.Nodes) != 3 || len(status.Placements) != 3 ||
		status.ActiveArtifact == nil || status.ActiveArtifact.ArtifactDigest != artifactDigest ||
		status.ActiveArtifact.ConfigRevision != configRevision || status.ActiveArtifact.ConfigDigest != configDigest {
		return false
	}
	for _, node := range status.Nodes {
		if node.Health != string(systemnats.HealthHealthy) {
			return false
		}
	}
	for _, placement := range status.Placements {
		if placement.Health != string(systemnats.ComponentHealthy) {
			return false
		}
	}
	return true
}

func mvpCrossesGrovlets(status groveshop.ClusterStatusView) bool {
	ordersNode := mvpPlacementNode(status, groveshop.ServiceOrders)
	inventoryNode := mvpPlacementNode(status, groveshop.ServiceInventory)
	return ordersNode != "" && inventoryNode != "" && ordersNode != inventoryNode
}

func mvpPlacementNode(status groveshop.ClusterStatusView, serviceID grove.ServiceID) string {
	for _, placement := range status.Placements {
		if placement.ServiceID == serviceID {
			return placement.NodeID
		}
	}
	return ""
}
