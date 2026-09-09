package systemnats_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Three Grovlets observe the same explicit placements, and the Grove call path
// selects its destination from that replicated view rather than a fixed caller
// configuration.
func TestPlacementConvergesAndRoutes(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	servers, transports := startPlacementCluster(t, ctx)
	want := []systemnats.PlacementRecord{
		{
			ServiceID:         1,
			NodeID:            "node-a",
			InvocationSubject: "_GROVE.system.invoke.node-a",
		},
		{
			ServiceID:         2,
			NodeID:            "node-b",
			InvocationSubject: "_GROVE.system.invoke.node-b",
		},
	}
	placements := make([]*systemnats.Placement, len(transports))
	for i := range placements {
		var records []systemnats.PlacementRecord
		if i == 0 {
			records = want
		}
		placement, err := systemnats.NewPlacement(records)
		if err != nil {
			t.Fatal(err)
		}
		placements[i] = placement
		nodeID := fmt.Sprintf("node-%d", i+1)
		if err := transports[i].ServePlacement(ctx, nodeID, placement); err != nil {
			t.Fatal(err)
		}
		view, err := transports[i].RequestPlacement(ctx, nodeID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Ready || len(view.Placements) != 0 {
			t.Errorf("%s initial placement = %#v; want initializing empty view", nodeID, view)
		}
	}

	runCtx, cancelRun := context.WithCancel(t.Context())
	var runs sync.WaitGroup
	for i, placement := range placements {
		runs.Add(1)
		go func() {
			defer runs.Done()
			if err := placement.Run(runCtx, transports[i]); err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("placement node-%d: %v", i+1, err)
			}
		}()
	}
	t.Cleanup(func() {
		cancelRun()
		runs.Wait()
	})

	nodeIDs := []string{"node-1", "node-2", "node-3"}
	views, err := waitForPlacementViews(ctx, transports, nodeIDs, want)
	if err != nil {
		t.Fatal(err)
	}
	for i, view := range views {
		if view.Error != "" || !slices.Equal(view.Placements, want) {
			t.Errorf("%s placement = %#v; want %#v", nodeIDs[i], view, want)
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
	kv, err := js.KeyValue(ctx, systemnats.PlacementBucket)
	if err != nil {
		t.Fatal(err)
	}
	status, err := kv.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if replicas := status.Config().Replicas; replicas != systemnats.PlacementReplicas {
		t.Errorf("placement replicas = %d; want %d", replicas, systemnats.PlacementReplicas)
	}
	entry, err := kv.Get(ctx, systemnats.PlacementKey(2))
	if err != nil {
		t.Fatal(err)
	}
	var stored systemnats.PlacementRecord
	if err := json.Unmarshal(entry.Value(), &stored); err != nil {
		t.Fatal(err)
	}
	if stored != want[1] {
		t.Errorf("stored placement = %#v; want %#v", stored, want[1])
	}

	replacement := systemnats.PlacementRecord{
		ServiceID:         2,
		NodeID:            "node-c",
		InvocationSubject: "_GROVE.system.invoke.node-c",
	}
	observed, err := placements[2].Replace(ctx, transports[2], want[1], replacement)
	if err != nil {
		t.Fatal(err)
	}
	if observed != replacement {
		t.Errorf("replacement result = %#v; want %#v", observed, replacement)
	}
	replaced := []systemnats.PlacementRecord{want[0], replacement}
	if _, err := waitForPlacementViews(ctx, transports, nodeIDs, replaced); err != nil {
		t.Fatal(err)
	}
	observed, err = placements[1].Replace(ctx, transports[1], want[1], systemnats.PlacementRecord{
		ServiceID:         2,
		NodeID:            "node-d",
		InvocationSubject: "_GROVE.system.invoke.node-d",
	})
	if !errors.Is(err, systemnats.ErrPlacementChanged) {
		t.Errorf("stale replacement error = %v; want %v", err, systemnats.ErrPlacementChanged)
	}
	if observed != replacement {
		t.Errorf("stale replacement observed = %#v; want %#v", observed, replacement)
	}

	if err := transports[1].Serve(ctx, replacement.InvocationSubject, func(_ context.Context, request grove.RequestEnvelope) grove.ResponseEnvelope {
		return grove.ResponseEnvelope{Payload: request.Payload}
	}); err != nil {
		t.Fatal(err)
	}
	client, err := transports[0].PlacementClient(placements[0])
	if err != nil {
		t.Fatal(err)
	}
	got, err := grove.Call[string, string](ctx, client, 2, 1, "inventory")
	if err != nil {
		t.Fatal(err)
	}
	if got != "inventory" {
		t.Errorf("placement-routed Call() = %q; want inventory", got)
	}

	if _, err := placements[0].Lookup(3); !errors.Is(err, systemnats.ErrServiceNotPlaced) {
		t.Errorf("missing service lookup error = %v; want %v", err, systemnats.ErrServiceNotPlaced)
	}
	if key := systemnats.PlacementKey(2); key != "services.2" {
		t.Errorf("placement key = %q; want services.2", key)
	}
	if subject := systemnats.PlacementSubject("node-a"); subject != "_GROVE.system.placement.node-a" {
		t.Errorf("placement subject = %q; want _GROVE.system.placement.node-a", subject)
	}
}

func startPlacementCluster(t *testing.T, ctx context.Context) ([]*systemnats.Server, []*systemnats.Transport) {
	t.Helper()
	routePorts := reserveRoutePorts(t, systemnats.PlacementReplicas)
	configs := make([]systemnats.ClusterConfig, systemnats.PlacementReplicas)
	for i := range configs {
		seedIndex := 0
		if i == 0 {
			seedIndex = 1
		}
		configs[i] = systemnats.ClusterConfig{
			Name:              fmt.Sprintf("placement-node-%d", i+1),
			Host:              "127.0.0.1",
			RouteHost:         "127.0.0.1",
			RoutePort:         routePorts[i],
			SeedURLs:          []string{fmt.Sprintf("nats-route://127.0.0.1:%d", routePorts[seedIndex])},
			JetStreamStoreDir: t.TempDir(),
		}
	}
	type serverResult struct {
		index  int
		server *systemnats.Server
		err    error
	}
	results := make(chan serverResult, len(configs))
	var starts sync.WaitGroup
	for i, cfg := range configs {
		starts.Add(1)
		go func() {
			defer starts.Done()
			server, err := systemnats.StartClusterServer(ctx, cfg)
			results <- serverResult{index: i, server: server, err: err}
		}()
	}
	servers := make([]*systemnats.Server, len(configs))
	var startErr error
	for range configs {
		result := <-results
		if result.err != nil {
			startErr = errors.Join(startErr, result.err)
			continue
		}
		servers[result.index] = result.server
	}
	starts.Wait()
	if startErr != nil {
		for _, server := range servers {
			if server != nil {
				server.Shutdown()
			}
		}
		t.Fatal(startErr)
	}
	t.Cleanup(func() {
		for i := len(servers) - 1; i >= 0; i-- {
			servers[i].Shutdown()
		}
	})

	transports := make([]*systemnats.Transport, 0, len(servers))
	for _, server := range servers {
		transport, err := systemnats.Connect(ctx, server.URL())
		if err != nil {
			t.Fatal(err)
		}
		transports = append(transports, transport)
	}
	t.Cleanup(func() {
		for _, transport := range transports {
			transport.Close()
		}
	})
	return servers, transports
}

func waitForPlacementViews(
	ctx context.Context,
	transports []*systemnats.Transport,
	nodeIDs []string,
	want []systemnats.PlacementRecord,
) ([]systemnats.PlacementView, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	views := make([]systemnats.PlacementView, len(nodeIDs))
	var lastErr error
	for {
		converged := true
		for i, nodeID := range nodeIDs {
			requestCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
			view, err := transports[i].RequestPlacement(requestCtx, nodeID)
			cancel()
			if err != nil {
				lastErr = err
				converged = false
				continue
			}
			views[i] = view
			if !view.Ready || !slices.Equal(view.Placements, want) {
				converged = false
			}
		}
		if converged {
			return views, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("placement did not converge: views=%#v: %w", views, errors.Join(lastErr, err))
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil, fmt.Errorf("placement did not converge: views=%#v: %w", views, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func TestNewPlacementRejectsInvalidRecords(t *testing.T) {
	invalid := []systemnats.PlacementRecord{
		{NodeID: "node-a", InvocationSubject: "subject"},
		{ServiceID: 1, InvocationSubject: "subject"},
		{ServiceID: 1, NodeID: "node-a"},
	}
	for _, record := range invalid {
		if _, err := systemnats.NewPlacement([]systemnats.PlacementRecord{record}); !errors.Is(err, systemnats.ErrPlacementRecordInvalid) {
			t.Errorf("NewPlacement(%#v) error = %v; want %v", record, err, systemnats.ErrPlacementRecordInvalid)
		}
	}
	if _, err := systemnats.NewPlacement([]systemnats.PlacementRecord{
		{ServiceID: 1, NodeID: "node-a", InvocationSubject: "subject-a"},
		{ServiceID: 1, NodeID: "node-b", InvocationSubject: "subject-b"},
	}); !errors.Is(err, systemnats.ErrPlacementRecordInvalid) {
		t.Errorf("duplicate placement error = %v; want %v", err, systemnats.ErrPlacementRecordInvalid)
	}

	placement, err := systemnats.NewPlacement(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := placement.Lookup(1); !errors.Is(err, systemnats.ErrPlacementUnavailable) {
		t.Errorf("initial lookup error = %v; want %v", err, systemnats.ErrPlacementUnavailable)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	server, err := systemnats.StartServer(ctx, "127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Shutdown)
	transport, err := systemnats.Connect(ctx, server.URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.Close)
	if err := transport.ServePlacement(ctx, "node-a", nil); !errors.Is(err, systemnats.ErrPlacementRequired) {
		t.Errorf("nil placement endpoint error = %v; want %v", err, systemnats.ErrPlacementRequired)
	}
	if _, err := transport.PlacementClient(nil); !errors.Is(err, systemnats.ErrPlacementRequired) {
		t.Errorf("nil placement client error = %v; want %v", err, systemnats.ErrPlacementRequired)
	}
}
