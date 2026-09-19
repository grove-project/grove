package main

import (
	"context"
	"errors"
	"testing"

	"github.com/grove-project/grove"
)

type completedDebugAttachSession struct{}

func (completedDebugAttachSession) Wait(context.Context) error { return nil }

type recordedDebugAttachTransport struct {
	closed bool
}

func (t *recordedDebugAttachTransport) Close() { t.closed = true }

func TestParseDebugAttachArguments(t *testing.T) {
	service, listen, err := parseDebugAttachArguments([]string{"orders", "--listen", "127.0.0.1:40000"})
	if err != nil {
		t.Fatal(err)
	}
	if service != "orders" || listen != "127.0.0.1:40000" {
		t.Errorf("debug attach = (%q, %q); want orders and 127.0.0.1:40000", service, listen)
	}
	service, listen, err = parseDebugAttachArguments([]string{"payment"})
	if err != nil {
		t.Fatal(err)
	}
	if service != "payment" || listen != "127.0.0.1:0" {
		t.Errorf("default debug attach = (%q, %q); want payment and ephemeral loopback", service, listen)
	}
	if _, _, err := parseDebugAttachArguments(nil); !errors.Is(err, errDebugServiceRequired) {
		t.Errorf("missing service error = %v; want %v", err, errDebugServiceRequired)
	}
	if _, _, err := parseDebugAttachArguments([]string{"orders", "unexpected"}); !errors.Is(err, errConsoleArguments) {
		t.Errorf("unexpected argument error = %v; want %v", err, errConsoleArguments)
	}
}

func TestDebugAttachActionTracksActiveSessionUntilDisconnect(t *testing.T) {
	controller := newApplicationController("groveshop", t.TempDir())
	result := debugAttachResult{
		ServiceID: grove.ServiceID(1), ServiceName: "Orders",
		NodeID: "node-2", WorkerID: "orders-1", DAPEndpoint: "127.0.0.1:40000",
	}
	key := controller.registerDebugSession(result)
	sessions := copyDebugSessions(controller.debugSessions)
	if len(sessions) != 1 || sessions[0].DAPEndpoint != result.DAPEndpoint {
		t.Fatalf("active sessions = %#v; want Orders DAP endpoint", sessions)
	}
	transport := &recordedDebugAttachTransport{}
	action := &debugAttachAction{
		result:    result,
		session:   completedDebugAttachSession{},
		transport: transport,
		onDone:    func() { controller.unregisterDebugSession(key) },
	}
	if err := action.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !transport.closed {
		t.Fatal("debug transport was not closed")
	}
	if sessions := copyDebugSessions(controller.debugSessions); len(sessions) != 0 {
		t.Fatalf("active sessions after disconnect = %#v; want none", sessions)
	}
}
