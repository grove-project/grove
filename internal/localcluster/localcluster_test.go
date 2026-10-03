package localcluster_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/grove-project/grove/internal/localcluster"
)

func TestNodeSpecArgs(t *testing.T) {
	tests := []struct {
		name string
		spec localcluster.NodeSpec
		want []string
	}{
		{
			name: "fixed topology member hosting an ingress component",
			spec: localcluster.NodeSpec{
				NodeID: "node-1", Subject: "_GROVE.system.application.node-1",
				RouteListen: "127.0.0.1:4100", SeedRoute: "nats-route://127.0.0.1:4101",
				Membership: true, Recovery: true,
				Components: []localcluster.NodeComponent{
					{Kind: "web", Listen: "127.0.0.1:8080"},
					{Kind: "orders", Options: []string{"replicas=2"}},
				},
			},
			want: []string{
				"--node-id", "node-1",
				"--advertise-endpoint", "nats-subject://system/node-1",
				"--system-nats-listen", "127.0.0.1:0",
				"--system-nats-route-listen", "127.0.0.1:4100",
				"--system-nats-seed", "nats-route://127.0.0.1:4101",
				"--system-nats-membership", "--system-nats-recovery",
				"--system-nats-subject", "_GROVE.system.application.node-1",
				"--component", "web", "--component-listen", "web=127.0.0.1:8080",
				"--component", "orders", "--component-option", "orders=replicas=2",
			},
		},
		{
			name: "founder of a discovered cluster",
			spec: localcluster.NodeSpec{
				NodeID: "node-1", Subject: "s", Membership: true, Recovery: true, RetireOnStop: true,
				IngressAddress: "127.0.0.1:8080",
			},
			want: []string{
				"--node-id", "node-1",
				"--advertise-endpoint", "nats-subject://system/node-1",
				"--system-nats-listen", "127.0.0.1:0",
				"--system-nats-route-listen", "127.0.0.1:0",
				"--system-nats-membership", "--system-nats-recovery", "--system-nats-retire-on-stop",
				"--system-nats-subject", "s",
				"--ingress-address", "127.0.0.1:8080",
			},
		},
		{
			name: "client node of an existing System NATS with Delve",
			spec: localcluster.NodeSpec{
				NodeID: "node-2-candidate", Subject: "s", SystemNATSURL: "nats://127.0.0.1:4222",
				DelvePath: "/bin/dlv", Components: []localcluster.NodeComponent{{Kind: "inventory"}},
			},
			want: []string{
				"--node-id", "node-2-candidate",
				"--advertise-endpoint", "nats-subject://system/node-2-candidate",
				"--system-nats-url", "nats://127.0.0.1:4222",
				"--system-nats-subject", "s",
				"--delve-path", "/bin/dlv",
				"--component", "inventory",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.spec.Args(); !slices.Equal(got, test.want) {
				t.Errorf("Args() =\n%q\nwant\n%q", got, test.want)
			}
		})
	}
}

func TestNodeIDAndIndex(t *testing.T) {
	for index := range 12 {
		id := localcluster.NodeID(index)
		if got := localcluster.NodeIndex(id); got != index {
			t.Errorf("NodeIndex(%q) = %d; want %d", id, got, index)
		}
	}
	for _, id := range []string{"", "node-", "node-0", "node--1", "node-x", "worker-1", "node-2-candidate"} {
		if got := localcluster.NodeIndex(id); got != -1 {
			t.Errorf("NodeIndex(%q) = %d; want -1", id, got)
		}
	}
}

func TestStartRejectsInvalidTopologyBeforeLaunching(t *testing.T) {
	tests := map[string]localcluster.Spec{
		"no nodes":              {},
		"component off-cluster": {Nodes: 2, Components: []localcluster.Component{{NodeID: "node-3", Kind: "web"}}},
		"component on bad id":   {Nodes: 2, Components: []localcluster.Component{{NodeID: "edge", Kind: "web"}}},
	}
	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			// The binary does not exist, so reaching a launch would fail with
			// a different error.
			_, err := localcluster.Start(t.Context(), "/nonexistent/grovlet", spec)
			if !errors.Is(err, localcluster.ErrInvalidTopology) {
				t.Errorf("Start() error = %v; want %v", err, localcluster.ErrInvalidTopology)
			}
		})
	}
}

func TestEmptyClusterHasNoNodes(t *testing.T) {
	cluster := localcluster.New()
	if cluster.Len() != 0 || len(cluster.NodeIDs()) != 0 || cluster.SystemNATSURL() != "" {
		t.Errorf("New() = %d nodes, ids %v, url %q", cluster.Len(), cluster.NodeIDs(), cluster.SystemNATSURL())
	}
	if _, ok := cluster.Node("node-1"); ok {
		t.Error("Node(node-1) found in an empty cluster")
	}
	if err := cluster.Kill(t.Context(), "node-1"); !errors.Is(err, localcluster.ErrInvalidTopology) {
		t.Errorf("Kill() error = %v; want %v", err, localcluster.ErrInvalidTopology)
	}
	if err := cluster.Restart(t.Context(), ""); !errors.Is(err, localcluster.ErrInvalidTopology) {
		t.Errorf("Restart() error = %v; want %v", err, localcluster.ErrInvalidTopology)
	}
}

func TestReservePortsReturnsDistinctPorts(t *testing.T) {
	ports, err := localcluster.ReservePorts(4)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(ports)
	if len(slices.Compact(ports)) != 4 {
		t.Errorf("ReservePorts(4) = %v; want four distinct ports", ports)
	}
}
