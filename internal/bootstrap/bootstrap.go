// Package bootstrap defines Grove's small, stable process-replacement wire
// contract independently of the application SDK and runtime control structs.
package bootstrap

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

const (
	// EnvelopeProtocolVersion is the stable outer bootstrap wire version.
	EnvelopeProtocolVersion = 1
	// BootstrapVersion is the process negotiation version implemented here.
	BootstrapVersion = 1
	// HelloMessageVersion is the schema version of a hello payload.
	HelloMessageVersion = 1
	// MessageHello identifies an artifact bootstrap hello envelope.
	MessageHello = "hello"

	// CapabilityArtifactIdentity means a peer reports separately verifiable
	// code, configuration, and complete artifact identities.
	CapabilityArtifactIdentity = "artifact-identity-v1"
	// CapabilitySideBySide means a candidate may coexist with the current
	// process without taking ownership.
	CapabilitySideBySide = "side-by-side-v1"
	// CapabilityReadiness means a peer can report process readiness before
	// ownership handoff.
	CapabilityReadiness = "readiness-v1"
)

var (
	// ErrEnvelopeInvalid is returned when a bootstrap wire envelope is malformed
	// or unsupported.
	ErrEnvelopeInvalid = errors.New("grove bootstrap envelope is invalid")
	// ErrHelloInvalid is returned when required peer identity or capability data
	// is missing.
	ErrHelloInvalid = errors.New("grove bootstrap hello is invalid")
	// ErrIncompatible is returned when current and candidate peers cannot safely
	// participate in side-by-side bootstrap.
	ErrIncompatible = errors.New("grove bootstrap peers are incompatible")
)

// Envelope is the deliberately small, explicitly versioned bootstrap wire
// container. Payload schemas evolve independently through MessageVersion.
type Envelope struct {
	ProtocolVersion int             `json:"protocol_version"`
	MessageType     string          `json:"message_type"`
	MessageVersion  int             `json:"message_version"`
	Payload         json.RawMessage `json:"payload"`
}

// ArtifactIdentity contains only the immutable identity needed to compare a
// current process and a candidate process.
type ArtifactIdentity struct {
	ApplicationID  string `json:"application_id"`
	RuntimeVersion string `json:"runtime_version"`
	CodeDigest     string `json:"code_digest"`
	ConfigRevision string `json:"config_revision"`
	ConfigDigest   string `json:"config_digest"`
	ArtifactDigest string `json:"artifact_digest"`
	ClusterID      string `json:"cluster_id"`
	NodeClass      string `json:"node_class,omitempty"`
	NodeZone       string `json:"node_zone,omitempty"`
}

// Hello advertises a peer's supported bootstrap versions, capabilities, and
// exact artifact identity.
type Hello struct {
	BootstrapVersions []int            `json:"bootstrap_versions"`
	Capabilities      []string         `json:"capabilities"`
	Artifact          ArtifactIdentity `json:"artifact"`
}

// Negotiation is the compatible protocol and capability intersection selected
// for a current/candidate pair.
type Negotiation struct {
	BootstrapVersion int
	Capabilities     []string
	Current          ArtifactIdentity
	Candidate        ArtifactIdentity
}

// MarshalHello validates hello and returns one stable hello envelope.
func MarshalHello(hello Hello) ([]byte, error) {
	hello, err := normalizeHello(hello)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(hello)
	if err != nil {
		return nil, fmt.Errorf("encode bootstrap hello: %w", err)
	}
	encoded, err := json.Marshal(Envelope{
		ProtocolVersion: EnvelopeProtocolVersion,
		MessageType:     MessageHello,
		MessageVersion:  HelloMessageVersion,
		Payload:         payload,
	})
	if err != nil {
		return nil, fmt.Errorf("encode bootstrap envelope: %w", err)
	}
	return append(encoded, '\n'), nil
}

// UnmarshalHello decodes a hello while ignoring unknown optional JSON fields.
// Required incompatible versions and malformed known fields still fail.
func UnmarshalHello(encoded []byte) (Hello, error) {
	var envelope Envelope
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		return Hello{}, fmt.Errorf("%w: decode: %v", ErrEnvelopeInvalid, err)
	}
	if envelope.ProtocolVersion != EnvelopeProtocolVersion || envelope.MessageType != MessageHello || envelope.MessageVersion != HelloMessageVersion || len(envelope.Payload) == 0 {
		return Hello{}, ErrEnvelopeInvalid
	}
	var hello Hello
	if err := json.Unmarshal(envelope.Payload, &hello); err != nil {
		return Hello{}, fmt.Errorf("%w: decode payload: %v", ErrHelloInvalid, err)
	}
	return normalizeHello(hello)
}

// Negotiate selects the highest common bootstrap version and the sorted
// capability intersection after checking immutable deployment compatibility.
func Negotiate(current, candidate Hello) (Negotiation, error) {
	current, err := normalizeHello(current)
	if err != nil {
		return Negotiation{}, err
	}
	candidate, err = normalizeHello(candidate)
	if err != nil {
		return Negotiation{}, err
	}
	if current.Artifact.ApplicationID != candidate.Artifact.ApplicationID ||
		current.Artifact.ClusterID != candidate.Artifact.ClusterID ||
		current.Artifact.NodeClass != candidate.Artifact.NodeClass ||
		current.Artifact.NodeZone != candidate.Artifact.NodeZone {
		return Negotiation{}, ErrIncompatible
	}
	version := highestCommonVersion(current.BootstrapVersions, candidate.BootstrapVersions)
	if version == 0 {
		return Negotiation{}, ErrIncompatible
	}
	capabilities := commonCapabilities(current.Capabilities, candidate.Capabilities)
	for _, required := range []string{CapabilityArtifactIdentity, CapabilitySideBySide, CapabilityReadiness} {
		if !slices.Contains(capabilities, required) {
			return Negotiation{}, fmt.Errorf("%w: missing capability %q", ErrIncompatible, required)
		}
	}
	return Negotiation{
		BootstrapVersion: version,
		Capabilities:     capabilities,
		Current:          current.Artifact,
		Candidate:        candidate.Artifact,
	}, nil
}

func normalizeHello(hello Hello) (Hello, error) {
	if len(hello.BootstrapVersions) == 0 || len(hello.Capabilities) == 0 {
		return Hello{}, ErrHelloInvalid
	}
	identity := hello.Artifact
	if identity.ApplicationID == "" || identity.RuntimeVersion == "" || identity.ConfigRevision == "" || identity.ClusterID == "" {
		return Hello{}, ErrHelloInvalid
	}
	for _, digest := range []string{identity.CodeDigest, identity.ConfigDigest, identity.ArtifactDigest} {
		if !validDigest(digest) {
			return Hello{}, ErrHelloInvalid
		}
	}
	versions := append([]int(nil), hello.BootstrapVersions...)
	sort.Ints(versions)
	versions = slices.Compact(versions)
	if versions[0] <= 0 {
		return Hello{}, ErrHelloInvalid
	}
	capabilities := append([]string(nil), hello.Capabilities...)
	for _, capability := range capabilities {
		if capability == "" {
			return Hello{}, ErrHelloInvalid
		}
	}
	sort.Strings(capabilities)
	capabilities = slices.Compact(capabilities)
	hello.BootstrapVersions = versions
	hello.Capabilities = capabilities
	return hello, nil
}

func highestCommonVersion(a, b []int) int {
	common := 0
	for _, version := range a {
		if version > common && slices.Contains(b, version) {
			common = version
		}
	}
	return common
}

func commonCapabilities(a, b []string) []string {
	common := make([]string, 0, min(len(a), len(b)))
	for _, capability := range a {
		if slices.Contains(b, capability) {
			common = append(common, capability)
		}
	}
	sort.Strings(common)
	return slices.Compact(common)
}

func validDigest(digest string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(digest, prefix) || len(digest) != len(prefix)+64 {
		return false
	}
	suffix := strings.TrimPrefix(digest, prefix)
	if suffix != strings.ToLower(suffix) {
		return false
	}
	_, err := hex.DecodeString(suffix)
	return err == nil
}
