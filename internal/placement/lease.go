package placement

import "time"

// Lease is the stored claim on an exclusive capability. Beat changes on every
// renewal so observers can tell a live owner from a silent one.
type Lease struct {
	Holder   string `json:"holder"`
	Epoch    uint64 `json:"epoch"`
	Beat     uint64 `json:"beat"`
	Released bool   `json:"released,omitempty"`
}

// ObservedLease is a stored lease as one observer saw it. FirstSeen is when
// this observer first saw the lease's current version, on its own clock, so
// staleness never depends on clocks agreeing across nodes.
type ObservedLease struct {
	Lease     Lease
	FirstSeen time.Time
}

// HeldLease is a node's own claim on a capability.
type HeldLease struct {
	Capability string
	Handler    Handler
	Epoch      uint64
	Beat       uint64
	// RenewedAt is when the latest successful claim or renewal was sent.
	RenewedAt time.Time
	// Lost is set once another holder took the capability or placement moved.
	Lost bool
	// Released is set when the workload gave the lease up.
	Released bool
}

// Active reports whether the lease is neither lost nor released.
func (l HeldLease) Active() bool { return !l.Lost && !l.Released }

// Renewal is the stored lease that extends held by one beat.
func (l HeldLease) Renewal(nodeID string) Lease {
	return Lease{Holder: nodeID, Epoch: l.Epoch, Beat: l.Beat + 1}
}

// Ownership is the placement of one exclusive handler as a lease decision
// sees it: the nodes it is placed on and its fencing epoch.
type Ownership struct {
	Nodes []string
	Epoch uint64
}

// OwnedBy reports whether nodeID is the single owner at epoch. A holder whose
// placement moved on, or whose epoch is older, must stop acting.
func (o Ownership) OwnedBy(nodeID string, epoch uint64) bool {
	return o.Epoch == epoch && len(o.Nodes) == 1 && o.Nodes[0] == nodeID
}

// LeaseHolds reports whether a held lease may still act: active, renewed
// within ttl of now, and still the single owner at the same epoch.
func LeaseHolds(lease HeldLease, owner Ownership, nodeID string, now time.Time, ttl time.Duration) bool {
	if !lease.Active() || now.Sub(lease.RenewedAt) >= ttl {
		return false
	}
	return owner.OwnedBy(nodeID, lease.Epoch)
}

// ClaimDecision is the outcome of one exclusive-capability claim attempt.
type ClaimDecision int

const (
	// ClaimDenied means this node is not the capability's placed owner.
	ClaimDenied ClaimDecision = iota
	// ClaimHeld means this node already holds the lease at the owner's epoch.
	ClaimHeld
	// ClaimWait means a previous holder may still be acting until its lease
	// time runs out, so the claim must be retried later.
	ClaimWait
	// ClaimWrite means this node should store Claim.Lease, replacing the
	// observed lease when one was seen.
	ClaimWrite
)

// ClaimRequest is the state one claim attempt decides on.
type ClaimRequest struct {
	NodeID string
	// Owner is the placement of the capability's handler; Placed is false
	// when no exclusive placement names the capability.
	Owner  Ownership
	Placed bool
	// Held is this node's existing claim on the capability, if any.
	Held *HeldLease
	// Observed is the stored lease, valid when Seen.
	Observed ObservedLease
	Seen     bool
	Now      time.Time
	TTL      time.Duration
}

// Claim is the decision for a ClaimRequest.
type Claim struct {
	Decision ClaimDecision
	// Lease is the record to store when Decision is ClaimWrite.
	Lease Lease
}

// DecideClaim is the exclusive-capability fencing policy. Only the node the
// placement names as the single owner may claim. It keeps an active claim at
// the same epoch, waits out another holder's unreleased lease for ttl after
// this observer first saw it, and otherwise claims at the placement's epoch.
func DecideClaim(request ClaimRequest) Claim {
	owner := request.Owner
	if !request.Placed || len(owner.Nodes) != 1 || owner.Nodes[0] != request.NodeID {
		return Claim{Decision: ClaimDenied}
	}
	if held := request.Held; held != nil && held.Active() && held.Epoch == owner.Epoch {
		return Claim{Decision: ClaimHeld}
	}
	observed := request.Observed.Lease
	if request.Seen && observed.Holder != request.NodeID && !observed.Released &&
		request.Now.Sub(request.Observed.FirstSeen) < request.TTL {
		return Claim{Decision: ClaimWait}
	}
	return Claim{
		Decision: ClaimWrite,
		Lease:    Lease{Holder: request.NodeID, Epoch: owner.Epoch, Beat: 1},
	}
}

// ClaimWaitUntil is when a ClaimWait for request can next succeed: the moment
// the observed lease has gone unrenewed for ttl.
func ClaimWaitUntil(request ClaimRequest) time.Time {
	return request.Observed.FirstSeen.Add(request.TTL)
}
