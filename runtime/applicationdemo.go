package runtime

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/localcluster"
	"github.com/grove-project/grove/internal/rollout"
	"github.com/grove-project/grove/internal/systemnats"
)

var (
	errApplicationConfigRequired = errors.New("rollout configuration path is required")
	errCandidateMustFail         = errors.New("a second lifecycle rollout must exercise application-owned invalid configuration")
	// errApplicationScenarioNodes is returned by the scripted demo actions for
	// an application whose Scenario declares no demo nodes, such as one that
	// leaves component placement to the runtime (grove#43).
	errApplicationScenarioNodes = errors.New("application scenario declares no demo nodes")
)

// applicationDemo owns the console's scripted demo scenarios. Each runs
// against a fixed-topology cluster from the application's Scenario: a
// known-good rollout, the rejection of a broken candidate, recovery from a
// node failure and the debug demo. It launches nodes only through
// internal/localcluster and writes deployment intent only through
// internal/rollout; it reports progress and attaches the cluster it starts
// through the console's callbacks.
type applicationDemo struct {
	binaryPath string
	runtimeDir string
	// event reports progress to the console.
	event func(string)
	// attach shows a cluster in the console; nil detaches it.
	attach func(*applicationCluster)
}

type rolloutActionResult struct {
	State       string          `json:"state"`
	WebURL      string          `json:"web_url"`
	Transitions []ClusterStatus `json:"transitions"`
	Status      ClusterStatus   `json:"status"`
}

type resilienceActionResult struct {
	FailedNodeID    string        `json:"failed_node_id"`
	RecoveredNodeID string        `json:"recovered_node_id"`
	Result          any           `json:"result"`
	Status          ClusterStatus `json:"status"`
}

// Summary describes the rollout for interactive frontends.
func (r rolloutActionResult) Summary() string {
	return fmt.Sprintf("Rollout: %s\nWeb UI: %s", r.State, r.WebURL)
}

// Summary names the recovered service and the application probe's result.
func (r resilienceActionResult) Summary() string {
	serviceName := "Service"
	result := fmt.Sprintf("%v", r.Result)
	if scenario := activeApplication.Scenario; scenario != nil {
		serviceName = applicationServiceName(scenario.RecoveryServiceID)
		if scenario.ProbeSummary != nil {
			result = scenario.ProbeSummary(r.Result)
		}
	}
	return fmt.Sprintf("%s recovered: %s -> %s\nResult: %s", serviceName, r.FailedNodeID, r.RecoveredNodeID, result)
}

func parseRolloutArguments(args []string) (string, error) {
	flags := flag.NewFlagSet("rollout.start", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var configPath string
	flags.StringVar(&configPath, "config", "", "application YAML configuration path")
	if err := flags.Parse(args); err != nil {
		return "", fmt.Errorf("parse rollout.start arguments: %w", err)
	}
	if flags.NArg() != 0 {
		return "", fmt.Errorf("parse rollout.start arguments: %w: %q", errConsoleArguments, flags.Args())
	}
	if configPath == "" {
		return "", errApplicationConfigRequired
	}
	return configPath, nil
}

// demoRollout returns the rollout owner writing through nodeID.
func demoRollout(transport *systemnats.Transport, nodeID string) (*rollout.Operator, error) {
	store, err := systemnats.NewRolloutStore(transport, nodeID)
	if err != nil {
		return nil, err
	}
	return rollout.New(store), nil
}

// startKnownGood builds a configured artifact, starts the scenario cluster
// with it and activates it as rollout generation 1.
func (d *applicationDemo) startKnownGood(ctx context.Context, configPath string) (rolloutActionResult, error) {
	compilation, err := compileApplicationConfiguration(configPath)
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("compile known-good configuration: %w", err)
	}
	d.event("rollout: building configured artifact " + compilation.Revision)
	artifactPath := filepath.Join(d.runtimeDir, "application-active")
	inspection, err := artifact.EmbedFile(d.binaryPath, artifactPath, compilation)
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("build known-good application artifact: %w", err)
	}
	d.event("rollout: starting three Grovlets")
	cluster, err := startApplicationCluster(ctx, artifactPath, inspection)
	if err != nil {
		return rolloutActionResult{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = cluster.local.Cleanup()
		}
	}()
	transport, err := systemnats.Connect(ctx, cluster.systemNATSURL)
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("connect to application cluster: %w", err)
	}
	defer transport.Close()
	d.event("rollout: waiting for service placement")
	if err := localcluster.WaitServing(ctx, transport, applicationServing(inspection.ArtifactDigest)); err != nil {
		return rolloutActionResult{}, fmt.Errorf("wait for application placement: %w\n%s", err, cluster.local.Diagnostics())
	}
	operator, err := demoRollout(transport, localcluster.NodeID(0))
	if err != nil {
		return rolloutActionResult{}, err
	}
	if err := operator.RecordDesired(ctx, applicationDesiredDeployment(inspection, "node-2")); err != nil {
		return rolloutActionResult{}, err
	}
	d.event("rollout: activating " + inspection.Config.Revision)
	if _, err := operator.Activate(ctx, applicationArtifactRecord(inspection), applicationGeneration(1)); err != nil {
		return rolloutActionResult{}, fmt.Errorf("activate known-good rollout: %w", err)
	}
	d.attach(cluster)
	d.event("deployed " + inspection.Config.Revision)
	status, err := waitForClusterStatus(ctx, cluster, func(status ClusterStatus) bool {
		return applicationStatusHealthy(status, inspection.ArtifactDigest) && status.Rollout != nil && status.Rollout.Phase == string(systemnats.RolloutActive)
	})
	if err != nil {
		d.attach(nil)
		return rolloutActionResult{}, fmt.Errorf("wait for known-good application status: %w\n%s", err, cluster.local.Diagnostics())
	}
	cleanup = false
	return rolloutActionResult{
		State: "active", WebURL: "http://" + cluster.webAddress,
		Transitions: []ClusterStatus{status}, Status: status,
	}, nil
}

// rejectBrokenCandidate proposes a candidate built from the application's
// invalid configuration, observes that its runtime never becomes ready, and
// rolls the cluster back to the active artifact with the candidate's cause.
func (d *applicationDemo) rejectBrokenCandidate(ctx context.Context, cluster *applicationCluster, configPath string) (rolloutActionResult, error) {
	compilation, validation, err := compileInvalidCandidateConfiguration(configPath)
	if err != nil {
		return rolloutActionResult{}, err
	}
	d.event("rollout: building candidate " + compilation.Revision)
	candidatePath := filepath.Join(d.runtimeDir, "application-candidate")
	inspection, err := artifact.EmbedFile(d.binaryPath, candidatePath, compilation)
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("build candidate application artifact: %w", err)
	}
	transport, err := systemnats.Connect(ctx, cluster.systemNATSURL)
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("connect to application cluster: %w", err)
	}
	defer transport.Close()
	operator, err := demoRollout(transport, localcluster.NodeID(0))
	if err != nil {
		return rolloutActionResult{}, err
	}
	candidate := applicationArtifactRecord(inspection)
	d.event("rollout: recording candidate " + inspection.Config.Revision)
	pending, err := operator.Propose(ctx, cluster.artifact.ArtifactDigest, candidate, applicationGeneration(2))
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("record pending candidate: %w", err)
	}
	pendingStatus, err := waitForClusterStatus(ctx, cluster, func(status ClusterStatus) bool {
		return status.Rollout != nil && status.Rollout.Phase == string(systemnats.RolloutPending) &&
			status.CandidateArtifact != nil && status.CandidateArtifact.ArtifactDigest == candidate.ArtifactDigest
	})
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("observe pending candidate: %w", err)
	}
	targetServiceID := activeApplication.Scenario.RecoveryServiceID
	targetComponent, _ := activeApplication.componentByID(targetServiceID)
	d.event("rollout: starting candidate " + targetComponent.Name)
	candidateNode, err := localcluster.Launch(candidatePath, localcluster.NodeSpec{
		NodeID:        "node-2-candidate",
		Subject:       "_GROVE.system.application.candidate." + targetComponent.Kind,
		SystemNATSURL: cluster.systemNATSURL,
		Components:    []localcluster.NodeComponent{{Kind: targetComponent.Kind}},
	})
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("start %s candidate: %w", targetComponent.Name, err)
	}
	defer candidateNode.Cleanup()
	candidateCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	readyErr := candidateNode.WaitReady(candidateCtx)
	cancel()
	if readyErr == nil {
		return rolloutActionResult{}, fmt.Errorf("invalid %s candidate became ready", targetComponent.Name)
	}
	currentPlacement, err := transport.RequestPlacement(ctx, applicationObserver())
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("read known-good placement: %w", err)
	}
	routes := applicationUpgradeRoutes(currentPlacement.Placements, candidate.ArtifactDigest)
	failure := systemnats.RolloutFailure{
		Code: "candidate_startup_failed", Component: targetComponent.Name,
		Field: validation.Field, Message: validation.Message,
	}
	d.event("rollout: candidate unhealthy; rolling back")
	if _, err := operator.Rollback(ctx, pending, routes, failure); err != nil {
		return rolloutActionResult{}, fmt.Errorf("rollback failed candidate: %w", err)
	}
	status, err := waitForClusterStatus(ctx, cluster, func(status ClusterStatus) bool {
		return applicationStatusHealthy(status, cluster.artifact.ArtifactDigest) && status.Rollout != nil &&
			status.Rollout.Phase == string(systemnats.RolloutRolledBack) && status.Rollout.Failure != nil
	})
	if err != nil {
		return rolloutActionResult{}, fmt.Errorf("observe candidate rollback: %w", err)
	}
	d.event("rejected " + inspection.Config.Revision + ": " + failure.Field + " " + failure.Message)
	return rolloutActionResult{
		State: "rolled-back", WebURL: "http://" + cluster.webAddress,
		Transitions: []ClusterStatus{pendingStatus, status}, Status: status,
	}, nil
}

// runResilience kills the node hosting the application's recovery service,
// waits for the service to recover on another node, records the recovered
// inventory as the desired deployment and reruns the application probe. The
// returned result has no Status; the console reads it once it has recorded
// the failure.
func (d *applicationDemo) runResilience(ctx context.Context, cluster *applicationCluster) (resilienceActionResult, error) {
	scenario := activeApplication.Scenario
	if scenario == nil || scenario.RecoveryServiceID == 0 || scenario.Probe == nil || scenario.ProbeHealthy == nil {
		return resilienceActionResult{}, errors.New("application does not define a resilience scenario")
	}
	if cluster.local == nil {
		return resilienceActionResult{}, errApplicationNotDeployed
	}
	targetServiceID := scenario.RecoveryServiceID
	targetName := applicationServiceName(targetServiceID)
	transport, err := systemnats.Connect(ctx, cluster.systemNATSURL)
	if err != nil {
		return resilienceActionResult{}, fmt.Errorf("connect to application cluster: %w", err)
	}
	placement, err := transport.RequestPlacement(ctx, applicationObserver())
	transport.Close()
	if err != nil {
		return resilienceActionResult{}, fmt.Errorf("read %s placement: %w", targetName, err)
	}
	failedNodeID := applicationPlacementNode(placement.Placements, targetServiceID)
	if _, ok := cluster.local.Node(failedNodeID); !ok {
		return resilienceActionResult{}, fmt.Errorf("%s host %q is not a managed Grovlet", targetName, failedNodeID)
	}
	if err := cluster.local.Kill(ctx, failedNodeID); err != nil {
		return resilienceActionResult{}, fmt.Errorf("kill %s host %s: %w", targetName, failedNodeID, err)
	}
	survivor := applicationSurvivor(cluster.local, failedNodeID)
	if err := cluster.local.UseSystemNATSOf(survivor); err != nil {
		return resilienceActionResult{}, fmt.Errorf("reconnect after %s host failure: %w", targetName, err)
	}
	transport, err = systemnats.Connect(ctx, cluster.local.SystemNATSURL())
	if err != nil {
		return resilienceActionResult{}, fmt.Errorf("reconnect after %s host failure: %w", targetName, err)
	}
	defer transport.Close()
	recoveredNodeID, err := localcluster.WaitRecovery(ctx, transport, localcluster.Recovery{
		Observer: survivor, Nodes: cluster.local.Len(), FailedNodeID: failedNodeID,
		ServiceID: targetServiceID, ArtifactDigest: cluster.artifact.ArtifactDigest,
	})
	if err != nil {
		return resilienceActionResult{}, fmt.Errorf("recover %s after node loss: %w\n%s", targetName, err, cluster.local.Diagnostics())
	}
	operator, err := demoRollout(transport, survivor)
	if err != nil {
		return resilienceActionResult{}, err
	}
	if err := operator.RecordDesired(ctx, applicationDesiredDeployment(cluster.artifact, recoveredNodeID)); err != nil {
		return resilienceActionResult{}, err
	}
	result, err := waitForApplicationProbe(ctx, cluster.webAddress, "resilience-probe")
	if err != nil {
		return resilienceActionResult{}, fmt.Errorf("run application probe after %s recovery: %w", targetName, err)
	}
	if !scenario.ProbeHealthy(result) {
		return resilienceActionResult{}, fmt.Errorf("recovered application probe did not complete: %#v", result)
	}
	return resilienceActionResult{FailedNodeID: failedNodeID, RecoveredNodeID: recoveredNodeID, Result: result}, nil
}

// applicationSurvivor returns the first local node other than failedNodeID.
func applicationSurvivor(cluster *localcluster.Cluster, failedNodeID string) string {
	for _, nodeID := range cluster.NodeIDs() {
		if nodeID != failedNodeID {
			return nodeID
		}
	}
	return ""
}

func compileApplicationConfiguration(path string) (artifact.Compilation, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return artifact.Compilation{}, fmt.Errorf("read configuration %q: %w", path, err)
	}
	configuration, err := activeApplication.Configuration.Compile(source)
	if err != nil {
		return artifact.Compilation{}, err
	}
	if err := validateApplicationConfiguration(configuration); err != nil {
		return artifact.Compilation{}, err
	}
	return artifact.Compilation{
		ProtocolVersion: artifact.CompilerProtocolVersion,
		Revision:        configuration.Revision,
		Encoding:        configuration.Encoding,
		Payload:         configuration.Payload,
		CanonicalYAML:   configuration.CanonicalYAML,
		Facts:           configuration.Facts,
	}, nil
}

type candidateValidation struct {
	Field   string
	Message string
}

func compileInvalidCandidateConfiguration(path string) (artifact.Compilation, candidateValidation, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return artifact.Compilation{}, candidateValidation{}, fmt.Errorf("read configuration %q: %w", path, err)
	}
	if activeApplication.Scenario == nil || activeApplication.Scenario.InvalidConfig == nil {
		return artifact.Compilation{}, candidateValidation{}, errCandidateMustFail
	}
	configuration, field, message, err := activeApplication.Scenario.InvalidConfig(source)
	if err != nil {
		return artifact.Compilation{}, candidateValidation{}, err
	}
	if field == "" || message == "" {
		return artifact.Compilation{}, candidateValidation{}, errCandidateMustFail
	}
	if err := validateApplicationConfiguration(configuration); err != nil {
		return artifact.Compilation{}, candidateValidation{}, err
	}
	return artifact.Compilation{
		ProtocolVersion: artifact.CompilerProtocolVersion,
		Revision:        configuration.Revision,
		Encoding:        configuration.Encoding,
		Payload:         configuration.Payload,
		CanonicalYAML:   configuration.CanonicalYAML,
		Facts:           configuration.Facts,
	}, candidateValidation{Field: field, Message: message}, nil
}

// startApplicationCluster starts the application's scenario topology from
// artifactPath, with its ingress on a free loopback port.
func startApplicationCluster(
	ctx context.Context,
	artifactPath string,
	inspection artifact.Inspection,
) (*applicationCluster, error) {
	nodeCount := activeApplication.scenarioNodeCount()
	if nodeCount < 1 {
		return nil, fmt.Errorf("start application cluster: %w", errApplicationScenarioNodes)
	}
	components, err := applicationTopology(activeApplication.scenarioInitialPlacements())
	if err != nil {
		return nil, err
	}
	webAddress, err := defaultIngressAddress()
	if err != nil {
		return nil, err
	}
	local, err := localcluster.Start(ctx, artifactPath, localcluster.Spec{
		Nodes: nodeCount, Components: components, IngressAddress: webAddress,
		SubjectRoot: "_GROVE.system.application.", Recovery: true,
	})
	if err != nil {
		return nil, fmt.Errorf("start application cluster: %w", err)
	}
	return &applicationCluster{
		local: local, systemNATSURL: local.SystemNATSURL(),
		webAddress: webAddress, artifact: inspection,
	}, nil
}

// applicationTopology maps a Scenario's fixed placements to local cluster
// components. A component with an HTTP handler serves the cluster ingress.
func applicationTopology(placements []ScenarioPlacement) ([]localcluster.Component, error) {
	components := make([]localcluster.Component, 0, len(placements))
	for _, placement := range placements {
		component, ok := activeApplication.componentByID(placement.ServiceID)
		if !ok {
			return nil, fmt.Errorf("invalid application scenario placement: service %d on %q", placement.ServiceID, placement.NodeID)
		}
		components = append(components, localcluster.Component{
			NodeID: placement.NodeID, Kind: component.Kind, Options: placement.Options,
			Ingress: component.HTTPHandler != nil,
		})
	}
	return components, nil
}

// applicationObserver is the scenario node whose views the demos read.
func applicationObserver() string {
	return localcluster.NodeID(activeApplication.scenarioNodeCount() - 1)
}

// applicationServing is when the scenario cluster serves digest: every
// scenario node healthy and each scenario service on its scenario node.
func applicationServing(digest string) localcluster.Serving {
	placements := make(map[grove.ServiceID]string, len(activeApplication.scenarioInitialPlacements()))
	for _, placement := range activeApplication.scenarioInitialPlacements() {
		placements[placement.ServiceID] = placement.NodeID
	}
	return localcluster.Serving{
		Observer: applicationObserver(), Nodes: activeApplication.scenarioNodeCount(),
		Placements: placements, ArtifactDigest: digest,
	}
}

// applicationGeneration is rollout generation n of the scenario cluster.
func applicationGeneration(n uint64) rollout.Generation {
	nodeIDs := make([]string, activeApplication.scenarioNodeCount())
	for i := range nodeIDs {
		nodeIDs[i] = localcluster.NodeID(i)
	}
	return rollout.Generation{
		RolloutID:  activeApplicationName() + "-" + strconv.FormatUint(n, 10),
		Generation: n, NodeIDs: nodeIDs,
	}
}

func applicationDesiredDeployment(inspection artifact.Inspection, inventoryNodeID string) systemnats.DesiredDeployment {
	targetServiceID := activeApplication.Scenario.RecoveryServiceID
	components := make([]systemnats.DesiredComponent, 0, len(activeApplication.scenarioInitialPlacements()))
	for _, placement := range activeApplication.scenarioInitialPlacements() {
		nodeID := placement.NodeID
		if placement.ServiceID == targetServiceID {
			nodeID = inventoryNodeID
		}
		components = append(components, systemnats.DesiredComponent{ServiceID: placement.ServiceID, NodeID: nodeID})
	}
	return systemnats.DesiredDeployment{
		ApplicationID: inspection.Manifest.ApplicationID, Version: inspection.Manifest.CodeVersion,
		ArtifactDigest: inspection.ArtifactDigest,
		Components:     components,
	}
}

func applicationArtifactRecord(inspection artifact.Inspection) systemnats.DeploymentArtifact {
	return systemnats.DeploymentArtifact{
		ApplicationID: inspection.Manifest.ApplicationID, CodeVersion: inspection.Manifest.CodeVersion,
		CodeDigest: inspection.CodeDigest, ConfigRevision: inspection.Config.Revision,
		ConfigDigest: inspection.Config.Digest, ArtifactDigest: inspection.ArtifactDigest,
		ClusterID: inspection.Config.Facts["cluster.name"], NodeZone: inspection.Config.Facts["node.zone"],
	}
}

func applicationUpgradeRoutes(current []systemnats.PlacementRecord, candidateDigest string) []systemnats.UpgradeRoute {
	routes := make([]systemnats.UpgradeRoute, len(current))
	for i, route := range current {
		nodeID := "node-1-candidate"
		if route.ServiceID == activeApplication.Scenario.RecoveryServiceID {
			nodeID = "node-2-candidate"
		}
		routes[i] = systemnats.UpgradeRoute{
			Current: route,
			Candidate: systemnats.PlacementRecord{
				ServiceID: route.ServiceID, NodeID: nodeID,
				InvocationSubject: "_GROVE.system.application.candidate." + strconv.FormatUint(uint64(route.ServiceID), 10),
				ArtifactDigest:    candidateDigest,
			},
		}
	}
	return routes
}

func applicationPlacementNode(placements []systemnats.PlacementRecord, serviceID grove.ServiceID) string {
	for _, placement := range placements {
		if placement.ServiceID == serviceID {
			return placement.NodeID
		}
	}
	return ""
}

func applicationStatusHealthy(status ClusterStatus, digest string) bool {
	if !status.Ready || status.Health != "healthy" || len(status.Nodes) != activeApplication.scenarioNodeCount() || len(status.Placements) != len(activeApplication.scenarioInitialPlacements()) ||
		status.ActiveArtifact == nil || status.ActiveArtifact.ArtifactDigest != digest {
		return false
	}
	for _, node := range status.Nodes {
		if node.Health != string(systemnats.HealthHealthy) {
			return false
		}
	}
	for _, placement := range status.Placements {
		if placement.Health != string(systemnats.ComponentHealthy) {
			return false
		}
	}
	return true
}

// waitForApplicationProbe proves that the recovered placement can serve the
// application call boundary, not merely that its component was observed as
// healthy by the control plane.
func waitForApplicationProbe(ctx context.Context, webAddress, probeID string) (any, error) {
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		result, err := activeApplication.Scenario.Probe(ctx, webAddress, probeID)
		if err == nil {
			return result, nil
		}
		lastErr = err
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for application probe %q: %w", probeID, errors.Join(lastErr, ctx.Err()))
		}
	}
}
