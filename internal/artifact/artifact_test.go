package artifact_test

import (
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
	if !strings.HasPrefix(inspection.MetadataDigest, "sha256:") || !strings.HasPrefix(inspection.ArtifactDigest, "sha256:") {
		t.Errorf("inspection digests = %q, %q", inspection.MetadataDigest, inspection.ArtifactDigest)
	}

	configured := artifactBytes(validManifest, "compiled"+strings.Repeat("\x00", artifact.ConfigRegionCapacity-len("compiled")))
	configuredInspection, err := artifact.InspectBytes(configured)
	if err != nil {
		t.Fatal(err)
	}
	if configuredInspection.ConfigEmpty {
		t.Error("populated config region reported empty")
	}
	if configuredInspection.MetadataDigest != inspection.MetadataDigest {
		t.Error("config bytes changed metadata digest")
	}
	if configuredInspection.ArtifactDigest == inspection.ArtifactDigest {
		t.Error("config bytes did not change artifact digest")
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

func artifactBytes(manifest, config string) []byte {
	return []byte("binary-prefix" +
		artifact.ManifestPrefix + manifest + artifact.ManifestSuffix +
		artifact.ConfigRegionPrefix + config + artifact.ConfigRegionSuffix +
		"binary-suffix")
}
