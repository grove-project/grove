package files_test

import (
	"slices"
	"testing"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/files"
	"github.com/grove-project/grove/internal/placement"
)

func TestRequiredCopies(t *testing.T) {
	tests := []struct {
		durability grove.Durability
		replicas   int
		want       int
	}{
		{grove.Local, 3, 1},
		{grove.Replicated, 3, 3},
		{grove.Replicated, 1, 1},
		{grove.Quorum, 3, 2},
		{grove.Quorum, 4, 3},
		{grove.Quorum, 5, 3},
		{grove.Quorum, 1, 1},
	}
	for _, test := range tests {
		if got := files.RequiredCopies(test.durability, test.replicas); got != test.want {
			t.Errorf("RequiredCopies(%s, %d) = %d; want %d", test.durability, test.replicas, got, test.want)
		}
	}
}

func TestChooseTargetsPrefersExistingReplicas(t *testing.T) {
	record := files.Record{ReplicaSet: []files.ReplicaStatus{{Node: "d", VersionID: "v1", State: files.ReplicaStale}}}
	got := files.ChooseTargets([]string{"c", "self", "b", "d", "b"}, "self", record, 2)
	if !slices.Equal(got, []string{"d", "b"}) {
		t.Fatalf("targets = %v; want [d b]", got)
	}
	if got := files.ChooseTargets([]string{"self"}, "self", record, 2); len(got) != 0 {
		t.Fatalf("targets with no peers = %v", got)
	}
}

func TestRepairLeader(t *testing.T) {
	current := files.VersionMeta{ID: "v2"}
	record := files.Record{
		Current: &current,
		Lease:   placement.Lease{Holder: "a", Epoch: 1},
		ReplicaSet: []files.ReplicaStatus{
			{Node: "a", VersionID: "v2", State: files.ReplicaVerified},
			{Node: "c", VersionID: "v2", State: files.ReplicaVerified},
			{Node: "b", VersionID: "v1", State: files.ReplicaStale},
		},
	}
	if got := files.RepairLeader(record, []string{"a", "b", "c"}); got != "a" {
		t.Errorf("leader with a live owner = %q; want a", got)
	}
	if got := files.RepairLeader(record, []string{"b", "c"}); got != "c" {
		t.Errorf("leader without the owner = %q; want the live holder c", got)
	}
	if got := files.RepairLeader(record, []string{"b"}); got != "" {
		t.Errorf("leader with only a stale node = %q; want none", got)
	}
}

func TestCommitMarksOtherCopiesStale(t *testing.T) {
	v1 := files.VersionMeta{ID: "v1", Generation: 1}
	record := files.Record{Current: &v1, ReplicaSet: []files.ReplicaStatus{
		{Node: "a", VersionID: "v1", State: files.ReplicaVerified},
		{Node: "b", VersionID: "v1", State: files.ReplicaVerified},
	}}
	staged := files.VersionMeta{ID: "v2", Generation: 2}
	record.Staging = &staged
	committed := record.Commit(staged, []string{"a", "c"}, 1)
	if committed.Current.ID != "v2" || committed.Staging != nil {
		t.Fatalf("current %v staging %v", committed.Current, committed.Staging)
	}
	if !slices.Equal(committed.Holders("v2"), []string{"a", "c"}) {
		t.Fatalf("holders = %v", committed.Holders("v2"))
	}
	if len(committed.History) != 1 || committed.History[0].ID != "v1" {
		t.Fatalf("history = %v", committed.History)
	}
	for _, replica := range committed.ReplicaSet {
		if replica.Node == "b" && replica.State != files.ReplicaStale {
			t.Fatalf("b = %+v; want stale", replica)
		}
	}
	if record.Current.ID != "v1" {
		t.Fatal("Commit modified its receiver")
	}
}

func TestVersionIDsSortByGeneration(t *testing.T) {
	ids := []string{files.VersionIDFor(10, "ffff"), files.VersionIDFor(9, "0000"), files.VersionIDFor(255, "aaaa")}
	if !slices.IsSorted([]string{ids[1], ids[0], ids[2]}) {
		t.Fatalf("version IDs %v do not sort by generation", ids)
	}
}
