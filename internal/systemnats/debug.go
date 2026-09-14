package systemnats

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/grove-project/grove"
	"github.com/nats-io/nats.go"
)

const (
	debugOpenSubjectRoot = "_GROVE.system.debug.open."
	debugChunkSize       = 32 * 1024
)

var (
	// ErrDebugControllerRequired is returned when a Grovlet has no local debug
	// session controller.
	ErrDebugControllerRequired = errors.New("debug controller is required")
	// ErrDebugRequestInvalid is returned when a debug request lacks a service or
	// session identity.
	ErrDebugRequestInvalid = errors.New("debug request is invalid")
	// ErrDebugSessionFailed is returned when the selected Grovlet cannot open a
	// debugger session.
	ErrDebugSessionFailed = errors.New("debug session failed")
)

// DebugRequest selects one service worker for an ephemeral debugger session.
type DebugRequest struct {
	// ServiceID is the stable application-owned service identifier.
	ServiceID grove.ServiceID `json:"service_id"`
	// SessionID uniquely identifies this debugger connection.
	SessionID string `json:"session_id"`
}

// DebugTarget describes the worker selected by a Grovlet for debugging.
type DebugTarget struct {
	// ServiceID is the selected service identifier.
	ServiceID grove.ServiceID `json:"service_id"`
	// ServiceName is the selected application-owned service name.
	ServiceName string `json:"service_name"`
	// NodeID is the selected Grovlet identity.
	NodeID string `json:"node_id"`
	// WorkerID is the selected generation-derived worker identity.
	WorkerID string `json:"worker_id"`
	// ArtifactDigest identifies the selected immutable artifact.
	ArtifactDigest string `json:"artifact_digest"`
	// CodeVersion identifies the application build version.
	CodeVersion string `json:"code_version"`
	// ProcessID is the node-local attach target. It is consumed by the Grove
	// gateway and must not be presented as a user-supplied selector.
	ProcessID int `json:"process_id"`
}

// DebugController opens node-local debugger streams for hosted workers.
type DebugController interface {
	// OpenDebug starts or attaches the debugger selected by request. Closing the
	// returned stream must release debugger resources and normal supervision.
	OpenDebug(context.Context, DebugRequest) (DebugTarget, io.ReadWriteCloser, error)
}

type debugOpenRequest struct {
	DebugRequest
	UpstreamSubject   string `json:"upstream_subject"`
	DownstreamSubject string `json:"downstream_subject"`
}

type debugOpenResponse struct {
	Target DebugTarget `json:"target"`
	Error  string      `json:"error,omitempty"`
}

type debugChunk struct {
	Data  []byte `json:"data,omitempty"`
	EOF   bool   `json:"eof,omitempty"`
	Error string `json:"error,omitempty"`
}

// DebugStream is one full-duplex DAP byte stream carried through System NATS.
type DebugStream struct {
	transport  *Transport
	upstream   string
	downstream *nats.Subscription
	messages   chan *nats.Msg
	pending    []byte
	readErr    error
	closeOnce  sync.Once
	closeErr   error
	closed     chan struct{}
}

// ServeDebug registers nodeID's ephemeral debugger endpoint.
func (t *Transport) ServeDebug(ctx context.Context, nodeID string, controller DebugController) error {
	if controller == nil {
		return &Error{Operation: "serve Grove debugger", Err: ErrDebugControllerRequired}
	}
	if _, err := t.connection.Subscribe(debugOpenSubjectRoot+nodeID, func(message *nats.Msg) {
		var request debugOpenRequest
		if err := json.Unmarshal(message.Data, &request); err != nil || !validDebugOpenRequest(request) {
			respondJSON(message, debugOpenResponse{Error: errors.Join(ErrDebugRequestInvalid, err).Error()})
			return
		}
		stream, err := serveDebugStream(ctx, t.connection, controller, request)
		if err != nil {
			respondJSON(message, debugOpenResponse{Error: err.Error()})
			return
		}
		respondJSON(message, debugOpenResponse{Target: stream.target})
	}); err != nil {
		return &Error{Operation: "subscribe Grove debugger", Err: err}
	}
	flushCtx, cancel := operationContext(ctx)
	defer cancel()
	if err := t.connection.FlushWithContext(flushCtx); err != nil {
		return &Error{Operation: "activate Grove debugger", Err: err}
	}
	return nil
}

type servedDebugStream struct {
	target DebugTarget
}

func serveDebugStream(
	ctx context.Context,
	connection *nats.Conn,
	controller DebugController,
	request debugOpenRequest,
) (servedDebugStream, error) {
	target, stream, err := controller.OpenDebug(ctx, request.DebugRequest)
	if err != nil {
		return servedDebugStream{}, err
	}
	var closeOnce sync.Once
	var upstream *nats.Subscription
	closed := make(chan struct{})
	closeStream := func() {
		closeOnce.Do(func() {
			close(closed)
			if upstream != nil {
				_ = upstream.Unsubscribe()
			}
			_ = stream.Close()
		})
	}
	upstream, err = connection.Subscribe(request.UpstreamSubject, func(message *nats.Msg) {
		var chunk debugChunk
		if err := json.Unmarshal(message.Data, &chunk); err != nil {
			publishDebugChunk(connection, request.DownstreamSubject, debugChunk{EOF: true, Error: err.Error()})
			closeStream()
			return
		}
		if chunk.EOF {
			closeStream()
			return
		}
		if len(chunk.Data) != 0 {
			if _, err := stream.Write(chunk.Data); err != nil {
				publishDebugChunk(connection, request.DownstreamSubject, debugChunk{EOF: true, Error: err.Error()})
				closeStream()
			}
		}
	})
	if err != nil {
		_ = stream.Close()
		return servedDebugStream{}, err
	}
	if err := connection.Flush(); err != nil {
		closeStream()
		return servedDebugStream{}, err
	}
	go func() {
		defer closeStream()
		buffer := make([]byte, debugChunkSize)
		for {
			count, err := stream.Read(buffer)
			if count != 0 {
				publishDebugChunk(connection, request.DownstreamSubject, debugChunk{Data: append([]byte(nil), buffer[:count]...)})
			}
			if err != nil {
				chunk := debugChunk{EOF: true}
				if !errors.Is(err, io.EOF) {
					chunk.Error = err.Error()
				}
				publishDebugChunk(connection, request.DownstreamSubject, chunk)
				return
			}
		}
	}()
	go func() {
		select {
		case <-ctx.Done():
			closeStream()
		case <-closed:
		}
	}()
	return servedDebugStream{target: target}, nil
}

func validDebugOpenRequest(request debugOpenRequest) bool {
	return request.ServiceID != 0 && request.SessionID != "" &&
		request.UpstreamSubject != "" && request.DownstreamSubject != ""
}

func publishDebugChunk(connection *nats.Conn, subject string, chunk debugChunk) {
	encoded, err := json.Marshal(chunk)
	if err == nil {
		_ = connection.Publish(subject, encoded)
	}
}

// OpenDebug opens a full-duplex debugger stream on nodeID.
func (t *Transport) OpenDebug(ctx context.Context, nodeID string, request DebugRequest) (DebugTarget, *DebugStream, error) {
	if nodeID == "" || request.ServiceID == 0 || request.SessionID == "" {
		return DebugTarget{}, nil, &Error{Operation: "open Grove debugger", Err: ErrDebugRequestInvalid}
	}
	upstream := nats.NewInbox()
	downstream := nats.NewInbox()
	messages := make(chan *nats.Msg, 64)
	subscription, err := t.connection.ChanSubscribe(downstream, messages)
	if err != nil {
		return DebugTarget{}, nil, &Error{Operation: "subscribe Grove debugger stream", Err: err}
	}
	open := debugOpenRequest{
		DebugRequest:      request,
		UpstreamSubject:   upstream,
		DownstreamSubject: downstream,
	}
	encoded, err := json.Marshal(open)
	if err != nil {
		_ = subscription.Unsubscribe()
		return DebugTarget{}, nil, &Error{Operation: "encode Grove debugger request", Err: err}
	}
	message, err := t.connection.RequestWithContext(ctx, debugOpenSubjectRoot+nodeID, encoded)
	if err != nil {
		_ = subscription.Unsubscribe()
		return DebugTarget{}, nil, &Error{Operation: "request Grove debugger", Err: err}
	}
	var response debugOpenResponse
	if err := json.Unmarshal(message.Data, &response); err != nil {
		_ = subscription.Unsubscribe()
		return DebugTarget{}, nil, &Error{Operation: "decode Grove debugger response", Err: err}
	}
	if response.Error != "" {
		_ = subscription.Unsubscribe()
		return DebugTarget{}, nil, &Error{Operation: "open Grove debugger", Err: errors.Join(ErrDebugSessionFailed, errors.New(response.Error))}
	}
	return response.Target, &DebugStream{
		transport:  t,
		upstream:   upstream,
		downstream: subscription,
		messages:   messages,
		closed:     make(chan struct{}),
	}, nil
}

// Read reads DAP bytes received from the selected Delve instance.
func (s *DebugStream) Read(buffer []byte) (int, error) {
	if len(s.pending) != 0 {
		count := copy(buffer, s.pending)
		s.pending = s.pending[count:]
		return count, nil
	}
	if s.readErr != nil {
		return 0, s.readErr
	}
	var (
		message *nats.Msg
		ok      bool
	)
	select {
	case message, ok = <-s.messages:
	case <-s.closed:
		return 0, io.EOF
	}
	if !ok {
		return 0, io.EOF
	}
	var chunk debugChunk
	if err := json.Unmarshal(message.Data, &chunk); err != nil {
		return 0, err
	}
	if chunk.Error != "" {
		s.readErr = errors.New(chunk.Error)
		return 0, s.readErr
	}
	if chunk.EOF {
		s.readErr = io.EOF
		return 0, io.EOF
	}
	count := copy(buffer, chunk.Data)
	s.pending = append(s.pending[:0], chunk.Data[count:]...)
	return count, nil
}

// Write sends DAP bytes to the selected Delve instance.
func (s *DebugStream) Write(data []byte) (int, error) {
	encoded, err := json.Marshal(debugChunk{Data: data})
	if err != nil {
		return 0, err
	}
	if err := s.transport.connection.Publish(s.upstream, encoded); err != nil {
		return 0, err
	}
	return len(data), nil
}

// Close ends the debugger stream and releases its System NATS subscription.
func (s *DebugStream) Close() error {
	s.closeOnce.Do(func() {
		close(s.closed)
		encoded, err := json.Marshal(debugChunk{EOF: true})
		if err == nil {
			err = s.transport.connection.Publish(s.upstream, encoded)
		}
		if err == nil {
			err = s.transport.connection.Flush()
		}
		s.closeErr = errors.Join(err, s.downstream.Unsubscribe())
	})
	return s.closeErr
}
