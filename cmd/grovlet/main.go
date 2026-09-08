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
	"strconv"
	"syscall"

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
	errGroveShopEndpoint             = errors.New("reference application service placement requires a System NATS endpoint")
	errNodeIdentityPair              = errors.New("node ID and advertised endpoint must be configured together")
	errNodeIDInvalid                 = errors.New("node ID is invalid")
	errAdvertiseInvalid              = errors.New("advertised endpoint is invalid")
)

type config struct {
	runtimeDir                string
	systemNATSListen          string
	systemNATSRouteListen     string
	systemNATSSeed            string
	systemNATSURL             string
	systemNATSSubject         string
	groveShopInventory        bool
	groveShopInventorySubject string
	nodeID                    string
	advertisedEndpoint        string
}

type lifecycleEvent struct {
	Event              string `json:"event"`
	NodeID             string `json:"node_id,omitempty"`
	AdvertisedEndpoint string `json:"advertised_endpoint,omitempty"`
	SystemNATSURL      string `json:"system_nats_url,omitempty"`
	SystemNATSRouteURL string `json:"system_nats_route_url,omitempty"`
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

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "grovlet: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cfg, err := parseConfig(args, stderr)
	if err != nil {
		return err
	}
	if err := prepareRuntimeDir(cfg.runtimeDir); err != nil {
		return err
	}
	systemRuntime, err := startSystemNATS(ctx, cfg)
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(stdout)
	if err := encoder.Encode(lifecycleEvent{
		Event:              "ready",
		NodeID:             cfg.nodeID,
		AdvertisedEndpoint: cfg.advertisedEndpoint,
		SystemNATSURL:      systemRuntime.url,
		SystemNATSRouteURL: systemRuntime.routeURL,
	}); err != nil {
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
	flags.StringVar(&cfg.systemNATSURL, "system-nats-url", "", "System NATS server URL")
	flags.StringVar(&cfg.systemNATSSubject, "system-nats-subject", "", "System NATS transport endpoint subject")
	flags.BoolVar(&cfg.groveShopInventory, "grove-shop-inventory", false, "register Grove Shop Inventory on this Grovlet")
	flags.StringVar(&cfg.groveShopInventorySubject, "grove-shop-orders-inventory-subject", "", "explicit Inventory endpoint for Grove Shop Orders")
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
	if cfg.systemNATSSubject != "" && cfg.systemNATSListen == "" && cfg.systemNATSURL == "" {
		return config{}, errSystemNATSRequired
	}
	if (cfg.groveShopInventory || cfg.groveShopInventorySubject != "") && cfg.systemNATSSubject == "" {
		return config{}, errGroveShopEndpoint
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
	server    *systemnats.Server
	transport *systemnats.Transport
	url       string
	routeURL  string
}

func startSystemNATS(ctx context.Context, cfg config) (*systemNATSRuntime, error) {
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
	if cfg.systemNATSSubject != "" {
		handler := systemnats.Handler(func(_ context.Context, request grove.RequestEnvelope) grove.ResponseEnvelope {
			return grove.ResponseEnvelope{Payload: request.Payload}
		})
		if cfg.groveShopInventory || cfg.groveShopInventorySubject != "" {
			registry := &grove.Registry{}
			if cfg.groveShopInventory {
				if err := groveshop.RegisterInventory(registry, &groveshop.Inventory{}); err != nil {
					runtime.stop()
					return nil, fmt.Errorf("register Grove Shop Inventory: %w", err)
				}
			}
			if cfg.groveShopInventorySubject != "" {
				inventoryClient, err := transport.RoutedClient(cfg.groveShopInventorySubject)
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

func (r *systemNATSRuntime) stop() {
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
