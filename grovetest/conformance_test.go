package grovetest

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/controlplane"
	"github.com/grove-project/grove/internal/placement"
)

// TestTestClusterMatchesProductionPlacement replays one scenario through the
// TestCluster and, step by step, through production reconciliation
// (controlplane.PlanHandlerPlacements over the same live registrations and
// claimed lease epochs). The handler placements and fencing epochs must agree
// after every step, so simulation cannot drift from production semantics.
func TestTestClusterMatchesProductionPlacement(t *testing.T) {
	const capability = "payment/reconcile"
	validate := HandlerID{Service: 2, Method: 1}
	reconcile := HandlerID{Service: 2, Method: 3}
	noop := func(context.Context, []byte) ([]byte, error) { return nil, nil }

	cluster := NewTestCluster(t)
	n1, n2, n3 := cluster.AddNode(), cluster.AddNode(), cluster.AddNode()
	for _, n := range []*TestNode{n1, n2, n3} {
		n.Register(validate, noop)
	}
	for _, n := range []*TestNode{n1, n3} { // node-2 cannot run Reconcile
		n.Register(reconcile, noop, ExclusiveCapability(capability))
	}
	production := make(map[placement.Handler]controlplane.HandlerPlacement)
	claim := func(n *TestNode) grove.Lease {
		t.Helper()
		lease, err := n.AcquireExclusive(t.Context(), capability)
		if err != nil {
			t.Fatalf("%s claim: %v", n.ID(), err)
		}
		return lease
	}

	steps := []struct {
		name string
		act  func()
	}{
		{"start", cluster.Start},
		{"owner claims", func() {
			if !claim(n1).Held() || claim(n3).Held() {
				t.Fatal("only the placed owner node-1 may hold the lease")
			}
		}},
		{"owner crashes", func() { cluster.KillNode(n1); cluster.Converge() }},
		{"successor claims", func() {
			if !claim(n3).Held() {
				t.Fatal("node-3 cannot claim after node-1's lease expired")
			}
		}},
		{"old owner restarts", func() { cluster.RestartNode(n1); cluster.Converge() }},
		{"owner isolated", func() { cluster.Isolate(n3); cluster.Converge() }},
		{"owner reconnects", func() { cluster.Reconnect(n3); cluster.Converge() }},
		{"node-1 claims after takeover", func() {
			if !claim(n1).Held() {
				t.Fatal("node-1 cannot claim the capability it now owns")
			}
		}},
		{"owner leaves unclaimed to node-3", func() { cluster.StopNode(n1); cluster.Converge() }},
		{"every capable node leaves", func() { cluster.StopNode(n3); cluster.Converge() }},
		{"capable node returns", func() { cluster.RestartNode(n3); cluster.Converge() }},
	}
	for _, step := range steps {
		step.act()
		cluster.mu.Lock()
		reconcileProduction(cluster, production)
		got := make(map[HandlerID][]string)
		epochs := make(map[HandlerID]uint64)
		for id, p := range production {
			got[id] = p.NodeIDs()
			if p.Exclusive {
				epochs[id] = p.Epoch
			}
		}
		simEpochs := make(map[HandlerID]uint64)
		for id := range cluster.store {
			if cluster.exclusiveLocked(id) {
				simEpochs[id] = cluster.epochs[id]
			}
		}
		simPlaced, diagnostics := clonePlacements(cluster.store), cluster.diagnosticsLocked()
		cluster.mu.Unlock()
		if !reflect.DeepEqual(got, simPlaced) || !reflect.DeepEqual(epochs, simEpochs) {
			t.Fatalf("%s: production placed %v epochs %v; TestCluster placed %v epochs %v\n%s",
				step.name, got, epochs, simPlaced, simEpochs, diagnostics)
		}
	}
	if epoch := cluster.Epoch(reconcile); epoch <= 3 {
		t.Fatalf("recreated placement epoch = %d; want above every epoch claimed before", epoch)
	}
}

// reconcileProduction applies production reconciliation to placements until
// it is stable, from the TestCluster's live nodes and claimed leases.
func reconcileProduction(c *TestCluster, placements map[placement.Handler]controlplane.HandlerPlacement) {
	nodes := make(map[string]controlplane.NodeHandlers)
	var live []string
	for _, n := range c.nodes {
		if n.state != NodeRunning || n.isolated {
			continue
		}
		live = append(live, n.id)
		record := controlplane.NodeHandlers{NodeID: n.id}
		for _, r := range n.regs {
			record.Handlers = append(record.Handlers, controlplane.HandlerRegistration{
				Service: r.id.Service, Method: r.id.Method, Exclusive: r.info.Exclusive, Capability: capabilityName(r),
			})
		}
		nodes[n.id] = record
	}
	sort.Strings(live)
	leaseEpochs := make(map[string]uint64)
	for capability, l := range c.leases {
		leaseEpochs[capability] = l.Lease.Epoch
	}
	for range maxConvergeRounds {
		plan := controlplane.PlanHandlerPlacements(nodes, placements, live, leaseEpochs)
		if len(plan.Writes) == 0 && len(plan.Deletes) == 0 {
			return
		}
		for _, write := range plan.Writes {
			placements[write.Placement.Handler()] = write.Placement
		}
		for _, id := range plan.Deletes {
			delete(placements, id)
		}
	}
}
