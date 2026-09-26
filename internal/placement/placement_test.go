package placement

import (
	"reflect"
	"testing"
)

var (
	validate  = Handler{Service: 2, Method: 1}
	reconcile = Handler{Service: 2, Method: 3}
)

func node(id string) Node {
	return Node{ID: id, Handlers: map[Handler]Scaling{validate: Automatic, reconcile: Exclusive}}
}

func TestPlaceScalesAutomaticAndPinsExclusive(t *testing.T) {
	got := Place(Topology{Nodes: []Node{node("node-3"), node("node-1"), node("node-2")}})
	if want := []string{"node-1", "node-2", "node-3"}; !reflect.DeepEqual(got[validate], want) {
		t.Fatalf("automatic = %v, want %v", got[validate], want)
	}
	if want := []string{"node-1"}; !reflect.DeepEqual(got[reconcile], want) {
		t.Fatalf("exclusive = %v, want %v", got[reconcile], want)
	}
}

func TestPlaceKeepsExclusiveOwnerWhileEligible(t *testing.T) {
	current := map[Handler][]string{reconcile: {"node-2"}}
	got := Place(Topology{Nodes: []Node{node("node-1"), node("node-2")}, Current: current})
	if !reflect.DeepEqual(got[reconcile], []string{"node-2"}) {
		t.Fatalf("owner moved to %v", got[reconcile])
	}
	got = Place(Topology{Nodes: []Node{node("node-1")}, Current: current})
	if !reflect.DeepEqual(got[reconcile], []string{"node-1"}) {
		t.Fatalf("owner did not fail over: %v", got[reconcile])
	}
}

func TestNextEpochAdvancesOnlyWhenOwnerChanges(t *testing.T) {
	if got := NextEpoch(nil, []string{"a"}, 0); got != 1 {
		t.Fatalf("first epoch = %d", got)
	}
	if got := NextEpoch([]string{"a"}, []string{"a"}, 1); got != 1 {
		t.Fatalf("unchanged epoch = %d", got)
	}
	if got := NextEpoch([]string{"a"}, []string{"b"}, 1); got != 2 {
		t.Fatalf("moved epoch = %d", got)
	}
}

func TestSelectorRoundRobinPerHandler(t *testing.T) {
	var s Selector
	if _, ok := s.Pick(validate, nil); ok {
		t.Fatal("picked from no targets")
	}
	targets := []string{"a", "b"}
	var got []string
	for range 4 {
		n, _ := s.Pick(validate, targets)
		got = append(got, n)
	}
	if !reflect.DeepEqual(got, []string{"a", "b", "a", "b"}) {
		t.Fatalf("got %v", got)
	}
	if n, _ := s.Pick(reconcile, targets); n != "a" {
		t.Fatalf("handlers share a cursor: %s", n)
	}
}
