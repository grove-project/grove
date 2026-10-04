package files

import "time"

// EventKind names a Grove Files lifecycle event.
type EventKind string

const (
	EventOwnershipAcquired  EventKind = "ownership.acquired"
	EventPromoted           EventKind = "ownership.promoted"
	EventOwnershipLost      EventKind = "ownership.lost"
	EventOwnershipReleased  EventKind = "ownership.released"
	EventSyncStarted        EventKind = "sync.started"
	EventSyncCommitted      EventKind = "sync.committed"
	EventSyncFailed         EventKind = "sync.failed"
	EventReplicaTransferred EventKind = "replica.transferred"
	EventCorrupt            EventKind = "replica.corrupt"
	EventBootstrap          EventKind = "recovery.bootstrap"
	EventReconciled         EventKind = "reconcile.converged"
	EventDiverged           EventKind = "reconcile.diverged"
	EventLineage            EventKind = "lineage.mismatch"
)

// Event is one structured Grove Files lifecycle event.
type Event struct {
	Kind    EventKind
	At      time.Time
	Node    string
	Path    string
	Version string
	Epoch   uint64
	// Peer is the other node involved: the source or target of a transfer,
	// or the previous owner of a promotion.
	Peer string
	Err  string
}
