package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/internal/artifact"
)

func TestRunConfigCompile(t *testing.T) {
	source := []byte("revision: acme-r42\ncustomer:\n  name: Acme Retail\nnode:\n  zone: cloud\ninventory:\n  reservation_buffer: 7\n")
	var output bytes.Buffer
	if err := runConfigCompile(t.Context(), nil, bytes.NewReader(source), &output); err != nil {
		t.Fatal(err)
	}
	var response artifact.CompilationResponse
	if err := json.NewDecoder(&output).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Failure != nil || response.Compilation == nil {
		t.Fatalf("compiler response = %#v", response)
	}
	configuration, err := groveshop.DecodeConfiguration(response.Compilation.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Revision != "acme-r42" || configuration.Node.Zone != "cloud" || configuration.Inventory.ReservationBuffer != 7 {
		t.Errorf("compiled runtime configuration = %#v", configuration)
	}
	if response.Compilation.Facts["cluster.name"] != "local" || response.Compilation.Facts["node.zone"] != "cloud" {
		t.Errorf("compiler facts = %v", response.Compilation.Facts)
	}
}

func TestTargetCompilerDefaultsMatchBlankArtifactRuntime(t *testing.T) {
	var output bytes.Buffer
	if err := runConfigCompile(t.Context(), nil, bytes.NewReader(nil), &output); err != nil {
		t.Fatal(err)
	}
	var response artifact.CompilationResponse
	if err := json.NewDecoder(&output).Decode(&response); err != nil {
		t.Fatal(err)
	}
	compiled, err := groveshop.DecodeConfiguration(response.Compilation.Payload)
	if err != nil {
		t.Fatal(err)
	}
	_, runtimeConfiguration, err := loadEmbeddedGroveShopConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	if compiled != runtimeConfiguration {
		t.Errorf("compiler defaults = %#v; blank artifact runtime defaults = %#v", compiled, runtimeConfiguration)
	}
}

func TestRunConfigCompileReturnsStructuredValidation(t *testing.T) {
	var output bytes.Buffer
	err := runConfigCompile(t.Context(), nil, bytes.NewBufferString("inventory:\n  reservation_buffer: -1\n"), &output)
	if err != nil {
		t.Fatal(err)
	}
	var response artifact.CompilationResponse
	if err := json.NewDecoder(&output).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Compilation != nil || response.Failure == nil || response.Failure.Kind != "validation" || response.Failure.Field != "inventory.reservation_buffer" {
		t.Errorf("invalid compiler response = %#v", response)
	}
	if err := runConfigCompile(t.Context(), []string{"extra"}, bytes.NewReader(nil), &output); err == nil {
		t.Fatal("config compiler accepted arguments")
	}
}

func TestLoadEmbeddedGroveShopConfigurationUsesDefaults(t *testing.T) {
	inspection, configuration, err := loadEmbeddedGroveShopConfiguration()
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.ConfigEmpty || inspection.Config == nil {
		t.Errorf("blank embedded inspection = %#v", inspection)
	}
	if want := groveshop.DefaultConfiguration(); configuration != want {
		t.Errorf("blank runtime configuration = %#v; want %#v", configuration, want)
	}
	if inspection.Config.Digest != artifact.ConfigDigest(inspection.CompiledConfig) {
		t.Errorf("default runtime config digest = %q", inspection.Config.Digest)
	}
}

func TestRunConfigCompileRejectsUnreadableInput(t *testing.T) {
	errRead := errors.New("read failed")
	if err := runConfigCompile(t.Context(), nil, errorReader{err: errRead}, &bytes.Buffer{}); !errors.Is(err, errRead) {
		t.Errorf("compiler read error = %v; want %v", err, errRead)
	}
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}
