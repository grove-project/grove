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
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/grove-project/grove/internal/nodeproc"
	"github.com/grove-project/grove/internal/scenario"
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
	errApplicationPlacementCluster   = errors.New("a node without System NATS membership hosts at most one candidate component")
	errCandidateComponent            = errors.New("a standalone candidate component requires node identity and no HTTP listener")
	errRouteSubjectCandidate         = errors.New("route subject applies only to a standalone candidate component")
	errApplicationListenRequired     = errors.New("HTTP application component requires a listen address")
	errNodeIdentityPair              = errors.New("node ID and advertised endpoint must be configured together")
	errNodeIDInvalid                 = errors.New("node ID is invalid")
	errAdvertiseInvalid              = errors.New("advertised endpoint is invalid")
	errClusterTooSmallToLeave        = errors.New("node cannot leave")
)

type config struct {
	runtimeDir             string
	systemNATSListen       string
	systemNATSRouteListen  string
	systemNATSSeed         string
	systemNATSMembership   bool
	systemNATSRecovery     bool
	systemNATSRetireOnStop bool
	systemNATSURL          string
	systemNATSSubject      string
	componentKinds         stringValues
	// routeSubject is where a standalone candidate's component sends its
	// Grove calls; see grovlet.hostCandidate.
	routeSubject       string
	componentListeners stringValues
	componentOptions   stringValues
	// isolatedComponents run in dedicated worker processes; every other
	// component shares the node's application runtime process.
	isolatedComponents stringValues
	// ingressAddress is set only on the node that founds a cluster. It is the
	// cluster's ingress address, recorded in control state for later nodes.
	ingressAddress string
	// hostAll is set on the founding node, which hosts every component.
	hostAll bool
	// adoptCluster is set on a clustered node that names no component: if the
	// cluster was founded with an ingress address, the node hosts every
	// component the application defines; otherwise it hosts none.
	adoptCluster              bool
	applicationConfiguration  Configuration
	applicationConfigDigest   string
	applicationArtifactDigest string
	applicationCodeVersion    string
	delvePath                 string
	nodeID                    string
	advertisedEndpoint        string
	// emit writes a lifecycle event to the process's event stream.
	emit func(lifecycleEvent)
}

// newEventEmitter serializes lifecycle events written from several goroutines.
func newEventEmitter(stdout io.Writer) (func(lifecycleEvent), *json.Encoder, *sync.Mutex) {
	var mu sync.Mutex
	encoder := json.NewEncoder(stdout)
	return func(event lifecycleEvent) {
		mu.Lock()
		defer mu.Unlock()
		_ = encoder.Encode(event)
	}, encoder, &mu
}

// lifecycleEvent is one line of the Grovlet lifecycle stream; the protocol is
// owned by internal/nodeproc, which supervisors use to read it.
type lifecycleEvent = nodeproc.Event

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
	if args[0] == scenario.Command {
		return runScenarioCommand(ctx, args[1:], stdout, stderr)
	}
	runCommand := run
	if len(args) != 0 && args[0] == "worker" {
		args = args[1:]
		runCommand = runWorker
	}
	if len(args) != 0 && args[0] == applicationRuntimeCommand {
		args = args[1:]
		runCommand = runApplicationRuntime
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
	emit, encoder, encoderMu := newEventEmitter(stdout)
	cfg.emit = emit
	node, err := startGrovlet(ctx, cfg)
	if err != nil {
		return err
	}

	ready := lifecycleEvent{
		Event:              "ready",
		NodeID:             cfg.nodeID,
		AdvertisedEndpoint: cfg.advertisedEndpoint,
		SystemNATSURL:      node.url,
		SystemNATSRouteURL: node.routeURL,
	}
	if !inspection.ConfigEmpty {
		ready.ConfigRevision = configuration.Revision
		ready.ConfigDigest = inspection.Config.Digest
		ready.ArtifactDigest = inspection.ArtifactDigest
		ready.ClusterName = configuration.Facts["cluster.name"]
		ready.NodeZone = configuration.Facts["node.zone"]
	}
	encoderMu.Lock()
	err = encoder.Encode(ready)
	encoderMu.Unlock()
	if err != nil {
		node.stop()
		return fmt.Errorf("encode ready event: %w", err)
	}

	<-ctx.Done()
	var leaveErr error
	if cfg.systemNATSRetireOnStop {
		leaveCtx, leaveCancel := context.WithTimeout(context.Background(), gracefulLeaveTimeout)
		leaveErr = node.gracefulLeave(leaveCtx, cfg.nodeID)
		leaveCancel()
	}
	node.stop()

	encoderMu.Lock()
	err = encoder.Encode(lifecycleEvent{Event: "stopped"})
	encoderMu.Unlock()
	if err != nil {
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
	flags.StringVar(&cfg.routeSubject, "route-subject", "", "Grove invocation subject that a standalone candidate's component calls")
	flags.Var(&cfg.componentListeners, "component-listen", "component HTTP listener as kind=address (repeatable)")
	flags.Var(&cfg.componentOptions, "component-option", "application-owned worker option as kind=value (repeatable)")
	flags.Var(&cfg.isolatedComponents, "component-isolate", "component kind to run in a dedicated worker process instead of the shared application runtime (repeatable)")
	flags.StringVar(&cfg.ingressAddress, "ingress-address", "", "cluster ingress address recorded by the node that founds the cluster")
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
	if (len(cfg.componentKinds) != 0 || cfg.systemNATSRecovery) && cfg.systemNATSSubject == "" {
		return config{}, errApplicationEndpoint
	}
	// Components run in the node's application runtime or isolated workers;
	// the Grovlet never hosts application code itself. Without membership a
	// node is a standalone candidate whose one component runs in a worker.
	standaloneCandidate := len(cfg.componentKinds) != 0 && !cfg.systemNATSMembership
	if standaloneCandidate && len(cfg.componentKinds) != 1 {
		return config{}, errApplicationPlacementCluster
	}
	if cfg.routeSubject != "" && !standaloneCandidate {
		return config{}, errRouteSubjectCandidate
	}
	if standaloneCandidate {
		component, ok := activeApplication.componentByKind(cfg.componentKinds[0])
		if cfg.nodeID == "" || (ok && component.HTTPHandler != nil) {
			return config{}, errCandidateComponent
		}
	}
	if cfg.ingressAddress != "" && !cfg.systemNATSRecovery {
		return config{}, errSystemNATSRecoveryCluster
	}
	if cfg.ingressAddress != "" {
		cfg = hostEveryComponent(cfg)
	} else if len(cfg.componentKinds) == 0 && cfg.systemNATSRecovery {
		cfg.adoptCluster = true
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

// hostEveryComponent applies the runtime's placement default: a clustered
// node hosts every component the application defines. Ingress is hosted by
// the node founding the cluster and moves to a survivor through recovery.
func hostEveryComponent(cfg config) config {
	cfg.hostAll = true
	for _, component := range activeApplication.Components {
		if component.HTTPHandler == nil {
			cfg.componentKinds = append(cfg.componentKinds, component.Kind)
		} else if cfg.ingressAddress != "" {
			cfg.componentKinds = append(cfg.componentKinds, component.Kind)
			cfg.componentListeners = append(cfg.componentListeners, component.Kind+"="+cfg.ingressAddress)
		}
	}
	return cfg
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
	for _, kind := range cfg.isolatedComponents {
		if _, ok := activeApplication.componentByKind(kind); !ok {
			return fmt.Errorf("%w: %q", ErrComponentUnknown, kind)
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
