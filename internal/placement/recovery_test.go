package placement

import (
	"reflect"
	"testing"

	"github.com/grove-project/grove"
)

func TestRecoverMovesFailedNodesServicesToCoordinator(t *testing.T) {
	members := []Member{
		{ID: "node-c", Live: true},
		{ID: "node-a", Failed: true},
		{ID: "node-b", Live: true},
		{ID: "node-d"}, // unavailable but never healthy: still joining
	}
	owners := map[grove.ServiceID]string{1: "node-a", 2: "node-c", 3: "node-a", 4: "node-d"}
	if got := Coordinator(members); got != "node-b" {
		t.Fatalf("Coordinator = %q; want the lowest live node, node-b", got)
	}
	want := map[grove.ServiceID]string{1: "node-b", 3: "node-b"}
	if got := Recover(members, owners); !reflect.DeepEqual(got, want) {
		t.Fatalf("Recover = %v; want %v", got, want)
	}
	if got := LiveNodes(members); !reflect.DeepEqual(got, []string{"node-b", "node-c"}) {
		t.Fatalf("LiveNodes = %v; want sorted live nodes", got)
	}
}

func TestRecoverWithoutLiveNodesMovesNothing(t *testing.T) {
	members := []Member{{ID: "node-a", Failed: true}}
	if got := Recover(members, map[grove.ServiceID]string{1: "node-a"}); len(got) != 0 {
		t.Fatalf("Recover = %v; want no moves without a live node", got)
	}
}

// The coordinator that recovers services is the node Place would pick as a
// new exclusive owner when every live node is eligible.
func TestCoordinatorMatchesExclusiveOwnerChoice(t *testing.T) {
	members := []Member{{ID: "node-3", Live: true}, {ID: "node-2", Live: true}, {ID: "node-1", Failed: true}}
	var nodes []Node
	for _, id := range LiveNodes(members) {
		nodes = append(nodes, node(id))
	}
	placed := Place(Topology{Nodes: nodes, Current: map[Handler][]string{reconcile: {"node-1"}}})
	if got := placed[reconcile]; len(got) != 1 || got[0] != Coordinator(members) {
		t.Fatalf("exclusive owner = %v; want the coordinator %s", got, Coordinator(members))
	}
}
