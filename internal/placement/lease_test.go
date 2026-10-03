package placement

import (
	"testing"
	"time"
)

func TestDecideClaim(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	ttl := 3 * time.Second
	request := func(mutate func(*ClaimRequest)) ClaimRequest {
		r := ClaimRequest{NodeID: "n1", Owner: Ownership{Nodes: []string{"n1"}, Epoch: 4}, Placed: true, Now: now, TTL: ttl}
		if mutate != nil {
			mutate(&r)
		}
		return r
	}
	other := ObservedLease{Lease: Lease{Holder: "n2", Epoch: 3, Beat: 9}, FirstSeen: now.Add(-time.Second)}

	cases := []struct {
		name string
		req  ClaimRequest
		want ClaimDecision
	}{
		{"first claim", request(nil), ClaimWrite},
		{"not the owner", request(func(r *ClaimRequest) { r.NodeID = "n2" }), ClaimDenied},
		{"not placed", request(func(r *ClaimRequest) { r.Placed = false }), ClaimDenied},
		{"placed on several nodes", request(func(r *ClaimRequest) { r.Owner.Nodes = []string{"n1", "n2"} }), ClaimDenied},
		{"already held", request(func(r *ClaimRequest) { r.Held = &HeldLease{Epoch: 4} }), ClaimHeld},
		{"held at an old epoch", request(func(r *ClaimRequest) { r.Held = &HeldLease{Epoch: 3} }), ClaimWrite},
		{"held but lost", request(func(r *ClaimRequest) { r.Held = &HeldLease{Epoch: 4, Lost: true} }), ClaimWrite},
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
		if claim.Decision == ClaimWrite && claim.Lease != (Lease{Holder: "n1", Epoch: 4, Beat: 1}) {
			t.Errorf("%s: lease = %+v; want n1 at the placement's epoch, first beat", tc.name, claim.Lease)
		}
	}
	wait := request(func(r *ClaimRequest) { r.Observed, r.Seen = other, true })
	if until := ClaimWaitUntil(wait); !until.Equal(other.FirstSeen.Add(ttl)) {
		t.Errorf("ClaimWaitUntil = %v; want first seen + ttl", until)
	}
	wait.Now = ClaimWaitUntil(wait)
	if got := DecideClaim(wait).Decision; got != ClaimWrite {
		t.Errorf("claim at ClaimWaitUntil = %v; want ClaimWrite", got)
	}
}

func TestLeaseHolds(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	ttl := 3 * time.Second
	owner := Ownership{Nodes: []string{"n1"}, Epoch: 4}
	held := HeldLease{Capability: "ledger", Handler: reconcile, Epoch: 4, Beat: 2, RenewedAt: now.Add(-time.Second)}

	if !LeaseHolds(held, owner, "n1", now, ttl) {
		t.Error("fresh lease of the placed owner does not hold")
	}
	for name, mutate := range map[string]func(*HeldLease, *Ownership){
		"expired":         func(l *HeldLease, _ *Ownership) { l.RenewedAt = now.Add(-ttl) },
		"older epoch":     func(l *HeldLease, _ *Ownership) { l.Epoch = 3 },
		"lost":            func(l *HeldLease, _ *Ownership) { l.Lost = true },
		"released":        func(l *HeldLease, _ *Ownership) { l.Released = true },
		"placement moved": func(_ *HeldLease, o *Ownership) { o.Nodes = []string{"n2"}; o.Epoch = 5 },
	} {
		l, o := held, owner
		mutate(&l, &o)
		if LeaseHolds(l, o, "n1", now, ttl) {
			t.Errorf("%s lease holds", name)
		}
	}
	if LeaseHolds(held, owner, "n2", now, ttl) {
		t.Error("a node that is not the placed owner holds the lease")
	}
	if renewal := held.Renewal("n1"); renewal != (Lease{Holder: "n1", Epoch: 4, Beat: 3}) {
		t.Errorf("renewal = %+v; want the next beat at the same epoch", renewal)
	}
}
