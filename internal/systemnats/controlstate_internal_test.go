package systemnats

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// reconcileControlStateReplicas must report changed=false when every bucket's
// configured replica count already matches the target, and changed=true when
// it actually resized (or found a concurrent Grovlet doing so). Callers rely
// on this to know whether there is anything worth waiting to catch up: an
// abruptly failed node's replica is never evicted from a control stream's
// peer set (its membership record is never marked Leaving; see
// MembershipRecord.Leaving), so waiting for it to report Current would wedge
// forever on a round that never touched the replica count in the first place.
func TestReconcileControlStateReplicasReportsChanged(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	node1, err := StartClusterServer(ctx, ClusterConfig{
		Name:              "node-1",
		Host:              "127.0.0.1",
		RouteHost:         "127.0.0.1",
		JetStreamStoreDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(node1.Shutdown)
	// A second real logical node is required: growing a bucket to two
	// replicas cannot place both copies on the same logical node's paired
	// bootstrap servers (they share a placement tag on purpose).
	node2, err := StartClusterServer(ctx, ClusterConfig{
		Name:              "node-2",
		Host:              "127.0.0.1",
		RouteHost:         "127.0.0.1",
		JetStreamStoreDir: t.TempDir(),
		SeedURLs:          []string{node1.RouteURL()},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(node2.Shutdown)
	server := node1
	transport, err := Connect(ctx, server.URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.Close)
	js, err := jetstream.New(transport.connection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket: MembershipBucket, Storage: jetstream.FileStorage, Replicas: 1,
	}); err != nil {
		t.Fatal(err)
	}

	records := map[string]MembershipRecord{
		"node-1": {NodeID: "node-1", AdvertisedEndpoint: "nats-subject://system/node-1"},
	}
	// The bucket is already at replicas=1 for one active node: nothing to do.
	changed, err := reconcileControlStateReplicas(ctx, js, records, false)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("reconcileControlStateReplicas reported changed on an already-settled bucket")
	}

	// A second active node raises the target: this round must resize. The
	// route to node-2 may not have finished forming yet, so retry briefly.
	records["node-2"] = MembershipRecord{NodeID: "node-2", AdvertisedEndpoint: "nats-subject://system/node-2"}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		changed, err = reconcileControlStateReplicas(ctx, js, records, false)
		if err == nil {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("grow replicas: %v", err)
		}
	}
	if !changed {
		t.Error("reconcileControlStateReplicas reported no change while growing replicas")
	}

	// Reconciling again at the now-settled target is a no-op once more.
	changed, err = reconcileControlStateReplicas(ctx, js, records, false)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("reconcileControlStateReplicas reported changed after already resizing to the target")
	}
}
