package systemnats_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/grove-project/grove/internal/systemnats"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// TestFounderRestartKeepsRetiredWitnessOut restarts a whole cluster after the
// founder released its bootstrap witness. The founder must come back as one
// voter: restarting the witness would add a fourth metadata voter that has to
// be released again before placement can be served.
func TestFounderRestartKeepsRetiredWitnessOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	ports := reserveRoutePorts(t, systemnats.MembershipReplicas)
	configs := make([]systemnats.ClusterConfig, systemnats.MembershipReplicas)
	for i := range configs {
		configs[i] = systemnats.ClusterConfig{
			Name:              fmt.Sprintf("node-%d", i+1),
			Host:              "127.0.0.1",
			RouteHost:         "127.0.0.1",
			RoutePort:         ports[i],
			JetStreamStoreDir: t.TempDir(),
		}
		if i != 0 {
			configs[i].SeedURLs = []string{fmt.Sprintf("nats-route://127.0.0.1:%d", ports[0])}
		}
	}
	var servers []*systemnats.Server
	shutdown := func() {
		for i := len(servers) - 1; i >= 0; i-- {
			servers[i].Shutdown()
		}
		servers = nil
	}
	t.Cleanup(shutdown)

	for _, cfg := range configs {
		server, err := systemnats.StartClusterServer(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		servers = append(servers, server)
	}
	founder := servers[0]
	replicateControlState(t, ctx, founder)
	if !founder.HasPeer() {
		t.Fatal("founder has no bootstrap witness")
	}
	waitForVoters(t, ctx, founder, systemnats.MembershipReplicas+1)
	for !founder.ControlStateSettled(ctx) {
		if ctx.Err() != nil {
			t.Fatal("control state did not settle")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := founder.ReleaseWitness(ctx); err != nil {
		t.Fatalf("release witness: %v", err)
	}
	waitForVoters(t, ctx, founder, systemnats.MembershipReplicas)

	shutdown()
	results := make([]*systemnats.Server, len(configs))
	errs := make([]error, len(configs))
	var started sync.WaitGroup
	for i, cfg := range configs {
		started.Add(1)
		go func() {
			defer started.Done()
			results[i], errs[i] = systemnats.StartClusterServer(ctx, cfg)
		}()
	}
	started.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("restart %s: %v", configs[i].Name, err)
		}
		servers = append(servers, results[i])
	}
	founder = servers[0]
	if founder.HasPeer() {
		t.Fatal("restarted founder brought its retired witness back")
	}
	waitForVoters(t, ctx, founder, systemnats.MembershipReplicas)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, voters := founder.MetadataState(); voters > systemnats.MembershipReplicas {
			t.Fatalf("metadata voters = %d after restart; want %d (witness rejoined)", voters, systemnats.MembershipReplicas)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// replicateControlState creates every control bucket with one replica on
// each node, the precondition for a witness release.
func replicateControlState(t *testing.T, ctx context.Context, founder *systemnats.Server) {
	t.Helper()
	connection, err := nats.Connect(founder.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range []string{
		systemnats.MembershipBucket, systemnats.PlacementBucket, systemnats.DesiredBucket, systemnats.DeploymentBucket,
	} {
		// Retry until every node's JetStream has joined and can hold a replica.
		for {
			_, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{
				Bucket: bucket, Storage: jetstream.FileStorage, Replicas: systemnats.MembershipReplicas,
			})
			if err == nil {
				break
			}
			if ctx.Err() != nil {
				t.Fatalf("create %s control bucket: %v", bucket, err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func waitForVoters(t *testing.T, ctx context.Context, server *systemnats.Server, want int) {
	t.Helper()
	for {
		leader, voters := server.MetadataState()
		if leader != "" && voters == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("metadata leader %q with %d voters; want %d voters: %v", leader, voters, want, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
