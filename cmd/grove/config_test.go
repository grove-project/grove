package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/grovetest"
	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/systemnats"
)

const configTestManifest = `{"format_version":1,"application_id":"grove-shop","code_version":"v-test","components":[{"service_id":1,"name":"Orders","runtime":"process","entrypoint":["worker","--component","orders"]}],"ui_assets":["web/index.html"],"config_region":{"format_version":1,"capacity":4096}}`

func TestParseConfigInvocation(t *testing.T) {
	tests := []struct {
		args   []string
		action configAction
		err    error
	}{
		{args: []string{"config", "validate", "--binary", "app", "--config", "app.yaml"}, action: configValidate},
		{args: []string{"config", "embed", "--binary", "app", "--config", "app.yaml", "--output", "configured"}, action: configEmbed},
		{args: []string{"config", "inspect", "--binary", "configured"}, action: configInspect},
		{args: []string{"config", "extract", "--binary", "configured", "--output", "app.yaml"}, action: configExtract},
		{args: []string{"config"}, err: errConfigAction},
		{args: []string{"config", "set", "--binary", "app"}, err: errConfigAction},
		{args: []string{"config", "inspect"}, err: errBinaryPathRequired},
		{args: []string{"config", "validate", "--binary", "app"}, err: errConfigPathRequired},
		{args: []string{"config", "embed", "--binary", "app", "--config", "app.yaml"}, err: errOutputPathRequired},
		{args: []string{"config", "extract", "--binary", "app"}, err: errOutputPathRequired},
		{args: []string{"config", "inspect", "--binary", "app", "extra"}, err: errUnexpectedArguments},
	}
	for _, test := range tests {
		parsed, err := parseInvocation(test.args, bytes.NewBuffer(nil))
		if test.err != nil {
			if !errors.Is(err, test.err) {
				t.Errorf("parseInvocation(%q) error = %v; want %v", test.args, err, test.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseInvocation(%q): %v", test.args, err)
			continue
		}
		if parsed.command != commandConfig || parsed.configAction != test.action {
			t.Errorf("parseInvocation(%q) = %#v", test.args, parsed)
		}
	}
}

func TestExecuteConfigLifecycle(t *testing.T) {
	directory := t.TempDir()
	basePath := filepath.Join(directory, "base")
	configPath := filepath.Join(directory, "config.yaml")
	configuredPath := filepath.Join(directory, "configured")
	extractedPath := filepath.Join(directory, "extracted.yaml")
	if err := os.WriteFile(basePath, configTestArtifactBytes(), 0o755); err != nil {
		t.Fatal(err)
	}
	source := []byte("accepted-by-target-v7: yes\n")
	if err := os.WriteFile(configPath, source, 0o600); err != nil {
		t.Fatal(err)
	}
	compilation := artifact.Compilation{
		ProtocolVersion: artifact.CompilerProtocolVersion,
		Revision:        "target-v7-r9",
		Encoding:        "target-v7-binary",
		Payload:         []byte("target-v7-compiled-result"),
		CanonicalYAML:   []byte("target: v7\nrevision: r9\n"),
		Facts:           map[string]string{"node.zone": "target-zone"},
	}
	compiler := func(_ context.Context, binary string, got []byte) (artifact.Compilation, error) {
		if binary != basePath || !bytes.Equal(got, source) {
			t.Errorf("compiler input = binary %q, source %q", binary, got)
		}
		return compilation, nil
	}
	var output bytes.Buffer
	validate := invocation{command: commandConfig, configAction: configValidate, binaryPath: basePath, configPath: configPath}
	if err := executeConfigWithCompiler(t.Context(), validate, &output, compiler); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "target-v7-r9") || !strings.Contains(output.String(), artifact.ConfigDigest(compilation.Payload)) {
		t.Errorf("config validate output = %q", output.String())
	}

	output.Reset()
	embed := invocation{command: commandConfig, configAction: configEmbed, binaryPath: basePath, configPath: configPath, outputPath: configuredPath}
	if err := executeConfigWithCompiler(t.Context(), embed, &output, compiler); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Created "+configuredPath) || !strings.Contains(output.String(), "node.zone") || !strings.Contains(output.String(), "target-zone") {
		t.Errorf("config embed output = %q", output.String())
	}
	base, err := artifact.InspectFile(basePath)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := artifact.InspectFile(configuredPath)
	if err != nil {
		t.Fatal(err)
	}
	if base.CodeDigest != configured.CodeDigest || base.ArtifactDigest == configured.ArtifactDigest || configured.Config.Digest != artifact.ConfigDigest(compilation.Payload) {
		t.Errorf("base/configured identity = %#v / %#v", base, configured)
	}

	output.Reset()
	inspect := invocation{command: commandConfig, configAction: configInspect, binaryPath: configuredPath}
	if err := executeConfigWithCompiler(t.Context(), inspect, &output, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Config revision") || !strings.Contains(output.String(), "target-v7-r9") {
		t.Errorf("config inspect output = %q", output.String())
	}

	extract := invocation{command: commandConfig, configAction: configExtract, binaryPath: configuredPath, outputPath: extractedPath}
	if err := executeConfigWithCompiler(t.Context(), extract, &output, nil); err != nil {
		t.Fatal(err)
	}
	extracted, err := os.ReadFile(extractedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(extracted, compilation.CanonicalYAML) {
		t.Errorf("extracted YAML = %q; want %q", extracted, compilation.CanonicalYAML)
	}
	if err := executeConfigWithCompiler(t.Context(), embed, &output, compiler); err == nil {
		t.Fatal("config embed overwrote an existing output")
	}
}

func TestCompileWithTargetPreservesTargetValidation(t *testing.T) {
	valid, err := compileWithTarget(t.Context(), grovletPath, []byte("inventory:\n  reservation_buffer: 3\n"))
	if err != nil {
		t.Fatal(err)
	}
	if valid.Revision != "default" || valid.Encoding != "gob" {
		t.Errorf("target compilation = %#v", valid)
	}
	_, err = compileWithTarget(t.Context(), grovletPath, []byte("inventory:\n  reservation_buffer: -1\n"))
	var targetErr *targetCompilationError
	if !errors.As(err, &targetErr) || targetErr.failure.Field != "inventory.reservation_buffer" {
		t.Errorf("target validation error = %v; want structured reservation_buffer failure", err)
	}
}

// The real CLI delegates to the built target, creates distinct immutable
// variants, and deploys the configured bytes as normal Grovlet processes.
func TestConfiguredArtifactLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	directory := t.TempDir()
	cloudConfig := filepath.Join(directory, "cloud.yaml")
	edgeConfig := filepath.Join(directory, "edge.yaml")
	invalidConfig := filepath.Join(directory, "invalid.yaml")
	cloudArtifact := filepath.Join(directory, "grove-shop-cloud")
	edgeArtifact := filepath.Join(directory, "grove-shop-edge")
	extractedConfig := filepath.Join(directory, "extracted.yaml")
	writeConfigFile(t, cloudConfig, "acme-cloud-r42", "cloud", 1)
	writeConfigFile(t, edgeConfig, "acme-edge-r42", "edge", 2)
	if err := os.WriteFile(invalidConfig, []byte("inventory:\n  reservation_buffer: -1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	output, err := runGroveCommand(ctx, "config", "validate", "--binary", grovletPath, "--config", cloudConfig)
	if err != nil || !strings.Contains(output, "Configuration  valid") {
		t.Fatalf("validate cloud config: %v; output=%q", err, output)
	}
	output, err = runGroveCommand(ctx, "config", "embed", "--binary", grovletPath, "--config", cloudConfig, "--output", cloudArtifact)
	if err != nil || !strings.Contains(output, "node.zone") || !strings.Contains(output, "cloud") {
		t.Fatalf("embed cloud config: %v; output=%q", err, output)
	}
	output, err = runGroveCommand(ctx, "config", "embed", "--binary", grovletPath, "--config", edgeConfig, "--output", edgeArtifact)
	if err != nil || !strings.Contains(output, "node.zone") || !strings.Contains(output, "edge") {
		t.Fatalf("embed edge config: %v; output=%q", err, output)
	}
	base, err := artifact.InspectFile(grovletPath)
	if err != nil {
		t.Fatal(err)
	}
	cloud, err := artifact.InspectFile(cloudArtifact)
	if err != nil {
		t.Fatal(err)
	}
	edge, err := artifact.InspectFile(edgeArtifact)
	if err != nil {
		t.Fatal(err)
	}
	if base.CodeDigest != cloud.CodeDigest || cloud.CodeDigest != edge.CodeDigest {
		t.Errorf("config variants changed code identity: base=%s cloud=%s edge=%s", base.CodeDigest, cloud.CodeDigest, edge.CodeDigest)
	}
	if cloud.Config.Digest == edge.Config.Digest || cloud.ArtifactDigest == edge.ArtifactDigest {
		t.Errorf("config variants are not distinct: cloud=%#v edge=%#v", cloud, edge)
	}
	if cloud.Config.Facts["node.zone"] != "cloud" || edge.Config.Facts["node.zone"] != "edge" {
		t.Errorf("variant node facts = cloud %v, edge %v", cloud.Config.Facts, edge.Config.Facts)
	}
	output, err = runGroveCommand(ctx, "config", "inspect", "--binary", edgeArtifact)
	if err != nil || !strings.Contains(output, "Config revision  acme-edge-r42") || !strings.Contains(output, "node.zone        edge") {
		t.Fatalf("inspect edge config: %v; output=%q", err, output)
	}
	output, err = runGroveCommand(ctx, "config", "extract", "--binary", cloudArtifact, "--output", extractedConfig)
	if err != nil || !strings.Contains(output, "Extracted ") {
		t.Fatalf("extract cloud config: %v; output=%q", err, output)
	}
	extracted, err := os.ReadFile(extractedConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(extracted, cloud.CanonicalYAML) || !strings.Contains(string(extracted), "name: production") {
		t.Errorf("extracted config = %q", extracted)
	}
	invalidArtifact := filepath.Join(directory, "invalid-artifact")
	output, err = runGroveCommand(ctx, "config", "embed", "--binary", grovletPath, "--config", invalidConfig, "--output", invalidArtifact)
	if err == nil || !strings.Contains(output, "inventory.reservation_buffer") {
		t.Fatalf("invalid config result: %v; output=%q", err, output)
	}
	if _, err := os.Stat(invalidArtifact); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("invalid config produced artifact: %v", err)
	}

	nodes, systemNATSURL := startGrovletsFromArtifact(t, ctx, cloudArtifact)
	defer stopGrovlets(t, nodes)
	for _, node := range nodes {
		logs := node.Logs()
		if !strings.Contains(logs, `"config_revision":"acme-cloud-r42"`) || !strings.Contains(logs, `"cluster_name":"production"`) || !strings.Contains(logs, `"node_zone":"cloud"`) {
			t.Errorf("configured Grovlet readiness = %q", logs)
		}
	}
	if err := waitForGroveOutput(ctx, systemNATSURL, "node-3", "Cluster     healthy\nNodes       3 / 3 healthy\nComponents  2 / 2 healthy\n", "status"); err != nil {
		t.Fatalf("wait for configured deployment: %v\n%s", err, grovletLogs(nodes))
	}
	transport, err := systemnats.Connect(ctx, systemNATSURL)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	if err := waitForObservedServices(ctx, transport, "node-3", groveshop.ServiceOrders, groveshop.ServiceInventory); err != nil {
		t.Fatalf("wait for configured placement: %v\n%s", err, grovletLogs(nodes))
	}
	client, err := transport.ObservedPlacementClient("node-3")
	if err != nil {
		t.Fatal(err)
	}
	tooLargeCtx, tooLargeCancel := context.WithTimeout(ctx, time.Second)
	_, err = grove.Call[groveshop.CreateOrderRequest, groveshop.Order](tooLargeCtx, client, groveshop.ServiceOrders, groveshop.MethodCreateOrder, groveshop.CreateOrderRequest{
		OrderID: "order-over-buffer", SKU: "coffee-beans", Quantity: 2, AmountCents: 2400, ShippingAddress: "24 Grove Lane",
	})
	tooLargeCancel()
	if err == nil || !strings.Contains(err.Error(), groveshop.ErrReservationBufferExceeded.Error()) {
		t.Errorf("order over embedded buffer error = %v; want %v", err, groveshop.ErrReservationBufferExceeded)
	}
	created, err := grove.Call[groveshop.CreateOrderRequest, groveshop.Order](ctx, client, groveshop.ServiceOrders, groveshop.MethodCreateOrder, groveshop.CreateOrderRequest{
		OrderID: "order-configured", SKU: "coffee-beans", Quantity: 1, AmountCents: 1200, ShippingAddress: "24 Grove Lane",
	})
	if err != nil {
		t.Fatalf("configured order: %v\n%s", err, grovletLogs(nodes))
	}
	if created.Status != groveshop.OrderCompleted {
		t.Errorf("configured order status = %q; want %q", created.Status, groveshop.OrderCompleted)
	}

	corruptBytes, err := os.ReadFile(cloudArtifact)
	if err != nil {
		t.Fatal(err)
	}
	region := bytes.Index(corruptBytes, []byte(artifact.ConfigRegionPrefix+"GRVCFG01"))
	if region < 0 {
		t.Fatal("configured artifact region not found")
	}
	corruptBytes[region+len(artifact.ConfigRegionPrefix)+12] ^= 0xff
	corruptArtifact := filepath.Join(directory, "corrupt-artifact")
	if err := os.WriteFile(corruptArtifact, corruptBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	corruptNode, err := grovetest.StartNode(corruptArtifact)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = corruptNode.Cleanup() })
	corruptCtx, corruptCancel := context.WithTimeout(ctx, 2*time.Second)
	err = corruptNode.WaitReady(corruptCtx)
	corruptCancel()
	logs := corruptNode.Logs()
	if err == nil || (logs != "" && !strings.Contains(logs, artifact.ErrConfigCorrupt.Error())) {
		t.Errorf("corrupt artifact startup error = %v; logs=%q", err, corruptNode.Logs())
	}
}

func waitForObservedServices(ctx context.Context, transport *systemnats.Transport, nodeID string, serviceIDs ...grove.ServiceID) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var (
		lastView systemnats.PlacementView
		lastErr  error
	)
	for {
		requestCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		view, err := transport.RequestPlacement(requestCtx, nodeID)
		cancel()
		if err == nil {
			lastView = view
			found := make(map[grove.ServiceID]bool, len(view.Placements))
			for _, placement := range view.Placements {
				found[placement.ServiceID] = true
			}
			complete := view.Ready
			for _, serviceID := range serviceIDs {
				complete = complete && found[serviceID]
			}
			if complete {
				return nil
			}
		} else {
			lastErr = err
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("placement for %q did not expose services %v: last view=%#v: %w", nodeID, serviceIDs, lastView, errors.Join(lastErr, err))
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("placement for %q did not expose services %v: last view=%#v: %w", nodeID, serviceIDs, lastView, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func writeConfigFile(t *testing.T, path, revision, zone string, reservationBuffer int) {
	t.Helper()
	source := "revision: " + revision + "\n" +
		"customer:\n  name: Acme Retail\n" +
		"cluster:\n  name: production\n" +
		"node:\n  zone: " + zone + "\n" +
		"inventory:\n  reservation_buffer: " + strconv.Itoa(reservationBuffer) + "\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

func configTestArtifactBytes() []byte {
	return []byte("binary-prefix" +
		artifact.ManifestPrefix + configTestManifest + artifact.ManifestSuffix +
		artifact.ConfigRegionPrefix + strings.Repeat("\x00", artifact.ConfigRegionCapacity) + artifact.ConfigRegionSuffix +
		"binary-suffix")
}
