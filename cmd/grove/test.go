package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/grovetest"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/systemnats"
	groveshop "github.com/grove-project/grove/internal/testapp"
)

const (
	testCommandTimeout         = 60 * time.Second
	testConditionInterval      = 25 * time.Millisecond
	defaultResilienceServiceID = groveshop.ServiceInventory
	commandTestNodeCount       = 3
)

func executeTest(ctx context.Context, parsed invocation, output io.Writer) error {
	inspection, err := artifact.InspectFile(parsed.binaryPath)
	if err != nil {
		return fmt.Errorf("inspect test artifact: %w", err)
	}
	if inspection.Manifest.ApplicationID != groveshop.ApplicationID {
		return fmt.Errorf("test artifact application %q: %w", inspection.Manifest.ApplicationID, errTestApplication)
	}
	nodes, systemNATSURL, err := startCommandTestCluster(ctx, parsed.binaryPath, parsed.resilience)
	if err != nil {
		return err
	}
	fail := func(operation string, operationErr error) error {
		diagnostics := commandTestDiagnostics(nodes)
		cleanupErr := cleanupCommandTestNodes(nodes)
		return fmt.Errorf("%s: %w\n%s", operation, errors.Join(operationErr, cleanupErr), diagnostics)
	}
	transport, err := systemnats.Connect(ctx, systemNATSURL)
	if err != nil {
		return fail("connect to test cluster", err)
	}
	if err := waitForCommandTestReady(ctx, transport, inspection.ArtifactDigest); err != nil {
		transport.Close()
		return fail("wait for test cluster", err)
	}
	client, err := transport.ObservedPlacementClient("node-3")
	if err != nil {
		transport.Close()
		return fail("create test client", err)
	}
	if err := runCommandTestFlow(ctx, client, "grove-test-order"); err != nil {
		transport.Close()
		return fail("run Grove Shop flow", err)
	}
	failedNodeID := ""
	recoveredNodeID := ""
	if parsed.resilience {
		failedNodeID, recoveredNodeID, transport, err = runCommandTestResilience(
			ctx,
			transport,
			nodes,
			parsed.serviceID,
			inspection.ArtifactDigest,
		)
		if err != nil {
			return fail("run resilience scenario", err)
		}
		client, err = transport.ObservedPlacementClient("node-3")
		if err != nil {
			transport.Close()
			return fail("create recovered test client", err)
		}
		if err := runCommandTestFlow(ctx, client, "grove-test-order-after-recovery"); err != nil {
			transport.Close()
			return fail("rerun Grove Shop flow", err)
		}
	}
	transport.Close()
	if err := stopCommandTestNodes(nodes, failedNodeID); err != nil {
		return fail("stop test cluster", err)
	}
	if err := cleanupCommandTestNodes(nodes); err != nil {
		return fmt.Errorf("clean up test cluster: %w", err)
	}
	transcript := "Grove Shop E2E\n✓ cluster ready\n✓ order completed\nPASS\n"
	if parsed.resilience {
		transcript = fmt.Sprintf(
			"Grove Shop E2E\n✓ baseline\nKilled service %d host %s\n✓ failure detected\n✓ service %d recovered on %s\n✓ flow after recovery\nPASS\n",
			parsed.serviceID,
			failedNodeID,
			parsed.serviceID,
			recoveredNodeID,
		)
	}
	_, err = io.WriteString(output, transcript)
	return err
}

func runCommandTestFlow(ctx context.Context, client *grove.Client, orderID string) error {
	order, err := grove.Call[groveshop.CreateOrderRequest, groveshop.Order](
		ctx,
		client,
		groveshop.ServiceOrders,
		groveshop.MethodCreateOrder,
		groveshop.CreateOrderRequest{
			OrderID:         orderID,
			SKU:             "coffee-beans",
			Quantity:        1,
			AmountCents:     1200,
			ShippingAddress: "30 Grove Lane",
		},
	)
	if err != nil {
		return err
	}
	if order.Status != groveshop.OrderCompleted || order.Reservation.ID != "reservation-"+orderID {
		return fmt.Errorf("order result = %#v", order)
	}
	return nil
}

func runCommandTestResilience(
	ctx context.Context,
	currentTransport *systemnats.Transport,
	nodes []*grovetest.Node,
	serviceID grove.ServiceID,
	artifactDigest string,
) (string, string, *systemnats.Transport, error) {
	placement, err := currentTransport.RequestPlacement(ctx, "node-3")
	if err != nil {
		currentTransport.Close()
		return "", "", nil, err
	}
	targetNodeID := ""
	for _, record := range placement.Placements {
		if record.ServiceID == serviceID {
			targetNodeID = record.NodeID
			break
		}
	}
	if targetNodeID == "" {
		currentTransport.Close()
		return "", "", nil, fmt.Errorf("service %d is not placed", serviceID)
	}
	targetIndex := commandTestNodeIndex(targetNodeID, nodes)
	if targetIndex < 0 {
		currentTransport.Close()
		return "", "", nil, fmt.Errorf("service %d host %q is not a test node", serviceID, targetNodeID)
	}
	currentTransport.Close()
	if err := nodes[targetIndex].Kill(ctx); err != nil {
		return targetNodeID, "", nil, err
	}
	survivorIndex := 0
	if survivorIndex == targetIndex {
		survivorIndex++
	}
	survivorURL, err := commandTestSystemNATSURL(nodes[survivorIndex].Logs())
	if err != nil {
		return targetNodeID, "", nil, err
	}
	transport, err := systemnats.Connect(ctx, survivorURL)
	if err != nil {
		return targetNodeID, "", nil, err
	}
	recoveredNodeID, err := waitForCommandTestRecovery(ctx, transport, targetNodeID, serviceID, artifactDigest)
	if err != nil {
		transport.Close()
		return targetNodeID, "", nil, err
	}
	return targetNodeID, recoveredNodeID, transport, nil
}

func commandTestNodeIndex(nodeID string, nodes []*grovetest.Node) int {
	for i := range nodes {
		if nodeID == fmt.Sprintf("node-%d", i+1) {
			return i
		}
	}
	return -1
}

func waitForCommandTestRecovery(
	ctx context.Context,
	transport *systemnats.Transport,
	failedNodeID string,
	serviceID grove.ServiceID,
	artifactDigest string,
) (string, error) {
	ticker := time.NewTicker(testConditionInterval)
	defer ticker.Stop()
	var lastCluster systemnats.ClusterView
	var lastPlacement systemnats.PlacementView
	var lastErr error
	for {
		var attemptErr error
		attemptCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		cluster, clusterErr := transport.RequestClusterView(attemptCtx, "node-3")
		placement, placementErr := transport.RequestPlacement(attemptCtx, "node-3")
		cancel()
		if clusterErr == nil {
			lastCluster = cluster
		}
		if placementErr == nil {
			lastPlacement = placement
		}
		clusterRecovered := clusterErr == nil && cluster.Ready && len(cluster.Nodes) == commandTestNodeCount
		failedObserved := false
		healthyNodes := make(map[string]bool, len(cluster.Nodes))
		for _, node := range cluster.Nodes {
			if node.NodeID == failedNodeID {
				failedObserved = node.Health == systemnats.HealthUnavailable
				continue
			}
			healthyNodes[node.NodeID] = node.Health == systemnats.HealthHealthy
			clusterRecovered = clusterRecovered && node.Health == systemnats.HealthHealthy
		}
		clusterRecovered = clusterRecovered && failedObserved
		var recoveredPlacement systemnats.PlacementRecord
		if placementErr == nil && placement.Ready {
			for _, record := range placement.Placements {
				if record.ServiceID == serviceID && record.NodeID != failedNodeID && record.ArtifactDigest == artifactDigest && healthyNodes[record.NodeID] {
					recoveredPlacement = record
					break
				}
			}
		}
		placementConverged := clusterRecovered && recoveredPlacement.NodeID != ""
		if placementConverged {
			for nodeID := range healthyNodes {
				observerCtx, observerCancel := context.WithTimeout(ctx, 250*time.Millisecond)
				view, err := transport.RequestPlacement(observerCtx, nodeID)
				observerCancel()
				if err != nil {
					attemptErr = errors.Join(attemptErr, err)
					placementConverged = false
					continue
				}
				observed := false
				for _, record := range view.Placements {
					if record == recoveredPlacement {
						observed = true
						break
					}
				}
				placementConverged = placementConverged && view.Ready && observed
			}
		}
		if placementConverged {
			componentCtx, componentCancel := context.WithTimeout(ctx, 250*time.Millisecond)
			view, err := transport.RequestComponents(componentCtx, recoveredPlacement.NodeID)
			componentCancel()
			if err == nil {
				for _, component := range view.Components {
					if component.ServiceID == serviceID &&
						component.InvocationSubject == recoveredPlacement.InvocationSubject &&
						component.State == systemnats.ComponentHealthy {
						return recoveredPlacement.NodeID, nil
					}
				}
			} else {
				attemptErr = errors.Join(attemptErr, err)
			}
		}
		lastErr = errors.Join(attemptErr, clusterErr, placementErr)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return "", fmt.Errorf("cluster=%#v placement=%#v: %w", lastCluster, lastPlacement, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func startCommandTestCluster(ctx context.Context, binaryPath string, resilience bool) ([]*grovetest.Node, string, error) {
	ports, err := reserveCommandTestPorts(commandTestNodeCount)
	if err != nil {
		return nil, "", fmt.Errorf("reserve test cluster ports: %w", err)
	}
	nodeArgs := [][]string{
		{"--system-nats-subject", "_GROVE.system.test.node-1", "--component", "orders"},
		{"--system-nats-subject", "_GROVE.system.test.node-2", "--component", "inventory"},
		{"--system-nats-subject", "_GROVE.system.test.node-3"},
	}
	nodes := make([]*grovetest.Node, 0, len(nodeArgs))
	for i, extra := range nodeArgs {
		if resilience {
			extra = append(extra, "--system-nats-recovery")
		}
		seed := 0
		if i == 0 {
			seed = 1
		}
		nodeID := fmt.Sprintf("node-%d", i+1)
		args := []string{
			"--node-id", nodeID,
			"--advertise-endpoint", "nats-subject://system/" + nodeID,
			"--system-nats-listen", "127.0.0.1:0",
			"--system-nats-route-listen", "127.0.0.1:" + strconv.Itoa(ports[i]),
			"--system-nats-seed", "nats-route://127.0.0.1:" + strconv.Itoa(ports[seed]),
			"--system-nats-membership",
		}
		node, err := grovetest.StartNode(binaryPath, append(args, extra...)...)
		if err != nil {
			_ = cleanupCommandTestNodes(nodes)
			return nil, "", fmt.Errorf("start test %s: %w", nodeID, err)
		}
		nodes = append(nodes, node)
	}
	for _, node := range nodes {
		if err := node.WaitReady(ctx); err != nil {
			diagnostics := commandTestDiagnostics(nodes)
			_ = cleanupCommandTestNodes(nodes)
			return nil, "", fmt.Errorf("wait for test Grovlets: %w\n%s", err, diagnostics)
		}
	}
	systemNATSURL, err := commandTestSystemNATSURL(nodes[0].Logs())
	if err != nil {
		diagnostics := commandTestDiagnostics(nodes)
		_ = cleanupCommandTestNodes(nodes)
		return nil, "", fmt.Errorf("read test System NATS URL: %w\n%s", err, diagnostics)
	}
	return nodes, systemNATSURL, nil
}

func reserveCommandTestPorts(count int) ([]int, error) {
	listeners := make([]net.Listener, 0, count)
	ports := make([]int, 0, count)
	for range count {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			for _, opened := range listeners {
				_ = opened.Close()
			}
			return nil, err
		}
		listeners = append(listeners, listener)
		ports = append(ports, listener.Addr().(*net.TCPAddr).Port)
	}
	var closeErr error
	for _, listener := range listeners {
		closeErr = errors.Join(closeErr, listener.Close())
	}
	return ports, closeErr
}

func commandTestSystemNATSURL(logs string) (string, error) {
	decoder := json.NewDecoder(strings.NewReader(logs))
	for {
		var event struct {
			Event         string `json:"event"`
			SystemNATSURL string `json:"system_nats_url"`
		}
		if err := decoder.Decode(&event); err != nil {
			return "", err
		}
		if event.Event == "ready" && event.SystemNATSURL != "" {
			return event.SystemNATSURL, nil
		}
	}
}

func waitForCommandTestReady(ctx context.Context, transport *systemnats.Transport, artifactDigest string) error {
	ticker := time.NewTicker(testConditionInterval)
	defer ticker.Stop()
	var lastCluster systemnats.ClusterView
	var lastPlacement systemnats.PlacementView
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, time.Second)
		var attemptErr error
		cluster, clusterErr := transport.RequestClusterView(attemptCtx, "node-3")
		placement, placementErr := transport.RequestPlacement(attemptCtx, "node-3")
		if clusterErr == nil {
			lastCluster = cluster
		}
		if placementErr == nil {
			lastPlacement = placement
		}
		nodesHealthy := clusterErr == nil && cluster.Ready && len(cluster.Nodes) == commandTestNodeCount
		for _, node := range cluster.Nodes {
			nodesHealthy = nodesHealthy && node.Health == systemnats.HealthHealthy
		}
		placementsReady := placementErr == nil && placement.Ready && len(placement.Placements) == 2
		expectedServices := map[grove.ServiceID]bool{
			groveshop.ServiceOrders:    false,
			groveshop.ServiceInventory: false,
		}
		for _, record := range placement.Placements {
			seen, expected := expectedServices[record.ServiceID]
			placementsReady = placementsReady && expected && !seen && record.ArtifactDigest == artifactDigest
			expectedServices[record.ServiceID] = true
		}
		componentsHealthy := placementsReady
		componentCount := 0
		if placementsReady {
			for _, record := range placement.Placements {
				view, err := transport.RequestComponents(attemptCtx, record.NodeID)
				if err != nil {
					attemptErr = errors.Join(attemptErr, err)
					componentsHealthy = false
					continue
				}
				for _, component := range view.Components {
					if component.ServiceID == record.ServiceID &&
						component.InvocationSubject == record.InvocationSubject &&
						component.State == systemnats.ComponentHealthy {
						componentCount++
						break
					}
				}
			}
		}
		cancel()
		if nodesHealthy && componentsHealthy && componentCount == 2 && placementsReady {
			return nil
		}
		lastErr = errors.Join(attemptErr, clusterErr, placementErr)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("cluster=%#v placement=%#v: %w", lastCluster, lastPlacement, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func stopCommandTestNodes(nodes []*grovetest.Node, skipNodeID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	errorsByNode := make([]error, len(nodes))
	var wait sync.WaitGroup
	for i, node := range nodes {
		if skipNodeID == fmt.Sprintf("node-%d", i+1) {
			continue
		}
		wait.Go(func() {
			if err := node.Stop(ctx); err != nil {
				errorsByNode[i] = fmt.Errorf("node-%d: %w", i+1, err)
			}
		})
	}
	wait.Wait()
	return errors.Join(errorsByNode...)
}

func cleanupCommandTestNodes(nodes []*grovetest.Node) error {
	var cleanupErr error
	for i, node := range nodes {
		if err := node.Cleanup(); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("node-%d: %w", i+1, err))
		}
	}
	return cleanupErr
}

func commandTestDiagnostics(nodes []*grovetest.Node) string {
	var diagnostics strings.Builder
	for i, node := range nodes {
		fmt.Fprintf(&diagnostics, "node-%d logs:\n%s", i+1, node.Logs())
		if !strings.HasSuffix(node.Logs(), "\n") {
			diagnostics.WriteByte('\n')
		}
	}
	return diagnostics.String()
}
