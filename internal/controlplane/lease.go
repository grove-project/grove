package controlplane

import (
	"time"

	"github.com/grove-project/grove/internal/placement"
)

// The exclusive-lease records and fencing policy belong to internal/placement,
// which the grovetest TestCluster also runs; these names adapt them to stored
// handler placements.
type (
	// CapabilityLease is the stored claim on an exclusive capability.
	CapabilityLease = placement.Lease
	// ObservedLease is a stored lease as one observer saw it.
	ObservedLease = placement.ObservedLease
	// HeldLease is this node's own claim on a capability.
	HeldLease = placement.HeldLease
	// ClaimDecision is the outcome of one exclusive-capability claim attempt.
	ClaimDecision = placement.ClaimDecision
)

const (
	ClaimDenied = placement.ClaimDenied
	ClaimHeld   = placement.ClaimHeld
	ClaimWait   = placement.ClaimWait
	ClaimWrite  = placement.ClaimWrite
)

// Ownership is the placement's owner set and epoch as the lease policy sees it.
func (p HandlerPlacement) Ownership() placement.Ownership {
	return placement.Ownership{Nodes: p.NodeIDs(), Epoch: p.Epoch}
}

// OwnsPlacement reports whether placements still name nodeID as the single
// owner of handler at epoch. A holder whose placement moved on must stop.
func OwnsPlacement(placements []HandlerPlacement, handler placement.Handler, epoch uint64, nodeID string) bool {
	for _, p := range placements {
		if p.Handler() == handler {
			return p.Ownership().OwnedBy(nodeID, epoch)
		}
	}
	return false
}

// LeaseHolds reports whether a held lease may still act: active, renewed
// within ttl of now, and still the placement's owner at the same epoch.
func LeaseHolds(lease HeldLease, placements []HandlerPlacement, nodeID string, now time.Time, ttl time.Duration) bool {
	for _, p := range placements {
		if p.Handler() == lease.Handler {
			return placement.LeaseHolds(lease, p.Ownership(), nodeID, now, ttl)
		}
	}
	return false
}

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

// DecideClaim finds the exclusive placement for request.Capability and applies
// placement.DecideClaim to it.
func DecideClaim(request ClaimRequest) Claim {
	var owned HandlerPlacement
	found := false
	for _, p := range request.Placements {
		if p.Exclusive && p.Capability == request.Capability {
			owned, found = p, true
		}
	}
	claim := placement.DecideClaim(placement.ClaimRequest{
		NodeID:   request.NodeID,
		Owner:    owned.Ownership(),
		Placed:   found,
		Held:     request.Held,
		Observed: request.Observed,
		Seen:     request.Seen,
		Now:      request.Now,
		TTL:      request.TTL,
	})
	if claim.Decision == ClaimDenied {
		return Claim{Decision: ClaimDenied}
	}
	return Claim{Decision: claim.Decision, Owned: owned, Lease: claim.Lease}
}
