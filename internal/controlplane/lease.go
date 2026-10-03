package controlplane

import (
	"time"

	"github.com/grove-project/grove/internal/placement"
)

// CapabilityLease is the stored claim on an exclusive capability. Beat changes
// on every renewal so observers can tell a live owner from a silent one.
type CapabilityLease struct {
	Holder   string `json:"holder"`
	Epoch    uint64 `json:"epoch"`
	Beat     uint64 `json:"beat"`
	Released bool   `json:"released,omitempty"`
}

// ObservedLease is a stored lease as one observer saw it. FirstSeen is when
// this observer first saw the lease's current version, on its own clock, so
// staleness never depends on clocks agreeing across nodes.
type ObservedLease struct {
	Lease     CapabilityLease
	FirstSeen time.Time
}

// HeldLease is this node's own claim on a capability.
type HeldLease struct {
	Capability string
	Handler    placement.Handler
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
func (l HeldLease) Renewal(nodeID string) CapabilityLease {
	return CapabilityLease{Holder: nodeID, Epoch: l.Epoch, Beat: l.Beat + 1}
}

// OwnsPlacement reports whether placements still name nodeID as the single
// owner of handler at epoch. A holder whose placement moved on must stop.
func OwnsPlacement(placements []HandlerPlacement, handler placement.Handler, epoch uint64, nodeID string) bool {
	for _, p := range placements {
		if p.Handler() == handler {
			return p.Epoch == epoch && len(p.Nodes) == 1 && p.Nodes[0].NodeID == nodeID
		}
	}
	return false
}

// LeaseHolds reports whether a held lease may still act: active, renewed
// within ttl of now, and still the placement's owner at the same epoch.
func LeaseHolds(lease HeldLease, placements []HandlerPlacement, nodeID string, now time.Time, ttl time.Duration) bool {
	if !lease.Active() || now.Sub(lease.RenewedAt) >= ttl {
		return false
	}
	return OwnsPlacement(placements, lease.Handler, lease.Epoch, nodeID)
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
	// ClaimWrite means this node should store ClaimRequest.Lease, as a
	// compare-and-set over the observed lease when one was seen.
	ClaimWrite
)

// ClaimRequest is the state one claim attempt decides on.
type ClaimRequest struct {
	NodeID     string
	Capability string
	// Placements is the observed handler placement.
	Placements []HandlerPlacement
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
	// Owned is the capability's placement when this node owns it.
	Owned HandlerPlacement
	// Lease is the record to store when Decision is ClaimWrite.
	Lease CapabilityLease
}

// DecideClaim is the exclusive-capability fencing policy. Only the node the
// placement names as the single owner may claim. It keeps an active claim at
// the same epoch, waits out another holder's unreleased lease for ttl after
// this observer first saw it, and otherwise claims at the placement's epoch.
func DecideClaim(request ClaimRequest) Claim {
	var owned HandlerPlacement
	found := false
	for _, p := range request.Placements {
		if p.Exclusive && p.Capability == request.Capability {
			owned, found = p, true
		}
	}
	if !found || len(owned.Nodes) != 1 || owned.Nodes[0].NodeID != request.NodeID {
		return Claim{Decision: ClaimDenied}
	}
	if held := request.Held; held != nil && held.Active() && held.Epoch == owned.Epoch {
		return Claim{Decision: ClaimHeld, Owned: owned}
	}
	observed := request.Observed.Lease
	if request.Seen && observed.Holder != request.NodeID && !observed.Released &&
		request.Now.Sub(request.Observed.FirstSeen) < request.TTL {
		return Claim{Decision: ClaimWait, Owned: owned}
	}
	return Claim{
		Decision: ClaimWrite,
		Owned:    owned,
		Lease:    CapabilityLease{Holder: request.NodeID, Epoch: owned.Epoch, Beat: 1},
	}
}
