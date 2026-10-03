package inspect_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/controlplane"
	"github.com/grove-project/grove/internal/inspect"
)

const (
	orders    grove.ServiceID = 1
	inventory grove.ServiceID = 2

	currentDigest   = "sha256:current"
	candidateDigest = "sha256:candidate"
)

var errNoResponders = errors.New("no responders")

// fixture is a fixed control-plane state. It has no write methods: an
// Inspector can only read it.
type fixture struct {
	cluster     controlplane.ClusterView
	placement   controlplane.PlacementView
	deployments controlplane.DeploymentView
	components  map[string]controlplane.ComponentView
	handlers    map[string]controlplane.HandlerPlacementView
	down        map[string]bool

	mu    sync.Mutex
	calls []string
}

func (f *fixture) record(call, nodeID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call+" "+nodeID)
	if f.down[nodeID] {
		return errNoResponders
	}
	return nil
}

func (f *fixture) RequestClusterView(_ context.Context, nodeID string) (controlplane.ClusterView, error) {
	return f.cluster, f.record("cluster", nodeID)
}

func (f *fixture) RequestPlacement(_ context.Context, nodeID string) (controlplane.PlacementView, error) {
	return f.placement, f.record("placement", nodeID)
}

func (f *fixture) RequestDeployments(_ context.Context, nodeID string) (controlplane.DeploymentView, error) {
	return f.deployments, f.record("deployments", nodeID)
}

func (f *fixture) RequestComponents(_ context.Context, nodeID string) (controlplane.ComponentView, error) {
	if err := f.record("components", nodeID); err != nil {
		return controlplane.ComponentView{}, err
	}
	return f.components[nodeID], nil
}

func (f *fixture) RequestHandlerPlacement(_ context.Context, nodeID string) (controlplane.HandlerPlacementView, error) {
	if err := f.record("handlers", nodeID); err != nil {
		return controlplane.HandlerPlacementView{}, err
	}
	return f.handlers[nodeID], nil
}

func shopFixture() *fixture {
	return &fixture{
		cluster: controlplane.ClusterView{Ready: true, Nodes: []controlplane.ClusterNode{
			{NodeID: "node-1", Health: controlplane.HealthHealthy, AdvertisedEndpoint: "nats-subject://system/node-1"},
			{NodeID: "node-2", Health: controlplane.HealthHealthy},
			{NodeID: "node-3", Health: controlplane.HealthHealthy},
		}},
		placement: controlplane.PlacementView{Ready: true, Placements: []controlplane.PlacementRecord{
			{ServiceID: orders, NodeID: "node-1", InvocationSubject: "orders", ArtifactDigest: currentDigest},
			{ServiceID: inventory, NodeID: "node-2", InvocationSubject: "inventory", ArtifactDigest: currentDigest},
		}},
		components: map[string]controlplane.ComponentView{
			"node-1": {Components: []controlplane.ComponentStatus{
				{ServiceID: orders, Name: "Orders", InvocationSubject: "orders", Generation: 1, State: controlplane.ComponentHealthy},
				{ServiceID: inventory, Name: "Inventory", InvocationSubject: "fallback-inventory", State: controlplane.ComponentStopped},
			}},
			"node-2": {Components: []controlplane.ComponentStatus{
				{ServiceID: orders, Name: "Orders", InvocationSubject: "fallback-orders", State: controlplane.ComponentStopped},
				{ServiceID: inventory, Name: "Inventory", InvocationSubject: "inventory", State: controlplane.ComponentHealthy},
			}},
			"node-3": {Components: []controlplane.ComponentStatus{
				{ServiceID: orders, Name: "Orders", InvocationSubject: "observer-orders", State: controlplane.ComponentStopped},
			}},
		},
		deployments: controlplane.DeploymentView{
			Ready: true,
			Artifacts: []controlplane.DeploymentArtifact{
				{ApplicationID: "grove-shop", CodeVersion: "v1", ArtifactDigest: currentDigest, ConfigRevision: "acme-r42", ConfigDigest: "sha256:config-a"},
				{ApplicationID: "grove-shop", CodeVersion: "v1", ArtifactDigest: candidateDigest, ConfigRevision: "acme-broken-r43", ConfigDigest: "sha256:config-b"},
			},
			Rollouts: []controlplane.Rollout{
				{ApplicationID: "other-app", Generation: 9, Phase: controlplane.RolloutActive},
				{
					ApplicationID: "grove-shop", Generation: 2, CurrentArtifactDigest: currentDigest,
					CandidateArtifactDigest: candidateDigest, Phase: controlplane.RolloutPending,
				},
			},
		},
	}
}

func shop() inspect.Application {
	return inspect.Application{
		ID:       "grove-shop",
		Artifact: inspect.Artifact{ApplicationID: "grove-shop", ArtifactDigest: "sha256:local"},
		ServiceName: func(id grove.ServiceID) string {
			if id == orders {
				return "Orders"
			}
			return ""
		},
	}
}

func TestStatusUsesPlacedComponentHealth(t *testing.T) {
	status, err := inspect.New(shopFixture(), shop(), "node-1").Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Ready || status.Health != "healthy" {
		t.Fatalf("status readiness = %t, health = %q; want ready and healthy: %#v", status.Ready, status.Health, status)
	}
	if len(status.Placements) != 2 || status.Placements[0].Health != "healthy" || status.Placements[1].Health != "healthy" {
		t.Errorf("placed component health = %#v", status.Placements)
	}
	if status.Placements[0].Name != "Orders" || status.Placements[1].Name != "Service 2" {
		t.Errorf("placement names = %q, %q", status.Placements[0].Name, status.Placements[1].Name)
	}
	if status.ActiveArtifact == nil || status.ActiveArtifact.ConfigRevision != "acme-r42" || status.CandidateArtifact == nil || status.CandidateArtifact.ConfigRevision != "acme-broken-r43" {
		t.Errorf("artifact identities = active %#v candidate %#v", status.ActiveArtifact, status.CandidateArtifact)
	}
	if status.Rollout == nil || status.Rollout.Phase != string(controlplane.RolloutPending) || status.Rollout.Generation != 2 {
		t.Errorf("rollout = %#v", status.Rollout)
	}
	if node := status.Nodes[0]; node.Endpoint != "nats-subject://system/node-1" || len(node.Components) != 2 || node.Components[0].Generation != 1 {
		t.Errorf("node-1 = %#v", node)
	}
}

func TestStatusDegradesWhenAPlacedComponentIsNotServing(t *testing.T) {
	state := shopFixture()
	state.components["node-2"].Components[1].State = controlplane.ComponentFailed
	status, err := inspect.New(state, shop(), "node-1").Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Health != "degraded" || status.Placements[1].Health != string(controlplane.ComponentFailed) {
		t.Errorf("status = %q, inventory placement = %#v", status.Health, status.Placements[1])
	}
	state.components["node-2"] = controlplane.ComponentView{}
	status, _ = inspect.New(state, shop(), "node-1").Status(t.Context())
	if status.Health != "degraded" || status.Placements[1].Health != "unavailable" {
		t.Errorf("status = %q, inventory placement = %#v", status.Health, status.Placements[1])
	}
}

func TestStatusWithoutRolloutReportsTheApplicationArtifact(t *testing.T) {
	state := shopFixture()
	state.deployments.Rollouts = state.deployments.Rollouts[:1]
	status, err := inspect.New(state, shop(), "node-1").Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Rollout != nil || status.ActiveArtifact == nil || status.ActiveArtifact.ArtifactDigest != "sha256:local" || status.CandidateArtifact != nil {
		t.Errorf("rollout %#v active %#v candidate %#v", status.Rollout, status.ActiveArtifact, status.CandidateArtifact)
	}
}

func TestStatusReadsThroughTheFirstNodeThatAnswers(t *testing.T) {
	state := shopFixture()
	state.down = map[string]bool{"node-1": true}
	status, err := inspect.New(state, shop(), "node-1", "node-2").Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Nodes[0].Error == "" || status.Health != "degraded" {
		t.Errorf("unreachable node-1 = %#v, health %q", status.Nodes[0], status.Health)
	}
	want := []string{"cluster node-1", "cluster node-2", "components node-1", "components node-2", "components node-3", "placement node-2", "deployments node-2"}
	if !reflect.DeepEqual(state.calls, want) {
		t.Errorf("reads = %v; want %v", state.calls, want)
	}
	state.down["node-2"] = true
	if _, err := inspect.New(state, shop(), "node-1", "node-2").Status(t.Context()); !errors.Is(err, errNoResponders) {
		t.Errorf("no node answers: %v", err)
	}
	if _, err := inspect.New(state, shop()).Status(t.Context()); !errors.Is(err, inspect.ErrNoNodes) {
		t.Errorf("no nodes: %v", err)
	}
}

func TestHandlersAskHealthyNodesForAReadyView(t *testing.T) {
	state := shopFixture()
	ready := controlplane.HandlerPlacementView{Ready: true}
	state.handlers = map[string]controlplane.HandlerPlacementView{"node-1": {}, "node-3": ready}
	state.cluster.Nodes[1].Health = controlplane.HealthUnavailable
	inspector := inspect.New(state, shop(), "node-1")
	status, err := inspector.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	state.calls = nil
	view, err := inspector.Handlers(t.Context(), status)
	if err != nil || !view.Ready {
		t.Fatalf("handlers = %#v, %v", view, err)
	}
	if want := []string{"handlers node-1", "handlers node-3"}; !reflect.DeepEqual(state.calls, want) {
		t.Errorf("reads = %v; want %v", state.calls, want)
	}
	state.handlers["node-3"] = controlplane.HandlerPlacementView{}
	if _, err := inspector.Handlers(t.Context(), status); !errors.Is(err, inspect.ErrNoHandlerPlacement) {
		t.Errorf("no ready view: %v", err)
	}
}

// TestInspectionIsReadOnly proves the surface cannot mutate: its port has
// read requests only, and reading twice returns the same answer from an
// unchanged state.
func TestInspectionIsReadOnly(t *testing.T) {
	source := reflect.TypeFor[inspect.Source]()
	for i := range source.NumMethod() {
		name := source.Method(i).Name
		if !strings.HasPrefix(name, "Request") {
			t.Errorf("inspect.Source.%s is not a read request", name)
		}
		for _, verb := range []string{"Start", "Stop", "Kill", "Put", "Replace", "Delete", "Record", "Claim", "Release"} {
			if strings.Contains(name, verb) {
				t.Errorf("inspect.Source.%s can change control-plane state", name)
			}
		}
	}
	inspector := reflect.TypeFor[*inspect.Inspector]()
	allowed := map[string]bool{"Nodes": true, "Status": true, "Handlers": true}
	for i := range inspector.NumMethod() {
		if name := inspector.Method(i).Name; !allowed[name] {
			t.Errorf("inspect.Inspector.%s is not a known read; inspection must stay read-only", name)
		}
	}
	state := shopFixture()
	before := shopFixture()
	reader := inspect.New(state, shop(), "node-1")
	first, err := reader.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, err := reader.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("status changed between reads:\n%#v\n%#v", first, second)
	}
	if !reflect.DeepEqual(state.cluster, before.cluster) || !reflect.DeepEqual(state.placement, before.placement) ||
		!reflect.DeepEqual(state.deployments, before.deployments) || !reflect.DeepEqual(state.components, before.components) {
		t.Error("reading changed the fixture state")
	}
}
