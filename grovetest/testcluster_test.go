package grovetest_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/grovetest"
)

var (
	ordersCreate = grovetest.HandlerID{Service: 1, Method: 1}
	loadGenRun   = grovetest.HandlerID{Service: 9, Method: 1}
)

// registerWhoAmI registers a handler that answers with the ID of the node executing it.
func registerWhoAmI(n *grovetest.TestNode, id grovetest.HandlerID, opts ...grovetest.HandlerOption) {
	n.Register(id, func(context.Context, []byte) ([]byte, error) {
		return grove.Encode(n.ID())
	}, opts...)
}

func whoAmI(t *testing.T, from *grovetest.TestNode, id grovetest.HandlerID) (string, error) {
	t.Helper()
	return grove.Call[struct{}, string](t.Context(), from.Client(), id.Service, id.Method, struct{}{})
}

func TestTestClusterTopologyAndKillRestart(t *testing.T) {
	cluster := grovetest.NewTestCluster(t)

	n1 := cluster.AddNode()
	registerWhoAmI(n1, ordersCreate)
	cluster.Start()
	cluster.AssertPlacements(ordersCreate, n1)

	n2, n3 := cluster.AddNode(), cluster.AddNode()
	registerWhoAmI(n2, ordersCreate)
	registerWhoAmI(n3, ordersCreate)
	cluster.Converge()
	cluster.AssertPlacements(ordersCreate, n1, n2, n3)
	if got := len(cluster.ActiveNodes()); got != 3 {
		t.Fatalf("active nodes = %d, want 3", got)
	}

	// Calls traverse the real Client -> Router -> Dispatcher -> Registry path
	// and spread across every placement.
	for range 6 {
		if _, err := whoAmI(t, n1, ordersCreate); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []*grovetest.TestNode{n1, n2, n3} {
		if got := cluster.Calls(ordersCreate)[n.ID()]; got != 2 {
			t.Fatalf("%s served %d calls, want 2: %v", n.ID(), got, cluster.Calls(ordersCreate))
		}
	}

	cluster.KillNode(n1)
	before := cluster.Clock().Now()
	// Until detection, peers keep stale placements and calls to the dead node
	// fail as transport failures.
	var sawTransport bool
	for range 3 {
		if _, err := whoAmI(t, n2, ordersCreate); errors.Is(err, grove.ErrTransportFailure) {
			sawTransport = true
		}
	}
	if !sawTransport {
		t.Fatal("expected a transport failure routing to crashed node before detection")
	}
	cluster.Converge()
	if !cluster.Clock().Now().After(before) {
		t.Fatal("failure detection did not advance the test clock")
	}
	cluster.AssertPlacements(ordersCreate, n2, n3)
	for range 4 {
		if _, err := whoAmI(t, n2, ordersCreate); err != nil {
			t.Fatalf("after reconciliation: %v", err)
		}
	}

	cluster.RestartNode(n1)
	cluster.Converge()
	cluster.AssertPlacements(ordersCreate, n1, n2, n3)
	if got, err := whoAmI(t, n1, ordersCreate); err != nil || got == "" {
		t.Fatalf("restarted node call = %q, %v", got, err)
	}
}

func TestTestClusterGracefulStop(t *testing.T) {
	cluster := grovetest.NewTestCluster(t)
	n1, n2 := cluster.AddNode(), cluster.AddNode()
	registerWhoAmI(n1, ordersCreate)
	registerWhoAmI(n2, ordersCreate)
	cluster.Start()

	cluster.StopNode(n1)
	cluster.Converge()
	cluster.AssertPlacements(ordersCreate, n2)
	if n1.State() != grovetest.NodeStopped {
		t.Fatalf("state = %s", n1.State())
	}
}

func TestTestClusterExclusiveOwnerFailover(t *testing.T) {
	cluster := grovetest.NewTestCluster(t)
	nodes := []*grovetest.TestNode{cluster.AddNode(), cluster.AddNode(), cluster.AddNode()}
	for _, n := range nodes {
		registerWhoAmI(n, loadGenRun, grovetest.Exclusive())
	}
	cluster.Start()
	cluster.AssertSingleOwner(loadGenRun)

	owner := cluster.Owner(loadGenRun)
	cluster.KillNode(owner)
	cluster.Converge()

	newOwner := cluster.Owner(loadGenRun)
	if newOwner == owner {
		t.Fatalf("owner %s did not change after kill", owner.ID())
	}
	cluster.AssertSingleOwner(loadGenRun)

	// The old owner rejoining must not steal or duplicate ownership.
	cluster.RestartNode(owner)
	cluster.Converge()
	cluster.AssertPlacements(loadGenRun, newOwner)
	cluster.AssertSingleOwner(loadGenRun)
}

func TestTestClusterPartition(t *testing.T) {
	cluster := grovetest.NewTestCluster(t)
	n1, n2 := cluster.AddNode(), cluster.AddNode()
	registerWhoAmI(n1, ordersCreate)
	registerWhoAmI(n2, ordersCreate)
	cluster.Start()

	cluster.Partition(n1, n2)
	var failed bool
	for range 2 {
		if _, err := whoAmI(t, n1, ordersCreate); errors.Is(err, grovetest.ErrNodeUnreachable) {
			failed = true
		}
	}
	if !failed {
		t.Fatal("expected calls across the partition to fail")
	}
	cluster.Heal()
	for range 2 {
		if _, err := whoAmI(t, n1, ordersCreate); err != nil {
			t.Fatalf("after heal: %v", err)
		}
	}
}

func TestTestClusterUnplacedHandler(t *testing.T) {
	cluster := grovetest.NewTestCluster(t)
	n1 := cluster.AddNode()
	cluster.Start()
	_, err := whoAmI(t, n1, ordersCreate)
	if !errors.Is(err, grovetest.ErrHandlerNotPlaced) {
		t.Fatalf("err = %v, want ErrHandlerNotPlaced", err)
	}
}

func TestTestClusterCustomPolicyRunsThroughRealRouting(t *testing.T) {
	// A policy that pins every handler to the highest node ID shows the policy
	// seam is what decides routing.
	pinLast := grovetest.PlacementPolicyFunc(func(top grovetest.Topology) map[grovetest.HandlerID][]string {
		out := make(map[grovetest.HandlerID][]string)
		for _, node := range top.Nodes {
			for id := range node.Handlers {
				out[id] = []string{node.ID}
			}
		}
		return out
	})
	cluster := grovetest.NewTestCluster(t, grovetest.WithPlacementPolicy(pinLast))
	n1, n2 := cluster.AddNode(), cluster.AddNode()
	registerWhoAmI(n1, ordersCreate)
	registerWhoAmI(n2, ordersCreate)
	cluster.Start()
	cluster.AssertPlacements(ordersCreate, n2)
	if got, err := whoAmI(t, n1, ordersCreate); err != nil || got != n2.ID() {
		t.Fatalf("got %q, %v; want %s", got, err, n2.ID())
	}
}

func TestTestClusterDiagnostics(t *testing.T) {
	cluster := grovetest.NewTestCluster(t)
	n1 := cluster.AddNode()
	registerWhoAmI(n1, ordersCreate)
	cluster.Start()
	cluster.KillNode(n1)
	cluster.Converge()
	d := cluster.Diagnostics()
	for _, want := range []string{"node-1 state=crashed", "placements:", "failure detected"} {
		if !strings.Contains(d, want) {
			t.Fatalf("diagnostics missing %q:\n%s", want, d)
		}
	}
}

// Repeated clusters must not leak goroutines and must run quickly.
func TestTestClusterRepeatedRunsDoNotLeak(t *testing.T) {
	runtime.GC()
	before := runtime.NumGoroutine()
	started := time.Now()
	for range 200 {
		t.Run("", func(t *testing.T) {
			cluster := grovetest.NewTestCluster(t)
			n1, n2 := cluster.AddNode(), cluster.AddNode()
			registerWhoAmI(n1, ordersCreate)
			registerWhoAmI(n2, ordersCreate)
			cluster.Start()
			cluster.KillNode(n1)
			cluster.Converge()
			cluster.RestartNode(n1)
			cluster.Converge()
			cluster.AssertPlacements(ordersCreate, n1, n2)
		})
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("200 scenarios took %s", elapsed)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines grew from %d to %d", before, after)
	}
}
