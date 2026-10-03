package runtime

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/localcluster"
	"github.com/grove-project/grove/internal/rollout"
	"github.com/grove-project/grove/internal/systemnats"
)

var errApplicationAlreadyDeployed = errors.New("Grove application is already deployed")

type debugDemoActionResult struct {
	State  string        `json:"state"`
	WebURL string        `json:"web_url"`
	Status ClusterStatus `json:"status"`
}

// Summary describes the debug demo for interactive frontends.
func (r debugDemoActionResult) Summary() string {
	return fmt.Sprintf("Debug demo: %s\nWeb UI: %s", r.State, r.WebURL)
}

// startDebugDemo builds a debug-capable artifact, starts the application's
// fixed debug topology with Delve and activates the artifact.
func (d *applicationDemo) startDebugDemo(ctx context.Context, configPath string) (debugDemoActionResult, error) {
	delvePath, err := exec.LookPath("dlv")
	if err != nil {
		return debugDemoActionResult{}, fmt.Errorf("locate Delve for debug demo: %w", err)
	}
	compilation, err := compileApplicationConfiguration(configPath)
	if err != nil {
		return debugDemoActionResult{}, fmt.Errorf("compile debug demo configuration: %w", err)
	}
	artifactPath := filepath.Join(d.runtimeDir, "application-debug")
	inspection, err := artifact.EmbedFile(d.binaryPath, artifactPath, compilation)
	if err != nil {
		return debugDemoActionResult{}, fmt.Errorf("build debug-capable application artifact: %w", err)
	}
	cluster, err := startDebugApplicationCluster(ctx, artifactPath, delvePath, inspection)
	if err != nil {
		return debugDemoActionResult{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = cluster.local.Cleanup()
		}
	}()
	transport, err := systemnats.Connect(ctx, cluster.systemNATSURL)
	if err != nil {
		return debugDemoActionResult{}, fmt.Errorf("connect to debug demo cluster: %w", err)
	}
	defer transport.Close()
	if err := localcluster.WaitServing(ctx, transport, localcluster.Serving{
		Observer: localcluster.NodeID(0), Nodes: activeApplication.scenarioDebugNodeCount(),
		Placements: debugApplicationPlacements(), ArtifactDigest: inspection.ArtifactDigest, RequireWorker: true,
	}); err != nil {
		return debugDemoActionResult{}, fmt.Errorf("wait for debug demo placement: %w\n%s", err, cluster.local.Diagnostics())
	}
	operator, err := demoRollout(transport, localcluster.NodeID(0))
	if err != nil {
		return debugDemoActionResult{}, err
	}
	if _, err := operator.Activate(ctx, applicationArtifactRecord(inspection), debugApplicationGeneration()); err != nil {
		return debugDemoActionResult{}, fmt.Errorf("activate debug demo rollout: %w", err)
	}
	d.attach(cluster)
	d.event("started five-node debug demo")
	status, err := waitForClusterStatus(ctx, cluster, func(status ClusterStatus) bool {
		return debugApplicationStatusHealthy(status, inspection.ArtifactDigest)
	})
	if err != nil {
		d.attach(nil)
		return debugDemoActionResult{}, fmt.Errorf("wait for debug demo status: %w\n%s", err, cluster.local.Diagnostics())
	}
	cleanup = false
	return debugDemoActionResult{State: "active", WebURL: "http://" + cluster.webAddress, Status: status}, nil
}

func parseDebugDemoArguments(args []string) (string, error) {
	flags := flag.NewFlagSet("debug.demo.start", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := "configs/acme.yaml"
	flags.StringVar(&configPath, "config", configPath, "application YAML configuration path")
	if err := flags.Parse(args); err != nil {
		return "", fmt.Errorf("parse debug.demo.start arguments: %w", err)
	}
	if flags.NArg() != 0 {
		return "", fmt.Errorf("parse debug.demo.start arguments: %w: %q", errConsoleArguments, flags.Args())
	}
	return configPath, nil
}

func startDebugApplicationCluster(ctx context.Context, artifactPath, delvePath string, inspection artifact.Inspection) (*applicationCluster, error) {
	nodeCount := activeApplication.scenarioDebugNodeCount()
	if nodeCount < 1 {
		return nil, fmt.Errorf("start debug demo cluster: %w", errApplicationScenarioNodes)
	}
	components, err := applicationTopology(activeApplication.scenarioDebugPlacements())
	if err != nil {
		return nil, err
	}
	webAddress, err := defaultIngressAddress()
	if err != nil {
		return nil, err
	}
	local, err := localcluster.Start(ctx, artifactPath, localcluster.Spec{
		Nodes: nodeCount, Components: components, IngressAddress: webAddress,
		SubjectRoot: "_GROVE.system.application.debug.", DelvePath: delvePath,
	})
	if err != nil {
		return nil, fmt.Errorf("start debug demo cluster: %w", err)
	}
	return &applicationCluster{
		local: local, systemNATSURL: local.SystemNATSURL(),
		webAddress: webAddress, artifact: inspection, debugDemo: true,
	}, nil
}

func debugApplicationPlacements() map[grove.ServiceID]string {
	placements := make(map[grove.ServiceID]string, len(activeApplication.scenarioDebugPlacements()))
	for _, placement := range activeApplication.scenarioDebugPlacements() {
		placements[placement.ServiceID] = placement.NodeID
	}
	return placements
}

// debugApplicationGeneration is the debug demo's only rollout generation.
func debugApplicationGeneration() rollout.Generation {
	nodeIDs := make([]string, activeApplication.scenarioDebugNodeCount())
	for i := range nodeIDs {
		nodeIDs[i] = localcluster.NodeID(i)
	}
	return rollout.Generation{RolloutID: activeApplicationName() + "-debug-1", Generation: 1, NodeIDs: nodeIDs}
}

func debugApplicationStatusHealthy(status ClusterStatus, digest string) bool {
	if !status.Ready || status.Health != "healthy" || len(status.Nodes) != activeApplication.scenarioDebugNodeCount() ||
		len(status.Placements) != len(debugApplicationPlacements()) || status.ActiveArtifact == nil ||
		status.ActiveArtifact.ArtifactDigest != digest {
		return false
	}
	want := debugApplicationPlacements()
	for _, node := range status.Nodes {
		if node.Health != string(systemnats.HealthHealthy) {
			return false
		}
	}
	for _, placement := range status.Placements {
		if placement.NodeID != want[placement.ServiceID] || placement.Health != string(systemnats.ComponentHealthy) {
			return false
		}
	}
	return true
}
