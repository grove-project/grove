package runtime

import (
	"strings"
	"testing"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
)

const (
	paymentService grove.ServiceID = 3
	methodValidate grove.MethodID  = 1
	methodCharge   grove.MethodID  = 2
	methodReconcil grove.MethodID  = 3
)

func withPaymentApplication(t *testing.T) {
	t.Helper()
	previous := activeApplication
	t.Cleanup(func() { activeApplication = previous })
	activeApplication = Definition{Components: []Component{{
		ServiceID: paymentService, Name: "Payment", Kind: "payment",
		Handlers: []HandlerSpec{
			{Method: methodValidate, Name: "Validate"},
			{Method: methodCharge, Name: "Charge"},
			{Method: methodReconcil, Name: "Reconcile", Exclusive: true, Capability: "payment/reconcile"},
		},
	}}}
}

func handlerPlacement(method grove.MethodID, epoch uint64, exclusive bool, nodes ...string) systemnats.HandlerPlacement {
	placement := systemnats.HandlerPlacement{Service: paymentService, Method: method, Exclusive: exclusive, Epoch: epoch}
	for _, node := range nodes {
		placement.Nodes = append(placement.Nodes, systemnats.HandlerNode{NodeID: node})
	}
	return placement
}

func registrations(nodes ...string) []systemnats.NodeHandlers {
	var out []systemnats.NodeHandlers
	for _, node := range nodes {
		out = append(out, systemnats.NodeHandlers{NodeID: node, Handlers: []systemnats.HandlerRegistration{
			{Service: paymentService, Method: methodValidate},
			{Service: paymentService, Method: methodCharge},
			{Service: paymentService, Method: methodReconcil, Exclusive: true, Capability: "payment/reconcile"},
		}})
	}
	return out
}

func clusterStatus(states map[string]string, order ...string) ClusterStatus {
	var status ClusterStatus
	for _, node := range order {
		status.Nodes = append(status.Nodes, NodeStatus{NodeID: node, Health: states[node]})
	}
	return status
}

func handlerByName(t *testing.T, view applicationServicesView, name string) applicationHandlerRow {
	t.Helper()
	for _, service := range view.Services {
		for _, handler := range service.Handlers {
			if handler.Name == name {
				return handler
			}
		}
	}
	t.Fatalf("handler %q not in view %#v", name, view)
	return applicationHandlerRow{}
}

func nodeIDs(rows []applicationPlacementRow) []string {
	var ids []string
	for _, row := range rows {
		ids = append(ids, row.NodeID+":"+row.Status)
	}
	return ids
}

func TestServicesViewShowsScalableAndExclusiveHandlersOfOneService(t *testing.T) {
	withPaymentApplication(t)
	view := buildServicesView(systemnats.HandlerPlacementView{
		Ready: true,
		Nodes: registrations("node-1", "node-2", "node-3"),
		Placements: []systemnats.HandlerPlacement{
			handlerPlacement(methodValidate, 0, false, "node-1", "node-2", "node-3"),
			handlerPlacement(methodCharge, 0, false, "node-1", "node-2", "node-3"),
			handlerPlacement(methodReconcil, 1, true, "node-1"),
		},
	}, clusterStatus(map[string]string{"node-1": "healthy", "node-2": "healthy", "node-3": "healthy"}, "node-1", "node-2", "node-3"))

	if len(view.Services) != 1 || view.Services[0].Name != "Payment" || view.Services[0].Status != "healthy" {
		t.Fatalf("services = %#v", view.Services)
	}
	charge := handlerByName(t, view, "Charge")
	if charge.Scaling != "automatic" || len(charge.Placements) != 3 || charge.Owner != "" {
		t.Errorf("Charge = %#v; want automatic on three nodes with no owner", charge)
	}
	if charge.Placements[1].ID != "payment/2" || charge.Placements[1].NodeID != "node-2" {
		t.Errorf("Charge placement identity = %#v", charge.Placements[1])
	}
	reconcile := handlerByName(t, view, "Reconcile")
	if reconcile.Scaling != "exclusive" || reconcile.Owner != "node-1" || len(reconcile.Placements) != 1 || !reconcile.Placements[0].Owner || reconcile.Epoch != 1 {
		t.Errorf("Reconcile = %#v; want exclusive, single owner node-1, epoch 1", reconcile)
	}
	if view.Services[0].PlacementCount() != 7 {
		t.Errorf("placement count = %d; want 7", view.Services[0].PlacementCount())
	}
	// The inverse view lists the same placements per node.
	if len(view.Nodes) != 3 || len(view.Nodes[0].Hosted) != 3 || len(view.Nodes[1].Hosted) != 2 {
		t.Fatalf("nodes = %#v; want node-1 hosting 3 placements and node-2 hosting 2", view.Nodes)
	}
	var owned []string
	for _, hosted := range view.Nodes[0].Hosted {
		if hosted.Owner {
			owned = append(owned, hosted.Handler)
		}
	}
	if strings.Join(owned, ",") != "Reconcile" {
		t.Errorf("node-1 owns %v; want Reconcile", owned)
	}
}

func TestServicesViewExposesFailureAndOwnershipTransfer(t *testing.T) {
	withPaymentApplication(t)
	states := map[string]string{"node-1": "unavailable", "node-2": "healthy", "node-3": "healthy"}
	status := clusterStatus(states, "node-1", "node-2", "node-3")

	// node-1 died: its placements are lost and Reconcile has no owner yet.
	view := buildServicesView(systemnats.HandlerPlacementView{
		Ready: true,
		Nodes: registrations("node-1", "node-2", "node-3"),
		Placements: []systemnats.HandlerPlacement{
			handlerPlacement(methodValidate, 0, false, "node-2", "node-3"),
			handlerPlacement(methodCharge, 0, false, "node-2", "node-3"),
		},
		Lost: []systemnats.LostPlacement{
			{Service: paymentService, Method: methodValidate, NodeID: "node-1"},
			{Service: paymentService, Method: methodCharge, NodeID: "node-1"},
			{Service: paymentService, Method: methodReconcil, NodeID: "node-1"},
		},
	}, status)
	charge := handlerByName(t, view, "Charge")
	if got := strings.Join(nodeIDs(charge.Placements), " "); got != "node-2:healthy node-3:healthy node-1:lost" {
		t.Errorf("Charge placements = %s; want survivors healthy and node-1 lost", got)
	}
	reconcile := handlerByName(t, view, "Reconcile")
	if !reconcile.Transfer || reconcile.Owner != "" || len(reconcile.Placements) != 3 {
		t.Errorf("Reconcile = %#v; want ownership transfer in progress", reconcile)
	}
	if got := strings.Join(nodeIDs(reconcile.Placements), " "); got != "node-1:lost node-2:starting node-3:starting" {
		t.Errorf("Reconcile placements = %s; want the lost owner and starting candidates", got)
	}
	if view.Services[0].Status != "recovering" {
		t.Errorf("service status = %q; want recovering", view.Services[0].Status)
	}

	// Reconciliation finishes: one new owner at a higher epoch, nothing lost.
	view = buildServicesView(systemnats.HandlerPlacementView{
		Ready: true,
		Nodes: registrations("node-2", "node-3"),
		Placements: []systemnats.HandlerPlacement{
			handlerPlacement(methodValidate, 0, false, "node-2", "node-3"),
			handlerPlacement(methodCharge, 0, false, "node-2", "node-3"),
			handlerPlacement(methodReconcil, 2, true, "node-2"),
		},
	}, status)
	reconcile = handlerByName(t, view, "Reconcile")
	if reconcile.Transfer || reconcile.Owner != "node-2" || reconcile.Epoch != 2 {
		t.Errorf("Reconcile after recovery = %#v; want owner node-2 at epoch 2", reconcile)
	}
	if view.Services[0].Status != "healthy" {
		t.Errorf("service status after recovery = %q; want healthy", view.Services[0].Status)
	}
}

func TestServicesViewFallsBackToWholeServicePlacements(t *testing.T) {
	previous := activeApplication
	t.Cleanup(func() { activeApplication = previous })
	activeApplication = Definition{Components: []Component{{ServiceID: 1, Name: "Orders", Kind: "orders"}}}
	view := buildServicesView(systemnats.HandlerPlacementView{}, ClusterStatus{
		Nodes:      []NodeStatus{{NodeID: "node-1", Health: "healthy"}},
		Placements: []PlacementStatus{{ServiceID: 1, Name: "Orders", NodeID: "node-1", Health: "healthy"}},
	})
	if len(view.Services) != 1 || view.Services[0].Handlers[0].Scaling != "service" || view.Services[0].Status != "healthy" {
		t.Fatalf("services = %#v; want one whole-service placement", view.Services)
	}
	if len(view.Nodes) != 1 || len(view.Nodes[0].Hosted) != 1 {
		t.Fatalf("nodes = %#v", view.Nodes)
	}
}

// Each placement carries the debug.attach invocation for its node, so the
// services view offers debugging without knowing the action.
func TestServicesViewPlacementsCarryTheirDebugInvocation(t *testing.T) {
	withPaymentApplication(t)
	view := buildServicesView(systemnats.HandlerPlacementView{
		Ready:      true,
		Nodes:      registrations("node-1", "node-2"),
		Placements: []systemnats.HandlerPlacement{handlerPlacement(methodReconcil, 1, true, "node-2")},
	}, clusterStatus(map[string]string{"node-1": "healthy", "node-2": "healthy"}, "node-1", "node-2"))
	debug := handlerByName(t, view, "Reconcile").Placements[0].Debug
	if debug.Name != "debug.attach" || strings.Join(debug.Args, " ") != "Payment --node node-2" {
		t.Fatalf("debug invocation = %#v; want debug.attach Payment --node node-2", debug)
	}
	if service, _, node, err := parseDebugAttachArguments(debug.Args); err != nil || service != "Payment" || node != "node-2" {
		t.Fatalf("debug.attach parses its own invocation %q as (%q, %q, %v)", debug.Args, service, node, err)
	}

	// A service placed as a whole is debugged on its node too.
	activeApplication = Definition{Components: []Component{{ServiceID: 1, Name: "Orders", Kind: "orders"}}}
	whole := buildServicesView(systemnats.HandlerPlacementView{}, ClusterStatus{
		Nodes:      []NodeStatus{{NodeID: "node-1", Health: "healthy"}},
		Placements: []PlacementStatus{{ServiceID: 1, Name: "Orders", NodeID: "node-1", Health: "healthy"}},
	})
	if debug := whole.Services[0].Handlers[0].Placements[0].Debug; strings.Join(debug.Args, " ") != "Orders --node node-1" {
		t.Fatalf("whole-service debug invocation = %#v", debug)
	}
}

// Placements say which node runs a service; the view also says which process
// on that node does, and shows many services sharing one application runtime.
func TestServicesViewShowsServicesSharingOneProcess(t *testing.T) {
	previous := activeApplication
	t.Cleanup(func() { activeApplication = previous })
	activeApplication = Definition{Components: []Component{
		{ServiceID: 1, Name: "Orders", Kind: "orders"},
		{ServiceID: 2, Name: "Inventory", Kind: "inventory"},
		{ServiceID: 3, Name: "Payment", Kind: "payment"},
	}}
	shared := func(id grove.ServiceID, name string) ComponentStatus {
		return ComponentStatus{ServiceID: id, Name: name, State: "healthy", ExecutionMode: "in-process", ProcessID: "app-runtime-1", PID: 100}
	}
	view := buildServicesView(systemnats.HandlerPlacementView{}, ClusterStatus{
		Nodes: []NodeStatus{{NodeID: "node-1", Health: "healthy", Components: []ComponentStatus{
			shared(1, "Orders"),
			shared(2, "Inventory"),
			{ServiceID: 3, Name: "Payment", State: "healthy", ExecutionMode: "isolated-process", ProcessID: "worker-1", PID: 200},
		}}},
		Placements: []PlacementStatus{
			{ServiceID: 1, Name: "Orders", NodeID: "node-1", Health: "healthy"},
			{ServiceID: 2, Name: "Inventory", NodeID: "node-1", Health: "healthy"},
			{ServiceID: 3, Name: "Payment", NodeID: "node-1", Health: "healthy"},
		},
	})
	if len(view.Nodes) != 1 || len(view.Nodes[0].Processes) != 2 {
		t.Fatalf("nodes = %#v; want one node with two processes", view.Nodes)
	}
	runtimeProcess := view.Nodes[0].Processes[0]
	if runtimeProcess.ProcessID != "app-runtime-1" || runtimeProcess.PID != 100 || strings.Join(runtimeProcess.Services, ",") != "Orders,Inventory" {
		t.Errorf("shared process = %#v; want Orders and Inventory in app-runtime-1", runtimeProcess)
	}
	labels := map[string]string{}
	for _, hosted := range view.Nodes[0].Hosted {
		labels[hosted.Service] = hosted.Label()
	}
	want := map[string]string{
		"Orders":    "app-runtime-1 (pid 100)",
		"Inventory": "app-runtime-1 (pid 100)",
		"Payment":   "worker-1 (pid 200) isolated",
	}
	for service, label := range want {
		if labels[service] != label {
			t.Errorf("%s process = %q; want %q", service, labels[service], label)
		}
	}
}
