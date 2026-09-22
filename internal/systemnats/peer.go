package systemnats

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/nats-io/nats.go"
)

const peerSubjectRoot = "_GROVE.system.peer."

type peerResponse struct {
	Error string `json:"error,omitempty"`
}

// ServePeer registers the internal command used to transfer the JetStream
// metadata witness to nodeID before its current logical owner retires.
func (t *Transport) ServePeer(ctx context.Context, nodeID string, server *Server) error {
	if nodeID == "" || server == nil {
		return &Error{Operation: "serve System NATS metadata peer", Err: ErrServerNameRequired}
	}
	if _, err := t.connection.Subscribe(peerSubjectRoot+nodeID, func(message *nats.Msg) {
		operationCtx, cancel := context.WithTimeout(context.Background(), defaultOperationTimeout)
		err := server.EnsurePeer(operationCtx)
		cancel()
		response := peerResponse{}
		if err != nil {
			response.Error = err.Error()
		}
		encoded, encodeErr := json.Marshal(response)
		if encodeErr == nil {
			_ = message.Respond(encoded)
		}
	}); err != nil {
		return &Error{Operation: "subscribe System NATS metadata peer endpoint", Err: err}
	}
	flushCtx, cancel := operationContext(ctx)
	defer cancel()
	if err := t.connection.FlushWithContext(flushCtx); err != nil {
		return &Error{Operation: "activate System NATS metadata peer endpoint", Err: err}
	}
	return nil
}

// RequestPeer asks nodeID to activate its dormant internal metadata witness.
func (t *Transport) RequestPeer(ctx context.Context, nodeID string) error {
	message, err := t.connection.RequestWithContext(ctx, peerSubjectRoot+nodeID, nil)
	if err != nil {
		return &Error{Operation: "request System NATS metadata peer", Err: err}
	}
	var response peerResponse
	if err := json.Unmarshal(message.Data, &response); err != nil {
		return &Error{Operation: "decode System NATS metadata peer response", Err: err}
	}
	if response.Error != "" {
		return &Error{Operation: "start System NATS metadata peer", Err: errors.New(response.Error)}
	}
	return nil
}
