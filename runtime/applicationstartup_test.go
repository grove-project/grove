package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/systemnats"
	groveshop "github.com/grove-project/grove/internal/testapp"
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
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	directory := t.TempDir()
	discoveryAddress := testApplicationDiscoveryAddress(t)
	t.Setenv(applicationDiscoveryEnvironment, discoveryAddress)
	compilation, err := compileApplicationConfiguration(filepath.Join("..", "configs", "acme.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	configuredPath := filepath.Join(directory, "groveshop")
	inspection, err := artifact.EmbedFile(grovletPath(t), configuredPath, compilation)
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

	// A cluster needs three nodes to serve, so Start new cluster bootstraps
	// three nodes from one console and every later console adds nodes with
	// Join. A node may not leave if that would drop the cluster below three,
	// so the original generation is replaced by adding before removing.
	consoles := make([]*testArtifactConsole, 0, 3)
	launch := func(existingNodes int, wantNodes ...string) {
		console := startTestArtifactConsole(t, ctx, configuredPath, discoveryAddress, filepath.Join(directory, "console-"+strconv.Itoa(len(consoles)+1)+".json"))
		consoles = append(consoles, console)
		if existingNodes == 0 {
			waitForArtifactConsoleText(t, ctx, console, "No Grove Test App cluster discovered")
			if output := console.output.String(); !strings.Contains(output, "> Start new cluster") || strings.Contains(output, "Join cluster") || strings.Contains(output, "New rollout") {
				t.Fatalf("first-console startup screen = %q", output)
			}
			// Without a node count, Start new cluster starts the three nodes a
			// cluster needs to serve.
			output := runConfiguredArtifactAction(t, ctx, configuredPath, console.statePath, "cluster.start")
			var started applicationJoinResult
			if err := json.Unmarshal(output, &started); err != nil {
				t.Fatalf("decode cluster start result %q: %v", output, err)
			}
			if started.State != "started" || !slices.Equal(started.NodeIDs, wantNodes) {
				t.Fatalf("cluster start result = %#v; want %v started", started, wantNodes)
			}
			return
		}
		waitForArtifactConsoleText(t, ctx, console, "Grove cluster discovered")
		if output := console.output.String(); !strings.Contains(output, "> Join cluster") ||
			!strings.Contains(output, "Nodes    "+strconv.Itoa(existingNodes)) ||
			strings.Contains(output, "Start new cluster") || strings.Contains(output, "New rollout") {
			t.Fatalf("join startup screen for %v = %q", wantNodes, output)
		}
		args := []string{"cluster.join"}
		if len(wantNodes) != 1 {
			args = append(args, "--nodes", strconv.Itoa(len(wantNodes)))
		}
		output := runConfiguredArtifactAction(t, ctx, configuredPath, console.statePath, args...)
		var joined applicationJoinResult
		if err := json.Unmarshal(output, &joined); err != nil {
			t.Fatalf("decode join result %q: %v", output, err)
		}
		if joined.State != "joined" || !slices.Equal(joined.NodeIDs, wantNodes) {
			t.Fatalf("join result = %#v; want %v joined", joined, wantNodes)
		}
	}
	// serving waits for the cluster to serve with wantNodes nodes and proves it
	// by completing an order through ingress.
	serving := func(wantNodes int, orderID string) applicationDiscoveryRecord {
		t.Helper()
		record := waitForApplicationDiscoveryNodes(t, ctx, discovery, wantNodes)
		waitForApplicationClusterStage(t, ctx, record.WebAddress, inspection.ArtifactDigest, wantNodes, 5)
		waitForApplicationControlReplicas(t, ctx, record.Nodes[0].SystemNATSURL, 3)
		order, err := waitForApplicationOrder(ctx, record.WebAddress, orderID)
		if err != nil || !applicationOrderCompleted(order) {
			t.Fatalf("order %s in a %d-node cluster = %#v, error=%v", orderID, wantNodes, order, err)
		}
		return record
	}
	launch(0, "node-1", "node-2", "node-3")
	serving(3, "three-nodes")
	launch(3, "node-4")
	serving(4, "four-nodes")
	launch(4, "node-5", "node-6")
	serving(6, "six-nodes")

	// Quitting the first console retires node-1, node-2 and node-3 one at a
	// time, so the final membership is exactly node-4,node-5,node-6. This
	// catches placement observers that restart empty during stream migration
	// and never rediscover existing keys.
	consoles[0].stop(t)
	record := waitForApplicationDiscoveryNodes(t, ctx, discovery, 3)
	status := waitForApplicationClusterStage(t, ctx, record.WebAddress, inspection.ArtifactDigest, 3, 5)
	waitForApplicationControlReplicas(t, ctx, record.Nodes[0].SystemNATSURL, 3)
	wantFinalNodes := []string{"node-4", "node-5", "node-6"}
	for index, node := range status.Nodes {
		if index >= len(wantFinalNodes) || node.NodeID != wantFinalNodes[index] {
			t.Fatalf("final replacement nodes = %#v; want %v", status.Nodes, wantFinalNodes)
		}
	}
	if len(status.Nodes) != len(wantFinalNodes) || len(status.Placements) != 5 {
		t.Fatalf("final replacement status = %#v; want nodes 4,5,6 and five placements", status)
	}
	if order, err := waitForApplicationOrder(ctx, record.WebAddress, "after-complete-node-replacement"); err != nil || !applicationOrderCompleted(order) {
		t.Fatalf("order after complete node replacement = %#v, error=%v", order, err)
	}
	for _, index := range []int{1, 2} {
		output := runConfiguredArtifactAction(t, ctx, configuredPath, consoles[index].statePath, "cluster.status")
		var observed ClusterStatus
		if err := json.Unmarshal(output, &observed); err != nil {
			t.Fatalf("decode observer status %q: %v", output, err)
		}
		if len(observed.Nodes) != 3 || len(observed.Placements) != 5 {
			t.Errorf("observer %s status = %#v", consoles[index].statePath, observed)
		}
	}

	for _, index := range []int{2, 1} {
		consoles[index].stop(t)
	}
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
) ClusterStatus {
	t.Helper()
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	var last ClusterStatus
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
				continue
			}
			// The configured count changes at once; the replicas are only
			// usable once the stream has a leader and every peer is current.
			stream, err := js.Stream(ctx, "KV_"+bucket)
			if err != nil {
				lastErr = err
				ready = false
				continue
			}
			infoCtx, infoCancel := context.WithTimeout(ctx, 500*time.Millisecond)
			info, err := stream.Info(infoCtx)
			infoCancel()
			if err != nil || info.Cluster == nil || info.Cluster.Leader == "" || len(info.Cluster.Replicas) != want-1 {
				lastErr = err
				ready = false
				continue
			}
			for _, replica := range info.Cluster.Replicas {
				if replica.Offline || !replica.Current {
					ready = false
				}
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
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	directory := t.TempDir()
	discoveryAddress := testApplicationDiscoveryAddress(t)
	t.Setenv(applicationDiscoveryEnvironment, discoveryAddress)
	compilation, err := compileApplicationConfiguration(filepath.Join("..", "configs", "acme.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	configuredPath := filepath.Join(directory, "groveshop")
	inspection, err := artifact.EmbedFile(grovletPath(t), configuredPath, compilation)
	if err != nil {
		t.Fatal(err)
	}

	// The first console starts node-1..3 and the second adds node-4..6, so
	// quitting the first retires the original generation one node at a time
	// while three nodes remain.
	consoles := []*testArtifactConsole{
		startClusterConsole(t, ctx, configuredPath, discoveryAddress, filepath.Join(directory, "console-1.json")),
		joinClusterConsole(t, ctx, configuredPath, discoveryAddress, filepath.Join(directory, "console-2.json"), 3),
	}

	discovery, err := newApplicationDiscovery(inspection.Manifest.ApplicationID, inspection.Config.Facts["cluster.name"])
	if err != nil {
		t.Fatal(err)
	}
	defer discovery.close()
	record, exists, err := discovery.discover(ctx)
	if err != nil || !exists {
		t.Fatalf("discover six-node cluster: exists=%t error=%v", exists, err)
	}
	if len(record.Nodes) != 6 {
		t.Fatalf("nodes before graceful leave = %#v", record.Nodes)
	}
	status := waitForApplicationClusterShape(t, ctx, record.WebAddress, inspection.ArtifactDigest, 6, "")
	if len(status.Placements) != 5 {
		t.Fatalf("placements before graceful leave = %#v", status.Placements)
	}

	// node-1 initially owns Orders and Web. Quitting the application console
	// that hosts node-1..3 must relocate their services, preserve the Web
	// address, and then delete their membership.
	retired := []string{"node-1", "node-2", "node-3"}
	consoles[0].stop(t)
	status = waitForApplicationClusterShape(t, ctx, record.WebAddress, inspection.ArtifactDigest, 3, "node-1")
	for _, placement := range status.Placements {
		if slices.Contains(retired, placement.NodeID) || placement.Health != string(systemnats.ComponentHealthy) {
			t.Fatalf("placement after node-1..3 retirement = %#v", placement)
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
		if slices.Contains(retired, node.NodeID) {
			t.Fatalf("retired node remains discoverable: %#v", record.Nodes)
		}
	}

	consoles[1].stop(t)
}

func waitForApplicationClusterShape(
	t *testing.T,
	ctx context.Context,
	webAddress string,
	artifactDigest string,
	nodeCount int,
	absentNode string,
) ClusterStatus {
	t.Helper()
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	var last ClusterStatus
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

// startClusterConsole launches an application console that finds no cluster
// and starts a new one with the default node count.
func startClusterConsole(
	t *testing.T,
	ctx context.Context,
	binaryPath string,
	discoveryAddress string,
	statePath string,
) *testArtifactConsole {
	t.Helper()
	console := startTestArtifactConsole(t, ctx, binaryPath, discoveryAddress, statePath)
	waitForArtifactConsoleText(t, ctx, console, "No Grove Test App cluster discovered")
	runConfiguredArtifactAction(t, ctx, binaryPath, console.statePath, "cluster.start")
	return console
}

// joinClusterConsole launches an application console that discovers the
// running cluster and adds that many nodes to it.
func joinClusterConsole(
	t *testing.T,
	ctx context.Context,
	binaryPath string,
	discoveryAddress string,
	statePath string,
	nodes int,
) *testArtifactConsole {
	t.Helper()
	console := startTestArtifactConsole(t, ctx, binaryPath, discoveryAddress, statePath)
	waitForArtifactConsoleText(t, ctx, console, "Grove cluster discovered")
	runConfiguredArtifactAction(t, ctx, binaryPath, console.statePath, "cluster.join", "--nodes", strconv.Itoa(nodes))
	return console
}

func waitForJoinedApplicationStatus(
	t *testing.T,
	ctx context.Context,
	webAddress string,
	artifactDigest string,
) ClusterStatus {
	t.Helper()
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	var last ClusterStatus
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

// An application that declares no startup placement must still scale out: a
// node joining under an ID the application never listed hosts the components
// the runtime chooses.
func TestJoinedNodeWithNeverSeenIDHostsRuntimeChosenComponents(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	if activeApplication.Scenario != nil && len(activeApplication.Scenario.StartupComponents) != 0 {
		t.Fatal("fixture must declare no startup components")
	}
	directory := t.TempDir()
	discoveryAddress := testApplicationDiscoveryAddress(t)
	t.Setenv(applicationDiscoveryEnvironment, discoveryAddress)
	compilation, err := compileApplicationConfiguration(filepath.Join("..", "configs", "acme.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	configuredPath := filepath.Join(directory, "groveshop")
	inspection, err := artifact.EmbedFile(grovletPath(t), configuredPath, compilation)
	if err != nil {
		t.Fatal(err)
	}

	consoles := []*testArtifactConsole{
		startClusterConsole(t, ctx, configuredPath, discoveryAddress, filepath.Join(directory, "console-1.json")),
		joinClusterConsole(t, ctx, configuredPath, discoveryAddress, filepath.Join(directory, "console-2.json"), 1),
	}

	discovery, err := newApplicationDiscovery(inspection.Manifest.ApplicationID, inspection.Config.Facts["cluster.name"])
	if err != nil {
		t.Fatal(err)
	}
	defer discovery.close()
	record, exists, err := discovery.discover(ctx)
	if err != nil || !exists {
		t.Fatalf("discover cluster: exists=%t error=%v", exists, err)
	}
	waitForApplicationClusterShape(t, ctx, record.WebAddress, inspection.ArtifactDigest, 4, "")

	// Node-4 leaves (four nodes down to the three a cluster needs), then a
	// node joins: the join flow never reuses IDs, so the new node is node-5,
	// which no definition or test ever listed.
	consoles[1].stop(t)
	waitForApplicationClusterShape(t, ctx, record.WebAddress, inspection.ArtifactDigest, 3, "node-4")
	consoles = append(consoles, joinClusterConsole(t, ctx, configuredPath, discoveryAddress, filepath.Join(directory, "console-3.json"), 1))
	status := waitForApplicationClusterShape(t, ctx, record.WebAddress, inspection.ArtifactDigest, 4, "node-4")

	var joined *NodeStatus
	for i := range status.Nodes {
		if status.Nodes[i].NodeID == "node-5" {
			joined = &status.Nodes[i]
		}
	}
	if joined == nil {
		t.Fatalf("node-5 missing from status: %#v", status.Nodes)
	}
	hosted := map[grove.ServiceID]string{}
	for _, component := range joined.Components {
		hosted[component.ServiceID] = component.State
	}
	for _, serviceID := range []grove.ServiceID{groveshop.ServiceOrders, groveshop.ServiceInventory, groveshop.ServicePayment, groveshop.ServiceShipping} {
		if hosted[serviceID] != string(systemnats.ComponentHealthy) {
			t.Errorf("node-5 component %d state = %q; components=%#v", serviceID, hosted[serviceID], joined.Components)
		}
	}
	order, err := waitForApplicationOrder(ctx, record.WebAddress, "after-node-5-join")
	if err != nil || !applicationOrderCompleted(order) {
		t.Fatalf("order after node-5 joined = %#v, %v", order, err)
	}
	consoles[2].stop(t)
	consoles[0].stop(t)
}

// Ingress is placed by the runtime: when the node serving the web address is
// lost, a surviving node takes the address over without any directive.
func TestIngressMovesToSurvivorWhenServingNodeLeaves(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	directory := t.TempDir()
	discoveryAddress := testApplicationDiscoveryAddress(t)
	t.Setenv(applicationDiscoveryEnvironment, discoveryAddress)
	compilation, err := compileApplicationConfiguration(filepath.Join("..", "configs", "acme.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	configuredPath := filepath.Join(directory, "groveshop")
	inspection, err := artifact.EmbedFile(grovletPath(t), configuredPath, compilation)
	if err != nil {
		t.Fatal(err)
	}
	// The first console starts node-1..3 and the second adds node-4..6, so
	// quitting the first retires the original generation one node at a time
	// while three nodes remain.
	consoles := []*testArtifactConsole{
		startClusterConsole(t, ctx, configuredPath, discoveryAddress, filepath.Join(directory, "console-1.json")),
		joinClusterConsole(t, ctx, configuredPath, discoveryAddress, filepath.Join(directory, "console-2.json"), 3),
	}
	discovery, err := newApplicationDiscovery(inspection.Manifest.ApplicationID, inspection.Config.Facts["cluster.name"])
	if err != nil {
		t.Fatal(err)
	}
	defer discovery.close()
	record, exists, err := discovery.discover(ctx)
	if err != nil || !exists {
		t.Fatalf("discover cluster: exists=%t error=%v", exists, err)
	}
	status := waitForApplicationClusterShape(t, ctx, record.WebAddress, inspection.ArtifactDigest, 6, "")
	if node := applicationPlacementNodeFromStatus(status, groveshop.ServiceWeb); node != "node-1" {
		t.Fatalf("ingress starts on %q; want the bootstrap node-1", node)
	}

	consoles[0].stop(t)
	status = waitForApplicationClusterShape(t, ctx, record.WebAddress, inspection.ArtifactDigest, 3, "node-1")
	if node := applicationPlacementNodeFromStatus(status, groveshop.ServiceWeb); node == "" || slices.Contains([]string{"node-1", "node-2", "node-3"}, node) {
		t.Fatalf("ingress placement after node-1..3 left = %q", node)
	}
	order, err := waitForApplicationOrder(ctx, record.WebAddress, "after-ingress-move")
	if err != nil || !applicationOrderCompleted(order) {
		t.Fatalf("order through moved ingress = %#v, %v", order, err)
	}
	consoles[1].stop(t)
}

// Killing (not gracefully stopping) the node that serves ingress must not
// lose ingress: the runtime moves it to a survivor on the same address, and
// the cluster keeps serving orders through it.
func TestIngressMovesToSurvivorWhenServingNodeIsKilled(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Second)
	defer cancel()
	began := time.Now()
	phase := func(name string) { t.Logf("+%6.2fs %s", time.Since(began).Seconds(), name) }
	directory := t.TempDir()
	discoveryAddress := testApplicationDiscoveryAddress(t)
	t.Setenv(applicationDiscoveryEnvironment, discoveryAddress)
	compilation, err := compileApplicationConfiguration(filepath.Join("..", "configs", "acme.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	configuredPath := filepath.Join(directory, "groveshop")
	inspection, err := artifact.EmbedFile(grovletPath(t), configuredPath, compilation)
	if err != nil {
		t.Fatal(err)
	}
	console := startClusterConsole(t, ctx, configuredPath, discoveryAddress, filepath.Join(directory, "console-1.json"))
	phase("cluster.start returned (node-1..3 ready in one console)")
	discovery, err := newApplicationDiscovery(inspection.Manifest.ApplicationID, inspection.Config.Facts["cluster.name"])
	if err != nil {
		t.Fatal(err)
	}
	defer discovery.close()
	record, exists, err := discovery.discover(ctx)
	if err != nil || !exists {
		t.Fatalf("discover cluster: exists=%t error=%v", exists, err)
	}
	status := waitForApplicationClusterShape(t, ctx, record.WebAddress, inspection.ArtifactDigest, 3, "")
	phase("3 nodes healthy, 5 placements, ingress serving")
	if node := applicationPlacementNodeFromStatus(status, groveshop.ServiceWeb); node != "node-1" {
		t.Fatalf("ingress starts on %q; want the founding node-1", node)
	}

	// Control state starts single-replica on the founder; killing the founder
	// before it is replicated across the quorum loses it by design.
	waitForApplicationControlReplicas(t, ctx, record.Nodes[len(record.Nodes)-1].SystemNATSURL, 3)
	phase("control state replicated to 3 replicas")

	// Once three nodes are healthy the bootstrap witness is released, so every
	// node is exactly one metadata voter and a single kill leaves a quorum.
	waitForMetadataVoters(t, ctx, record.Nodes[len(record.Nodes)-1].SystemNATSURL, 3)
	phase("metadata group is three voters")

	// SIGKILL only node-1's Grovlet; its workers die with it.
	pattern := "^" + regexp.QuoteMeta(configuredPath) + " .*--node-id node-1( |$)"
	if err := exec.Command("pkill", "-KILL", "-f", pattern).Run(); err != nil {
		t.Fatalf("kill node-1: %v", err)
	}

	phase("node-1 SIGKILLed")
	killedAt := time.Now()
	// Timeline of the handoff: when each stage first becomes true, measured
	// from the kill. Used to see where the handoff time goes.
	var timeline sync.WaitGroup
	stopTimeline := make(chan struct{})
	watch := func(name string, condition func() bool) {
		timeline.Add(1)
		go func() {
			defer timeline.Done()
			for {
				select {
				case <-stopTimeline:
					return
				default:
				}
				if condition() {
					t.Logf("timeline +%5.1fs after kill: %s", time.Since(killedAt).Seconds(), name)
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
		}()
	}
	survivor := record.Nodes[len(record.Nodes)-1].SystemNATSURL
	watch("metadata leader elected among survivors", func() bool {
		leader := metadataLeader(survivor)
		return leader != "" && leader != "node-1"
	})
	watch("JetStream API answers", func() bool {
		c, err := nats.Connect(survivor)
		if err != nil {
			return false
		}
		defer c.Close()
		j, err := jetstream.New(c)
		if err != nil {
			return false
		}
		attempt, cancelAttempt := context.WithTimeout(ctx, time.Second)
		defer cancelAttempt()
		_, err = j.AccountInfo(attempt)
		return err == nil
	})
	// A dying worker can answer for an instant after its node is killed, so
	// measure the moment ingress starts answering and then keeps answering
	// for two seconds: that is the handoff, not the dying process.
	var okSince time.Time
	var okSinceAfterKill time.Duration
	watch("ingress stably serving (2s without a failure)", func() bool {
		var probe ClusterStatus
		attempt, cancelAttempt := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancelAttempt()
		if readApplicationJSON(attempt, "http://"+record.WebAddress+"/grove/status", &probe) != nil {
			okSince = time.Time{}
			return false
		}
		if okSince.IsZero() {
			okSince = time.Now()
			okSinceAfterKill = time.Since(killedAt)
		}
		if time.Since(okSince) >= 2*time.Second {
			t.Logf("timeline: ingress continuously serving since +%5.1fs after kill", okSinceAfterKill.Seconds())
			return true
		}
		return false
	})
	defer func() {
		close(stopTimeline)
		timeline.Wait()
	}()
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	var last ClusterStatus
	for {
		attemptCtx, cancelAttempt := context.WithTimeout(ctx, time.Second)
		err := readApplicationJSON(attemptCtx, "http://"+record.WebAddress+"/grove/status", &last)
		cancelAttempt()
		node := applicationPlacementNodeFromStatus(last, groveshop.ServiceWeb)
		if err == nil && node != "" && node != "node-1" {
			t.Logf("timeline +%5.1fs after kill: placement record moved to %s", time.Since(killedAt).Seconds(), node)
			phase("ingress placement moved to " + node)
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("ingress did not move after node-1 was killed: status=%#v error=%v", last, err)
		}
	}
	order, err := waitForApplicationOrder(ctx, record.WebAddress, "after-ingress-kill")
	if err != nil || !applicationOrderCompleted(order) {
		t.Fatalf("order through moved ingress = %#v, %v", order, err)
	}
	phase("order served through moved ingress")
	// The survivors re-elected a metadata leader: the JetStream API answers.
	connection, err := nats.Connect(record.Nodes[len(record.Nodes)-1].SystemNATSURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	survivorJS, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := survivorJS.AccountInfo(ctx); err != nil {
		t.Fatalf("survivors have no metadata leader after node-1 was killed: %v", err)
	}
	console.stop(t)
}

// metadataVoters returns the JetStream metadata group size reported by the
// servers at url (via the internal system account), or 0 when unknown.
func metadataVoters(url string) int {
	connection, err := nats.Connect(url, nats.UserInfo("grove-system", "grove-system-internal"))
	if err != nil {
		return 0
	}
	defer connection.Close()
	inbox := connection.NewRespInbox()
	sub, err := connection.SubscribeSync(inbox)
	if err != nil {
		return 0
	}
	if err := connection.PublishRequest("$SYS.REQ.SERVER.PING.JSZ", inbox, []byte(`{}`)); err != nil {
		return 0
	}
	size := 0
	for {
		message, err := sub.NextMsg(time.Second)
		if err != nil {
			return size
		}
		var response struct {
			Data struct {
				Meta struct {
					Size int `json:"cluster_size"`
				} `json:"meta_cluster"`
			} `json:"data"`
		}
		if json.Unmarshal(message.Data, &response) == nil && response.Data.Meta.Size != 0 {
			size = response.Data.Meta.Size
		}
	}
}

func waitForMetadataVoters(t *testing.T, ctx context.Context, url string, want int) {
	t.Helper()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	last := 0
	for {
		if last = metadataVoters(url); last == want {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("metadata group has %d voters; want %d", last, want)
		}
	}
}

// metadataLeader returns the JetStream metadata group's leader as reported by
// the servers at url (via the internal system account), or "" when there is
// none or it is unknown.
func metadataLeader(url string) string {
	connection, err := nats.Connect(url, nats.UserInfo("grove-system", "grove-system-internal"))
	if err != nil {
		return ""
	}
	defer connection.Close()
	inbox := connection.NewRespInbox()
	sub, err := connection.SubscribeSync(inbox)
	if err != nil {
		return ""
	}
	if err := connection.PublishRequest("$SYS.REQ.SERVER.PING.JSZ", inbox, []byte(`{}`)); err != nil {
		return ""
	}
	leader := ""
	for {
		message, err := sub.NextMsg(time.Second)
		if err != nil {
			return leader
		}
		var response struct {
			Data struct {
				Meta struct {
					Leader string `json:"leader"`
				} `json:"meta_cluster"`
			} `json:"data"`
		}
		if json.Unmarshal(message.Data, &response) == nil && response.Data.Meta.Leader != "" {
			leader = response.Data.Meta.Leader
		}
	}
}

// Losing the third node and replacing it must leave the cluster with a
// metadata leader and serving, with no bootstrap witness involved: after the
// witness is released every node is exactly one voter.
func TestReplacementNodeJoinsAfterThirdNodeIsKilled(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	began := time.Now()
	phase := func(name string) { t.Logf("+%6.2fs %s", time.Since(began).Seconds(), name) }
	directory := t.TempDir()
	discoveryAddress := testApplicationDiscoveryAddress(t)
	t.Setenv(applicationDiscoveryEnvironment, discoveryAddress)
	compilation, err := compileApplicationConfiguration(filepath.Join("..", "configs", "acme.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	configuredPath := filepath.Join(directory, "groveshop")
	inspection, err := artifact.EmbedFile(grovletPath(t), configuredPath, compilation)
	if err != nil {
		t.Fatal(err)
	}
	consoles := []*testArtifactConsole{
		startClusterConsole(t, ctx, configuredPath, discoveryAddress, filepath.Join(directory, "console-1.json")),
	}

	discovery, err := newApplicationDiscovery(inspection.Manifest.ApplicationID, inspection.Config.Facts["cluster.name"])
	if err != nil {
		t.Fatal(err)
	}
	defer discovery.close()
	record, exists, err := discovery.discover(ctx)
	if err != nil || !exists {
		t.Fatalf("discover cluster: exists=%t error=%v", exists, err)
	}
	survivorURL := record.Nodes[0].SystemNATSURL // node-1 stays up throughout
	waitForApplicationClusterShape(t, ctx, record.WebAddress, inspection.ArtifactDigest, 3, "")
	waitForApplicationControlReplicas(t, ctx, survivorURL, 3)
	waitForMetadataVoters(t, ctx, survivorURL, 3)
	phase("three nodes, control state replicated, no witness (3 metadata voters)")
	leaderBefore := metadataLeader(survivorURL)
	t.Logf("metadata leader before the kill: %q", leaderBefore)

	pattern := "^" + regexp.QuoteMeta(configuredPath) + " .*--node-id node-3( |$)"
	if err := exec.Command("pkill", "-KILL", "-f", pattern).Run(); err != nil {
		t.Fatalf("kill node-3: %v", err)
	}
	phase("node-3 SIGKILLed")

	// Two of three voters remain, so a metadata leader exists (or is
	// re-elected within an election timeout) and the API answers.
	connection, err := nats.Connect(survivorURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	for {
		attempt, cancelAttempt := context.WithTimeout(ctx, 2*time.Second)
		_, err := js.AccountInfo(attempt)
		cancelAttempt()
		if err == nil {
			break
		}
		select {
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
			t.Fatalf("survivors never regained a metadata leader after node-3 was killed: %v", err)
		}
	}
	leaderAfter := metadataLeader(survivorURL)
	phase("metadata leader after the kill: " + leaderAfter)
	if leaderAfter == "" || leaderAfter == "node-3" {
		t.Fatalf("metadata leader after node-3 was killed = %q", leaderAfter)
	}

	// The replacement is a brand-new node, not node-3 coming back.
	consoles = append(consoles, joinClusterConsole(t, ctx, configuredPath, discoveryAddress, filepath.Join(directory, "console-2.json"), 1))
	phase("replacement node joined")
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	var status ClusterStatus
	for {
		attempt, cancelAttempt := context.WithTimeout(ctx, time.Second)
		err := readApplicationJSON(attempt, "http://"+record.WebAddress+"/grove/status", &status)
		cancelAttempt()
		healthy, replacement := 0, false
		for _, node := range status.Nodes {
			if node.Health == string(systemnats.HealthHealthy) {
				healthy++
			}
			replacement = replacement || node.NodeID == "node-4" && node.Health == string(systemnats.HealthHealthy)
		}
		if err == nil && status.Ready && healthy == 3 && replacement && len(status.Placements) == 5 {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("replacement did not restore three healthy nodes: status=%#v error=%v", status, err)
		}
	}
	phase("three healthy nodes again (node-1, node-2, node-4)")
	if leader := metadataLeader(survivorURL); leader == "" || leader == "node-3" {
		t.Fatalf("metadata leader after replacement = %q", leader)
	}
	order, err := waitForApplicationOrder(ctx, record.WebAddress, "after-replacement")
	if err != nil || !applicationOrderCompleted(order) {
		t.Fatalf("order after replacement = %#v, %v", order, err)
	}
	phase("order served")

	// The election and formation phases are observable in the cluster log
	// view of a surviving node's console.
	var logs applicationLogsView
	output := runConfiguredArtifactAction(t, ctx, configuredPath, consoles[0].statePath, "logs.view")
	if err := json.Unmarshal(output, &logs); err != nil {
		t.Fatalf("decode logs view %q: %v", output, err)
	}
	clusterLog := strings.Join(logs.Cluster, "\n")
	for _, want := range []string{"event=cluster_formed", "event=metadata_voters voters=3 detail=changed from 4", "event=metadata_leader_elected"} {
		if !strings.Contains(clusterLog, want) {
			t.Errorf("cluster log lacks %q:\n%s", want, clusterLog)
		}
	}
	consoles[1].stop(t)
	consoles[0].stop(t)
}
