package runtime

import (
	"context"
	"fmt"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/consoleview"
)

const groveModulePath = "github.com/grove-project/grove"

// The App screens' results are the console view models.
type (
	applicationOverviewView = consoleview.Overview
	applicationConfigView   = consoleview.Config
	applicationRouteView    = consoleview.Route
	applicationIngressView  = consoleview.Ingress
	applicationVersionGroup = consoleview.VersionGroup
	applicationVersionView  = consoleview.Version
)

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
