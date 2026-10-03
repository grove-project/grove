package controlplane

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/grove-project/grove/internal/placement"
)

var (
	catalog = placement.Handler{Service: 1, Method: 1}
	billing = placement.Handler{Service: 2, Method: 1}
)

func node(id string, registrations ...HandlerRegistration) NodeHandlers {
	return NodeHandlers{NodeID: id, InvocationSubject: "grove." + id, Handlers: registrations}
}

func automatic(h placement.Handler) HandlerRegistration {
	return HandlerRegistration{Service: h.Service, Method: h.Method}
}

func exclusive(h placement.Handler, capability string) HandlerRegistration {
	return HandlerRegistration{Service: h.Service, Method: h.Method, Exclusive: true, Capability: capability}
}

func nodesOf(records ...NodeHandlers) map[string]NodeHandlers {
	nodes := make(map[string]NodeHandlers, len(records))
	for _, r := range records {
		nodes[r.NodeID] = r
	}
	return nodes
}

// applyPlan stores a plan's writes and deletes, as the adapter's
// compare-and-set writes would when they win.
func applyPlan(stored map[placement.Handler]HandlerPlacement, plan HandlerPlacementPlan) map[placement.Handler]HandlerPlacement {
	next := make(map[placement.Handler]HandlerPlacement, len(stored))
	for id, p := range stored {
		next[id] = p
	}
	for _, w := range plan.Writes {
		next[w.Placement.Handler()] = w.Placement
	}
	for _, id := range plan.Deletes {
		delete(next, id)
	}
	return next
}

func TestValidateRegistrations(t *testing.T) {
	if err := ValidateRegistrations([]HandlerRegistration{automatic(catalog), exclusive(billing, "billing/ledger=1")}); err != nil {
		t.Fatalf("valid registrations: %v", err)
	}
	for name, regs := range map[string][]HandlerRegistration{
		"zero service":                {{Method: 1}},
		"handler twice":               {automatic(catalog), automatic(catalog)},
		"exclusive no capability":     {exclusive(billing, "")},
		"exclusive bad capability":    {exclusive(billing, "billing ledger")},
		"exclusive dotted capability": {exclusive(billing, "billing.ledger")},
	} {
		if err := ValidateRegistrations(regs); !errors.Is(err, ErrHandlerPlacementInvalid) {
			t.Errorf("%s: err = %v; want ErrHandlerPlacementInvalid", name, err)
		}
	}
}

func TestPlanHandlerPlacementsAutomaticFollowsLiveNodes(t *testing.T) {
	nodes := nodesOf(
		node("n2", automatic(catalog)),
		node("n1", automatic(catalog)),
		node("n3", HandlerRegistration{Service: 1, Method: 1, InvocationSubject: "grove.n3.catalog"}),
	)
	plan := PlanHandlerPlacements(nodes, nil, []string{"n1", "n2", "n3"}, nil)
	if len(plan.Writes) != 1 || !plan.Writes[0].Create || len(plan.Deletes) != 0 {
		t.Fatalf("plan = %+v; want one created placement", plan)
	}
	got := plan.Writes[0].Placement
	if !slices.Equal(got.NodeIDs(), []string{"n1", "n2", "n3"}) || got.Exclusive || got.Epoch != 0 {
		t.Errorf("placement = %+v; want every live node, unfenced", got)
	}
	if got.Nodes[0].InvocationSubject != "grove.n1" || got.Nodes[2].InvocationSubject != "grove.n3.catalog" {
		t.Errorf("subjects = %+v; want the node subject unless the handler overrides it", got.Nodes)
	}

	// Converged state plans nothing.
	stored := applyPlan(nil, plan)
	if again := PlanHandlerPlacements(nodes, stored, []string{"n1", "n2", "n3"}, nil); len(again.Writes) != 0 || len(again.Deletes) != 0 {
		t.Errorf("converged plan = %+v; want no changes", again)
	}

	// A node that stops being live leaves the placement.
	shrunk := PlanHandlerPlacements(nodes, stored, []string{"n1", "n3"}, nil)
	if len(shrunk.Writes) != 1 || shrunk.Writes[0].Create || !slices.Equal(shrunk.Writes[0].Placement.NodeIDs(), []string{"n1", "n3"}) {
		t.Errorf("plan after n2 failed = %+v; want n1 and n3 updated in place", shrunk)
	}

	// No live node registers the handler: its placement is deleted.
	if gone := PlanHandlerPlacements(nodes, stored, nil, nil); len(gone.Writes) != 0 || !slices.Equal(gone.Deletes, []placement.Handler{catalog}) {
		t.Errorf("plan with no live nodes = %+v; want the placement deleted", gone)
	}
}

func TestPlanHandlerPlacementsFencesExclusiveOwner(t *testing.T) {
	nodes := nodesOf(
		node("n1", automatic(catalog), exclusive(billing, "ledger")),
		node("n2", exclusive(billing, "ledger")),
	)
	live := []string{"n1", "n2"}
	first := PlanHandlerPlacements(nodes, nil, live, nil)
	stored := applyPlan(nil, first)
	owner := stored[billing]
	if !slices.Equal(owner.NodeIDs(), []string{"n1"}) || !owner.Exclusive || owner.Capability != "ledger" || owner.Epoch != 1 {
		t.Fatalf("exclusive placement = %+v; want n1 alone at epoch 1", owner)
	}

	// The owner stays put while it is eligible, even when a lower ID joins.
	nodes["n0"] = node("n0", exclusive(billing, "ledger"))
	if plan := PlanHandlerPlacements(nodes, stored, []string{"n0", "n1", "n2"}, map[string]uint64{"ledger": 1}); len(plan.Writes) != 0 {
		t.Errorf("plan after n0 joined = %+v; want the owner kept", plan)
	}

	// The owner fails: ownership moves and the epoch grows past both the
	// stored placement and every epoch ever claimed on the capability.
	moved := PlanHandlerPlacements(nodes, stored, []string{"n0", "n2"}, map[string]uint64{"ledger": 7})
	if len(moved.Writes) != 1 {
		t.Fatalf("plan after owner failure = %+v; want one write", moved)
	}
	next := moved.Writes[0].Placement
	if !slices.Equal(next.NodeIDs(), []string{"n0"}) || next.Epoch != 8 {
		t.Errorf("new owner = %+v; want n0 at epoch 8", next)
	}

	// A deleted and recreated placement still never reuses an epoch.
	recreated := PlanHandlerPlacements(nodes, nil, []string{"n2"}, map[string]uint64{"ledger": 8})
	if got := recreated.Writes[0].Placement.Epoch; got != 9 {
		t.Errorf("recreated placement epoch = %d; want 9", got)
	}
}

func TestResolveAndLookupLiveHandlerPlacements(t *testing.T) {
	view := HandlerPlacementView{Ready: true, Placements: []HandlerPlacement{
		{Service: 1, Method: 1, Nodes: []HandlerNode{{NodeID: "n1"}, {NodeID: "n2"}}},
		{Service: 2, Method: 1, Exclusive: true, Epoch: 3, Nodes: []HandlerNode{{NodeID: "n2"}}},
	}}
	resolved := ResolveHandlerPlacements(view, []string{"n1"})
	if len(resolved.Placements) != 1 || !slices.Equal(resolved.Placements[0].NodeIDs(), []string{"n1"}) {
		t.Errorf("resolved placements = %+v; want catalog on n1 only", resolved.Placements)
	}
	if want := []LostPlacement{{Service: 1, Method: 1, NodeID: "n2"}, {Service: 2, Method: 1, NodeID: "n2"}}; !slices.Equal(resolved.Lost, want) {
		t.Errorf("lost = %+v; want %+v", resolved.Lost, want)
	}
	if len(view.Placements[0].Nodes) != 2 {
		t.Error("ResolveHandlerPlacements changed its input")
	}

	got, err := LiveHandlerPlacement(view, []string{"n2"}, 1, 1)
	if err != nil || !slices.Equal(got.NodeIDs(), []string{"n2"}) {
		t.Errorf("LiveHandlerPlacement = (%+v, %v); want catalog on n2", got, err)
	}
	if _, err := LiveHandlerPlacement(view, []string{"n1"}, 2, 1); !errors.Is(err, ErrHandlerNotPlaced) {
		t.Errorf("exclusive owner not live: err = %v", err)
	}
	if _, err := LiveHandlerPlacement(view, []string{"n1"}, 9, 9); !errors.Is(err, ErrHandlerNotPlaced) {
		t.Errorf("unknown handler: err = %v", err)
	}
}

func TestDecideClaim(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	ttl := 3 * time.Second
	placements := []HandlerPlacement{{Service: 2, Method: 1, Exclusive: true, Capability: "ledger", Epoch: 4, Nodes: []HandlerNode{{NodeID: "n1"}}}}
	request := func(mutate func(*ClaimRequest)) ClaimRequest {
		r := ClaimRequest{NodeID: "n1", Capability: "ledger", Placements: placements, Now: now, TTL: ttl}
		if mutate != nil {
			mutate(&r)
		}
		return r
	}
	other := ObservedLease{Lease: CapabilityLease{Holder: "n2", Epoch: 3, Beat: 9}, FirstSeen: now.Add(-time.Second)}

	cases := []struct {
		name string
		req  ClaimRequest
		want ClaimDecision
	}{
		{"first claim", request(nil), ClaimWrite},
		{"not the owner", request(func(r *ClaimRequest) { r.NodeID = "n2" }), ClaimDenied},
		{"unknown capability", request(func(r *ClaimRequest) { r.Capability = "other" }), ClaimDenied},
		{"already held", request(func(r *ClaimRequest) { r.Held = &HeldLease{Capability: "ledger", Epoch: 4} }), ClaimHeld},
		{"held at an old epoch", request(func(r *ClaimRequest) { r.Held = &HeldLease{Capability: "ledger", Epoch: 3} }), ClaimWrite},
		{"held but released", request(func(r *ClaimRequest) { r.Held = &HeldLease{Capability: "ledger", Epoch: 4, Released: true} }), ClaimWrite},
		{"previous holder within ttl", request(func(r *ClaimRequest) { r.Observed, r.Seen = other, true }), ClaimWait},
		{"previous holder silent past ttl", request(func(r *ClaimRequest) {
			r.Observed, r.Seen = other, true
			r.Observed.FirstSeen = now.Add(-ttl)
		}), ClaimWrite},
		{"previous holder released", request(func(r *ClaimRequest) {
			r.Observed, r.Seen = other, true
			r.Observed.Lease.Released = true
		}), ClaimWrite},
		{"own stale lease", request(func(r *ClaimRequest) {
			r.Observed, r.Seen = other, true
			r.Observed.Lease.Holder = "n1"
		}), ClaimWrite},
	}
	for _, tc := range cases {
		claim := DecideClaim(tc.req)
		if claim.Decision != tc.want {
			t.Errorf("%s: decision = %v; want %v", tc.name, claim.Decision, tc.want)
		}
		if claim.Decision == ClaimWrite && claim.Lease != (CapabilityLease{Holder: "n1", Epoch: 4, Beat: 1}) {
			t.Errorf("%s: lease = %+v; want n1 at the placement's epoch, first beat", tc.name, claim.Lease)
		}
	}
}

func TestLeaseHolds(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	ttl := 3 * time.Second
	placements := []HandlerPlacement{{Service: 2, Method: 1, Exclusive: true, Capability: "ledger", Epoch: 4, Nodes: []HandlerNode{{NodeID: "n1"}}}}
	held := HeldLease{Capability: "ledger", Handler: billing, Epoch: 4, Beat: 2, RenewedAt: now.Add(-time.Second)}

	if !LeaseHolds(held, placements, "n1", now, ttl) {
		t.Error("fresh lease of the placed owner does not hold")
	}
	expired := held
	expired.RenewedAt = now.Add(-ttl)
	if LeaseHolds(expired, placements, "n1", now, ttl) {
		t.Error("lease holds after its ttl without renewal")
	}
	stale := held
	stale.Epoch = 3
	if LeaseHolds(stale, placements, "n1", now, ttl) {
		t.Error("lease from an older epoch holds")
	}
	for name, mutate := range map[string]func(*HeldLease){
		"lost":     func(l *HeldLease) { l.Lost = true },
		"released": func(l *HeldLease) { l.Released = true },
	} {
		l := held
		mutate(&l)
		if LeaseHolds(l, placements, "n1", now, ttl) {
			t.Errorf("%s lease holds", name)
		}
	}
	if LeaseHolds(held, placements, "n2", now, ttl) || OwnsPlacement(placements, billing, 4, "n2") {
		t.Error("a node that is not the placed owner holds the lease")
	}
	if OwnsPlacement(nil, billing, 4, "n1") {
		t.Error("lease holds with no placement")
	}
	if got := held.Renewal("n1"); got != (CapabilityLease{Holder: "n1", Epoch: 4, Beat: 3}) {
		t.Errorf("Renewal = %+v; want the next beat at the same epoch", got)
	}
}
