package runtime

import (
	"fmt"
	"os"
	"runtime"

	"github.com/grove-project/grove/internal/artifact"
)

const (
	// ArtifactManifestPrefix and the related constants let an application embed
	// Grove's stable artifact envelope without importing a runtime-internal
	// package. Keep the concatenation in an application-owned package-level
	// variable so the envelope remains present in the compiled executable.
	ArtifactManifestPrefix = artifact.ManifestPrefix
	ArtifactManifestSuffix = artifact.ManifestSuffix
	ArtifactConfigPrefix   = artifact.ConfigRegionPrefix
	ArtifactConfigSuffix   = artifact.ConfigRegionSuffix

	blankConfig16  = "\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"
	blankConfig256 = blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16 +
		blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16 + blankConfig16
	// BlankConfigRegion is the fixed MVP configuration reservation included in
	// an application artifact.
	BlankConfigRegion = blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256 +
		blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256 + blankConfig256
)

func inspectEmbeddedArtifact() (artifact.Inspection, error) {
	runtime.KeepAlive(*activeApplication.EmbeddedArtifact)
	executable, err := os.Executable()
	if err != nil {
		return artifact.Inspection{}, fmt.Errorf("locate embedded Grove application artifact: %w", err)
	}
	return artifact.InspectFile(executable)
}
