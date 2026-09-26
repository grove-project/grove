package main

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/grovetest"
	"github.com/grove-project/grove/internal/systemnats"
	groveshop "github.com/grove-project/grove/internal/testapp"
)

// Killing a node under load must not make handler placement unavailable on
// the survivors: control-plane (JetStream) elections must not stop routing.
// Calls to the dead node may fail until heartbeats mark it unavailable.
func TestHandlerPlacementSurvivesNodeKill(t *testing.T) {
	const victim = 1
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Second)
	defer cancel()
	binary, err := grovetest.BuildGrovlet(ctx, t.TempDir(), "./internal/testapp/cmd/handlerapp")
	if err != nil {
		t.Fatal(err)
	}
	nodeIDs := []string{"node-1", "node-2", "node-3"}
	ports := reserveRoutePorts(t, len(nodeIDs))
	nodes := make([]*grovetest.Node, len(nodeIDs))
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
			"--system-nats-membership", "--system-nats-recovery", "--system-nats-retire-on-stop",
			"--system-nats-subject", handlerNodeSubj + nodeID,
			"--component", "payment", "--component-option", "payment=" + nodeID,
		}
		node, err := grovetest.StartNode(binary, args...)
		if err != nil {
			t.Fatal(err)
		}
		nodes[i] = node
		t.Cleanup(func() { _ = node.Cleanup() })
	}
	for _, node := range nodes {
		if err := node.WaitReady(ctx); err != nil {
			t.Fatalf("wait: %v\n%s", err, grovletLogs(nodes))
		}
	}
	obs := (victim + 1) % 3
	transport, err := systemnats.Connect(ctx, readySystemNATSURL(t, nodes[obs].Logs()))
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	client, err := grove.NewRoutedClient(transport.ObservedHandlerRouter(nodeIDs[obs], func(s grove.ServiceID, m grove.MethodID) bool {
		return s == groveshop.ServicePayment && (m == whoAmIMethod || m == groveshop.MethodCharge)
	}, transport.ObservedPlacementRouter(nodeIDs[obs])))
	if err != nil {
		t.Fatal(err)
	}
	call := func() (string, error) {
		return grove.Call[struct{}, string](ctx, client, groveshop.ServicePayment, whoAmIMethod, struct{}{})
	}
	seen := map[string]bool{}
	for deadline := time.Now().Add(60 * time.Second); len(seen) < 3; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("not all nodes serving\n%s", grovletLogs(nodes))
		}
		if id, err := call(); err == nil {
			seen[id] = true
		}
	}
	loadCtx, stopLoad := context.WithCancel(ctx)
	defer stopLoad()
	for range 32 {
		go func() {
			for loadCtx.Err() == nil {
				_, _ = grove.Call[struct{}, string](loadCtx, client, groveshop.ServicePayment, whoAmIMethod, struct{}{})
			}
		}()
	}
	time.Sleep(3 * time.Second)
	if err := nodes[victim].Kill(ctx); err != nil {
		t.Fatal(err)
	}
	killedAt := time.Now()
	errs := map[string]int{}
	served := map[string]int{}
	var lastOK time.Time
	for time.Since(killedAt) < 12*time.Second {
		id, err := call()
		if err != nil {
			errs[err.Error()]++
			time.Sleep(20 * time.Millisecond)
			continue
		}
		served[id]++
		lastOK = time.Now()
		time.Sleep(5 * time.Millisecond)
	}
	t.Logf("served=%v errs=%v lastOK=%s ago", served, errs, time.Since(lastOK))
	for message, count := range errs {
		if strings.Contains(message, systemnats.ErrHandlerPlacementUnavailable.Error()) {
			t.Fatalf("handler placement became unavailable %d times\n%s", count, grovletLogs(nodes))
		}
	}
	if time.Since(lastOK) > 2*time.Second {
		t.Fatalf("no recovery\n%s", grovletLogs(nodes))
	}
}
