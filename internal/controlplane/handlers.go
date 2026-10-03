package controlplane

import (
	"errors"
	"sort"
	"strings"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/placement"
)

var (
	// ErrHandlerNotPlaced is returned when a handler has no live placement.
	ErrHandlerNotPlaced = errors.New("grove handler is not placed")
	// ErrHandlerPlacementUnavailable is returned before the handler-placement
	// view or the live-node view has initialized.
	ErrHandlerPlacementUnavailable = errors.New("grove handler placement is unavailable")
	// ErrHandlerPlacementInvalid is returned for an invalid registration.
	ErrHandlerPlacementInvalid = errors.New("grove handler placement configuration is invalid")
)

// HandlerRegistration declares one handler a node can run.
type HandlerRegistration struct {
	Service grove.ServiceID `json:"service"`
	Method  grove.MethodID  `json:"method"`
	// Exclusive requests exactly one active owner cluster-wide.
	Exclusive bool `json:"exclusive,omitempty"`
	// Capability names the exclusive capability the handler's workload claims
	// with grove.Exclusive. It is required when Exclusive is set.
	Capability string `json:"capability,omitempty"`
	// InvocationSubject overrides the node's default subject for this handler,
	// for nodes that host different services behind different endpoints.
	InvocationSubject string `json:"invocation_subject,omitempty"`
}

// Handler is the placeable handler this registration declares.
func (r HandlerRegistration) Handler() placement.Handler {
	return placement.Handler{Service: r.Service, Method: r.Method}
}

// NodeHandlers is one node's published registrations.
type NodeHandlers struct {
	NodeID            string                `json:"node_id"`
	InvocationSubject string                `json:"invocation_subject"`
	Handlers          []HandlerRegistration `json:"handlers"`
}

// HandlerNode is one concrete placement of a handler.
type HandlerNode struct {
	NodeID            string `json:"node_id"`
	InvocationSubject string `json:"invocation_subject"`
}

// HandlerPlacement is the authoritative placement of one handler.
type HandlerPlacement struct {
	Service    grove.ServiceID `json:"service"`
	Method     grove.MethodID  `json:"method"`
	Exclusive  bool            `json:"exclusive,omitempty"`
	Capability string          `json:"capability,omitempty"`
	// Epoch fences exclusive ownership; it increases whenever the owner moves.
	Epoch uint64        `json:"epoch"`
	Nodes []HandlerNode `json:"nodes"`
}

// Handler is the handler this placement places.
func (p HandlerPlacement) Handler() placement.Handler {
	return placement.Handler{Service: p.Service, Method: p.Method}
}

// NodeIDs lists the placement's nodes in stored order.
func (p HandlerPlacement) NodeIDs() []string {
	ids := make([]string, len(p.Nodes))
	for i, n := range p.Nodes {
		ids[i] = n.NodeID
	}
	return ids
}

// LeaseView is one observed exclusive capability lease.
type LeaseView struct {
	Capability string `json:"capability"`
	Holder     string `json:"holder"`
	Epoch      uint64 `json:"epoch"`
}

// HandlerPlacementView is one node's observation of handler placement.
type HandlerPlacementView struct {
	Ready      bool               `json:"ready"`
	Nodes      []NodeHandlers     `json:"nodes"`
	Placements []HandlerPlacement `json:"placements"`
	Leases     []LeaseView        `json:"leases"`
	// Lost lists placements whose node is no longer live. Resolved views drop
	// them from Placements and report them here so recovery stays visible.
	Lost  []LostPlacement `json:"lost,omitempty"`
	Error string          `json:"error,omitempty"`
}

// LostPlacement is a placement removed from routing because its node is not
// live; it stays visible until reconciliation replaces it.
type LostPlacement struct {
	Service grove.ServiceID `json:"service"`
	Method  grove.MethodID  `json:"method"`
	NodeID  string          `json:"node_id"`
}

// ValidateRegistrations checks that every handler names a service, appears
// once, and names a valid capability when it is exclusive.
func ValidateRegistrations(handlers []HandlerRegistration) error {
	seen := make(map[placement.Handler]bool)
	for _, r := range handlers {
		if r.Service == 0 || seen[r.Handler()] || (r.Exclusive && !ValidCapabilityName(r.Capability)) {
			return ErrHandlerPlacementInvalid
		}
		seen[r.Handler()] = true
	}
	return nil
}

// ValidCapabilityName reports whether name can name an exclusive capability:
// non-empty ASCII letters, digits and "-_/=".
func ValidCapabilityName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_/=", r)) {
			return false
		}
	}
	return true
}

// HandlerPlacementWrite is one placement record reconciliation wants stored.
type HandlerPlacementWrite struct {
	Placement HandlerPlacement
	// Create is true when no placement is stored for the handler yet.
	Create bool
}

// HandlerPlacementPlan is what reconciliation changes in authoritative
// handler placement, ordered by handler.
type HandlerPlacementPlan struct {
	Writes  []HandlerPlacementWrite
	Deletes []placement.Handler
}

// PlanHandlerPlacements is one reconciliation step. From the registrations of
// live nodes and the stored placements it computes the deterministic placement
// (placement.Place) and returns the records that differ. An exclusive handler
// whose owner moves gets a higher epoch than both its stored placement and the
// highest epoch ever claimed on its capability (leaseEpochs), so fencing
// survives a deleted and recreated placement. Handlers no live node registers
// any more are deleted.
//
// Every node runs the same plan over the same state, so concurrent
// reconcilers propose identical writes and compare-and-set lets one win.
func PlanHandlerPlacements(
	nodes map[string]NodeHandlers,
	placements map[placement.Handler]HandlerPlacement,
	live []string,
	leaseEpochs map[string]uint64,
) HandlerPlacementPlan {
	liveSet := make(map[string]bool, len(live))
	for _, id := range live {
		liveSet[id] = true
	}
	topology := placement.Topology{Current: make(map[placement.Handler][]string, len(placements))}
	subjects := make(map[string]map[placement.Handler]string)
	meta := make(map[placement.Handler]HandlerRegistration)
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if !liveSet[id] {
			continue
		}
		record := nodes[id]
		subjects[id] = make(map[placement.Handler]string, len(record.Handlers))
		node := placement.Node{ID: id, Handlers: make(map[placement.Handler]placement.Scaling, len(record.Handlers))}
		for _, r := range record.Handlers {
			scaling := placement.Automatic
			if r.Exclusive {
				scaling = placement.Exclusive
			}
			node.Handlers[r.Handler()] = scaling
			subjects[id][r.Handler()] = cmpSubject(r.InvocationSubject, record.InvocationSubject)
			meta[r.Handler()] = r
		}
		topology.Nodes = append(topology.Nodes, node)
	}
	for id, record := range placements {
		topology.Current[id] = record.NodeIDs()
	}
	desired := placement.Place(topology)

	var plan HandlerPlacementPlan
	for _, id := range sortedHandlers(desired) {
		target := desired[id]
		current, exists := placements[id]
		if exists && equalStrings(current.NodeIDs(), target) {
			continue
		}
		next := HandlerPlacement{
			Service: id.Service, Method: id.Method,
			Exclusive: meta[id].Exclusive, Capability: meta[id].Capability, Epoch: current.Epoch,
		}
		if next.Exclusive {
			next.Epoch = placement.FencedEpoch(current.NodeIDs(), target, current.Epoch, leaseEpochs[next.Capability])
		}
		for _, nodeID := range target {
			next.Nodes = append(next.Nodes, HandlerNode{NodeID: nodeID, InvocationSubject: subjects[nodeID][id]})
		}
		plan.Writes = append(plan.Writes, HandlerPlacementWrite{Placement: next, Create: !exists})
	}
	for _, id := range sortedHandlers(placements) {
		if _, wanted := desired[id]; !wanted {
			plan.Deletes = append(plan.Deletes, id)
		}
	}
	return plan
}

// ResolveHandlerPlacements restricts view to live nodes: placements with no
// live node are dropped, and every dropped node is reported in Lost, so every
// consumer routes over the same healthy set.
func ResolveHandlerPlacements(view HandlerPlacementView, live []string) HandlerPlacementView {
	liveSet := make(map[string]bool, len(live))
	for _, id := range live {
		liveSet[id] = true
	}
	resolved := make([]HandlerPlacement, 0, len(view.Placements))
	for _, p := range view.Placements {
		nodes := make([]HandlerNode, 0, len(p.Nodes))
		for _, n := range p.Nodes {
			if liveSet[n.NodeID] {
				nodes = append(nodes, n)
			} else {
				view.Lost = append(view.Lost, LostPlacement{Service: p.Service, Method: p.Method, NodeID: n.NodeID})
			}
		}
		if len(nodes) != 0 {
			p.Nodes = nodes
			resolved = append(resolved, p)
		}
	}
	view.Placements = resolved
	return view
}

// LiveHandlerPlacement returns the handler's placement in view restricted to
// live nodes, or ErrHandlerNotPlaced when none of its nodes is live.
func LiveHandlerPlacement(view HandlerPlacementView, live []string, service grove.ServiceID, method grove.MethodID) (HandlerPlacement, error) {
	liveSet := make(map[string]bool, len(live))
	for _, id := range live {
		liveSet[id] = true
	}
	for _, p := range view.Placements {
		if p.Service != service || p.Method != method {
			continue
		}
		healthy := make([]HandlerNode, 0, len(p.Nodes))
		for _, n := range p.Nodes {
			if liveSet[n.NodeID] {
				healthy = append(healthy, n)
			}
		}
		if len(healthy) == 0 {
			break
		}
		p.Nodes = healthy
		return p, nil
	}
	return HandlerPlacement{}, ErrHandlerNotPlaced
}

func sortedHandlers[V any](m map[placement.Handler]V) []placement.Handler {
	ids := make([]placement.Handler, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return ids[i].Service < ids[j].Service || ids[i].Service == ids[j].Service && ids[i].Method < ids[j].Method
	})
	return ids
}

func cmpSubject(specific, fallback string) string {
	if specific != "" {
		return specific
	}
	return fallback
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
