package systemnats_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/grove-project/grove/internal/systemnats"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Three observers build the same deterministic local view from one
// three-replica authoritative membership bucket.
func TestMembershipConverges(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	servers := make([]*systemnats.Server, 0, systemnats.MembershipReplicas)
	transports := make([]*systemnats.Transport, 0, systemnats.MembershipReplicas)
	memberships := make([]*systemnats.Membership, 0, systemnats.MembershipReplicas)
	nodeIDs := make([]string, 0, systemnats.MembershipReplicas)

	for i := range systemnats.MembershipReplicas {
		cfg := systemnats.ClusterConfig{
			Name:              fmt.Sprintf("node-%d", i+1),
			Host:              "127.0.0.1",
			RouteHost:         "127.0.0.1",
			JetStreamStoreDir: t.TempDir(),
		}
		if i != 0 {
			cfg.SeedURLs = []string{servers[0].RouteURL()}
		}
		started, err := systemnats.StartClusterServer(ctx, cfg)
		if err != nil {
			for _, server := range servers {
				server.Shutdown()
			}
			t.Fatal(err)
		}
		servers = append(servers, started)
	}
	t.Cleanup(func() {
		for i := len(servers) - 1; i >= 0; i-- {
			servers[i].Shutdown()
		}
	})
	setupConnection, err := nats.Connect(servers[0].URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(setupConnection.Close)
	setupJS, err := jetstream.New(setupConnection)
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range []string{
		systemnats.PlacementBucket,
		systemnats.DesiredBucket,
		systemnats.DeploymentBucket,
	} {
		if _, err := setupJS.CreateKeyValue(ctx, jetstream.KeyValueConfig{
			Bucket: bucket, Storage: jetstream.FileStorage, Replicas: 1,
		}); err != nil {
			t.Fatalf("create %s control bucket: %v", bucket, err)
		}
	}

	for i, server := range servers {
		transport, err := systemnats.Connect(ctx, server.URL())
		if err != nil {
			t.Fatal(err)
		}
		transports = append(transports, transport)
		nodeID := fmt.Sprintf("node-%d", i+1)
		nodeIDs = append(nodeIDs, nodeID)
		membership, err := systemnats.NewMembership(systemnats.MembershipRecord{
			NodeID:             nodeID,
			AdvertisedEndpoint: "nats-subject://system/" + nodeID,
		})
		if err != nil {
			t.Fatal(err)
		}
		memberships = append(memberships, membership)
		if err := transport.ServeMembership(ctx, nodeID, membership); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, transport := range transports {
			transport.Close()
		}
	})

	for i, transport := range transports {
		view, err := transport.RequestMembership(ctx, nodeIDs[i])
		if err != nil {
			t.Fatal(err)
		}
		if view.Ready || len(view.Members) != 0 {
			t.Errorf("%s initial membership = %#v; want initializing empty view", nodeIDs[i], view)
		}
	}

	runCtx, cancelRun := context.WithCancel(t.Context())
	var runs sync.WaitGroup
	startObserver := func(i int) {
		membership := memberships[i]
		runs.Add(1)
		go func() {
			defer runs.Done()
			if err := membership.Run(runCtx, transports[i]); err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("membership %s: %v", nodeIDs[i], err)
			}
		}()
	}
	t.Cleanup(func() {
		cancelRun()
		runs.Wait()
	})

	want := []systemnats.MembershipRecord{
		{NodeID: "node-1", AdvertisedEndpoint: "nats-subject://system/node-1"},
		{NodeID: "node-2", AdvertisedEndpoint: "nats-subject://system/node-2"},
		{NodeID: "node-3", AdvertisedEndpoint: "nats-subject://system/node-3"},
	}
	var views []systemnats.MembershipView
	for i := range memberships {
		startObserver(i)
		currentViews, err := waitForMembershipViews(
			ctx,
			transports[:i+1],
			nodeIDs[:i+1],
			want[:i+1],
		)
		if err != nil {
			t.Fatal(err)
		}
		views = currentViews
		for _, view := range views {
			if !slices.Equal(view.Members, want[:i+1]) {
				t.Errorf("membership after starting %s = %#v; want %#v", nodeIDs[i], view.Members, want[:i+1])
			}
		}
		for _, bucket := range []string{
			systemnats.MembershipBucket,
			systemnats.PlacementBucket,
			systemnats.DesiredBucket,
			systemnats.DeploymentBucket,
		} {
			if err := waitForBucketReplicas(ctx, servers[0].URL(), bucket, i+1); err != nil {
				t.Fatalf("%s replicas after starting %s: %v", bucket, nodeIDs[i], err)
			}
		}
	}
	for i, view := range views {
		if !slices.Equal(view.Members, want) {
			t.Errorf("%s membership = %#v; want %#v", nodeIDs[i], view.Members, want)
		}
	}

	connection, err := nats.Connect(servers[0].URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	kv, err := js.KeyValue(ctx, systemnats.MembershipBucket)
	if err != nil {
		t.Fatal(err)
	}
	status, err := kv.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if replicas := status.Config().Replicas; replicas != systemnats.MembershipReplicas {
		t.Errorf("membership replicas = %d; want %d", replicas, systemnats.MembershipReplicas)
	}
	if key := systemnats.MembershipKey("node-1"); key != "nodes.node-1" {
		t.Errorf("membership key = %q; want nodes.node-1", key)
	}
	if subject := systemnats.MembershipSubject("node-1"); subject != "_GROVE.system.membership.node-1" {
		t.Errorf("membership subject = %q; want _GROVE.system.membership.node-1", subject)
	}

	if _, err := systemnats.NewMembership(systemnats.MembershipRecord{NodeID: "incomplete"}); !errors.Is(err, systemnats.ErrMembershipRecordInvalid) {
		t.Errorf("incomplete membership error = %v; want %v", err, systemnats.ErrMembershipRecordInvalid)
	}
	if err := transports[0].ServeMembership(ctx, "node", nil); !errors.Is(err, systemnats.ErrMembershipRequired) {
		t.Errorf("nil membership endpoint error = %v; want %v", err, systemnats.ErrMembershipRequired)
	}

	// Retire the bootstrap logical node first. Its paired metadata peer must be
	// evacuated without making the surviving membership observers unavailable.
	if err := memberships[0].BeginLeave(ctx, transports[0]); err != nil {
		t.Fatal(err)
	}
	if err := servers[1].EnsurePeer(ctx); err != nil {
		t.Fatalf("transfer internal metadata witness to node-2: %v", err)
	}
	if err := servers[0].PrepareRetire(ctx); err != nil {
		t.Fatal(err)
	}
	for _, bucket := range []string{
		systemnats.MembershipBucket,
		systemnats.PlacementBucket,
		systemnats.DesiredBucket,
		systemnats.DeploymentBucket,
	} {
		if err := waitForBucketReplicas(ctx, servers[1].URL(), bucket, 2); err != nil {
			t.Fatalf("%s replicas after node-1 began graceful leave: %v", bucket, err)
		}
	}
	if allLeaving, err := memberships[0].AllLeaving(ctx, transports[0], want); err != nil {
		t.Fatal(err)
	} else if allLeaving {
		t.Fatal("one leaving node reported whole-cluster shutdown")
	}
	if err := memberships[0].Leave(ctx, transports[0]); err != nil {
		t.Fatal(err)
	}
	wantAfterLeave := []systemnats.MembershipRecord{want[1], want[2]}
	survivorTransports := []*systemnats.Transport{transports[1], transports[2]}
	survivorNodeIDs := []string{nodeIDs[1], nodeIDs[2]}
	views, err = waitForMembershipViews(ctx, survivorTransports, survivorNodeIDs, wantAfterLeave)
	if err != nil {
		t.Fatal(err)
	}
	for i, view := range views {
		if !slices.Equal(view.Members, wantAfterLeave) {
			t.Errorf("%s membership after node-1 leave = %#v; want %#v", survivorNodeIDs[i], view.Members, wantAfterLeave)
		}
	}
	if err := servers[0].Retire(ctx); err != nil {
		t.Fatal(err)
	}
	servers[0].Shutdown()

	if err := memberships[2].BeginLeave(ctx, transports[2]); err != nil {
		t.Fatal(err)
	}
	if err := servers[2].PrepareRetire(ctx); err != nil {
		t.Fatal(err)
	}
	for _, bucket := range []string{
		systemnats.MembershipBucket,
		systemnats.PlacementBucket,
		systemnats.DesiredBucket,
		systemnats.DeploymentBucket,
	} {
		if err := waitForBucketReplicas(ctx, servers[1].URL(), bucket, 1); err != nil {
			t.Fatalf("%s replicas after node-3 began final graceful leave: %v", bucket, err)
		}
	}
	if err := memberships[2].Leave(ctx, transports[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := waitForMembershipViews(ctx, transports[1:2], nodeIDs[1:2], want[1:2]); err != nil {
		t.Fatal(err)
	}
	if err := servers[2].Retire(ctx); err != nil {
		t.Fatal(err)
	}
	servers[2].Shutdown()
	for _, bucket := range []string{
		systemnats.MembershipBucket,
		systemnats.PlacementBucket,
		systemnats.DesiredBucket,
		systemnats.DeploymentBucket,
	} {
		if err := waitForBucketReplicas(ctx, servers[1].URL(), bucket, 1); err != nil {
			t.Fatalf("%s unavailable after node-3 retired: %v", bucket, err)
		}
	}
}

func waitForBucketReplicas(ctx context.Context, serverURL, bucket string, want int) error {
	connection, err := nats.Connect(serverURL)
	if err != nil {
		return err
	}
	defer connection.Close()
	js, err := jetstream.New(connection)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var last int
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		kv, err := js.KeyValue(attemptCtx, bucket)
		if err == nil {
			status, statusErr := kv.Status(attemptCtx)
			if statusErr == nil {
				last = status.Config().Replicas
				if last == want {
					return nil
				}
			}
			if statusErr != nil {
				lastErr = statusErr
			}
		} else {
			lastErr = err
		}
		cancel()
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("bucket %s replicas = %d, error = %v, want %d: %w", bucket, last, lastErr, want, ctx.Err())
		}
	}
}

func reserveRoutePorts(t *testing.T, count int) []int {
	t.Helper()
	listeners := make([]net.Listener, 0, count)
	defer func() {
		for _, listener := range listeners {
			if err := listener.Close(); err != nil {
				t.Errorf("close route-port reservation: %v", err)
			}
		}
	}()
	ports := make([]int, 0, count)
	for range count {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, listener)
		_, portText, err := net.SplitHostPort(listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			t.Fatal(err)
		}
		ports = append(ports, port)
	}
	return ports
}

func waitForMembershipViews(
	ctx context.Context,
	transports []*systemnats.Transport,
	nodeIDs []string,
	want []systemnats.MembershipRecord,
) ([]systemnats.MembershipView, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	views := make([]systemnats.MembershipView, len(nodeIDs))
	var lastErr error
	for {
		converged := true
		for i, nodeID := range nodeIDs {
			requestCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
			view, err := transports[i].RequestMembership(requestCtx, nodeID)
			cancel()
			if err != nil {
				lastErr = err
				converged = false
				continue
			}
			views[i] = view
			if !view.Ready || !slices.Equal(view.Members, want) {
				converged = false
			}
		}
		if converged {
			return views, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("membership did not converge: views=%#v: %w", views, errors.Join(lastErr, err))
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil, fmt.Errorf("membership did not converge: views=%#v: %w", views, errors.Join(lastErr, ctx.Err()))
		}
	}
}
