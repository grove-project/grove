package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/derailed/tcell/v2"
	"github.com/grove-project/grove/console"
	"github.com/grove-project/grove/internal/consoleview"
)

func newAppScreensTUI(t *testing.T) *console.TUI {
	t.Helper()
	result := func(value any) console.Handler {
		return func(context.Context, []string) (any, error) { return value, nil }
	}
	return newTestConsole(t, console.Model{Application: "shop"},
		console.Action{
			Name: "app.overview", Label: "Overview", Section: "App", View: console.ViewApp, Key: 'o', KeyHint: "App",
			Handler: result(consoleview.Overview{Name: "shop", ApplicationID: "com.example.shop", Version: "v1", Build: "7f91ac2"}),
		},
		console.Action{
			Name: "app.config", Label: "Configuration", Section: "App", View: console.ViewApp,
			Handler: result(consoleview.Config{Revision: "acme-r42", Source: "embedded", Status: "active", YAML: "payments:\n  timeout: 3s\n"}),
		},
		console.Action{
			Name: "app.ingress", Label: "Ingress", Section: "App", View: console.ViewApp,
			Handler: result(consoleview.Ingress{URL: "http://127.0.0.1:1", Routes: []consoleview.Route{
				{Method: "GET", Path: "/api/products", Service: "catalog"},
			}}),
		},
		console.Action{
			Name: "app.version", Label: "Version / Build", Section: "App", View: console.ViewApp,
			Handler: result(consoleview.Version{Version: "v1", Build: "7f91ac2", Distribution: []consoleview.VersionGroup{
				{Version: "v1", Build: "7f91ac2", Nodes: []string{"node-1", "node-2"}},
				{Version: "v2", Build: "aa11bb2", Nodes: []string{"node-3"}},
			}}),
		},
	)
}

func TestTUIAppScreensNavigateBackToHome(t *testing.T) {
	view, err := newApplicationTUI(t.Context(), newAppScreensTUI(t))
	if err != nil {
		t.Fatal(err)
	}
	view.queueDraw = func(update func()) { update() }
	view.keyboard(tcell.NewEventKey(tcell.KeyRune, 'o', tcell.ModNone))
	if !view.appOpen || view.appMode != "app.overview" {
		t.Fatalf("app open=%t mode=%q; want overview", view.appOpen, view.appMode)
	}
	if title := view.appView.GetTitle(); !strings.Contains(title, "App · shop") {
		t.Fatalf("home title = %q; want the application name", title)
	}
	for key, want := range map[rune]struct{ mode, title, text string }{
		'c': {"app.config", "App · Configuration", "acme-r42"},
		'i': {"app.ingress", "App · Ingress", "/api/products"},
		'v': {"app.version", "App · Version / Build", "node-3"},
	} {
		view.appKeyboard(tcell.NewEventKey(tcell.KeyRune, key, tcell.ModNone))
		if view.appMode != want.mode {
			t.Fatalf("key %q mode = %q; want %q", key, view.appMode, want.mode)
		}
		if title := view.appView.GetTitle(); !strings.Contains(title, want.title) {
			t.Fatalf("key %q title = %q; want %q", key, title, want.title)
		}
		waitFor(t, want.mode+" screen", func() bool {
			text := view.appView.GetText(true)
			return strings.Contains(text, want.text) && strings.Contains(text, "<o> Overview")
		})
		view.appKeyboard(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
		if !view.appOpen || view.appMode != "app.overview" {
			t.Fatalf("esc from %s: open=%t mode=%q; want overview", want.mode, view.appOpen, view.appMode)
		}
	}
	waitFor(t, "overview footer", func() bool {
		text := view.appView.GetText(true)
		return strings.Contains(text, "<c> Configuration  <i> Ingress  <v> Version / Build  <esc> Back") &&
			!strings.Contains(text, "<o> Overview")
	})
	view.appKeyboard(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	if view.appOpen {
		t.Fatal("esc from overview did not return home")
	}
	if view.app.GetFocus() != view.table {
		t.Fatalf("focus after back = %T; want home table", view.app.GetFocus())
	}
}

func TestRenderApplicationViews(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	overview := renderApplicationOverview(consoleview.Overview{
		Name: "shop", ApplicationID: "com.example.shop", Nodes: 3, StartedAt: now.Add(-(time.Hour + 42*time.Minute + 18*time.Second)),
	}, now)
	for _, want := range []string{"com.example.shop", "01:42:18 ago", "Identity", "Runtime", "Deployment"} {
		if !strings.Contains(overview, want) {
			t.Errorf("overview missing %q:\n%s", want, overview)
		}
	}
	config := renderApplicationConfig(consoleview.Config{Revision: "r1", Source: "embedded", Status: "active", YAML: "a: 1\n"})
	if !strings.Contains(config, "read-only") || strings.Contains(strings.ToLower(config), "edit config") {
		t.Errorf("config screen must be read-only:\n%s", config)
	}
	ingress := renderApplicationIngress(consoleview.Ingress{Routes: []consoleview.Route{{Method: "POST", Path: "/api/orders", Service: "orders"}}})
	if !strings.Contains(ingress, "POST") || !strings.Contains(ingress, "orders") {
		t.Errorf("ingress screen missing route:\n%s", ingress)
	}
	version := renderApplicationVersion(consoleview.Version{Version: "v2", Distribution: []consoleview.VersionGroup{{Version: "v2", Build: "aa11bb2", Nodes: []string{"node-3"}}}})
	if !strings.Contains(version, "aa11bb2") || !strings.Contains(version, "node-3") {
		t.Errorf("version screen missing distribution:\n%s", version)
	}
}
