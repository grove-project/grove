package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/grovetest"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/systemnats"
	groveshop "github.com/grove-project/grove/internal/testapp"
)

const (
	debugDemoNodeCount         = 5
	debugDemoConditionInterval = 25 * time.Millisecond
	localStateEnvironment      = "GROVE_LOCAL_STATE"
)

type localConnection struct {
	SystemNATSURL string `json:"system_nats_url"`
	NodeID        string `json:"node_id"`
	WebURL        string `json:"web_url"`
}

func executeDeploy(ctx context.Context, parsed invocation, output io.Writer) error {
	delvePath, err := exec.LookPath("dlv")
	if err != nil {
		return fmt.Errorf("locate Delve (install dlv before using --debug-demo): %w", err)
	}
	temporaryDir, err := os.MkdirTemp("", "grove-debug-demo-")
	if err != nil {
		return fmt.Errorf("create debug demo directory: %w", err)
	}
	defer os.RemoveAll(temporaryDir)
	configuredArtifact := filepath.Join(temporaryDir, "grove-shop")
	compilation, err := compileConfigFile(ctx, parsed.binaryPath, parsed.configPath, compileWithTarget)
	if err != nil {
		return fmt.Errorf("compile debug demo configuration: %w", err)
	}
	inspection, err := artifact.EmbedFile(parsed.binaryPath, configuredArtifact, compilation)
	if err != nil {
		return fmt.Errorf("build configured debug demo artifact: %w", err)
	}
	routePorts, err := reserveLocalPorts(debugDemoNodeCount)
	if err != nil {
		return fmt.Errorf("reserve debug demo route ports: %w", err)
	}
	webPorts, err := reserveLocalPorts(1)
	if err != nil {
		return fmt.Errorf("reserve debug demo Web port: %w", err)
	}
	webAddress := "127.0.0.1:" + strconv.Itoa(webPorts[0])
	nodes, systemNATSURL, err := startDebugDemoNodes(ctx, configuredArtifact, delvePath, routePorts, webAddress)
	if err != nil {
		return err
	}
	defer cleanupDebugDemoNodes(nodes)
	transport, err := systemnats.Connect(ctx, systemNATSURL)
	if err != nil {
		return fmt.Errorf("connect to debug demo cluster: %w", err)
	}
	if err := waitForDebugDemoReady(ctx, transport, inspection.ArtifactDigest); err != nil {
		transport.Close()
		return fmt.Errorf("wait for debug demo cluster: %w\n%s", err, debugDemoDiagnostics(nodes))
	}
	transport.Close()
	state := localConnection{SystemNATSURL: systemNATSURL, NodeID: "node-1", WebURL: "http://" + webAddress}
	if err := writeLocalConnection(state); err != nil {
		return err
	}
	defer removeLocalConnection()
	fmt.Fprintln(output, "Grove Shop debug demo ready")
	fmt.Fprintf(output, "Artifact %s\n", inspection.ArtifactDigest)
	fmt.Fprintf(output, "Web      %s\n", state.WebURL)
	fmt.Fprintln(output, "Web      node-1")
	fmt.Fprintln(output, "Orders   node-2")
	fmt.Fprintln(output, "Inventory node-3")
	fmt.Fprintln(output, "Payment  node-4")
	fmt.Fprintln(output, "Shipping node-5")
	fmt.Fprintln(output, "Keep this command running; press Ctrl-C to stop the cluster.")
	<-ctx.Done()
	return nil
}

func startDebugDemoNodes(
	ctx context.Context,
	artifactPath string,
	delvePath string,
	routePorts []int,
	webAddress string,
) ([]*grovetest.Node, string, error) {
	extras := [][]string{
		{"--component", "web", "--component-listen", "web=" + webAddress},
		{"--component", "orders", "--component-option", "orders=distributed"},
		{"--component", "inventory"},
		{"--component", "payment"},
		{"--component", "shipping"},
	}
	nodes := make([]*grovetest.Node, 0, len(extras))
	for i := range extras {
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
			"--system-nats-subject", "_GROVE.system.debug-demo." + nodeID,
			"--delve-path", delvePath,
		}
		node, err := grovetest.StartNode(artifactPath, append(args, extras[i]...)...)
		if err != nil {
			cleanupDebugDemoNodes(nodes)
			return nil, "", fmt.Errorf("start debug demo %s: %w", nodeID, err)
		}
		nodes = append(nodes, node)
	}
	for _, node := range nodes {
		if err := node.WaitReady(ctx); err != nil {
			cleanupDebugDemoNodes(nodes)
			return nil, "", fmt.Errorf("wait for debug demo Grovlets: %w\n%s", err, debugDemoDiagnostics(nodes))
		}
	}
	systemNATSURL, err := systemNATSURLFromLogs(nodes[0].Logs())
	if err != nil {
		cleanupDebugDemoNodes(nodes)
		return nil, "", fmt.Errorf("read debug demo System NATS URL: %w", err)
	}
	return nodes, systemNATSURL, nil
}

func reserveLocalPorts(count int) ([]int, error) {
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

func systemNATSURLFromLogs(logs string) (string, error) {
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

func waitForDebugDemoReady(ctx context.Context, transport *systemnats.Transport, artifactDigest string) error {
	wantNodes := map[grove.ServiceID]string{
		groveshop.ServiceWeb:       "node-1",
		groveshop.ServiceOrders:    "node-2",
		groveshop.ServiceInventory: "node-3",
		groveshop.ServicePayment:   "node-4",
		groveshop.ServiceShipping:  "node-5",
	}
	ticker := time.NewTicker(debugDemoConditionInterval)
	defer ticker.Stop()
	var lastPlacement systemnats.PlacementView
	var lastCluster systemnats.ClusterView
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
		ready := clusterErr == nil && cluster.Ready && len(cluster.Nodes) == debugDemoNodeCount &&
			placementErr == nil && placement.Ready && len(placement.Placements) == len(wantNodes)
		for _, node := range cluster.Nodes {
			ready = ready && node.Health == systemnats.HealthHealthy
		}
		for _, record := range placement.Placements {
			wantNode, expected := wantNodes[record.ServiceID]
			ready = ready && expected && record.NodeID == wantNode && record.ArtifactDigest == artifactDigest
			view, err := transport.RequestComponents(attemptCtx, record.NodeID)
			if err != nil {
				lastErr = errors.Join(lastErr, err)
				ready = false
				continue
			}
			found := false
			for _, component := range view.Components {
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
		lastErr = errors.Join(clusterErr, placementErr, lastErr)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("cluster=%#v placement=%#v: %w", lastCluster, lastPlacement, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func cleanupDebugDemoNodes(nodes []*grovetest.Node) {
	for _, node := range nodes {
		_ = node.Cleanup()
	}
}

func debugDemoDiagnostics(nodes []*grovetest.Node) string {
	var diagnostics strings.Builder
	for i, node := range nodes {
		fmt.Fprintf(&diagnostics, "node-%d logs:\n%s", i+1, node.Logs())
		if !strings.HasSuffix(node.Logs(), "\n") {
			diagnostics.WriteByte('\n')
		}
	}
	return diagnostics.String()
}

func localStatePath() string {
	if path := os.Getenv(localStateEnvironment); path != "" {
		return path
	}
	return filepath.Join(".grove", "debug-demo.json")
}

func writeLocalConnection(connection localConnection) error {
	return writeLocalConnectionFile(localStatePath(), connection)
}

func writeLocalConnectionFile(path string, connection localConnection) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create local Grove state directory: %w", err)
	}
	encoded, err := json.Marshal(connection)
	if err != nil {
		return fmt.Errorf("encode local Grove connection: %w", err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return fmt.Errorf("write local Grove connection %q: %w", path, err)
	}
	return nil
}

func readLocalConnection() (localConnection, error) {
	path := localStatePath()
	encoded, err := os.ReadFile(path)
	if err != nil {
		return localConnection{}, err
	}
	var connection localConnection
	if err := json.Unmarshal(encoded, &connection); err != nil {
		return localConnection{}, fmt.Errorf("decode local Grove connection %q: %w", path, err)
	}
	return connection, nil
}

func removeLocalConnection() {
	path := localStatePath()
	_ = os.Remove(path)
	_ = os.Remove(filepath.Dir(path))
}

func applyLocalConnection(parsed *invocation) {
	if parsed.systemNATSURL != "" && parsed.nodeID != "" {
		return
	}
	connection, err := readLocalConnection()
	if err != nil {
		return
	}
	if parsed.systemNATSURL == "" {
		parsed.systemNATSURL = connection.SystemNATSURL
	}
	if parsed.nodeID == "" {
		parsed.nodeID = connection.NodeID
	}
}
