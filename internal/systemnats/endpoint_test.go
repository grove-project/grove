package systemnats_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
	"github.com/nats-io/nats.go"
)

type callerKey struct{}

// A call whose destination is an endpoint of the caller's own process never
// leaves the process; other processes still reach the endpoint over System
// NATS, and a stopped endpoint is withdrawn from both paths.
func TestServeEndpointDispatchesOwnCallsInProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	server, err := systemnats.StartServer(ctx, "127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown()
	host, err := systemnats.Connect(ctx, server.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	remote, err := systemnats.Connect(ctx, server.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()

	const subject = "_GROVE.system.invoke.node-a.service.2"
	// The handler reports whether it runs on the caller's own context, which
	// only an in-process dispatch can carry.
	endpoint, err := host.ServeEndpoint(ctx, subject, func(ctx context.Context, request grove.RequestEnvelope) grove.ResponseEnvelope {
		route := "system-nats"
		if ctx.Value(callerKey{}) != nil {
			route = "in-process"
		}
		return grove.ResponseEnvelope{Payload: []byte(route)}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !host.LocalEndpoint(subject) || remote.LocalEndpoint(subject) {
		t.Fatalf("local endpoint = host %t remote %t; want only the host", host.LocalEndpoint(subject), remote.LocalEndpoint(subject))
	}
	callerCtx := context.WithValue(ctx, callerKey{}, true)
	request := grove.RequestEnvelope{RequestID: "request-1", ServiceID: 2, MethodID: 1}
	for _, tc := range []struct {
		name      string
		transport *systemnats.Transport
		want      string
	}{
		{name: "same process", transport: host, want: "in-process"},
		{name: "other process", transport: remote, want: "system-nats"},
	} {
		response, err := tc.transport.Request(callerCtx, subject, request)
		if err != nil {
			t.Fatalf("%s request: %v", tc.name, err)
		}
		if got := string(response.Payload); got != tc.want || response.RequestID != request.RequestID {
			t.Errorf("%s response = %q %q; want %q %q", tc.name, got, response.RequestID, tc.want, request.RequestID)
		}
	}

	if err := endpoint.Stop(); err != nil {
		t.Fatal(err)
	}
	if host.LocalEndpoint(subject) {
		t.Error("stopped endpoint is still local")
	}
	for name, transport := range map[string]*systemnats.Transport{"same process": host, "other process": remote} {
		requestCtx, requestCancel := context.WithTimeout(ctx, time.Second)
		_, err := transport.Request(requestCtx, subject, request)
		requestCancel()
		if err == nil {
			t.Errorf("%s request after stop succeeded; want no responders", name)
		} else if !errors.Is(err, nats.ErrNoResponders) {
			t.Errorf("%s request after stop = %v; want no responders", name, err)
		}
	}
}
