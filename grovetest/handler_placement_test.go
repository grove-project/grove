package grovetest_test

import (
	"context"
	"testing"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/grovetest"
)

// PaymentService mixes scalable handlers with one exclusive handler.
var (
	paymentValidate  = grovetest.HandlerID{Service: 2, Method: 1}
	paymentCharge    = grovetest.HandlerID{Service: 2, Method: 2}
	paymentReconcile = grovetest.HandlerID{Service: 2, Method: 3}
)

const reconcileCapability = "payment/reconcile"

// registerPayment registers PaymentService on n. Reconcile claims its
// exclusive capability and stores the ownership so tests can observe it.
func registerPayment(n *grovetest.TestNode, owned *[]*grove.Ownership) {
	registerWhoAmI(n, paymentValidate)
	registerWhoAmI(n, paymentCharge)
	n.Register(paymentReconcile, func(ctx context.Context, _ []byte) ([]byte, error) {
		ownership := grove.Exclusive(ctx, reconcileCapability)
		*owned = append(*owned, ownership)
		return grove.Encode(n.ID())
	}, grovetest.ExclusiveCapability(reconcileCapability))
}

func TestHandlerLevelPlacementScalesAndFailsOver(t *testing.T) {
	cluster := grovetest.NewTestCluster(t)
	var owned []*grove.Ownership

	n1 := cluster.AddNode()
	registerPayment(n1, &owned)
	cluster.Start()
	// 1. One node: one placement per handler, no replica count anywhere.
	cluster.AssertPlacements(paymentCharge, n1)

	// 2-3. Capacity joins; ordinary handlers expand, the exclusive one does not.
	n2 := cluster.AddNode()
	registerPayment(n2, &owned)
	cluster.Converge()
	cluster.AssertPlacements(paymentCharge, n1, n2)
	n3 := cluster.AddNode()
	registerPayment(n3, &owned)
	cluster.Converge()
	cluster.AssertPlacements(paymentCharge, n1, n2, n3)
	cluster.AssertPlacements(paymentValidate, n1, n2, n3)

	// 4. Calls are spread over concrete placements.
	for range 9 {
		if _, err := whoAmI(t, n1, paymentCharge); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []*grovetest.TestNode{n1, n2, n3} {
		if got := cluster.Calls(paymentCharge)[n.ID()]; got != 3 {
			t.Fatalf("%s served %d Charge calls, want 3: %v", n.ID(), got, cluster.Calls(paymentCharge))
		}
	}

	// 5-6. Reconcile in the same service has exactly one owner on every view.
	cluster.AssertSingleOwner(paymentReconcile)
	owner := cluster.Owner(paymentReconcile)
	if owner != n1 {
		t.Fatalf("owner = %s, want stable first owner node-1", owner.ID())
	}
	for range 4 {
		got, err := whoAmI(t, n3, paymentReconcile)
		if err != nil || got != owner.ID() {
			t.Fatalf("Reconcile served by %q err=%v, want %s", got, err, owner.ID())
		}
	}
	if !owned[len(owned)-1].Enabled() {
		t.Fatal("current owner's ownership is not enabled")
	}
	firstEpoch := cluster.Epoch(paymentReconcile)
	held := owned[len(owned)-1]

	// 7-9. Kill the owner: scalable traffic continues, ownership moves, and the
	// dead owner's ownership is fenced.
	cluster.KillNode(owner)
	cluster.Converge()
	cluster.AssertPlacements(paymentCharge, n2, n3)
	for range 6 {
		got, err := whoAmI(t, n2, paymentCharge)
		if err != nil || got == owner.ID() {
			t.Fatalf("Charge served by %q err=%v after %s died", got, err, owner.ID())
		}
	}
	cluster.AssertSingleOwner(paymentReconcile)
	if next := cluster.Owner(paymentReconcile); next != n2 {
		t.Fatalf("new owner = %s, want node-2", next.ID())
	}
	if cluster.Epoch(paymentReconcile) <= firstEpoch {
		t.Fatal("ownership moved without advancing the fencing epoch")
	}
	if held.Enabled() {
		t.Fatal("stale owner still enabled after its node died")
	}
	cluster.RestartNode(owner)
	cluster.Converge()
	if held.Enabled() {
		t.Fatal("stale owner re-enabled after restart")
	}
	cluster.AssertSingleOwner(paymentReconcile)
	if got := cluster.Owner(paymentReconcile); got != n2 {
		t.Fatalf("owner after old owner rejoined = %s, want node-2 (no flapping)", got.ID())
	}
}

func TestIsolatedOwnerFencesItselfBeforeTakeover(t *testing.T) {
	cluster := grovetest.NewTestCluster(t)
	var owned []*grove.Ownership
	n1, n2 := cluster.AddNode(), cluster.AddNode()
	registerPayment(n1, &owned)
	registerPayment(n2, &owned)
	cluster.Start()

	if _, err := whoAmI(t, n2, paymentReconcile); err != nil {
		t.Fatal(err)
	}
	stale := owned[0]
	if !stale.Enabled() {
		t.Fatal("owner not enabled")
	}

	// The owner's process keeps running but cannot renew: it must stop acting
	// once its lease expires, which precedes any takeover.
	cluster.Isolate(n1)
	cluster.Clock().Advance(cluster.LeaseTTL())
	if stale.Enabled() {
		t.Fatal("isolated owner kept acting after lease expiry")
	}
	cluster.Converge()
	cluster.AssertSingleOwner(paymentReconcile)
	if cluster.Owner(paymentReconcile) != n2 {
		t.Fatal("ownership did not move to node-2")
	}
	cluster.Reconnect(n1)
	cluster.Converge()
	if stale.Enabled() {
		t.Fatal("stale owner re-enabled after reconnect")
	}
	cluster.AssertSingleOwner(paymentReconcile)
}

func TestExclusiveWithoutRuntimeOwnsLocally(t *testing.T) {
	o := grove.Exclusive(t.Context(), "anything")
	if !o.Enabled() {
		t.Fatal("unit-test ownership should be enabled")
	}
	o.Release()
	if o.Enabled() {
		t.Fatal("released ownership still enabled")
	}
}
