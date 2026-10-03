package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/derailed/tcell/v2"
	"github.com/derailed/tview"
	"github.com/grove-project/grove/console"
	"github.com/grove-project/grove/internal/consoleview"
)

func nothing(context.Context, []string) (any, error) { return nil, nil }

func newTestConsole(t *testing.T, model console.Model, actions ...console.Action) *console.TUI {
	t.Helper()
	registry := &console.Registry{}
	for _, action := range actions {
		if err := registry.Register(action); err != nil {
			t.Fatal(err)
		}
	}
	tui, err := console.NewTUI(registry, func(context.Context) (console.Model, error) { return model, nil })
	if err != nil {
		t.Fatal(err)
	}
	return tui
}

func healthyModel() console.Model {
	return console.Model{
		Application:     "Grove Shop",
		Sections:        []string{"Cluster", "Deployments"},
		Health:          "healthy",
		NodesHealthy:    3,
		NodesTotal:      3,
		ServicesHealthy: 3,
		ServicesTotal:   3,
		ActiveVersion:   "v1",
		ConfigRevision:  "sha256:active",
		IngressURL:      "http://127.0.0.1:8080",
		DebugSessions: []console.DebugSession{
			{ServiceName: "Orders", NodeID: "node-2", WorkerID: "orders-1", DAPEndpoint: "127.0.0.1:40000"},
		},
	}
}

func newShopTUI(t *testing.T, rollout console.Handler) *console.TUI {
	t.Helper()
	if rollout == nil {
		rollout = nothing
	}
	return newTestConsole(t, healthyModel(),
		console.Action{Name: "cluster.restart", Label: "Restart cluster", Section: "Cluster", Handler: nothing},
		console.Action{Name: "cluster.status", Label: "Status", Section: "Cluster", Handler: nothing, Key: 's', KeyHint: "Status"},
		console.Action{Name: "debug.demo.start", Label: "Debug demo", Section: "Deployments", Handler: nothing},
		console.Action{
			Name: "logs.view", Label: "View logs", Section: "Logs", View: console.ViewLogs, Key: 'l', KeyHint: "Logs",
			Handler: func(context.Context, []string) (any, error) { return consoleview.Logs{}, nil },
		},
		console.Action{
			Name: "rollout.start", Label: "New rollout", Section: "Deployments", Handler: rollout,
			Inputs: []console.Input{{Label: "Config path", Flag: "--config", Initial: "configs/acme.yaml"}},
		},
	)
}

func TestTUIHandlesK9sStyleNavigation(t *testing.T) {
	view, err := newApplicationTUI(t.Context(), newShopTUI(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	deliverToTable := func(event *tcell.EventKey) {
		t.Helper()
		if event = view.keyboard(event); event != nil {
			view.table.InputHandler()(event, func(primitive tview.Primitive) {
				view.app.SetFocus(primitive)
			})
		}
	}

	row, _ := view.table.GetSelection()
	if row != 1 {
		t.Fatalf("initial selection row = %d; want 1", row)
	}
	deliverToTable(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	row, _ = view.table.GetSelection()
	if row != 2 {
		t.Fatalf("down-arrow selection row = %d; want 2", row)
	}
	deliverToTable(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone))
	row, _ = view.table.GetSelection()
	if row != 3 {
		t.Fatalf("j selection row = %d; want 3", row)
	}
	deliverToTable(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone))
	row, _ = view.table.GetSelection()
	if row != 2 {
		t.Fatalf("k selection row = %d; want 2", row)
	}

	view.keyboard(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone))
	view.prompt.SetText("rollout")
	if len(view.visibleActions) != 1 || view.visibleActions[0].Name != "rollout.start" {
		t.Fatalf("filtered actions = %#v; want rollout.start", view.visibleActions)
	}
	view.promptDone(tcell.KeyEnter)
	view.keyboard(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if !view.dialogOpen {
		t.Fatal("Enter on rollout action did not open its argument dialog")
	}
	input, ok := view.app.GetFocus().(*tview.InputField)
	if !ok {
		t.Fatalf("rollout dialog focus = %T; want input field", view.app.GetFocus())
	}
	if input.GetLabel() != "Config path: " || input.GetText() != "configs/acme.yaml" {
		t.Fatalf("rollout dialog input = %q %q; want the declared input and default", input.GetLabel(), input.GetText())
	}
	view.closeDialog()

	var openedURL string
	view.openURL = func(url string) error {
		openedURL = url
		return nil
	}
	view.header.SetRect(0, 0, 120, 5)
	mouseCapture := view.header.GetMouseCapture()
	_, event := mouseCapture(
		tview.MouseLeftClick,
		tcell.NewEventMouse(10, 3, tcell.ButtonPrimary, tcell.ModNone),
	)
	if openedURL != "http://127.0.0.1:8080" {
		t.Fatalf("opened URL = %q; want app ingress", openedURL)
	}
	if event != nil {
		t.Fatal("ingress click was forwarded instead of consumed")
	}
	if action, ok := view.actionForKey('l'); !ok || action.Name != "logs.view" {
		t.Fatalf("logs key = (%q, %t); want logs.view", action.Name, ok)
	}

	header := view.header.GetText(true)
	if !strings.Contains(header, "HEALTHY") || !strings.Contains(header, "3/3") ||
		!strings.Contains(header, "http://127.0.0.1:8080") ||
		!strings.Contains(header, "Orders") || !strings.Contains(header, "127.0.0.1:40000") {
		t.Fatalf("status header = %q; want health counts and ingress URL", header)
	}
}

// The runtime decides which actions are offered; the TUI shows exactly those.
func TestTUIOffersTheActionsTheModelNames(t *testing.T) {
	model := console.Model{Application: "Grove Shop", StartupAction: "cluster.start", Actions: []string{"cluster.start"}}
	tui := newTestConsole(t, model,
		console.Action{Name: "cluster.status", Label: "Status", Section: "Cluster", Handler: nothing},
		console.Action{Name: "cluster.start", Label: "Start new cluster", Section: "Cluster", Handler: nothing},
	)
	view, err := newApplicationTUI(t.Context(), tui)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.actions) != 1 || view.actions[0].Name != "cluster.start" {
		t.Fatalf("startup actions = %#v; want cluster.start only", view.actions)
	}
	if header := view.header.GetText(true); !strings.Contains(header, "No Grove Shop cluster discovered") {
		t.Fatalf("startup header = %q; want the start offer", header)
	}

	joined := model
	joined.StartupDiscovered, joined.StartupCluster, joined.Actions = true, "acme", []string{"cluster.status"}
	if !view.setActionsForModel(joined) || len(view.actions) != 1 || view.actions[0].Name != "cluster.status" {
		t.Fatalf("actions after the model changed = %#v; want cluster.status", view.actions)
	}
	view.updateModel(joined)
	if header := view.header.GetText(true); !strings.Contains(header, "Grove cluster discovered") || !strings.Contains(header, "acme") {
		t.Fatalf("discovered header = %q; want the join offer", header)
	}
}

func TestTUIFormBuildsArgumentsFromDeclaredInputs(t *testing.T) {
	invoked := make(chan []string, 1)
	tui := newTestConsole(t, console.Model{Application: "Grove Shop"},
		console.Action{
			Name: "cluster.start", Label: "Start new cluster", Section: "Cluster",
			Inputs: []console.Input{
				{Label: "Nodes to start (min 3)", Flag: "--nodes", Initial: "3"},
				{Label: "Ingress address", Flag: "--ingress", InitialFunc: func() (string, error) { return "127.0.0.1:9000", nil }},
			},
			Handler: func(_ context.Context, args []string) (any, error) {
				invoked <- args
				return nil, nil
			},
		},
		console.Action{
			Name: "debug.attach", Label: "Attach debugger", Section: "Debug",
			Inputs:  []console.Input{{Label: "Arguments", Initial: "x", InitialFunc: func() (string, error) { return "", errors.New("no free port") }}},
			Handler: nothing,
		},
	)
	view, err := newApplicationTUI(t.Context(), tui)
	if err != nil {
		t.Fatal(err)
	}
	view.queueDraw = func(update func()) { update() }

	view.activateActionName("cluster.start", nil)
	first, ok := view.app.GetFocus().(*tview.InputField)
	if !view.dialogOpen || !ok {
		t.Fatal("cluster.start did not open its form")
	}
	if first.GetText() != "3" {
		t.Fatalf("first input = %q; want 3", first.GetText())
	}
	first.SetText("5")
	first.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(p tview.Primitive) { view.app.SetFocus(p) })
	second := view.app.GetFocus().(*tview.InputField)
	if second.GetText() != "127.0.0.1:9000" {
		t.Fatalf("second input = %q; want the computed default", second.GetText())
	}
	second.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(p tview.Primitive) { view.app.SetFocus(p) })
	select {
	case args := <-invoked:
		if strings.Join(args, " ") != "--nodes 5 --ingress 127.0.0.1:9000" {
			t.Fatalf("cluster.start args = %q", args)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cluster.start was not invoked")
	}
	if view.dialogOpen {
		t.Fatal("form stayed open after submit")
	}

	waitFor(t, "cluster.start to finish", func() bool { return !view.busy })
	view.activateActionName("debug.attach", nil)
	if view.dialogOpen || !strings.Contains(view.flash.GetText(true), "no free port") {
		t.Fatalf("dialog open=%t flash=%q; want the default's error", view.dialogOpen, view.flash.GetText(true))
	}
}

type summarized struct{ text string }

func (s summarized) Summary() string { return s.text }

type blockingSession struct {
	started chan struct{}
	release chan struct{}
}

func (s *blockingSession) InitialResult() any {
	return summarized{"Debugger ready: Orders on node-2/orders-1\nDAP: 127.0.0.1:40000"}
}

func (s *blockingSession) Wait(ctx context.Context) error {
	close(s.started)
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestTUIDebugAttachBecomesReadyWithoutWaitingForDisconnect(t *testing.T) {
	session := &blockingSession{started: make(chan struct{}), release: make(chan struct{})}
	tui := newTestConsole(t, console.Model{Application: "Grove Shop", Health: "healthy"}, console.Action{
		Name: "debug.attach", Label: "Attach debugger", Section: "Debug",
		Handler: func(context.Context, []string) (any, error) { return session, nil },
	})
	view, err := newApplicationTUI(t.Context(), tui)
	if err != nil {
		t.Fatal(err)
	}
	view.queueDraw = func(update func()) { update() }
	view.activateActionName("debug.attach", []string{"orders", "--listen", "127.0.0.1:40000"})
	select {
	case <-session.started:
	case <-time.After(2 * time.Second):
		t.Fatal("debug session did not begin waiting for its DAP client")
	}
	waitFor(t, "debug endpoint to become ready", func() bool {
		return !view.busy && strings.Contains(view.flash.GetText(true), "127.0.0.1:40000")
	})
	close(session.release)
	waitFor(t, "debug session disconnect", func() bool {
		return strings.Contains(view.flash.GetText(true), "disconnected")
	})
}

func TestTUIRefreshesDuringRollout(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	rollout := func(ctx context.Context, args []string) (any, error) {
		if len(args) != 2 || args[0] != "--config" || args[1] != "configs/acme.yaml" {
			t.Errorf("rollout arguments = %q; want default config path", args)
		}
		close(started)
		select {
		case <-release:
			return map[string]string{"state": "active"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	view, err := newApplicationTUI(t.Context(), newShopTUI(t, rollout))
	if err != nil {
		t.Fatal(err)
	}
	// Production updates are queued on tview's event loop. Running them inline
	// keeps this unit test deterministic while exercising the same callbacks.
	view.queueDraw = func(update func()) { update() }
	view.activateActionName("rollout.start", []string{"--config", "configs/acme.yaml"})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("rollout action did not start")
	}
	view.refreshModel(console.Model{
		Application:       "Grove Shop",
		Health:            "not-deployed",
		CandidateRevision: "sha256:candidate",
		RolloutPhase:      "building artifact",
		LastEvent:         "rollout: building embedded-config artifact",
	})
	if header := view.header.GetText(true); !strings.Contains(header, "building artifact") {
		t.Fatalf("rollout header = %q; want current rollout phase", header)
	}
	if flash := view.flash.GetText(true); !strings.Contains(flash, "in progress") {
		t.Fatalf("rollout progress = %q; want responsive progress indicator", flash)
	}
	close(release)
	waitFor(t, "rollout completion", func() bool {
		return !view.busy && strings.Contains(view.flash.GetText(true), `"state": "active"`)
	})
}

func TestSummarizeUsesTheResultsOwnSummary(t *testing.T) {
	if got := summarize(summarized{"Cluster: healthy\nNodes: 3"}); got != "Cluster: healthy\nNodes: 3" {
		t.Errorf("summary = %q", got)
	}
	if got := summarize(map[string]int{"nodes": 3}); !strings.Contains(got, `"nodes": 3`) {
		t.Errorf("JSON fallback = %q", got)
	}
}

func TestHintLineAdvertisesDeclaredKeys(t *testing.T) {
	tui := newTestConsole(t, console.Model{Application: "Grove Shop"},
		console.Action{Name: "services.view", Label: "Services", Section: "Services", Handler: nothing, Key: 'p', KeyHint: "Services"},
		console.Action{Name: "cluster.nodes", Label: "Nodes", Section: "Cluster", Handler: nothing, Key: 'n', KeyHint: "Nodes"},
		console.Action{Name: "debug.attach", Label: "Attach debugger", Section: "Debug", Handler: nothing, Key: 'a'},
	)
	view, err := newApplicationTUI(t.Context(), tui)
	if err != nil {
		t.Fatal(err)
	}
	hint := view.hintLine()
	for _, want := range []string{"<p>[-:-:-] Services", "<n>[-:-:-] Nodes", "<enter>", "<q>"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint line %q lacks %q", hint, want)
		}
	}
	if strings.Contains(hint, "<a>") {
		t.Errorf("hint line %q advertises a key without a KeyHint", hint)
	}
	if action, ok := view.actionForKey('a'); !ok || action.Name != "debug.attach" {
		t.Errorf("key a = %q, %t; want debug.attach", action.Name, ok)
	}
	if label := keyLabel('p'); label != "p" {
		t.Errorf("key column = %q; want p", label)
	}
}

func TestRenderApplicationLogs(t *testing.T) {
	rendered := renderApplicationLogs(consoleview.Logs{
		Health:        "degraded",
		Causes:        []string{"Inventory on node-2 is failed: inventory.reservation_buffer must be zero or greater"},
		Application:   []string{"node-2 grovlet: start component 2"},
		Cluster:       []string{"node=node-2 event=ready"},
		SystemNATS:    []string{"client-endpoint=nats://127.0.0.1:4222"},
		DebugSessions: []console.DebugSession{{ServiceName: "Orders", NodeID: "node-2", WorkerID: "orders-1", DAPEndpoint: "127.0.0.1:40000"}},
	})
	for _, want := range []string{"WHY NOT HEALTHY", "ACTIVE DELVE / DAP SESSIONS", "127.0.0.1:40000", "APPLICATION", "CLUSTER", "SYSTEM NATS / CONTROL PLANE", "reservation_buffer"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered logs = %q; want %q", rendered, want)
		}
	}
}

func waitFor(t *testing.T, description string, condition func() bool) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatalf("timed out waiting for %s", description)
		}
	}
}
