package runtime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/grove-project/grove/console"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/localcluster"
	"github.com/grove-project/grove/internal/systemnats"
)

const (
	applicationConditionInterval = 25 * time.Millisecond
	applicationOperationTimeout  = time.Minute
)

var (
	errApplicationNotDeployed = errors.New("Grove application is not deployed")
)

// applicationController is the operator console's facade. It registers the
// console actions, serializes operations, and holds what the console shows:
// the attached cluster, the startup offer and the last event. The work
// behind each action belongs to an owner:
//
//   - applicationHost: hosting nodes in this process (discovery, start, join, leave)
//   - applicationDemo: the scripted demo scenarios (rollout, resilience, restart, debug demo)
//   - internal/rollout, via the demos: every deployment-intent write
//   - internal/localcluster, via host and demos: every node process
//   - internal/debuggateway: debugger sessions
type applicationController struct {
	host *applicationHost
	demo *applicationDemo

	// operationMu serializes operations that change the attached cluster.
	operationMu sync.Mutex

	mu             sync.RWMutex
	cluster        *applicationCluster
	startup        artifact.Inspection
	startAvailable bool
	joinAvailable  bool
	startupNodes   int
	lastEvent      string
	debugSessions  map[string]console.DebugSession
	startedAt      time.Time
}

// applicationCluster is the cluster the console is attached to.
type applicationCluster struct {
	// local holds the nodes this process hosts; nil while the console only
	// observes a discovered cluster.
	local         *localcluster.Cluster
	systemNATSURL string
	webAddress    string
	artifact      artifact.Inspection
	failedNodeID  string
	debugDemo     bool
	// discoveredNodes are cluster nodes learned from discovery, which the
	// console can read the cluster through before it hosts any.
	discoveredNodes []string
	// reader is the console's connection for inspecting this cluster.
	reader clusterReader
}

func newApplicationController(binaryPath, runtimeDir string) *applicationController {
	c := &applicationController{
		host:          newApplicationHost(binaryPath),
		debugSessions: make(map[string]console.DebugSession),
		startedAt:     time.Now(),
	}
	c.demo = &applicationDemo{
		binaryPath: binaryPath, runtimeDir: runtimeDir,
		event: c.setLastEvent, attach: c.attach,
	}
	return c
}

func registerApplicationConsoleActions(registry *console.Registry, controller *applicationController) error {
	actions := []console.Action{
		{
			Name: "app.overview", Label: "Overview", Section: "App",
			Description: "Show application identity, runtime, and deployment.",
			Handler:     controller.appOverview,
		},
		{
			Name: "app.config", Label: "Configuration", Section: "App",
			Description: "Inspect the active embedded configuration (read-only).",
			Handler:     controller.appConfig,
		},
		{
			Name: "app.ingress", Label: "Ingress", Section: "App",
			Description: "Inspect registered ingress routes.",
			Handler:     controller.appIngress,
		},
		{
			Name: "app.version", Label: "Version / Build", Section: "App",
			Description: "Show build identity and version distribution across nodes.",
			Handler:     controller.appVersion,
		},
		{
			Name: "services.view", Label: "Services", Section: "Services",
			Description: "Drill from services to handlers to concrete placements.",
			Handler:     controller.appServices,
		},
		{
			Name: "cluster.nodes", Label: "Nodes", Section: "Cluster",
			Description: "Show which handler placements each node hosts.",
			Handler:     controller.appServices,
		},
		{
			Name: "cluster.status", Label: "Status", Section: "Cluster",
			Description: "Read current application cluster and rollout state.",
			Handler: func(ctx context.Context, args []string) (any, error) {
				if len(args) != 0 {
					return nil, errConsoleArguments
				}
				return controller.status(ctx)
			},
		},
		{
			Name: "rollout.start", Label: "New rollout", Section: "Cluster", Hidden: true,
			Description: "Build and roll out an immutable configured application artifact.",
			Handler:     controller.startRollout,
		},
		{
			Name: "debug.demo.start", Label: "Debug demo > Start", Section: "Debug", Hidden: true,
			Description: "Start the application's deterministic debugging topology.",
			Handler:     controller.startDebugDemo,
		},
		{
			Name: "cluster.restart", Label: "Restart cluster", Section: "Cluster", Hidden: true,
			Description: "Restart every Grovlet from its durable runtime state.",
			Handler:     controller.restartCluster,
		},
		{
			Name: "resilience.run", Label: "Run resilience scenario", Section: "App", Hidden: true,
			Description: "Recover the application-selected service after its Grovlet fails, then rerun the application probe.",
			Handler:     controller.runResilience,
		},
		{
			Name: "logs.view", Label: "View logs", Section: "Logs",
			Description: "Explain health using application, cluster, and System NATS diagnostics.",
			Handler:     controller.logs,
		},
		{
			Name: "debug.attach", Label: "Attach debugger", Section: "Debug",
			Description: "Resolve an application service and expose its Delve DAP session locally.",
			Handler:     controller.attachDebugger,
		},
	}
	if controller.applicationStartAvailable() {
		actions = append(actions, console.Action{
			Name: "cluster.start", Label: "Start new cluster", Section: "Cluster",
			Description: "Start a new application cluster with this process hosting its first nodes (at least three).",
			Handler:     controller.startDiscoveredApplicationCluster,
		})
	}
	if controller.applicationJoinAvailable() {
		actions = append(actions, console.Action{
			Name: "cluster.join", Label: "Join cluster", Section: "Cluster",
			Description: "Add one or more nodes hosted by this process to the discovered application cluster.",
			Handler:     controller.joinApplicationCluster,
		})
	}
	for _, action := range actions {
		if err := registry.Register(action); err != nil {
			return fmt.Errorf("register Grove action %q: %w", action.Name, err)
		}
	}
	if activeApplication.RegisterActions != nil {
		if err := activeApplication.RegisterActions(registry); err != nil {
			return fmt.Errorf("register %s actions: %w", activeApplication.Name, err)
		}
	}
	return nil
}

// startRollout runs the scripted rollout demo: the first rollout deploys a
// known-good artifact, the next rejects a broken candidate.
func (c *applicationController) startRollout(ctx context.Context, args []string) (any, error) {
	operationCtx, cancel := context.WithTimeout(ctx, applicationOperationTimeout)
	defer cancel()
	configPath, err := parseRolloutArguments(args)
	if err != nil {
		return nil, err
	}
	c.setLastEvent("rollout: validating " + filepath.Base(configPath))
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	cluster := c.attached()
	var result rolloutActionResult
	if cluster == nil {
		result, err = c.demo.startKnownGood(operationCtx, configPath)
	} else {
		result, err = c.demo.rejectBrokenCandidate(operationCtx, cluster, configPath)
	}
	if err != nil {
		c.setLastEvent("rollout failed: " + err.Error())
	}
	return result, err
}

func (c *applicationController) runResilience(ctx context.Context, args []string) (any, error) {
	operationCtx, cancel := context.WithTimeout(ctx, applicationOperationTimeout)
	defer cancel()
	if len(args) != 0 {
		return nil, errConsoleArguments
	}
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	cluster := c.attached()
	if cluster == nil {
		return nil, errApplicationNotDeployed
	}
	c.mu.RLock()
	failedNodeID := cluster.failedNodeID
	c.mu.RUnlock()
	if failedNodeID != "" {
		return nil, errors.New("restart the cluster before running another resilience scenario")
	}
	result, err := c.demo.runResilience(operationCtx, cluster)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	cluster.failedNodeID = result.FailedNodeID
	cluster.systemNATSURL = cluster.local.SystemNATSURL()
	c.lastEvent = applicationServiceName(activeApplication.Scenario.RecoveryServiceID) + " recovered from " + result.FailedNodeID + " on " + result.RecoveredNodeID
	c.mu.Unlock()
	status, err := c.status(operationCtx)
	if err != nil {
		return nil, fmt.Errorf("read recovered application status: %w", err)
	}
	result.Status = status
	return result, nil
}

func (c *applicationController) restartCluster(ctx context.Context, args []string) (any, error) {
	operationCtx, cancel := context.WithTimeout(ctx, applicationOperationTimeout)
	defer cancel()
	if len(args) != 0 {
		return nil, errConsoleArguments
	}
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	cluster := c.attached()
	if cluster == nil || cluster.local == nil {
		return nil, errApplicationNotDeployed
	}
	c.mu.RLock()
	failedNodeID := cluster.failedNodeID
	c.mu.RUnlock()
	if err := cluster.local.Restart(operationCtx, failedNodeID); err != nil {
		return nil, err
	}
	c.mu.Lock()
	cluster.systemNATSURL = cluster.local.SystemNATSURL()
	cluster.failedNodeID = ""
	c.lastEvent = "reconstructed deployment from durable state"
	c.mu.Unlock()
	status, err := waitForClusterStatus(operationCtx, cluster, func(status ClusterStatus) bool {
		return applicationStatusHealthy(status, cluster.artifact.ArtifactDigest)
	})
	if err != nil {
		return nil, fmt.Errorf("wait for reconstructed application status: %w\n%s", err, cluster.local.Diagnostics())
	}
	return status, nil
}

func (c *applicationController) startDebugDemo(ctx context.Context, args []string) (any, error) {
	operationCtx, cancel := context.WithTimeout(ctx, applicationOperationTimeout)
	defer cancel()
	configPath, err := parseDebugDemoArguments(args)
	if err != nil {
		return nil, err
	}
	c.operationMu.Lock()
	defer c.operationMu.Unlock()
	if c.attached() != nil {
		return nil, errApplicationAlreadyDeployed
	}
	result, err := c.demo.startDebugDemo(operationCtx, configPath)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// attached returns the cluster the console is attached to, or nil.
func (c *applicationController) attached() *applicationCluster {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cluster
}

// attach makes cluster the one the console shows; nil detaches.
func (c *applicationController) attach(cluster *applicationCluster) {
	c.mu.Lock()
	previous := c.cluster
	c.cluster = cluster
	c.mu.Unlock()
	if previous != nil && previous != cluster {
		previous.reader.close()
	}
}

// inspection returns what reading the attached cluster needs, and false
// when the console is not attached to one.
func (c *applicationController) inspection() (inspectionTarget, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.cluster == nil {
		return inspectionTarget{}, false
	}
	return c.cluster.target(), true
}

// status reads the attached cluster's status through its control plane.
func (c *applicationController) status(ctx context.Context) (ClusterStatus, error) {
	target, attached := c.inspection()
	if !attached {
		return ClusterStatus{Health: "not-deployed", Nodes: []NodeStatus{}, Placements: []PlacementStatus{}}, nil
	}
	return target.status(ctx)
}

func (c *applicationController) readModel(ctx context.Context) (console.Model, error) {
	status, err := c.status(ctx)
	model := console.Model{
		Application:   activeApplication.Name,
		Sections:      []string{"App", "Cluster", "Services", "Debug", "Logs"},
		Health:        status.Health,
		NodesTotal:    len(status.Nodes),
		ServicesTotal: len(status.Placements),
	}
	if err != nil {
		model.Health = "starting"
	}
	for _, node := range status.Nodes {
		if node.Health == string(systemnats.HealthHealthy) {
			model.NodesHealthy++
		}
	}
	for _, placement := range status.Placements {
		if placement.Health == string(systemnats.ComponentHealthy) || placement.Health == string(systemnats.ComponentDebugging) {
			model.ServicesHealthy++
		}
	}
	if status.ActiveArtifact != nil {
		model.ActiveVersion = status.ActiveArtifact.CodeVersion
		model.ConfigRevision = status.ActiveArtifact.ConfigRevision
	}
	if status.CandidateArtifact != nil {
		model.CandidateRevision = status.CandidateArtifact.ConfigRevision
	}
	if status.Rollout != nil {
		model.RolloutPhase = status.Rollout.Phase
	}
	c.mu.RLock()
	model.LastEvent = c.lastEvent
	if err != nil && model.LastEvent == "" {
		model.LastEvent = "waiting for Grove control-plane state: " + err.Error()
	}
	if c.cluster != nil && c.cluster.webAddress != "" {
		model.IngressURL = "http://" + c.cluster.webAddress
	}
	model.DebugSessions = copyDebugSessions(c.debugSessions)
	if c.startAvailable {
		model.StartupAction = "cluster.start"
	}
	if c.joinAvailable {
		model.StartupAction = "cluster.join"
		model.StartupNodes = c.startupNodes
		model.StartupStatus = "Healthy"
	}
	if model.StartupAction != "" {
		model.Application = activeApplication.Name
		model.StartupCluster = c.startup.Config.Facts["cluster.name"]
		model.StartupBuild = shortApplicationBuild(c.startup.ArtifactDigest)
	}
	c.mu.RUnlock()
	return model, nil
}

func shortApplicationBuild(digest string) string {
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) > 7 {
		return digest[:7]
	}
	return digest
}

func (c *applicationController) setLastEvent(event string) {
	c.mu.Lock()
	c.lastEvent = event
	c.mu.Unlock()
}

// close detaches the console. Nodes this process hosts in a discovered
// cluster leave it gracefully; a demo cluster is torn down.
func (c *applicationController) close() {
	c.mu.Lock()
	cluster := c.cluster
	c.cluster = nil
	clear(c.debugSessions)
	c.mu.Unlock()
	if cluster != nil {
		cluster.reader.close()
	}
	if c.host.hosting() {
		c.host.leave()
		return
	}
	if cluster != nil && cluster.local != nil {
		_ = cluster.local.Cleanup()
	}
	c.host.close()
}
