// Package systemnats hides Grove's System NATS server and request/reply
// transport from application-facing packages.
package systemnats

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/grove-project/grove"
	server "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

const (
	defaultOperationTimeout = 5 * time.Second
	routeCheckInterval      = 10 * time.Millisecond
	systemClusterName       = "grove-system"
)

var (
	// ErrServerNotReady is returned when an embedded server does not become
	// ready before its startup context ends.
	ErrServerNotReady = errors.New("system NATS server is not ready")
	// ErrSubjectRequired is returned when a transport operation has no subject.
	ErrSubjectRequired = errors.New("system NATS subject is required")
	// ErrHandlerRequired is returned when Serve receives a nil Handler.
	ErrHandlerRequired = errors.New("system NATS handler is required")
	// ErrServerNameRequired is returned when a clustered server has no stable
	// node name.
	ErrServerNameRequired = errors.New("system NATS server name is required")
	// ErrRouteHostRequired is returned when a clustered server has no route
	// listener host.
	ErrRouteHostRequired = errors.New("system NATS route host is required")
	// ErrSeedURLInvalid is returned when a cluster seed is not an absolute NATS
	// route URL.
	ErrSeedURLInvalid = errors.New("system NATS seed URL is invalid")
)

// Error reports a failed hidden System NATS operation.
type Error struct {
	// Operation identifies the server or transport action that failed.
	Operation string
	// Err is the underlying NATS, Grove codec, or context error.
	Err error
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %v", e.Operation, e.Err)
}

func (e *Error) Unwrap() error {
	return e.Err
}

// Server is an embedded System NATS server.
type Server struct {
	server *server.Server
	once   sync.Once
}

// StartServer starts an embedded System NATS server on host and port and waits
// until it accepts connections. A zero port selects a free port on host.
func StartServer(ctx context.Context, host string, port int) (*Server, error) {
	return startServer(ctx, &server.Options{
		Host:   host,
		Port:   randomPort(port),
		NoLog:  true,
		NoSigs: true,
	}, false)
}

// ClusterConfig configures one embedded server in a System NATS cluster.
type ClusterConfig struct {
	// Name is the stable name of this NATS server within the cluster.
	Name string
	// Host is the client listener host.
	Host string
	// Port is the client listener port. Zero selects a free port.
	Port int
	// RouteHost is the server-route listener host.
	RouteHost string
	// RoutePort is the server-route listener port. Zero selects a free port.
	RoutePort int
	// SeedURLs are explicit NATS route URLs used to join an existing cluster.
	// An empty slice starts a seed server.
	SeedURLs []string
}

// StartClusterServer starts an embedded System NATS cluster server and waits
// until its listeners are ready. A joiner with SeedURLs also waits until at
// least one server route is established.
func StartClusterServer(ctx context.Context, cfg ClusterConfig) (*Server, error) {
	if cfg.Name == "" {
		return nil, &Error{Operation: "configure System NATS cluster", Err: ErrServerNameRequired}
	}
	if cfg.RouteHost == "" {
		return nil, &Error{Operation: "configure System NATS cluster", Err: ErrRouteHostRequired}
	}
	routes, err := parseSeedURLs(cfg.SeedURLs)
	if err != nil {
		return nil, err
	}
	return startServer(ctx, &server.Options{
		ServerName: cfg.Name,
		Host:       cfg.Host,
		Port:       randomPort(cfg.Port),
		Cluster: server.ClusterOpts{
			Name: systemClusterName,
			Host: cfg.RouteHost,
			Port: randomPort(cfg.RoutePort),
		},
		Routes: routes,
		NoLog:  true,
		NoSigs: true,
	}, len(routes) != 0)
}

func parseSeedURLs(seedURLs []string) ([]*url.URL, error) {
	routes := make([]*url.URL, 0, len(seedURLs))
	for _, seed := range seedURLs {
		route, err := url.Parse(seed)
		if err != nil || route.Scheme != "nats-route" || route.Host == "" {
			return nil, &Error{
				Operation: "parse System NATS seed URL",
				Err:       errors.Join(ErrSeedURLInvalid, err),
			}
		}
		routes = append(routes, route)
	}
	return routes, nil
}

func randomPort(port int) int {
	if port == 0 {
		return server.RANDOM_PORT
	}
	return port
}

func startServer(ctx context.Context, opts *server.Options, waitForRoute bool) (*Server, error) {
	if err := ctx.Err(); err != nil {
		return nil, &Error{Operation: "start System NATS server", Err: err}
	}
	natsServer, err := server.NewServer(opts)
	if err != nil {
		return nil, &Error{Operation: "create System NATS server", Err: err}
	}
	natsServer.Start()
	startCtx, cancel := operationContext(ctx)
	defer cancel()
	ready := make(chan bool, 1)
	go func() {
		ready <- natsServer.ReadyForConnections(operationTimeout(startCtx))
	}()
	select {
	case isReady := <-ready:
		if isReady {
			if waitForRoute {
				if err := waitForServerRoute(startCtx, natsServer); err != nil {
					shutdownServer(natsServer)
					return nil, &Error{Operation: "join System NATS cluster", Err: err}
				}
			}
			return &Server{server: natsServer}, nil
		}
		shutdownServer(natsServer)
		cause := startCtx.Err()
		if cause == nil {
			cause = ErrServerNotReady
		}
		return nil, &Error{Operation: "start System NATS server", Err: cause}
	case <-startCtx.Done():
		shutdownServer(natsServer)
		return nil, &Error{Operation: "start System NATS server", Err: startCtx.Err()}
	}
}

func waitForServerRoute(ctx context.Context, natsServer *server.Server) error {
	if natsServer.NumRoutes() != 0 {
		return nil
	}
	ticker := time.NewTicker(routeCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if natsServer.NumRoutes() != 0 {
				return nil
			}
		case <-ctx.Done():
			return errors.Join(ErrServerNotReady, ctx.Err())
		}
	}
}

func shutdownServer(natsServer *server.Server) {
	natsServer.Shutdown()
	natsServer.WaitForShutdown()
}

// URL returns the client connection URL for the embedded server.
func (s *Server) URL() string {
	return s.server.ClientURL()
}

// RouteURL returns the server-route URL for a clustered embedded server.
// Standalone servers return an empty string.
func (s *Server) RouteURL() string {
	address := s.server.ClusterAddr()
	if address == nil {
		return ""
	}
	return (&url.URL{Scheme: "nats-route", Host: address.String()}).String()
}

// Shutdown closes clients and stops the embedded server. It is safe to call
// Shutdown more than once.
func (s *Server) Shutdown() {
	s.once.Do(func() {
		shutdownServer(s.server)
	})
}

// Handler processes one decoded invocation envelope received over System NATS.
type Handler func(context.Context, grove.RequestEnvelope) grove.ResponseEnvelope

// Transport is a hidden System NATS request/reply connection.
type Transport struct {
	connection *nats.Conn
}

// Connect establishes a System NATS transport connection to url.
func Connect(ctx context.Context, url string) (*Transport, error) {
	if err := ctx.Err(); err != nil {
		return nil, &Error{Operation: "connect to System NATS", Err: err}
	}
	connection, err := nats.Connect(
		url,
		nats.Name("grove-system"),
		nats.Timeout(operationTimeout(ctx)),
	)
	if err != nil {
		return nil, &Error{Operation: "connect to System NATS", Err: err}
	}
	return &Transport{connection: connection}, nil
}

// Serve registers handler on one System NATS subject and waits until the
// subscription is active.
func (t *Transport) Serve(ctx context.Context, subject string, handler Handler) error {
	if subject == "" {
		return &Error{Operation: "serve System NATS endpoint", Err: ErrSubjectRequired}
	}
	if handler == nil {
		return &Error{Operation: "serve System NATS endpoint", Err: ErrHandlerRequired}
	}
	if _, err := t.connection.Subscribe(subject, func(message *nats.Msg) {
		var request grove.RequestEnvelope
		if err := grove.Decode(message.Data, &request); err != nil {
			respond(message, grove.ResponseEnvelope{
				Error: &grove.ResponseError{Code: grove.ErrorSerialization, Message: err.Error()},
			})
			return
		}
		response := handler(ctx, request)
		response.RequestID = request.RequestID
		respond(message, response)
	}); err != nil {
		return &Error{Operation: "subscribe System NATS endpoint", Err: err}
	}
	flushCtx, cancel := operationContext(ctx)
	defer cancel()
	if err := t.connection.FlushWithContext(flushCtx); err != nil {
		return &Error{Operation: "activate System NATS endpoint", Err: err}
	}
	return nil
}

func respond(message *nats.Msg, response grove.ResponseEnvelope) {
	encoded, err := grove.Encode(response)
	if err != nil {
		return
	}
	_ = message.Respond(encoded)
}

// Request sends one invocation envelope to subject and waits for its correlated
// response.
func (t *Transport) Request(
	ctx context.Context,
	subject string,
	request grove.RequestEnvelope,
) (grove.ResponseEnvelope, error) {
	if subject == "" {
		return grove.ResponseEnvelope{}, &Error{Operation: "request System NATS endpoint", Err: ErrSubjectRequired}
	}
	encoded, err := grove.Encode(request)
	if err != nil {
		return grove.ResponseEnvelope{}, &Error{Operation: "encode System NATS request", Err: err}
	}
	message, err := t.connection.RequestWithContext(ctx, subject, encoded)
	if err != nil {
		return grove.ResponseEnvelope{}, &Error{Operation: "request System NATS endpoint", Err: err}
	}
	var response grove.ResponseEnvelope
	if err := grove.Decode(message.Data, &response); err != nil {
		return grove.ResponseEnvelope{}, &Error{Operation: "decode System NATS response", Err: err}
	}
	if response.RequestID != request.RequestID {
		return grove.ResponseEnvelope{}, &Error{Operation: "correlate System NATS response", Err: grove.ErrRequestIDMismatch}
	}
	return response, nil
}

// RoutedClient creates a Grove Client that sends every call to subject through
// this System NATS connection.
func (t *Transport) RoutedClient(subject string) (*grove.Client, error) {
	if subject == "" {
		return nil, &Error{Operation: "create routed Grove client", Err: ErrSubjectRequired}
	}
	return grove.NewRoutedClient(subjectRouter{transport: t, subject: subject})
}

type subjectRouter struct {
	transport *Transport
	subject   string
}

func (r subjectRouter) Route(
	ctx context.Context,
	request grove.RequestEnvelope,
) (grove.ResponseEnvelope, error) {
	response, err := r.transport.Request(ctx, r.subject, request)
	if err != nil {
		return grove.ResponseEnvelope{}, fmt.Errorf("request: %w: %w", grove.ErrTransportFailure, err)
	}
	return response, nil
}

// Close closes the System NATS connection. It is safe to call Close more than
// once.
func (t *Transport) Close() {
	t.connection.Close()
}

func operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, defaultOperationTimeout)
}

func operationTimeout(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 {
			return remaining
		}
		return time.Nanosecond
	}
	return defaultOperationTimeout
}
