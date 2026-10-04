package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/files"
	"github.com/grove-project/grove/internal/systemnats"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Grove Files across real Grovlet processes: an application process syncs a
// file through its Grovlet, the bytes land in every node's runtime
// directory, the owner's Grovlet is killed and another node takes the file
// over, and after every Grovlet stops and the System NATS state is deleted the
// restarted cluster rebuilds the catalog from the nodes' disks.
func TestGroveFilesAcrossGrovletProcesses(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	cluster := startMembershipGrovlets(t, ctx)
	waitForFormedCluster(t, ctx, cluster)
	const path = "state/e2e.bin"
	data := bytes.Repeat([]byte("grove files across processes "), 20_000) // ~580 KB: several chunks

	// The test process plays an application process on node-1.
	app := grove.WithFileStore(ctx, cluster.transports[0].NewRemoteFileStore(ctx, "node-1", 0))
	var file grove.OwnedFile
	waitUntil(t, ctx, cluster, "node-1 acquires the file", func() bool {
		attempt, cancel := context.WithTimeout(app, 5*time.Second)
		defer cancel()
		var err error
		file, err = grove.Files(app).Acquire(attempt, path, grove.Replicas(3), grove.WithDurability(grove.Quorum))
		return err == nil
	})
	if err := os.WriteFile(file.LocalPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	version, err := file.Sync(app)
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, clusterLogs(cluster.nodes))
	}
	if err := file.WaitReplicated(app, version); err != nil {
		t.Fatalf("wait replicated: %v\n%s", err, clusterLogs(cluster.nodes))
	}
	// The catalog bucket appeared with the first write; it grows to every
	// control-plane voter like the other control-state buckets.
	natsURL := readyEventFromLogs(t, cluster.nodes[2].Logs()).SystemNATSURL
	waitUntil(t, ctx, cluster, "the files catalog is replicated on three nodes", func() bool {
		return filesCatalogReplicas(ctx, natsURL) == 3
	})
	digest := sha256.Sum256(data)
	if version.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("version checksum %s", version.SHA256)
	}
	blob := func(i int) string {
		return filepath.Join(cluster.nodes[i].TempDir(), "files", "objects", files.FileIDFor(path), "versions", version.ID+".blob")
	}
	for i := range cluster.nodes {
		if got, err := os.ReadFile(blob(i)); err != nil || !bytes.Equal(got, data) {
			t.Fatalf("%s replica: %d bytes, %v", cluster.nodeIDs[i], len(got), err)
		}
	}

	// The owner's Grovlet dies. node-2 takes the file over from the
	// committed version once node-1's ownership has run out.
	if err := cluster.nodes[0].Kill(ctx); err != nil {
		t.Fatal(err)
	}
	takeover := grove.WithFileStore(ctx, cluster.transports[1].NewRemoteFileStore(ctx, "node-2", 0))
	acquireCtx, cancelAcquire := context.WithTimeout(takeover, 60*time.Second)
	defer cancelAcquire()
	owned, err := grove.Files(takeover).Acquire(acquireCtx, path)
	if err != nil {
		t.Fatalf("take over: %v\n%s", err, clusterLogs(cluster.nodes))
	}
	if owned.CurrentVersion() != version || owned.Owner() != "node-2" {
		t.Fatalf("node-2 owns %+v as %q; want %+v", owned.CurrentVersion(), owned.Owner(), version)
	}
	if got, _ := os.ReadFile(owned.LocalPath()); !bytes.Equal(got, data) {
		t.Fatalf("node-2 materialized %d bytes; want %d", len(got), len(data))
	}
	if err := owned.Release(takeover); err != nil {
		t.Fatal(err)
	}

	// Stop everything and delete the System NATS state: no live cluster
	// metadata survives, only the nodes' Grove Files directories.
	for i := 1; i < len(cluster.nodes); i++ {
		if err := cluster.nodes[i].Kill(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, transport := range cluster.transports {
		transport.Close()
	}
	for _, node := range cluster.nodes {
		if err := os.RemoveAll(filepath.Join(node.TempDir(), "system-nats")); err != nil {
			t.Fatal(err)
		}
	}
	for i, node := range cluster.nodes {
		if err := node.Restart(); err != nil {
			t.Fatalf("restart %s: %v", cluster.nodeIDs[i], err)
		}
	}
	for i, node := range cluster.nodes {
		if err := node.WaitReady(ctx); err != nil {
			t.Fatalf("wait for %s: %v\n%s", cluster.nodeIDs[i], err, clusterLogs(cluster.nodes))
		}
		ready := readyEventFromLogs(t, node.Logs())
		transport, err := systemnats.Connect(ctx, ready.SystemNATSURL)
		if err != nil {
			t.Fatal(err)
		}
		cluster.transports[i] = transport
		t.Cleanup(transport.Close)
	}
	waitForFormedCluster(t, ctx, cluster)
	catalog := cluster.transports[2].FilesCatalog()
	waitUntil(t, ctx, cluster, "catalog rebuilt from disk with every node holding the version", func() bool {
		record, _, err := catalog.Get(ctx, files.FileIDFor(path))
		return err == nil && record.Current != nil && record.Current.Public() == version &&
			len(record.Holders(version.ID)) == 3
	})
	reader := grove.WithFileStore(ctx, cluster.transports[2].NewRemoteFileStore(ctx, "node-3", 0))
	opened, err := grove.Files(reader).Open(reader, path)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(opened.LocalPath()); !bytes.Equal(got, data) || opened.CurrentVersion() != version {
		t.Fatalf("after cold restart node-3 reads %d bytes at %s", len(got), opened.CurrentVersion().ID)
	}
}

func waitUntil(t *testing.T, ctx context.Context, cluster membershipGrovlets, what string, condition func() bool) {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for !condition() {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting until %s\n%s", what, clusterLogs(cluster.nodes))
		case <-ticker.C:
		}
	}
}

func filesCatalogReplicas(ctx context.Context, url string) int {
	connection, err := nats.Connect(url)
	if err != nil {
		return 0
	}
	defer connection.Close()
	js, err := jetstream.New(connection)
	if err != nil {
		return 0
	}
	stream, err := js.Stream(ctx, "KV_"+systemnats.FilesBucket)
	if err != nil {
		return 0
	}
	info, err := stream.Info(ctx)
	if err != nil {
		return 0
	}
	return info.Config.Replicas
}

// waitForFormedCluster waits until every node sees three healthy members and
// the bootstrap witness has handed its metadata vote back, so control state
// is settled before the test writes files.
func waitForFormedCluster(t *testing.T, ctx context.Context, cluster membershipGrovlets) {
	t.Helper()
	if _, err := waitForGrovletHealth(ctx, cluster, []int{0, 1, 2}, func(view systemnats.ClusterView) bool {
		if !view.Ready || len(view.Nodes) != 3 {
			return false
		}
		for _, node := range view.Nodes {
			if node.Health != systemnats.HealthHealthy {
				return false
			}
		}
		return true
	}); err != nil {
		t.Fatalf("cluster did not form: %v\n%s", err, clusterLogs(cluster.nodes))
	}
	waitForMetadataVoters(t, ctx, readyEventFromLogs(t, cluster.nodes[0].Logs()).SystemNATSURL, 3)
}
