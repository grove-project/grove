package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/derailed/tview"
	"github.com/grove-project/grove/internal/consoleview"
)

func renderApplicationOverview(view consoleview.Overview, now time.Time) string {
	var out strings.Builder
	field := func(label, value string) {
		fmt.Fprintf(&out, "  %-14s%s\n", label, tview.Escape(displayTUIValue(value)))
	}
	section := func(title string) { fmt.Fprintf(&out, "\n[aqua::b]%s[-:-:-]\n", title) }
	section("Identity")
	field("Name", view.Name)
	field("App ID", view.ApplicationID)
	field("Version", view.Version)
	field("Build", view.Build)
	field("Built", view.Built)
	section("Runtime")
	field("Grove SDK", view.SDKVersion)
	field("Go", view.GoVersion)
	field("Architecture", view.Architecture)
	section("Deployment")
	field("Cluster", view.Cluster)
	field("Nodes", fmt.Sprint(view.Nodes))
	started := "-"
	if !view.StartedAt.IsZero() {
		elapsed := now.Sub(view.StartedAt).Round(time.Second)
		started = fmt.Sprintf("%02d:%02d:%02d ago", int(elapsed.Hours()), int(elapsed.Minutes())%60, int(elapsed.Seconds())%60)
	}
	field("Started", started)
	return out.String()
}

func renderApplicationConfig(view consoleview.Config) string {
	var out strings.Builder
	fmt.Fprintf(&out, "\n[aqua::b]ACTIVE CONFIG[-:-:-]  [gray::](read-only)[-:-:-]\n")
	fmt.Fprintf(&out, "  %-14s%s\n", "Revision", tview.Escape(displayTUIValue(view.Revision)))
	fmt.Fprintf(&out, "  %-14s%s\n", "Source", tview.Escape(displayTUIValue(view.Source)))
	fmt.Fprintf(&out, "  %-14s%s\n\n", "Status", tview.Escape(displayTUIValue(view.Status)))
	for _, line := range strings.Split(strings.TrimRight(view.YAML, "\n"), "\n") {
		fmt.Fprintf(&out, "  %s\n", tview.Escape(line))
	}
	out.WriteString("\n[gray::]A changed configuration arrives with a new application binary via rollout.[-:-:-]\n")
	return out.String()
}

func renderApplicationIngress(view consoleview.Ingress) string {
	var out strings.Builder
	out.WriteString("\n[aqua::b]LISTENING[-:-:-]\n")
	if view.URL == "" {
		out.WriteString("  [gray::]not deployed[-:-:-]\n")
	} else {
		fmt.Fprintf(&out, "  %s\n", tview.Escape(view.URL))
	}
	fmt.Fprintf(&out, "\n[aqua::b]%-8s %-22s %s[-:-:-]\n", "METHOD", "PATH", "SERVICE")
	if len(view.Routes) == 0 {
		out.WriteString("  [gray::]No ingress routes registered.[-:-:-]\n")
	}
	for _, route := range view.Routes {
		fmt.Fprintf(&out, "%-8s %-22s %s\n", tview.Escape(route.Method), tview.Escape(route.Path), tview.Escape(route.Service))
	}
	return out.String()
}

func renderApplicationVersion(view consoleview.Version) string {
	var out strings.Builder
	fmt.Fprintf(&out, "\n[aqua::b]THIS BINARY[-:-:-]\n  %-14s%s\n  %-14s%s\n",
		"Version", tview.Escape(displayTUIValue(view.Version)), "Build", tview.Escape(displayTUIValue(view.Build)))
	if view.Rollout != "" {
		fmt.Fprintf(&out, "  %-14s%s\n", "Rollout", tview.Escape(view.Rollout))
	}
	fmt.Fprintf(&out, "\n[aqua::b]%-14s %-10s %s[-:-:-]\n", "VERSION", "BUILD", "NODES")
	if len(view.Distribution) == 0 {
		out.WriteString("  [gray::]No nodes reported.[-:-:-]\n")
	}
	for _, group := range view.Distribution {
		fmt.Fprintf(&out, "%-14s %-10s %s\n",
			tview.Escape(displayTUIValue(group.Version)),
			tview.Escape(displayTUIValue(group.Build)),
			tview.Escape(strings.Join(group.Nodes, ", ")))
	}
	return out.String()
}
