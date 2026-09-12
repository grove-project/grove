package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
)

const (
	// ReadinessMessageVersion is the schema version of a readiness payload.
	ReadinessMessageVersion = 1
	// MessageReadiness identifies a candidate runtime readiness envelope.
	MessageReadiness = "readiness"

	// ReadinessHealthy means the exact artifact runtime is ready to receive
	// application traffic.
	ReadinessHealthy ReadinessState = "healthy"
)

var (
	// ErrReadinessInvalid is returned when required runtime readiness identity
	// or state is missing.
	ErrReadinessInvalid = errors.New("grove bootstrap readiness is invalid")
)

// ReadinessState is the stable bootstrap health state of a candidate runtime.
type ReadinessState string

// Readiness identifies one exact candidate runtime and its bootstrap health.
type Readiness struct {
	NodeID         string         `json:"node_id"`
	ArtifactDigest string         `json:"artifact_digest"`
	State          ReadinessState `json:"state"`
}

// MarshalReadiness validates readiness and returns one stable envelope.
func MarshalReadiness(readiness Readiness) ([]byte, error) {
	if err := validateReadiness(readiness); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(readiness)
	if err != nil {
		return nil, fmt.Errorf("encode bootstrap readiness: %w", err)
	}
	encoded, err := json.Marshal(Envelope{
		ProtocolVersion: EnvelopeProtocolVersion,
		MessageType:     MessageReadiness,
		MessageVersion:  ReadinessMessageVersion,
		Payload:         payload,
	})
	if err != nil {
		return nil, fmt.Errorf("encode bootstrap envelope: %w", err)
	}
	return append(encoded, '\n'), nil
}

// UnmarshalReadiness decodes readiness while ignoring unknown optional JSON
// fields. Required incompatible versions and malformed known fields fail.
func UnmarshalReadiness(encoded []byte) (Readiness, error) {
	var envelope Envelope
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		return Readiness{}, fmt.Errorf("%w: decode: %v", ErrEnvelopeInvalid, err)
	}
	if envelope.ProtocolVersion != EnvelopeProtocolVersion || envelope.MessageType != MessageReadiness || envelope.MessageVersion != ReadinessMessageVersion || len(envelope.Payload) == 0 {
		return Readiness{}, ErrEnvelopeInvalid
	}
	var readiness Readiness
	if err := json.Unmarshal(envelope.Payload, &readiness); err != nil {
		return Readiness{}, fmt.Errorf("%w: decode payload: %v", ErrReadinessInvalid, err)
	}
	if err := validateReadiness(readiness); err != nil {
		return Readiness{}, err
	}
	return readiness, nil
}

func validateReadiness(readiness Readiness) error {
	if readiness.NodeID == "" || !validDigest(readiness.ArtifactDigest) || readiness.State != ReadinessHealthy {
		return ErrReadinessInvalid
	}
	return nil
}
