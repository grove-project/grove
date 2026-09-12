package artifact_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grove-project/grove/internal/artifact"
)

const validManifest = `{"format_version":1,"application_id":"grove-shop","code_version":"v0.1.0","components":[{"service_id":1,"name":"Orders","runtime":"process","entrypoint":["worker","--component","orders"]}],"ui_assets":["web/index.html"],"config_region":{"format_version":1,"capacity":4096}}`

func TestInspectBytes(t *testing.T) {
	binary := artifactBytes(validManifest, strings.Repeat("\x00", artifact.ConfigRegionCapacity))
	inspection, err := artifact.InspectBytes(binary)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Manifest.ApplicationID != "grove-shop" || inspection.Manifest.CodeVersion != "v0.1.0" {
		t.Errorf("manifest identity = %#v", inspection.Manifest)
	}
	if len(inspection.Manifest.Components) != 1 || inspection.Manifest.Components[0].Name != "Orders" {
		t.Errorf("manifest components = %#v", inspection.Manifest.Components)
	}
	if !inspection.ConfigEmpty {
		t.Error("blank config region reported non-empty")
	}
	if inspection.Config != nil || inspection.CodeDigest != inspection.ArtifactDigest {
		t.Errorf("blank config inspection = %#v", inspection)
	}

	compilation := artifact.Compilation{
		ProtocolVersion: artifact.CompilerProtocolVersion,
		Revision:        "acme-r42",
		Encoding:        "gob",
		Payload:         []byte("compiled runtime configuration"),
		CanonicalYAML:   []byte("revision: acme-r42\n"),
	}
	configured, configuredInspection, err := artifact.EmbedBytes(binary, compilation)
	if err != nil {
		t.Fatal(err)
	}
	if configuredInspection.ConfigEmpty {
		t.Error("populated config region reported empty")
	}
	if configuredInspection.MetadataDigest != inspection.MetadataDigest {
		t.Error("config bytes changed metadata digest")
	}
	if configuredInspection.CodeDigest != inspection.CodeDigest {
		t.Error("config bytes changed normalized code digest")
	}
	if configuredInspection.ArtifactDigest == inspection.ArtifactDigest {
		t.Error("config bytes did not change artifact digest")
	}
	if configuredInspection.Config == nil || configuredInspection.Config.Revision != "acme-r42" || configuredInspection.Config.Digest != artifact.ConfigDigest(compilation.Payload) {
		t.Errorf("configured metadata = %#v", configuredInspection.Config)
	}
	if !bytes.Equal(configuredInspection.CompiledConfig, compilation.Payload) || !bytes.Equal(configuredInspection.CanonicalYAML, compilation.CanonicalYAML) {
		t.Errorf("configured payload = %q, YAML = %q", configuredInspection.CompiledConfig, configuredInspection.CanonicalYAML)
	}
	corrupt := append([]byte(nil), configured...)
	region := bytes.Index(corrupt, []byte(artifact.ConfigRegionPrefix)) + len(artifact.ConfigRegionPrefix)
	corrupt[region+12] ^= 0xff
	if _, err := artifact.InspectBytes(corrupt); !errors.Is(err, artifact.ErrConfigCorrupt) {
		t.Errorf("corrupt configuration error = %v; want %v", err, artifact.ErrConfigCorrupt)
	}
}

func TestInspectBytesRejectsInvalidArtifacts(t *testing.T) {
	tests := []struct {
		name   string
		binary []byte
		err    error
	}{
		{name: "missing manifest", binary: []byte("ordinary binary"), err: artifact.ErrManifestNotFound},
		{name: "invalid manifest", binary: artifactBytes(`{"format_version":2}`, strings.Repeat("\x00", artifact.ConfigRegionCapacity)), err: artifact.ErrManifestInvalid},
		{name: "missing config", binary: []byte(artifact.ManifestPrefix + validManifest + artifact.ManifestSuffix), err: artifact.ErrConfigRegionNotFound},
		{name: "short config", binary: artifactBytes(validManifest, "short"), err: artifact.ErrConfigRegionNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := artifact.InspectBytes(test.binary)
			if !errors.Is(err, test.err) {
				t.Errorf("InspectBytes() error = %v; want %v", err, test.err)
			}
		})
	}
}

func TestInspectFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grove-shop")
	if err := os.WriteFile(path, artifactBytes(validManifest, strings.Repeat("\x00", artifact.ConfigRegionCapacity)), 0o700); err != nil {
		t.Fatal(err)
	}
	inspection, err := artifact.InspectFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Manifest.ApplicationID != "grove-shop" || inspection.ArtifactDigest == "" {
		t.Errorf("InspectFile() = %#v", inspection)
	}
	if _, err := artifact.InspectFile(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("InspectFile() missing path returned nil error")
	}
}

func TestEmbedBytesRejectsInvalidInputs(t *testing.T) {
	base := artifactBytes(validManifest, strings.Repeat("\x00", artifact.ConfigRegionCapacity))
	invalid := artifact.Compilation{ProtocolVersion: 99, Revision: "r1", Encoding: "gob", Payload: []byte("payload"), CanonicalYAML: []byte("revision: r1\n")}
	if _, _, err := artifact.EmbedBytes(base, invalid); !errors.Is(err, artifact.ErrCompilationInvalid) {
		t.Errorf("invalid compilation error = %v; want %v", err, artifact.ErrCompilationInvalid)
	}
	large := make([]byte, artifact.ConfigRegionCapacity*2)
	if _, err := rand.Read(large); err != nil {
		t.Fatal(err)
	}
	overflow := artifact.Compilation{ProtocolVersion: artifact.CompilerProtocolVersion, Revision: "large", Encoding: "gob", Payload: large, CanonicalYAML: large}
	if _, _, err := artifact.EmbedBytes(base, overflow); !errors.Is(err, artifact.ErrConfigOverflow) {
		t.Errorf("overflow error = %v; want %v", err, artifact.ErrConfigOverflow)
	}
	configured, _, err := artifact.EmbedBytes(base, artifact.Compilation{
		ProtocolVersion: artifact.CompilerProtocolVersion,
		Revision:        "r1",
		Encoding:        "gob",
		Payload:         []byte("payload"),
		CanonicalYAML:   []byte("revision: r1\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := artifact.EmbedBytes(configured, artifact.Compilation{
		ProtocolVersion: artifact.CompilerProtocolVersion,
		Revision:        "r2",
		Encoding:        "gob",
		Payload:         []byte("other"),
		CanonicalYAML:   []byte("revision: r2\n"),
	}); !errors.Is(err, artifact.ErrConfigAlreadyEmbedded) {
		t.Errorf("configured input error = %v; want %v", err, artifact.ErrConfigAlreadyEmbedded)
	}
}

func artifactBytes(manifest, config string) []byte {
	return []byte("binary-prefix" +
		artifact.ManifestPrefix + manifest + artifact.ManifestSuffix +
		artifact.ConfigRegionPrefix + config + artifact.ConfigRegionSuffix +
		"binary-suffix")
}
