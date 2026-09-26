package debuggateway

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
)

type stubComponents struct{ view systemnats.ComponentView }

func (s stubComponents) SnapshotComponents() systemnats.ComponentView        { return s.view }
func (stubComponents) StartComponent(context.Context, grove.ServiceID) error { return nil }
func (stubComponents) StopComponent(context.Context, grove.ServiceID) error  { return nil }
func (stubComponents) KillComponent(context.Context, grove.ServiceID) error  { return nil }

// A service hosted on several nodes is debugged on the node the caller names,
// and a placement that is not running there is reported against that node.
func TestOpenOnNodeTargetsTheNamedPlacement(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
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
	for node, state := range map[string]systemnats.ComponentState{"node-1": systemnats.ComponentHealthy, "node-2": systemnats.ComponentFailed} {
		view := systemnats.ComponentView{Components: []systemnats.ComponentStatus{{ServiceID: 3, Name: "Payment", State: state}}}
		if err := transport.ServeComponents(ctx, node, stubComponents{view: view}); err != nil {
			t.Fatal(err)
		}
	}

	_, err = OpenOnNode(ctx, transport, "node-2", "payment", "127.0.0.1:0")
	if !errors.Is(err, ErrServiceNotRunning) || !strings.Contains(err.Error(), "node-2") {
		t.Errorf("failed placement error = %v; want ErrServiceNotRunning naming node-2", err)
	}
	_, err = OpenOnNode(ctx, transport, "node-1", "orders", "127.0.0.1:0")
	if !errors.Is(err, ErrServiceNotRunning) || !strings.Contains(err.Error(), "node-1") {
		t.Errorf("unhosted service error = %v; want ErrServiceNotRunning naming node-1", err)
	}
}
