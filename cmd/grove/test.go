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
	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/grovetest"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/systemnats"
)

const (
	testCommandTimeout    = 60 * time.Second
	testConditionInterval = 25 * time.Millisecond
)

func executeTest(ctx context.Context, parsed invocation, output io.Writer) error {
	inspection, err := artifact.InspectFile(parsed.binaryPath)
	if err != nil {
		return fmt.Errorf("inspect test artifact: %w", err)
	}
	if inspection.Manifest.ApplicationID != "grove-shop" {
		return fmt.Errorf("test artifact application %q: %w", inspection.Manifest.ApplicationID, errTestApplication)
	}
	nodes, systemNATSURL, err := startCommandTestCluster(ctx, parsed.binaryPath)
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
	order, err := grove.Call[groveshop.CreateOrderRequest, groveshop.Order](
		ctx,
		client,
		groveshop.ServiceOrders,
		groveshop.MethodCreateOrder,
		groveshop.CreateOrderRequest{
			OrderID:         "grove-test-order",
			SKU:             "coffee-beans",
			Quantity:        1,
			AmountCents:     1200,
			ShippingAddress: "29 Grove Lane",
		},
	)
	transport.Close()
	if err != nil {
		return fail("run Grove Shop flow", err)
	}
	if order.Status != groveshop.OrderCompleted || order.Reservation.ID != "reservation-grove-test-order" {
		return fail("verify Grove Shop flow", fmt.Errorf("order result = %#v", order))
	}
	if err := stopCommandTestNodes(nodes); err != nil {
		return fail("stop test cluster", err)
	}
	if err := cleanupCommandTestNodes(nodes); err != nil {
		return fmt.Errorf("clean up test cluster: %w", err)
	}
	_, err = io.WriteString(output, "Grove Shop E2E\n✓ cluster ready\n✓ order completed\nPASS\n")
	return err
}

func startCommandTestCluster(ctx context.Context, binaryPath string) ([]*grovetest.Node, string, error) {
	ports, err := reserveCommandTestPorts(3)
	if err != nil {
		return nil, "", fmt.Errorf("reserve test cluster ports: %w", err)
	}
	nodeArgs := [][]string{
		{"--system-nats-subject", "_GROVE.system.test.node-1", "--grove-shop-orders"},
		{"--system-nats-subject", "_GROVE.system.test.node-2", "--grove-shop-inventory"},
		{"--system-nats-subject", "_GROVE.system.test.node-3"},
	}
	nodes := make([]*grovetest.Node, 0, len(nodeArgs))
	for i, extra := range nodeArgs {
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
		attemptCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		var attemptErr error
		cluster, clusterErr := transport.RequestClusterView(attemptCtx, "node-3")
		placement, placementErr := transport.RequestPlacement(attemptCtx, "node-3")
		componentsHealthy := true
		componentCount := 0
		if clusterErr == nil {
			lastCluster = cluster
			for _, node := range cluster.Nodes {
				view, err := transport.RequestComponents(attemptCtx, node.NodeID)
				if err != nil {
					attemptErr = err
					componentsHealthy = false
					break
				}
				for _, component := range view.Components {
					componentCount++
					componentsHealthy = componentsHealthy && component.State == systemnats.ComponentHealthy
				}
			}
		}
		cancel()
		if placementErr == nil {
			lastPlacement = placement
		}
		nodesHealthy := clusterErr == nil && cluster.Ready && len(cluster.Nodes) == 3
		for _, node := range cluster.Nodes {
			nodesHealthy = nodesHealthy && node.Health == systemnats.HealthHealthy
		}
		placementsReady := placementErr == nil && placement.Ready && len(placement.Placements) == 2
		for _, record := range placement.Placements {
			placementsReady = placementsReady && record.ArtifactDigest == artifactDigest
		}
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

func stopCommandTestNodes(nodes []*grovetest.Node) error {
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
