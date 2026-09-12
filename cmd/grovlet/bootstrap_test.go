package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/internal/bootstrap"
)

func TestRunBootstrapHello(t *testing.T) {
	var output bytes.Buffer
	if err := runBootstrapHello(nil, &output); err != nil {
		t.Fatal(err)
	}
	hello, err := bootstrap.UnmarshalHello(output.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if hello.Artifact.ApplicationID != "grove-shop" || hello.Artifact.RuntimeVersion != "v0.1.0-dev" || hello.Artifact.ConfigRevision != groveshop.DefaultConfigRevision || hello.Artifact.ClusterID != "local" || hello.Artifact.NodeZone != "local" {
		t.Errorf("bootstrap hello identity = %#v", hello.Artifact)
	}
	if hello.Artifact.ArtifactDigest == "" || hello.Artifact.CodeDigest == "" || hello.Artifact.ConfigDigest == "" {
		t.Fatal("bootstrap hello omitted artifact identities")
	}
	if err := runBootstrapHello([]string{"extra"}, &output); err == nil {
		t.Fatal("bootstrap hello accepted arguments")
	}
	errWrite := errors.New("write failed")
	if err := runBootstrapHello(nil, errorWriter{err: errWrite}); !errors.Is(err, errWrite) {
		t.Errorf("bootstrap hello write error = %v; want %v", err, errWrite)
	}
}
