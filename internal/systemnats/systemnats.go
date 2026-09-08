// Package systemnats hides Grove's System NATS server and request/reply
// transport from application-facing packages.
package systemnats

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/grove-project/grove"
	server "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

const defaultOperationTimeout = 5 * time.Second

var (
	// ErrServerNotReady is returned when an embedded server does not become
	// ready before its startup context ends.
	ErrServerNotReady = errors.New("system NATS server is not ready")
	// ErrSubjectRequired is returned when a transport operation has no subject.
	ErrSubjectRequired = errors.New("system NATS subject is required")
	// ErrHandlerRequired is returned when Serve receives a nil Handler.
	ErrHandlerRequired = errors.New("system NATS handler is required")
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

// Server is an embedded standalone System NATS server.
type Server struct {
	server *server.Server
	once   sync.Once
}

// StartServer starts an embedded System NATS server on host and port and waits
// until it accepts connections. A zero port selects a free port on host.
func StartServer(ctx context.Context, host string, port int) (*Server, error) {
	if err := ctx.Err(); err != nil {
		return nil, &Error{Operation: "start System NATS server", Err: err}
	}
	if port == 0 {
		port = server.RANDOM_PORT
	}
	natsServer, err := server.NewServer(&server.Options{
		Host:   host,
		Port:   port,
		NoLog:  true,
		NoSigs: true,
	})
	if err != nil {
		return nil, &Error{Operation: "create System NATS server", Err: err}
	}
	natsServer.Start()
	ready := make(chan bool, 1)
	go func() {
		ready <- natsServer.ReadyForConnections(operationTimeout(ctx))
	}()
	select {
	case isReady := <-ready:
		if isReady {
			return &Server{server: natsServer}, nil
		}
		natsServer.Shutdown()
		natsServer.WaitForShutdown()
		cause := ctx.Err()
		if cause == nil {
			cause = ErrServerNotReady
		}
		return nil, &Error{Operation: "start System NATS server", Err: cause}
	case <-ctx.Done():
		natsServer.Shutdown()
		natsServer.WaitForShutdown()
		return nil, &Error{Operation: "start System NATS server", Err: ctx.Err()}
	}
}

// URL returns the client connection URL for the embedded server.
func (s *Server) URL() string {
	return s.server.ClientURL()
}

// Shutdown closes clients and stops the embedded server. It is safe to call
// Shutdown more than once.
func (s *Server) Shutdown() {
	s.once.Do(func() {
		s.server.Shutdown()
		s.server.WaitForShutdown()
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
