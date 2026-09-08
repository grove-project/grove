package grovetest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

var (
	// ErrInvalidNodeCount is returned when NewCluster receives a non-positive
	// node count.
	ErrInvalidNodeCount = errors.New("node count must be positive")
	// ErrClusterCleaned is returned when an operation targets a cleaned Cluster.
	ErrClusterCleaned = errors.New("cluster has been cleaned up")
	// ErrClusterRunning is returned when Start targets a running Cluster.
	ErrClusterRunning = errors.New("cluster is already running")
	// ErrClusterNotRunning is returned when a wait or stop targets a Cluster
	// that has not been started.
	ErrClusterNotRunning = errors.New("cluster is not running")
)

// ClusterError reports a failed cluster operation together with diagnostics
// from every Node.
type ClusterError struct {
	// Operation identifies the cluster operation that failed.
	Operation string
	// NodeID identifies the Node involved in the failure, when applicable.
	NodeID string
	// Err is the underlying resource, process, or context error.
	Err error
	// Diagnostics contains the state and captured logs of every Node.
	Diagnostics string
}

func (e *ClusterError) Error() string {
	message := fmt.Sprintf("%s: %v", e.Operation, e.Err)
	if e.NodeID != "" {
		message = fmt.Sprintf("%s: node %s: %v", e.Operation, e.NodeID, e.Err)
	}
	if e.Diagnostics == "" {
		return message
	}
	return message + "\n" + e.Diagnostics
}

func (e *ClusterError) Unwrap() error {
	return e.Err
}

// Cluster controls a fixed set of real Grovlet processes on one host. Its
// lifecycle methods are intended for sequential use by one test goroutine.
type Cluster struct {
	nodes        []*Node
	reservations []net.Listener
	running      bool
	cleaned      bool
}

// NewCluster creates nodeCount unstarted Nodes for binaryPath. Each Node owns
// an isolated runtime directory, deterministic cluster-local ID, and loopback
// TCP port reserved until Cleanup. Call Cleanup when the test finishes.
func NewCluster(binaryPath string, nodeCount int) (*Cluster, error) {
	if nodeCount <= 0 {
		return nil, &ClusterError{Operation: "create cluster", Err: ErrInvalidNodeCount}
	}

	cluster := &Cluster{
		nodes:        make([]*Node, 0, nodeCount),
		reservations: make([]net.Listener, 0, nodeCount),
	}
	for i := range nodeCount {
		nodeID := fmt.Sprintf("node-%d", i+1)
		reservation, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			clusterErr := cluster.failure("reserve node port", nodeID, err)
			_ = cluster.Cleanup()
			return nil, clusterErr
		}
		cluster.reservations = append(cluster.reservations, reservation)

		node, err := newNode(binaryPath, nil)
		if err != nil {
			clusterErr := cluster.failure("create node", nodeID, err)
			_ = cluster.Cleanup()
			return nil, clusterErr
		}
		node.id = nodeID
		node.port = reservation.Addr().(*net.TCPAddr).Port
		cluster.nodes = append(cluster.nodes, node)
	}
	return cluster, nil
}

// NodeCount returns the fixed number of Nodes in the Cluster.
func (c *Cluster) NodeCount() int {
	return len(c.nodes)
}

// Node returns the Node at index i. It panics when i is outside the range
// [0, NodeCount()).
func (c *Cluster) Node(i int) *Node {
	return c.nodes[i]
}

// Start starts every Node in the Cluster. If any Node fails to start, Start
// cleans up the entire Cluster and returns diagnostics for every Node.
func (c *Cluster) Start() error {
	if c.cleaned {
		return c.failure("start cluster", "", ErrClusterCleaned)
	}
	if c.running {
		return c.failure("start cluster", "", ErrClusterRunning)
	}

	for _, node := range c.nodes {
		if err := node.start(); err != nil {
			clusterErr := c.failure("start cluster", node.id, err)
			_ = c.Cleanup()
			return clusterErr
		}
	}
	c.running = true
	return nil
}

// WaitAllReady waits for every Node in the Cluster to report readiness. Any
// failure includes the state and logs of the complete Cluster.
func (c *Cluster) WaitAllReady(ctx context.Context) error {
	if c.cleaned {
		return c.failure("wait for cluster readiness", "", ErrClusterCleaned)
	}
	if !c.running {
		return c.failure("wait for cluster readiness", "", ErrClusterNotRunning)
	}
	for _, node := range c.nodes {
		if err := node.WaitReady(ctx); err != nil {
			return c.failure("wait for cluster readiness", node.id, err)
		}
	}
	return nil
}

// Stop gracefully stops every live Node in the Cluster. A Node that has
// already exited does not prevent the remaining Nodes from stopping.
func (c *Cluster) Stop(ctx context.Context) error {
	if c.cleaned {
		return c.failure("stop cluster", "", ErrClusterCleaned)
	}
	if !c.running {
		return c.failure("stop cluster", "", ErrClusterNotRunning)
	}

	var stopErrors []error
	for _, node := range c.nodes {
		if node.process == nil || !node.process.running() {
			continue
		}
		if err := node.Stop(ctx); err != nil {
			stopErrors = append(stopErrors, fmt.Errorf("%s: %w", node.id, err))
		}
	}
	c.running = false
	if err := errors.Join(stopErrors...); err != nil {
		return c.failure("stop cluster", "", err)
	}
	return nil
}

// DumpDiagnostics returns the ID, reserved port, state, runtime directory, and
// captured process output for every Node in the Cluster.
func (c *Cluster) DumpDiagnostics() string {
	var diagnostics strings.Builder
	diagnostics.WriteString("cluster diagnostics:\n")
	for _, node := range c.nodes {
		fmt.Fprintf(
			&diagnostics,
			"node=%s port=%d state=%s runtime_dir=%q\n",
			node.id,
			node.port,
			node.state(),
			node.tempDir,
		)
		if logs := node.Logs(); logs != "" {
			diagnostics.WriteString("logs:\n")
			diagnostics.WriteString(logs)
			if !strings.HasSuffix(logs, "\n") {
				diagnostics.WriteByte('\n')
			}
		}
	}
	return diagnostics.String()
}

// Cleanup forcibly stops all live Nodes, removes their runtime directories,
// and releases their reserved ports. It is safe to call Cleanup more than once.
func (c *Cluster) Cleanup() error {
	if c.cleaned {
		return nil
	}

	var cleanupErrors []error
	for _, node := range c.nodes {
		if err := node.Cleanup(); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("%s: %w", node.id, err))
		}
	}
	for _, reservation := range c.reservations {
		if err := reservation.Close(); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("release node port: %w", err))
		}
	}
	c.running = false
	c.cleaned = true
	if err := errors.Join(cleanupErrors...); err != nil {
		return c.failure("clean up cluster", "", err)
	}
	return nil
}

func (c *Cluster) failure(operation, nodeID string, err error) error {
	return &ClusterError{
		Operation:   operation,
		NodeID:      nodeID,
		Err:         err,
		Diagnostics: c.DumpDiagnostics(),
	}
}

func (n *Node) state() string {
	if n.cleaned {
		return "cleaned"
	}
	if n.process == nil {
		return "not started"
	}
	if n.process.running() {
		return "running"
	}
	if n.process.err != nil {
		return "exited with error"
	}
	return "stopped"
}
