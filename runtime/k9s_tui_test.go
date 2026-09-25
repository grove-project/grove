package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/derailed/tcell/v2"
	"github.com/derailed/tview"
	"github.com/grove-project/grove/console"
)

func TestApplicationTUIHandlesK9sStyleNavigation(t *testing.T) {
	view, err := newApplicationTUI(t.Context(), newTestApplicationTUI(t, nil))
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
	if _, ok := view.app.GetFocus().(*tview.InputField); !ok {
		t.Fatalf("rollout dialog focus = %T; want input field", view.app.GetFocus())
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
	if action, ok := actionForHotkey('l'); !ok || action != "logs.view" {
		t.Fatalf("logs hotkey = (%q, %t); want logs.view", action, ok)
	}

	header := view.header.GetText(true)
	if !strings.Contains(header, "HEALTHY") || !strings.Contains(header, "3/3") ||
		!strings.Contains(header, "http://127.0.0.1:8080") ||
		!strings.Contains(header, "Orders") || !strings.Contains(header, "127.0.0.1:40000") {
		t.Fatalf("status header = %q; want health counts and ingress URL", header)
	}
}

func TestApplicationTUIShowsStartupActionOnlyUntilConfirmation(t *testing.T) {
	actions := []console.Action{
		{Name: "cluster.status"},
		{Name: "cluster.start"},
		{Name: "cluster.join"},
	}
	startup := actionsForApplicationModel(actions, console.Model{StartupAction: "cluster.start"})
	if len(startup) != 1 || startup[0].Name != "cluster.start" {
		t.Fatalf("startup actions = %#v; want cluster.start only", startup)
	}
	normal := actionsForApplicationModel(actions, console.Model{})
	if len(normal) != 1 || normal[0].Name != "cluster.status" {
		t.Fatalf("normal actions = %#v; want startup actions removed", normal)
	}
}

type blockingApplicationTUISession struct {
	started chan struct{}
	release chan struct{}
}

func (s *blockingApplicationTUISession) InitialResult() any {
	return debugAttachResult{
		ServiceName: "Orders", NodeID: "node-2", WorkerID: "orders-1",
		DAPEndpoint: "127.0.0.1:40000",
	}
}

func (s *blockingApplicationTUISession) Wait(ctx context.Context) error {
	close(s.started)
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestApplicationTUIDebugAttachBecomesReadyWithoutWaitingForDisconnect(t *testing.T) {
	session := &blockingApplicationTUISession{started: make(chan struct{}), release: make(chan struct{})}
	var registry console.Registry
	if err := registry.Register(console.Action{
		Name: "debug.attach", Label: "Attach debugger", Section: "Debug",
		Handler: func(context.Context, []string) (any, error) { return session, nil },
	}); err != nil {
		t.Fatal(err)
	}
	tui, err := console.NewTUI(&registry, func(context.Context) (console.Model, error) {
		return console.Model{Application: "Grove Shop", Health: "healthy"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
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
	waitForApplicationTUI(t, "debug endpoint to become ready", func() bool {
		return !view.busy && strings.Contains(view.flash.GetText(true), "127.0.0.1:40000")
	})
	close(session.release)
	waitForApplicationTUI(t, "debug session disconnect", func() bool {
		return strings.Contains(view.flash.GetText(true), "disconnected")
	})
}

func TestApplicationTUIRefreshesDuringRollout(t *testing.T) {
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
	view, err := newApplicationTUI(t.Context(), newTestApplicationTUI(t, rollout))
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
	waitForApplicationTUI(t, "rollout completion", func() bool {
		return !view.busy && strings.Contains(view.flash.GetText(true), `"state": "active"`)
	})
}

func newTestApplicationTUI(t *testing.T, rollout console.Handler) *console.TUI {
	t.Helper()
	if rollout == nil {
		rollout = func(context.Context, []string) (any, error) { return nil, nil }
	}
	registry := &console.Registry{}
	actions := []console.Action{
		{Name: "cluster.restart", Label: "Restart cluster", Section: "Cluster", Handler: func(context.Context, []string) (any, error) { return nil, nil }},
		{Name: "cluster.status", Label: "Status", Section: "Cluster", Handler: func(context.Context, []string) (any, error) { return nil, nil }},
		{Name: "debug.demo.start", Label: "Debug demo", Section: "Deployments", Handler: func(context.Context, []string) (any, error) { return nil, nil }},
		{Name: "logs.view", Label: "View logs", Section: "Logs", Handler: func(context.Context, []string) (any, error) { return applicationLogsView{}, nil }},
		{Name: "rollout.start", Label: "New rollout", Section: "Deployments", Handler: rollout},
	}
	for _, action := range actions {
		if err := registry.Register(action); err != nil {
			t.Fatal(err)
		}
	}
	tui, err := console.NewTUI(registry, func(context.Context) (console.Model, error) {
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
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tui
}

func waitForApplicationTUI(t *testing.T, description string, condition func() bool) {
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
