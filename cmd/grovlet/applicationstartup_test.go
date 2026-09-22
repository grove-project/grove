package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/systemnats"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func TestApplicationDiscoveryRecordLifecycle(t *testing.T) {
	t.Setenv(applicationDiscoveryEnvironment, testApplicationDiscoveryAddress(t))
	discovery, err := newApplicationDiscovery("grove-shop", "acme-local")
	if err != nil {
		t.Fatal(err)
	}
	defer discovery.close()
	observer, err := newApplicationDiscovery("grove-shop", "acme-local")
	if err != nil {
		t.Fatal(err)
	}
	defer observer.close()
	record := applicationDiscoveryRecord{
		ProtocolVersion: applicationDiscoveryVersion,
		Revision:        1,
		ApplicationID:   "grove-shop",
		ClusterID:       "acme-local",
		ArtifactDigest:  "sha256:artifact",
		WebAddress:      "127.0.0.1:8080",
		NextNode:        2,
		Nodes: []applicationDiscoveryNode{{
			NodeID: "node-1", SystemNATSURL: "nats://127.0.0.1:4222", RouteURL: "nats-route://127.0.0.1:6222",
		}},
	}
	if err := discovery.publish(record); err != nil {
		t.Fatal(err)
	}
	current, exists, err := observer.discover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !exists || current.ArtifactDigest != record.ArtifactDigest || len(current.Nodes) != 1 {
		t.Fatalf("discovered record = %#v, exists=%t", current, exists)
	}
	if err := discovery.removeNode(t.Context(), "node-1"); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := observer.discover(t.Context()); err != nil || exists {
		t.Fatalf("discovery after last node left: exists=%t error=%v", exists, err)
	}
}

func TestConfiguredArtifactBootstrapsThenJoinsOneCluster(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	directory := t.TempDir()
	discoveryAddress := testApplicationDiscoveryAddress(t)
	t.Setenv(applicationDiscoveryEnvironment, discoveryAddress)
	compilation, err := compileApplicationConfiguration(filepath.Join("..", "..", "configs", "acme.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	configuredPath := filepath.Join(directory, "groveshop")
	inspection, err := artifact.EmbedFile(grovletPath, configuredPath, compilation)
	if err != nil {
		t.Fatal(err)
	}
	// An unrelated application's discovery hint must not suppress Grove Shop's
	// only valid no-compatible-cluster action.
	unrelated, err := newApplicationDiscovery("unrelated-application", inspection.Config.Facts["cluster.name"])
	if err != nil {
		t.Fatal(err)
	}
	defer unrelated.close()
	if err := unrelated.publish(applicationDiscoveryRecord{
		ProtocolVersion: applicationDiscoveryVersion,
		Revision:        1,
		ApplicationID:   "unrelated-application",
		ClusterID:       inspection.Config.Facts["cluster.name"],
		ArtifactDigest:  "sha256:unrelated",
		WebAddress:      "127.0.0.1:1",
		NextNode:        2,
		Nodes: []applicationDiscoveryNode{{
			NodeID: "node-1", SystemNATSURL: "nats://127.0.0.1:1", RouteURL: "nats-route://127.0.0.1:2",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	discovery, err := newApplicationDiscovery(inspection.Manifest.ApplicationID, inspection.Config.Facts["cluster.name"])
	if err != nil {
		t.Fatal(err)
	}
	defer discovery.close()

	consoles := make([]*testArtifactConsole, 0, 4)
	existingNodeCounts := []int{0, 1, 1, 2}
	clusterNodeCounts := []int{1, 2, 2, 3}
	for index := range 4 {
		console := startTestArtifactConsole(t, ctx, configuredPath, discoveryAddress, filepath.Join(directory, "console-"+strconv.Itoa(index+1)+".json"))
		consoles = append(consoles, console)
		if index == 0 {
			waitForArtifactConsoleText(t, ctx, console, "No GroveShop cluster discovered")
			if output := console.output.String(); !strings.Contains(output, "> Start new cluster") || strings.Contains(output, "Join cluster") || strings.Contains(output, "New rollout") {
				t.Fatalf("first-node startup screen = %q", output)
			}
			output := runConfiguredArtifactAction(t, ctx, configuredPath, console.statePath, "cluster.start")
			var started applicationJoinResult
			if err := json.Unmarshal(output, &started); err != nil {
				t.Fatalf("decode cluster start result %q: %v", output, err)
			}
			if started.State != "started" || started.NodeID != "node-1" {
				t.Fatalf("cluster start result = %#v", started)
			}
		} else {
			waitForArtifactConsoleText(t, ctx, console, "Grove cluster discovered")
			if output := console.output.String(); !strings.Contains(output, "> Join cluster") ||
				!strings.Contains(output, "Nodes    "+strconv.Itoa(existingNodeCounts[index])) ||
				strings.Contains(output, "Start new cluster") || strings.Contains(output, "New rollout") {
				t.Fatalf("join startup screen for node-%d = %q", index+1, output)
			}
			output := runConfiguredArtifactAction(t, ctx, configuredPath, console.statePath, "cluster.join")
			var joined applicationJoinResult
			if err := json.Unmarshal(output, &joined); err != nil {
				t.Fatalf("decode join result %q: %v", output, err)
			}
			wantNode := "node-" + strconv.Itoa(index+1)
			if joined.State != "joined" || joined.NodeID != wantNode {
				t.Fatalf("join result = %#v; want node %s joined", joined, wantNode)
			}
		}

		wantNodes := clusterNodeCounts[index]
		record := waitForApplicationDiscoveryNodes(t, ctx, discovery, wantNodes)
		waitForApplicationClusterStage(t, ctx, record.WebAddress, inspection.ArtifactDigest, wantNodes, 5)
		waitForApplicationControlReplicas(t, ctx, record.Nodes[0].SystemNATSURL, wantNodes)
		order, err := waitForApplicationOrder(ctx, record.WebAddress, "cluster-stage-"+strconv.Itoa(index+1))
		if err != nil || !applicationOrderCompleted(order) {
			t.Fatalf("order after starting node-%d in %d-node cluster = %#v, error=%v", index+1, wantNodes, order, err)
		}

		if index == 1 {
			// Exercise the reported replacement sequence exactly: node-2 leaves
			// gracefully, node-1 remains a ready R1 cluster, and the next joiner
			// receives node-3 while the logical cluster returns to two nodes.
			consoles[1].stop(t)
			record = waitForApplicationDiscoveryNodes(t, ctx, discovery, 1)
			waitForApplicationClusterShape(t, ctx, record.WebAddress, inspection.ArtifactDigest, 1, "node-2")
			waitForApplicationControlReplicas(t, ctx, record.Nodes[0].SystemNATSURL, 1)
			order, err = waitForApplicationOrder(ctx, record.WebAddress, "after-node-2-retirement")
			if err != nil || !applicationOrderCompleted(order) {
				t.Fatalf("order after returning to one node = %#v, error=%v", order, err)
			}
		}
	}

	record, exists, err := discovery.discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("cluster discovery record is missing")
	}
	if len(record.Nodes) != 3 {
		t.Fatalf("discovered nodes = %#v; want one three-node cluster", record.Nodes)
	}
	for _, node := range record.Nodes {
		if node.NodeID == "node-2" {
			t.Fatalf("retired node-2 remains in replacement cluster: %#v", record.Nodes)
		}
	}

	status := waitForJoinedApplicationStatus(t, ctx, record.WebAddress, inspection.ArtifactDigest)
	if len(status.Nodes) != 3 || len(status.Placements) != 5 {
		t.Fatalf("joined status = %#v; want 3 nodes and 5 services", status)
	}
	for _, index := range []int{0, 2, 3} {
		console := consoles[index]
		output := runConfiguredArtifactAction(t, ctx, configuredPath, console.statePath, "cluster.status")
		var observed groveshop.ClusterStatusView
		if err := json.Unmarshal(output, &observed); err != nil {
			t.Fatalf("decode observer status %q: %v", output, err)
		}
		if len(observed.Nodes) != 3 || len(observed.Placements) != 5 {
			t.Errorf("observer %s status = %#v", console.statePath, observed)
		}
	}

	consoles[3].stop(t)
	record = waitForApplicationDiscoveryNodes(t, ctx, discovery, 2)
	waitForApplicationClusterShape(t, ctx, record.WebAddress, inspection.ArtifactDigest, 2, "node-4")
	waitForApplicationControlReplicas(t, ctx, record.Nodes[0].SystemNATSURL, 2)
	consoles[2].stop(t)
	record = waitForApplicationDiscoveryNodes(t, ctx, discovery, 1)
	waitForApplicationClusterShape(t, ctx, record.WebAddress, inspection.ArtifactDigest, 1, "node-3")
	waitForApplicationControlReplicas(t, ctx, record.Nodes[0].SystemNATSURL, 1)
	consoles[0].stop(t)
	if _, exists, err := discovery.discover(ctx); err != nil || exists {
		t.Errorf("discovery after every console stopped: exists=%t error=%v", exists, err)
	}
}

func waitForApplicationDiscoveryNodes(
	t *testing.T,
	ctx context.Context,
	discovery *applicationDiscovery,
	want int,
) applicationDiscoveryRecord {
	t.Helper()
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	var last applicationDiscoveryRecord
	var lastErr error
	for {
		record, exists, err := discovery.discover(ctx)
		last = record
		lastErr = err
		if err == nil && exists && len(record.Nodes) == want {
			return record
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("wait for discovery with %d nodes: record=%#v error=%v: %v", want, last, lastErr, ctx.Err())
		}
	}
}

func waitForApplicationClusterStage(
	t *testing.T,
	ctx context.Context,
	webAddress string,
	artifactDigest string,
	nodeCount int,
	placementCount int,
) groveshop.ClusterStatusView {
	t.Helper()
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	var last groveshop.ClusterStatusView
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, time.Second)
		err := readApplicationJSON(attemptCtx, "http://"+webAddress+"/grove/status", &last)
		cancel()
		ready := err == nil && last.Ready && last.Health == "healthy" && len(last.Nodes) == nodeCount &&
			len(last.Placements) == placementCount && last.ActiveArtifact != nil &&
			last.ActiveArtifact.ArtifactDigest == artifactDigest
		if ready {
			return last
		}
		lastErr = err
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("wait for %d-node/%d-service cluster stage: status=%#v error=%v: %v", nodeCount, placementCount, last, lastErr, ctx.Err())
		}
	}
}

func waitForApplicationControlReplicas(t *testing.T, ctx context.Context, systemNATSURL string, want int) {
	t.Helper()
	connection, err := nats.Connect(systemNATSURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	buckets := []string{
		systemnats.MembershipBucket,
		systemnats.PlacementBucket,
		systemnats.DesiredBucket,
		systemnats.DeploymentBucket,
	}
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	last := make(map[string]int, len(buckets))
	var lastErr error
	for {
		ready := true
		for _, bucket := range buckets {
			attemptCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			kv, err := js.KeyValue(attemptCtx, bucket)
			if err != nil {
				cancel()
				lastErr = err
				ready = false
				continue
			}
			status, err := kv.Status(attemptCtx)
			cancel()
			if err != nil {
				lastErr = err
				ready = false
				continue
			}
			last[bucket] = status.Config().Replicas
			if last[bucket] != want {
				ready = false
			}
		}
		if ready {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("wait for control bucket replicas=%d: replicas=%v error=%v: %v", want, last, lastErr, ctx.Err())
		}
	}
}

func TestConfiguredArtifactGracefulLeaveRetiresNodeAndRecoversServices(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	directory := t.TempDir()
	discoveryAddress := testApplicationDiscoveryAddress(t)
	t.Setenv(applicationDiscoveryEnvironment, discoveryAddress)
	compilation, err := compileApplicationConfiguration(filepath.Join("..", "..", "configs", "acme.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	configuredPath := filepath.Join(directory, "groveshop")
	inspection, err := artifact.EmbedFile(grovletPath, configuredPath, compilation)
	if err != nil {
		t.Fatal(err)
	}

	consoles := make([]*testArtifactConsole, 0, 4)
	for index := range 4 {
		console := startTestArtifactConsole(
			t, ctx, configuredPath, discoveryAddress,
			filepath.Join(directory, "console-"+strconv.Itoa(index+1)+".json"),
		)
		consoles = append(consoles, console)
		if index == 0 {
			waitForArtifactConsoleText(t, ctx, console, "No GroveShop cluster discovered")
			runConfiguredArtifactAction(t, ctx, configuredPath, console.statePath, "cluster.start")
		} else {
			waitForArtifactConsoleText(t, ctx, console, "Grove cluster discovered")
			runConfiguredArtifactAction(t, ctx, configuredPath, console.statePath, "cluster.join")
		}
	}

	discovery, err := newApplicationDiscovery(inspection.Manifest.ApplicationID, inspection.Config.Facts["cluster.name"])
	if err != nil {
		t.Fatal(err)
	}
	defer discovery.close()
	record, exists, err := discovery.discover(ctx)
	if err != nil || !exists {
		t.Fatalf("discover four-node cluster: exists=%t error=%v", exists, err)
	}
	if len(record.Nodes) != 4 {
		t.Fatalf("nodes before graceful leave = %#v", record.Nodes)
	}
	status := waitForApplicationClusterShape(t, ctx, record.WebAddress, inspection.ArtifactDigest, 4, "")
	if len(status.Placements) != 5 {
		t.Fatalf("placements before graceful leave = %#v", status.Placements)
	}

	// node-1 initially owns Orders and Web. Quitting its application console
	// must relocate both services, preserve the Web address, and then delete its
	// membership.
	consoles[0].stop(t)
	status = waitForApplicationClusterShape(t, ctx, record.WebAddress, inspection.ArtifactDigest, 3, "node-1")
	for _, placement := range status.Placements {
		if placement.NodeID == "node-1" || placement.Health != string(systemnats.ComponentHealthy) {
			t.Fatalf("placement after node-1 retirement = %#v", placement)
		}
	}
	order, err := waitForApplicationOrder(ctx, record.WebAddress, "after-node-1-retirement")
	if err != nil {
		t.Fatalf("order after graceful node retirement: %v", err)
	}
	if !applicationOrderCompleted(order) {
		t.Fatalf("order after graceful node retirement = %#v", order)
	}

	record, exists, err = discovery.discover(ctx)
	if err != nil || !exists {
		t.Fatalf("discover cluster after graceful leave: exists=%t error=%v", exists, err)
	}
	if len(record.Nodes) != 3 {
		t.Fatalf("discovery nodes after graceful leave = %#v", record.Nodes)
	}
	for _, node := range record.Nodes {
		if node.NodeID == "node-1" {
			t.Fatalf("retired node remains discoverable: %#v", record.Nodes)
		}
	}

	for _, index := range []int{3, 2, 1} {
		consoles[index].stop(t)
	}
}

func waitForApplicationClusterShape(
	t *testing.T,
	ctx context.Context,
	webAddress string,
	artifactDigest string,
	nodeCount int,
	absentNode string,
) groveshop.ClusterStatusView {
	t.Helper()
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	var last groveshop.ClusterStatusView
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, time.Second)
		err := readApplicationJSON(attemptCtx, "http://"+webAddress+"/grove/status", &last)
		cancel()
		ready := err == nil && last.Ready && last.Health == "healthy" && len(last.Nodes) == nodeCount &&
			len(last.Placements) == 5 && last.ActiveArtifact != nil &&
			last.ActiveArtifact.ArtifactDigest == artifactDigest
		if ready {
			for _, node := range last.Nodes {
				if node.NodeID == absentNode || node.Health != string(systemnats.HealthHealthy) {
					ready = false
					break
				}
			}
		}
		if ready {
			return last
		}
		lastErr = err
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("wait for %d-node Grove Shop cluster: status=%#v error=%v: %v", nodeCount, last, lastErr, ctx.Err())
		}
	}
}

func testApplicationDiscoveryAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	port := listener.LocalAddr().(*net.UDPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return net.JoinHostPort("239.255.71.82", strconv.Itoa(port))
}

func waitForArtifactConsoleText(t *testing.T, ctx context.Context, console *testArtifactConsole, want string) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if strings.Contains(console.output.String(), want) {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("wait for console text %q: %v; output=%q", want, ctx.Err(), console.output.String())
		}
	}
}

type testArtifactConsole struct {
	command   *exec.Cmd
	input     io.WriteCloser
	output    lockedBuffer
	statePath string
	stopped   bool
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(value)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func startTestArtifactConsole(
	t *testing.T,
	ctx context.Context,
	binaryPath string,
	discoveryAddress string,
	statePath string,
) *testArtifactConsole {
	t.Helper()
	console := &testArtifactConsole{statePath: statePath}
	console.command = exec.CommandContext(ctx, binaryPath)
	console.command.Env = append(
		os.Environ(),
		consoleStateEnvironment+"="+statePath,
		applicationDiscoveryEnvironment+"="+discoveryAddress,
	)
	input, err := console.command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	console.input = input
	console.command.Stdout = &console.output
	console.command.Stderr = &console.output
	if err := console.command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !console.stopped {
			_ = console.command.Process.Kill()
			_ = console.command.Wait()
		}
	})
	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(statePath); err == nil {
			break
		}
		select {
		case <-ticker.C:
		case <-waitCtx.Done():
			t.Fatalf("wait for configured console %s: %v; output=%q", statePath, waitCtx.Err(), console.output.String())
		}
	}
	return console
}

func (c *testArtifactConsole) stop(t *testing.T) {
	t.Helper()
	if c.stopped {
		return
	}
	if _, err := io.WriteString(c.input, "q\n"); err != nil {
		t.Fatalf("stop console %s: %v; output=%q", c.statePath, err, c.output.String())
	}
	if err := c.command.Wait(); err != nil {
		t.Fatalf("wait for console %s: %v; output=%q", c.statePath, err, c.output.String())
	}
	c.stopped = true
}

func runConfiguredArtifactAction(
	t *testing.T,
	ctx context.Context,
	binaryPath string,
	statePath string,
	args ...string,
) []byte {
	t.Helper()
	command := exec.CommandContext(ctx, binaryPath, append([]string{"action"}, args...)...)
	command.Env = append(os.Environ(), consoleStateEnvironment+"="+statePath)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("configured artifact action %q: %v; output=%q", args, err, output)
	}
	return output
}

func waitForJoinedApplicationStatus(
	t *testing.T,
	ctx context.Context,
	webAddress string,
	artifactDigest string,
) groveshop.ClusterStatusView {
	t.Helper()
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	var last groveshop.ClusterStatusView
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, time.Second)
		err := readApplicationJSON(attemptCtx, "http://"+webAddress+"/grove/status", &last)
		cancel()
		if err == nil && last.Ready && last.Health == "healthy" && len(last.Nodes) == 3 &&
			len(last.Placements) == 5 && last.ActiveArtifact != nil &&
			last.ActiveArtifact.ArtifactDigest == artifactDigest {
			return last
		}
		lastErr = err
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("wait for joined Grove Shop status: status=%#v error=%v: %v", last, lastErr, ctx.Err())
		}
	}
}
