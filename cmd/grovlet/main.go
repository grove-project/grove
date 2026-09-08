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
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
)

var (
	errRuntimeDirRequired = errors.New("runtime directory is required")
	errSystemNATSConflict = errors.New("system NATS listen address and URL are mutually exclusive")
	errSystemNATSRequired = errors.New("system NATS endpoint requires a listen address or URL")
)

type config struct {
	runtimeDir        string
	systemNATSListen  string
	systemNATSURL     string
	systemNATSSubject string
}

type lifecycleEvent struct {
	Event         string `json:"event"`
	SystemNATSURL string `json:"system_nats_url,omitempty"`
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
	if err := encoder.Encode(lifecycleEvent{Event: "ready", SystemNATSURL: systemRuntime.url}); err != nil {
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
	flags.StringVar(&cfg.systemNATSURL, "system-nats-url", "", "System NATS server URL")
	flags.StringVar(&cfg.systemNATSSubject, "system-nats-subject", "", "System NATS transport endpoint subject")
	if err := flags.Parse(args); err != nil {
		return config{}, fmt.Errorf("parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected arguments: %q", flags.Args())
	}
	if cfg.runtimeDir == "" {
		return config{}, errRuntimeDirRequired
	}
	if cfg.systemNATSListen != "" && cfg.systemNATSURL != "" {
		return config{}, errSystemNATSConflict
	}
	if cfg.systemNATSSubject != "" && cfg.systemNATSListen == "" && cfg.systemNATSURL == "" {
		return config{}, errSystemNATSRequired
	}
	return cfg, nil
}

type systemNATSRuntime struct {
	server    *systemnats.Server
	transport *systemnats.Transport
	url       string
}

func startSystemNATS(ctx context.Context, cfg config) (*systemNATSRuntime, error) {
	runtime := &systemNATSRuntime{}
	url := cfg.systemNATSURL
	if cfg.systemNATSListen != "" {
		host, portText, err := net.SplitHostPort(cfg.systemNATSListen)
		if err != nil {
			return nil, fmt.Errorf("parse System NATS listen address: %w", err)
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			return nil, fmt.Errorf("parse System NATS listen port: %w", err)
		}
		if port < 0 || port > 65535 {
			return nil, fmt.Errorf("validate System NATS listen port: %w", syscall.EINVAL)
		}
		runtime.server, err = systemnats.StartServer(ctx, host, port)
		if err != nil {
			return nil, err
		}
		url = runtime.server.URL()
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
		if err := transport.Serve(
			ctx,
			cfg.systemNATSSubject,
			func(_ context.Context, request grove.RequestEnvelope) grove.ResponseEnvelope {
				return grove.ResponseEnvelope{Payload: request.Payload}
			},
		); err != nil {
			runtime.stop()
			return nil, err
		}
	}
	return runtime, nil
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
