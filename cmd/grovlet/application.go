package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/console"
	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/grovetest"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/systemnats"
	"go.yaml.in/yaml/v3"
)

const (
	applicationNodeCount         = 3
	applicationConditionInterval = 25 * time.Millisecond
)

var (
	errApplicationConfigRequired = errors.New("rollout configuration path is required")
	errApplicationNotDeployed    = errors.New("Grove Shop is not deployed")
	errCandidateMustFail         = errors.New("a second Task 031 rollout must exercise invalid Inventory configuration")
)

type applicationController struct {
	binaryPath  string
	runtimeDir  string
	operationMu sync.Mutex
	mu          sync.RWMutex
	cluster     *applicationCluster
	lastEvent   string
}

type applicationCluster struct {
	nodes         []*grovetest.Node
	systemNATSURL string
	webAddress    string
	artifact      artifact.Inspection
	failedNodeID  string
}

type rolloutActionResult struct {
	State       string                        `json:"state"`
	WebURL      string                        `json:"web_url"`
	Transitions []groveshop.ClusterStatusView `json:"transitions"`
	Status      groveshop.ClusterStatusView   `json:"status"`
}

type resilienceActionResult struct {
	FailedNodeID    string                      `json:"failed_node_id"`
	RecoveredNodeID string                      `json:"recovered_node_id"`
	Order           groveshop.Order             `json:"order"`
	Status          groveshop.ClusterStatusView `json:"status"`
}

func newApplicationController(binaryPath, runtimeDir string) *applicationController {
	return &applicationController{binaryPath: binaryPath, runtimeDir: runtimeDir}
}

func registerApplicationConsoleActions(registry *console.Registry, controller *applicationController) error {
	actions := []console.Action{
		{
			Name: "cluster.status", Label: "Status", Section: "Cluster",
			Description: "Read current Grove Shop cluster and rollout state.",
			Handler: func(ctx context.Context, args []string) (any, error) {
				if len(args) != 0 {
					return nil, errConsoleArguments
				}
				return controller.status(ctx)
			},
		},
		{
			Name: "rollout.start", Label: "New rollout", Section: "Deployments",
			Description: "Build and roll out an immutable configured Grove Shop artifact.",
			Handler:     controller.startRollout,
		},
		{
			Name: "cluster.restart", Label: "Restart cluster", Section: "Cluster",
			Description: "Restart every Grovlet from its durable runtime state.",
			Handler:     controller.restartCluster,
		},
		{
			Name: "resilience.run", Label: "Run resilience scenario", Section: "Application",
			Description: "Recover Inventory after its hosting Grovlet fails, then rerun an order.",
			Handler:     controller.runResilience,
		},
	}
	for _, action := range actions {
		if err := registry.Register(action); err != nil {
			return fmt.Errorf("register Grove action %q: %w", action.Name, err)
		}
	}
	if err := groveshop.RegisterActions(registry); err != nil {
		return fmt.Errorf("register Grove Shop actions: %w", err)
	}
	return nil
}

func (c *applicationController) startRollout(ctx context.Context, args []string) (any, error) {
	configPath, err := parseRolloutArguments(args)
	if err != nil {
		return nil, err
	}
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	c.mu.RLock()
	deployed := c.cluster != nil
	c.mu.RUnlock()
	if !deployed {
		return c.startKnownGood(ctx, configPath)
	}
	return c.rejectBrokenCandidate(ctx, configPath)
}

func parseRolloutArguments(args []string) (string, error) {
	flags := flag.NewFlagSet("rollout.start", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var configPath string
	flags.StringVar(&configPath, "config", "", "application YAML configuration path")
	if err := flags.Parse(args); err != nil {
		return "", fmt.Errorf("parse rollout.start arguments: %w", err)
	}
	if flags.NArg() != 0 {
		return "", fmt.Errorf("parse rollout.start arguments: %w: %q", errConsoleArguments, flags.Args())
	}
	if configPath == "" {
		return "", errApplicationConfigRequired
	}
	return configPath, nil
}

func (c *applicationController) runResilience(ctx context.Context, args []string) (any, error) {
	if len(args) != 0 {
		return nil, errConsoleArguments
	}
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	c.mu.RLock()
	cluster := c.cluster
	c.mu.RUnlock()
	if cluster == nil {
		return nil, errApplicationNotDeployed
	}
	if cluster.failedNodeID != "" {
		return nil, errors.New("restart the cluster before running another resilience scenario")
	}
	transport, err := systemnats.Connect(ctx, cluster.systemNATSURL)
	if err != nil {
		return nil, fmt.Errorf("connect to Grove Shop cluster: %w", err)
	}
	placement, err := transport.RequestPlacement(ctx, "node-3")
	if err != nil {
		transport.Close()
		return nil, fmt.Errorf("read Inventory placement: %w", err)
	}
	failedNodeID := applicationPlacementNode(placement.Placements, groveshop.ServiceInventory)
	failedIndex := applicationNodeIndex(failedNodeID)
	if failedIndex < 0 || failedIndex >= len(cluster.nodes) {
		transport.Close()
		return nil, fmt.Errorf("Inventory host %q is not a managed Grovlet", failedNodeID)
	}
	transport.Close()
	if err := cluster.nodes[failedIndex].Kill(ctx); err != nil {
		return nil, fmt.Errorf("kill Inventory host %s: %w", failedNodeID, err)
	}
	transport, err = systemnats.Connect(ctx, cluster.systemNATSURL)
	if err != nil {
		return nil, fmt.Errorf("reconnect after Inventory host failure: %w", err)
	}
	defer transport.Close()
	recoveredNodeID, err := waitForApplicationRecovery(ctx, transport, failedNodeID, cluster.artifact.ArtifactDigest)
	if err != nil {
		return nil, fmt.Errorf("recover Inventory after node loss: %w\n%s", err, applicationDiagnostics(cluster.nodes))
	}
	if err := putApplicationDesired(ctx, transport, applicationDesiredDeployment(cluster.artifact, recoveredNodeID)); err != nil {
		return nil, err
	}
	order, err := createApplicationOrder(ctx, cluster.webAddress, "resilience-order")
	if err != nil {
		return nil, fmt.Errorf("run order after Inventory recovery: %w", err)
	}
	if !applicationOrderCompleted(order) {
		return nil, fmt.Errorf("recovered order did not complete: %#v", order)
	}
	c.mu.Lock()
	cluster.failedNodeID = failedNodeID
	c.lastEvent = "Inventory recovered from " + failedNodeID + " on " + recoveredNodeID
	c.mu.Unlock()
	status, err := c.status(ctx)
	if err != nil {
		return nil, fmt.Errorf("read recovered application status: %w", err)
	}
	return resilienceActionResult{
		FailedNodeID: failedNodeID, RecoveredNodeID: recoveredNodeID, Order: order, Status: status,
	}, nil
}

func (c *applicationController) restartCluster(ctx context.Context, args []string) (any, error) {
	if len(args) != 0 {
		return nil, errConsoleArguments
	}
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	c.mu.RLock()
	cluster := c.cluster
	c.mu.RUnlock()
	if cluster == nil {
		return nil, errApplicationNotDeployed
	}
	for i := len(cluster.nodes) - 1; i >= 0; i-- {
		if fmt.Sprintf("node-%d", i+1) == cluster.failedNodeID {
			continue
		}
		if err := cluster.nodes[i].Stop(ctx); err != nil {
			return nil, fmt.Errorf("stop node-%d for durable restart: %w\n%s", i+1, err, applicationDiagnostics(cluster.nodes))
		}
	}
	for i, node := range cluster.nodes {
		if err := node.Restart(); err != nil {
			return nil, fmt.Errorf("restart node-%d: %w\n%s", i+1, err, applicationDiagnostics(cluster.nodes))
		}
	}
	for i, node := range cluster.nodes {
		if err := node.WaitReady(ctx); err != nil {
			return nil, fmt.Errorf("wait for restarted node-%d: %w\n%s", i+1, err, applicationDiagnostics(cluster.nodes))
		}
	}
	systemNATSURL, err := applicationSystemNATSURL(cluster.nodes[0].Logs())
	if err != nil {
		return nil, fmt.Errorf("read restarted System NATS URL: %w", err)
	}
	c.mu.Lock()
	cluster.systemNATSURL = systemNATSURL
	cluster.failedNodeID = ""
	c.lastEvent = "reconstructed deployment from durable state"
	c.mu.Unlock()
	status, err := c.waitForStatus(ctx, func(status groveshop.ClusterStatusView) bool {
		return applicationStatusHealthy(status, cluster.artifact.ArtifactDigest)
	})
	if err != nil {
		return nil, fmt.Errorf("wait for reconstructed application status: %w\n%s", err, applicationDiagnostics(cluster.nodes))
	}
	return status, nil
}

func (c *applicationController) startKnownGood(ctx context.Context, configPath string) (rolloutActionResult, error) {
	compilation, err := compileApplicationConfiguration(configPath)
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("compile known-good configuration: %w", err)
	}
	artifactPath := filepath.Join(c.runtimeDir, "groveshop-active")
	inspection, err := artifact.EmbedFile(c.binaryPath, artifactPath, compilation)
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("build known-good Grove Shop artifact: %w", err)
	}
	cluster, err := startApplicationCluster(ctx, artifactPath, inspection)
	if err != nil {
		return rolloutActionResult{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			cleanupApplicationNodes(cluster.nodes)
		}
	}()
	transport, err := systemnats.Connect(ctx, cluster.systemNATSURL)
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("connect to Grove Shop cluster: %w", err)
	}
	defer transport.Close()
	if err := waitForApplicationPlacement(ctx, transport, inspection.ArtifactDigest); err != nil {
		return rolloutActionResult{}, fmt.Errorf("wait for Grove Shop placement: %w\n%s", err, applicationDiagnostics(cluster.nodes))
	}
	desired := applicationDesiredDeployment(inspection, "node-2")
	if err := putApplicationDesired(ctx, transport, desired); err != nil {
		return rolloutActionResult{}, err
	}
	record := applicationArtifactRecord(inspection)
	if err := transport.PutDeploymentArtifact(ctx, "node-1", record); err != nil {
		return rolloutActionResult{}, fmt.Errorf("record known-good artifact: %w", err)
	}
	active := applicationRollout(1, record.ClusterID, record.ArtifactDigest, "", systemnats.RolloutActive)
	if err := transport.PutRollout(ctx, "node-2", active); err != nil {
		return rolloutActionResult{}, fmt.Errorf("activate known-good rollout: %w", err)
	}
	c.mu.Lock()
	c.cluster = cluster
	c.lastEvent = "deployed " + inspection.Config.Revision
	c.mu.Unlock()
	status, err := c.waitForStatus(ctx, func(status groveshop.ClusterStatusView) bool {
		return applicationStatusHealthy(status, inspection.ArtifactDigest) && status.Rollout != nil && status.Rollout.Phase == string(systemnats.RolloutActive)
	})
	if err != nil {
		c.mu.Lock()
		c.cluster = nil
		c.mu.Unlock()
		return rolloutActionResult{}, fmt.Errorf("wait for known-good application status: %w\n%s", err, applicationDiagnostics(cluster.nodes))
	}
	cleanup = false
	return rolloutActionResult{
		State: "active", WebURL: "http://" + cluster.webAddress,
		Transitions: []groveshop.ClusterStatusView{status}, Status: status,
	}, nil
}

func (c *applicationController) rejectBrokenCandidate(ctx context.Context, configPath string) (rolloutActionResult, error) {
	c.mu.RLock()
	cluster := c.cluster
	c.mu.RUnlock()
	if cluster == nil {
		return rolloutActionResult{}, errApplicationNotDeployed
	}
	compilation, validation, err := compileInvalidCandidateConfiguration(configPath)
	if err != nil {
		return rolloutActionResult{}, err
	}
	candidatePath := filepath.Join(c.runtimeDir, "groveshop-candidate")
	inspection, err := artifact.EmbedFile(c.binaryPath, candidatePath, compilation)
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("build candidate Grove Shop artifact: %w", err)
	}
	transport, err := systemnats.Connect(ctx, cluster.systemNATSURL)
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("connect to Grove Shop cluster: %w", err)
	}
	defer transport.Close()
	candidate := applicationArtifactRecord(inspection)
	if err := transport.PutDeploymentArtifact(ctx, "node-1", candidate); err != nil {
		return rolloutActionResult{}, fmt.Errorf("record candidate artifact: %w", err)
	}
	pending := applicationRollout(2, candidate.ClusterID, cluster.artifact.ArtifactDigest, candidate.ArtifactDigest, systemnats.RolloutPending)
	if err := transport.PutRollout(ctx, "node-3", pending); err != nil {
		return rolloutActionResult{}, fmt.Errorf("record pending candidate: %w", err)
	}
	pendingStatus, err := c.waitForStatus(ctx, func(status groveshop.ClusterStatusView) bool {
		return status.Rollout != nil && status.Rollout.Phase == string(systemnats.RolloutPending) &&
			status.CandidateArtifact != nil && status.CandidateArtifact.ArtifactDigest == candidate.ArtifactDigest
	})
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("observe pending candidate: %w", err)
	}
	candidateNode, err := grovetest.StartNode(
		candidatePath,
		"--node-id", "node-2-candidate",
		"--advertise-endpoint", "nats-subject://system/node-2-candidate",
		"--system-nats-url", cluster.systemNATSURL,
		"--system-nats-subject", "_GROVE.system.application.candidate.inventory",
		"--grove-shop-inventory",
	)
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("start Inventory candidate: %w", err)
	}
	defer candidateNode.Cleanup()
	candidateCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	readyErr := candidateNode.WaitReady(candidateCtx)
	cancel()
	if readyErr == nil {
		return rolloutActionResult{}, errors.New("invalid Inventory candidate became ready")
	}
	currentPlacement, err := transport.RequestPlacement(ctx, "node-3")
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("read known-good placement: %w", err)
	}
	routes := applicationUpgradeRoutes(currentPlacement.Placements, candidate.ArtifactDigest)
	failure := systemnats.RolloutFailure{
		Code: "candidate_startup_failed", Component: "Inventory",
		Field: validation.Field, Message: validation.Message,
	}
	_, err = systemnats.NewDeployments().RollbackFailedUpgrade(ctx, transport, pending, routes, failure)
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("rollback failed candidate: %w", err)
	}
	status, err := c.waitForStatus(ctx, func(status groveshop.ClusterStatusView) bool {
		return applicationStatusHealthy(status, cluster.artifact.ArtifactDigest) && status.Rollout != nil &&
			status.Rollout.Phase == string(systemnats.RolloutRolledBack) && status.Rollout.Failure != nil
	})
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("observe candidate rollback: %w", err)
	}
	c.mu.Lock()
	c.lastEvent = "rejected " + inspection.Config.Revision + ": " + failure.Field + " " + failure.Message
	c.mu.Unlock()
	return rolloutActionResult{
		State: "rolled-back", WebURL: "http://" + cluster.webAddress,
		Transitions: []groveshop.ClusterStatusView{pendingStatus, status}, Status: status,
	}, nil
}

func compileApplicationConfiguration(path string) (artifact.Compilation, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return artifact.Compilation{}, fmt.Errorf("read configuration %q: %w", path, err)
	}
	configuration, canonical, err := groveshop.CompileConfigurationYAML(source)
	if err != nil {
		return artifact.Compilation{}, err
	}
	payload, err := groveshop.EncodeConfiguration(configuration)
	if err != nil {
		return artifact.Compilation{}, err
	}
	return artifact.Compilation{
		ProtocolVersion: artifact.CompilerProtocolVersion,
		Revision:        configuration.Revision,
		Encoding:        groveShopConfigEncoding,
		Payload:         payload,
		CanonicalYAML:   canonical,
		Facts: map[string]string{
			"cluster.name": configuration.Cluster.Name,
			"node.zone":    configuration.Node.Zone,
		},
	}, nil
}

func compileInvalidCandidateConfiguration(path string) (artifact.Compilation, *groveshop.ConfigurationError, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return artifact.Compilation{}, nil, fmt.Errorf("read configuration %q: %w", path, err)
	}
	configuration := groveshop.DefaultConfiguration()
	decoder := yaml.NewDecoder(bytes.NewReader(source))
	decoder.KnownFields(true)
	if err := decoder.Decode(&configuration); err != nil {
		return artifact.Compilation{}, nil, fmt.Errorf("decode candidate configuration: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return artifact.Compilation{}, nil, errors.New("candidate configuration contains multiple YAML documents")
	}
	validationErr := groveshop.ValidateConfiguration(configuration)
	var validation *groveshop.ConfigurationError
	if validationErr == nil || !errors.As(validationErr, &validation) || validation.Field != "inventory.reservation_buffer" {
		return artifact.Compilation{}, nil, errCandidateMustFail
	}
	payload, err := grove.Encode(configuration)
	if err != nil {
		return artifact.Compilation{}, nil, fmt.Errorf("encode invalid candidate configuration: %w", err)
	}
	return artifact.Compilation{
		ProtocolVersion: artifact.CompilerProtocolVersion,
		Revision:        configuration.Revision,
		Encoding:        groveShopConfigEncoding,
		Payload:         payload,
		CanonicalYAML:   source,
		Facts: map[string]string{
			"cluster.name": configuration.Cluster.Name,
			"node.zone":    configuration.Node.Zone,
		},
	}, validation, nil
}

func startApplicationCluster(
	ctx context.Context,
	artifactPath string,
	inspection artifact.Inspection,
) (*applicationCluster, error) {
	routePorts, err := reserveApplicationPorts(applicationNodeCount)
	if err != nil {
		return nil, fmt.Errorf("reserve application route ports: %w", err)
	}
	webPorts, err := reserveApplicationPorts(1)
	if err != nil {
		return nil, fmt.Errorf("reserve application Web port: %w", err)
	}
	cluster := &applicationCluster{
		webAddress: "127.0.0.1:" + strconv.Itoa(webPorts[0]),
		artifact:   inspection,
	}
	extras := [][]string{
		{"--grove-shop-orders", "--grove-shop-web", "--grove-shop-web-listen", cluster.webAddress},
		{"--grove-shop-inventory"},
		{},
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
			"--system-nats-membership", "--system-nats-recovery",
			"--system-nats-subject", "_GROVE.system.application." + nodeID,
		}
		node, err := grovetest.StartNode(artifactPath, append(args, extra...)...)
		if err != nil {
			cleanupApplicationNodes(cluster.nodes)
			return nil, fmt.Errorf("start application %s: %w", nodeID, err)
		}
		cluster.nodes = append(cluster.nodes, node)
	}
	for _, node := range cluster.nodes {
		if err := node.WaitReady(ctx); err != nil {
			cleanupApplicationNodes(cluster.nodes)
			return nil, fmt.Errorf("wait for application Grovlets: %w\n%s", err, applicationDiagnostics(cluster.nodes))
		}
	}
	cluster.systemNATSURL, err = applicationSystemNATSURL(cluster.nodes[0].Logs())
	if err != nil {
		cleanupApplicationNodes(cluster.nodes)
		return nil, fmt.Errorf("read application System NATS URL: %w\n%s", err, applicationDiagnostics(cluster.nodes))
	}
	return cluster, nil
}

func reserveApplicationPorts(count int) ([]int, error) {
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

func applicationSystemNATSURL(logs string) (string, error) {
	decoder := json.NewDecoder(strings.NewReader(logs))
	url := ""
	for {
		var event lifecycleEvent
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) && url != "" {
				return url, nil
			}
			return "", err
		}
		if event.Event == "ready" && event.SystemNATSURL != "" {
			url = event.SystemNATSURL
		}
	}
}

func waitForApplicationPlacement(ctx context.Context, transport *systemnats.Transport, digest string) error {
	want := map[grove.ServiceID]string{
		groveshop.ServiceOrders: "node-1", groveshop.ServiceInventory: "node-2", groveshop.ServiceWeb: "node-1",
	}
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	var lastCluster systemnats.ClusterView
	var lastPlacement systemnats.PlacementView
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, time.Second)
		cluster, clusterErr := transport.RequestClusterView(attemptCtx, "node-3")
		placement, placementErr := transport.RequestPlacement(attemptCtx, "node-3")
		if clusterErr == nil {
			lastCluster = cluster
		}
		if placementErr == nil {
			lastPlacement = placement
		}
		ready := clusterErr == nil && cluster.Ready && len(cluster.Nodes) == applicationNodeCount &&
			placementErr == nil && placement.Ready && len(placement.Placements) == len(want)
		for _, node := range cluster.Nodes {
			ready = ready && node.Health == systemnats.HealthHealthy
		}
		for _, record := range placement.Placements {
			wantNode, expected := want[record.ServiceID]
			ready = ready && expected && record.NodeID == wantNode && record.ArtifactDigest == digest
			view, err := transport.RequestComponents(attemptCtx, record.NodeID)
			if err != nil {
				lastErr = errors.Join(lastErr, err)
				ready = false
				continue
			}
			found := false
			for _, component := range view.Components {
				if component.ServiceID == record.ServiceID && component.State == systemnats.ComponentHealthy {
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
		lastErr = errors.Join(clusterErr, placementErr, lastErr)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("cluster=%#v placement=%#v: %w", lastCluster, lastPlacement, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func putApplicationDesired(ctx context.Context, transport *systemnats.Transport, desired systemnats.DesiredDeployment) error {
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		err := transport.PutDesired(attemptCtx, "node-1", desired)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("record desired deployment: %w", errors.Join(lastErr, ctx.Err()))
		}
	}
}

func applicationDesiredDeployment(inspection artifact.Inspection, inventoryNodeID string) systemnats.DesiredDeployment {
	return systemnats.DesiredDeployment{
		ApplicationID: inspection.Manifest.ApplicationID, Version: inspection.Manifest.CodeVersion,
		ArtifactDigest: inspection.ArtifactDigest,
		Components: []systemnats.DesiredComponent{
			{ServiceID: groveshop.ServiceOrders, NodeID: "node-1"},
			{ServiceID: groveshop.ServiceInventory, NodeID: inventoryNodeID},
			{ServiceID: groveshop.ServiceWeb, NodeID: "node-1"},
		},
	}
}

func applicationArtifactRecord(inspection artifact.Inspection) systemnats.DeploymentArtifact {
	return systemnats.DeploymentArtifact{
		ApplicationID: inspection.Manifest.ApplicationID, CodeVersion: inspection.Manifest.CodeVersion,
		CodeDigest: inspection.CodeDigest, ConfigRevision: inspection.Config.Revision,
		ConfigDigest: inspection.Config.Digest, ArtifactDigest: inspection.ArtifactDigest,
		ClusterID: inspection.Config.Facts["cluster.name"], NodeZone: inspection.Config.Facts["node.zone"],
	}
}

func applicationRollout(generation uint64, clusterID, current, candidate string, phase systemnats.RolloutPhase) systemnats.Rollout {
	nodes := make([]systemnats.RolloutNodeProgress, applicationNodeCount)
	for i := range nodes {
		nodes[i] = systemnats.RolloutNodeProgress{
			NodeID: fmt.Sprintf("node-%d", i+1), CurrentArtifactDigest: current,
			CandidateArtifactDigest: candidate, Phase: phase,
		}
	}
	return systemnats.Rollout{
		ApplicationID: "grove-shop", ClusterID: clusterID,
		RolloutID: "grove-shop-" + strconv.FormatUint(generation, 10), Generation: generation,
		CurrentArtifactDigest: current, CandidateArtifactDigest: candidate, Phase: phase, Nodes: nodes,
	}
}

func applicationUpgradeRoutes(current []systemnats.PlacementRecord, candidateDigest string) []systemnats.UpgradeRoute {
	routes := make([]systemnats.UpgradeRoute, len(current))
	for i, route := range current {
		nodeID := "node-1-candidate"
		if route.ServiceID == groveshop.ServiceInventory {
			nodeID = "node-2-candidate"
		}
		routes[i] = systemnats.UpgradeRoute{
			Current: route,
			Candidate: systemnats.PlacementRecord{
				ServiceID: route.ServiceID, NodeID: nodeID,
				InvocationSubject: "_GROVE.system.application.candidate." + strconv.FormatUint(uint64(route.ServiceID), 10),
				ArtifactDigest:    candidateDigest,
			},
		}
	}
	return routes
}

func applicationPlacementNode(placements []systemnats.PlacementRecord, serviceID grove.ServiceID) string {
	for _, placement := range placements {
		if placement.ServiceID == serviceID {
			return placement.NodeID
		}
	}
	return ""
}

func applicationNodeIndex(nodeID string) int {
	if !strings.HasPrefix(nodeID, "node-") {
		return -1
	}
	index, err := strconv.Atoi(strings.TrimPrefix(nodeID, "node-"))
	if err != nil || index <= 0 {
		return -1
	}
	return index - 1
}

func waitForApplicationRecovery(
	ctx context.Context,
	transport *systemnats.Transport,
	failedNodeID string,
	artifactDigest string,
) (string, error) {
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	var lastCluster systemnats.ClusterView
	var lastPlacement systemnats.PlacementView
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, time.Second)
		cluster, clusterErr := transport.RequestClusterView(attemptCtx, "node-3")
		placement, placementErr := transport.RequestPlacement(attemptCtx, "node-3")
		cancel()
		if clusterErr == nil {
			lastCluster = cluster
		}
		if placementErr == nil {
			lastPlacement = placement
		}
		healthyNodes := make(map[string]bool, len(cluster.Nodes))
		failedObserved := false
		clusterRecovered := clusterErr == nil && cluster.Ready && len(cluster.Nodes) == applicationNodeCount
		for _, node := range cluster.Nodes {
			if node.NodeID == failedNodeID {
				failedObserved = node.Health == systemnats.HealthUnavailable
				continue
			}
			healthyNodes[node.NodeID] = node.Health == systemnats.HealthHealthy
			clusterRecovered = clusterRecovered && node.Health == systemnats.HealthHealthy
		}
		var recovered systemnats.PlacementRecord
		if placementErr == nil && placement.Ready {
			for _, record := range placement.Placements {
				if record.ServiceID == groveshop.ServiceInventory && record.NodeID != failedNodeID &&
					record.ArtifactDigest == artifactDigest && healthyNodes[record.NodeID] {
					recovered = record
					break
				}
			}
		}
		if clusterRecovered && failedObserved && recovered.NodeID != "" {
			componentCtx, componentCancel := context.WithTimeout(ctx, time.Second)
			view, err := transport.RequestComponents(componentCtx, recovered.NodeID)
			componentCancel()
			if err == nil {
				for _, component := range view.Components {
					if component.ServiceID == groveshop.ServiceInventory && component.State == systemnats.ComponentHealthy {
						return recovered.NodeID, nil
					}
				}
			} else {
				lastErr = errors.Join(lastErr, err)
			}
		}
		lastErr = errors.Join(lastErr, clusterErr, placementErr)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return "", fmt.Errorf("cluster=%#v placement=%#v: %w", lastCluster, lastPlacement, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func (c *applicationController) status(ctx context.Context) (groveshop.ClusterStatusView, error) {
	c.mu.RLock()
	cluster := c.cluster
	c.mu.RUnlock()
	if cluster == nil {
		return groveshop.ClusterStatusView{Health: "not-deployed", Nodes: []groveshop.NodeStatusView{}, Placements: []groveshop.PlacementStatusView{}}, nil
	}
	var status groveshop.ClusterStatusView
	if err := readApplicationJSON(ctx, "http://"+cluster.webAddress+"/grove/status", &status); err != nil {
		return groveshop.ClusterStatusView{}, err
	}
	return status, nil
}

func (c *applicationController) waitForStatus(ctx context.Context, accept func(groveshop.ClusterStatusView) bool) (groveshop.ClusterStatusView, error) {
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	var last groveshop.ClusterStatusView
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		status, err := c.status(attemptCtx)
		cancel()
		if err == nil {
			last = status
			if accept(status) {
				return status, nil
			}
		}
		lastErr = err
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return last, fmt.Errorf("status=%#v: %w", last, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func readApplicationJSON(ctx context.Context, url string, output any) error {
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

func createApplicationOrder(ctx context.Context, webAddress, orderID string) (groveshop.Order, error) {
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

func applicationStatusHealthy(status groveshop.ClusterStatusView, digest string) bool {
	if !status.Ready || status.Health != "healthy" || len(status.Nodes) != applicationNodeCount || len(status.Placements) != 3 ||
		status.ActiveArtifact == nil || status.ActiveArtifact.ArtifactDigest != digest {
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

func applicationOrderCompleted(order groveshop.Order) bool {
	return order.Status == groveshop.OrderCompleted && slices.Equal(order.History, []groveshop.OrderStatus{
		groveshop.OrderCreated,
		groveshop.OrderReserved,
		groveshop.OrderPaid,
		groveshop.OrderShipping,
		groveshop.OrderCompleted,
	})
}

func (c *applicationController) readModel(ctx context.Context) (console.Model, error) {
	status, err := c.status(ctx)
	if err != nil {
		return console.Model{}, err
	}
	model := console.Model{Application: "Grove Shop", Health: status.Health, NodesTotal: len(status.Nodes), ServicesTotal: len(status.Placements)}
	for _, node := range status.Nodes {
		if node.Health == string(systemnats.HealthHealthy) {
			model.NodesHealthy++
		}
	}
	for _, placement := range status.Placements {
		if placement.Health == string(systemnats.ComponentHealthy) {
			model.ServicesHealthy++
		}
	}
	if status.ActiveArtifact != nil {
		model.ActiveVersion = status.ActiveArtifact.CodeVersion
		model.ConfigRevision = status.ActiveArtifact.ConfigRevision
	}
	c.mu.RLock()
	model.LastEvent = c.lastEvent
	c.mu.RUnlock()
	return model, nil
}

func cleanupApplicationNodes(nodes []*grovetest.Node) {
	for _, node := range nodes {
		_ = node.Cleanup()
	}
}

func applicationDiagnostics(nodes []*grovetest.Node) string {
	var output strings.Builder
	for i, node := range nodes {
		fmt.Fprintf(&output, "node-%d logs:\n%s", i+1, node.Logs())
		if !strings.HasSuffix(node.Logs(), "\n") {
			output.WriteByte('\n')
		}
	}
	return output.String()
}

func (c *applicationController) close() {
	c.mu.Lock()
	cluster := c.cluster
	c.cluster = nil
	c.mu.Unlock()
	if cluster != nil {
		cleanupApplicationNodes(cluster.nodes)
	}
}
