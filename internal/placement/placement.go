// Package placement decides where registered handlers run and which
// placement serves each call. It is pure and deterministic so the same logic
// runs in production reconciliation and in the grovetest TestCluster.
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
// one node: its current owner while still eligible, otherwise the lowest node
// ID. Exclusivity constrains only that handler, not its service.
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
		owner := ids[0]
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
