package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/derailed/tcell/v2"
	"github.com/grove-project/grove/console"
)

func newAppTestTUI(t *testing.T) *console.TUI {
	t.Helper()
	registry := &console.Registry{}
	results := map[string]any{
		"app.overview": applicationOverviewView{Name: "shop", ApplicationID: "com.example.shop", Version: "v1", Build: "7f91ac2"},
		"app.config":   applicationConfigView{Revision: "acme-r42", Source: "embedded", Status: "active", YAML: "payments:\n  timeout: 3s\n"},
		"app.ingress": applicationIngressView{URL: "http://127.0.0.1:1", Routes: []applicationRouteView{
			{Method: "GET", Path: "/api/products", Service: "catalog"},
		}},
		"app.version": applicationVersionView{Version: "v1", Build: "7f91ac2", Distribution: []applicationVersionGroup{
			{Version: "v1", Build: "7f91ac2", Nodes: []string{"node-1", "node-2"}},
			{Version: "v2", Build: "aa11bb2", Nodes: []string{"node-3"}},
		}},
	}
	for name := range results {
		result := results[name]
		if err := registry.Register(console.Action{
			Name: name, Label: name, Section: "App",
			Handler: func(context.Context, []string) (any, error) { return result, nil },
		}); err != nil {
			t.Fatal(err)
		}
	}
	tui, err := console.NewTUI(registry, func(context.Context) (console.Model, error) {
		return console.Model{Application: "shop"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tui
}

func TestApplicationTUIAppScreensNavigateBackToHome(t *testing.T) {
	view, err := newApplicationTUI(t.Context(), newAppTestTUI(t))
	if err != nil {
		t.Fatal(err)
	}
	view.queueDraw = func(update func()) { update() }
	view.keyboard(tcell.NewEventKey(tcell.KeyRune, 'o', tcell.ModNone))
	if !view.appOpen || view.appMode != "overview" {
		t.Fatalf("app open=%t mode=%q; want overview", view.appOpen, view.appMode)
	}
	for key, want := range map[rune]struct{ mode, text string }{
		'c': {"config", "acme-r42"},
		'i': {"ingress", "/api/products"},
		'v': {"version", "node-3"},
	} {
		view.appKeyboard(tcell.NewEventKey(tcell.KeyRune, key, tcell.ModNone))
		if view.appMode != want.mode {
			t.Fatalf("key %q mode = %q; want %q", key, view.appMode, want.mode)
		}
		waitForApplicationTUI(t, want.mode+" screen", func() bool {
			return strings.Contains(view.appView.GetText(true), want.text)
		})
		view.appKeyboard(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
		if !view.appOpen || view.appMode != "overview" {
			t.Fatalf("esc from %s: open=%t mode=%q; want overview", want.mode, view.appOpen, view.appMode)
		}
	}
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
	overview := renderApplicationOverview(applicationOverviewView{
		Name: "shop", ApplicationID: "com.example.shop", Nodes: 3, StartedAt: now.Add(-(time.Hour + 42*time.Minute + 18*time.Second)),
	}, now)
	for _, want := range []string{"com.example.shop", "01:42:18 ago", "Identity", "Runtime", "Deployment"} {
		if !strings.Contains(overview, want) {
			t.Errorf("overview missing %q:\n%s", want, overview)
		}
	}
	config := renderApplicationConfig(applicationConfigView{Revision: "r1", Source: "embedded", Status: "active", YAML: "a: 1\n"})
	if !strings.Contains(config, "read-only") || strings.Contains(strings.ToLower(config), "edit config") {
		t.Errorf("config screen must be read-only:\n%s", config)
	}
	ingress := renderApplicationIngress(applicationIngressView{Routes: []applicationRouteView{{Method: "POST", Path: "/api/orders", Service: "orders"}}})
	if !strings.Contains(ingress, "POST") || !strings.Contains(ingress, "orders") {
		t.Errorf("ingress screen missing route:\n%s", ingress)
	}
}

func TestApplicationRoutesComeFromComponentRegistration(t *testing.T) {
	previous := activeApplication
	t.Cleanup(func() { activeApplication = previous })
	activeApplication = Definition{Components: []Component{
		{Name: "Web", Routes: []Route{{Method: "GET", Path: "/"}}},
		{Name: "Orders"},
	}}
	result, err := newApplicationController("", "").appIngress(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	view := result.(applicationIngressView)
	if len(view.Routes) != 1 || view.Routes[0] != (applicationRouteView{Method: "GET", Path: "/", Service: "Web"}) {
		t.Fatalf("routes = %#v; want GET / -> Web", view.Routes)
	}
}
