package bootstrap_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/grove-project/grove/internal/bootstrap"
)

func TestHelloRoundTripAndNegotiation(t *testing.T) {
	current := hello("a", "b", "c", "r42")
	current.BootstrapVersions = []int{1, 2, 1}
	current.Capabilities = append(current.Capabilities, "current-only")
	candidate := hello("d", "e", "f", "r43")
	candidate.BootstrapVersions = []int{1, 2}
	candidate.Capabilities = append(candidate.Capabilities, "candidate-only")
	encoded, err := bootstrap.MarshalHello(candidate)
	if err != nil {
		t.Fatal(err)
	}
	encoded = bytes.Replace(encoded, []byte(`"payload":{`), []byte(`"optional":"ignored","payload":{"optional":true,`), 1)
	decoded, err := bootstrap.UnmarshalHello(encoded)
	if err != nil {
		t.Fatal(err)
	}
	negotiated, err := bootstrap.Negotiate(current, decoded)
	if err != nil {
		t.Fatal(err)
	}
	if negotiated.BootstrapVersion != 2 || negotiated.Current.ArtifactDigest != testDigest("c") || negotiated.Candidate.ArtifactDigest != testDigest("f") {
		t.Errorf("negotiation = %#v", negotiated)
	}
	wantCapabilities := []string{bootstrap.CapabilityArtifactIdentity, bootstrap.CapabilityReadiness, bootstrap.CapabilitySideBySide}
	if !slicesEqual(negotiated.Capabilities, wantCapabilities) {
		t.Errorf("negotiated capabilities = %v; want %v", negotiated.Capabilities, wantCapabilities)
	}
}

func TestNegotiationRejectsIncompatiblePeers(t *testing.T) {
	tests := []struct {
		name      string
		candidate bootstrap.Hello
	}{
		{name: "cluster", candidate: func() bootstrap.Hello {
			value := hello("d", "e", "f", "r43")
			value.Artifact.ClusterID = "other"
			return value
		}()},
		{name: "zone", candidate: func() bootstrap.Hello {
			value := hello("d", "e", "f", "r43")
			value.Artifact.NodeZone = "edge"
			return value
		}()},
		{name: "protocol", candidate: func() bootstrap.Hello {
			value := hello("d", "e", "f", "r43")
			value.BootstrapVersions = []int{2}
			return value
		}()},
		{name: "capability", candidate: func() bootstrap.Hello {
			value := hello("d", "e", "f", "r43")
			value.Capabilities = []string{bootstrap.CapabilityArtifactIdentity}
			return value
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := bootstrap.Negotiate(hello("a", "b", "c", "r42"), test.candidate); !errors.Is(err, bootstrap.ErrIncompatible) {
				t.Errorf("Negotiate() error = %v; want %v", err, bootstrap.ErrIncompatible)
			}
		})
	}
}

func TestHelloRejectsMalformedData(t *testing.T) {
	invalid := hello("a", "b", "c", "r42")
	invalid.Artifact.CodeDigest = "not-a-digest"
	if _, err := bootstrap.MarshalHello(invalid); !errors.Is(err, bootstrap.ErrHelloInvalid) {
		t.Errorf("MarshalHello() error = %v; want %v", err, bootstrap.ErrHelloInvalid)
	}
	if _, err := bootstrap.UnmarshalHello([]byte(`{"protocol_version":2,"message_type":"hello","message_version":1,"payload":{}}`)); !errors.Is(err, bootstrap.ErrEnvelopeInvalid) {
		t.Errorf("UnmarshalHello() error = %v; want %v", err, bootstrap.ErrEnvelopeInvalid)
	}
}

func hello(code, config, artifact, revision string) bootstrap.Hello {
	return bootstrap.Hello{
		BootstrapVersions: []int{bootstrap.BootstrapVersion},
		Capabilities: []string{
			bootstrap.CapabilityArtifactIdentity,
			bootstrap.CapabilitySideBySide,
			bootstrap.CapabilityReadiness,
		},
		Artifact: bootstrap.ArtifactIdentity{
			ApplicationID:  "grove-shop",
			RuntimeVersion: "v1",
			CodeDigest:     testDigest(code),
			ConfigRevision: revision,
			ConfigDigest:   testDigest(config),
			ArtifactDigest: testDigest(artifact),
			ClusterID:      "production",
			NodeZone:       "cloud",
		},
	}
}

func testDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
