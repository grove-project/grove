package systemnats_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
)

func TestComponentEndpointsControlHostedState(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	server, err := systemnats.StartServer(ctx, "127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Shutdown)
	transport, err := systemnats.Connect(ctx, server.URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.Close)
	controller := &fakeComponentController{state: systemnats.ComponentStopped}
	if err := transport.ServeComponents(ctx, "node-a", controller); err != nil {
		t.Fatal(err)
	}
	view, err := transport.RequestComponents(ctx, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Components) != 1 || view.Components[0].State != systemnats.ComponentStopped {
		t.Fatalf("initial component view = %#v; want stopped", view)
	}
	view, err = transport.RequestStartComponent(ctx, "node-a", 2)
	if err != nil {
		t.Fatal(err)
	}
	if view.Components[0].State != systemnats.ComponentHealthy {
		t.Errorf("started component view = %#v; want healthy", view)
	}
	view, err = transport.RequestStopComponent(ctx, "node-a", 2)
	if err != nil {
		t.Fatal(err)
	}
	if view.Components[0].State != systemnats.ComponentStopped {
		t.Errorf("stopped component view = %#v; want stopped", view)
	}
	if _, err := transport.RequestStartComponent(ctx, "node-a", 9); !errors.Is(err, systemnats.ErrComponentCommandFailed) {
		t.Errorf("failed component command error = %v; want %v", err, systemnats.ErrComponentCommandFailed)
	}
	if err := transport.ServeComponents(ctx, "node-b", nil); !errors.Is(err, systemnats.ErrComponentControllerRequired) {
		t.Errorf("nil controller error = %v; want %v", err, systemnats.ErrComponentControllerRequired)
	}
	if subject := systemnats.ComponentSubject("node-a"); subject != "_GROVE.system.components.node-a" {
		t.Errorf("component subject = %q; want _GROVE.system.components.node-a", subject)
	}
}

type fakeComponentController struct {
	state systemnats.ComponentState
}

func (c *fakeComponentController) SnapshotComponents() systemnats.ComponentView {
	return systemnats.ComponentView{Components: []systemnats.ComponentStatus{{
		ServiceID: 2,
		Name:      "Inventory",
		State:     c.state,
	}}}
}

func (c *fakeComponentController) StartComponent(_ context.Context, serviceID grove.ServiceID) error {
	if serviceID != 2 {
		return errors.New("not hosted")
	}
	c.state = systemnats.ComponentHealthy
	return nil
}

func (c *fakeComponentController) StopComponent(_ context.Context, serviceID grove.ServiceID) error {
	if serviceID != 2 {
		return errors.New("not hosted")
	}
	c.state = systemnats.ComponentStopped
	return nil
}
