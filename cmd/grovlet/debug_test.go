package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/grove-project/grove/internal/systemnats"
)

func TestDebugControllerRestoresSupervisionWhenDelveCannotStart(t *testing.T) {
	process := newFakeComponentProcess()
	manager := newComponentManager([]componentSpec{{
		serviceID: 3, name: "Payment", kind: workerPayment,
	}}, func(context.Context, componentSpec) (componentProcess, error) {
		return process, nil
	})
	if err := manager.StartComponent(t.Context(), 3); err != nil {
		t.Fatal(err)
	}
	controller := newDebugController("node-4", t.TempDir(), t.TempDir()+"/missing-dlv", manager)
	_, _, err := controller.OpenDebug(t.Context(), systemnats.DebugRequest{ServiceID: 3, SessionID: "missing-delve"})
	if err == nil || !strings.Contains(err.Error(), "start Delve") {
		t.Fatalf("missing Delve error = %v", err)
	}
	assertComponentState(t, manager, systemnats.ComponentHealthy)
	if _, _, err := controller.OpenDebug(t.Context(), systemnats.DebugRequest{ServiceID: 4, SessionID: "missing-worker"}); !errors.Is(err, errComponentNotHosted) {
		t.Errorf("missing worker error = %v; want %v", err, errComponentNotHosted)
	}
}
