package runtime

import (
	"context"
	"fmt"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/derailed/tview"
	"github.com/grove-project/grove/internal/artifact"
)

const groveModulePath = "github.com/grove-project/grove"

type applicationOverviewView struct {
	Name          string    `json:"name"`
	ApplicationID string    `json:"application_id"`
	Version       string    `json:"version"`
	Build         string    `json:"build"`
	Built         string    `json:"built"`
	SDKVersion    string    `json:"sdk_version"`
	GoVersion     string    `json:"go_version"`
	Architecture  string    `json:"architecture"`
	Cluster       string    `json:"cluster"`
	Nodes         int       `json:"nodes"`
	StartedAt     time.Time `json:"started_at"`
}

type applicationConfigView struct {
	Revision string `json:"revision"`
	Source   string `json:"source"`
	Status   string `json:"status"`
	YAML     string `json:"yaml"`
}

type applicationRouteView struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Service string `json:"service"`
}

type applicationIngressView struct {
	URL    string                 `json:"url"`
	Routes []applicationRouteView `json:"routes"`
}

type applicationVersionGroup struct {
	Version string   `json:"version"`
	Build   string   `json:"build"`
	Nodes   []string `json:"nodes"`
}

type applicationVersionView struct {
	Version      string                    `json:"version"`
	Build        string                    `json:"build"`
	Rollout      string                    `json:"rollout,omitempty"`
	Distribution []applicationVersionGroup `json:"distribution"`
}

// appInspection returns the artifact identity known to this process: the
// deployed cluster's artifact when present, else the startup artifact.
func (c *applicationController) appInspection() (inspection artifact.Inspection, deployed bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.cluster != nil && c.cluster.artifact.ArtifactDigest != "" {
		return c.cluster.artifact, true
	}
	return c.startup, false
}

func (c *applicationController) appOverview(ctx context.Context, args []string) (any, error) {
	if len(args) != 0 {
		return nil, errConsoleArguments
	}
	inspection, _ := c.appInspection()
	status, _ := c.status(ctx)
	view := applicationOverviewView{
		Name:          activeApplication.Name,
		ApplicationID: activeApplication.ApplicationID,
		Version:       inspection.Manifest.CodeVersion,
		Build:         shortApplicationBuild(inspection.ArtifactDigest),
		Built:         buildTime(),
		SDKVersion:    sdkVersion(),
		GoVersion:     strings.TrimPrefix(runtime.Version(), "go"),
		Architecture:  runtime.GOOS + "/" + runtime.GOARCH,
		Nodes:         len(status.Nodes),
	}
	if inspection.Config != nil {
		view.Cluster = inspection.Config.Facts["cluster.name"]
	}
	if status.ActiveArtifact != nil && view.Version == "" {
		view.Version = status.ActiveArtifact.CodeVersion
	}
	c.mu.RLock()
	view.StartedAt = c.startedAt
	c.mu.RUnlock()
	return view, nil
}

func (c *applicationController) appConfig(ctx context.Context, args []string) (any, error) {
	if len(args) != 0 {
		return nil, errConsoleArguments
	}
	inspection, deployed := c.appInspection()
	view := applicationConfigView{Source: "embedded", Status: "not deployed"}
	if deployed {
		view.Status = "active"
	}
	if inspection.Config != nil {
		view.Revision = inspection.Config.Revision
	}
	view.YAML = string(inspection.CanonicalYAML)
	if view.YAML == "" && activeApplication.Configuration.Default != nil {
		configuration, err := activeApplication.Configuration.Default()
		if err != nil {
			return nil, fmt.Errorf("read default configuration: %w", err)
		}
		view.Revision = configuration.Revision
		view.YAML = string(configuration.CanonicalYAML)
	}
	if status, err := c.status(ctx); err == nil && status.ActiveArtifact != nil && status.ActiveArtifact.ConfigRevision != "" {
		view.Revision = status.ActiveArtifact.ConfigRevision
	}
	return view, nil
}

func (c *applicationController) appIngress(_ context.Context, args []string) (any, error) {
	if len(args) != 0 {
		return nil, errConsoleArguments
	}
	view := applicationIngressView{Routes: []applicationRouteView{}}
	c.mu.RLock()
	if c.cluster != nil && c.cluster.webAddress != "" {
		view.URL = "http://" + c.cluster.webAddress
	}
	c.mu.RUnlock()
	for _, component := range activeApplication.Components {
		for _, route := range component.Routes {
			view.Routes = append(view.Routes, applicationRouteView{
				Method: route.Method, Path: route.Path, Service: component.Name,
			})
		}
	}
	return view, nil
}

func (c *applicationController) appVersion(ctx context.Context, args []string) (any, error) {
	if len(args) != 0 {
		return nil, errConsoleArguments
	}
	inspection, _ := c.appInspection()
	status, _ := c.status(ctx)
	view := applicationVersionView{
		Version:      inspection.Manifest.CodeVersion,
		Build:        shortApplicationBuild(inspection.ArtifactDigest),
		Distribution: []applicationVersionGroup{},
	}
	if status.Rollout != nil {
		view.Rollout = status.Rollout.Phase
	}
	versions := make(map[string]string)
	for _, known := range []*ArtifactStatus{status.ActiveArtifact, status.CandidateArtifact} {
		if known != nil {
			versions[known.ArtifactDigest] = known.CodeVersion
		}
	}
	versions[inspection.ArtifactDigest] = inspection.Manifest.CodeVersion
	nodeDigests := make(map[string]string)
	for _, placement := range status.Placements {
		nodeDigests[placement.NodeID] = placement.ArtifactDigest
	}
	groups := make(map[string]*applicationVersionGroup)
	for _, node := range status.Nodes {
		digest := nodeDigests[node.NodeID]
		group, ok := groups[digest]
		if !ok {
			group = &applicationVersionGroup{Version: versions[digest], Build: shortApplicationBuild(digest)}
			groups[digest] = group
		}
		group.Nodes = append(group.Nodes, node.NodeID)
	}
	for _, group := range groups {
		view.Distribution = append(view.Distribution, *group)
	}
	sort.Slice(view.Distribution, func(i, j int) bool {
		return view.Distribution[i].Build < view.Distribution[j].Build
	})
	return view, nil
}

func buildTime() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.time" {
			if parsed, err := time.Parse(time.RFC3339, setting.Value); err == nil {
				return parsed.Local().Format("2006-01-02 15:04")
			}
		}
	}
	return ""
}

func sdkVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "devel"
	}
	if info.Main.Path == groveModulePath && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	for _, dependency := range info.Deps {
		if dependency.Path == groveModulePath {
			return dependency.Version
		}
	}
	return "devel"
}

func renderApplicationOverview(view applicationOverviewView, now time.Time) string {
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

func renderApplicationConfig(view applicationConfigView) string {
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

func renderApplicationIngress(view applicationIngressView) string {
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

func renderApplicationVersion(view applicationVersionView) string {
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
