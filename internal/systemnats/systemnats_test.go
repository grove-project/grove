package systemnats_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
)

// Transport carries Grove envelopes without exposing NATS to the application
// SDK.
func ExampleTransport() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server, err := systemnats.StartServer(ctx, "127.0.0.1", 0)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer server.Shutdown()
	responder, err := systemnats.Connect(ctx, server.URL())
	if err != nil {
		fmt.Println(err)
		return
	}
	defer responder.Close()
	if err := responder.Serve(ctx, "_GROVE.system.invoke.example", func(_ context.Context, request grove.RequestEnvelope) grove.ResponseEnvelope {
		return grove.ResponseEnvelope{Payload: request.Payload}
	}); err != nil {
		fmt.Println(err)
		return
	}
	requester, err := systemnats.Connect(ctx, server.URL())
	if err != nil {
		fmt.Println(err)
		return
	}
	defer requester.Close()
	response, err := requester.Request(ctx, "_GROVE.system.invoke.example", grove.RequestEnvelope{
		RequestID: "request-1",
		ServiceID: 2,
		MethodID:  1,
		Payload:   []byte("inventory"),
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(response.RequestID, string(response.Payload))
	// Output:
	// request-1 inventory
}

func TestTransportRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	server, err := systemnats.StartServer(ctx, "127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Shutdown)
	responder, err := systemnats.Connect(ctx, server.URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(responder.Close)
	if err := responder.Serve(ctx, "_GROVE.system.invoke.test", func(_ context.Context, request grove.RequestEnvelope) grove.ResponseEnvelope {
		if request.ServiceID != 2 || request.MethodID != 1 {
			t.Errorf("request destination = (%d, %d); want (2, 1)", request.ServiceID, request.MethodID)
		}
		return grove.ResponseEnvelope{RequestID: "wrong", Payload: append([]byte("handled "), request.Payload...)}
	}); err != nil {
		t.Fatal(err)
	}
	requester, err := systemnats.Connect(ctx, server.URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(requester.Close)

	request := grove.RequestEnvelope{
		RequestID: "request-42",
		ServiceID: 2,
		MethodID:  1,
		Payload:   []byte("inventory"),
	}
	response, err := requester.Request(ctx, "_GROVE.system.invoke.test", request)
	if err != nil {
		t.Fatal(err)
	}
	if response.RequestID != request.RequestID {
		t.Errorf("response request ID = %q; want %q", response.RequestID, request.RequestID)
	}
	if got := string(response.Payload); got != "handled inventory" {
		t.Errorf("response payload = %q; want handled inventory", got)
	}
	if err := responder.Serve(ctx, "_GROVE.system.invoke.call", func(_ context.Context, request grove.RequestEnvelope) grove.ResponseEnvelope {
		return grove.ResponseEnvelope{Payload: request.Payload}
	}); err != nil {
		t.Fatal(err)
	}
	client, err := requester.RoutedClient("_GROVE.system.invoke.call")
	if err != nil {
		t.Fatal(err)
	}
	called, err := grove.Call[string, string](ctx, client, 2, 1, "inventory")
	if err != nil {
		t.Fatal(err)
	}
	if called != "inventory" {
		t.Errorf("routed Call() = %q; want inventory", called)
	}
	missingClient, err := requester.RoutedClient("_GROVE.system.invoke.missing")
	if err != nil {
		t.Fatal(err)
	}
	requestCtx, cancelMissing := context.WithTimeout(t.Context(), time.Second)
	defer cancelMissing()
	if _, err := grove.Call[string, string](requestCtx, missingClient, 2, 1, "inventory"); !errors.Is(err, grove.ErrTransportFailure) {
		t.Errorf("routed Call() transport error = %v; want %v", err, grove.ErrTransportFailure)
	}

	if err := responder.Serve(ctx, "", func(context.Context, grove.RequestEnvelope) grove.ResponseEnvelope {
		return grove.ResponseEnvelope{}
	}); !errors.Is(err, systemnats.ErrSubjectRequired) {
		t.Errorf("Serve() empty subject error = %v; want %v", err, systemnats.ErrSubjectRequired)
	}
	if err := responder.Serve(ctx, "_GROVE.system.invoke.nil", nil); !errors.Is(err, systemnats.ErrHandlerRequired) {
		t.Errorf("Serve() nil handler error = %v; want %v", err, systemnats.ErrHandlerRequired)
	}

	canceled, cancelRequest := context.WithCancel(t.Context())
	cancelRequest()
	if _, err := requester.Request(canceled, "_GROVE.system.invoke.missing", request); !errors.Is(err, context.Canceled) {
		t.Errorf("Request() canceled error = %v; want %v", err, context.Canceled)
	}
}
