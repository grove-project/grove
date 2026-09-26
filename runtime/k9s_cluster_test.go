package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/derailed/tcell/v2"
	"github.com/derailed/tview"
	"github.com/grove-project/grove/console"
)

func TestClusterFlowDrillsIntoNodeAndJumpsToScopedLogs(t *testing.T) {
	data := applicationClusterView{
		Health: "healthy", NodesHealthy: 2, NodesTotal: 2, ServicesHealthy: 2, ServicesTotal: 2,
		Version: "v0.2.0", ConfigRevision: "acme-r42", Uptime: "01:42:18",
		Nodes: []applicationClusterNodeRow{
			{NodeID: "node-1", Health: "healthy", Hosted: []applicationHostedRow{{Service: "Orders", Handler: "CreateOrder", Status: "healthy", Owner: true}}},
			{NodeID: "node-2", Health: "healthy"},
		},
	}
	loggedArgs := make(chan []string, 1)
	registry := &console.Registry{}
	for _, action := range []console.Action{
		{Name: "cluster.nodes", Label: "Nodes", Section: "Cluster", Handler: func(context.Context, []string) (any, error) { return data, nil }},
		{Name: "logs.view", Label: "View logs", Section: "Logs", Handler: func(_ context.Context, args []string) (any, error) {
			loggedArgs <- args
			return applicationLogsView{}, nil
		}},
	} {
		if err := registry.Register(action); err != nil {
			t.Fatal(err)
		}
	}
	tui, err := console.NewTUI(registry, func(context.Context) (console.Model, error) {
		return console.Model{Application: "Grove Shop", Sections: []string{"Cluster"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := newApplicationTUI(t.Context(), tui)
	if err != nil {
		t.Fatal(err)
	}
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
	listText := func() string {
		var out strings.Builder
		for row := 0; row < view.clusterTable.GetRowCount(); row++ {
			for column := 0; column < view.clusterTable.GetColumnCount(); column++ {
				out.WriteString(view.clusterTable.GetCell(row, column).Text + " ")
			}
			out.WriteString("\n")
		}
		return out.String()
	}
	pressList := func(key tcell.Key, r rune) {
		t.Helper()
		event := view.clusterListKeyboard(tcell.NewEventKey(key, r, tcell.ModNone))
		if event != nil {
			view.clusterTable.InputHandler()(event, func(p tview.Primitive) { view.app.SetFocus(p) })
		}
	}

	view.activateActionName("cluster.nodes", nil)
	if !view.clusterOpen {
		t.Fatal("cluster.nodes did not open the Cluster overlay")
	}
	waitFor("nodes listed", func() bool { return strings.Contains(listText(), "node-1") })
	if header := view.clusterHeader.GetText(true); !strings.Contains(header, "HEALTHY") || !strings.Contains(header, "2/2") ||
		!strings.Contains(header, "v0.2.0") || !strings.Contains(header, "acme-r42") || !strings.Contains(header, "01:42:18") {
		t.Fatalf("cluster header = %q; want health/node/service/version/config/uptime summary", header)
	}

	pressList(tcell.KeyEnter, 0) // node-1
	if view.clusterNodeID != "node-1" {
		t.Fatalf("clusterNodeID = %q; want node-1", view.clusterNodeID)
	}
	detail := view.clusterNode.GetText(true)
	if !strings.Contains(detail, "Orders") || !strings.Contains(detail, "CreateOrder") || !strings.Contains(detail, "RUNTIME") {
		t.Fatalf("node detail = %q; want hosted services and runtime state", detail)
	}

	event := view.clusterNodeKeyboard(tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone))
	if event != nil {
		t.Fatal("'l' on node detail was not consumed")
	}
	select {
	case args := <-loggedArgs:
		if len(args) != 1 || args[0] != "node-1" {
			t.Fatalf("logs.view args = %v; want [node-1]", args)
		}
	case <-t.Context().Done():
		t.Fatal("logs.view was not invoked")
	}
	if view.clusterOpen {
		t.Error("cluster overlay stayed open after jumping to node-scoped logs")
	}
	if !view.logsOpen {
		t.Error("logs overlay did not open after jumping from the node detail")
	}
}

func TestClusterListKeyboardBackAndCloseNavigation(t *testing.T) {
	registry := &console.Registry{}
	if err := registry.Register(console.Action{
		Name: "cluster.nodes", Label: "Nodes", Section: "Cluster",
		Handler: func(context.Context, []string) (any, error) { return applicationClusterView{}, nil },
	}); err != nil {
		t.Fatal(err)
	}
	tui, err := console.NewTUI(registry, func(context.Context) (console.Model, error) {
		return console.Model{Application: "Grove Shop", Sections: []string{"Cluster"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := newApplicationTUI(t.Context(), tui)
	if err != nil {
		t.Fatal(err)
	}
	view.queueDraw = func(update func()) { update() }

	view.activateActionName("cluster.nodes", nil)
	if !view.clusterOpen {
		t.Fatal("cluster.nodes did not open the Cluster overlay")
	}
	if event := view.clusterListKeyboard(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)); event != nil {
		t.Fatal("Esc on the node list was not consumed")
	}
	if view.clusterOpen {
		t.Error("Esc on the top-level node list did not close the Cluster overlay")
	}
}
