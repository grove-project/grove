// Package runtime hosts Grove's application runtime behind an explicit,
// application-owned composition boundary.
package runtime

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
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/bootstrap"
	"github.com/grove-project/grove/internal/systemnats"
)

const gracefulLeaveTimeout = 30 * time.Second

var (
	errRuntimeDirRequired            = errors.New("runtime directory is required")
	errSystemNATSConflict            = errors.New("embedded System NATS configuration and URL are mutually exclusive")
	errSystemNATSRequired            = errors.New("system NATS endpoint requires a listen address or URL")
	errSystemNATSRouteListenRequired = errors.New("system NATS route listener requires an embedded client listener")
	errSystemNATSSeedRouteRequired   = errors.New("system NATS seed requires a route listener")
	errSystemNATSClusterIdentity     = errors.New("system NATS clustering requires node identity")
	errSystemNATSMembershipCluster   = errors.New("system NATS membership requires a route listener")
	errSystemNATSRecoveryCluster     = errors.New("system NATS recovery requires membership")
	errApplicationEndpoint           = errors.New("application component placement requires a System NATS endpoint")
	errApplicationPlacementCluster   = errors.New("placed application components require System NATS membership or an explicit route")
	errApplicationListenRequired     = errors.New("HTTP application component requires a listen address")
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
	systemNATSRetireOnStop    bool
	systemNATSURL             string
	systemNATSSubject         string
	componentKinds            stringValues
	componentListeners        stringValues
	componentOptions          stringValues
	routeSubject              string
	applicationConfiguration  Configuration
	applicationConfigDigest   string
	applicationArtifactDigest string
	applicationCodeVersion    string
	delvePath                 string
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

type stringValues []string

func (values *stringValues) String() string {
	return strings.Join(*values, ",")
}

func (values *stringValues) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("value must not be empty")
	}
	*values = append(*values, value)
	return nil
}

func (e runtimeDirError) Error() string {
	return fmt.Sprintf("runtime directory %q: %v", e.path, e.err)
}

func (e runtimeDirError) Unwrap() error {
	return e.err
}

// Main runs a Grove application artifact. Application executables should be a
// thin main package that constructs Definition and calls Main.
func Main(definition Definition) {
	if err := configureApplication(definition); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", definition.Name, err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := execute(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", definition.Name, err)
		os.Exit(1)
	}
}

// Execute runs one application command with caller-supplied process streams.
// It is primarily useful to application tests and embedded launchers.
func Execute(ctx context.Context, definition Definition, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if err := configureApplication(definition); err != nil {
		return err
	}
	return execute(ctx, args, stdin, stdout, stderr)
}

func execute(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return runApplicationConsole(ctx, nil, stdin, stdout)
	}
	if args[0] == "action" {
		return runApplicationAction(ctx, args[1:], stdout)
	}
	if len(args) != 0 && args[0] == "bootstrap-hello" {
		return runBootstrapHello(args[1:], stdout)
	}
	if len(args) != 0 && args[0] == "config-compile" {
		return runConfigCompile(ctx, args[1:], stdin, stdout)
	}
	runCommand := run
	if len(args) != 0 && args[0] == "worker" {
		args = args[1:]
		runCommand = runWorker
	}
	return runCommand(ctx, args, stdout, stderr)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	inspection, configuration, err := loadEmbeddedConfiguration()
	if err != nil {
		return fmt.Errorf("verify embedded application artifact: %w", err)
	}
	cfg, err := parseConfig(args, stderr)
	if err != nil {
		return err
	}
	cfg.applicationConfiguration = configuration
	cfg.applicationConfigDigest = inspection.Config.Digest
	cfg.applicationArtifactDigest = inspection.ArtifactDigest
	cfg.applicationCodeVersion = inspection.Manifest.CodeVersion
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
		ready.ClusterName = configuration.Facts["cluster.name"]
		ready.NodeZone = configuration.Facts["node.zone"]
	}
	if err := encoder.Encode(ready); err != nil {
		systemRuntime.stop()
		return fmt.Errorf("encode ready event: %w", err)
	}

	<-ctx.Done()
	var leaveErr error
	if cfg.systemNATSRetireOnStop {
		leaveCtx, leaveCancel := context.WithTimeout(context.Background(), gracefulLeaveTimeout)
		leaveErr = systemRuntime.gracefulLeave(leaveCtx, cfg.nodeID)
		leaveCancel()
	}
	systemRuntime.stop()

	if err := encoder.Encode(lifecycleEvent{Event: "stopped"}); err != nil {
		return fmt.Errorf("encode stopped event: %w", err)
	}
	return leaveErr
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
	flags.BoolVar(&cfg.systemNATSRetireOnStop, "system-nats-retire-on-stop", false, "retire this logical node after graceful service relocation")
	flags.StringVar(&cfg.systemNATSURL, "system-nats-url", "", "System NATS server URL")
	flags.StringVar(&cfg.systemNATSSubject, "system-nats-subject", "", "System NATS transport endpoint subject")
	flags.Var(&cfg.componentKinds, "component", "application component kind to host (repeatable)")
	flags.Var(&cfg.componentListeners, "component-listen", "component HTTP listener as kind=address (repeatable)")
	flags.Var(&cfg.componentOptions, "component-option", "application-owned worker option as kind=value (repeatable)")
	flags.StringVar(&cfg.routeSubject, "route-subject", "", "explicit Grove invocation subject used by directly hosted components")
	flags.StringVar(&cfg.delvePath, "delve-path", "", "path to the Delve executable used for worker debugging")
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
	if cfg.systemNATSMembership && cfg.systemNATSRouteListen == "" {
		return config{}, errSystemNATSMembershipCluster
	}
	if cfg.systemNATSRecovery && !cfg.systemNATSMembership {
		return config{}, errSystemNATSRecoveryCluster
	}
	if cfg.systemNATSRetireOnStop && !cfg.systemNATSRecovery {
		return config{}, errSystemNATSRecoveryCluster
	}
	if cfg.systemNATSSubject != "" && cfg.systemNATSListen == "" && cfg.systemNATSURL == "" {
		return config{}, errSystemNATSRequired
	}
	if (len(cfg.componentKinds) != 0 || cfg.routeSubject != "" || cfg.systemNATSRecovery) && cfg.systemNATSSubject == "" {
		return config{}, errApplicationEndpoint
	}
	if len(cfg.componentKinds) != 0 && !cfg.systemNATSMembership && cfg.routeSubject == "" && len(cfg.componentKinds) > 1 {
		return config{}, errApplicationPlacementCluster
	}
	if err := validateConfiguredComponents(cfg); err != nil {
		return config{}, err
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

func validateConfiguredComponents(cfg config) error {
	kinds := make(map[string]struct{}, len(cfg.componentKinds))
	for _, kind := range cfg.componentKinds {
		component, ok := activeApplication.componentByKind(kind)
		if !ok {
			return fmt.Errorf("%w: %q", ErrComponentUnknown, kind)
		}
		if _, duplicate := kinds[kind]; duplicate {
			return fmt.Errorf("%w: duplicate component %q", ErrApplicationDefinitionInvalid, kind)
		}
		kinds[kind] = struct{}{}
		listen := configuredValue(cfg.componentListeners, kind)
		if (component.HTTPHandler != nil) != (listen != "") && !(cfg.systemNATSRecovery && component.HTTPHandler != nil) {
			return fmt.Errorf("%w: %s", errApplicationListenRequired, component.Name)
		}
	}
	for _, assignment := range append(slices.Clone(cfg.componentListeners), cfg.componentOptions...) {
		kind, _, ok := strings.Cut(assignment, "=")
		if !ok || kind == "" {
			return fmt.Errorf("invalid component assignment %q", assignment)
		}
		if _, configured := kinds[kind]; !configured && !cfg.systemNATSRecovery {
			return fmt.Errorf("%w: %q", ErrComponentUnknown, kind)
		}
	}
	return nil
}

func configuredValue(values []string, kind string) string {
	prefix := kind + "="
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimPrefix(value, prefix)
		}
	}
	return ""
}

func configuredOptions(values []string, kind string) []string {
	prefix := kind + "="
	var options []string
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			options = append(options, strings.TrimPrefix(value, prefix))
		}
	}
	return options
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
	membership        *systemnats.Membership
	placement         *systemnats.Placement
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
		runtime.membership = membership
		if runtime.server != nil {
			if err := transport.ServePeer(ctx, cfg.nodeID, runtime.server); err != nil {
				runtime.stop()
				return nil, err
			}
		}

		deployments := systemnats.NewDeployments()
		if err := transport.ServeDeployments(ctx, cfg.nodeID, deployments); err != nil {
			runtime.stop()
			return nil, err
		}
		runtime.startDeployments(ctx, deployments)

		desired := systemnats.NewDesired()
		placementRecords := applicationPlacements(cfg)
		initialServiceIDs := applicationPlacedServiceIDs(cfg)
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
			placementRecords, initialServiceIDs = applicationStartupState(cfg, desiredView)
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
		runtime.placement = placement
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

		componentSpecs := applicationComponentSpecs(cfg)
		if cfg.systemNATSRecovery {
			componentSpecs = applicationRecoveryComponentSpecs(cfg)
		}
		runtime.components = newComponentManager(
			componentSpecs,
			newWorkerStarter(url, cfg.nodeID),
		)
		if err := transport.ServeComponents(ctx, cfg.nodeID, runtime.components); err != nil {
			runtime.stop()
			return nil, err
		}
		if err := transport.ServeDebug(ctx, cfg.nodeID, newDebugController(cfg.nodeID, cfg.runtimeDir, cfg.delvePath, runtime.components)); err != nil {
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
		if !cfg.systemNATSMembership && len(cfg.componentKinds) != 0 {
			registry := &grove.Registry{}
			var client *grove.Client
			if cfg.routeSubject != "" {
				client, err = transport.RoutedClient(cfg.routeSubject)
				if err != nil {
					runtime.stop()
					return nil, err
				}
			}
			for _, kind := range cfg.componentKinds {
				component, _ := activeApplication.componentByKind(kind)
				if component.HTTPHandler != nil {
					runtime.stop()
					return nil, fmt.Errorf("direct HTTP component %s requires clustered worker placement", component.Name)
				}
				if err := component.Register(ComponentContext{
					Context:       ctx,
					Registry:      registry,
					Client:        client,
					Configuration: cfg.applicationConfiguration.Value,
					ConfigDigest:  cfg.applicationConfigDigest,
					Options:       configuredOptions(cfg.componentOptions, kind),
				}); err != nil {
					runtime.stop()
					return nil, fmt.Errorf("register application component %s: %w", component.Name, err)
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
	if cfg.nodeID != "" {
		if err := transport.ServeBootstrapReadiness(ctx, bootstrap.Readiness{
			NodeID:         cfg.nodeID,
			ArtifactDigest: cfg.applicationArtifactDigest,
			State:          bootstrap.ReadinessHealthy,
		}); err != nil {
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

func applicationPlacements(cfg config) []systemnats.PlacementRecord {
	placements := make([]systemnats.PlacementRecord, 0, len(cfg.componentKinds))
	for _, kind := range cfg.componentKinds {
		component, _ := activeApplication.componentByKind(kind)
		placements = append(placements, systemnats.PlacementRecord{
			ServiceID:         component.ServiceID,
			NodeID:            cfg.nodeID,
			InvocationSubject: componentInvocationSubject(cfg.systemNATSSubject, component.ServiceID),
			ArtifactDigest:    cfg.applicationArtifactDigest,
		})
	}
	return placements
}

func applicationComponentSpecs(cfg config) []componentSpec {
	return componentSpecsForKinds(cfg, cfg.componentKinds)
}

func applicationRecoveryComponentSpecs(cfg config) []componentSpec {
	kinds := make([]string, 0, len(activeApplication.Components))
	for _, component := range activeApplication.Components {
		if component.HTTPHandler != nil && configuredValue(cfg.componentListeners, component.Kind) == "" {
			continue
		}
		kinds = append(kinds, component.Kind)
	}
	return componentSpecsForKinds(cfg, kinds)
}

func componentSpecsForKinds(cfg config, kinds []string) []componentSpec {
	components := make([]componentSpec, 0, len(kinds))
	for _, kind := range kinds {
		component, _ := activeApplication.componentByKind(kind)
		components = append(components, componentSpec{
			serviceID:      component.ServiceID,
			name:           component.Name,
			kind:           component.Kind,
			subject:        componentInvocationSubject(cfg.systemNATSSubject, component.ServiceID),
			artifactDigest: cfg.applicationArtifactDigest,
			codeVersion:    cfg.applicationCodeVersion,
			listenAddress:  configuredValue(cfg.componentListeners, kind),
			options:        configuredOptions(cfg.componentOptions, kind),
		})
	}
	return components
}

func applicationPlacedServiceIDs(cfg config) []grove.ServiceID {
	serviceIDs := make([]grove.ServiceID, 0, len(cfg.componentKinds))
	for _, kind := range cfg.componentKinds {
		component, _ := activeApplication.componentByKind(kind)
		serviceIDs = append(serviceIDs, component.ServiceID)
	}
	return serviceIDs
}

func applicationStartupState(cfg config, desired systemnats.DesiredView) ([]systemnats.PlacementRecord, []grove.ServiceID) {
	if cfg.systemNATSRecovery && desired.Ready && len(desired.Deployments) != 0 {
		return nil, nil
	}
	return applicationPlacements(cfg), applicationPlacedServiceIDs(cfg)
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

func (r *systemNATSRuntime) gracefulLeave(ctx context.Context, nodeID string) error {
	if r.membership == nil || r.transport == nil {
		return nil
	}
	membership := r.membership.Snapshot()
	if !membership.Ready {
		return nil
	}
	if err := r.membership.BeginLeave(ctx, r.transport); err != nil {
		return err
	}
	if r.reconcileCancel != nil {
		r.reconcileCancel()
		<-r.reconcileDone
		r.reconcileCancel = nil
	}
	if r.recoveryCancel != nil {
		r.recoveryCancel()
		<-r.recoveryDone
		r.recoveryCancel = nil
	}
	if r.components != nil {
		if err := r.components.stopAll(ctx); err != nil {
			return fmt.Errorf("stop components before node retirement: %w", err)
		}
	}
	if r.healthCancel != nil {
		r.healthCancel()
		<-r.healthDone
		r.healthCancel = nil
	}
	peerIDs := make([]string, 0, len(membership.Members)-1)
	for _, member := range membership.Members {
		if member.NodeID != nodeID {
			peerIDs = append(peerIDs, member.NodeID)
		}
	}
	if r.placement != nil && len(peerIDs) != 0 {
		shouldRetire, err := r.waitForPlacementRetirement(ctx, nodeID, peerIDs, membership.Members)
		if err != nil {
			return err
		}
		if !shouldRetire {
			return nil
		}
	}
	if r.server != nil {
		if r.server.HasPeer() && len(peerIDs) != 0 {
			var transferErr error
			for _, peerID := range peerIDs {
				if err := r.transport.RequestPeer(ctx, peerID); err == nil {
					transferErr = nil
					break
				} else {
					transferErr = errors.Join(transferErr, err)
				}
			}
			if transferErr != nil {
				return fmt.Errorf("transfer System NATS metadata witness: %w", transferErr)
			}
		}
		if err := r.server.PrepareRetire(ctx); err != nil {
			return err
		}
	}
	if err := r.membership.Leave(ctx, r.transport); err != nil {
		return err
	}
	if len(peerIDs) != 0 {
		if err := r.waitForMembershipRetirement(ctx, nodeID, peerIDs); err != nil {
			return err
		}
	}
	if r.server != nil {
		if err := r.server.Retire(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (r *systemNATSRuntime) waitForMembershipRetirement(
	ctx context.Context,
	nodeID string,
	peerIDs []string,
) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var last systemnats.MembershipView
	var lastErr error
	for {
		for _, peerID := range peerIDs {
			requestCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
			view, err := r.transport.RequestMembership(requestCtx, peerID)
			cancel()
			if err != nil {
				lastErr = err
				continue
			}
			last = view
			retired := view.Ready
			for _, member := range view.Members {
				if member.NodeID == nodeID {
					retired = false
					break
				}
			}
			if retired {
				return nil
			}
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("wait for membership retirement of %s: membership=%#v: %w", nodeID, last, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func (r *systemNATSRuntime) waitForPlacementRetirement(
	ctx context.Context,
	nodeID string,
	peerIDs []string,
	members []systemnats.MembershipRecord,
) (bool, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var last systemnats.PlacementView
	var lastErr error
	for {
		allLeaving, err := r.membership.AllLeaving(ctx, r.transport, members)
		if err == nil && allLeaving {
			return false, nil
		}
		if err != nil {
			lastErr = err
		}
		for _, peerID := range peerIDs {
			requestCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
			view, err := r.transport.RequestPlacement(requestCtx, peerID)
			cancel()
			if err != nil {
				lastErr = err
				continue
			}
			last = view
			if view.Ready && !placementReferencesNode(view, nodeID) {
				return true, nil
			}
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return false, fmt.Errorf("wait for service relocation from %s: placement=%#v: %w", nodeID, last, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func placementReferencesNode(view systemnats.PlacementView, nodeID string) bool {
	for _, placement := range view.Placements {
		if placement.NodeID == nodeID {
			return true
		}
	}
	return false
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
