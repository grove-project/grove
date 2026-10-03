package controlplane

import (
	"errors"

	"github.com/grove-project/grove"
)

var (
	// ErrPlacementRecordInvalid is returned for an incomplete placement record
	// or a duplicate service assignment.
	ErrPlacementRecordInvalid = errors.New("grove placement record is invalid")
	// ErrPlacementUnavailable is returned before placement has initialized or
	// while the control plane cannot confirm it.
	ErrPlacementUnavailable = errors.New("grove placement is unavailable")
	// ErrServiceNotPlaced is returned when no placement exists for a service.
	ErrServiceNotPlaced = errors.New("grove service is not placed")
	// ErrPlacementChanged is returned when a compare-and-set placement handoff
	// observes a record other than the one it expected to replace.
	ErrPlacementChanged = errors.New("grove placement changed")
)

// PlacementRecord is the authoritative owner and endpoint of one service.
type PlacementRecord struct {
	// ServiceID is the stable application-owned service identifier.
	ServiceID grove.ServiceID `json:"service_id"`
	// NodeID is the logical Grovlet that owns the service.
	NodeID string `json:"node_id"`
	// InvocationSubject is the endpoint that receives the service's calls.
	InvocationSubject string `json:"invocation_subject"`
	// ArtifactDigest identifies the exact artifact serving the placement.
	ArtifactDigest string `json:"artifact_digest"`
}

// PlacementView is one Grovlet's observation of authoritative placement.
type PlacementView struct {
	// Ready reports whether the initial authoritative snapshot completed.
	Ready bool `json:"ready"`
	// Placements contains records sorted by service ID.
	Placements []PlacementRecord `json:"placements"`
	// Error describes the latest transient initialization or watch failure.
	Error string `json:"error,omitempty"`
}

// ValidPlacementRecord reports whether record names a service, node, endpoint
// and artifact.
func ValidPlacementRecord(record PlacementRecord) bool {
	return record.ServiceID != 0 && record.NodeID != "" && record.InvocationSubject != "" && ValidSHA256Digest(record.ArtifactDigest)
}

// ValidatePlacementRecords checks that every record is valid and that each
// service is placed at most once.
func ValidatePlacementRecords(records []PlacementRecord) error {
	seen := make(map[grove.ServiceID]struct{}, len(records))
	for _, record := range records {
		if !ValidPlacementRecord(record) {
			return ErrPlacementRecordInvalid
		}
		if _, exists := seen[record.ServiceID]; exists {
			return ErrPlacementRecordInvalid
		}
		seen[record.ServiceID] = struct{}{}
	}
	return nil
}

// FindPlacement resolves serviceID in view. A view that is not ready resolves
// nothing, so callers never route on unconfirmed placement.
func FindPlacement(view PlacementView, serviceID grove.ServiceID) (PlacementRecord, error) {
	if !view.Ready {
		return PlacementRecord{}, ErrPlacementUnavailable
	}
	for _, record := range view.Placements {
		if record.ServiceID == serviceID {
			return record, nil
		}
	}
	return PlacementRecord{}, ErrServiceNotPlaced
}

// CheckPlacementReplacement decides a compare-and-set handoff of one service
// from current to replacement, given the stored record. It reports write=false
// with no error when replacement is already stored (an idempotent retry), and
// ErrPlacementChanged when the stored record is neither.
func CheckPlacementReplacement(stored, current, replacement PlacementRecord) (write bool, err error) {
	if !ValidPlacementRecord(current) || !ValidPlacementRecord(replacement) || current.ServiceID != replacement.ServiceID {
		return false, ErrPlacementRecordInvalid
	}
	if stored == replacement {
		return false, nil
	}
	if stored != current {
		return false, ErrPlacementChanged
	}
	return true, nil
}
