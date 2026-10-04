package grovetest_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/grovetest"
)

// saveOrders is application code: it uses an ordinary local file and Grove
// only to acquire it and publish safe points.
func saveOrders(ctx context.Context, orders string) (grove.Version, error) {
	file, err := grove.Files(ctx).Acquire(ctx, "state/orders.txt", grove.Replicas(3), grove.WithDurability(grove.Quorum))
	if err != nil {
		return grove.Version{}, err
	}
	defer file.Release(ctx)
	if err := os.WriteFile(file.LocalPath(), []byte(orders), 0o600); err != nil {
		return grove.Version{}, err
	}
	return file.Sync(ctx)
}

func TestFilesClusterFailoverAndColdRestart(t *testing.T) {
	cluster := grovetest.NewFilesCluster(t, "node-a", "node-b", "node-c")
	committed, err := saveOrders(cluster.Context("node-a"), "1,2,3")
	if err != nil {
		t.Fatal(err)
	}
	cluster.WaitHolders("state/orders.txt", 3)

	// The owner dies holding the file: another node takes over from the
	// last committed version once the owner's lease runs out.
	ctx := cluster.Context("node-a")
	held, err := grove.Files(ctx).Acquire(ctx, "state/orders.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(held.LocalPath(), []byte("1,2,3,4 never synced"), 0o600); err != nil {
		t.Fatal(err)
	}
	cluster.Kill("node-a")
	ctx, cancel := context.WithTimeout(cluster.Context("node-b"), 10*time.Second)
	defer cancel()
	owned, err := grove.Files(ctx).Acquire(ctx, "state/orders.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(owned.LocalPath()); string(got) != "1,2,3" || owned.CurrentVersion() != committed {
		t.Fatalf("failover at %s with %q; want %s with the last committed content", owned.CurrentVersion().ID, got, committed.ID)
	}
	if _, err := held.Sync(cluster.Context("node-a")); err == nil {
		t.Fatal("the dead owner's handle still syncs")
	}
	if err := os.WriteFile(owned.LocalPath(), []byte("1,2,3,5"), 0o600); err != nil {
		t.Fatal(err)
	}
	latest, err := owned.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := owned.Release(ctx); err != nil {
		t.Fatal(err)
	}

	// Stop everything and lose the metadata; one replica brings it back.
	cluster.StopAll()
	cluster.LoseMetadata()
	cluster.Start("node-c")
	waitFor(t, func() bool { return cluster.Current("state/orders.txt") == latest })
	cluster.Start("node-a")
	cluster.Start("node-b")
	waitFor(t, func() bool {
		holders := cluster.Holders("state/orders.txt")
		return slices.Contains(holders, "node-a") && slices.Contains(holders, "node-b")
	})
	ctx = cluster.Context("node-a")
	file, err := grove.Files(ctx).Open(ctx, "state/orders.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(file.LocalPath()); string(got) != "1,2,3,5" {
		t.Fatalf("after cold restart node-a reads %q", got)
	}
}

func TestFilesClusterOwnershipIsExclusive(t *testing.T) {
	cluster := grovetest.NewFilesCluster(t, "node-a", "node-b")
	ctx := cluster.Context("node-a")
	if _, err := grove.Files(ctx).Acquire(ctx, "state/lock"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(cluster.Context("node-b"), 3*grovetest.FilesLeaseTTL)
	defer cancel()
	if _, err := grove.Files(ctx).Acquire(ctx, "state/lock"); !errors.Is(err, grove.ErrFileOwned) {
		t.Fatalf("second owner: %v; want ErrFileOwned", err)
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in 10s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
