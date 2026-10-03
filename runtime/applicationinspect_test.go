package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
	groveshop "github.com/grove-project/grove/internal/testapp"
)

// The console reads Grove's state from the control plane, not from the
// application: with the application's Web component stopped, its ingress
// is gone, yet status still reports every node and placement and shows the
// stopped Web placement.
func TestConsoleStatusDoesNotDependOnApplicationIngress(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	controller := newApplicationController(grovletPath(t), t.TempDir())
	t.Cleanup(controller.close)
	active, err := controller.startRollout(ctx, []string{"--config", filepath.Join("..", "configs", "acme.yaml")})
	if err != nil {
		t.Fatalf("start known-good rollout: %v", err)
	}
	webURL := active.(rolloutActionResult).WebURL
	status, err := controller.status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	placements := len(status.Placements)
	webNode := applicationPlacementNodeFromStatus(status, groveshop.ServiceWeb)
	if status.Health != "healthy" || webNode == "" {
		t.Fatalf("status before stopping Web = %#v", status)
	}

	transport, err := systemnats.Connect(ctx, controller.attached().systemNATSURL)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	if _, err := transport.RequestStopComponent(ctx, webNode, groveshop.ServiceWeb); err != nil {
		t.Fatalf("stop Web on %s: %v", webNode, err)
	}
	// The Grovlet restarts Web from the desired deployment, so observe the
	// window in which the ingress is gone: status must still be readable
	// and must show Web not serving.
	for {
		var ingress ClusterStatus
		probeCtx, probeCancel := context.WithTimeout(ctx, time.Second)
		ingressErr := readApplicationJSON(probeCtx, webURL+"/grove/status", &ingress)
		probeCancel()
		status, err = controller.status(ctx)
		if err != nil {
			t.Fatalf("status while Web is stopped: %v", err)
		}
		if ingressErr != nil && placementHealth(status, groveshop.ServiceWeb) != string(systemnats.ComponentHealthy) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("never observed status while the ingress was down; last status %#v", status)
		case <-time.After(applicationConditionInterval):
		}
	}
	if status.Health != "degraded" || len(status.Nodes) != 3 || len(status.Placements) != placements {
		t.Errorf("status with Web stopped = health %q, %d nodes, %d placements", status.Health, len(status.Nodes), len(status.Placements))
	}
	if placementHealth(status, groveshop.ServiceOrders) != string(systemnats.ComponentHealthy) {
		t.Errorf("Orders placement = %q; want healthy", placementHealth(status, groveshop.ServiceOrders))
	}
}

func placementHealth(status ClusterStatus, serviceID grove.ServiceID) string {
	for _, placement := range status.Placements {
		if placement.ServiceID == serviceID {
			return placement.Health
		}
	}
	return ""
}
