package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/inspect"
	"github.com/grove-project/grove/internal/systemnats"
)

// applicationProcess is what every component hosted by one application
// process shares: the System NATS connection, the routed Grove client, the
// exclusive-capability provider and the embedded configuration. Both the
// node's shared application runtime and an isolated worker are one
// applicationProcess; they differ only in how many components they host.
type applicationProcess struct {
	ctx       context.Context
	transport *systemnats.Transport
	nodeID    string
	client    *grove.Client
	provider  *systemnats.RemoteExclusiveProvider
	files     *systemnats.RemoteFileStore
	config    Configuration
	digest    string
	artifact  ArtifactStatus
}

func newApplicationProcess(ctx context.Context, systemNATSURL, nodeID string) (*applicationProcess, error) {
	inspection, applicationConfig, err := loadEmbeddedConfiguration()
	if err != nil {
		return nil, fmt.Errorf("load embedded application configuration: %w", err)
	}
	transport, err := systemnats.Connect(ctx, systemNATSURL)
	if err != nil {
		return nil, err
	}
	// Calls whose selected destination is an endpoint this process serves are
	// dispatched in-process by the transport; every other call keeps the
	// distributed placement routing.
	client, err := transport.ObservedPlacementClient(nodeID)
	if applicationDeclaresHandlers() {
		client, err = grove.NewRoutedClient(transport.ObservedHandlerRouter(
			nodeID, applicationManagesHandler, transport.ObservedPlacementRouter(nodeID)))
	}
	if err != nil {
		transport.Close()
		return nil, err
	}
	process := &applicationProcess{
		ctx:       ctx,
		transport: transport,
		nodeID:    nodeID,
		client:    client,
		files:     transport.NewRemoteFileStore(ctx, nodeID, systemnats.DefaultLocalFileTTL),
		config:    applicationConfig,
		digest:    inspection.Config.Digest,
		artifact: ArtifactStatus{
			ApplicationID:  inspection.Manifest.ApplicationID,
			CodeVersion:    inspection.Manifest.CodeVersion,
			ArtifactDigest: inspection.ArtifactDigest,
			ConfigRevision: applicationConfig.Revision,
			ConfigDigest:   inspection.Config.Digest,
		},
	}
	// Background workloads started by Register claim exclusive capabilities
	// through the same provider as request handlers.
	if applicationDeclaresHandlers() {
		process.provider = transport.NewRemoteExclusiveProvider(ctx, nodeID, 0)
	}
	return process, nil
}

// withProviders attaches the runtime capabilities application code reaches
// through its context: Grove Files and, with handler-level placement,
// exclusive capabilities.
func (p *applicationProcess) withProviders(ctx context.Context) context.Context {
	ctx = grove.WithFileStore(ctx, p.files)
	if p.provider != nil {
		ctx = grove.WithExclusiveProvider(ctx, p.provider)
	}
	return ctx
}

// routeTo sends every Grove call the process's components make to one
// invocation subject instead of through placement. A standalone candidate
// uses it to call the candidate components it is paired with.
func (p *applicationProcess) routeTo(subject string) error {
	client, err := p.transport.RoutedClient(subject)
	if err != nil {
		return err
	}
	p.client = client
	return nil
}

func (p *applicationProcess) Close() {
	p.transport.Close()
}

// componentLaunch is one request to host a component in an application
// process.
type componentLaunch struct {
	Kind          string   `json:"component"`
	Subject       string   `json:"subject"`
	ListenAddress string   `json:"listen,omitempty"`
	Options       []string `json:"options,omitempty"`
}

// runningComponent is one component hosted by an applicationProcess. Its
// lifetime is independent from the process: stopping it leaves every other
// component in the process running.
type runningComponent struct {
	name     string
	cancel   context.CancelFunc
	endpoint *systemnats.Endpoint
	web      *http.Server
	webDone  chan error
	// failed is closed with failure set when the component ends on its own,
	// such as its HTTP listener failing.
	failed  chan struct{}
	failure error
}

// host registers and serves one component. Register runs with a context that
// ends when the component stops, so its background workloads stop with it.
func (p *applicationProcess) host(launch componentLaunch) (*runningComponent, error) {
	component, ok := activeApplication.componentByKind(launch.Kind)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrComponentUnknown, launch.Kind)
	}
	if (component.HTTPHandler != nil) != (launch.ListenAddress != "") {
		return nil, errors.New("HTTP component listen configuration is invalid")
	}
	componentCtx, cancel := context.WithCancel(p.ctx)
	registerCtx := p.withProviders(componentCtx)
	registry := &grove.Registry{}
	componentContext := ComponentContext{
		Context:       registerCtx,
		Registry:      registry,
		Client:        p.client,
		Configuration: p.config.Value,
		ConfigDigest:  p.digest,
		Artifact:      p.artifact,
		ReadStatus:    inspect.New(p.transport, applicationInspection(p.artifact), p.nodeID).Status,
		ListenAddress: launch.ListenAddress,
		NodeID:        p.nodeID,
		Options:       launch.Options,
	}
	running := &runningComponent{name: component.Name, cancel: cancel, failed: make(chan struct{})}
	if component.Register != nil {
		if err := component.Register(componentContext); err != nil {
			cancel()
			return nil, fmt.Errorf("register application component %s: %w", component.Name, err)
		}
	}
	if component.HTTPHandler != nil {
		listener, err := net.Listen("tcp", launch.ListenAddress)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("listen for application component %s: %w", component.Name, err)
		}
		handler, err := component.HTTPHandler(componentContext)
		if err != nil {
			_ = listener.Close()
			cancel()
			return nil, fmt.Errorf("create application component %s HTTP handler: %w", component.Name, err)
		}
		running.web = &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
		running.webDone = make(chan error, 1)
		go func() {
			err := running.web.Serve(listener)
			running.webDone <- err
			if !errors.Is(err, http.ErrServerClosed) {
				running.failure = fmt.Errorf("serve application component %s: %w", component.Name, err)
				close(running.failed)
			}
		}()
	}
	dispatcher, err := grove.NewDispatcher(registry)
	if err != nil {
		running.kill()
		return nil, err
	}
	serve := systemnats.Handler(func(ctx context.Context, request grove.RequestEnvelope) grove.ResponseEnvelope {
		return dispatcher.Dispatch(p.withProviders(ctx), request)
	})
	running.endpoint, err = p.transport.ServeEndpoint(p.ctx, launch.Subject, serve)
	if err != nil {
		running.kill()
		return nil, err
	}
	return running, nil
}

// stop withdraws the component's endpoint, shuts its HTTP server down
// gracefully and ends its Register context.
func (r *runningComponent) stop(ctx context.Context) error {
	var errs []error
	if r.endpoint != nil {
		errs = append(errs, r.endpoint.Stop())
	}
	if r.web != nil {
		if err := r.web.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("stop application component %s: %w", r.name, err))
		}
		if err := <-r.webDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs = append(errs, fmt.Errorf("serve application component %s: %w", r.name, err))
		}
	}
	r.cancel()
	return errors.Join(errs...)
}

// kill ends the component abruptly without draining its HTTP server. Other
// components in the same process are unaffected.
func (r *runningComponent) kill() {
	if r.endpoint != nil {
		_ = r.endpoint.Stop()
	}
	if r.web != nil {
		_ = r.web.Close()
	}
	r.cancel()
}
