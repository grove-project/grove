package bootstrap_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/grove-project/grove/internal/bootstrap"
)

func TestReadinessRoundTripIgnoresOptionalFields(t *testing.T) {
	want := bootstrap.Readiness{
		NodeID:         "node-candidate",
		ArtifactDigest: testBootstrapDigest("a"),
		State:          bootstrap.ReadinessHealthy,
	}
	encoded, err := bootstrap.MarshalReadiness(want)
	if err != nil {
		t.Fatal(err)
	}
	encoded = bytes.Replace(encoded, []byte(`"payload":{`), []byte(`"optional":"ignored","payload":{"optional":true,`), 1)
	got, err := bootstrap.UnmarshalReadiness(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("readiness = %#v; want %#v", got, want)
	}
}

func TestReadinessRejectsInvalidWireValues(t *testing.T) {
	valid := bootstrap.Readiness{NodeID: "node-candidate", ArtifactDigest: testBootstrapDigest("a"), State: bootstrap.ReadinessHealthy}
	encoded, err := bootstrap.MarshalReadiness(valid)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		encoded []byte
		err     error
	}{
		{name: "malformed", encoded: []byte("{"), err: bootstrap.ErrEnvelopeInvalid},
		{name: "wrong message", encoded: bytes.Replace(encoded, []byte(`"readiness"`), []byte(`"hello"`), 1), err: bootstrap.ErrEnvelopeInvalid},
		{name: "missing node", encoded: bytes.Replace(encoded, []byte(`"node-candidate"`), []byte(`""`), 1), err: bootstrap.ErrReadinessInvalid},
		{name: "wrong state", encoded: bytes.Replace(encoded, []byte(`"healthy"`), []byte(`"starting"`), 1), err: bootstrap.ErrReadinessInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := bootstrap.UnmarshalReadiness(test.encoded); !errors.Is(err, test.err) {
				t.Errorf("UnmarshalReadiness() error = %v; want %v", err, test.err)
			}
		})
	}
	invalid := valid
	invalid.ArtifactDigest = "sha256:" + strings.Repeat("z", 64)
	if _, err := bootstrap.MarshalReadiness(invalid); !errors.Is(err, bootstrap.ErrReadinessInvalid) {
		t.Errorf("MarshalReadiness() error = %v; want %v", err, bootstrap.ErrReadinessInvalid)
	}
}

func testBootstrapDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}
