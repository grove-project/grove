package placement

import (
	"sort"

	"github.com/grove-project/grove"
)

// Member is one node as cluster membership observes it.
type Member struct {
	ID string
	// Live nodes are healthy and can run work.
	Live bool
	// Failed nodes were healthy before and are unavailable now, so their work
	// must move. A node that has never been healthy has not failed.
	Failed bool
}

// LiveNodes returns the sorted IDs of the live members. Handler placement and
// routing consider only these nodes.
func LiveNodes(members []Member) []string {
	live := make([]string, 0, len(members))
	for _, m := range members {
		if m.Live {
			live = append(live, m.ID)
		}
	}
	sort.Strings(live)
	return live
}

// Coordinator is the live node that acts for the cluster when one node must
// decide: the lowest live node ID, the same rule Place uses to pick a new
// exclusive owner. It is "" when no node is live.
func Coordinator(members []Member) string {
	return lowest(LiveNodes(members))
}

// Recover decides where services move when their node fails. owners maps
// each service to the node that serves it, as last recorded, so recovery can
// decide while the control plane is unavailable. Every service on a failed
// node moves to the Coordinator; the result holds only services that move.
func Recover(members []Member, owners map[grove.ServiceID]string) map[grove.ServiceID]string {
	target := Coordinator(members)
	if target == "" {
		return nil
	}
	failed := make(map[string]bool, len(members))
	for _, m := range members {
		failed[m.ID] = m.Failed
	}
	moves := make(map[grove.ServiceID]string)
	for service, node := range owners {
		if failed[node] {
			moves[service] = target
		}
	}
	return moves
}

// lowest returns the first of sorted ids, or "" when there are none.
func lowest(sorted []string) string {
	if len(sorted) == 0 {
		return ""
	}
	return sorted[0]
}
