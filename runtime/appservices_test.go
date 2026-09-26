package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/derailed/tcell/v2"
	"github.com/derailed/tview"
	"github.com/grove-project/grove"
	"github.com/grove-project/grove/console"
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
	if view.Services[0].placementCount() != 7 {
		t.Errorf("placement count = %d; want 7", view.Services[0].placementCount())
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

func TestServicesDrillDownLevelsAndBackNavigation(t *testing.T) {
	withPaymentApplication(t)
	view := buildServicesView(systemnats.HandlerPlacementView{
		Ready: true,
		Nodes: registrations("node-1", "node-2"),
		Placements: []systemnats.HandlerPlacement{
			handlerPlacement(methodValidate, 0, false, "node-1", "node-2"),
			handlerPlacement(methodCharge, 0, false, "node-1", "node-2"),
			handlerPlacement(methodReconcil, 1, true, "node-2"),
		},
	}, clusterStatus(map[string]string{"node-1": "healthy", "node-2": "healthy"}, "node-1", "node-2"))

	location := servicesLocation{}
	screen := buildServicesScreen(view, location)
	if screen.level != "services" || screen.title != "APP / SERVICES" || screen.rows[0].cells[0] != "Payment" || screen.rows[0].cells[2] != "5" {
		t.Fatalf("services screen = %#v", screen)
	}
	location = location.drillDown(screen, screen.rows[0])
	screen = buildServicesScreen(view, location)
	if screen.level != "handlers" || screen.title != "APP / SERVICES / PAYMENT" || len(screen.rows) != 3 {
		t.Fatalf("handlers screen = %#v", screen)
	}
	if got := screen.rows[2].cells; got[0] != "Reconcile" || got[1] != "exclusive" || got[3] != "node-2" {
		t.Errorf("Reconcile row = %v; want exclusive owned by node-2", got)
	}
	location = location.drillDown(screen, screen.rows[2])
	screen = buildServicesScreen(view, location)
	if screen.level != "placements" || screen.title != "APP / SERVICES / PAYMENT / RECONCILE" || screen.service != "Payment" {
		t.Fatalf("placements screen = %#v", screen)
	}
	if got := screen.rows[0].cells; got[1] != "node-2" || got[2] != "healthy" || !strings.Contains(got[3], "owner") {
		t.Errorf("placement row = %v; want the owner on node-2", got)
	}
	if !strings.Contains(screen.hint, "Debug") {
		t.Errorf("placement hint %q does not offer debugging", screen.hint)
	}

	for _, want := range []string{"handlers", "services"} {
		var atTop bool
		location, atTop = location.up()
		if atTop || buildServicesScreen(view, location).level != want {
			t.Fatalf("up did not reach %s: %#v", want, location)
		}
	}
	if _, atTop := location.up(); !atTop {
		t.Error("services level is not the top")
	}

	// A handler that disappears drops the view back to its service.
	stale := servicesLocation{service: paymentService, hasService: true, method: 99, hasHandler: true}
	if got := buildServicesScreen(view, stale).level; got != "handlers" {
		t.Errorf("vanished handler level = %s; want handlers", got)
	}

	nodes := buildServicesScreen(view, servicesLocation{nodes: true})
	if nodes.level != "nodes" || nodes.rows[0].cells[0] != "node-1" || nodes.rows[0].cells[2] != "2" {
		t.Fatalf("nodes screen = %#v", nodes)
	}
	hosted := buildServicesScreen(view, servicesLocation{nodes: true}.drillDown(nodes, nodes.rows[1]))
	if hosted.level != "hosted" || len(hosted.rows) != 3 || hosted.rows[2].cells[4] != "owner" {
		t.Fatalf("hosted screen = %#v; want node-2's three placements with Reconcile owned", hosted)
	}
}

func TestServicesOverlayDrillsToPlacementAndAttachesDebuggerToThatNode(t *testing.T) {
	withPaymentApplication(t)
	data := buildServicesView(systemnats.HandlerPlacementView{
		Ready: true,
		Nodes: registrations("node-1", "node-2"),
		Placements: []systemnats.HandlerPlacement{
			handlerPlacement(methodValidate, 0, false, "node-1", "node-2"),
			handlerPlacement(methodCharge, 0, false, "node-1", "node-2"),
			handlerPlacement(methodReconcil, 1, true, "node-2"),
		},
	}, clusterStatus(map[string]string{"node-1": "healthy", "node-2": "healthy"}, "node-1", "node-2"))

	attached := make(chan []string, 1)
	registry := &console.Registry{}
	for _, action := range []console.Action{
		{Name: "services.view", Label: "Services", Section: "Services", Handler: func(context.Context, []string) (any, error) { return data, nil }},
		{Name: "debug.attach", Label: "Attach debugger", Section: "Debug", Handler: func(_ context.Context, args []string) (any, error) {
			attached <- args
			return nil, nil
		}},
		{Name: "logs.view", Label: "View logs", Section: "Logs", Handler: func(context.Context, []string) (any, error) { return applicationLogsView{}, nil }},
	} {
		if err := registry.Register(action); err != nil {
			t.Fatal(err)
		}
	}
	tui, err := console.NewTUI(registry, func(context.Context) (console.Model, error) {
		return console.Model{Application: "Grove Shop", Sections: []string{"Services"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := newApplicationTUI(t.Context(), tui)
	if err != nil {
		t.Fatal(err)
	}
	// Updates are applied on the test goroutine so the overlay is never
	// touched concurrently.
	updates := make(chan func(), 64)
	view.queueDraw = func(update func()) { updates <- update }
	pump := func() {
		for {
			select {
			case update := <-updates:
				update()
			default:
				return
			}
		}
	}
	waitFor := func(description string, condition func() bool) {
		t.Helper()
		waitForApplicationTUI(t, description, func() bool { pump(); return condition() })
	}
	press := func(key tcell.Key, r rune) {
		t.Helper()
		event := view.servicesKeyboard(tcell.NewEventKey(key, r, tcell.ModNone))
		if event != nil {
			view.servicesTable.InputHandler()(event, func(p tview.Primitive) { view.app.SetFocus(p) })
		}
	}
	tableText := func() string {
		var out strings.Builder
		for row := 0; row < view.servicesTable.GetRowCount(); row++ {
			for column := 0; column < view.servicesTable.GetColumnCount(); column++ {
				out.WriteString(view.servicesTable.GetCell(row, column).Text + " ")
			}
			out.WriteString("\n")
		}
		return out.String()
	}

	view.activateActionName("services.view", nil)
	if !view.servicesOpen {
		t.Fatal("services.view did not open the services overlay")
	}
	waitFor("services listed", func() bool { return strings.Contains(tableText(), "Payment") })

	press(tcell.KeyEnter, 0) // Payment
	waitFor("handlers listed", func() bool { return strings.Contains(tableText(), "Reconcile") })
	press(tcell.KeyDown, 0)
	press(tcell.KeyDown, 0) // Reconcile
	press(tcell.KeyEnter, 0)
	waitFor("placements listed", func() bool { return strings.Contains(tableText(), "payment/1") })
	if text := tableText(); !strings.Contains(text, "node-2") || !strings.Contains(text, "owner (epoch 1)") {
		t.Fatalf("placement table does not name the owner:\n%s", text)
	}

	press(tcell.KeyRune, 'd')
	select {
	case args := <-attached:
		if strings.Join(args, " ") != "Payment --node node-2" {
			t.Fatalf("debug.attach args = %v; want the selected placement's node", args)
		}
	case <-t.Context().Done():
		t.Fatal("debug.attach was not invoked")
	}
	if view.servicesOpen {
		t.Error("services overlay stayed open after starting the debugger")
	}
}

func TestServicesAndNodesHaveHotkeys(t *testing.T) {
	for key, want := range map[rune]string{'p': "services.view", 'n': "cluster.nodes"} {
		if got, ok := actionForHotkey(key); !ok || got != want || hotkeyLabel(want) != string(key) {
			t.Errorf("hotkey %q = (%q, %t); want %s", key, got, ok, want)
		}
	}
	if hint := k9sHintLine(); !strings.Contains(hint, "Services") || !strings.Contains(hint, "Nodes") {
		t.Errorf("hint line %q does not advertise services and nodes", hint)
	}
}
