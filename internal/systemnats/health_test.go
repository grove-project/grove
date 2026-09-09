package systemnats

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// A surviving observer retains durable membership while an expired heartbeat
// changes only the dead node's derived health.
func TestHealthRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	server, err := StartServer(ctx, "127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Shutdown)
	transportA, err := Connect(ctx, server.URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transportA.Close)
	transportB, err := Connect(ctx, server.URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transportB.Close)
	query, err := Connect(ctx, server.URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(query.Close)

	membership := &Membership{view: MembershipView{
		Ready: true,
		Members: []MembershipRecord{
			{NodeID: "node-b", AdvertisedEndpoint: "nats-subject://system/node-b"},
			{NodeID: "node-a", AdvertisedEndpoint: "nats-subject://system/node-a"},
		},
	}}
	config := HealthConfig{
		HeartbeatInterval: 10 * time.Millisecond,
		UnavailableAfter:  60 * time.Millisecond,
	}
	healthA, err := NewHealth("node-a", membership, config)
	if err != nil {
		t.Fatal(err)
	}
	healthB, err := NewHealth("node-b", membership, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := transportA.ServeClusterView(ctx, "node-a", healthA); err != nil {
		t.Fatal(err)
	}
	initial, err := query.RequestClusterView(ctx, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if initial.Ready || len(initial.Nodes) != 0 {
		t.Errorf("initial cluster view = %#v; want initializing empty view", initial)
	}

	runCtx, cancelRun := context.WithCancel(t.Context())
	nodeBCtx, cancelNodeB := context.WithCancel(runCtx)
	var runs sync.WaitGroup
	runHealth := func(name string, health *Health, transport *Transport, healthCtx context.Context) {
		runs.Add(1)
		go func() {
			defer runs.Done()
			if err := health.Run(healthCtx, transport); err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("%s health: %v", name, err)
			}
		}()
	}
	runHealth("node-a", healthA, transportA, runCtx)
	runHealth("node-b", healthB, transportB, nodeBCtx)
	t.Cleanup(func() {
		cancelRun()
		runs.Wait()
	})

	healthy, err := waitForClusterView(ctx, query, "node-a", func(view ClusterView) bool {
		return view.Ready &&
			len(view.Nodes) == 2 &&
			view.Nodes[0].NodeID == "node-a" &&
			view.Nodes[0].Health == HealthHealthy &&
			view.Nodes[1].NodeID == "node-b" &&
			view.Nodes[1].Health == HealthHealthy
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range healthy.Nodes {
		if _, err := time.Parse(time.RFC3339Nano, node.LastSeen); err != nil {
			t.Errorf("%s last seen = %q: %v", node.NodeID, node.LastSeen, err)
		}
	}

	cancelNodeB()
	unavailable, err := waitForClusterView(ctx, query, "node-a", func(view ClusterView) bool {
		return view.Ready &&
			len(view.Nodes) == 2 &&
			view.Nodes[0].Health == HealthHealthy &&
			view.Nodes[1].Health == HealthUnavailable
	})
	if err != nil {
		t.Fatal(err)
	}
	if unavailable.Nodes[1].AdvertisedEndpoint != "nats-subject://system/node-b" {
		t.Errorf("unavailable node endpoint = %q; want node-b endpoint", unavailable.Nodes[1].AdvertisedEndpoint)
	}
	if subject := ClusterSubject("node-a"); subject != "_GROVE.system.cluster.node-a" {
		t.Errorf("cluster subject = %q; want _GROVE.system.cluster.node-a", subject)
	}

	if _, err := NewHealth("", membership, HealthConfig{}); !errors.Is(err, ErrHealthNodeIDRequired) {
		t.Errorf("empty node health error = %v; want %v", err, ErrHealthNodeIDRequired)
	}
	if _, err := NewHealth("node", nil, HealthConfig{}); !errors.Is(err, ErrHealthMembershipRequired) {
		t.Errorf("nil membership health error = %v; want %v", err, ErrHealthMembershipRequired)
	}
	if _, err := NewHealth("node", membership, HealthConfig{HeartbeatInterval: -1}); !errors.Is(err, ErrHealthConfigInvalid) {
		t.Errorf("invalid health timing error = %v; want %v", err, ErrHealthConfigInvalid)
	}
	if err := transportA.ServeClusterView(ctx, "node", nil); !errors.Is(err, ErrHealthRequired) {
		t.Errorf("nil cluster view error = %v; want %v", err, ErrHealthRequired)
	}
}

func waitForClusterView(
	ctx context.Context,
	transport *Transport,
	nodeID string,
	condition func(ClusterView) bool,
) (ClusterView, error) {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	var lastView ClusterView
	var lastErr error
	for {
		requestCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		view, err := transport.RequestClusterView(requestCtx, nodeID)
		cancel()
		if err == nil {
			lastView = view
			if condition(view) {
				return view, nil
			}
		} else {
			lastErr = err
		}
		if err := ctx.Err(); err != nil {
			return ClusterView{}, fmt.Errorf("cluster view condition failed: view=%#v: %w", lastView, errors.Join(lastErr, err))
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ClusterView{}, fmt.Errorf("cluster view condition failed: view=%#v: %w", lastView, errors.Join(lastErr, ctx.Err()))
		}
	}
}
