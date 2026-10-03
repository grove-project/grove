// Package localcluster launches and supervises Grove clusters whose nodes are
// processes on this machine.
//
// It is the one owner of local cluster lifecycle: how a node is started
// (its flags, seed route and System NATS subject), how a fixed topology of
// nodes is brought up together, and how those nodes are killed, restarted,
// stopped and cleaned up. The operator console, `grove test` and
// `grove deploy` all launch nodes through it; each node process itself is
// supervised by internal/nodeproc.
package localcluster

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grove-project/grove/internal/nodeproc"
)

// ErrInvalidTopology is returned when a Spec places a component on a node
// outside the topology, or declares no nodes.
var ErrInvalidTopology = errors.New("invalid local cluster topology")

// NodeSpec describes how one Grovlet node process is started.
type NodeSpec struct {
	// NodeID is the node's identity, such as "node-1".
	NodeID string
	// Subject is the node's System NATS request subject.
	Subject string
	// SystemNATSURL connects the node to an existing System NATS server
	// instead of serving one itself.
	SystemNATSURL string
	// RouteListen is the System NATS cluster route address the node serves;
	// it defaults to a free loopback port. Ignored with SystemNATSURL.
	RouteListen string
	// SeedRoute is the route URL of a node to join through.
	SeedRoute string
	// Membership, Recovery and RetireOnStop enable the node's control-plane
	// membership, component recovery and retirement on graceful stop.
	Membership, Recovery, RetireOnStop bool
	// IngressAddress is recorded by a founding node as the cluster ingress.
	IngressAddress string
	// DelvePath enables debugging with the given Delve binary.
	DelvePath string
	// Components are the component kinds the node is told to host, with
	// their options and listen addresses.
	Components []NodeComponent
}

// NodeComponent is a component a node is told to host.
type NodeComponent struct {
	Kind    string
	Options []string
	// Listen is the address an HTTP ingress component listens on.
	Listen string
}

// Args returns the Grovlet command-line arguments for spec.
func (spec NodeSpec) Args() []string {
	args := []string{
		"--node-id", spec.NodeID,
		"--advertise-endpoint", "nats-subject://system/" + spec.NodeID,
	}
	if spec.SystemNATSURL != "" {
		args = append(args, "--system-nats-url", spec.SystemNATSURL)
	} else {
		routeListen := spec.RouteListen
		if routeListen == "" {
			routeListen = "127.0.0.1:0"
		}
		args = append(args, "--system-nats-listen", "127.0.0.1:0", "--system-nats-route-listen", routeListen)
	}
	if spec.SeedRoute != "" {
		args = append(args, "--system-nats-seed", spec.SeedRoute)
	}
	if spec.Membership {
		args = append(args, "--system-nats-membership")
	}
	if spec.Recovery {
		args = append(args, "--system-nats-recovery")
	}
	if spec.RetireOnStop {
		args = append(args, "--system-nats-retire-on-stop")
	}
	args = append(args, "--system-nats-subject", spec.Subject)
	if spec.IngressAddress != "" {
		args = append(args, "--ingress-address", spec.IngressAddress)
	}
	if spec.DelvePath != "" {
		args = append(args, "--delve-path", spec.DelvePath)
	}
	for _, component := range spec.Components {
		args = append(args, "--component", component.Kind)
		for _, option := range component.Options {
			args = append(args, "--component-option", component.Kind+"="+option)
		}
		if component.Listen != "" {
			args = append(args, "--component-listen", component.Kind+"="+component.Listen)
		}
	}
	return args
}

// Launch starts one node process without waiting for it to become ready.
func Launch(binaryPath string, spec NodeSpec) (*nodeproc.Process, error) {
	return nodeproc.Start(binaryPath, spec.Args()...)
}

// StartNode starts one node process, waits until it is ready and returns its
// ready lifecycle event. The node is cleaned up if it does not become ready.
func StartNode(ctx context.Context, binaryPath string, spec NodeSpec) (*nodeproc.Process, nodeproc.Event, error) {
	node, err := Launch(binaryPath, spec)
	if err != nil {
		return nil, nodeproc.Event{}, err
	}
	if err := node.WaitReady(ctx); err != nil {
		_ = node.Cleanup()
		return nil, nodeproc.Event{}, err
	}
	ready, err := nodeproc.ReadyEvent(node.Logs())
	if err != nil {
		_ = node.Cleanup()
		return nil, nodeproc.Event{}, fmt.Errorf("read %s readiness: %w", spec.NodeID, err)
	}
	return node, ready, nil
}

// Component places one component on one node of a fixed topology.
type Component struct {
	NodeID  string
	Kind    string
	Options []string
	// Ingress makes the component listen on Spec.IngressAddress.
	Ingress bool
}

// Spec is a fixed topology of nodes started together. Node i (from zero) is
// "node-<i+1>"; each node seeds from node-1, and node-1 from node-2.
type Spec struct {
	Nodes      int
	Components []Component
	// SubjectRoot prefixes each node's System NATS subject.
	SubjectRoot    string
	Recovery       bool
	DelvePath      string
	IngressAddress string
}

// Cluster is a set of local node processes and their node IDs. It is safe
// for concurrent use; operations that change the cluster's processes (Kill,
// Restart, Stop) should still be serialized by the caller.
type Cluster struct {
	mu            sync.RWMutex
	ids           []string
	nodes         []*nodeproc.Process
	systemNATSURL string
}

// Start starts every node of spec, waits until all are ready and returns the
// cluster connected through node-1's System NATS URL. Nodes must start
// together: a node becomes ready only once the control plane has a quorum.
func Start(ctx context.Context, binaryPath string, spec Spec) (*Cluster, error) {
	if spec.Nodes < 1 {
		return nil, ErrInvalidTopology
	}
	components := make([][]NodeComponent, spec.Nodes)
	for _, component := range spec.Components {
		index := NodeIndex(component.NodeID)
		if index < 0 || index >= spec.Nodes {
			return nil, fmt.Errorf("%w: %s on %q", ErrInvalidTopology, component.Kind, component.NodeID)
		}
		nodeComponent := NodeComponent{Kind: component.Kind, Options: component.Options}
		if component.Ingress {
			nodeComponent.Listen = spec.IngressAddress
		}
		components[index] = append(components[index], nodeComponent)
	}
	routePorts, err := ReservePorts(spec.Nodes)
	if err != nil {
		return nil, fmt.Errorf("reserve route ports: %w", err)
	}
	cluster := New()
	for i := range spec.Nodes {
		seed := 0
		if i == 0 && spec.Nodes > 1 {
			seed = 1
		}
		nodeID := NodeID(i)
		node, err := Launch(binaryPath, NodeSpec{
			NodeID:      nodeID,
			Subject:     spec.SubjectRoot + nodeID,
			RouteListen: "127.0.0.1:" + strconv.Itoa(routePorts[i]),
			SeedRoute:   "nats-route://127.0.0.1:" + strconv.Itoa(routePorts[seed]),
			Membership:  true,
			Recovery:    spec.Recovery,
			DelvePath:   spec.DelvePath,
			Components:  components[i],
		})
		if err != nil {
			_ = cluster.Cleanup()
			return nil, fmt.Errorf("start %s: %w", nodeID, err)
		}
		cluster.Add(nodeID, node, "")
	}
	for i, node := range cluster.Nodes() {
		if err := node.WaitReady(ctx); err != nil {
			diagnostics := cluster.Diagnostics()
			_ = cluster.Cleanup()
			return nil, fmt.Errorf("wait for %s: %w\n%s", NodeID(i), err, diagnostics)
		}
	}
	if err := cluster.UseSystemNATSOf(NodeID(0)); err != nil {
		diagnostics := cluster.Diagnostics()
		_ = cluster.Cleanup()
		return nil, fmt.Errorf("read System NATS URL: %w\n%s", err, diagnostics)
	}
	return cluster, nil
}

// New returns an empty cluster that nodes are added to one at a time, as
// when this process founds or joins a discovered cluster.
func New() *Cluster {
	return &Cluster{}
}

// Add appends node as nodeID. A non-empty systemNATSURL becomes the URL the
// cluster is reached through.
func (c *Cluster) Add(nodeID string, node *nodeproc.Process, systemNATSURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ids = append(c.ids, nodeID)
	c.nodes = append(c.nodes, node)
	if systemNATSURL != "" {
		c.systemNATSURL = systemNATSURL
	}
}

func (c *Cluster) snapshot() ([]string, []*nodeproc.Process) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]string(nil), c.ids...), append([]*nodeproc.Process(nil), c.nodes...)
}

// Len returns the number of nodes.
func (c *Cluster) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.nodes)
}

// Nodes returns the node processes in node order.
func (c *Cluster) Nodes() []*nodeproc.Process {
	_, nodes := c.snapshot()
	return nodes
}

// NodeIDs returns the node IDs in node order.
func (c *Cluster) NodeIDs() []string {
	ids, _ := c.snapshot()
	return ids
}

// Node returns the node process with nodeID.
func (c *Cluster) Node(nodeID string) (*nodeproc.Process, bool) {
	ids, nodes := c.snapshot()
	for i, id := range ids {
		if id == nodeID {
			return nodes[i], true
		}
	}
	return nil, false
}

// SystemNATSURL is the System NATS URL clients use to reach the cluster.
func (c *Cluster) SystemNATSURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.systemNATSURL
}

// UseSystemNATSOf connects the cluster through nodeID's System NATS server,
// as read from the node's latest ready event.
func (c *Cluster) UseSystemNATSOf(nodeID string) error {
	node, ok := c.Node(nodeID)
	if !ok {
		return fmt.Errorf("%w: no node %q", ErrInvalidTopology, nodeID)
	}
	event, err := nodeproc.ReadyEvent(node.Logs())
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.systemNATSURL = event.SystemNATSURL
	c.mu.Unlock()
	return nil
}

// Kill forcibly terminates nodeID, as a node failure would.
func (c *Cluster) Kill(ctx context.Context, nodeID string) error {
	node, ok := c.Node(nodeID)
	if !ok {
		return fmt.Errorf("%w: %q is not a local node", ErrInvalidTopology, nodeID)
	}
	return node.Kill(ctx)
}

// Restart stops every node except skipNodeID (one already failed), from the
// last to the first, restarts all of them from their durable runtime
// directories, waits until each is ready and reconnects through the first
// node.
func (c *Cluster) Restart(ctx context.Context, skipNodeID string) error {
	ids, nodes := c.snapshot()
	if len(nodes) == 0 {
		return ErrInvalidTopology
	}
	for i := len(nodes) - 1; i >= 0; i-- {
		if ids[i] == skipNodeID {
			continue
		}
		if err := nodes[i].Stop(ctx); err != nil {
			return fmt.Errorf("stop %s for durable restart: %w\n%s", ids[i], err, c.Diagnostics())
		}
	}
	for i, node := range nodes {
		if err := node.Restart(); err != nil {
			return fmt.Errorf("restart %s: %w\n%s", ids[i], err, c.Diagnostics())
		}
	}
	for i, node := range nodes {
		if err := node.WaitReady(ctx); err != nil {
			return fmt.Errorf("wait for restarted %s: %w\n%s", ids[i], err, c.Diagnostics())
		}
	}
	if err := c.UseSystemNATSOf(ids[0]); err != nil {
		return fmt.Errorf("read restarted System NATS URL: %w", err)
	}
	return nil
}

// Stop gracefully stops every node except skipNodeID, concurrently.
func (c *Cluster) Stop(ctx context.Context, skipNodeID string) error {
	ids, nodes := c.snapshot()
	errorsByNode := make([]error, len(nodes))
	var wait sync.WaitGroup
	for i, node := range nodes {
		if ids[i] == skipNodeID {
			continue
		}
		wait.Go(func() {
			if err := node.Stop(ctx); err != nil {
				errorsByNode[i] = fmt.Errorf("%s: %w", ids[i], err)
			}
		})
	}
	wait.Wait()
	return errors.Join(errorsByNode...)
}

// Leave stops the nodes one at a time, giving each up to timeout to leave
// the cluster gracefully, and then cleans them up.
func (c *Cluster) Leave(timeout time.Duration) {
	for _, node := range c.Nodes() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		_ = node.Stop(ctx)
		cancel()
		_ = node.Cleanup()
	}
}

// Cleanup forcibly stops every node and removes its runtime directory.
func (c *Cluster) Cleanup() error {
	ids, nodes := c.snapshot()
	var cleanupErr error
	for i, node := range nodes {
		if err := node.Cleanup(); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("%s: %w", ids[i], err))
		}
	}
	return cleanupErr
}

// Diagnostics returns every node's captured output, labeled by node ID.
func (c *Cluster) Diagnostics() string {
	ids, nodes := c.snapshot()
	var output strings.Builder
	for i, node := range nodes {
		logs := node.Logs()
		fmt.Fprintf(&output, "%s logs:\n%s", ids[i], logs)
		if !strings.HasSuffix(logs, "\n") {
			output.WriteByte('\n')
		}
	}
	return output.String()
}

// NodeID returns the ID of the node at index (from zero).
func NodeID(index int) string {
	return "node-" + strconv.Itoa(index+1)
}

// NodeIndex returns the index of a "node-<n>" ID, or -1.
func NodeIndex(nodeID string) int {
	number, found := strings.CutPrefix(nodeID, "node-")
	if !found {
		return -1
	}
	index, err := strconv.Atoi(number)
	if err != nil || index < 1 {
		return -1
	}
	return index - 1
}

// ReservePorts returns count free loopback TCP ports.
func ReservePorts(count int) ([]int, error) {
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
