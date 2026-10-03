package runtime

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/bootstrap"
	"github.com/grove-project/grove/internal/systemnats"
)

// grovlet is one node's supervisor process. It runs or joins the node's
// System NATS, serves the node's share of the control plane (membership,
// deployments, desired state, placement, health) and supervises the
// components placed on the node through its componentManager. It never runs
// application code: components execute in the node's application runtime
// or in isolated workers (docs/architecture/process-model.md).
type grovlet struct {
	server    *systemnats.Server
	transport *systemnats.Transport
	url       string
	routeURL  string

	membership *systemnats.Membership
	placement  *systemnats.Placement
	handlers   *systemnats.HandlerPlacements
	components *componentManager
	// metadataVoters is the JetStream metadata group size last observed by
	// this node's server; zero while unknown.
	metadataVoters atomic.Int32

	// Control-plane loops, stopped after the components.
	membershipLoop, deploymentsLoop, desiredLoop, placementLoop, healthLoop *background
	// Supervision loops, stopped before the components.
	eventsLoop, witnessLoop, recoveryLoop, reconcileLoop, handlersLoop *background
}

// background is one goroutine the Grovlet runs until it stops.
type background struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// goBackground runs run on its own goroutine until the returned background
// is stopped.
func goBackground(ctx context.Context, run func(context.Context)) *background {
	runCtx, cancel := context.WithCancel(ctx)
	loop := &background{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(loop.done)
		run(runCtx)
	}()
	return loop
}

// stop cancels the goroutine and waits for it. It may be called more than
// once, and a nil background is a no-op.
func (b *background) stop() {
	if b == nil {
		return
	}
	b.cancel()
	<-b.done
}

// controlState is the node's share of the control plane, as the Grovlet's
// supervision needs it.
type controlState struct {
	desired *systemnats.Desired
	health  *systemnats.Health
	// initialServiceIDs are the components this node starts with.
	initialServiceIDs []grove.ServiceID
}

// startGrovlet starts the node in phases: System NATS, the control plane
// when the node is clustered, the supervision of its components, and its
// endpoint. Any failure stops what already started.
func startGrovlet(ctx context.Context, cfg config) (_ *grovlet, err error) {
	g := &grovlet{}
	defer func() {
		if err != nil {
			g.stop()
		}
	}()
	if err := g.startSystemNATS(ctx, cfg); err != nil {
		return nil, err
	}
	if g.url == "" {
		return g, nil
	}
	if cfg.systemNATSMembership {
		var state controlState
		if cfg, state, err = g.serveControlPlane(ctx, cfg); err != nil {
			return nil, err
		}
		if err := g.superviseComponents(ctx, cfg, state); err != nil {
			return nil, err
		}
	}
	if err := g.serveEndpoint(ctx, cfg); err != nil {
		return nil, err
	}
	return g, nil
}

// startSystemNATS starts the node's embedded System NATS server, or uses the
// configured URL, and connects to it.
func (g *grovlet) startSystemNATS(ctx context.Context, cfg config) error {
	url := cfg.systemNATSURL
	if cfg.systemNATSListen != "" {
		host, port, err := parseListenAddress(cfg.systemNATSListen)
		if err != nil {
			return fmt.Errorf("parse System NATS client listener: %w", err)
		}
		if cfg.systemNATSRouteListen == "" {
			g.server, err = systemnats.StartServer(ctx, host, port)
		} else {
			routeHost, routePort, routeErr := parseListenAddress(cfg.systemNATSRouteListen)
			if routeErr != nil {
				return fmt.Errorf("parse System NATS route listener: %w", routeErr)
			}
			clusterConfig := systemnats.ClusterConfig{
				Name:      cfg.nodeID,
				Host:      host,
				Port:      port,
				RouteHost: routeHost,
				RoutePort: routePort,
			}
			if cfg.systemNATSSeed != "" {
				clusterConfig.SeedURLs = []string{cfg.systemNATSSeed}
			}
			if cfg.systemNATSMembership {
				clusterConfig.JetStreamStoreDir = filepath.Join(cfg.runtimeDir, "system-nats")
			}
			g.server, err = systemnats.StartClusterServer(ctx, clusterConfig)
		}
		if err != nil {
			return err
		}
		url = g.server.URL()
		g.routeURL = g.server.RouteURL()
	}
	if url == "" {
		return nil
	}
	g.url = url
	transport, err := systemnats.Connect(ctx, url)
	if err != nil {
		return err
	}
	g.transport = transport
	return nil
}

// serveControlPlane serves the node's membership, deployments, desired
// state, placement and health, restoring durable control state when the
// node restarts. It returns cfg with the cluster's ingress resolved.
func (g *grovlet) serveControlPlane(ctx context.Context, cfg config) (config, controlState, error) {
	restoreControlState := cfg.systemNATSRecovery && systemNATSStateExists(cfg.runtimeDir)
	transport := g.transport
	membership, err := systemnats.NewMembership(systemnats.MembershipRecord{
		NodeID:             cfg.nodeID,
		AdvertisedEndpoint: cfg.advertisedEndpoint,
	})
	if err != nil {
		return cfg, controlState{}, err
	}
	if err := transport.ServeMembership(ctx, cfg.nodeID, membership); err != nil {
		return cfg, controlState{}, err
	}
	g.membershipLoop = goBackground(ctx, func(ctx context.Context) { _ = membership.Run(ctx, transport) })
	g.membership = membership
	if g.server != nil {
		if err := transport.ServePeer(ctx, cfg.nodeID, g.server); err != nil {
			return cfg, controlState{}, err
		}
	}

	deployments := systemnats.NewDeployments()
	if err := transport.ServeDeployments(ctx, cfg.nodeID, deployments); err != nil {
		return cfg, controlState{}, err
	}
	g.deploymentsLoop = goBackground(ctx, func(ctx context.Context) { _ = deployments.Run(ctx, transport) })
	if cfg, err = resolveIngress(ctx, cfg, transport, deployments); err != nil {
		return cfg, controlState{}, err
	}
	if restoreControlState && cfg.hostAll {
		if cfg, err = rejoinRuntimePlacedCluster(ctx, cfg, transport); err != nil {
			return cfg, controlState{}, err
		}
	}

	desired := systemnats.NewDesired()
	serveDesired := func() error {
		if err := transport.ServeDesired(ctx, cfg.nodeID, desired); err != nil {
			return err
		}
		g.desiredLoop = goBackground(ctx, func(ctx context.Context) { _ = desired.Run(ctx, transport) })
		return nil
	}
	placementRecords := applicationPlacements(cfg)
	if cfg.adoptCluster {
		// A joining node hosts the components but does not claim their
		// placement: the founder's records stay authoritative and recovery
		// moves them when their node is lost.
		placementRecords = nil
	}
	initialServiceIDs := applicationPlacedServiceIDs(cfg)
	if restoreControlState {
		if err := serveDesired(); err != nil {
			return cfg, controlState{}, err
		}
		desiredView, err := waitForDesiredState(ctx, desired)
		if err != nil {
			return cfg, controlState{}, err
		}
		placementRecords, initialServiceIDs = applicationStartupState(cfg, desiredView)
	}

	placement, err := systemnats.NewPlacement(placementRecords)
	if err != nil {
		return cfg, controlState{}, err
	}
	if err := transport.ServePlacement(ctx, cfg.nodeID, placement); err != nil {
		return cfg, controlState{}, err
	}
	g.placementLoop = goBackground(ctx, func(ctx context.Context) { _ = placement.Run(ctx, transport) })
	g.placement = placement
	if !restoreControlState {
		if err := serveDesired(); err != nil {
			return cfg, controlState{}, err
		}
	}

	settled := func(nodes int) error {
		if g.server == nil {
			return nil
		}
		return controlPlaneSettled(int(g.metadataVoters.Load()), nodes)
	}
	health, err := systemnats.NewHealth(cfg.nodeID, membership, systemnats.HealthConfig{
		MinNodes: systemnats.MinClusterNodes,
		Settled:  settled,
	})
	if err != nil {
		return cfg, controlState{}, err
	}
	// The cluster serves placement only once it has enough nodes.
	placement.SetGate(func() error {
		joined := len(health.Snapshot().Nodes)
		if joined < systemnats.MinClusterNodes {
			return systemnats.ClusterFormingError(joined, systemnats.MinClusterNodes)
		}
		return settled(joined)
	})
	if err := transport.ServeClusterView(ctx, cfg.nodeID, health); err != nil {
		return cfg, controlState{}, err
	}
	g.healthLoop = goBackground(ctx, func(ctx context.Context) { _ = health.Run(ctx, transport) })
	g.startWitnessRelease(ctx, health)
	g.startControlPlaneEvents(ctx, cfg, health)
	return cfg, controlState{desired: desired, health: health, initialServiceIDs: initialServiceIDs}, nil
}

// superviseComponents starts the node's components in its application
// runtime or isolated workers, serves their lifecycle and debugging, and
// runs handler placement, recovery and desired-state reconciliation.
func (g *grovlet) superviseComponents(ctx context.Context, cfg config, state controlState) error {
	componentSpecs := applicationComponentSpecs(cfg)
	if cfg.systemNATSRecovery {
		componentSpecs = applicationRecoveryComponentSpecs(cfg)
	}
	g.components = newComponentManager(componentSpecs, newExecutionStarter(g.url, cfg.nodeID))
	if err := g.transport.ServeComponents(ctx, cfg.nodeID, g.components); err != nil {
		return err
	}
	if err := g.transport.ServeDebug(ctx, cfg.nodeID, newDebugController(cfg.nodeID, cfg.runtimeDir, cfg.delvePath, g.components)); err != nil {
		return err
	}
	if applicationDeclaresHandlers() {
		if err := g.startHandlerPlacement(ctx, cfg, state.health); err != nil {
			return err
		}
	}
	if err := g.components.start(ctx, state.initialServiceIDs); err != nil {
		return err
	}
	if cfg.systemNATSRecovery {
		recovery := newServiceRecovery(cfg.nodeID, state.health, g.placement, g.components, g.transport)
		g.recoveryLoop = goBackground(ctx, func(ctx context.Context) { _ = recovery.Run(ctx) })
		reconciler := &desiredReconciler{nodeID: cfg.nodeID, desired: state.desired, components: g.components}
		g.reconcileLoop = goBackground(ctx, func(ctx context.Context) { _ = reconciler.Run(ctx) })
	}
	return nil
}

// serveEndpoint answers the node's transport endpoint subject and reports
// the node's bootstrap readiness. The endpoint echoes requests; it hosts no
// application code.
func (g *grovlet) serveEndpoint(ctx context.Context, cfg config) error {
	if cfg.systemNATSSubject != "" {
		echo := systemnats.Handler(func(_ context.Context, request grove.RequestEnvelope) grove.ResponseEnvelope {
			return grove.ResponseEnvelope{Payload: request.Payload}
		})
		if err := g.transport.Serve(ctx, cfg.systemNATSSubject, echo); err != nil {
			return err
		}
	}
	if cfg.nodeID != "" {
		if err := g.transport.ServeBootstrapReadiness(ctx, bootstrap.Readiness{
			NodeID:         cfg.nodeID,
			ArtifactDigest: cfg.applicationArtifactDigest,
			State:          bootstrap.ReadinessHealthy,
		}); err != nil {
			return err
		}
	}
	return nil
}

func parseListenAddress(address string) (string, int, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return "", 0, err
	}
	if port < 0 || port > 65535 {
		return "", 0, syscall.EINVAL
	}
	return host, port, nil
}

// startControlPlaneEvents reports leader elections, voter changes and cluster
// formation as lifecycle events, so they appear in the cluster log view.
func (g *grovlet) startControlPlaneEvents(ctx context.Context, cfg config, health *systemnats.Health) {
	if g.server == nil {
		return
	}
	g.eventsLoop = goBackground(ctx, func(eventsCtx context.Context) {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		var (
			leader     string
			voters     int
			joined     = -1
			formed     bool
			everLeader bool
		)
		emit := func(event lifecycleEvent) {
			if cfg.emit == nil {
				return
			}
			event.NodeID = cfg.nodeID
			cfg.emit(event)
		}
		for {
			select {
			case <-ticker.C:
			case <-eventsCtx.Done():
				return
			}
			if count := len(health.Snapshot().Nodes); count != joined {
				joined = count
				if count >= systemnats.MinClusterNodes {
					if !formed {
						formed = true
						emit(lifecycleEvent{Event: "cluster_formed", Detail: fmt.Sprintf("%d of %d nodes joined", count, systemnats.MinClusterNodes)})
					}
				} else if !formed {
					emit(lifecycleEvent{Event: "cluster_forming", Detail: fmt.Sprintf("%d of %d nodes joined; not serving", count, systemnats.MinClusterNodes)})
				}
			}
			nextLeader, nextVoters := g.server.MetadataState()
			if nextVoters != 0 {
				// Keep the last known size through a failed read, so a
				// momentary gap does not count as a settled control plane.
				g.metadataVoters.Store(int32(nextVoters))
			}
			if nextVoters != 0 && nextVoters != voters {
				if voters != 0 {
					emit(lifecycleEvent{Event: "metadata_voters", Voters: nextVoters, Detail: fmt.Sprintf("changed from %d", voters)})
				}
				voters = nextVoters
			}
			switch {
			case nextLeader == leader:
			case nextLeader == "":
				emit(lifecycleEvent{Event: "metadata_leader_lost", Leader: leader, Voters: voters, Detail: "control plane has no leader; placement and cluster operations fail fast"})
			case leader == "" && !everLeader:
				emit(lifecycleEvent{Event: "metadata_leader_elected", Leader: nextLeader, Voters: voters})
			case leader == "":
				emit(lifecycleEvent{Event: "metadata_leader_elected", Leader: nextLeader, Voters: voters, Detail: "re-elected after leader loss"})
			default:
				emit(lifecycleEvent{Event: "metadata_leader_changed", Leader: nextLeader, Voters: voters, Detail: "from " + leader})
			}
			if nextLeader != "" {
				everLeader = true
			}
			leader = nextLeader
		}
	})
}

// controlPlaneSettled reports whether the JetStream metadata group has no more
// voters than joined nodes. Zero voters means the embedded server has not
// reported its metadata group yet. That is unknown, not settled: after a
// cluster restart the founder's bootstrap witness rejoins as an extra voter
// once the group forms, and serving placement before then would withdraw it
// again until the witness is released.
func controlPlaneSettled(voters, nodes int) error {
	if voters == 0 {
		return fmt.Errorf("%w: control-plane voters not yet observed", systemnats.ErrClusterSettling)
	}
	if voters > nodes {
		return systemnats.ClusterSettlingError(voters, nodes)
	}
	return nil
}

// startWitnessRelease retires the bootstrap metadata witness once three
// healthy logical nodes exist, so every node is one voter and the loss of any
// single node leaves a quorum that can re-elect a metadata leader.
func (g *grovlet) startWitnessRelease(ctx context.Context, health *systemnats.Health) {
	if g.server == nil {
		return
	}
	g.witnessLoop = goBackground(ctx, func(witnessCtx context.Context) {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
			case <-witnessCtx.Done():
				return
			}
			if !g.server.HasPeer() {
				continue
			}
			healthy := 0
			for _, node := range health.Snapshot().Nodes {
				if node.Health == systemnats.HealthHealthy {
					healthy++
				}
			}
			if healthy < systemnats.MinClusterNodes {
				continue
			}
			releaseCtx, releaseCancel := context.WithTimeout(witnessCtx, 30*time.Second)
			if g.server.ControlStateSettled(releaseCtx) {
				_ = g.server.ReleaseWitness(releaseCtx)
			}
			releaseCancel()
		}
	})
}

// stop stops the node: its supervision loops, its components, its handler
// placement and control-plane loops, then System NATS. It is safe on a
// partly started Grovlet.
func (g *grovlet) stop() {
	for _, loop := range []*background{g.eventsLoop, g.witnessLoop, g.reconcileLoop, g.recoveryLoop} {
		loop.stop()
	}
	if g.components != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = g.components.stopAll(stopCtx)
		cancel()
	}
	for _, loop := range []*background{g.handlersLoop, g.healthLoop, g.placementLoop, g.desiredLoop, g.deploymentsLoop, g.membershipLoop} {
		loop.stop()
	}
	if g.transport != nil {
		g.transport.Close()
		g.transport = nil
	}
	if g.server != nil {
		g.server.Shutdown()
		g.server = nil
	}
}
