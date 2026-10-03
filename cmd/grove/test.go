package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/nodeproc"
	"github.com/grove-project/grove/internal/scenario"
	"github.com/grove-project/grove/internal/systemnats"
)

const (
	testCommandTimeout    = 60 * time.Second
	testConditionInterval = 25 * time.Millisecond
)

// commandTestCluster is the cluster `grove test` runs an application's
// end-to-end check against: one node per check component, in order, and a
// final check node that hosts no component and routes the check's calls.
type commandTestCluster struct {
	binaryPath    string
	nodes         []*nodeproc.Process
	components    []scenario.Component
	systemNATSURL string
}

func (c *commandTestCluster) checkNodeID() string {
	return fmt.Sprintf("node-%d", len(c.nodes))
}

func executeTest(ctx context.Context, parsed invocation, output io.Writer) error {
	inspection, err := artifact.InspectFile(parsed.binaryPath)
	if err != nil {
		return fmt.Errorf("inspect test artifact: %w", err)
	}
	description, err := describeApplicationScenario(ctx, parsed.binaryPath)
	if err != nil {
		return fmt.Errorf("test artifact application %q: %w", inspection.Manifest.ApplicationID, errors.Join(errTestApplication, err))
	}
	if len(description.CheckComponents) == 0 {
		return fmt.Errorf("test artifact application %q: %w", inspection.Manifest.ApplicationID, errors.Join(errTestApplication, scenario.ErrNoCheck))
	}
	serviceID := parsed.serviceID
	if serviceID == 0 {
		serviceID = description.RecoveryServiceID
	}
	if parsed.resilience && serviceID == 0 {
		return fmt.Errorf("test artifact application %q declares no recovery service: %w", inspection.Manifest.ApplicationID, errServiceIDRequired)
	}
	cluster, err := startCommandTestCluster(ctx, parsed.binaryPath, description.CheckComponents, parsed.resilience)
	if err != nil {
		return err
	}
	nodes := cluster.nodes
	fail := func(operation string, operationErr error) error {
		diagnostics := commandTestDiagnostics(nodes)
		cleanupErr := cleanupCommandTestNodes(nodes)
		return fmt.Errorf("%s: %w\n%s", operation, errors.Join(operationErr, cleanupErr), diagnostics)
	}
	transport, err := systemnats.Connect(ctx, cluster.systemNATSURL)
	if err != nil {
		return fail("connect to test cluster", err)
	}
	if err := waitForCommandTestReady(ctx, transport, cluster, inspection.ArtifactDigest); err != nil {
		transport.Close()
		return fail("wait for test cluster", err)
	}
	summary, err := runCommandTestCheck(ctx, cluster, "grove-test-order")
	if err != nil {
		transport.Close()
		return fail("run "+description.Name+" check", err)
	}
	failedNodeID := ""
	recoveredNodeID := ""
	if parsed.resilience {
		failedNodeID, recoveredNodeID, transport, err = runCommandTestResilience(
			ctx,
			transport,
			cluster,
			serviceID,
			inspection.ArtifactDigest,
		)
		if err != nil {
			return fail("run resilience scenario", err)
		}
		if _, err := runCommandTestCheck(ctx, cluster, "grove-test-order-after-recovery"); err != nil {
			transport.Close()
			return fail("rerun "+description.Name+" check", err)
		}
	}
	transport.Close()
	if err := stopCommandTestNodes(nodes, failedNodeID); err != nil {
		return fail("stop test cluster", err)
	}
	if err := cleanupCommandTestNodes(nodes); err != nil {
		return fmt.Errorf("clean up test cluster: %w", err)
	}
	transcript := fmt.Sprintf("%s E2E\n✓ cluster ready\n✓ %s\nPASS\n", description.Name, summary)
	if parsed.resilience {
		transcript = fmt.Sprintf(
			"%s E2E\n✓ baseline\nKilled service %d host %s\n✓ failure detected\n✓ service %d recovered on %s\n✓ flow after recovery\nPASS\n",
			description.Name,
			serviceID,
			failedNodeID,
			serviceID,
			recoveredNodeID,
		)
	}
	_, err = io.WriteString(output, transcript)
	return err
}

// describeApplicationScenario asks the application binary for its scenario,
// so the CLI never links application code.
func describeApplicationScenario(ctx context.Context, binaryPath string) (scenario.Description, error) {
	var description scenario.Description
	if err := runApplicationScenarioCommand(ctx, binaryPath, &description, scenario.DescribeCommand); err != nil {
		return scenario.Description{}, err
	}
	if description.ProtocolVersion != scenario.ProtocolVersion {
		return scenario.Description{}, fmt.Errorf("scenario protocol version %d is unsupported", description.ProtocolVersion)
	}
	return description, nil
}

// runCommandTestCheck runs the application's own end-to-end check, inside
// the application binary, against the cluster through the check node.
func runCommandTestCheck(ctx context.Context, cluster *commandTestCluster, runID string) (string, error) {
	var result scenario.CheckResult
	if err := runApplicationScenarioCommand(
		ctx,
		cluster.binaryPath,
		&result,
		scenario.CheckCommand,
		"--system-nats-url", cluster.systemNATSURL,
		"--node-id", cluster.checkNodeID(),
		"--run-id", runID,
	); err != nil {
		return "", err
	}
	if result.ProtocolVersion != scenario.ProtocolVersion {
		return "", fmt.Errorf("scenario protocol version %d is unsupported", result.ProtocolVersion)
	}
	return result.Summary, nil
}

func runApplicationScenarioCommand(ctx context.Context, binaryPath string, response any, args ...string) error {
	command := exec.CommandContext(ctx, binaryPath, append([]string{scenario.Command}, args...)...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("run %s %s: %w: %s", scenario.Command, args[0], err, strings.TrimSpace(stderr.String()))
	}
	if err := json.Unmarshal(stdout.Bytes(), response); err != nil {
		return fmt.Errorf("decode %s %s response: %w", scenario.Command, args[0], err)
	}
	return nil
}

func runCommandTestResilience(
	ctx context.Context,
	currentTransport *systemnats.Transport,
	cluster *commandTestCluster,
	serviceID grove.ServiceID,
	artifactDigest string,
) (string, string, *systemnats.Transport, error) {
	nodes := cluster.nodes
	placement, err := currentTransport.RequestPlacement(ctx, cluster.checkNodeID())
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
	cluster.systemNATSURL = survivorURL
	transport, err := systemnats.Connect(ctx, survivorURL)
	if err != nil {
		return targetNodeID, "", nil, err
	}
	recoveredNodeID, err := waitForCommandTestRecovery(ctx, transport, cluster.checkNodeID(), len(nodes), targetNodeID, serviceID, artifactDigest)
	if err != nil {
		transport.Close()
		return targetNodeID, "", nil, err
	}
	return targetNodeID, recoveredNodeID, transport, nil
}

func commandTestNodeIndex(nodeID string, nodes []*nodeproc.Process) int {
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
	observerNodeID string,
	nodeCount int,
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
		cluster, clusterErr := transport.RequestClusterView(attemptCtx, observerNodeID)
		placement, placementErr := transport.RequestPlacement(attemptCtx, observerNodeID)
		cancel()
		if clusterErr == nil {
			lastCluster = cluster
		}
		if placementErr == nil {
			lastPlacement = placement
		}
		clusterRecovered := clusterErr == nil && cluster.Ready && len(cluster.Nodes) == nodeCount
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

func startCommandTestCluster(ctx context.Context, binaryPath string, components []scenario.Component, resilience bool) (*commandTestCluster, error) {
	nodeCount := len(components) + 1
	ports, err := reserveCommandTestPorts(nodeCount)
	if err != nil {
		return nil, fmt.Errorf("reserve test cluster ports: %w", err)
	}
	cluster := &commandTestCluster{binaryPath: binaryPath, components: components}
	for i := range nodeCount {
		nodeID := fmt.Sprintf("node-%d", i+1)
		extra := []string{"--system-nats-subject", "_GROVE.system.test." + nodeID}
		if i < len(components) {
			extra = append(extra, "--component", components[i].Kind)
		}
		if resilience {
			extra = append(extra, "--system-nats-recovery")
		}
		seed := 0
		if i == 0 {
			seed = 1
		}
		args := []string{
			"--node-id", nodeID,
			"--advertise-endpoint", "nats-subject://system/" + nodeID,
			"--system-nats-listen", "127.0.0.1:0",
			"--system-nats-route-listen", "127.0.0.1:" + strconv.Itoa(ports[i]),
			"--system-nats-seed", "nats-route://127.0.0.1:" + strconv.Itoa(ports[seed]),
			"--system-nats-membership",
		}
		node, err := nodeproc.Start(binaryPath, append(args, extra...)...)
		if err != nil {
			_ = cleanupCommandTestNodes(cluster.nodes)
			return nil, fmt.Errorf("start test %s: %w", nodeID, err)
		}
		cluster.nodes = append(cluster.nodes, node)
	}
	for _, node := range cluster.nodes {
		if err := node.WaitReady(ctx); err != nil {
			diagnostics := commandTestDiagnostics(cluster.nodes)
			_ = cleanupCommandTestNodes(cluster.nodes)
			return nil, fmt.Errorf("wait for test Grovlets: %w\n%s", err, diagnostics)
		}
	}
	systemNATSURL, err := commandTestSystemNATSURL(cluster.nodes[0].Logs())
	if err != nil {
		diagnostics := commandTestDiagnostics(cluster.nodes)
		_ = cleanupCommandTestNodes(cluster.nodes)
		return nil, fmt.Errorf("read test System NATS URL: %w\n%s", err, diagnostics)
	}
	cluster.systemNATSURL = systemNATSURL
	return cluster, nil
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
	event, err := nodeproc.ReadyEvent(logs)
	if err != nil {
		return "", err
	}
	return event.SystemNATSURL, nil
}

func waitForCommandTestReady(ctx context.Context, transport *systemnats.Transport, testCluster *commandTestCluster, artifactDigest string) error {
	checkNodeID := testCluster.checkNodeID()
	nodeCount := len(testCluster.nodes)
	ticker := time.NewTicker(testConditionInterval)
	defer ticker.Stop()
	var lastCluster systemnats.ClusterView
	var lastPlacement systemnats.PlacementView
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, time.Second)
		var attemptErr error
		cluster, clusterErr := transport.RequestClusterView(attemptCtx, checkNodeID)
		placement, placementErr := transport.RequestPlacement(attemptCtx, checkNodeID)
		if clusterErr == nil {
			lastCluster = cluster
		}
		if placementErr == nil {
			lastPlacement = placement
		}
		nodesHealthy := clusterErr == nil && cluster.Ready && len(cluster.Nodes) == nodeCount
		for _, node := range cluster.Nodes {
			nodesHealthy = nodesHealthy && node.Health == systemnats.HealthHealthy
		}
		placementsReady := placementErr == nil && placement.Ready && len(placement.Placements) == len(testCluster.components)
		expectedServices := make(map[grove.ServiceID]bool, len(testCluster.components))
		for _, component := range testCluster.components {
			expectedServices[component.ServiceID] = false
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
		// Every node gates its own views on its own control-plane state, so the
		// cluster serves only once each node reports ready, not just the check
		// node.
		allNodesServing := nodesHealthy && placementsReady
		for i := 1; allNodesServing && i <= nodeCount; i++ {
			nodeID := fmt.Sprintf("node-%d", i)
			nodeCluster, err := transport.RequestClusterView(attemptCtx, nodeID)
			if err != nil || !nodeCluster.Ready {
				attemptErr = errors.Join(attemptErr, err)
				allNodesServing = false
				break
			}
			nodePlacement, err := transport.RequestPlacement(attemptCtx, nodeID)
			if err != nil || !nodePlacement.Ready {
				attemptErr = errors.Join(attemptErr, err)
				allNodesServing = false
			}
		}
		cancel()
		if allNodesServing && componentsHealthy && componentCount == len(testCluster.components) && placementsReady {
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

func stopCommandTestNodes(nodes []*nodeproc.Process, skipNodeID string) error {
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

func cleanupCommandTestNodes(nodes []*nodeproc.Process) error {
	var cleanupErr error
	for i, node := range nodes {
		if err := node.Cleanup(); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("node-%d: %w", i+1, err))
		}
	}
	return cleanupErr
}

func commandTestDiagnostics(nodes []*nodeproc.Process) string {
	var diagnostics strings.Builder
	for i, node := range nodes {
		fmt.Fprintf(&diagnostics, "node-%d logs:\n%s", i+1, node.Logs())
		if !strings.HasSuffix(node.Logs(), "\n") {
			diagnostics.WriteByte('\n')
		}
	}
	return diagnostics.String()
}
