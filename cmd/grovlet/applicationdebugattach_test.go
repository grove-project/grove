package main

import (
	"errors"
	"testing"
)

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
