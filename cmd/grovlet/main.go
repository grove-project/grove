// Command grovlet is the executable entry point for a Grove node.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/internal/systemnats"
)

var (
	errRuntimeDirRequired            = errors.New("runtime directory is required")
	errSystemNATSConflict            = errors.New("embedded System NATS configuration and URL are mutually exclusive")
	errSystemNATSRequired            = errors.New("system NATS endpoint requires a listen address or URL")
	errSystemNATSRouteListenRequired = errors.New("system NATS route listener requires an embedded client listener")
	errSystemNATSSeedRouteRequired   = errors.New("system NATS seed requires a route listener")
	errSystemNATSClusterIdentity     = errors.New("system NATS clustering requires node identity")
	errSystemNATSMembershipCluster   = errors.New("system NATS membership requires a route listener and seed")
	errSystemNATSRecoveryCluster     = errors.New("system NATS recovery requires membership")
	errGroveShopEndpoint             = errors.New("reference application service placement requires a System NATS endpoint")
	errGroveShopPlacementCluster     = errors.New("placed Grove Shop components require System NATS membership")
	errGroveShopOrdersConflict       = errors.New("orders placement and explicit Inventory destination are mutually exclusive")
	errGroveShopWebListenRequired    = errors.New("Grove Shop Web requires an HTTP listen address")
	errNodeIdentityPair              = errors.New("node ID and advertised endpoint must be configured together")
	errNodeIDInvalid                 = errors.New("node ID is invalid")
	errAdvertiseInvalid              = errors.New("advertised endpoint is invalid")
)

type config struct {
	runtimeDir                string
	systemNATSListen          string
	systemNATSRouteListen     string
	systemNATSSeed            string
	systemNATSMembership      bool
	systemNATSRecovery        bool
	systemNATSURL             string
	systemNATSSubject         string
	groveShopOrders           bool
	groveShopInventory        bool
	groveShopInventorySubject string
	groveShopWeb              bool
	groveShopWebListen        string
	groveShopConfiguration    groveshop.Configuration
	groveShopConfigDigest     string
	groveShopArtifactDigest   string
	nodeID                    string
	advertisedEndpoint        string
}

type lifecycleEvent struct {
	Event              string `json:"event"`
	NodeID             string `json:"node_id,omitempty"`
	AdvertisedEndpoint string `json:"advertised_endpoint,omitempty"`
	SystemNATSURL      string `json:"system_nats_url,omitempty"`
	SystemNATSRouteURL string `json:"system_nats_route_url,omitempty"`
	ConfigRevision     string `json:"config_revision,omitempty"`
	ConfigDigest       string `json:"config_digest,omitempty"`
	ArtifactDigest     string `json:"artifact_digest,omitempty"`
	ClusterName        string `json:"cluster_name,omitempty"`
	NodeZone           string `json:"node_zone,omitempty"`
}

type runtimeDirError struct {
	path string
	err  error
}

func (e runtimeDirError) Error() string {
	return fmt.Sprintf("runtime directory %q: %v", e.path, e.err)
}

func (e runtimeDirError) Unwrap() error {
	return e.err
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()

	args := os.Args[1:]
	if len(args) != 0 && args[0] == "bootstrap-hello" {
		if err := runBootstrapHello(args[1:], os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "grovlet: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if len(args) != 0 && args[0] == "config-compile" {
		if err := runConfigCompile(ctx, args[1:], os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "grovlet: %v\n", err)
			os.Exit(1)
		}
		return
	}
	runCommand := run
	if len(args) != 0 && args[0] == "worker" {
		args = args[1:]
		runCommand = runWorker
	}
	if err := runCommand(ctx, args, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "grovlet: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	inspection, configuration, err := loadEmbeddedGroveShopConfiguration()
	if err != nil {
		return fmt.Errorf("verify embedded Grove Shop artifact: %w", err)
	}
	cfg, err := parseConfig(args, stderr)
	if err != nil {
		return err
	}
	cfg.groveShopConfiguration = configuration
	cfg.groveShopConfigDigest = inspection.Config.Digest
	cfg.groveShopArtifactDigest = inspection.ArtifactDigest
	if err := prepareRuntimeDir(cfg.runtimeDir); err != nil {
		return err
	}
	systemRuntime, err := startSystemNATS(ctx, cfg)
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(stdout)
	ready := lifecycleEvent{
		Event:              "ready",
		NodeID:             cfg.nodeID,
		AdvertisedEndpoint: cfg.advertisedEndpoint,
		SystemNATSURL:      systemRuntime.url,
		SystemNATSRouteURL: systemRuntime.routeURL,
	}
	if !inspection.ConfigEmpty {
		ready.ConfigRevision = configuration.Revision
		ready.ConfigDigest = inspection.Config.Digest
		ready.ArtifactDigest = inspection.ArtifactDigest
		ready.ClusterName = configuration.Cluster.Name
		ready.NodeZone = configuration.Node.Zone
	}
	if err := encoder.Encode(ready); err != nil {
		systemRuntime.stop()
		return fmt.Errorf("encode ready event: %w", err)
	}

	<-ctx.Done()
	systemRuntime.stop()

	if err := encoder.Encode(lifecycleEvent{Event: "stopped"}); err != nil {
		return fmt.Errorf("encode stopped event: %w", err)
	}
	return nil
}

func parseConfig(args []string, stderr io.Writer) (config, error) {
	var cfg config
	flags := flag.NewFlagSet("grovlet", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&cfg.runtimeDir, "runtime-dir", "", "directory for Grovlet runtime state")
	flags.StringVar(&cfg.systemNATSListen, "system-nats-listen", "", "loopback address for an embedded System NATS server")
	flags.StringVar(&cfg.systemNATSRouteListen, "system-nats-route-listen", "", "address for the embedded System NATS route listener")
	flags.StringVar(&cfg.systemNATSSeed, "system-nats-seed", "", "explicit System NATS seed route URL")
	flags.BoolVar(&cfg.systemNATSMembership, "system-nats-membership", false, "register and observe replicated Grove membership")
	flags.BoolVar(&cfg.systemNATSRecovery, "system-nats-recovery", false, "recover placed services from unavailable Grovlets")
	flags.StringVar(&cfg.systemNATSURL, "system-nats-url", "", "System NATS server URL")
	flags.StringVar(&cfg.systemNATSSubject, "system-nats-subject", "", "System NATS transport endpoint subject")
	flags.BoolVar(&cfg.groveShopOrders, "grove-shop-orders", false, "place Grove Shop Orders on this Grovlet")
	flags.BoolVar(&cfg.groveShopInventory, "grove-shop-inventory", false, "host Grove Shop Inventory on this Grovlet")
	flags.StringVar(&cfg.groveShopInventorySubject, "grove-shop-orders-inventory-subject", "", "explicit Inventory endpoint for Grove Shop Orders")
	flags.BoolVar(&cfg.groveShopWeb, "grove-shop-web", false, "place Grove Shop Web on this Grovlet")
	flags.StringVar(&cfg.groveShopWebListen, "grove-shop-web-listen", "", "HTTP listen address for Grove Shop Web")
	flags.StringVar(&cfg.nodeID, "node-id", "", "stable process-lifetime Grove node ID")
	flags.StringVar(&cfg.advertisedEndpoint, "advertise-endpoint", "", "advertised Grove transport endpoint URL")
	if err := flags.Parse(args); err != nil {
		return config{}, fmt.Errorf("parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected arguments: %q", flags.Args())
	}
	if cfg.runtimeDir == "" {
		return config{}, errRuntimeDirRequired
	}
	if cfg.systemNATSURL != "" && (cfg.systemNATSListen != "" || cfg.systemNATSRouteListen != "" || cfg.systemNATSSeed != "") {
		return config{}, errSystemNATSConflict
	}
	if cfg.systemNATSRouteListen != "" && cfg.systemNATSListen == "" {
		return config{}, errSystemNATSRouteListenRequired
	}
	if cfg.systemNATSSeed != "" && cfg.systemNATSRouteListen == "" {
		return config{}, errSystemNATSSeedRouteRequired
	}
	if cfg.systemNATSSeed != "" {
		seed, err := url.Parse(cfg.systemNATSSeed)
		if err != nil || seed.Scheme != "nats-route" || seed.Host == "" {
			return config{}, fmt.Errorf(
				"validate System NATS seed: %w",
				errors.Join(systemnats.ErrSeedURLInvalid, err),
			)
		}
	}
	if cfg.systemNATSMembership && (cfg.systemNATSRouteListen == "" || cfg.systemNATSSeed == "") {
		return config{}, errSystemNATSMembershipCluster
	}
	if cfg.systemNATSRecovery && !cfg.systemNATSMembership {
		return config{}, errSystemNATSRecoveryCluster
	}
	if cfg.systemNATSSubject != "" && cfg.systemNATSListen == "" && cfg.systemNATSURL == "" {
		return config{}, errSystemNATSRequired
	}
	if (cfg.groveShopOrders || cfg.groveShopInventory || cfg.groveShopInventorySubject != "" || cfg.groveShopWeb || cfg.systemNATSRecovery) && cfg.systemNATSSubject == "" {
		return config{}, errGroveShopEndpoint
	}
	if (cfg.groveShopOrders || cfg.groveShopWeb) && !cfg.systemNATSMembership {
		return config{}, errGroveShopPlacementCluster
	}
	if cfg.groveShopOrders && cfg.groveShopInventorySubject != "" {
		return config{}, errGroveShopOrdersConflict
	}
	if cfg.groveShopWeb && cfg.groveShopWebListen == "" {
		return config{}, errGroveShopWebListenRequired
	}
	if !cfg.groveShopWeb && cfg.groveShopWebListen != "" {
		return config{}, errGroveShopWebListenRequired
	}
	if (cfg.nodeID == "") != (cfg.advertisedEndpoint == "") {
		return config{}, errNodeIdentityPair
	}
	if cfg.nodeID != "" && !validNodeID(cfg.nodeID) {
		return config{}, errNodeIDInvalid
	}
	if cfg.advertisedEndpoint != "" {
		endpoint, err := url.Parse(cfg.advertisedEndpoint)
		if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
			return config{}, fmt.Errorf("validate advertised endpoint: %w", errors.Join(errAdvertiseInvalid, err))
		}
	}
	if cfg.systemNATSRouteListen != "" && cfg.nodeID == "" {
		return config{}, errSystemNATSClusterIdentity
	}
	return cfg, nil
}

func validNodeID(nodeID string) bool {
	for i, character := range nodeID {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' {
			continue
		}
		if i != 0 && (character == '.' || character == '_' || character == '-') {
			continue
		}
		return false
	}
	return nodeID != ""
}

type systemNATSRuntime struct {
	server            *systemnats.Server
	transport         *systemnats.Transport
	url               string
	routeURL          string
	membershipCancel  context.CancelFunc
	membershipDone    chan struct{}
	placementCancel   context.CancelFunc
	placementDone     chan struct{}
	healthCancel      context.CancelFunc
	healthDone        chan struct{}
	components        *componentManager
	desiredCancel     context.CancelFunc
	desiredDone       chan struct{}
	deploymentsCancel context.CancelFunc
	deploymentsDone   chan struct{}
	recoveryCancel    context.CancelFunc
	recoveryDone      chan struct{}
	reconcileCancel   context.CancelFunc
	reconcileDone     chan struct{}
}

func startSystemNATS(ctx context.Context, cfg config) (*systemNATSRuntime, error) {
	restoreControlState := cfg.systemNATSRecovery && systemNATSStateExists(cfg.runtimeDir)
	runtime := &systemNATSRuntime{}
	url := cfg.systemNATSURL
	if cfg.systemNATSListen != "" {
		host, port, err := parseListenAddress(cfg.systemNATSListen)
		if err != nil {
			return nil, fmt.Errorf("parse System NATS client listener: %w", err)
		}
		if cfg.systemNATSRouteListen == "" {
			runtime.server, err = systemnats.StartServer(ctx, host, port)
		} else {
			routeHost, routePort, routeErr := parseListenAddress(cfg.systemNATSRouteListen)
			if routeErr != nil {
				return nil, fmt.Errorf("parse System NATS route listener: %w", routeErr)
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
			runtime.server, err = systemnats.StartClusterServer(ctx, clusterConfig)
		}
		if err != nil {
			return nil, err
		}
		url = runtime.server.URL()
		runtime.routeURL = runtime.server.RouteURL()
	}
	if url == "" {
		return runtime, nil
	}
	runtime.url = url

	transport, err := systemnats.Connect(ctx, url)
	if err != nil {
		runtime.stop()
		return nil, err
	}
	runtime.transport = transport
	var placement *systemnats.Placement
	if cfg.systemNATSMembership {
		membership, err := systemnats.NewMembership(systemnats.MembershipRecord{
			NodeID:             cfg.nodeID,
			AdvertisedEndpoint: cfg.advertisedEndpoint,
		})
		if err != nil {
			runtime.stop()
			return nil, err
		}
		if err := transport.ServeMembership(ctx, cfg.nodeID, membership); err != nil {
			runtime.stop()
			return nil, err
		}
		runtime.startMembership(ctx, membership)

		deployments := systemnats.NewDeployments()
		if err := transport.ServeDeployments(ctx, cfg.nodeID, deployments); err != nil {
			runtime.stop()
			return nil, err
		}
		runtime.startDeployments(ctx, deployments)

		desired := systemnats.NewDesired()
		placementRecords := groveShopPlacements(cfg)
		initialServiceIDs := groveShopPlacedServiceIDs(cfg)
		if restoreControlState {
			if err := transport.ServeDesired(ctx, cfg.nodeID, desired); err != nil {
				runtime.stop()
				return nil, err
			}
			runtime.startDesired(ctx, desired)
			desiredView, err := waitForDesiredState(ctx, desired)
			if err != nil {
				runtime.stop()
				return nil, err
			}
			placementRecords, initialServiceIDs = groveShopStartupState(cfg, desiredView)
		}

		placement, err = systemnats.NewPlacement(placementRecords)
		if err != nil {
			runtime.stop()
			return nil, err
		}
		if err := transport.ServePlacement(ctx, cfg.nodeID, placement); err != nil {
			runtime.stop()
			return nil, err
		}
		runtime.startPlacement(ctx, placement)
		if !restoreControlState {
			if err := transport.ServeDesired(ctx, cfg.nodeID, desired); err != nil {
				runtime.stop()
				return nil, err
			}
			runtime.startDesired(ctx, desired)
		}

		health, err := systemnats.NewHealth(cfg.nodeID, membership, systemnats.HealthConfig{})
		if err != nil {
			runtime.stop()
			return nil, err
		}
		if err := transport.ServeClusterView(ctx, cfg.nodeID, health); err != nil {
			runtime.stop()
			return nil, err
		}
		runtime.startHealth(ctx, health)

		componentSpecs := groveShopComponentSpecs(cfg)
		if cfg.systemNATSRecovery {
			componentSpecs = groveShopRecoveryComponentSpecs(cfg)
		}
		runtime.components = newComponentManager(
			componentSpecs,
			newWorkerStarter(url, cfg.nodeID),
		)
		if err := transport.ServeComponents(ctx, cfg.nodeID, runtime.components); err != nil {
			runtime.stop()
			return nil, err
		}
		if err := runtime.components.start(ctx, initialServiceIDs); err != nil {
			runtime.stop()
			return nil, err
		}
		if cfg.systemNATSRecovery {
			runtime.startRecovery(ctx, newServiceRecovery(cfg.nodeID, health, placement, runtime.components, transport))
			runtime.startReconciler(ctx, &desiredReconciler{nodeID: cfg.nodeID, desired: desired, components: runtime.components})
		}
	}
	if cfg.systemNATSSubject != "" {
		handler := systemnats.Handler(func(_ context.Context, request grove.RequestEnvelope) grove.ResponseEnvelope {
			return grove.ResponseEnvelope{Payload: request.Payload}
		})
		if !cfg.systemNATSMembership && (cfg.groveShopInventory || cfg.groveShopInventorySubject != "") {
			registry := &grove.Registry{}
			if cfg.groveShopInventory {
				inventory := groveshop.NewInventory(cfg.groveShopConfiguration.Inventory.ReservationBuffer)
				if err := groveshop.RegisterInventory(registry, inventory); err != nil {
					runtime.stop()
					return nil, fmt.Errorf("register Grove Shop Inventory: %w", err)
				}
			}
			if cfg.groveShopInventorySubject != "" {
				var inventoryClient *grove.Client
				inventoryClient, err = transport.RoutedClient(cfg.groveShopInventorySubject)
				if err != nil {
					runtime.stop()
					return nil, err
				}
				orders := groveshop.NewGroveOrders(
					inventoryClient,
					&groveshop.Payment{},
					&groveshop.Shipping{},
				)
				if err := groveshop.RegisterOrders(registry, orders); err != nil {
					runtime.stop()
					return nil, fmt.Errorf("register Grove Shop Orders: %w", err)
				}
			}
			dispatcher, err := grove.NewDispatcher(registry)
			if err != nil {
				runtime.stop()
				return nil, err
			}
			handler = func(ctx context.Context, request grove.RequestEnvelope) grove.ResponseEnvelope {
				return dispatcher.Dispatch(ctx, request)
			}
		}
		if err := transport.Serve(
			ctx,
			cfg.systemNATSSubject,
			handler,
		); err != nil {
			runtime.stop()
			return nil, err
		}
	}
	return runtime, nil
}

func systemNATSStateExists(runtimeDir string) bool {
	_, err := os.Stat(filepath.Join(runtimeDir, "system-nats"))
	return err == nil
}

func groveShopPlacements(cfg config) []systemnats.PlacementRecord {
	placements := make([]systemnats.PlacementRecord, 0, 3)
	if cfg.groveShopOrders {
		placements = append(placements, systemnats.PlacementRecord{
			ServiceID:         groveshop.ServiceOrders,
			NodeID:            cfg.nodeID,
			InvocationSubject: componentInvocationSubject(cfg.systemNATSSubject, groveshop.ServiceOrders),
			ArtifactDigest:    cfg.groveShopArtifactDigest,
		})
	}
	if cfg.groveShopInventory {
		placements = append(placements, systemnats.PlacementRecord{
			ServiceID:         groveshop.ServiceInventory,
			NodeID:            cfg.nodeID,
			InvocationSubject: componentInvocationSubject(cfg.systemNATSSubject, groveshop.ServiceInventory),
			ArtifactDigest:    cfg.groveShopArtifactDigest,
		})
	}
	if cfg.groveShopWeb {
		placements = append(placements, systemnats.PlacementRecord{
			ServiceID:         groveshop.ServiceWeb,
			NodeID:            cfg.nodeID,
			InvocationSubject: componentInvocationSubject(cfg.systemNATSSubject, groveshop.ServiceWeb),
			ArtifactDigest:    cfg.groveShopArtifactDigest,
		})
	}
	return placements
}

func groveShopComponentSpecs(cfg config) []componentSpec {
	components := make([]componentSpec, 0, 3)
	if cfg.groveShopOrders {
		components = append(components, componentSpec{
			serviceID: groveshop.ServiceOrders,
			name:      "Orders",
			kind:      workerOrders,
			subject:   componentInvocationSubject(cfg.systemNATSSubject, groveshop.ServiceOrders),
		})
	}
	if cfg.groveShopInventory {
		components = append(components, componentSpec{
			serviceID: groveshop.ServiceInventory,
			name:      "Inventory",
			kind:      workerInventory,
			subject:   componentInvocationSubject(cfg.systemNATSSubject, groveshop.ServiceInventory),
		})
	}
	if cfg.groveShopWeb {
		components = append(components, componentSpec{
			serviceID:  groveshop.ServiceWeb,
			name:       "Web",
			kind:       workerWeb,
			subject:    componentInvocationSubject(cfg.systemNATSSubject, groveshop.ServiceWeb),
			workerArgs: []string{"--web-listen", cfg.groveShopWebListen},
		})
	}
	return components
}

func groveShopRecoveryComponentSpecs(cfg config) []componentSpec {
	return []componentSpec{
		{
			serviceID: groveshop.ServiceOrders,
			name:      "Orders",
			kind:      workerOrders,
			subject:   componentInvocationSubject(cfg.systemNATSSubject, groveshop.ServiceOrders),
		},
		{
			serviceID: groveshop.ServiceInventory,
			name:      "Inventory",
			kind:      workerInventory,
			subject:   componentInvocationSubject(cfg.systemNATSSubject, groveshop.ServiceInventory),
		},
	}
}

func groveShopPlacedServiceIDs(cfg config) []grove.ServiceID {
	serviceIDs := make([]grove.ServiceID, 0, 3)
	if cfg.groveShopOrders {
		serviceIDs = append(serviceIDs, groveshop.ServiceOrders)
	}
	if cfg.groveShopInventory {
		serviceIDs = append(serviceIDs, groveshop.ServiceInventory)
	}
	if cfg.groveShopWeb {
		serviceIDs = append(serviceIDs, groveshop.ServiceWeb)
	}
	return serviceIDs
}

func groveShopStartupState(cfg config, desired systemnats.DesiredView) ([]systemnats.PlacementRecord, []grove.ServiceID) {
	if cfg.systemNATSRecovery && desired.Ready && len(desired.Deployments) != 0 {
		return nil, nil
	}
	return groveShopPlacements(cfg), groveShopPlacedServiceIDs(cfg)
}

func waitForDesiredState(ctx context.Context, desired *systemnats.Desired) (systemnats.DesiredView, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		view := desired.Snapshot()
		if view.Ready {
			return view, nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return systemnats.DesiredView{}, fmt.Errorf("wait for desired deployment state: %w", ctx.Err())
		}
	}
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

func (r *systemNATSRuntime) startMembership(ctx context.Context, membership *systemnats.Membership) {
	membershipCtx, cancel := context.WithCancel(ctx)
	r.membershipCancel = cancel
	r.membershipDone = make(chan struct{})
	go func() {
		defer close(r.membershipDone)
		_ = membership.Run(membershipCtx, r.transport)
	}()
}

func (r *systemNATSRuntime) startPlacement(ctx context.Context, placement *systemnats.Placement) {
	placementCtx, cancel := context.WithCancel(ctx)
	r.placementCancel = cancel
	r.placementDone = make(chan struct{})
	go func() {
		defer close(r.placementDone)
		_ = placement.Run(placementCtx, r.transport)
	}()
}

func (r *systemNATSRuntime) startHealth(ctx context.Context, health *systemnats.Health) {
	healthCtx, cancel := context.WithCancel(ctx)
	r.healthCancel = cancel
	r.healthDone = make(chan struct{})
	go func() {
		defer close(r.healthDone)
		_ = health.Run(healthCtx, r.transport)
	}()
}

func (r *systemNATSRuntime) startDesired(ctx context.Context, desired *systemnats.Desired) {
	desiredCtx, cancel := context.WithCancel(ctx)
	r.desiredCancel = cancel
	r.desiredDone = make(chan struct{})
	go func() {
		defer close(r.desiredDone)
		_ = desired.Run(desiredCtx, r.transport)
	}()
}

func (r *systemNATSRuntime) startDeployments(ctx context.Context, deployments *systemnats.Deployments) {
	deploymentsCtx, cancel := context.WithCancel(ctx)
	r.deploymentsCancel = cancel
	r.deploymentsDone = make(chan struct{})
	go func() {
		defer close(r.deploymentsDone)
		_ = deployments.Run(deploymentsCtx, r.transport)
	}()
}

func (r *systemNATSRuntime) startRecovery(ctx context.Context, recovery *serviceRecovery) {
	recoveryCtx, cancel := context.WithCancel(ctx)
	r.recoveryCancel = cancel
	r.recoveryDone = make(chan struct{})
	go func() {
		defer close(r.recoveryDone)
		_ = recovery.Run(recoveryCtx)
	}()
}

func (r *systemNATSRuntime) startReconciler(ctx context.Context, reconciler *desiredReconciler) {
	reconcileCtx, cancel := context.WithCancel(ctx)
	r.reconcileCancel = cancel
	r.reconcileDone = make(chan struct{})
	go func() {
		defer close(r.reconcileDone)
		_ = reconciler.Run(reconcileCtx)
	}()
}

func (r *systemNATSRuntime) stop() {
	if r.reconcileCancel != nil {
		r.reconcileCancel()
		<-r.reconcileDone
	}
	if r.recoveryCancel != nil {
		r.recoveryCancel()
		<-r.recoveryDone
	}
	if r.components != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = r.components.stopAll(stopCtx)
		cancel()
	}
	if r.healthCancel != nil {
		r.healthCancel()
		<-r.healthDone
	}
	if r.placementCancel != nil {
		r.placementCancel()
		<-r.placementDone
	}
	if r.desiredCancel != nil {
		r.desiredCancel()
		<-r.desiredDone
	}
	if r.deploymentsCancel != nil {
		r.deploymentsCancel()
		<-r.deploymentsDone
	}
	if r.membershipCancel != nil {
		r.membershipCancel()
		<-r.membershipDone
	}
	if r.transport != nil {
		r.transport.Close()
	}
	if r.server != nil {
		r.server.Shutdown()
	}
}

func prepareRuntimeDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return runtimeDirError{path: path, err: err}
	}

	info, err := os.Stat(path)
	if err != nil {
		return runtimeDirError{path: path, err: err}
	}
	if !info.IsDir() {
		return runtimeDirError{path: path, err: syscall.ENOTDIR}
	}
	return nil
}
