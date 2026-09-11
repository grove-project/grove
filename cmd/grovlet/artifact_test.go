package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/systemnats"
)

// One built artifact is inspected, deployed unchanged to three Grovlets, and
// exercised through its embedded Web UI and distributed business path.
func TestGroveShopArtifactDeploys(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()

	inspection, err := artifact.InspectFile(grovletPath)
	if err != nil {
		t.Fatal(err)
	}
	assertGroveShopManifest(t, inspection)
	ui, err := groveshop.WebAsset("index.html")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(grovletPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(binary, ui) {
		t.Fatal("built Grove Shop artifact does not contain its complete embedded UI")
	}

	webAddress := "127.0.0.1:" + strconv.Itoa(reserveGrovletRoutePorts(t, 1)[0])
	cluster := startMembershipGrovlets(
		t,
		ctx,
		[]string{
			"--system-nats-subject", "_GROVE.system.artifact.node-1",
			"--grove-shop-orders",
			"--grove-shop-web",
			"--grove-shop-web-listen", webAddress,
		},
		[]string{
			"--system-nats-subject", "_GROVE.system.artifact.node-2",
			"--grove-shop-inventory",
		},
		[]string{"--system-nats-subject", "_GROVE.system.artifact.node-3"},
	)
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if err := stopGrovlets(stopCtx, cluster.nodes); err != nil {
			t.Errorf("stop artifact cluster: %v\n%s", err, clusterLogs(cluster.nodes))
		}
	}()

	wantPlacement := []systemnats.PlacementRecord{
		{ServiceID: groveshop.ServiceOrders, NodeID: "node-1", InvocationSubject: "_GROVE.system.artifact.node-1.service.1"},
		{ServiceID: groveshop.ServiceInventory, NodeID: "node-2", InvocationSubject: "_GROVE.system.artifact.node-2.service.2"},
		{ServiceID: groveshop.ServiceWeb, NodeID: "node-1", InvocationSubject: "_GROVE.system.artifact.node-1.service.5"},
	}
	if _, err := waitForGrovletPlacement(ctx, cluster, wantPlacement); err != nil {
		t.Fatalf("wait for artifact placement: %v\n%s", err, clusterLogs(cluster.nodes))
	}
	if _, err := waitForGrovletComponentState(ctx, cluster.transports[0], "node-1", groveshop.ServiceWeb, systemnats.ComponentHealthy); err != nil {
		t.Fatalf("wait for Web component: %v\n%s", err, clusterLogs(cluster.nodes))
	}
	page, err := waitForHTTPBody(ctx, "http://"+webAddress+"/")
	if err != nil {
		t.Fatalf("request embedded Grove Shop UI: %v\n%s", err, clusterLogs(cluster.nodes))
	}
	if !bytes.Equal(page, ui) {
		t.Error("Web component response differs from the UI bytes embedded in the artifact")
	}

	client, err := cluster.transports[2].ObservedPlacementClient("node-3")
	if err != nil {
		t.Fatal(err)
	}
	created, err := grove.Call[groveshop.CreateOrderRequest, groveshop.Order](
		ctx,
		client,
		groveshop.ServiceOrders,
		groveshop.MethodCreateOrder,
		groveshop.CreateOrderRequest{
			OrderID:         "order-artifact",
			SKU:             "coffee-beans",
			Quantity:        2,
			AmountCents:     2400,
			ShippingAddress: "23 Grove Lane",
		},
	)
	if err != nil {
		t.Fatalf("create order through deployed artifact: %v\n%s", err, clusterLogs(cluster.nodes))
	}
	if created.Status != groveshop.OrderCompleted || created.Reservation.ID != "reservation-order-artifact" {
		t.Errorf("artifact order = %#v; want completed order and reservation-order-artifact", created)
	}
}

func assertGroveShopManifest(t *testing.T, inspection artifact.Inspection) {
	t.Helper()
	manifest := inspection.Manifest
	if manifest.FormatVersion != artifact.FormatVersion || manifest.ApplicationID != "grove-shop" || manifest.CodeVersion != "v0.1.0-dev" {
		t.Errorf("artifact identity = %#v", manifest)
	}
	wantComponents := []artifact.Component{
		{ServiceID: groveshop.ServiceOrders, Name: "Orders", Runtime: "process", Entrypoint: []string{"worker", "--component", "orders"}},
		{ServiceID: groveshop.ServiceInventory, Name: "Inventory", Runtime: "process", Entrypoint: []string{"worker", "--component", "inventory"}},
		{ServiceID: groveshop.ServiceWeb, Name: "Web", Runtime: "process", Entrypoint: []string{"worker", "--component", "web"}},
	}
	if !slices.EqualFunc(manifest.Components, wantComponents, func(a, b artifact.Component) bool {
		return a.ServiceID == b.ServiceID && a.Name == b.Name && a.Runtime == b.Runtime && slices.Equal(a.Entrypoint, b.Entrypoint)
	}) {
		t.Errorf("artifact components = %#v; want %#v", manifest.Components, wantComponents)
	}
	if !slices.Equal(manifest.UIAssets, []string{"web/index.html"}) {
		t.Errorf("artifact UI assets = %q; want web/index.html", manifest.UIAssets)
	}
	if manifest.ConfigRegion.FormatVersion != artifact.ConfigFormatVersion || manifest.ConfigRegion.Capacity != artifact.ConfigRegionCapacity || !inspection.ConfigEmpty {
		t.Errorf("artifact config reservation = %#v, empty=%t", manifest.ConfigRegion, inspection.ConfigEmpty)
	}
	if !strings.HasPrefix(inspection.MetadataDigest, "sha256:") || !strings.HasPrefix(inspection.ArtifactDigest, "sha256:") || inspection.MetadataDigest == inspection.ArtifactDigest {
		t.Errorf("artifact digests = metadata %q, artifact %q", inspection.MetadataDigest, inspection.ArtifactDigest)
	}
}

func TestParseWebWorkerConfig(t *testing.T) {
	args := []string{
		"--component", workerWeb,
		"--system-nats-url", "nats://127.0.0.1:4222",
		"--subject", "_GROVE.system.artifact.node-1.service.5",
		"--placement-node-id", "node-1",
		"--parent-fd", "3",
		"--web-listen", "127.0.0.1:8080",
	}
	cfg, err := parseWorkerConfig(args, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.component != workerWeb || cfg.webListen != "127.0.0.1:8080" {
		t.Errorf("Web worker config = %#v", cfg)
	}
	withoutListen := slices.Delete(append([]string(nil), args...), len(args)-2, len(args))
	if _, err := parseWorkerConfig(withoutListen, io.Discard); err == nil {
		t.Fatal("Web worker without listen address returned nil error")
	}
	ordersWithListen := append([]string(nil), args...)
	ordersWithListen[1] = workerOrders
	if _, err := parseWorkerConfig(ordersWithListen, io.Discard); err == nil {
		t.Fatal("Orders worker with Web listen address returned nil error")
	}
}

func waitForHTTPBody(ctx context.Context, url string) ([]byte, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	client := &http.Client{}
	var lastErr error
	for {
		requestCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, url, nil)
		if err == nil {
			var response *http.Response
			response, err = client.Do(request)
			if err == nil {
				body, readErr := io.ReadAll(response.Body)
				closeErr := response.Body.Close()
				err = errors.Join(readErr, closeErr)
				if err == nil && response.StatusCode == http.StatusOK {
					cancel()
					return body, nil
				}
				if err == nil {
					err = fmt.Errorf("HTTP status %s", response.Status)
				}
			}
		}
		cancel()
		lastErr = err
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil, fmt.Errorf("HTTP endpoint %s did not become ready: %w", url, errors.Join(lastErr, ctx.Err()))
		}
	}
}
