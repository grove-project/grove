package main

import (
	"testing"

	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/bootstrap"
)

func TestReadTargetBootstrapHello(t *testing.T) {
	hello, err := readTargetBootstrapHello(t.Context(), grovletPath)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := artifact.InspectFile(grovletPath)
	if err != nil {
		t.Fatal(err)
	}
	if hello.Artifact.ArtifactDigest != inspection.ArtifactDigest || hello.Artifact.CodeDigest != inspection.CodeDigest || hello.Artifact.ApplicationID != inspection.Manifest.ApplicationID {
		t.Errorf("target bootstrap hello = %#v; inspection = %#v", hello.Artifact, inspection)
	}
	negotiation, err := negotiateBootstrapTargets(t.Context(), grovletPath, grovletPath)
	if err != nil {
		t.Fatal(err)
	}
	if negotiation.BootstrapVersion != bootstrap.BootstrapVersion || negotiation.Current.ArtifactDigest != inspection.ArtifactDigest || negotiation.Candidate.ArtifactDigest != inspection.ArtifactDigest {
		t.Errorf("target bootstrap negotiation = %#v", negotiation)
	}
}
