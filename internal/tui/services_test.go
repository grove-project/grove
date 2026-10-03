package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/derailed/tcell/v2"
	"github.com/derailed/tview"
	"github.com/grove-project/grove"
	"github.com/grove-project/grove/console"
	"github.com/grove-project/grove/internal/consoleview"
)

const paymentService grove.ServiceID = 3

func placement(id, nodeID string, owner bool) consoleview.Placement {
	return consoleview.Placement{
		ID: id, NodeID: nodeID, Status: consoleview.PlacementHealthy, Owner: owner,
		Debug: consoleview.Invocation{Name: "debug.attach", Args: []string{"Payment", "--node", nodeID}},
	}
}

// paymentServices is Payment with two scalable handlers on node-1 and node-2
// and an exclusive Reconcile owned by node-2, as the runtime builds it.
func paymentServices() consoleview.Services {
	hosted := func(handler, scaling string, owner bool) consoleview.Hosted {
		return consoleview.Hosted{Service: "Payment", Handler: handler, Scaling: scaling, Status: consoleview.PlacementHealthy, Owner: owner}
	}
	return consoleview.Services{
		Services: []consoleview.Service{{
			ServiceID: paymentService, Name: "Payment", Status: consoleview.PlacementHealthy,
			Handlers: []consoleview.Handler{
				{Method: 1, Name: "Validate", Scaling: "automatic", Placements: []consoleview.Placement{
					placement("payment/1", "node-1", false), placement("payment/2", "node-2", false),
				}},
				{Method: 2, Name: "Charge", Scaling: "automatic", Placements: []consoleview.Placement{
					placement("payment/1", "node-1", false), placement("payment/2", "node-2", false),
				}},
				{Method: 3, Name: "Reconcile", Scaling: "exclusive", Capability: "payment/reconcile", Epoch: 1, Owner: "node-2",
					Placements: []consoleview.Placement{placement("payment/1", "node-2", true)}},
			},
		}},
		Nodes: []consoleview.Node{
			{NodeID: "node-1", Health: "healthy", Hosted: []consoleview.Hosted{
				hosted("Validate", "automatic", false), hosted("Charge", "automatic", false),
			}},
			{NodeID: "node-2", Health: "healthy", Hosted: []consoleview.Hosted{
				hosted("Validate", "automatic", false), hosted("Charge", "automatic", false), hosted("Reconcile", "exclusive", true),
			}},
		},
	}
}

func TestServicesDrillDownLevelsAndBackNavigation(t *testing.T) {
	view := paymentServices()
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

func TestServicesOverlayDrillsToPlacementAndRunsItsDebugInvocation(t *testing.T) {
	data := paymentServices()
	attached := make(chan []string, 1)
	tui := newTestConsole(t, console.Model{Application: "Grove Shop", Sections: []string{"Services"}},
		console.Action{
			Name: "services.view", Label: "Services", Section: "Services", View: console.ViewServices,
			Handler: func(context.Context, []string) (any, error) { return data, nil },
		},
		console.Action{Name: "debug.attach", Label: "Attach debugger", Section: "Debug", Handler: func(_ context.Context, args []string) (any, error) {
			attached <- args
			return nil, nil
		}},
		console.Action{
			Name: "logs.view", Label: "View logs", Section: "Logs", View: console.ViewLogs,
			Handler: func(context.Context, []string) (any, error) { return consoleview.Logs{}, nil },
		},
	)
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
	waitForUpdate := func(description string, condition func() bool) {
		t.Helper()
		waitFor(t, description, func() bool { pump(); return condition() })
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
	waitForUpdate("services listed", func() bool { return strings.Contains(tableText(), "Payment") })

	press(tcell.KeyEnter, 0) // Payment
	waitForUpdate("handlers listed", func() bool { return strings.Contains(tableText(), "Reconcile") })
	press(tcell.KeyDown, 0)
	press(tcell.KeyDown, 0) // Reconcile
	press(tcell.KeyEnter, 0)
	waitForUpdate("placements listed", func() bool { return strings.Contains(tableText(), "payment/1") })
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

	// l opens the logs view the runtime registered.
	waitForUpdate("debug.attach to finish", func() bool { return !view.busy })
	view.activateActionName("services.view", nil)
	press(tcell.KeyRune, 'l')
	if view.servicesOpen || !view.logsOpen || view.logsAction != "logs.view" {
		t.Fatalf("services open=%t logs open=%t action=%q; want the logs view", view.servicesOpen, view.logsOpen, view.logsAction)
	}
}

func TestServicesPlacementWithoutDebugInvocationIsNotDebuggable(t *testing.T) {
	view := paymentServices()
	view.Services[0].Handlers[2].Placements[0].Debug = consoleview.Invocation{}
	location := servicesLocation{service: paymentService, hasService: true, method: 3, hasHandler: true}
	screen := buildServicesScreen(view, location)
	if screen.level != "placements" || screen.rows[0].debug.Name != "" {
		t.Fatalf("placements screen = %#v; want a row with no debug invocation", screen)
	}
}

// Placements say which node runs a service; the nodes screen also counts the
// processes on each node.
func TestNodesScreenCountsProcesses(t *testing.T) {
	view := consoleview.Services{Nodes: []consoleview.Node{{
		NodeID: "node-1", Health: "healthy",
		Hosted: []consoleview.Hosted{{Service: "Payment", Execution: consoleview.Execution{ProcessID: "worker-1", PID: 200, ExecutionMode: consoleview.ExecutionIsolated}}},
		Processes: []consoleview.Process{
			{Execution: consoleview.Execution{ProcessID: "app-runtime-1", PID: 100}, Services: []string{"Orders", "Inventory"}},
			{Execution: consoleview.Execution{ProcessID: "worker-1", PID: 200}, Services: []string{"Payment"}},
		},
	}}}
	screen := nodesScreen(view)
	if got := screen.rows[0].cells[3]; got != "2" {
		t.Errorf("node processes cell = %q; want 2", got)
	}
	hosted := hostedScreen(view.Nodes[0])
	if got := hosted.rows[0].cells[5]; got != "worker-1 (pid 200) isolated" {
		t.Errorf("hosted process cell = %q", got)
	}
}
