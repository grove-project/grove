package controlplane

import (
	"errors"
	"testing"
	"time"

	"github.com/grove-project/grove"
)

func serviceID(id uint32) grove.ServiceID { return grove.ServiceID(id) }

func TestControlStateReplicas(t *testing.T) {
	members := func(active, leaving int) map[string]MembershipRecord {
		records := make(map[string]MembershipRecord)
		for i := range active + leaving {
			id := string(rune('a' + i))
			records[id] = MembershipRecord{NodeID: id, AdvertisedEndpoint: "e", Leaving: i >= active}
		}
		return records
	}
	for _, tc := range []struct{ active, leaving, want int }{
		{0, 0, 1}, {1, 0, 1}, {2, 0, 2}, {3, 0, 3}, {5, 0, 3},
		{2, 1, 2}, {0, 3, 1}, {3, 2, 3},
	} {
		if got := ControlStateReplicas(members(tc.active, tc.leaving)); got != tc.want {
			t.Errorf("%d active, %d leaving: replicas = %d; want %d", tc.active, tc.leaving, got, tc.want)
		}
	}
}

func TestValidMembershipRecord(t *testing.T) {
	if !ValidMembershipRecord(MembershipRecord{NodeID: "n1", AdvertisedEndpoint: "nats://h:1"}) {
		t.Error("complete record rejected")
	}
	if ValidMembershipRecord(MembershipRecord{NodeID: "n1"}) || ValidMembershipRecord(MembershipRecord{AdvertisedEndpoint: "e"}) {
		t.Error("incomplete record accepted")
	}
}

func TestEvaluateHealth(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	membership := MembershipView{Ready: true, Members: []MembershipRecord{
		{NodeID: "n3", AdvertisedEndpoint: "e3"},
		{NodeID: "n1", AdvertisedEndpoint: "e1"},
		{NodeID: "n2", AdvertisedEndpoint: "e2", Leaving: true},
		{NodeID: "n4", AdvertisedEndpoint: "e4"},
	}}
	lastSeen := map[string]time.Time{
		"n1":       now.Add(-100 * time.Millisecond), // fresh
		"n2":       now,                              // fresh but leaving
		"n3":       now.Add(-time.Second),            // stale
		"stranger": now,                              // heartbeat from a non-member
	}
	view := EvaluateHealth(membership, lastSeen, now, 500*time.Millisecond)
	if !view.Ready || len(view.Nodes) != 4 {
		t.Fatalf("view = %+v; want ready with the four members only", view)
	}
	want := map[string]HealthState{"n1": HealthHealthy, "n2": HealthUnavailable, "n3": HealthUnavailable, "n4": HealthUnavailable}
	for i, node := range view.Nodes {
		if node.Health != want[node.NodeID] {
			t.Errorf("%s health = %s; want %s", node.NodeID, node.Health, want[node.NodeID])
		}
		if i > 0 && view.Nodes[i-1].NodeID >= node.NodeID {
			t.Errorf("nodes not sorted: %+v", view.Nodes)
		}
	}
	if view.Nodes[3].LastSeen != "" || view.Nodes[0].LastSeen == "" {
		t.Errorf("LastSeen should be set only for heard-from members: %+v", view.Nodes)
	}
	// Exactly at the deadline still counts as healthy.
	edge := EvaluateHealth(membership, map[string]time.Time{"n1": now.Add(-500 * time.Millisecond)}, now, 500*time.Millisecond)
	if edge.Nodes[0].Health != HealthHealthy {
		t.Errorf("heartbeat exactly at the deadline = %s; want healthy", edge.Nodes[0].Health)
	}
	notReady := EvaluateHealth(MembershipView{Error: "watch failed"}, nil, now, time.Second)
	if notReady.Ready || notReady.Error != "watch failed" {
		t.Errorf("uninitialized membership = %+v; want not ready with its error", notReady)
	}
}

func TestGateClusterView(t *testing.T) {
	nodes := func(n int) []ClusterNode { return make([]ClusterNode, n) }
	forming := GateClusterView(ClusterView{Ready: true, Nodes: nodes(2)}, MinClusterNodes, nil)
	if forming.Ready || forming.Error != ClusterFormingError(2, 3).Error() {
		t.Errorf("two of three nodes = %+v; want forming", forming)
	}
	settling := errors.New("settling")
	held := GateClusterView(ClusterView{Ready: true, Nodes: nodes(3)}, MinClusterNodes, func(n int) error {
		if n != 3 {
			t.Errorf("settled asked with %d nodes", n)
		}
		return settling
	})
	if held.Ready || held.Error != "settling" {
		t.Errorf("unsettled = %+v; want held not ready", held)
	}
	ready := GateClusterView(ClusterView{Ready: true, Nodes: nodes(3)}, MinClusterNodes, func(int) error { return nil })
	if !ready.Ready || ready.Error != "" {
		t.Errorf("formed and settled = %+v; want ready", ready)
	}
	down := GateClusterView(ClusterView{Error: "no leader"}, MinClusterNodes, func(int) error { t.Error("settled asked for a view that is not ready"); return nil })
	if down.Ready || down.Error != "no leader" {
		t.Errorf("not ready view changed: %+v", down)
	}
}

func TestValidateDesiredDeployment(t *testing.T) {
	deployment := DesiredDeployment{
		ApplicationID: "shop", Version: "v1", ArtifactDigest: digestA,
		Components: []DesiredComponent{{ServiceID: 3, NodeID: "n1"}, {ServiceID: 1, NodeID: "n2"}},
	}
	got, err := ValidateDesiredDeployment(deployment)
	if err != nil {
		t.Fatalf("ValidateDesiredDeployment: %v", err)
	}
	if got.Components[0].ServiceID != 1 || deployment.Components[0].ServiceID != 3 {
		t.Errorf("components = %+v; want a sorted copy", got.Components)
	}
	for name, mutate := range map[string]func(*DesiredDeployment){
		"dotted application": func(d *DesiredDeployment) { d.ApplicationID = "a.b" },
		"no version":         func(d *DesiredDeployment) { d.Version = "" },
		"bad digest":         func(d *DesiredDeployment) { d.ArtifactDigest = "sha256:1" },
		"no components":      func(d *DesiredDeployment) { d.Components = nil },
		"zero service":       func(d *DesiredDeployment) { d.Components = []DesiredComponent{{NodeID: "n1"}} },
		"no node":            func(d *DesiredDeployment) { d.Components = []DesiredComponent{{ServiceID: 1}} },
		"service twice":      func(d *DesiredDeployment) { d.Components = []DesiredComponent{{1, "n1"}, {1, "n2"}} },
	} {
		d := deployment
		mutate(&d)
		if _, err := ValidateDesiredDeployment(d); !errors.Is(err, ErrDesiredDeploymentInvalid) {
			t.Errorf("%s: err = %v; want ErrDesiredDeploymentInvalid", name, err)
		}
	}
}

func TestPlacementRecordRules(t *testing.T) {
	a := PlacementRecord{ServiceID: 1, NodeID: "n1", InvocationSubject: "s1", ArtifactDigest: digestA}
	b := PlacementRecord{ServiceID: 1, NodeID: "n2", InvocationSubject: "s2", ArtifactDigest: digestB}
	other := PlacementRecord{ServiceID: 2, NodeID: "n1", InvocationSubject: "s1", ArtifactDigest: digestA}

	if err := ValidatePlacementRecords([]PlacementRecord{a, other}); err != nil {
		t.Errorf("distinct services: %v", err)
	}
	if err := ValidatePlacementRecords([]PlacementRecord{a, b}); !errors.Is(err, ErrPlacementRecordInvalid) {
		t.Errorf("service placed twice: err = %v", err)
	}
	if err := ValidatePlacementRecords([]PlacementRecord{{ServiceID: 1, NodeID: "n1", InvocationSubject: "s"}}); !errors.Is(err, ErrPlacementRecordInvalid) {
		t.Errorf("record without artifact: err = %v", err)
	}

	view := PlacementView{Ready: true, Placements: []PlacementRecord{a, other}}
	if got, err := FindPlacement(view, 2); err != nil || got != other {
		t.Errorf("FindPlacement(2) = (%+v, %v)", got, err)
	}
	if _, err := FindPlacement(view, 9); !errors.Is(err, ErrServiceNotPlaced) {
		t.Errorf("unplaced service: err = %v", err)
	}
	view.Ready = false
	if _, err := FindPlacement(view, 1); !errors.Is(err, ErrPlacementUnavailable) {
		t.Errorf("view not ready: err = %v", err)
	}

	for _, tc := range []struct {
		name   string
		stored PlacementRecord
		write  bool
		err    error
	}{
		{"stored is current", a, true, nil},
		{"already replaced", b, false, nil},
		{"moved elsewhere", PlacementRecord{ServiceID: 1, NodeID: "n3", InvocationSubject: "s3", ArtifactDigest: digestA}, false, ErrPlacementChanged},
	} {
		write, err := CheckPlacementReplacement(tc.stored, a, b)
		if write != tc.write || !errors.Is(err, tc.err) || (tc.err == nil && err != nil) {
			t.Errorf("%s: = (%v, %v); want (%v, %v)", tc.name, write, err, tc.write, tc.err)
		}
	}
	if _, err := CheckPlacementReplacement(a, a, other); !errors.Is(err, ErrPlacementRecordInvalid) {
		t.Errorf("replacement for another service: err = %v", err)
	}
}
