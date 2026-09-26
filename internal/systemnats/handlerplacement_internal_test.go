package systemnats

import (
	"reflect"
	"testing"

	"github.com/grove-project/grove"
)

// A placement on a node that failure detection dropped leaves routing at once
// but stays visible as lost until reconciliation replaces it.
func TestResolvedViewSeparatesLostFromHealthyPlacements(t *testing.T) {
	live := []string{"node-2"}
	h, err := NewHandlerPlacements(HandlerPlacementConfig{
		NodeID: "node-2", InvocationSubject: "s",
		Live: func() ([]string, bool) { return live, true },
	})
	if err != nil {
		t.Fatal(err)
	}
	h.view = HandlerPlacementView{Ready: true, Placements: []HandlerPlacement{
		{Service: 2, Method: 2, Nodes: []HandlerNode{{NodeID: "node-1"}, {NodeID: "node-2"}}},
		{Service: 2, Method: 3, Exclusive: true, Epoch: 4, Nodes: []HandlerNode{{NodeID: "node-1"}}},
	}}

	resolved := h.Resolved()
	if len(resolved.Placements) != 1 || resolved.Placements[0].Method != 2 ||
		len(resolved.Placements[0].Nodes) != 1 || resolved.Placements[0].Nodes[0].NodeID != "node-2" {
		t.Fatalf("resolved placements = %#v; want only Charge on node-2", resolved.Placements)
	}
	want := []LostPlacement{
		{Service: grove.ServiceID(2), Method: 2, NodeID: "node-1"},
		{Service: grove.ServiceID(2), Method: 3, NodeID: "node-1"},
	}
	if !reflect.DeepEqual(resolved.Lost, want) {
		t.Fatalf("lost = %#v; want %#v", resolved.Lost, want)
	}
	// The exclusive handler is unroutable until it has a live owner.
	if _, err := h.Lookup(2, 3); err == nil {
		t.Fatal("Lookup routed to an exclusive owner that is not live")
	}
	if got := len(h.Snapshot().Placements); got != 2 {
		t.Fatalf("resolving changed the observed view: %d placements", got)
	}

	live = []string{"node-1", "node-2"}
	if resolved := h.Resolved(); len(resolved.Lost) != 0 || len(resolved.Placements) != 2 {
		t.Fatalf("node-1 back: resolved = %#v; want nothing lost", resolved)
	}
}
