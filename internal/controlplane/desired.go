package controlplane

import (
	"errors"
	"sort"

	"github.com/grove-project/grove"
)

// ErrDesiredDeploymentInvalid is returned for incomplete or ambiguous desired
// deployment records.
var ErrDesiredDeploymentInvalid = errors.New("grove desired deployment is invalid")

// DesiredComponent assigns one application service to a logical Grovlet.
type DesiredComponent struct {
	// ServiceID is the stable application-owned service identifier.
	ServiceID grove.ServiceID `json:"service_id"`
	// NodeID is the logical Grovlet intended to run the service.
	NodeID string `json:"node_id"`
}

// DesiredDeployment is the minimum current intent for one Grove application.
type DesiredDeployment struct {
	// ApplicationID is the stable application-owned deployment identifier.
	ApplicationID string `json:"application_id"`
	// Version labels the single current version represented by this MVP record.
	Version string `json:"version"`
	// ArtifactDigest identifies the exact immutable artifact intended to run.
	ArtifactDigest string `json:"artifact_digest"`
	// Components contains the intended service assignments.
	Components []DesiredComponent `json:"components"`
}

// DesiredView is one Grovlet's watcher-derived desired deployment state.
type DesiredView struct {
	// Ready reports whether the initial authoritative snapshot completed.
	Ready bool `json:"ready"`
	// Deployments contains records sorted by application ID.
	Deployments []DesiredDeployment `json:"deployments"`
	// Error describes the latest transient initialization or watch failure.
	Error string `json:"error,omitempty"`
}

// ValidateDesiredDeployment checks deployment and returns its canonical form
// with components sorted by service ID. Every component needs a service and a
// node, and a service may be assigned only once.
func ValidateDesiredDeployment(deployment DesiredDeployment) (DesiredDeployment, error) {
	if !ValidApplicationID(deployment.ApplicationID) || deployment.Version == "" || !ValidSHA256Digest(deployment.ArtifactDigest) || len(deployment.Components) == 0 {
		return DesiredDeployment{}, ErrDesiredDeploymentInvalid
	}
	components := append([]DesiredComponent(nil), deployment.Components...)
	seen := make(map[grove.ServiceID]struct{}, len(components))
	for _, component := range components {
		if component.ServiceID == 0 || component.NodeID == "" {
			return DesiredDeployment{}, ErrDesiredDeploymentInvalid
		}
		if _, exists := seen[component.ServiceID]; exists {
			return DesiredDeployment{}, ErrDesiredDeploymentInvalid
		}
		seen[component.ServiceID] = struct{}{}
	}
	sort.Slice(components, func(i, j int) bool { return components[i].ServiceID < components[j].ServiceID })
	deployment.Components = components
	return deployment, nil
}
