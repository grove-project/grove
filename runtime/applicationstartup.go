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
	"github.com/grove-project/grove/internal/localcluster"
	"github.com/grove-project/grove/internal/nodeproc"
	"github.com/grove-project/grove/internal/systemnats"
)

var (
	errApplicationClusterUnreachable = errors.New("matching Grove cluster is unreachable")
	errApplicationJoinUnavailable    = errors.New("no same-artifact Grove cluster is available to join")
	errApplicationRolloutRequired    = errors.New("the discovered Grove cluster is running a different artifact; rollout is required")
	errApplicationNodeCount          = errors.New("invalid node count")
)

type applicationJoinResult struct {
	State   string   `json:"state"`
	NodeIDs []string `json:"node_ids"`
}

// Summary describes the cluster this process entered.
func (r applicationJoinResult) Summary() string {
	return fmt.Sprintf("Cluster: %s\nNodes: %s", r.State, strings.Join(r.NodeIDs, ", "))
}

// applicationHost owns the nodes a configured application process hosts in
// its discovered cluster: finding the cluster, founding a new one, joining
// an existing one, and leaving it gracefully. Node processes are launched
// through internal/localcluster. Its methods are called with the console's
// operation lock held.
type applicationHost struct {
	binaryPath string
	discovery  *applicationDiscovery
	local      *localcluster.Cluster
}

// applicationStartupOffer is what discovery found for this process's
// artifact: nothing (start is offered), a same-artifact cluster (join is
// offered) or a cluster running another artifact.
type applicationStartupOffer struct {
	discovered    bool
	join          bool
	nodes         int
	systemNATSURL string
	webAddress    string
	// nodeIDs are the discovered nodes, the reachable one first.
	nodeIDs []string
}

func newApplicationHost(binaryPath string) *applicationHost {
	return &applicationHost{binaryPath: binaryPath, local: localcluster.New()}
}

// prepare starts discovery for inspection's application and cluster and
// reports what it found.
func (h *applicationHost) prepare(ctx context.Context, inspection artifact.Inspection) (applicationStartupOffer, error) {
	clusterID := inspection.Config.Facts["cluster.name"]
	discovery, err := newApplicationDiscovery(inspection.Manifest.ApplicationID, clusterID)
	if err != nil {
		return applicationStartupOffer{}, err
	}
	h.discovery = discovery
	record, exists, err := discovery.discover(ctx)
	if err != nil || !exists {
		return applicationStartupOffer{}, err
	}
	if record.ApplicationID != inspection.Manifest.ApplicationID || record.ClusterID != clusterID {
		return applicationStartupOffer{}, errApplicationDiscoveryInvalid
	}
	reachable, err := reachableApplicationDiscoveryNode(ctx, record)
	if err != nil {
		return applicationStartupOffer{}, err
	}
	nodeIDs := []string{reachable.NodeID}
	for _, node := range record.Nodes {
		if node.NodeID != reachable.NodeID {
			nodeIDs = append(nodeIDs, node.NodeID)
		}
	}
	return applicationStartupOffer{
		discovered: true, join: record.ArtifactDigest == inspection.ArtifactDigest, nodes: len(record.Nodes),
		systemNATSURL: reachable.SystemNATSURL, webAddress: record.WebAddress, nodeIDs: nodeIDs,
	}, nil
}

// start founds a new cluster for inspection with count nodes hosted here,
// its ingress on webAddress. joined is called after each node joins with
// the System NATS URL to reach the cluster through. It returns the IDs of
// the nodes that joined, even on error.
func (h *applicationHost) start(
	ctx context.Context,
	inspection artifact.Inspection,
	webAddress string,
	count int,
	joined func(nodeID, systemNATSURL string),
) ([]string, error) {
	if h.discovery == nil {
		return nil, errors.New("a new application cluster is not available to start")
	}
	if _, exists, err := h.discovery.discover(ctx); err != nil {
		return nil, err
	} else if exists {
		return nil, errors.New("a compatible application cluster was discovered; start is no longer available")
	}
	node, ready, err := h.startNode(ctx, "node-1", "", webAddress)
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
	if err := h.discovery.publish(discovered); err != nil {
		_ = node.Cleanup()
		return nil, err
	}
	h.local.Add(founder.NodeID, node, ready.SystemNATSURL)
	joined(founder.NodeID, ready.SystemNATSURL)
	// The remaining nodes join through the founder exactly as a later Join
	// would, so the cluster reaches the size it needs to serve.
	started, err := h.addNodes(ctx, discovered, founder.RouteURL, count-1, joined)
	started = append([]string{founder.NodeID}, started...)
	if err != nil {
		return started, fmt.Errorf("bootstrap application cluster after %s: %w", strings.Join(started, ", "), err)
	}
	return started, nil
}

// join adds count nodes hosted here to the discovered cluster running
// inspection's artifact. joined and the result are as for start.
func (h *applicationHost) join(
	ctx context.Context,
	inspection artifact.Inspection,
	count int,
	joined func(nodeID, systemNATSURL string),
) ([]string, error) {
	if h.discovery == nil {
		return nil, errApplicationJoinUnavailable
	}
	record, exists, err := h.discovery.discover(ctx)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errApplicationJoinUnavailable
	}
	if record.ApplicationID != inspection.Manifest.ApplicationID ||
		record.ClusterID != inspection.Config.Facts["cluster.name"] {
		return nil, errApplicationDiscoveryInvalid
	}
	if record.ArtifactDigest != inspection.ArtifactDigest {
		return nil, errApplicationRolloutRequired
	}
	seed, err := reachableApplicationDiscoveryNode(ctx, record)
	if err != nil {
		return nil, err
	}
	return h.addNodes(ctx, record, seed.RouteURL, count, joined)
}

// addNodes starts count nodes here, one at a time, each joining through
// seedRoute under the next unused node ID. Discovery is republished after
// every node so other processes always see the current membership. It
// returns the IDs of the nodes that joined, even on error.
func (h *applicationHost) addNodes(
	ctx context.Context,
	record applicationDiscoveryRecord,
	seedRoute string,
	count int,
	joined func(nodeID, systemNATSURL string),
) ([]string, error) {
	added := make([]string, 0, count)
	for range count {
		nodeID := "node-" + strconv.Itoa(record.NextNode)
		node, ready, err := h.startNode(ctx, nodeID, seedRoute, record.WebAddress)
		if err != nil {
			return added, fmt.Errorf("join application cluster as %s: %w", nodeID, err)
		}
		record.NextNode++
		record.Revision++
		record.Nodes = append(slices.Clone(record.Nodes), applicationDiscoveryNode{
			NodeID: nodeID, SystemNATSURL: ready.SystemNATSURL, RouteURL: ready.SystemNATSRouteURL,
		})
		if err := h.discovery.publish(record); err != nil {
			_ = node.Cleanup()
			return added, err
		}
		h.local.Add(nodeID, node, ready.SystemNATSURL)
		joined(nodeID, ready.SystemNATSURL)
		added = append(added, nodeID)
	}
	return added, nil
}

// startNode starts one node of the discovered cluster. Only the founding
// node, which has no seed route, is told the ingress address; it records it
// for the rest of the cluster.
func (h *applicationHost) startNode(ctx context.Context, nodeID, seedRoute, webAddress string) (*nodeproc.Process, nodeproc.Event, error) {
	spec := localcluster.NodeSpec{
		NodeID: nodeID, Subject: "_GROVE.system.application." + nodeID, SeedRoute: seedRoute,
		Membership: true, Recovery: true, RetireOnStop: true,
	}
	if seedRoute == "" {
		spec.IngressAddress = webAddress
	}
	node, ready, err := localcluster.StartNode(ctx, h.binaryPath, spec)
	if err != nil {
		return nil, nodeproc.Event{}, err
	}
	if ready.SystemNATSRouteURL == "" {
		_ = node.Cleanup()
		return nil, nodeproc.Event{}, fmt.Errorf("read %s readiness: ready lifecycle event has no System NATS route URL", nodeID)
	}
	return node, ready, nil
}

// hosting reports whether this process hosts nodes of a discovered cluster.
func (h *applicationHost) hosting() bool {
	return h.local.Len() != 0
}

// leave stops the hosted nodes gracefully, removes them from discovery and
// stops discovery.
func (h *applicationHost) leave() {
	// A configured application node may spend up to gracefulLeaveTimeout
	// relocating services and evacuating its JetStream peers. Keep the
	// supervising console alive slightly longer so q cannot kill the child
	// halfway through that protocol and then remove it from discovery.
	h.local.Leave(gracefulLeaveTimeout + 5*time.Second)
	if h.discovery != nil {
		for _, nodeID := range h.local.NodeIDs() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = h.discovery.removeNode(ctx, nodeID)
			cancel()
		}
	}
	h.close()
}

// close stops discovery.
func (h *applicationHost) close() {
	if h.discovery != nil {
		h.discovery.close()
	}
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

// defaultIngressAddress is the ingress address offered when the operator does
// not choose one: a free loopback port.
func defaultIngressAddress() (string, error) {
	ports, err := localcluster.ReservePorts(1)
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

// prepareConfiguredApplicationStartup discovers this artifact's cluster and
// offers Start or Join accordingly.
func (c *applicationController) prepareConfiguredApplicationStartup(
	ctx context.Context,
	inspection artifact.Inspection,
) error {
	c.mu.Lock()
	c.startup = inspection
	c.mu.Unlock()
	offer, err := c.host.prepare(ctx, inspection)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !offer.discovered {
		c.startAvailable = true
		c.lastEvent = "No " + activeApplication.Name + " cluster discovered"
		return nil
	}
	c.cluster = &applicationCluster{
		systemNATSURL:   offer.systemNATSURL,
		webAddress:      offer.webAddress,
		artifact:        inspection,
		discoveredNodes: offer.nodeIDs,
	}
	if offer.join {
		c.joinAvailable = true
		c.startupNodes = offer.nodes
		c.lastEvent = "matching " + activeApplication.Name + " cluster discovered; Join is the default action"
	} else {
		c.lastEvent = activeApplication.Name + " cluster discovered with a different artifact; rollout is required"
	}
	return nil
}

func (c *applicationController) applicationStartAvailable() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.startAvailable
}

func (c *applicationController) applicationJoinAvailable() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.joinAvailable
}

// hostedNodeJoined returns the callback that attaches the console to the
// cluster this process hosts nodes in, as each node joins it.
func (c *applicationController) hostedNodeJoined(inspection artifact.Inspection, webAddress string) func(string, string) {
	return func(_, systemNATSURL string) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.cluster == nil {
			c.cluster = &applicationCluster{webAddress: webAddress, artifact: inspection}
		}
		c.cluster.local = c.host.local
		c.cluster.systemNATSURL = systemNATSURL
	}
}

func (c *applicationController) startDiscoveredApplicationCluster(ctx context.Context, args []string) (any, error) {
	webAddress, count, err := parseStartArguments(args)
	if err != nil {
		return nil, err
	}
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	c.mu.RLock()
	inspection := c.startup
	startAvailable := c.startAvailable
	c.mu.RUnlock()
	if inspection.Config == nil || !startAvailable {
		return nil, errors.New("a new application cluster is not available to start")
	}
	if webAddress == "" {
		if webAddress, err = defaultIngressAddress(); err != nil {
			return nil, err
		}
	}
	joined := c.hostedNodeJoined(inspection, webAddress)
	started, err := c.host.start(ctx, inspection, webAddress, count, func(nodeID, systemNATSURL string) {
		joined(nodeID, systemNATSURL)
		c.mu.Lock()
		c.startAvailable = false
		c.startupNodes = 0
		c.mu.Unlock()
	})
	if len(started) != 0 {
		c.setLastEvent("bootstrapped " + activeApplication.Name + " cluster with " + strings.Join(started, ", "))
	}
	if err != nil {
		return nil, err
	}
	return applicationJoinResult{State: "started", NodeIDs: started}, nil
}

func (c *applicationController) joinApplicationCluster(ctx context.Context, args []string) (any, error) {
	count, err := parseJoinArguments(args)
	if err != nil {
		return nil, err
	}
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	c.mu.RLock()
	cluster := c.cluster
	joinAvailable := c.joinAvailable
	c.mu.RUnlock()
	if cluster == nil || !joinAvailable {
		return nil, errApplicationJoinUnavailable
	}
	joined, err := c.host.join(ctx, cluster.artifact, count, c.hostedNodeJoined(cluster.artifact, cluster.webAddress))
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
