package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/grovetest"
	"github.com/grove-project/grove/internal/systemnats"
	groveshop "github.com/grove-project/grove/internal/testapp"
	"github.com/grove-project/grove/internal/testbin"
)

var (
	groveBinary   = testbin.New("grove CLI", buildGrove)
	grovletBinary = testbin.New("test Grovlet", func(ctx context.Context, dir string) (string, error) {
		return grovetest.BuildGrovlet(ctx, dir, "./internal/testapp/cmd/testapp")
	})
	handlerGrovletBinary = testbin.New("handler-placement test Grovlet", func(ctx context.Context, dir string) (string, error) {
		return grovetest.BuildGrovlet(ctx, dir, "./internal/testapp/cmd/handlerapp")
	})
	debugGrovletBinary = testbin.New("debug test Grovlet", func(ctx context.Context, dir string) (string, error) {
		return grovetest.BuildDebugGrovlet(ctx, dir, "./internal/testapp/cmd/testapp")
	})
	delveTool = testbin.New("dlv", func(ctx context.Context, _ string) (string, error) {
		output, err := exec.CommandContext(ctx, "go", "tool", "-n", "dlv").Output()
		return strings.TrimSpace(string(output)), err
	})
)

// The real-process artifacts are built on first use; each getter skips the
// calling test under -short.
func grovePath(tb testing.TB) string        { return groveBinary.Get(tb) }
func grovletPath(tb testing.TB) string      { return grovletBinary.Get(tb) }
func debugGrovletPath(tb testing.TB) string { return debugGrovletBinary.Get(tb) }
func handlerGrovletPath(tb testing.TB) string {
	return handlerGrovletBinary.Get(tb)
}
func delvePath(tb testing.TB) string { return delveTool.Get(tb) }

func Example() {
	parsed, err := parseInvocation([]string{
		"nodes",
		"--system-nats-url", "nats://127.0.0.1:4222",
		"--node-id", "node-1",
	}, io.Discard)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(parsed.command)
	fmt.Println(parsed.nodeID)
	// Output:
	// nodes
	// node-1
}

func TestParseInvocation(t *testing.T) {
	valid := []struct {
		args        []string
		command     commandName
		action      componentAction
		service     grove.ServiceID
		binary      string
		resilience  bool
		serviceName string
		listen      string
		debugDemo   bool
	}{
		{args: []string{"status", "--system-nats-url", "nats://control", "--node-id", "node-1"}, command: commandStatus},
		{args: []string{"nodes", "--system-nats-url", "nats://control", "--node-id", "node-1"}, command: commandNodes},
		{args: []string{"components", "--system-nats-url", "nats://control", "--node-id", "node-1"}, command: commandComponents},
		{args: []string{"component", "start", "--system-nats-url", "nats://control", "--node-id", "node-2", "--service-id", "2"}, command: commandComponent, action: componentStart, service: 2},
		{args: []string{"component", "stop", "--system-nats-url", "nats://control", "--node-id", "node-2", "--service-id", "2"}, command: commandComponent, action: componentStop, service: 2},
		{args: []string{"test", "--binary", "./grove-shop"}, command: commandTest, binary: "./grove-shop"},
		{args: []string{"test", "--binary", "./grove-shop", "--resilience", "--service-id", "1"}, command: commandTest, service: 1, binary: "./grove-shop", resilience: true},
		{args: []string{"debug", "--system-nats-url", "nats://control", "--node-id", "node-1", "--service", "orders", "--listen", "127.0.0.1:40000"}, command: commandDebug, serviceName: "orders", listen: "127.0.0.1:40000"},
		{args: []string{"deploy", "--config", "configs/acme.yaml", "--debug-demo"}, command: commandDeploy, binary: "./bin/grove-shop", debugDemo: true},
	}
	for _, test := range valid {
		parsed, err := parseInvocation(test.args, io.Discard)
		if err != nil {
			t.Errorf("parseInvocation(%q): %v", test.args, err)
			continue
		}
		if parsed.command != test.command || parsed.action != test.action || parsed.serviceID != test.service || parsed.binaryPath != test.binary || parsed.resilience != test.resilience || parsed.serviceName != test.serviceName || parsed.listenAddress != test.listen || parsed.debugDemo != test.debugDemo {
			t.Errorf("parseInvocation(%q) = %#v; want command %q, action %q, service %d, binary %q, resilience %t", test.args, parsed, test.command, test.action, test.service, test.binary, test.resilience)
		}
	}

	invalid := []struct {
		args []string
		err  error
	}{
		{err: errCommandRequired},
		{args: []string{"unknown"}, err: errCommandUnknown},
		{args: []string{"deploy", "--debug-demo"}, err: errConfigPathRequired},
		{args: []string{"deploy", "--config", "configs/acme.yaml"}, err: errDebugDemoRequired},
		{args: []string{"status", "--node-id", "node-1"}, err: errSystemNATSURLRequired},
		{args: []string{"nodes", "--system-nats-url", "nats://control"}, err: errNodeIDRequired},
		{args: []string{"components", "--system-nats-url", "nats://control", "--node-id", "node-1", "extra"}, err: errUnexpectedArguments},
		{args: []string{"component"}, err: errComponentAction},
		{args: []string{"component", "kill"}, err: errComponentAction},
		{args: []string{"component", "start", "--system-nats-url", "nats://control", "--node-id", "node-1"}, err: errServiceIDRequired},
		{args: []string{"component", "stop", "--system-nats-url", "nats://control", "--node-id", "node-1", "--service-id", "4294967296"}, err: errServiceIDRequired},
		{args: []string{"test"}, err: errBinaryPathRequired},
		{args: []string{"test", "--binary", "./grove-shop", "extra"}, err: errUnexpectedArguments},
		{args: []string{"test", "--binary", "./grove-shop", "--service-id", "2"}, err: errResilienceRequired},
		{args: []string{"test", "--binary", "./grove-shop", "--resilience", "--service-id", "0"}, err: errServiceIDRequired},
		{args: []string{"debug", "--node-id", "node-1", "--service", "orders", "--listen", "127.0.0.1:40000"}, err: errSystemNATSURLRequired},
		{args: []string{"debug", "--system-nats-url", "nats://control", "--node-id", "node-1", "--listen", "127.0.0.1:40000"}, err: errServiceNameRequired},
		{args: []string{"debug", "--system-nats-url", "nats://control", "--node-id", "node-1", "--service", "orders"}, err: errDebugListenRequired},
	}
	for _, test := range invalid {
		_, err := parseInvocation(test.args, io.Discard)
		if !errors.Is(err, test.err) {
			t.Errorf("parseInvocation(%q) error = %v; want %v", test.args, err, test.err)
		}
	}
}

func TestExecute(t *testing.T) {
	client := &fakeControlClient{
		cluster: systemnats.ClusterView{Ready: true, Nodes: []systemnats.ClusterNode{
			{NodeID: "node-1", AdvertisedEndpoint: "nats-subject://system/node-1", Health: systemnats.HealthHealthy},
			{NodeID: "node-2", AdvertisedEndpoint: "nats-subject://system/node-2", Health: systemnats.HealthHealthy},
		}},
		components: map[string]systemnats.ComponentView{
			"node-1": {Components: []systemnats.ComponentStatus{{ServiceID: 1, Name: "Orders", State: systemnats.ComponentHealthy, Generation: 1}}},
			"node-2": {Components: []systemnats.ComponentStatus{{ServiceID: 2, Name: "Inventory", State: systemnats.ComponentStopped, Generation: 2}}},
		},
	}
	var output bytes.Buffer
	if err := execute(t.Context(), invocation{command: commandStatus, nodeID: "node-1"}, client, &output); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "Cluster     degraded\nNodes       2 / 2 healthy\nComponents  1 / 2 healthy\n"; got != want {
		t.Errorf("status output = %q; want %q", got, want)
	}

	output.Reset()
	if err := execute(t.Context(), invocation{command: commandNodes, nodeID: "node-1"}, client, &output); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "NODE    HEALTH   ENDPOINT\nnode-1  healthy  nats-subject://system/node-1\nnode-2  healthy  nats-subject://system/node-2\n"; got != want {
		t.Errorf("nodes output = %q; want %q", got, want)
	}

	output.Reset()
	if err := execute(t.Context(), invocation{command: commandComponents, nodeID: "node-1"}, client, &output); err != nil {
		t.Fatal(err)
	}
	wantComponents := "NODE    SERVICE  NAME       STATE    GENERATION  ERROR\n" +
		"node-1  1        Orders     healthy  1           -\n" +
		"node-2  2        Inventory  stopped  2           -\n"
	if got := output.String(); got != wantComponents {
		t.Errorf("components output = %q; want %q", got, wantComponents)
	}
}

func TestExecuteControlsComponent(t *testing.T) {
	client := &fakeControlClient{components: map[string]systemnats.ComponentView{
		"node-2": {Components: []systemnats.ComponentStatus{{ServiceID: 2, Name: "Inventory", State: systemnats.ComponentHealthy, Generation: 3}}},
	}}
	for _, action := range []componentAction{componentStart, componentStop} {
		var output bytes.Buffer
		parsed := invocation{command: commandComponent, action: action, nodeID: "node-2", serviceID: 2}
		if err := execute(t.Context(), parsed, client, &output); err != nil {
			t.Errorf("execute component %s: %v", action, err)
			continue
		}
		if client.action != action || client.nodeID != "node-2" || client.serviceID != 2 {
			t.Errorf("component %s call = action %q, node %q, service %d", action, client.action, client.nodeID, client.serviceID)
		}
		if !strings.Contains(output.String(), "node-2  2        Inventory  healthy  3") {
			t.Errorf("component %s output = %q", action, output.String())
		}
	}
	commandErr := errors.New("control request failed")
	client.commandErr = commandErr
	err := execute(t.Context(), invocation{
		command: commandComponent, action: componentStop, nodeID: "node-2", serviceID: 2,
	}, client, io.Discard)
	if !errors.Is(err, commandErr) {
		t.Errorf("execute failed component command error = %v; want %v", err, commandErr)
	}
}

type fakeControlClient struct {
	cluster    systemnats.ClusterView
	components map[string]systemnats.ComponentView
	action     componentAction
	nodeID     string
	serviceID  grove.ServiceID
	commandErr error
}

func (c *fakeControlClient) RequestClusterView(context.Context, string) (systemnats.ClusterView, error) {
	return c.cluster, nil
}

func (c *fakeControlClient) RequestComponents(_ context.Context, nodeID string) (systemnats.ComponentView, error) {
	return c.components[nodeID], nil
}

func (c *fakeControlClient) RequestPlacement(context.Context, string) (systemnats.PlacementView, error) {
	return systemnats.PlacementView{}, nil
}

func (c *fakeControlClient) RequestDeployments(context.Context, string) (systemnats.DeploymentView, error) {
	return systemnats.DeploymentView{}, nil
}

func (c *fakeControlClient) RequestHandlerPlacement(context.Context, string) (systemnats.HandlerPlacementView, error) {
	return systemnats.HandlerPlacementView{}, nil
}

func (c *fakeControlClient) RequestStartComponent(_ context.Context, nodeID string, serviceID grove.ServiceID) (systemnats.ComponentView, error) {
	c.action = componentStart
	c.nodeID = nodeID
	c.serviceID = serviceID
	return c.components[nodeID], c.commandErr
}

func (c *fakeControlClient) RequestStopComponent(_ context.Context, nodeID string, serviceID grove.ServiceID) (systemnats.ComponentView, error) {
	c.action = componentStop
	c.nodeID = nodeID
	c.serviceID = serviceID
	return c.components[nodeID], c.commandErr
}

// A real CLI process observes and controls three Grovlets before the public
// Grove call path proves the distributed application recovered.
func TestGroveCLILifecycleAgainstGrovletCluster(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	nodes, systemNATSURL := startGrovlets(t, ctx)
	defer stopGrovlets(t, nodes)

	wantStatus := "Cluster     healthy\nNodes       3 / 3 healthy\nComponents  2 / 2 healthy\n"
	if err := waitForGroveOutput(t, ctx, systemNATSURL, "node-1", wantStatus, "status"); err != nil {
		t.Fatalf("wait for Grove status: %v\n%s", err, grovletLogs(nodes))
	}
	wantNodes := "NODE    HEALTH   ENDPOINT\n" +
		"node-1  healthy  nats-subject://system/node-1\n" +
		"node-2  healthy  nats-subject://system/node-2\n" +
		"node-3  healthy  nats-subject://system/node-3\n"
	if err := waitForGroveOutput(t, ctx, systemNATSURL, "node-1", wantNodes, "nodes"); err != nil {
		t.Fatalf("wait for Grove nodes: %v\n%s", err, grovletLogs(nodes))
	}
	wantComponents := "NODE    SERVICE  NAME       STATE    GENERATION  ERROR\n" +
		"node-1  1        Orders     healthy  1           -\n" +
		"node-2  2        Inventory  healthy  1           -\n"
	if err := waitForGroveOutput(t, ctx, systemNATSURL, "node-1", wantComponents, "components"); err != nil {
		t.Fatalf("wait for Grove components: %v\n%s", err, grovletLogs(nodes))
	}

	stopped, err := runGroveCLI(t, ctx, systemNATSURL, "node-2", "component", "stop", "--service-id", "2")
	if err != nil {
		t.Fatalf("stop Inventory through Grove CLI: %v; output=%q\n%s", err, stopped, grovletLogs(nodes))
	}
	wantStopped := "NODE    SERVICE  NAME       STATE    GENERATION  ERROR\n" +
		"node-2  2        Inventory  stopped  2           -\n"
	if stopped != wantStopped {
		t.Errorf("stopped Inventory output = %q; want %q", stopped, wantStopped)
	}
	wantStoppedComponents := "NODE    SERVICE  NAME       STATE    GENERATION  ERROR\n" +
		"node-1  1        Orders     healthy  1           -\n" +
		"node-2  2        Inventory  stopped  2           -\n"
	if err := waitForGroveOutput(t, ctx, systemNATSURL, "node-1", wantStoppedComponents, "components"); err != nil {
		t.Fatalf("wait for stopped Inventory placement: %v\n%s", err, grovletLogs(nodes))
	}

	transport, err := systemnats.Connect(ctx, systemNATSURL)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	client, err := transport.ObservedPlacementClient("node-3")
	if err != nil {
		t.Fatal(err)
	}
	failedCtx, failedCancel := context.WithTimeout(ctx, time.Second)
	_, err = grove.Call[groveshop.CreateOrderRequest, groveshop.Order](
		failedCtx,
		client,
		groveshop.ServiceOrders,
		groveshop.MethodCreateOrder,
		groveshop.CreateOrderRequest{
			OrderID:         "order-inventory-stopped",
			SKU:             "coffee-beans",
			Quantity:        1,
			AmountCents:     1200,
			ShippingAddress: "21 Grove Lane",
		},
	)
	failedCancel()
	if err == nil {
		t.Fatal("order succeeded while Inventory was stopped")
	}

	restarted, err := runGroveCLI(t, ctx, systemNATSURL, "node-2", "component", "start", "--service-id", "2")
	if err != nil {
		t.Fatalf("start Inventory through Grove CLI: %v; output=%q\n%s", err, restarted, grovletLogs(nodes))
	}
	wantRestarted := "NODE    SERVICE  NAME       STATE    GENERATION  ERROR\n" +
		"node-2  2        Inventory  healthy  3           -\n"
	if restarted != wantRestarted {
		t.Errorf("restarted Inventory output = %q; want %q", restarted, wantRestarted)
	}
	if err := waitForGroveOutput(t, ctx, systemNATSURL, "node-3", wantStatus, "status"); err != nil {
		t.Fatalf("wait for recovered Grove status: %v\n%s", err, grovletLogs(nodes))
	}
	if err := waitForObservedServices(ctx, transport, "node-3", groveshop.ServiceOrders, groveshop.ServiceInventory); err != nil {
		t.Fatalf("wait for recovered placement: %v\n%s", err, grovletLogs(nodes))
	}

	created, err := grove.Call[groveshop.CreateOrderRequest, groveshop.Order](
		ctx,
		client,
		groveshop.ServiceOrders,
		groveshop.MethodCreateOrder,
		groveshop.CreateOrderRequest{
			OrderID:         "order-cli-restarted",
			SKU:             "coffee-beans",
			Quantity:        2,
			AmountCents:     2400,
			ShippingAddress: "22 Grove Lane",
		},
	)
	if err != nil {
		t.Fatalf("order after CLI restart: %v\n%s", err, grovletLogs(nodes))
	}
	if created.Status != groveshop.OrderCompleted {
		t.Errorf("order after CLI restart status = %q; want %q", created.Status, groveshop.OrderCompleted)
	}
	if created.Reservation.ID != "reservation-order-cli-restarted" {
		t.Errorf("order after CLI restart reservation = %q; want reservation-order-cli-restarted", created.Reservation.ID)
	}
}

func waitForGroveOutput(t testing.TB, ctx context.Context, systemNATSURL, nodeID, want string, args ...string) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var lastOutput string
	var lastErr error
	for {
		attemptCtx, attemptCancel := context.WithTimeout(ctx, time.Second)
		lastOutput, lastErr = runGroveCLI(t, attemptCtx, systemNATSURL, nodeID, args...)
		attemptCancel()
		if lastErr == nil && lastOutput == want {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("grove %s did not converge: output=%q error=%v: %w", strings.Join(args, " "), lastOutput, lastErr, ctx.Err())
		}
	}
}

func runGroveCLI(t testing.TB, ctx context.Context, systemNATSURL, nodeID string, args ...string) (string, error) {
	commandArgs := append([]string(nil), args...)
	commandArgs = append(commandArgs, "--system-nats-url", systemNATSURL, "--node-id", nodeID)
	return runGroveCommand(t, ctx, commandArgs...)
}

func runGroveCommand(t testing.TB, ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, grovePath(t), args...)
	output, err := command.CombinedOutput()
	return string(output), err
}

func startGrovlets(t *testing.T, ctx context.Context) ([]*grovetest.Node, string) {
	return startGrovletsFromArtifact(t, ctx, grovletPath(t))
}

func startGrovletsFromArtifact(t *testing.T, ctx context.Context, artifactPath string) ([]*grovetest.Node, string) {
	t.Helper()
	nodeIDs := []string{"node-1", "node-2", "node-3"}
	nodeArgs := [][]string{
		{"--system-nats-subject", "_GROVE.system.cli.node-1", "--component", "orders"},
		{"--system-nats-subject", "_GROVE.system.cli.node-2", "--component", "inventory"},
		{"--system-nats-subject", "_GROVE.system.cli.node-3"},
	}
	ports := reserveRoutePorts(t, len(nodeIDs))
	nodes := make([]*grovetest.Node, 0, len(nodeIDs))
	for i, nodeID := range nodeIDs {
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
		args = append(args, nodeArgs[i]...)
		node, err := grovetest.StartNode(artifactPath, args...)
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, node)
		t.Cleanup(func() {
			if err := node.Cleanup(); err != nil {
				t.Errorf("cleanup %s: %v", nodeID, err)
			}
		})
	}
	for _, node := range nodes {
		if err := node.WaitReady(ctx); err != nil {
			t.Fatalf("wait for Grovlets: %v\n%s", err, grovletLogs(nodes))
		}
	}
	return nodes, readySystemNATSURL(t, nodes[0].Logs())
}

func reserveRoutePorts(t *testing.T, count int) []int {
	t.Helper()
	listeners := make([]net.Listener, 0, count)
	defer func() {
		for _, listener := range listeners {
			if err := listener.Close(); err != nil {
				t.Errorf("close route-port reservation: %v", err)
			}
		}
	}()
	ports := make([]int, 0, count)
	for range count {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, listener)
		_, portText, err := net.SplitHostPort(listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			t.Fatal(err)
		}
		ports = append(ports, port)
	}
	return ports
}

func readySystemNATSURL(t *testing.T, logs string) string {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(logs))
	for {
		var event struct {
			Event         string `json:"event"`
			SystemNATSURL string `json:"system_nats_url"`
		}
		if err := decoder.Decode(&event); err != nil {
			t.Fatalf("decode Grovlet ready event: %v; logs=%q", err, logs)
		}
		if event.Event == "ready" {
			return event.SystemNATSURL
		}
	}
}

func stopGrovlets(t *testing.T, nodes []*grovetest.Node) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wait sync.WaitGroup
	for _, node := range nodes {
		wait.Go(func() {
			if err := node.Stop(ctx); err != nil {
				t.Errorf("stop Grovlet: %v\n%s", err, grovletLogs(nodes))
			}
		})
	}
	wait.Wait()
}

func cleanupGrovetestNodes(nodes []*grovetest.Node) {
	for _, node := range nodes {
		_ = node.Cleanup()
	}
}

func grovletLogs(nodes []*grovetest.Node) string {
	var logs strings.Builder
	for i, node := range nodes {
		fmt.Fprintf(&logs, "node-%d logs:\n%s\n", i+1, node.Logs())
	}
	return logs.String()
}

func TestMain(m *testing.M) {
	code := m.Run()
	if err := testbin.Cleanup(); err != nil && code == 0 {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}

func buildGrove(ctx context.Context, outputDir string) (string, error) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("runtime caller information is unavailable")
	}
	moduleDir := filepath.Clean(filepath.Join(filepath.Dir(filename), "../.."))
	binaryPath := filepath.Join(outputDir, "grove")
	cmd := exec.CommandContext(ctx, "go", "build", "-o", binaryPath, "./cmd/grove")
	cmd.Dir = moduleDir
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("build Grove CLI: %w: %s", err, output.String())
	}
	return binaryPath, nil
}
