package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/grovetest"
	"github.com/grove-project/grove/internal/systemnats"
	groveshop "github.com/grove-project/grove/internal/testapp"
)

const (
	whoAmIMethod    = grove.MethodID(100)
	loadGenMethod   = grove.MethodID(101)
	handlerNodeSubj = "_GROVE.system.cli."
)

type loadGenStatus struct {
	Node   string
	Active bool
	Ticks  int64
}

// Real Grovlet processes on a replicated System NATS cluster place an ordinary
// handler on every node, keep exactly one owner of an exclusive workload, and
// move that workload after its node is killed. HTTP ingress sees the same
// healthy placements as internal calls throughout.
func TestHandlerLevelPlacementAcrossGrovlets(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	binary, err := grovetest.BuildGrovlet(ctx, t.TempDir(), "./internal/testapp/cmd/handlerapp")
	if err != nil {
		t.Fatal(err)
	}

	// Three nodes host Payment. A fourth hosts only the ingress and is never
	// killed, so which payment node owns the workload does not matter.
	nodeIDs := []string{"node-1", "node-2", "node-3"}
	const ingressNode = "node-4"
	clusterIDs := append(append([]string(nil), nodeIDs...), ingressNode)
	ports := reserveRoutePorts(t, len(clusterIDs)+1)
	ingressAddress := "127.0.0.1:" + strconv.Itoa(ports[len(clusterIDs)])
	nodes := make([]*grovetest.Node, len(clusterIDs))
	for i, nodeID := range clusterIDs {
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
			"--system-nats-subject", handlerNodeSubj + nodeID,
		}
		if nodeID == ingressNode {
			args = append(args, "--component", "web", "--component-listen", "web="+ingressAddress)
		} else {
			args = append(args, "--component", "payment", "--component-option", "payment="+nodeID)
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
			t.Fatalf("wait for Grovlets: %v\n%s", err, grovletLogs(nodes))
		}
	}
	transport, err := systemnats.Connect(ctx, readySystemNATSURL(t, nodes[len(nodes)-1].Logs()))
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	client, err := grove.NewRoutedClient(transport.ObservedHandlerRouter(ingressNode, func(service grove.ServiceID, method grove.MethodID) bool {
		return service == groveshop.ServicePayment && (method == groveshop.MethodCharge || method == whoAmIMethod || method == loadGenMethod)
	}, transport.ObservedPlacementRouter(ingressNode)))
	if err != nil {
		t.Fatal(err)
	}

	// One ordinary handler, three concrete placements, no replica count.
	seen := map[string]int{}
	eventually := func(what string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(60 * time.Second)
		for !condition() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s\n%s", what, grovletLogs(nodes))
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	eventually("whoami served by all three nodes", func() bool {
		id, err := grove.Call[struct{}, string](ctx, client, groveshop.ServicePayment, whoAmIMethod, struct{}{})
		if err == nil {
			seen[id]++
		}
		return len(seen) == 3
	})

	// Ingress resolves handlers through the same placements as internal RPC.
	ingress := func(path string) (string, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+ingressAddress+path, nil)
		if err != nil {
			return "", err
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return "", err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != http.StatusOK {
			return "", fmt.Errorf("ingress %s: status %d: %s: %v", path, response.StatusCode, body, err)
		}
		return string(body), nil
	}
	viaIngress := map[string]int{}
	eventually("ingress whoami served by all three nodes", func() bool {
		if node, err := ingress("/probe/whoami"); err == nil {
			viaIngress[node]++
		}
		return len(viaIngress) == 3
	})

	// Exactly one node runs the exclusive workload.
	status := func(node string) (loadGenStatus, error) {
		direct, err := transport.RoutedClient(handlerNodeSubj + node + ".service." + strconv.Itoa(int(groveshop.ServicePayment)))
		if err != nil {
			return loadGenStatus{}, err
		}
		return grove.Call[struct{}, loadGenStatus](ctx, direct, groveshop.ServicePayment, loadGenMethod, struct{}{})
	}
	activeNodes := func(among ...string) []string {
		var active []string
		for _, node := range among {
			if s, err := status(node); err == nil && s.Active {
				active = append(active, node)
			}
		}
		return active
	}
	var owner string
	eventually("one active load generator", func() bool {
		active := activeNodes(nodeIDs...)
		if len(active) > 1 {
			t.Fatalf("multiple active owners: %v", active)
		}
		if len(active) == 1 {
			owner = active[0]
		}
		return owner != ""
	})
	// The exclusive handler routes only to that owner.
	for range 4 {
		s, err := grove.Call[struct{}, loadGenStatus](ctx, client, groveshop.ServicePayment, loadGenMethod, struct{}{})
		if err != nil || s.Node != owner {
			t.Fatalf("exclusive call served by %q err=%v, want %s", s.Node, err, owner)
		}
	}

	if node, err := ingress("/probe/loadgen"); err != nil || node != owner {
		t.Fatalf("ingress exclusive call served by %q err=%v, want %s", node, err, owner)
	}

	// Kill the owner's Grovlet: its workers die with it. Ownership must move
	// to exactly one survivor, never overlapping.
	ownerIndex := int(owner[len(owner)-1] - '1')
	if err := nodes[ownerIndex].Kill(ctx); err != nil {
		t.Fatal(err)
	}
	var survivors []string
	for _, id := range nodeIDs {
		if id != owner {
			survivors = append(survivors, id)
		}
	}
	var successor string
	eventually("ownership moves to a survivor", func() bool {
		active := activeNodes(survivors...)
		if len(active) > 1 {
			t.Fatalf("multiple active owners after failover: %v", active)
		}
		if len(active) == 1 {
			successor = active[0]
		}
		return successor != ""
	})
	eventually("dead node leaves routing", func() bool {
		served := map[string]bool{}
		for range 6 {
			id, err := grove.Call[struct{}, string](ctx, client, groveshop.ServicePayment, whoAmIMethod, struct{}{})
			if err != nil || id == owner {
				return false
			}
			served[id] = true
		}
		return len(served) == 2
	})
	var lastIngress string
	eventually("ingress stops routing to the dead node ("+"see last: "+lastIngress+")", func() bool {
		served := map[string]bool{}
		for range 6 {
			node, err := ingress("/probe/whoami")
			if err != nil || node == owner {
				lastIngress = fmt.Sprintf("node=%q err=%v", node, err)
				t.Log(lastIngress)
				return false
			}
			served[node] = true
		}
		return len(served) == 2
	})
	// Until the observing node has caught up with the new owner, ingress refuses
	// the exclusive call; it must never run it on anyone but the owner.
	eventually("ingress exclusive call reaches the new owner", func() bool {
		node, err := ingress("/probe/loadgen")
		if err != nil {
			return false
		}
		if node != successor {
			t.Fatalf("ingress served the exclusive handler on %q; owner is %s", node, successor)
		}
		return true
	})
	if s, err := grove.Call[struct{}, loadGenStatus](ctx, client, groveshop.ServicePayment, loadGenMethod, struct{}{}); err != nil || s.Node != successor {
		t.Fatalf("exclusive call after failover served by %q err=%v, want %s", s.Node, err, successor)
	}
	if successor == owner {
		t.Fatalf("ownership did not move from %s", owner)
	}
}
