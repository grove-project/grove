package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
)

type workerConfig struct {
	component       string
	systemNATSURL   string
	subject         string
	placementNodeID string
	parentFD        int
	listenAddress   string
	options         []string
}

type workerProcess struct {
	cmd       *exec.Cmd
	keepalive *os.File
	done      chan struct{}
	mu        sync.Mutex
	err       error
}

func newWorkerStarter(systemNATSURL, placementNodeID string) componentStarter {
	return func(ctx context.Context, spec componentSpec) (componentProcess, error) {
		return startWorkerProcess(ctx, systemNATSURL, placementNodeID, spec)
	}
}

func startWorkerProcess(ctx context.Context, systemNATSURL, placementNodeID string, spec componentSpec) (*workerProcess, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate Grovlet executable: %w", err)
	}
	parentRead, parentWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create worker parent pipe: %w", err)
	}
	args := []string{
		"worker",
		"--component", spec.kind,
		"--system-nats-url", systemNATSURL,
		"--subject", spec.subject,
		"--placement-node-id", placementNodeID,
		"--parent-fd", "3",
	}
	if spec.listenAddress != "" {
		args = append(args, "--listen", spec.listenAddress)
	}
	if len(spec.options) != 0 {
		args = append(args, "--")
		args = append(args, spec.options...)
	}
	cmd := exec.Command(executable, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		parentRead.Close()
		parentWrite.Close()
		return nil, fmt.Errorf("open worker stdout: %w", err)
	}
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{parentRead}
	if err := cmd.Start(); err != nil {
		parentRead.Close()
		parentWrite.Close()
		return nil, fmt.Errorf("start worker: %w", err)
	}
	parentRead.Close()
	process := &workerProcess{cmd: cmd, keepalive: parentWrite, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		process.mu.Lock()
		process.err = err
		process.mu.Unlock()
		close(process.done)
	}()

	ready := make(chan error, 1)
	go func() {
		decoder := json.NewDecoder(stdout)
		for {
			var event lifecycleEvent
			if err := decoder.Decode(&event); err != nil {
				ready <- err
				return
			}
			if event.Event == "ready" {
				ready <- nil
				return
			}
		}
	}()
	startCtx := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok {
		startCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
	}
	defer cancel()
	select {
	case err := <-ready:
		if err != nil {
			_ = process.Stop(startCtx)
			return nil, fmt.Errorf("read worker readiness: %w", err)
		}
		return process, nil
	case <-process.done:
		exitErr := process.Err()
		if exitErr == nil {
			exitErr = errors.New("worker exited without reporting readiness")
		}
		return nil, fmt.Errorf("worker exited before readiness: %w", exitErr)
	case <-startCtx.Done():
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		_ = process.Stop(stopCtx)
		stopCancel()
		return nil, fmt.Errorf("wait for worker readiness: %w", startCtx.Err())
	}
}

func (p *workerProcess) Stop(ctx context.Context) error {
	_ = p.keepalive.Close()
	select {
	case <-p.done:
		return p.Err()
	case <-ctx.Done():
		_ = p.cmd.Process.Kill()
		<-p.done
		return ctx.Err()
	}
}

func (p *workerProcess) Kill(ctx context.Context) error {
	_ = p.keepalive.Close()
	if err := p.cmd.Process.Kill(); err != nil {
		return err
	}
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *workerProcess) Done() <-chan struct{} {
	return p.done
}

func (p *workerProcess) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

func (p *workerProcess) PID() int {
	return p.cmd.Process.Pid
}

func runWorker(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cfg, err := parseWorkerConfig(args, stderr)
	if err != nil {
		return err
	}
	inspection, applicationConfig, err := loadEmbeddedConfiguration()
	if err != nil {
		return fmt.Errorf("load embedded application configuration: %w", err)
	}
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	parent := os.NewFile(uintptr(cfg.parentFD), "grovlet-parent")
	if parent == nil {
		return errors.New("worker parent pipe is unavailable")
	}
	defer parent.Close()
	go func() {
		_, _ = io.Copy(io.Discard, parent)
		cancel()
	}()

	transport, err := systemnats.Connect(workerCtx, cfg.systemNATSURL)
	if err != nil {
		return err
	}
	defer transport.Close()
	registry := &grove.Registry{}
	client, err := transport.ObservedPlacementClient(cfg.placementNodeID)
	if applicationDeclaresHandlers() {
		client, err = grove.NewRoutedClient(transport.ObservedHandlerRouter(
			cfg.placementNodeID, transport.ObservedPlacementRouter(cfg.placementNodeID)))
	}
	if err != nil {
		return err
	}
	component, ok := activeApplication.componentByKind(cfg.component)
	if !ok {
		return fmt.Errorf("%w: %q", ErrComponentUnknown, cfg.component)
	}
	localArtifact := ArtifactStatus{
		ApplicationID:  inspection.Manifest.ApplicationID,
		CodeVersion:    inspection.Manifest.CodeVersion,
		ArtifactDigest: inspection.ArtifactDigest,
		ConfigRevision: applicationConfig.Revision,
		ConfigDigest:   inspection.Config.Digest,
	}
	// Background workloads started by Register claim exclusive capabilities
	// through the same provider as request handlers.
	var provider *systemnats.RemoteExclusiveProvider
	registerCtx := workerCtx
	if applicationDeclaresHandlers() {
		provider = transport.NewRemoteExclusiveProvider(workerCtx, cfg.placementNodeID, 0)
		registerCtx = grove.WithExclusiveProvider(workerCtx, provider)
	}
	componentContext := ComponentContext{
		Context:       registerCtx,
		Registry:      registry,
		Client:        client,
		Configuration: applicationConfig.Value,
		ConfigDigest:  inspection.Config.Digest,
		Artifact:      localArtifact,
		ReadStatus:    newStatusReader(transport, cfg.placementNodeID, localArtifact),
		ListenAddress: cfg.listenAddress,
		Options:       cfg.options,
	}
	if component.Register != nil {
		if err := component.Register(componentContext); err != nil {
			return fmt.Errorf("register application component %s: %w", component.Name, err)
		}
	}
	var (
		webServer *http.Server
		webDone   chan error
	)
	if component.HTTPHandler != nil {
		listener, err := net.Listen("tcp", cfg.listenAddress)
		if err != nil {
			return fmt.Errorf("listen for application component %s: %w", component.Name, err)
		}
		handler, err := component.HTTPHandler(componentContext)
		if err != nil {
			_ = listener.Close()
			return fmt.Errorf("create application component %s HTTP handler: %w", component.Name, err)
		}
		webServer = &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
		}
		defer webServer.Close()
		webDone = make(chan error, 1)
		go func() { webDone <- webServer.Serve(listener) }()
	}
	dispatcher, err := grove.NewDispatcher(registry)
	if err != nil {
		return err
	}
	serve := systemnats.Handler(dispatcher.Dispatch)
	if provider != nil {
		serve = func(ctx context.Context, request grove.RequestEnvelope) grove.ResponseEnvelope {
			return dispatcher.Dispatch(grove.WithExclusiveProvider(ctx, provider), request)
		}
	}
	if err := transport.Serve(workerCtx, cfg.subject, serve); err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	if err := encoder.Encode(lifecycleEvent{Event: "ready"}); err != nil {
		return fmt.Errorf("encode worker ready event: %w", err)
	}
	select {
	case <-workerCtx.Done():
	case err := <-webDone:
		return fmt.Errorf("serve application component %s: %w", component.Name, err)
	}
	if webServer != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
		err := webServer.Shutdown(shutdownCtx)
		shutdownCancel()
		if err != nil {
			return fmt.Errorf("stop application component %s: %w", component.Name, err)
		}
		if err := <-webDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve application component %s: %w", component.Name, err)
		}
	}
	if err := encoder.Encode(lifecycleEvent{Event: "stopped"}); err != nil {
		return fmt.Errorf("encode worker stopped event: %w", err)
	}
	return nil
}

func parseWorkerConfig(args []string, stderr io.Writer) (workerConfig, error) {
	var cfg workerConfig
	flags := flag.NewFlagSet("grovlet worker", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&cfg.component, "component", "", "application component kind")
	flags.StringVar(&cfg.systemNATSURL, "system-nats-url", "", "parent Grovlet System NATS URL")
	flags.StringVar(&cfg.subject, "subject", "", "component invocation subject")
	flags.StringVar(&cfg.placementNodeID, "placement-node-id", "", "parent placement observer node ID")
	flags.IntVar(&cfg.parentFD, "parent-fd", 0, "parent-lifetime file descriptor")
	flags.StringVar(&cfg.listenAddress, "listen", "", "application component HTTP listen address")
	if err := flags.Parse(args); err != nil {
		return workerConfig{}, fmt.Errorf("parse worker flags: %w", err)
	}
	cfg.options = slices.Clone(flags.Args())
	if cfg.component == "" || cfg.systemNATSURL == "" || cfg.subject == "" || cfg.placementNodeID == "" || cfg.parentFD < 3 {
		return workerConfig{}, errors.New("worker configuration is incomplete")
	}
	component, ok := activeApplication.componentByKind(cfg.component)
	if !ok {
		return workerConfig{}, fmt.Errorf("%w: %q", ErrComponentUnknown, cfg.component)
	}
	if (component.HTTPHandler != nil) != (cfg.listenAddress != "") {
		return workerConfig{}, errors.New("HTTP component listen configuration is invalid")
	}
	return cfg, nil
}

func componentInvocationSubject(nodeSubject string, serviceID grove.ServiceID) string {
	return nodeSubject + ".service." + strconv.FormatUint(uint64(serviceID), 10)
}
