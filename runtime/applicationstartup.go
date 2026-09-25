package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/grove-project/grove/grovetest"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/systemnats"
)

var (
	errApplicationClusterUnreachable = errors.New("matching Grove cluster is unreachable")
	errApplicationJoinUnavailable    = errors.New("no same-artifact Grove cluster is available to join")
	errApplicationRolloutRequired    = errors.New("the discovered Grove cluster is running a different artifact; rollout is required")
)

func (c *applicationController) prepareConfiguredApplicationStartup(
	ctx context.Context,
	inspection artifact.Inspection,
) error {
	clusterID := inspection.Config.Facts["cluster.name"]
	discovery, err := newApplicationDiscovery(inspection.Manifest.ApplicationID, clusterID)
	if err != nil {
		return err
	}
	c.discovery = discovery
	c.startup = inspection
	record, exists, err := discovery.discover(ctx)
	if err != nil {
		return err
	}
	if exists {
		if record.ApplicationID != inspection.Manifest.ApplicationID || record.ClusterID != clusterID {
			return errApplicationDiscoveryInvalid
		}
		reachable, err := reachableApplicationDiscoveryNode(ctx, record)
		if err != nil {
			return err
		}
		c.mu.Lock()
		c.cluster = &applicationCluster{
			systemNATSURL: reachable.SystemNATSURL,
			webAddress:    record.WebAddress,
			artifact:      inspection,
		}
		if record.ArtifactDigest == inspection.ArtifactDigest {
			c.joinAvailable = true
			c.startupNodes = len(record.Nodes)
			c.lastEvent = "matching " + activeApplication.Name + " cluster discovered; Join is the default action"
		} else {
			c.lastEvent = activeApplication.Name + " cluster discovered with a different artifact; rollout is required"
		}
		c.mu.Unlock()
		return nil
	}
	c.mu.Lock()
	c.startAvailable = true
	c.lastEvent = "No " + activeApplication.Name + " cluster discovered"
	c.mu.Unlock()
	return nil
}

func (c *applicationController) applicationStartAvailable() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.startAvailable
}

func (c *applicationController) startDiscoveredApplicationCluster(ctx context.Context, args []string) (any, error) {
	if len(args) != 0 {
		return nil, errConsoleArguments
	}
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	c.mu.RLock()
	discovery := c.discovery
	inspection := c.startup
	startAvailable := c.startAvailable
	c.mu.RUnlock()
	if discovery == nil || inspection.Config == nil || !startAvailable {
		return nil, errors.New("a new application cluster is not available to start")
	}
	if _, exists, err := discovery.discover(ctx); err != nil {
		return nil, err
	} else if exists {
		return nil, errors.New("a compatible application cluster was discovered; start is no longer available")
	}
	webPorts, err := reserveApplicationPorts(1)
	if err != nil {
		return nil, fmt.Errorf("reserve bootstrap Web port: %w", err)
	}
	webAddress := "127.0.0.1:" + strconv.Itoa(webPorts[0])
	node, ready, err := c.startDiscoveredApplicationNode(ctx, "node-1", "", webAddress)
	if err != nil {
		return nil, fmt.Errorf("bootstrap one-node application cluster: %w", err)
	}
	discovered := applicationDiscoveryRecord{
		ProtocolVersion: applicationDiscoveryVersion,
		Revision:        1,
		ApplicationID:   inspection.Manifest.ApplicationID,
		ClusterID:       inspection.Config.Facts["cluster.name"],
		ArtifactDigest:  inspection.ArtifactDigest,
		WebAddress:      webAddress,
		NextNode:        2,
		Nodes: []applicationDiscoveryNode{{
			NodeID: "node-1", SystemNATSURL: ready.SystemNATSURL, RouteURL: ready.SystemNATSRouteURL,
		}},
	}
	if err := discovery.publish(discovered); err != nil {
		_ = node.Cleanup()
		return nil, err
	}
	c.mu.Lock()
	c.cluster = &applicationCluster{
		nodes: []*grovetest.Node{node}, systemNATSURL: ready.SystemNATSURL,
		webAddress: webAddress, artifact: inspection,
	}
	c.startAvailable = false
	c.startupNodes = 0
	c.localNodeID = "node-1"
	c.lastEvent = "bootstrapped " + activeApplication.Name + " cluster with node-1"
	c.mu.Unlock()
	return applicationJoinResult{State: "started", NodeID: "node-1"}, nil
}

func (c *applicationController) applicationJoinAvailable() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.joinAvailable
}

func (c *applicationController) joinApplicationCluster(ctx context.Context, args []string) (any, error) {
	if len(args) != 0 {
		return nil, errConsoleArguments
	}
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	c.mu.RLock()
	discovery := c.discovery
	inspection := c.cluster
	joinAvailable := c.joinAvailable
	c.mu.RUnlock()
	if discovery == nil || inspection == nil || !joinAvailable {
		return nil, errApplicationJoinUnavailable
	}
	record, exists, err := discovery.discover(ctx)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errApplicationJoinUnavailable
	}
	if record.ApplicationID != inspection.artifact.Manifest.ApplicationID ||
		record.ClusterID != inspection.artifact.Config.Facts["cluster.name"] {
		return nil, errApplicationDiscoveryInvalid
	}
	if record.ArtifactDigest != inspection.artifact.ArtifactDigest {
		return nil, errApplicationRolloutRequired
	}
	seed, err := reachableApplicationDiscoveryNode(ctx, record)
	if err != nil {
		return nil, err
	}
	nodeNumber := record.NextNode
	nodeID := "node-" + strconv.Itoa(nodeNumber)
	node, ready, err := c.startDiscoveredApplicationNode(ctx, nodeID, seed.RouteURL, record.WebAddress)
	if err != nil {
		return nil, fmt.Errorf("join application cluster as %s: %w", nodeID, err)
	}
	record.NextNode++
	record.Revision++
	record.Nodes = append(record.Nodes, applicationDiscoveryNode{
		NodeID: nodeID, SystemNATSURL: ready.SystemNATSURL, RouteURL: ready.SystemNATSRouteURL,
	})
	if err := discovery.publish(record); err != nil {
		_ = node.Cleanup()
		return nil, err
	}
	c.mu.Lock()
	c.cluster.nodes = append(c.cluster.nodes, node)
	c.cluster.systemNATSURL = ready.SystemNATSURL
	c.localNodeID = nodeID
	c.joinAvailable = false
	c.startupNodes = 0
	c.lastEvent = nodeID + " joined the " + activeApplication.Name + " cluster"
	c.mu.Unlock()
	return applicationJoinResult{State: "joined", NodeID: nodeID}, nil
}

func reachableApplicationDiscoveryNode(
	ctx context.Context,
	record applicationDiscoveryRecord,
) (applicationDiscoveryNode, error) {
	var lastErr error
	for _, node := range record.Nodes {
		probeCtx, cancel := context.WithTimeout(ctx, time.Second)
		transport, err := systemnats.Connect(probeCtx, node.SystemNATSURL)
		cancel()
		if err == nil {
			transport.Close()
			return node, nil
		}
		lastErr = errors.Join(lastErr, fmt.Errorf("%s: %w", node.NodeID, err))
	}
	return applicationDiscoveryNode{}, fmt.Errorf("%w: %w", errApplicationClusterUnreachable, lastErr)
}

func (c *applicationController) startDiscoveredApplicationNode(
	ctx context.Context,
	nodeID string,
	seedRoute string,
	webAddress string,
) (*grovetest.Node, lifecycleEvent, error) {
	args := []string{
		"--node-id", nodeID,
		"--advertise-endpoint", "nats-subject://system/" + nodeID,
		"--system-nats-listen", "127.0.0.1:0",
		"--system-nats-route-listen", "127.0.0.1:0",
		"--system-nats-membership",
		"--system-nats-recovery",
		"--system-nats-retire-on-stop",
		"--system-nats-subject", "_GROVE.system.application." + nodeID,
	}
	if seedRoute != "" {
		args = append(args, "--system-nats-seed", seedRoute)
	}
	for _, placement := range activeApplication.scenarioStartupComponents() {
		if placement.NodeID != nodeID {
			continue
		}
		component, ok := activeApplication.componentByID(placement.ServiceID)
		if !ok {
			return nil, lifecycleEvent{}, fmt.Errorf("startup component %d is not defined", placement.ServiceID)
		}
		args = append(args, "--component", component.Kind)
		for _, option := range placement.Options {
			args = append(args, "--component-option", component.Kind+"="+option)
		}
		if component.HTTPHandler != nil {
			args = append(args, "--component-listen", component.Kind+"="+webAddress)
		}
	}
	node, err := grovetest.StartNode(c.binaryPath, args...)
	if err != nil {
		return nil, lifecycleEvent{}, err
	}
	if err := node.WaitReady(ctx); err != nil {
		_ = node.Cleanup()
		return nil, lifecycleEvent{}, err
	}
	ready, err := applicationReadyEvent(node.Logs())
	if err != nil {
		_ = node.Cleanup()
		return nil, lifecycleEvent{}, fmt.Errorf("read %s readiness: %w", nodeID, err)
	}
	return node, ready, nil
}

func applicationReadyEvent(logs string) (lifecycleEvent, error) {
	decoder := json.NewDecoder(strings.NewReader(logs))
	for {
		var event lifecycleEvent
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				return lifecycleEvent{}, errors.New("ready lifecycle event is missing")
			}
			return lifecycleEvent{}, err
		}
		if event.Event == "ready" && event.SystemNATSURL != "" && event.SystemNATSRouteURL != "" {
			return event, nil
		}
	}
}
