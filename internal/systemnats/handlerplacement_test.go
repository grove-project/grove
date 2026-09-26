package systemnats_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
)

const (
	chargeService, chargeMethod       = grove.ServiceID(2), grove.MethodID(2)
	reconcileService, reconcileMethod = grove.ServiceID(2), grove.MethodID(3)
	reconcileCapability               = "payment/reconcile"
)

// liveNodes is the test's failure-detection input to placement.
type liveNodes struct {
	mu  sync.Mutex
	ids map[string]bool
}

func (l *liveNodes) set(id string, live bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ids == nil {
		l.ids = make(map[string]bool)
	}
	l.ids[id] = live
}

func (l *liveNodes) get() ([]string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for id, live := range l.ids {
		if live {
			out = append(out, id)
		}
	}
	return out, true
}

type handlerNode struct {
	id         string
	placements *systemnats.HandlerPlacements
	client     *grove.Client
	stop       context.CancelFunc
	stopped    chan struct{}
	ownership  chan *grove.Ownership
}

func startHandlerNode(t *testing.T, ctx context.Context, id string, transport *systemnats.Transport, live *liveNodes) *handlerNode {
	t.Helper()
	subject := "_GROVE.system.invoke." + id
	placements, err := systemnats.NewHandlerPlacements(systemnats.HandlerPlacementConfig{
		NodeID:            id,
		InvocationSubject: subject,
		Handlers: []systemnats.HandlerRegistration{
			{Service: chargeService, Method: chargeMethod},
			{Service: reconcileService, Method: reconcileMethod, Exclusive: true, Capability: reconcileCapability},
		},
		Live:           live.get,
		LeaseTTL:       600 * time.Millisecond,
		ReconcileEvery: 40 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	node := &handlerNode{id: id, placements: placements, stopped: make(chan struct{}), ownership: make(chan *grove.Ownership, 16)}
	registry := &grove.Registry{}
	whoAmI := func(context.Context, []byte) ([]byte, error) { return grove.Encode(id) }
	if err := registry.Register(chargeService, chargeMethod, whoAmI); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(reconcileService, reconcileMethod, func(ctx context.Context, _ []byte) ([]byte, error) {
		ownership := grove.Exclusive(ctx, reconcileCapability)
		if !ownership.Enabled() {
			return nil, errors.New("not the owner")
		}
		node.ownership <- ownership
		return grove.Encode(id)
	}); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := grove.NewDispatcher(registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := transport.Serve(ctx, subject, placements.Dispatch(dispatcher)); err != nil {
		t.Fatal(err)
	}
	if node.client, err = transport.HandlerPlacementClient(placements); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(t.Context())
	node.stop = cancel
	go func() {
		defer close(node.stopped)
		_ = placements.Run(runCtx, transport)
	}()
	t.Cleanup(func() { cancel(); <-node.stopped })
	live.set(id, true)
	return node
}

func eventually(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func placedOn(n *handlerNode, service grove.ServiceID, method grove.MethodID) []string {
	for _, p := range n.placements.Snapshot().Placements {
		if p.Service == service && p.Method == method {
			return nodeIDs(p)
		}
	}
	return nil
}

func nodeIDs(p systemnats.HandlerPlacement) []string {
	ids := make([]string, len(p.Nodes))
	for i, n := range p.Nodes {
		ids[i] = n.NodeID
	}
	return ids
}

func callID(ctx context.Context, from *handlerNode, service grove.ServiceID, method grove.MethodID) (string, error) {
	return grove.Call[struct{}, string](ctx, from.client, service, method, struct{}{})
}

// Three nodes on a real replicated System NATS cluster reconcile handler
// placement with compare-and-swap, balance calls, keep one exclusive owner,
// and fence a stale owner before its successor acts.
func TestHandlerPlacementOnRealCluster(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	_, transports := startPlacementCluster(t, ctx)
	live := &liveNodes{}

	n1 := startHandlerNode(t, ctx, "node-1", transports[0], live)
	eventually(t, "single placement", func() bool { return len(placedOn(n1, chargeService, chargeMethod)) == 1 })

	n2 := startHandlerNode(t, ctx, "node-2", transports[1], live)
	n3 := startHandlerNode(t, ctx, "node-3", transports[2], live)
	nodes := []*handlerNode{n1, n2, n3}
	for _, n := range nodes {
		eventually(t, n.id+" sees 3 Charge placements", func() bool { return len(placedOn(n, chargeService, chargeMethod)) == 3 })
		eventually(t, n.id+" sees one Reconcile owner", func() bool { return len(placedOn(n, reconcileService, reconcileMethod)) == 1 })
	}
	if owner := placedOn(n1, reconcileService, reconcileMethod)[0]; owner != "node-1" {
		t.Fatalf("reconcile owner = %s, want node-1", owner)
	}

	served := map[string]int{}
	for range 9 {
		id, err := callID(ctx, n2, chargeService, chargeMethod)
		if err != nil {
			t.Fatal(err)
		}
		served[id]++
	}
	for _, n := range nodes {
		if served[n.id] != 3 {
			t.Fatalf("Charge distribution = %v, want 3 each", served)
		}
	}

	if id, err := callID(ctx, n3, reconcileService, reconcileMethod); err != nil || id != "node-1" {
		t.Fatalf("Reconcile served by %q err=%v, want node-1", id, err)
	}
	stale := <-n1.ownership
	if !stale.Enabled() {
		t.Fatal("owner is not enabled")
	}

	// Failure detection drops node-1 while its process keeps running.
	live.set("node-1", false)
	eventually(t, "node-1 leaves routing", func() bool { return len(placedOn(n2, chargeService, chargeMethod)) == 2 })
	for range 6 {
		if id, err := callID(ctx, n2, chargeService, chargeMethod); err != nil || id == "node-1" {
			t.Fatalf("Charge served by %q err=%v after node-1 was removed", id, err)
		}
	}
	eventually(t, "stale owner fenced", func() bool { return !stale.Enabled() })

	// Views (TUI, status) and routing read the same resolved placements: what
	// Lookup would route to is exactly what Resolved reports as placed.
	for _, service := range []struct {
		method grove.MethodID
		want   int
	}{{chargeMethod, 2}, {reconcileMethod, 1}} {
		eventually(t, "resolved view and routing agree", func() bool {
			found, err := n2.placements.Lookup(chargeService, service.method)
			if err != nil {
				return service.method == reconcileMethod // owner not yet reassigned
			}
			for _, p := range n2.placements.Resolved().Placements {
				if p.Service == chargeService && p.Method == service.method {
					return len(p.Nodes) == service.want && equalNodes(nodeIDs(p), nodeIDs(found))
				}
			}
			return false
		})
	}

	// The successor acts only after the old lease has run out, and there is
	// never a moment with two enabled owners.
	var next *grove.Ownership
	eventually(t, "ownership moves", func() bool {
		select {
		case o := <-n2.ownership:
			next = o
		default:
		}
		if next == nil {
			if _, err := callID(ctx, n3, reconcileService, reconcileMethod); err != nil {
				return false
			}
		}
		select {
		case o := <-n2.ownership:
			next = o
		case o := <-n3.ownership:
			next = o
		default:
		}
		return next != nil
	})
	if stale.Enabled() {
		t.Fatal("stale owner re-enabled after takeover")
	}
	if !next.Enabled() {
		t.Fatal("new owner is not enabled")
	}
	if owner := placedOn(n3, reconcileService, reconcileMethod); len(owner) != 1 || owner[0] == "node-1" {
		t.Fatalf("owner after failover = %v", owner)
	}
	for _, p := range n3.placements.Snapshot().Placements {
		if p.Service == reconcileService && p.Method == reconcileMethod && p.Epoch < 2 {
			t.Fatalf("epoch = %d after failover, want >= 2", p.Epoch)
		}
	}
}

// An owner that can no longer reach the control plane stops acting once its
// lease runs out, even though nothing told it ownership moved.
func TestHandlerOwnerFencesItselfWhenRenewalStops(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	_, transports := startPlacementCluster(t, ctx)
	live := &liveNodes{}
	n1 := startHandlerNode(t, ctx, "node-1", transports[0], live)
	eventually(t, "owner placed", func() bool { return len(placedOn(n1, reconcileService, reconcileMethod)) == 1 })
	var owned *grove.Ownership
	eventually(t, "ownership claimed", func() bool {
		if _, err := callID(ctx, n1, reconcileService, reconcileMethod); err != nil {
			return false
		}
		select {
		case owned = <-n1.ownership:
		default:
		}
		return owned != nil
	})
	n1.stop()
	<-n1.stopped
	eventually(t, "lease expiry without renewal", func() bool { return !owned.Enabled() })
}

func equalNodes(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
