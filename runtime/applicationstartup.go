package runtime

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/nodeproc"
	"github.com/grove-project/grove/internal/systemnats"
)

var (
	errApplicationClusterUnreachable = errors.New("matching Grove cluster is unreachable")
	errApplicationJoinUnavailable    = errors.New("no same-artifact Grove cluster is available to join")
	errApplicationRolloutRequired    = errors.New("the discovered Grove cluster is running a different artifact; rollout is required")
	errApplicationNodeCount          = errors.New("invalid node count")
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
	webAddress, count, err := parseStartArguments(args)
	if err != nil {
		return nil, err
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
	if webAddress == "" {
		if webAddress, err = defaultIngressAddress(); err != nil {
			return nil, err
		}
	}
	node, ready, err := c.startDiscoveredApplicationNode(ctx, "node-1", "", webAddress)
	if err != nil {
		return nil, fmt.Errorf("bootstrap application cluster: %w", err)
	}
	founder := applicationDiscoveryNode{
		NodeID: "node-1", SystemNATSURL: ready.SystemNATSURL, RouteURL: ready.SystemNATSRouteURL,
	}
	discovered := applicationDiscoveryRecord{
		ProtocolVersion: applicationDiscoveryVersion,
		Revision:        1,
		ApplicationID:   inspection.Manifest.ApplicationID,
		ClusterID:       inspection.Config.Facts["cluster.name"],
		ArtifactDigest:  inspection.ArtifactDigest,
		WebAddress:      webAddress,
		NextNode:        2,
		Nodes:           []applicationDiscoveryNode{founder},
	}
	if err := discovery.publish(discovered); err != nil {
		_ = node.Cleanup()
		return nil, err
	}
	c.mu.Lock()
	c.cluster = &applicationCluster{
		nodes: []*nodeproc.Process{node}, systemNATSURL: ready.SystemNATSURL,
		webAddress: webAddress, artifact: inspection,
	}
	c.startAvailable = false
	c.startupNodes = 0
	c.localNodeIDs = []string{"node-1"}
	c.mu.Unlock()
	// The remaining nodes join through the founder exactly as a later Join
	// would, so the cluster reaches the size it needs to serve.
	started, err := c.addApplicationNodes(ctx, discovery, discovered, founder.RouteURL, count-1)
	started = append([]string{"node-1"}, started...)
	c.setLastEvent("bootstrapped " + activeApplication.Name + " cluster with " + strings.Join(started, ", "))
	if err != nil {
		return nil, fmt.Errorf("bootstrap application cluster after %s: %w", strings.Join(started, ", "), err)
	}
	return applicationJoinResult{State: "started", NodeIDs: started}, nil
}

// defaultIngressAddress is the ingress address offered when the operator does
// not choose one: a free loopback port.
func defaultIngressAddress() (string, error) {
	ports, err := reserveApplicationPorts(1)
	if err != nil {
		return "", fmt.Errorf("reserve ingress port: %w", err)
	}
	return "127.0.0.1:" + strconv.Itoa(ports[0]), nil
}

// parseStartArguments reads the optional ingress address and node count chosen
// for the new cluster. Without them the runtime picks a free ingress address
// and starts the smallest cluster that serves.
func parseStartArguments(args []string) (string, int, error) {
	flags := flag.NewFlagSet("cluster.start", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	address := ""
	flags.StringVar(&address, "ingress", "", "cluster ingress address")
	count := flags.Int("nodes", systemnats.MinClusterNodes, "nodes to start in this process")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return "", 0, errConsoleArguments
	}
	if *count < systemnats.MinClusterNodes {
		return "", 0, fmt.Errorf("%w: a new cluster needs at least %d nodes", errApplicationNodeCount, systemnats.MinClusterNodes)
	}
	return address, *count, nil
}

// parseJoinArguments reads how many nodes this process adds to the cluster.
func parseJoinArguments(args []string) (int, error) {
	flags := flag.NewFlagSet("cluster.join", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	count := flags.Int("nodes", 1, "nodes to add from this process")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return 0, errConsoleArguments
	}
	if *count < 1 {
		return 0, fmt.Errorf("%w: join at least one node", errApplicationNodeCount)
	}
	return *count, nil
}

func (c *applicationController) applicationJoinAvailable() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.joinAvailable
}

func (c *applicationController) joinApplicationCluster(ctx context.Context, args []string) (any, error) {
	count, err := parseJoinArguments(args)
	if err != nil {
		return nil, err
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
	joined, err := c.addApplicationNodes(ctx, discovery, record, seed.RouteURL, count)
	if len(joined) != 0 {
		c.mu.Lock()
		c.joinAvailable = false
		c.startupNodes = 0
		c.lastEvent = strings.Join(joined, ", ") + " joined the " + activeApplication.Name + " cluster"
		c.mu.Unlock()
	}
	if err != nil {
		return nil, err
	}
	return applicationJoinResult{State: "joined", NodeIDs: joined}, nil
}

// addApplicationNodes starts count nodes in this process, one at a time, each
// joining through seedRoute under the next unused node ID. Discovery is
// republished after every node so other processes always see the current
// membership. It returns the IDs of the nodes that joined, even on error.
func (c *applicationController) addApplicationNodes(
	ctx context.Context,
	discovery *applicationDiscovery,
	record applicationDiscoveryRecord,
	seedRoute string,
	count int,
) ([]string, error) {
	joined := make([]string, 0, count)
	for range count {
		nodeID := "node-" + strconv.Itoa(record.NextNode)
		node, ready, err := c.startDiscoveredApplicationNode(ctx, nodeID, seedRoute, record.WebAddress)
		if err != nil {
			return joined, fmt.Errorf("join application cluster as %s: %w", nodeID, err)
		}
		record.NextNode++
		record.Revision++
		record.Nodes = append(slices.Clone(record.Nodes), applicationDiscoveryNode{
			NodeID: nodeID, SystemNATSURL: ready.SystemNATSURL, RouteURL: ready.SystemNATSRouteURL,
		})
		if err := discovery.publish(record); err != nil {
			_ = node.Cleanup()
			return joined, err
		}
		c.mu.Lock()
		c.cluster.nodes = append(c.cluster.nodes, node)
		c.cluster.systemNATSURL = ready.SystemNATSURL
		c.localNodeIDs = append(c.localNodeIDs, nodeID)
		c.mu.Unlock()
		joined = append(joined, nodeID)
	}
	return joined, nil
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
) (*nodeproc.Process, lifecycleEvent, error) {
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
	// The node hosts whatever the runtime decides; only the founding node is
	// told the ingress address, which it records for the rest of the cluster.
	if seedRoute == "" {
		args = append(args, "--ingress-address", webAddress)
	}
	node, err := nodeproc.Start(c.binaryPath, args...)
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
	event, err := nodeproc.ReadyEvent(logs)
	if err != nil {
		return lifecycleEvent{}, err
	}
	if event.SystemNATSRouteURL == "" {
		return lifecycleEvent{}, errors.New("ready lifecycle event has no System NATS route URL")
	}
	return event, nil
}
