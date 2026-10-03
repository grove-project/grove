// Package placement is Grove's placement subsystem. It owns every decision
// about where work runs:
//
//   - Place and NextEpoch decide which nodes run each handler and fence
//     exclusive ownership with an epoch.
//   - Selector picks the placement that serves each call.
//   - Recover decides where a failed node's services move.
//   - DecideClaim and LeaseHolds decide when an exclusive owner may act.
//
// It is pure and deterministic, with no storage, transport or clock of its
// own, so production (internal/controlplane records stored by
// internal/systemnats) and the grovetest TestCluster run the same rules. See
// docs/architecture/placement.md.
package placement

import (
	"fmt"
	"sort"
	"sync"

	"github.com/grove-project/grove"
)

// Handler names one placeable handler: an explicit service and method pair.
// Placement is decided per handler, never per service.
type Handler struct {
	Service grove.ServiceID
	Method  grove.MethodID
}

func (h Handler) String() string { return fmt.Sprintf("%d.%d", h.Service, h.Method) }

// Scaling is how a handler's placement count is decided.
type Scaling int

const (
	// Automatic handlers run on every eligible node; Grove decides the count.
	Automatic Scaling = iota
	// Exclusive handlers have exactly one active owner cluster-wide.
	Exclusive
)

func (s Scaling) String() string {
	if s == Exclusive {
		return "exclusive"
	}
	return "automatic"
}

// Node is one live node and the handlers it can run.
type Node struct {
	ID       string
	Handlers map[Handler]Scaling
}

// Topology is the input to Place.
type Topology struct {
	Nodes []Node
	// Current holds the published placements, used to keep exclusive owners
	// stable while they remain eligible.
	Current map[Handler][]string
}

// Place returns handler -> sorted node IDs. Automatic handlers are placed on
// every node that registered them. An exclusive handler is placed on exactly
// one node: its current owner while still eligible, otherwise the lowest
// eligible node ID (the rule Coordinator also uses). Exclusivity constrains
// only that handler, not its service.
func Place(t Topology) map[Handler][]string {
	eligible := make(map[Handler][]string)
	exclusive := make(map[Handler]bool)
	for _, node := range t.Nodes {
		for h, scaling := range node.Handlers {
			eligible[h] = append(eligible[h], node.ID)
			exclusive[h] = exclusive[h] || scaling == Exclusive
		}
	}
	placements := make(map[Handler][]string, len(eligible))
	for h, ids := range eligible {
		sort.Strings(ids)
		if !exclusive[h] {
			placements[h] = ids
			continue
		}
		owner := lowest(ids)
		for _, current := range t.Current[h] {
			for _, id := range ids {
				if id == current {
					owner = current
				}
			}
		}
		placements[h] = []string{owner}
	}
	return placements
}

// NextEpoch returns the fencing epoch for an exclusive handler after its
// owner set changes from previous to next. The epoch increases whenever
// ownership moves, so an owner holding an older epoch is stale.
func NextEpoch(previous, next []string, epoch uint64) uint64 {
	if len(previous) == len(next) {
		same := true
		for i := range previous {
			same = same && previous[i] == next[i]
		}
		if same && epoch > 0 {
			return epoch
		}
	}
	return epoch + 1
}

// FencedEpoch is the epoch an exclusive handler gets when its owners change
// from previous to next. stored is the epoch of its stored placement (zero if
// none is stored) and claimed the highest epoch any lease on its capability
// has been claimed at. Epochs only grow, even when a placement is deleted and
// recreated, because the lease remembers the highest epoch ever claimed.
func FencedEpoch(previous, next []string, stored, claimed uint64) uint64 {
	return NextEpoch(previous, next, max(stored, claimed))
}

// Selector spreads calls round-robin across a handler's healthy placements.
// The zero value is ready to use.
type Selector struct {
	mu   sync.Mutex
	next map[Handler]uint64
}

// Pick returns the next target for h, or false when there are none.
func (s *Selector) Pick(h Handler, targets []string) (string, bool) {
	if len(targets) == 0 {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next == nil {
		s.next = make(map[Handler]uint64)
	}
	n := s.next[h]
	s.next[h] = n + 1
	return targets[n%uint64(len(targets))], true
}
