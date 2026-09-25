package runtime

import (
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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/console"
	"github.com/grove-project/grove/grovetest"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/systemnats"
)

const (
	applicationConditionInterval = 25 * time.Millisecond
	applicationOperationTimeout  = time.Minute
)

var (
	errApplicationConfigRequired = errors.New("rollout configuration path is required")
	errApplicationNotDeployed    = errors.New("Grove application is not deployed")
	errCandidateMustFail         = errors.New("a second lifecycle rollout must exercise application-owned invalid configuration")
)

type applicationController struct {
	binaryPath     string
	runtimeDir     string
	operationMu    sync.Mutex
	mu             sync.RWMutex
	cluster        *applicationCluster
	discovery      *applicationDiscovery
	startup        artifact.Inspection
	startAvailable bool
	joinAvailable  bool
	startupNodes   int
	localNodeID    string
	lastEvent      string
	debugSessions  map[string]console.DebugSession
}

type applicationCluster struct {
	nodes         []*grovetest.Node
	systemNATSURL string
	webAddress    string
	artifact      artifact.Inspection
	failedNodeID  string
	debugDemo     bool
}

type rolloutActionResult struct {
	State       string          `json:"state"`
	WebURL      string          `json:"web_url"`
	Transitions []ClusterStatus `json:"transitions"`
	Status      ClusterStatus   `json:"status"`
}

type resilienceActionResult struct {
	FailedNodeID    string        `json:"failed_node_id"`
	RecoveredNodeID string        `json:"recovered_node_id"`
	Result          any           `json:"result"`
	Status          ClusterStatus `json:"status"`
}

type applicationJoinResult struct {
	State  string `json:"state"`
	NodeID string `json:"node_id"`
}

func newApplicationController(binaryPath, runtimeDir string) *applicationController {
	return &applicationController{
		binaryPath: binaryPath, runtimeDir: runtimeDir,
		debugSessions: make(map[string]console.DebugSession),
	}
}

func registerApplicationConsoleActions(registry *console.Registry, controller *applicationController) error {
	actions := []console.Action{
		{
			Name: "cluster.status", Label: "Status", Section: "Cluster",
			Description: "Read current application cluster and rollout state.",
			Handler: func(ctx context.Context, args []string) (any, error) {
				if len(args) != 0 {
					return nil, errConsoleArguments
				}
				return controller.status(ctx)
			},
		},
		{
			Name: "rollout.start", Label: "New rollout", Section: "Deployments",
			Description: "Build and roll out an immutable configured application artifact.",
			Handler:     controller.startRollout,
		},
		{
			Name: "debug.demo.start", Label: "Debug demo > Start", Section: "Deployments",
			Description: "Start the application's deterministic debugging topology.",
			Handler:     controller.startDebugDemo,
		},
		{
			Name: "cluster.restart", Label: "Restart cluster", Section: "Cluster",
			Description: "Restart every Grovlet from its durable runtime state.",
			Handler:     controller.restartCluster,
		},
		{
			Name: "resilience.run", Label: "Run resilience scenario", Section: "Application",
			Description: "Recover the application-selected service after its Grovlet fails, then rerun the application probe.",
			Handler:     controller.runResilience,
		},
		{
			Name: "logs.view", Label: "View logs", Section: "Logs",
			Description: "Explain health using application, cluster, and System NATS diagnostics.",
			Handler:     controller.logs,
		},
		{
			Name: "debug.attach", Label: "Attach debugger", Section: "Debug",
			Description: "Resolve an application service and expose its Delve DAP session locally.",
			Handler:     controller.attachDebugger,
		},
	}
	if controller.applicationStartAvailable() {
		actions = append(actions, console.Action{
			Name: "cluster.start", Label: "Start new cluster", Section: "Cluster",
			Description: "Start the first node of a new application cluster.",
			Handler:     controller.startDiscoveredApplicationCluster,
		})
	}
	if controller.applicationJoinAvailable() {
		actions = append(actions, console.Action{
			Name: "cluster.join", Label: "Join cluster", Section: "Cluster",
			Description: "Join the discovered application cluster with this artifact.",
			Handler:     controller.joinApplicationCluster,
		})
	}
	for _, action := range actions {
		if err := registry.Register(action); err != nil {
			return fmt.Errorf("register Grove action %q: %w", action.Name, err)
		}
	}
	if activeApplication.RegisterActions != nil {
		if err := activeApplication.RegisterActions(registry); err != nil {
			return fmt.Errorf("register %s actions: %w", activeApplication.Name, err)
		}
	}
	return nil
}

func (c *applicationController) startRollout(ctx context.Context, args []string) (any, error) {
	operationCtx, cancel := context.WithTimeout(ctx, applicationOperationTimeout)
	defer cancel()
	configPath, err := parseRolloutArguments(args)
	if err != nil {
		return nil, err
	}
	c.setLastEvent("rollout: validating " + filepath.Base(configPath))
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	c.mu.RLock()
	deployed := c.cluster != nil
	c.mu.RUnlock()
	if !deployed {
		result, rolloutErr := c.startKnownGood(operationCtx, configPath)
		if rolloutErr != nil {
			c.setLastEvent("rollout failed: " + rolloutErr.Error())
		}
		return result, rolloutErr
	}
	result, rolloutErr := c.rejectBrokenCandidate(operationCtx, configPath)
	if rolloutErr != nil {
		c.setLastEvent("rollout failed: " + rolloutErr.Error())
	}
	return result, rolloutErr
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
	operationCtx, cancel := context.WithTimeout(ctx, applicationOperationTimeout)
	defer cancel()
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
	if activeApplication.Scenario == nil || activeApplication.Scenario.RecoveryServiceID == 0 || activeApplication.Scenario.Probe == nil || activeApplication.Scenario.ProbeHealthy == nil {
		return nil, errors.New("application does not define a resilience scenario")
	}
	targetServiceID := activeApplication.Scenario.RecoveryServiceID
	targetName := applicationServiceName(targetServiceID)
	transport, err := systemnats.Connect(operationCtx, cluster.systemNATSURL)
	if err != nil {
		return nil, fmt.Errorf("connect to application cluster: %w", err)
	}
	placement, err := transport.RequestPlacement(operationCtx, "node-3")
	if err != nil {
		transport.Close()
		return nil, fmt.Errorf("read %s placement: %w", targetName, err)
	}
	failedNodeID := applicationPlacementNode(placement.Placements, targetServiceID)
	failedIndex := applicationNodeIndex(failedNodeID)
	if failedIndex < 0 || failedIndex >= len(cluster.nodes) {
		transport.Close()
		return nil, fmt.Errorf("%s host %q is not a managed Grovlet", targetName, failedNodeID)
	}
	transport.Close()
	if err := cluster.nodes[failedIndex].Kill(operationCtx); err != nil {
		return nil, fmt.Errorf("kill %s host %s: %w", targetName, failedNodeID, err)
	}
	transport, err = systemnats.Connect(operationCtx, cluster.systemNATSURL)
	if err != nil {
		return nil, fmt.Errorf("reconnect after %s host failure: %w", targetName, err)
	}
	defer transport.Close()
	recoveredNodeID, err := waitForApplicationRecovery(operationCtx, transport, targetServiceID, failedNodeID, cluster.artifact.ArtifactDigest)
	if err != nil {
		return nil, fmt.Errorf("recover %s after node loss: %w\n%s", targetName, err, applicationDiagnostics(cluster.nodes))
	}
	if err := putApplicationDesired(operationCtx, transport, applicationDesiredDeployment(cluster.artifact, recoveredNodeID)); err != nil {
		return nil, err
	}
	result, err := waitForApplicationProbe(operationCtx, cluster.webAddress, "resilience-probe")
	if err != nil {
		return nil, fmt.Errorf("run application probe after %s recovery: %w", targetName, err)
	}
	if !activeApplication.Scenario.ProbeHealthy(result) {
		return nil, fmt.Errorf("recovered application probe did not complete: %#v", result)
	}
	c.mu.Lock()
	cluster.failedNodeID = failedNodeID
	c.lastEvent = targetName + " recovered from " + failedNodeID + " on " + recoveredNodeID
	c.mu.Unlock()
	status, err := c.status(operationCtx)
	if err != nil {
		return nil, fmt.Errorf("read recovered application status: %w", err)
	}
	return resilienceActionResult{
		FailedNodeID: failedNodeID, RecoveredNodeID: recoveredNodeID, Result: result, Status: status,
	}, nil
}

func (c *applicationController) restartCluster(ctx context.Context, args []string) (any, error) {
	operationCtx, cancel := context.WithTimeout(ctx, applicationOperationTimeout)
	defer cancel()
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
		if err := cluster.nodes[i].Stop(operationCtx); err != nil {
			return nil, fmt.Errorf("stop node-%d for durable restart: %w\n%s", i+1, err, applicationDiagnostics(cluster.nodes))
		}
	}
	for i, node := range cluster.nodes {
		if err := node.Restart(); err != nil {
			return nil, fmt.Errorf("restart node-%d: %w\n%s", i+1, err, applicationDiagnostics(cluster.nodes))
		}
	}
	for i, node := range cluster.nodes {
		if err := node.WaitReady(operationCtx); err != nil {
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
	status, err := c.waitForStatus(operationCtx, func(status ClusterStatus) bool {
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
	c.setLastEvent("rollout: building configured artifact " + compilation.Revision)
	artifactPath := filepath.Join(c.runtimeDir, "application-active")
	inspection, err := artifact.EmbedFile(c.binaryPath, artifactPath, compilation)
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("build known-good application artifact: %w", err)
	}
	c.setLastEvent("rollout: starting three Grovlets")
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
		return rolloutActionResult{}, fmt.Errorf("connect to application cluster: %w", err)
	}
	defer transport.Close()
	c.setLastEvent("rollout: waiting for service placement")
	if err := waitForApplicationPlacement(ctx, transport, inspection.ArtifactDigest); err != nil {
		return rolloutActionResult{}, fmt.Errorf("wait for application placement: %w\n%s", err, applicationDiagnostics(cluster.nodes))
	}
	desired := applicationDesiredDeployment(inspection, "node-2")
	if err := putApplicationDesired(ctx, transport, desired); err != nil {
		return rolloutActionResult{}, err
	}
	record := applicationArtifactRecord(inspection)
	if err := transport.PutDeploymentArtifact(ctx, "node-1", record); err != nil {
		return rolloutActionResult{}, fmt.Errorf("record known-good artifact: %w", err)
	}
	c.setLastEvent("rollout: activating " + inspection.Config.Revision)
	active := applicationRollout(1, record.ClusterID, record.ArtifactDigest, "", systemnats.RolloutActive)
	if err := transport.PutRollout(ctx, "node-2", active); err != nil {
		return rolloutActionResult{}, fmt.Errorf("activate known-good rollout: %w", err)
	}
	c.mu.Lock()
	c.cluster = cluster
	c.lastEvent = "deployed " + inspection.Config.Revision
	c.mu.Unlock()
	status, err := c.waitForStatus(ctx, func(status ClusterStatus) bool {
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
		Transitions: []ClusterStatus{status}, Status: status,
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
	c.setLastEvent("rollout: building candidate " + compilation.Revision)
	candidatePath := filepath.Join(c.runtimeDir, "application-candidate")
	inspection, err := artifact.EmbedFile(c.binaryPath, candidatePath, compilation)
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("build candidate application artifact: %w", err)
	}
	transport, err := systemnats.Connect(ctx, cluster.systemNATSURL)
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("connect to application cluster: %w", err)
	}
	defer transport.Close()
	candidate := applicationArtifactRecord(inspection)
	c.setLastEvent("rollout: recording candidate " + inspection.Config.Revision)
	if err := transport.PutDeploymentArtifact(ctx, "node-1", candidate); err != nil {
		return rolloutActionResult{}, fmt.Errorf("record candidate artifact: %w", err)
	}
	pending := applicationRollout(2, candidate.ClusterID, cluster.artifact.ArtifactDigest, candidate.ArtifactDigest, systemnats.RolloutPending)
	if err := transport.PutRollout(ctx, "node-3", pending); err != nil {
		return rolloutActionResult{}, fmt.Errorf("record pending candidate: %w", err)
	}
	pendingStatus, err := c.waitForStatus(ctx, func(status ClusterStatus) bool {
		return status.Rollout != nil && status.Rollout.Phase == string(systemnats.RolloutPending) &&
			status.CandidateArtifact != nil && status.CandidateArtifact.ArtifactDigest == candidate.ArtifactDigest
	})
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("observe pending candidate: %w", err)
	}
	targetServiceID := activeApplication.Scenario.RecoveryServiceID
	targetComponent, _ := activeApplication.componentByID(targetServiceID)
	c.setLastEvent("rollout: starting candidate " + targetComponent.Name)
	candidateNode, err := grovetest.StartNode(
		candidatePath,
		"--node-id", "node-2-candidate",
		"--advertise-endpoint", "nats-subject://system/node-2-candidate",
		"--system-nats-url", cluster.systemNATSURL,
		"--system-nats-subject", "_GROVE.system.application.candidate."+targetComponent.Kind,
		"--component", targetComponent.Kind,
	)
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("start %s candidate: %w", targetComponent.Name, err)
	}
	defer candidateNode.Cleanup()
	candidateCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	readyErr := candidateNode.WaitReady(candidateCtx)
	cancel()
	if readyErr == nil {
		return rolloutActionResult{}, fmt.Errorf("invalid %s candidate became ready", targetComponent.Name)
	}
	currentPlacement, err := transport.RequestPlacement(ctx, "node-3")
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("read known-good placement: %w", err)
	}
	routes := applicationUpgradeRoutes(currentPlacement.Placements, candidate.ArtifactDigest)
	failure := systemnats.RolloutFailure{
		Code: "candidate_startup_failed", Component: targetComponent.Name,
		Field: validation.Field, Message: validation.Message,
	}
	c.setLastEvent("rollout: candidate unhealthy; rolling back")
	_, err = systemnats.NewDeployments().RollbackFailedUpgrade(ctx, transport, pending, routes, failure)
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("rollback failed candidate: %w", err)
	}
	status, err := c.waitForStatus(ctx, func(status ClusterStatus) bool {
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
		Transitions: []ClusterStatus{pendingStatus, status}, Status: status,
	}, nil
}

func compileApplicationConfiguration(path string) (artifact.Compilation, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return artifact.Compilation{}, fmt.Errorf("read configuration %q: %w", path, err)
	}
	configuration, err := activeApplication.Configuration.Compile(source)
	if err != nil {
		return artifact.Compilation{}, err
	}
	if err := validateApplicationConfiguration(configuration); err != nil {
		return artifact.Compilation{}, err
	}
	return artifact.Compilation{
		ProtocolVersion: artifact.CompilerProtocolVersion,
		Revision:        configuration.Revision,
		Encoding:        configuration.Encoding,
		Payload:         configuration.Payload,
		CanonicalYAML:   configuration.CanonicalYAML,
		Facts:           configuration.Facts,
	}, nil
}

type candidateValidation struct {
	Field   string
	Message string
}

func compileInvalidCandidateConfiguration(path string) (artifact.Compilation, candidateValidation, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return artifact.Compilation{}, candidateValidation{}, fmt.Errorf("read configuration %q: %w", path, err)
	}
	if activeApplication.Scenario == nil || activeApplication.Scenario.InvalidConfig == nil {
		return artifact.Compilation{}, candidateValidation{}, errCandidateMustFail
	}
	configuration, field, message, err := activeApplication.Scenario.InvalidConfig(source)
	if err != nil {
		return artifact.Compilation{}, candidateValidation{}, err
	}
	if field == "" || message == "" {
		return artifact.Compilation{}, candidateValidation{}, errCandidateMustFail
	}
	if err := validateApplicationConfiguration(configuration); err != nil {
		return artifact.Compilation{}, candidateValidation{}, err
	}
	return artifact.Compilation{
		ProtocolVersion: artifact.CompilerProtocolVersion,
		Revision:        configuration.Revision,
		Encoding:        configuration.Encoding,
		Payload:         configuration.Payload,
		CanonicalYAML:   configuration.CanonicalYAML,
		Facts:           configuration.Facts,
	}, candidateValidation{Field: field, Message: message}, nil
}

func startApplicationCluster(
	ctx context.Context,
	artifactPath string,
	inspection artifact.Inspection,
) (*applicationCluster, error) {
	nodeCount := activeApplication.scenarioNodeCount()
	routePorts, err := reserveApplicationPorts(nodeCount)
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
	extras := make([][]string, nodeCount)
	for _, placement := range activeApplication.scenarioInitialPlacements() {
		index := applicationNodeIndex(placement.NodeID)
		component, ok := activeApplication.componentByID(placement.ServiceID)
		if !ok || index < 0 || index >= len(extras) {
			cleanupApplicationNodes(cluster.nodes)
			return nil, fmt.Errorf("invalid application scenario placement: service %d on %q", placement.ServiceID, placement.NodeID)
		}
		extras[index] = append(extras[index], "--component", component.Kind)
		for _, option := range placement.Options {
			extras[index] = append(extras[index], "--component-option", component.Kind+"="+option)
		}
		if component.HTTPHandler != nil {
			extras[index] = append(extras[index], "--component-listen", component.Kind+"="+cluster.webAddress)
		}
	}
	for i, extra := range extras {
		seed := 0
		if i == 0 && len(extras) > 1 {
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
	want := make(map[grove.ServiceID]string, len(activeApplication.scenarioInitialPlacements()))
	for _, placement := range activeApplication.scenarioInitialPlacements() {
		want[placement.ServiceID] = placement.NodeID
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
		ready := clusterErr == nil && cluster.Ready && len(cluster.Nodes) == activeApplication.scenarioNodeCount() &&
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
	targetServiceID := activeApplication.Scenario.RecoveryServiceID
	components := make([]systemnats.DesiredComponent, 0, len(activeApplication.scenarioInitialPlacements()))
	for _, placement := range activeApplication.scenarioInitialPlacements() {
		nodeID := placement.NodeID
		if placement.ServiceID == targetServiceID {
			nodeID = inventoryNodeID
		}
		components = append(components, systemnats.DesiredComponent{ServiceID: placement.ServiceID, NodeID: nodeID})
	}
	return systemnats.DesiredDeployment{
		ApplicationID: inspection.Manifest.ApplicationID, Version: inspection.Manifest.CodeVersion,
		ArtifactDigest: inspection.ArtifactDigest,
		Components:     components,
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
	nodes := make([]systemnats.RolloutNodeProgress, activeApplication.scenarioNodeCount())
	for i := range nodes {
		nodes[i] = systemnats.RolloutNodeProgress{
			NodeID: fmt.Sprintf("node-%d", i+1), CurrentArtifactDigest: current,
			CandidateArtifactDigest: candidate, Phase: phase,
		}
	}
	return systemnats.Rollout{
		ApplicationID: activeApplicationName(), ClusterID: clusterID,
		RolloutID: activeApplicationName() + "-" + strconv.FormatUint(generation, 10), Generation: generation,
		CurrentArtifactDigest: current, CandidateArtifactDigest: candidate, Phase: phase, Nodes: nodes,
	}
}

func applicationUpgradeRoutes(current []systemnats.PlacementRecord, candidateDigest string) []systemnats.UpgradeRoute {
	routes := make([]systemnats.UpgradeRoute, len(current))
	for i, route := range current {
		nodeID := "node-1-candidate"
		if route.ServiceID == activeApplication.Scenario.RecoveryServiceID {
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
	serviceID grove.ServiceID,
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
		clusterRecovered := clusterErr == nil && cluster.Ready && len(cluster.Nodes) == activeApplication.scenarioNodeCount()
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
				if record.ServiceID == serviceID && record.NodeID != failedNodeID &&
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
					if component.ServiceID == serviceID && component.State == systemnats.ComponentHealthy {
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

func (c *applicationController) status(ctx context.Context) (ClusterStatus, error) {
	c.mu.RLock()
	cluster := c.cluster
	c.mu.RUnlock()
	if cluster == nil {
		return ClusterStatus{Health: "not-deployed", Nodes: []NodeStatus{}, Placements: []PlacementStatus{}}, nil
	}
	var status ClusterStatus
	if err := readApplicationJSON(ctx, "http://"+cluster.webAddress+"/grove/status", &status); err != nil {
		return ClusterStatus{}, err
	}
	return status, nil
}

func (c *applicationController) waitForStatus(ctx context.Context, accept func(ClusterStatus) bool) (ClusterStatus, error) {
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	var last ClusterStatus
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

func applicationStatusHealthy(status ClusterStatus, digest string) bool {
	if !status.Ready || status.Health != "healthy" || len(status.Nodes) != activeApplication.scenarioNodeCount() || len(status.Placements) != len(activeApplication.scenarioInitialPlacements()) ||
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

// waitForApplicationProbe proves that the recovered placement can serve the
// application call boundary, not merely that its component was observed as
// healthy by the control plane.
func waitForApplicationProbe(ctx context.Context, webAddress, probeID string) (any, error) {
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		result, err := activeApplication.Scenario.Probe(ctx, webAddress, probeID)
		if err == nil {
			return result, nil
		}
		lastErr = err
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for application probe %q: %w", probeID, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func (c *applicationController) readModel(ctx context.Context) (console.Model, error) {
	status, err := c.status(ctx)
	model := console.Model{
		Application:   activeApplication.Name,
		Sections:      []string{"Cluster", "Services", "Nodes", "Deployments", "Configuration", "Logs", "Debug", "Application"},
		Health:        status.Health,
		NodesTotal:    len(status.Nodes),
		ServicesTotal: len(status.Placements),
	}
	if err != nil {
		model.Health = "starting"
	}
	for _, node := range status.Nodes {
		if node.Health == string(systemnats.HealthHealthy) {
			model.NodesHealthy++
		}
	}
	for _, placement := range status.Placements {
		if placement.Health == string(systemnats.ComponentHealthy) || placement.Health == string(systemnats.ComponentDebugging) {
			model.ServicesHealthy++
		}
	}
	if status.ActiveArtifact != nil {
		model.ActiveVersion = status.ActiveArtifact.CodeVersion
		model.ConfigRevision = status.ActiveArtifact.ConfigRevision
	}
	if status.CandidateArtifact != nil {
		model.CandidateRevision = status.CandidateArtifact.ConfigRevision
	}
	if status.Rollout != nil {
		model.RolloutPhase = status.Rollout.Phase
	}
	c.mu.RLock()
	model.LastEvent = c.lastEvent
	if err != nil && model.LastEvent == "" {
		model.LastEvent = "waiting for Grove control-plane state: " + err.Error()
	}
	if c.cluster != nil && c.cluster.webAddress != "" {
		model.IngressURL = "http://" + c.cluster.webAddress
	}
	model.DebugSessions = copyDebugSessions(c.debugSessions)
	if c.startAvailable {
		model.StartupAction = "cluster.start"
	}
	if c.joinAvailable {
		model.StartupAction = "cluster.join"
		model.StartupNodes = c.startupNodes
		model.StartupStatus = "Healthy"
	}
	if model.StartupAction != "" {
		model.Application = activeApplication.Name
		model.StartupCluster = c.startup.Config.Facts["cluster.name"]
		model.StartupBuild = shortApplicationBuild(c.startup.ArtifactDigest)
	}
	c.mu.RUnlock()
	return model, nil
}

func shortApplicationBuild(digest string) string {
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) > 7 {
		return digest[:7]
	}
	return digest
}

func (c *applicationController) setLastEvent(event string) {
	c.mu.Lock()
	c.lastEvent = event
	c.mu.Unlock()
}

func cleanupApplicationNodes(nodes []*grovetest.Node) {
	for _, node := range nodes {
		_ = node.Cleanup()
	}
}

func gracefullyStopApplicationNodes(nodes []*grovetest.Node) {
	for _, node := range nodes {
		// A configured application node may spend up to gracefulLeaveTimeout
		// relocating services and evacuating its JetStream peers. Keep the
		// supervising console alive slightly longer so q cannot kill the child
		// halfway through that protocol and then remove it from discovery.
		ctx, cancel := context.WithTimeout(context.Background(), gracefulLeaveTimeout+5*time.Second)
		_ = node.Stop(ctx)
		cancel()
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
	discovery := c.discovery
	localNodeID := c.localNodeID
	c.cluster = nil
	c.discovery = nil
	c.localNodeID = ""
	clear(c.debugSessions)
	c.mu.Unlock()
	if cluster != nil {
		if discovery != nil {
			gracefullyStopApplicationNodes(cluster.nodes)
		} else {
			cleanupApplicationNodes(cluster.nodes)
		}
	}
	if discovery != nil && localNodeID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = discovery.removeNode(ctx, localNodeID)
		cancel()
	}
	if discovery != nil {
		discovery.close()
	}
}
