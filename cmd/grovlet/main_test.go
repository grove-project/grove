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
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/grovetest"
	"github.com/grove-project/grove/internal/systemnats"
)

var grovletPath string

// A Grovlet announces when it is ready and when a graceful shutdown completes.
func Example() {
	runtimeDir, err := os.MkdirTemp("", "grovlet-example-")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer os.RemoveAll(runtimeDir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var stdout bytes.Buffer
	if err := run(ctx, []string{"--runtime-dir", runtimeDir}, &stdout, io.Discard); err != nil {
		fmt.Println(err)
		return
	}

	fmt.Print(stdout.String())
	// Output:
	// {"event":"ready"}
	// {"event":"stopped"}
}

func TestParseConfig(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	cfg, err := parseConfig([]string{
		"--runtime-dir", runtimeDir,
		"--system-nats-url", "nats://127.0.0.1:4222",
		"--system-nats-subject", "_GROVE.system.invoke.node-a",
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.runtimeDir != runtimeDir {
		t.Errorf("runtime directory = %q; want %q", cfg.runtimeDir, runtimeDir)
	}
	if cfg.systemNATSURL != "nats://127.0.0.1:4222" {
		t.Errorf("System NATS URL = %q; want nats://127.0.0.1:4222", cfg.systemNATSURL)
	}
	if cfg.systemNATSSubject != "_GROVE.system.invoke.node-a" {
		t.Errorf("System NATS subject = %q; want _GROVE.system.invoke.node-a", cfg.systemNATSSubject)
	}
	clusterCfg, err := parseConfig([]string{
		"--runtime-dir", runtimeDir,
		"--node-id", "node-a",
		"--advertise-endpoint", "nats-subject://system/node-a",
		"--system-nats-listen", "127.0.0.1:0",
		"--system-nats-route-listen", "127.0.0.1:0",
		"--system-nats-seed", "nats-route://127.0.0.1:6222",
		"--system-nats-membership",
		"--system-nats-subject", "_GROVE.system.invoke.node-a",
		"--grove-shop-orders",
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if clusterCfg.systemNATSRouteListen != "127.0.0.1:0" {
		t.Errorf("System NATS route listener = %q; want 127.0.0.1:0", clusterCfg.systemNATSRouteListen)
	}
	if clusterCfg.systemNATSSeed != "nats-route://127.0.0.1:6222" {
		t.Errorf("System NATS seed = %q; want nats-route://127.0.0.1:6222", clusterCfg.systemNATSSeed)
	}
	if !clusterCfg.systemNATSMembership {
		t.Error("System NATS membership is disabled; want enabled")
	}
	if !clusterCfg.groveShopOrders {
		t.Error("Grove Shop Orders placement is disabled; want enabled")
	}

	if _, err := parseConfig(nil, io.Discard); !errors.Is(err, errRuntimeDirRequired) {
		t.Errorf("error = %v; want %v", err, errRuntimeDirRequired)
	}
	if _, err := parseConfig([]string{
		"--runtime-dir", runtimeDir,
		"--system-nats-listen", "127.0.0.1:4222",
		"--system-nats-url", "nats://127.0.0.1:4222",
	}, io.Discard); !errors.Is(err, errSystemNATSConflict) {
		t.Errorf("listen and URL error = %v; want %v", err, errSystemNATSConflict)
	}
	if _, err := parseConfig([]string{
		"--runtime-dir", runtimeDir,
		"--system-nats-subject", "_GROVE.system.invoke.node-a",
	}, io.Discard); !errors.Is(err, errSystemNATSRequired) {
		t.Errorf("endpoint without connection error = %v; want %v", err, errSystemNATSRequired)
	}
	if _, err := parseConfig([]string{
		"--runtime-dir", runtimeDir,
		"--system-nats-url", "nats://127.0.0.1:4222",
		"--system-nats-subject", "_GROVE.system.invoke.node-a",
		"--grove-shop-orders",
	}, io.Discard); !errors.Is(err, errGroveShopPlacementCluster) {
		t.Errorf("Orders without placement cluster error = %v; want %v", err, errGroveShopPlacementCluster)
	}
	if _, err := parseConfig([]string{
		"--runtime-dir", runtimeDir,
		"--node-id", "node-a",
		"--advertise-endpoint", "nats-subject://system/node-a",
		"--system-nats-listen", "127.0.0.1:0",
		"--system-nats-route-listen", "127.0.0.1:0",
		"--system-nats-seed", "nats-route://127.0.0.1:6222",
		"--system-nats-membership",
		"--system-nats-subject", "_GROVE.system.invoke.node-a",
		"--grove-shop-orders",
		"--grove-shop-orders-inventory-subject", "_GROVE.system.invoke.node-b",
	}, io.Discard); !errors.Is(err, errGroveShopOrdersConflict) {
		t.Errorf("Orders destination conflict error = %v; want %v", err, errGroveShopOrdersConflict)
	}
	if _, err := parseConfig([]string{
		"--runtime-dir", runtimeDir,
		"--system-nats-route-listen", "127.0.0.1:0",
	}, io.Discard); !errors.Is(err, errSystemNATSRouteListenRequired) {
		t.Errorf("route without embedded server error = %v; want %v", err, errSystemNATSRouteListenRequired)
	}
	if _, err := parseConfig([]string{
		"--runtime-dir", runtimeDir,
		"--system-nats-listen", "127.0.0.1:0",
		"--system-nats-seed", "nats-route://127.0.0.1:6222",
	}, io.Discard); !errors.Is(err, errSystemNATSSeedRouteRequired) {
		t.Errorf("seed without route listener error = %v; want %v", err, errSystemNATSSeedRouteRequired)
	}
	if _, err := parseConfig([]string{
		"--runtime-dir", runtimeDir,
		"--system-nats-listen", "127.0.0.1:0",
		"--system-nats-route-listen", "127.0.0.1:0",
		"--system-nats-membership",
	}, io.Discard); !errors.Is(err, errSystemNATSMembershipCluster) {
		t.Errorf("membership without seed error = %v; want %v", err, errSystemNATSMembershipCluster)
	}
	if _, err := parseConfig([]string{
		"--runtime-dir", runtimeDir,
		"--system-nats-listen", "127.0.0.1:0",
		"--system-nats-route-listen", "127.0.0.1:0",
	}, io.Discard); !errors.Is(err, errSystemNATSClusterIdentity) {
		t.Errorf("cluster without identity error = %v; want %v", err, errSystemNATSClusterIdentity)
	}
	if _, err := parseConfig([]string{
		"--runtime-dir", runtimeDir,
		"--node-id", "node-a",
		"--advertise-endpoint", "nats-subject://system/node-a",
		"--system-nats-listen", "127.0.0.1:0",
		"--system-nats-route-listen", "127.0.0.1:0",
		"--system-nats-seed", "nats://127.0.0.1:6222",
	}, io.Discard); !errors.Is(err, systemnats.ErrSeedURLInvalid) {
		t.Errorf("invalid seed URL error = %v; want %v", err, systemnats.ErrSeedURLInvalid)
	}
	if _, err := parseConfig([]string{
		"--runtime-dir", runtimeDir,
		"--node-id", "node-a",
	}, io.Discard); !errors.Is(err, errNodeIdentityPair) {
		t.Errorf("incomplete identity error = %v; want %v", err, errNodeIdentityPair)
	}
	if _, err := parseConfig([]string{
		"--runtime-dir", runtimeDir,
		"--node-id", "bad node",
		"--advertise-endpoint", "nats-subject://system/node",
	}, io.Discard); !errors.Is(err, errNodeIDInvalid) {
		t.Errorf("invalid node ID error = %v; want %v", err, errNodeIDInvalid)
	}
	if _, err := parseConfig([]string{
		"--runtime-dir", runtimeDir,
		"--node-id", "node-a",
		"--advertise-endpoint", "relative-endpoint",
	}, io.Discard); !errors.Is(err, errAdvertiseInvalid) {
		t.Errorf("invalid advertised endpoint error = %v; want %v", err, errAdvertiseInvalid)
	}
}

func TestPrepareRuntimeDir(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	if err := prepareRuntimeDir(runtimeDir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Errorf("runtime path %q is not a directory", runtimeDir)
	}

	runtimeFile := filepath.Join(t.TempDir(), "runtime-file")
	if err := os.WriteFile(runtimeFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err = prepareRuntimeDir(runtimeFile)
	var runtimeErr runtimeDirError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("error type = %T; want runtimeDirError", err)
	}
	if runtimeErr.path != runtimeFile {
		t.Errorf("error path = %q; want %q", runtimeErr.path, runtimeFile)
	}
}

func TestRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		var stdout bytes.Buffer
		done := make(chan error, 1)
		go func() {
			done <- run(ctx, []string{"--runtime-dir", t.TempDir()}, &stdout, io.Discard)
		}()

		synctest.Wait()
		const readyOutput = "{\"event\":\"ready\"}\n"
		if got := stdout.String(); got != readyOutput {
			t.Errorf("output before shutdown = %q; want %q", got, readyOutput)
		}
		select {
		case err := <-done:
			t.Fatalf("run returned before shutdown: %v", err)
		default:
		}

		cancel()
		synctest.Wait()
		if err := <-done; err != nil {
			t.Fatal(err)
		}

		const stoppedOutput = readyOutput + "{\"event\":\"stopped\"}\n"
		if got := stdout.String(); got != stoppedOutput {
			t.Errorf("output after shutdown = %q; want %q", got, stoppedOutput)
		}
	})

	wantErr := errors.New("write failed")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := run(ctx, []string{"--runtime-dir", t.TempDir()}, errorWriter{err: wantErr}, io.Discard); !errors.Is(err, wantErr) {
		t.Errorf("error = %v; want %v", err, wantErr)
	}
}

func TestGrovletProcess(t *testing.T) {
	node, err := grovetest.StartNode(grovletPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := node.Cleanup(); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})

	waitCtx, cancelWait := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelWait()
	if err := node.WaitReady(waitCtx); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(node.TempDir()); err != nil || !info.IsDir() {
		t.Fatalf("runtime directory is not ready: %v; logs: %q", err, node.Logs())
	}
	if err := node.Stop(waitCtx); err != nil {
		t.Fatal(err)
	}
}

// Two Grovlets must exchange the transport-independent envelope through the
// embedded System NATS process rather than a same-process shortcut.
func TestGrovletSystemNATSTransport(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	const (
		hostSubject = "_GROVE.system.invoke.host"
		peerSubject = "_GROVE.system.invoke.peer"
	)

	host, err := grovetest.StartNode(
		grovletPath,
		"--system-nats-listen", "127.0.0.1:0",
		"--system-nats-subject", hostSubject,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Cleanup(); err != nil {
			t.Errorf("cleanup host: %v", err)
		}
	})
	if err := host.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	serverURL := readyEventFromLogs(t, host.Logs()).SystemNATSURL

	peer, err := grovetest.StartNode(
		grovletPath,
		"--system-nats-url", serverURL,
		"--system-nats-subject", peerSubject,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := peer.Cleanup(); err != nil {
			t.Errorf("cleanup peer: %v", err)
		}
	})
	if err := peer.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}

	transport, err := systemnats.Connect(ctx, serverURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.Close)
	payload, err := grove.Encode("inventory request")
	if err != nil {
		t.Fatal(err)
	}
	request := grove.RequestEnvelope{
		RequestID: "request-between-grovlets",
		ServiceID: 2,
		MethodID:  1,
		Payload:   payload,
	}
	for _, subject := range []string{hostSubject, peerSubject} {
		response, err := transport.Request(ctx, subject, request)
		if err != nil {
			t.Fatal(err)
		}
		var got string
		if err := grove.Decode(response.Payload, &got); err != nil {
			t.Fatal(err)
		}
		if got != "inventory request" {
			t.Errorf("response through %s = %q; want inventory request", subject, got)
		}
	}

	if err := peer.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := host.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

// Three independently embedded NATS servers exchange control traffic after two
// Grovlets join through the first Grovlet's explicit seed route.
func TestGrovletSystemNATSCluster(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	subjects := []string{
		"_GROVE.system.invoke.cluster.node-1",
		"_GROVE.system.invoke.cluster.node-2",
		"_GROVE.system.invoke.cluster.node-3",
	}
	nodes := make([]*grovetest.Node, 0, len(subjects))
	readyEvents := make([]lifecycleEvent, 0, len(subjects))

	seed, err := grovetest.StartNode(
		grovletPath,
		"--node-id", "node-1",
		"--advertise-endpoint", "nats-subject://system/node-1",
		"--system-nats-listen", "127.0.0.1:0",
		"--system-nats-route-listen", "127.0.0.1:0",
		"--system-nats-subject", subjects[0],
	)
	if err != nil {
		t.Fatal(err)
	}
	nodes = append(nodes, seed)
	t.Cleanup(func() {
		if err := seed.Cleanup(); err != nil {
			t.Errorf("cleanup seed: %v", err)
		}
	})
	if err := seed.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	seedReady := readyEventFromLogs(t, seed.Logs())
	readyEvents = append(readyEvents, seedReady)
	if seedReady.SystemNATSRouteURL == "" {
		t.Fatalf("seed route URL is empty; logs: %q", seed.Logs())
	}

	for i := 1; i < len(subjects); i++ {
		nodeID := fmt.Sprintf("node-%d", i+1)
		node, err := grovetest.StartNode(
			grovletPath,
			"--node-id", nodeID,
			"--advertise-endpoint", "nats-subject://system/"+nodeID,
			"--system-nats-listen", "127.0.0.1:0",
			"--system-nats-route-listen", "127.0.0.1:0",
			"--system-nats-seed", seedReady.SystemNATSRouteURL,
			"--system-nats-subject", subjects[i],
		)
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, node)
		t.Cleanup(func() {
			if err := node.Cleanup(); err != nil {
				t.Errorf("cleanup %s: %v", nodeID, err)
			}
		})
		if err := node.WaitReady(ctx); err != nil {
			t.Fatal(err)
		}
		readyEvents = append(readyEvents, readyEventFromLogs(t, node.Logs()))
	}

	clientURLs := make(map[string]struct{}, len(readyEvents))
	routeURLs := make(map[string]struct{}, len(readyEvents))
	for i, ready := range readyEvents {
		wantNodeID := fmt.Sprintf("node-%d", i+1)
		if ready.NodeID != wantNodeID {
			t.Errorf("ready node ID = %q; want %q", ready.NodeID, wantNodeID)
		}
		if ready.SystemNATSURL == "" || ready.SystemNATSRouteURL == "" {
			t.Errorf("%s readiness lacks NATS client or route URL: %#v", wantNodeID, ready)
		}
		clientURLs[ready.SystemNATSURL] = struct{}{}
		routeURLs[ready.SystemNATSRouteURL] = struct{}{}
	}
	if len(clientURLs) != len(nodes) || len(routeURLs) != len(nodes) {
		t.Errorf("cluster addresses are not distinct: client=%v route=%v", clientURLs, routeURLs)
	}

	transport, err := systemnats.Connect(ctx, seedReady.SystemNATSURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.Close)
	payload, err := grove.Encode("shared control plane")
	if err != nil {
		t.Fatal(err)
	}
	for i, subject := range subjects {
		request := grove.RequestEnvelope{
			RequestID: fmt.Sprintf("request-node-%d", i+1),
			ServiceID: 2,
			MethodID:  1,
			Payload:   payload,
		}
		response, err := requestGrovletEventually(ctx, transport, subject, request)
		if err != nil {
			t.Fatalf("request %s through seed: %v\n%s", subject, err, clusterLogs(nodes))
		}
		var got string
		if err := grove.Decode(response.Payload, &got); err != nil {
			t.Fatal(err)
		}
		if response.RequestID != request.RequestID || got != "shared control plane" {
			t.Errorf("response through %s = %#v, %q; want correlated shared control plane", subject, response, got)
		}
	}

	for i := len(nodes) - 1; i >= 0; i-- {
		if err := nodes[i].Stop(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func requestGrovletEventually(
	ctx context.Context,
	transport *systemnats.Transport,
	subject string,
	request grove.RequestEnvelope,
) (grove.ResponseEnvelope, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		requestCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		response, err := transport.Request(requestCtx, subject, request)
		cancel()
		if err == nil {
			return response, nil
		}
		lastErr = err
		if err := ctx.Err(); err != nil {
			return grove.ResponseEnvelope{}, errors.Join(lastErr, err)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return grove.ResponseEnvelope{}, errors.Join(lastErr, ctx.Err())
		}
	}
}

func clusterLogs(nodes []*grovetest.Node) string {
	var logs strings.Builder
	for i, node := range nodes {
		nodeLogs := node.Logs()
		fmt.Fprintf(&logs, "node-%d logs:\n%s", i+1, nodeLogs)
		if !strings.HasSuffix(nodeLogs, "\n") {
			logs.WriteByte('\n')
		}
	}
	return logs.String()
}

// Every Grovlet reports the same logical membership after its local watcher
// converges on the replicated JetStream/KV bucket.
func TestGrovletMembershipConverges(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cluster := startMembershipGrovlets(t, ctx)

	want := []systemnats.MembershipRecord{
		{NodeID: cluster.nodeIDs[0], AdvertisedEndpoint: cluster.endpoints[0]},
		{NodeID: cluster.nodeIDs[1], AdvertisedEndpoint: cluster.endpoints[1]},
		{NodeID: cluster.nodeIDs[2], AdvertisedEndpoint: cluster.endpoints[2]},
	}
	views, err := waitForGrovletMembership(ctx, cluster.transports, cluster.nodeIDs, want)
	if err != nil {
		t.Fatalf("%v\n%s", err, clusterLogs(cluster.nodes))
	}
	for i, view := range views {
		if view.Error != "" || !slices.Equal(view.Members, want) {
			t.Errorf("%s membership = %#v; want %#v", cluster.nodeIDs[i], view, want)
		}
	}

	for i := len(cluster.nodes) - 1; i >= 0; i-- {
		if err := cluster.nodes[i].Stop(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

type membershipGrovlets struct {
	nodeIDs    []string
	endpoints  []string
	nodes      []*grovetest.Node
	transports []*systemnats.Transport
}

func startMembershipGrovlets(t *testing.T, ctx context.Context, nodeArgs ...[]string) membershipGrovlets {
	t.Helper()
	cluster := membershipGrovlets{
		nodeIDs: []string{"node-1", "node-2", "node-3"},
		endpoints: []string{
			"nats-subject://system/node-1",
			"nats-subject://system/node-2",
			"nats-subject://system/node-3",
		},
	}
	routePorts := reserveGrovletRoutePorts(t, len(cluster.nodeIDs))
	if len(nodeArgs) != 0 && len(nodeArgs) != len(cluster.nodeIDs) {
		t.Fatalf("node argument sets = %d; want 0 or %d", len(nodeArgs), len(cluster.nodeIDs))
	}
	for i, nodeID := range cluster.nodeIDs {
		seedIndex := 0
		if i == 0 {
			seedIndex = 1
		}
		args := []string{
			"--node-id", nodeID,
			"--advertise-endpoint", cluster.endpoints[i],
			"--system-nats-listen", "127.0.0.1:0",
			"--system-nats-route-listen", fmt.Sprintf("127.0.0.1:%d", routePorts[i]),
			"--system-nats-seed", fmt.Sprintf("nats-route://127.0.0.1:%d", routePorts[seedIndex]),
			"--system-nats-membership",
		}
		if len(nodeArgs) != 0 {
			args = append(args, nodeArgs[i]...)
		}
		node, err := grovetest.StartNode(grovletPath, args...)
		if err != nil {
			t.Fatal(err)
		}
		cluster.nodes = append(cluster.nodes, node)
		t.Cleanup(func() {
			if err := node.Cleanup(); err != nil {
				t.Errorf("cleanup %s: %v", nodeID, err)
			}
		})
	}
	for i, node := range cluster.nodes {
		if err := node.WaitReady(ctx); err != nil {
			t.Fatalf("wait for %s: %v\n%s", cluster.nodeIDs[i], err, clusterLogs(cluster.nodes))
		}
		ready := readyEventFromLogs(t, node.Logs())
		transport, err := systemnats.Connect(ctx, ready.SystemNATSURL)
		if err != nil {
			t.Fatalf("connect to %s: %v\n%s", cluster.nodeIDs[i], err, clusterLogs(cluster.nodes))
		}
		cluster.transports = append(cluster.transports, transport)
		t.Cleanup(transport.Close)
	}
	return cluster
}

func reserveGrovletRoutePorts(t *testing.T, count int) []int {
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
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
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

func waitForGrovletMembership(
	ctx context.Context,
	transports []*systemnats.Transport,
	nodeIDs []string,
	want []systemnats.MembershipRecord,
) ([]systemnats.MembershipView, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	views := make([]systemnats.MembershipView, len(nodeIDs))
	var lastErr error
	for {
		converged := true
		for i, nodeID := range nodeIDs {
			requestCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
			view, err := transports[i].RequestMembership(requestCtx, nodeID)
			cancel()
			if err != nil {
				lastErr = err
				converged = false
				continue
			}
			views[i] = view
			if !view.Ready || !slices.Equal(view.Members, want) {
				converged = false
			}
		}
		if converged {
			return views, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("grovlet membership did not converge: views=%#v: %w", views, errors.Join(lastErr, err))
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil, fmt.Errorf("grovlet membership did not converge: views=%#v: %w", views, errors.Join(lastErr, ctx.Err()))
		}
	}
}

// Both surviving Grovlets retain the killed node's membership record and
// independently transition its heartbeat-derived health to unavailable.
func TestGrovletNodeHealthConverges(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cluster := startMembershipGrovlets(t, ctx)
	allNodes := []int{0, 1, 2}
	healthyViews, err := waitForGrovletHealth(ctx, cluster, allNodes, func(view systemnats.ClusterView) bool {
		if !view.Ready || len(view.Nodes) != 3 {
			return false
		}
		for _, node := range view.Nodes {
			if node.Health != systemnats.HealthHealthy || node.LastSeen == "" {
				return false
			}
		}
		return true
	})
	if err != nil {
		t.Fatalf("wait for healthy cluster: %v\n%s", err, clusterLogs(cluster.nodes))
	}
	for i, view := range healthyViews {
		if view.Nodes[0].NodeID != "node-1" || view.Nodes[1].NodeID != "node-2" || view.Nodes[2].NodeID != "node-3" {
			t.Errorf("%s healthy view order = %#v", cluster.nodeIDs[i], view.Nodes)
		}
	}

	if err := cluster.nodes[1].Kill(ctx); err != nil {
		t.Fatal(err)
	}
	survivors := []int{0, 2}
	unavailableViews, err := waitForGrovletHealth(ctx, cluster, survivors, func(view systemnats.ClusterView) bool {
		return view.Ready &&
			len(view.Nodes) == 3 &&
			view.Nodes[0].NodeID == "node-1" &&
			view.Nodes[0].Health == systemnats.HealthHealthy &&
			view.Nodes[1].NodeID == "node-2" &&
			view.Nodes[1].Health == systemnats.HealthUnavailable &&
			view.Nodes[2].NodeID == "node-3" &&
			view.Nodes[2].Health == systemnats.HealthHealthy
	})
	if err != nil {
		t.Fatalf("wait for unavailable node: %v\n%s", err, clusterLogs(cluster.nodes))
	}
	for i, view := range unavailableViews {
		if view.Nodes[1].AdvertisedEndpoint != cluster.endpoints[1] || view.Nodes[1].LastSeen == "" {
			t.Errorf("%s unavailable node = %#v; want retained node-2 identity and last seen", cluster.nodeIDs[survivors[i]], view.Nodes[1])
		}
	}

	for _, i := range []int{2, 0} {
		if err := cluster.nodes[i].Stop(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func waitForGrovletHealth(
	ctx context.Context,
	cluster membershipGrovlets,
	observers []int,
	condition func(systemnats.ClusterView) bool,
) ([]systemnats.ClusterView, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	views := make([]systemnats.ClusterView, len(observers))
	var lastErr error
	for {
		converged := true
		for i, observer := range observers {
			requestCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
			view, err := cluster.transports[observer].RequestClusterView(requestCtx, cluster.nodeIDs[observer])
			cancel()
			if err != nil {
				lastErr = err
				converged = false
				continue
			}
			views[i] = view
			if !condition(view) {
				converged = false
			}
		}
		if converged {
			return views, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("grovlet health did not converge: views=%#v: %w", views, errors.Join(lastErr, err))
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil, fmt.Errorf("grovlet health did not converge: views=%#v: %w", views, errors.Join(lastErr, ctx.Err()))
		}
	}
}

// Every Grovlet observes Orders on node 1 and Inventory on node 2 before the
// Orders implementation resolves its Inventory call from replicated placement.
func TestGrovletServicePlacementConvergesAndRoutes(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	const (
		ordersSubject    = "_GROVE.system.invoke.node-1"
		inventorySubject = "_GROVE.system.invoke.node-2"
		observerSubject  = "_GROVE.system.invoke.node-3"
	)
	cluster := startMembershipGrovlets(
		t,
		ctx,
		[]string{"--system-nats-subject", ordersSubject, "--grove-shop-orders"},
		[]string{"--system-nats-subject", inventorySubject, "--grove-shop-inventory"},
		[]string{"--system-nats-subject", observerSubject},
	)
	want := []systemnats.PlacementRecord{
		{
			ServiceID:         groveshop.ServiceOrders,
			NodeID:            cluster.nodeIDs[0],
			InvocationSubject: componentInvocationSubject(ordersSubject, groveshop.ServiceOrders),
		},
		{
			ServiceID:         groveshop.ServiceInventory,
			NodeID:            cluster.nodeIDs[1],
			InvocationSubject: componentInvocationSubject(inventorySubject, groveshop.ServiceInventory),
		},
	}
	views, err := waitForGrovletPlacement(ctx, cluster, want)
	if err != nil {
		t.Fatalf("%v\n%s", err, clusterLogs(cluster.nodes))
	}
	for i, view := range views {
		if view.Error != "" || !slices.Equal(view.Placements, want) {
			t.Errorf("%s placement = %#v; want %#v", cluster.nodeIDs[i], view, want)
		}
	}

	client, err := cluster.transports[2].RoutedClient(want[0].InvocationSubject)
	if err != nil {
		t.Fatal(err)
	}
	created, err := grove.Call[groveshop.CreateOrderRequest, groveshop.Order](
		ctx,
		client,
		groveshop.ServiceOrders,
		groveshop.MethodCreateOrder,
		groveshop.CreateOrderRequest{
			OrderID:         "order-placement",
			SKU:             "coffee-beans",
			Quantity:        2,
			AmountCents:     2400,
			ShippingAddress: "15 Grove Lane",
		},
	)
	if err != nil {
		t.Fatalf("placement-routed order: %v\n%s", err, clusterLogs(cluster.nodes))
	}
	if created.Status != groveshop.OrderCompleted {
		t.Errorf("placement-routed order status = %q; want %q", created.Status, groveshop.OrderCompleted)
	}
	if created.Reservation.ID != "reservation-order-placement" {
		t.Errorf("placement-routed reservation ID = %q; want reservation-order-placement", created.Reservation.ID)
	}

	for i := len(cluster.nodes) - 1; i >= 0; i-- {
		if err := cluster.nodes[i].Stop(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func waitForGrovletPlacement(
	ctx context.Context,
	cluster membershipGrovlets,
	want []systemnats.PlacementRecord,
) ([]systemnats.PlacementView, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	views := make([]systemnats.PlacementView, len(cluster.nodeIDs))
	var lastErr error
	for {
		converged := true
		for i, nodeID := range cluster.nodeIDs {
			requestCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
			view, err := cluster.transports[i].RequestPlacement(requestCtx, nodeID)
			cancel()
			if err != nil {
				lastErr = err
				converged = false
				continue
			}
			views[i] = view
			if !view.Ready || !slices.Equal(view.Placements, want) {
				converged = false
			}
		}
		if converged {
			return views, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("grovlet placement did not converge: views=%#v: %w", views, errors.Join(lastErr, err))
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil, fmt.Errorf("grovlet placement did not converge: views=%#v: %w", views, errors.Join(lastErr, ctx.Err()))
		}
	}
}

// A Grovlet stops and restarts its hosted Inventory worker while replicated
// placement remains stable and the cross-node Orders flow becomes healthy
// again.
func TestGrovletComponentLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	const (
		ordersSubject    = "_GROVE.system.lifecycle.node-1"
		inventorySubject = "_GROVE.system.lifecycle.node-2"
		observerSubject  = "_GROVE.system.lifecycle.node-3"
	)
	cluster := startMembershipGrovlets(
		t,
		ctx,
		[]string{"--system-nats-subject", ordersSubject, "--grove-shop-orders"},
		[]string{"--system-nats-subject", inventorySubject, "--grove-shop-inventory"},
		[]string{"--system-nats-subject", observerSubject},
	)
	wantPlacement := []systemnats.PlacementRecord{
		{ServiceID: groveshop.ServiceOrders, NodeID: "node-1", InvocationSubject: componentInvocationSubject(ordersSubject, groveshop.ServiceOrders)},
		{ServiceID: groveshop.ServiceInventory, NodeID: "node-2", InvocationSubject: componentInvocationSubject(inventorySubject, groveshop.ServiceInventory)},
	}
	if _, err := waitForGrovletPlacement(ctx, cluster, wantPlacement); err != nil {
		t.Fatalf("%v\n%s", err, clusterLogs(cluster.nodes))
	}
	if _, err := waitForGrovletComponentState(ctx, cluster.transports[1], "node-2", groveshop.ServiceInventory, systemnats.ComponentHealthy); err != nil {
		t.Fatalf("wait for healthy Inventory: %v\n%s", err, clusterLogs(cluster.nodes))
	}
	if _, err := cluster.transports[0].RequestStopComponent(ctx, "node-2", groveshop.ServiceInventory); err != nil {
		t.Fatalf("stop Inventory: %v\n%s", err, clusterLogs(cluster.nodes))
	}
	if _, err := waitForGrovletComponentState(ctx, cluster.transports[1], "node-2", groveshop.ServiceInventory, systemnats.ComponentStopped); err != nil {
		t.Fatalf("wait for stopped Inventory: %v\n%s", err, clusterLogs(cluster.nodes))
	}
	if _, err := waitForGrovletPlacement(ctx, cluster, wantPlacement); err != nil {
		t.Fatalf("placement changed after stop: %v\n%s", err, clusterLogs(cluster.nodes))
	}
	if _, err := cluster.transports[2].RequestStartComponent(ctx, "node-2", groveshop.ServiceInventory); err != nil {
		t.Fatalf("restart Inventory: %v\n%s", err, clusterLogs(cluster.nodes))
	}
	if _, err := waitForGrovletComponentState(ctx, cluster.transports[1], "node-2", groveshop.ServiceInventory, systemnats.ComponentHealthy); err != nil {
		t.Fatalf("wait for restarted Inventory: %v\n%s", err, clusterLogs(cluster.nodes))
	}

	client, err := cluster.transports[2].RoutedClient(wantPlacement[0].InvocationSubject)
	if err != nil {
		t.Fatal(err)
	}
	created, err := grove.Call[groveshop.CreateOrderRequest, groveshop.Order](ctx, client, groveshop.ServiceOrders, groveshop.MethodCreateOrder, groveshop.CreateOrderRequest{
		OrderID: "order-restarted", SKU: "coffee-beans", Quantity: 1, AmountCents: 1200, ShippingAddress: "16 Grove Lane",
	})
	if err != nil {
		t.Fatalf("order after restart: %v\n%s", err, clusterLogs(cluster.nodes))
	}
	if created.Status != groveshop.OrderCompleted {
		t.Errorf("order after restart status = %q; want %q", created.Status, groveshop.OrderCompleted)
	}
	for i := len(cluster.nodes) - 1; i >= 0; i-- {
		if err := cluster.nodes[i].Stop(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func waitForGrovletComponentState(
	ctx context.Context,
	transport *systemnats.Transport,
	nodeID string,
	serviceID grove.ServiceID,
	want systemnats.ComponentState,
) (systemnats.ComponentStatus, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var lastView systemnats.ComponentView
	var lastErr error
	for {
		requestCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		view, err := transport.RequestComponents(requestCtx, nodeID)
		cancel()
		if err == nil {
			lastView = view
			for _, component := range view.Components {
				if component.ServiceID == serviceID && component.State == want {
					return component, nil
				}
			}
		} else {
			lastErr = err
		}
		if err := ctx.Err(); err != nil {
			return systemnats.ComponentStatus{}, fmt.Errorf("component did not reach %s: view=%#v: %w", want, lastView, errors.Join(lastErr, err))
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return systemnats.ComponentStatus{}, fmt.Errorf("component did not reach %s: view=%#v: %w", want, lastView, errors.Join(lastErr, ctx.Err()))
		}
	}
}

// Orders and Inventory keep one Grove call path when placed in separate real
// Grovlet processes.
func TestGrovletCrossNodeServiceInvocation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	const (
		ordersSubject    = "_GROVE.system.invoke.orders-node"
		inventorySubject = "_GROVE.system.invoke.inventory-node"
	)

	ordersNode, err := grovetest.StartNode(
		grovletPath,
		"--node-id", "orders-node",
		"--advertise-endpoint", "nats-subject://system/orders-node",
		"--system-nats-listen", "127.0.0.1:0",
		"--system-nats-subject", ordersSubject,
		"--grove-shop-orders-inventory-subject", inventorySubject,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ordersNode.Cleanup(); err != nil {
			t.Errorf("cleanup Orders node: %v", err)
		}
	})
	if err := ordersNode.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	ordersReady := readyEventFromLogs(t, ordersNode.Logs())
	serverURL := ordersReady.SystemNATSURL
	if ordersReady.NodeID != "orders-node" || ordersReady.AdvertisedEndpoint != "nats-subject://system/orders-node" {
		t.Errorf("Orders readiness identity = (%q, %q); want orders-node endpoint", ordersReady.NodeID, ordersReady.AdvertisedEndpoint)
	}

	inventoryNode, err := grovetest.StartNode(
		grovletPath,
		"--node-id", "inventory-node",
		"--advertise-endpoint", "nats-subject://system/inventory-node",
		"--system-nats-url", serverURL,
		"--system-nats-subject", inventorySubject,
		"--grove-shop-inventory",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := inventoryNode.Cleanup(); err != nil {
			t.Errorf("cleanup Inventory node: %v", err)
		}
	})
	if err := inventoryNode.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	inventoryReady := readyEventFromLogs(t, inventoryNode.Logs())
	if inventoryReady.NodeID != "inventory-node" || inventoryReady.AdvertisedEndpoint != "nats-subject://system/inventory-node" {
		t.Errorf("Inventory readiness identity = (%q, %q); want inventory-node endpoint", inventoryReady.NodeID, inventoryReady.AdvertisedEndpoint)
	}
	if inventoryReady.NodeID == ordersReady.NodeID || inventoryReady.AdvertisedEndpoint == ordersReady.AdvertisedEndpoint {
		t.Error("same-host Grovlets did not retain distinct identities and endpoints")
	}

	transport, err := systemnats.Connect(ctx, serverURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.Close)
	client, err := transport.RoutedClient(ordersSubject)
	if err != nil {
		t.Fatal(err)
	}
	created, err := grove.Call[groveshop.CreateOrderRequest, groveshop.Order](
		ctx,
		client,
		groveshop.ServiceOrders,
		groveshop.MethodCreateOrder,
		groveshop.CreateOrderRequest{
			OrderID:         "order-cross-node",
			SKU:             "coffee-beans",
			Quantity:        2,
			AmountCents:     2400,
			ShippingAddress: "12 Grove Lane",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != groveshop.OrderCompleted {
		t.Errorf("cross-node order status = %q; want %q", created.Status, groveshop.OrderCompleted)
	}
	if created.Reservation.ID != "reservation-order-cross-node" {
		t.Errorf("cross-node reservation ID = %q; want reservation-order-cross-node", created.Reservation.ID)
	}

	_, err = grove.Call[groveshop.CreateOrderRequest, groveshop.Order](
		ctx,
		client,
		groveshop.ServiceOrders,
		groveshop.MethodCreateOrder,
		groveshop.CreateOrderRequest{OrderID: "invalid-cross-node", SKU: "coffee-beans"},
	)
	var responseErr *grove.ResponseError
	if !errors.As(err, &responseErr) || responseErr.Code != grove.ErrorHandler {
		t.Errorf("cross-node handler error = %v; want handler ResponseError", err)
	}

	if err := inventoryNode.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ordersNode.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

func readyEventFromLogs(t *testing.T, logs string) lifecycleEvent {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(logs))
	for {
		var event lifecycleEvent
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatal(err)
		}
		if event.Event == "ready" {
			return event
		}
	}
	t.Fatalf("Grovlet logs do not contain a ready event: %q", logs)
	return lifecycleEvent{}
}

func TestMain(m *testing.M) {
	buildDir, err := os.MkdirTemp("", "grovlet-command-build-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	path, buildErr := grovetest.BuildGrovlet(ctx, buildDir)
	cancel()
	if buildErr != nil {
		fmt.Fprintln(os.Stderr, buildErr)
		_ = os.RemoveAll(buildDir)
		os.Exit(1)
	}
	grovletPath = path

	code := m.Run()
	if err := os.RemoveAll(buildDir); err != nil && code == 0 {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}
