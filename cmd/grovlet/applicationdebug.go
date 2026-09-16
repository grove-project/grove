package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/grovetest"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/systemnats"
)

const debugApplicationNodeCount = 5

var errApplicationAlreadyDeployed = errors.New("Grove Shop is already deployed")

type debugDemoActionResult struct {
	State  string                      `json:"state"`
	WebURL string                      `json:"web_url"`
	Status groveshop.ClusterStatusView `json:"status"`
}

func (c *applicationController) startDebugDemo(ctx context.Context, args []string) (any, error) {
	operationCtx, cancel := context.WithTimeout(ctx, applicationOperationTimeout)
	defer cancel()
	configPath, err := parseDebugDemoArguments(args)
	if err != nil {
		return nil, err
	}
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	c.mu.RLock()
	deployed := c.cluster != nil
	c.mu.RUnlock()
	if deployed {
		return nil, errApplicationAlreadyDeployed
	}
	delvePath, err := exec.LookPath("dlv")
	if err != nil {
		return nil, fmt.Errorf("locate Delve for debug demo: %w", err)
	}
	compilation, err := compileApplicationConfiguration(configPath)
	if err != nil {
		return nil, fmt.Errorf("compile debug demo configuration: %w", err)
	}
	artifactPath := filepath.Join(c.runtimeDir, "groveshop-debug")
	inspection, err := artifact.EmbedFile(c.binaryPath, artifactPath, compilation)
	if err != nil {
		return nil, fmt.Errorf("build debug-capable Grove Shop artifact: %w", err)
	}
	cluster, err := startDebugApplicationCluster(operationCtx, artifactPath, delvePath, inspection)
	if err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			cleanupApplicationNodes(cluster.nodes)
		}
	}()
	transport, err := systemnats.Connect(operationCtx, cluster.systemNATSURL)
	if err != nil {
		return nil, fmt.Errorf("connect to debug demo cluster: %w", err)
	}
	defer transport.Close()
	if err := waitForDebugApplicationPlacement(operationCtx, transport, inspection.ArtifactDigest); err != nil {
		return nil, fmt.Errorf("wait for debug demo placement: %w\n%s", err, applicationDiagnostics(cluster.nodes))
	}
	record := applicationArtifactRecord(inspection)
	if err := transport.PutDeploymentArtifact(operationCtx, "node-1", record); err != nil {
		return nil, fmt.Errorf("record debug demo artifact: %w", err)
	}
	if err := transport.PutRollout(operationCtx, "node-2", debugApplicationRollout(record)); err != nil {
		return nil, fmt.Errorf("activate debug demo rollout: %w", err)
	}
	c.mu.Lock()
	c.cluster = cluster
	c.lastEvent = "started five-node debug demo"
	c.mu.Unlock()
	status, err := c.waitForStatus(operationCtx, func(status groveshop.ClusterStatusView) bool {
		return debugApplicationStatusHealthy(status, inspection.ArtifactDigest)
	})
	if err != nil {
		c.mu.Lock()
		c.cluster = nil
		c.mu.Unlock()
		return nil, fmt.Errorf("wait for debug demo status: %w\n%s", err, applicationDiagnostics(cluster.nodes))
	}
	cleanup = false
	return debugDemoActionResult{State: "active", WebURL: "http://" + cluster.webAddress, Status: status}, nil
}

func parseDebugDemoArguments(args []string) (string, error) {
	flags := flag.NewFlagSet("debug.demo.start", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := "configs/acme.yaml"
	flags.StringVar(&configPath, "config", configPath, "application YAML configuration path")
	if err := flags.Parse(args); err != nil {
		return "", fmt.Errorf("parse debug.demo.start arguments: %w", err)
	}
	if flags.NArg() != 0 {
		return "", fmt.Errorf("parse debug.demo.start arguments: %w: %q", errConsoleArguments, flags.Args())
	}
	return configPath, nil
}

func startDebugApplicationCluster(ctx context.Context, artifactPath, delvePath string, inspection artifact.Inspection) (*applicationCluster, error) {
	routePorts, err := reserveApplicationPorts(debugApplicationNodeCount)
	if err != nil {
		return nil, fmt.Errorf("reserve debug demo route ports: %w", err)
	}
	webPorts, err := reserveApplicationPorts(1)
	if err != nil {
		return nil, fmt.Errorf("reserve debug demo Web port: %w", err)
	}
	cluster := &applicationCluster{
		webAddress: "127.0.0.1:" + strconv.Itoa(webPorts[0]),
		artifact:   inspection, debugDemo: true,
	}
	extras := [][]string{
		{"--grove-shop-web", "--grove-shop-web-listen", cluster.webAddress},
		{"--grove-shop-orders", "--grove-shop-distributed-orders"},
		{"--grove-shop-inventory"},
		{"--grove-shop-payment"},
		{"--grove-shop-shipping"},
	}
	for i, extra := range extras {
		seed := 0
		if i == 0 {
			seed = 1
		}
		nodeID := fmt.Sprintf("node-%d", i+1)
		args := []string{
			"--node-id", nodeID,
			"--advertise-endpoint", "nats-subject://system/" + nodeID,
			"--system-nats-listen", "127.0.0.1:0",
			"--system-nats-route-listen", "127.0.0.1:" + strconv.Itoa(routePorts[i]),
			"--system-nats-seed", "nats-route://127.0.0.1:" + strconv.Itoa(routePorts[seed]),
			"--system-nats-membership",
			"--system-nats-subject", "_GROVE.system.application.debug." + nodeID,
			"--delve-path", delvePath,
		}
		node, err := grovetest.StartNode(artifactPath, append(args, extra...)...)
		if err != nil {
			cleanupApplicationNodes(cluster.nodes)
			return nil, fmt.Errorf("start debug demo %s: %w", nodeID, err)
		}
		cluster.nodes = append(cluster.nodes, node)
	}
	for i, node := range cluster.nodes {
		if err := node.WaitReady(ctx); err != nil {
			cleanupApplicationNodes(cluster.nodes)
			return nil, fmt.Errorf("wait for debug demo node-%d: %w\n%s", i+1, err, applicationDiagnostics(cluster.nodes))
		}
	}
	cluster.systemNATSURL, err = applicationSystemNATSURL(cluster.nodes[0].Logs())
	if err != nil {
		cleanupApplicationNodes(cluster.nodes)
		return nil, fmt.Errorf("read debug demo System NATS URL: %w\n%s", err, applicationDiagnostics(cluster.nodes))
	}
	return cluster, nil
}

func waitForDebugApplicationPlacement(ctx context.Context, transport *systemnats.Transport, digest string) error {
	want := debugApplicationPlacements()
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	var lastCluster systemnats.ClusterView
	var lastPlacement systemnats.PlacementView
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, time.Second)
		cluster, clusterErr := transport.RequestClusterView(attemptCtx, "node-1")
		placement, placementErr := transport.RequestPlacement(attemptCtx, "node-1")
		if clusterErr == nil {
			lastCluster = cluster
		}
		if placementErr == nil {
			lastPlacement = placement
		}
		ready := clusterErr == nil && cluster.Ready && len(cluster.Nodes) == debugApplicationNodeCount &&
			placementErr == nil && placement.Ready && len(placement.Placements) == len(want)
		for _, node := range cluster.Nodes {
			ready = ready && node.Health == systemnats.HealthHealthy
		}
		for _, record := range placement.Placements {
			wantNode, expected := want[record.ServiceID]
			ready = ready && expected && record.NodeID == wantNode && record.ArtifactDigest == digest
			components, err := transport.RequestComponents(attemptCtx, record.NodeID)
			if err != nil {
				lastErr = errors.Join(lastErr, err)
				ready = false
				continue
			}
			found := false
			for _, component := range components.Components {
				if component.ServiceID == record.ServiceID && component.InvocationSubject == record.InvocationSubject && component.State == systemnats.ComponentHealthy && component.WorkerID != "" {
					found = true
					break
				}
			}
			ready = ready && found
		}
		cancel()
		if ready {
			return nil
		}
		lastErr = errors.Join(lastErr, clusterErr, placementErr)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("cluster=%#v placement=%#v: %w", lastCluster, lastPlacement, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func debugApplicationPlacements() map[grove.ServiceID]string {
	return map[grove.ServiceID]string{
		groveshop.ServiceWeb: "node-1", groveshop.ServiceOrders: "node-2",
		groveshop.ServiceInventory: "node-3", groveshop.ServicePayment: "node-4",
		groveshop.ServiceShipping: "node-5",
	}
}

func debugApplicationRollout(artifact systemnats.DeploymentArtifact) systemnats.Rollout {
	nodes := make([]systemnats.RolloutNodeProgress, debugApplicationNodeCount)
	for i := range nodes {
		nodes[i] = systemnats.RolloutNodeProgress{
			NodeID: fmt.Sprintf("node-%d", i+1), CurrentArtifactDigest: artifact.ArtifactDigest,
			Phase: systemnats.RolloutActive,
		}
	}
	return systemnats.Rollout{
		ApplicationID: artifact.ApplicationID, ClusterID: artifact.ClusterID,
		RolloutID: "grove-shop-debug-1", Generation: 1,
		CurrentArtifactDigest: artifact.ArtifactDigest, Phase: systemnats.RolloutActive, Nodes: nodes,
	}
}

func debugApplicationStatusHealthy(status groveshop.ClusterStatusView, digest string) bool {
	if !status.Ready || status.Health != "healthy" || len(status.Nodes) != debugApplicationNodeCount ||
		len(status.Placements) != len(debugApplicationPlacements()) || status.ActiveArtifact == nil ||
		status.ActiveArtifact.ArtifactDigest != digest {
		return false
	}
	want := debugApplicationPlacements()
	for _, node := range status.Nodes {
		if node.Health != string(systemnats.HealthHealthy) {
			return false
		}
	}
	for _, placement := range status.Placements {
		if placement.NodeID != want[placement.ServiceID] || placement.Health != string(systemnats.ComponentHealthy) {
			return false
		}
	}
	return true
}
