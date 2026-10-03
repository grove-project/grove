package systemnats

import "github.com/grove-project/grove/internal/controlplane"

// The control-plane domain is defined in internal/controlplane. These aliases
// keep the System NATS adapter's API stable for runtime and CLI callers; the
// adapter stores, watches and transports these records and never redefines
// their meaning.

// Membership and health.
type (
	MembershipRecord = controlplane.MembershipRecord
	MembershipView   = controlplane.MembershipView
	HealthState      = controlplane.HealthState
	ClusterNode      = controlplane.ClusterNode
	ClusterView      = controlplane.ClusterView
)

const (
	HealthHealthy     = controlplane.HealthHealthy
	HealthUnavailable = controlplane.HealthUnavailable
	MinClusterNodes   = controlplane.MinClusterNodes
)

// Desired state, artifacts and rollouts.
type (
	DesiredComponent    = controlplane.DesiredComponent
	DesiredDeployment   = controlplane.DesiredDeployment
	DesiredView         = controlplane.DesiredView
	RolloutPhase        = controlplane.RolloutPhase
	DeploymentArtifact  = controlplane.DeploymentArtifact
	RolloutNodeProgress = controlplane.RolloutNodeProgress
	RolloutFailure      = controlplane.RolloutFailure
	Rollout             = controlplane.Rollout
	DeploymentView      = controlplane.DeploymentView
	UpgradeRoute        = controlplane.UpgradeRoute
)

const (
	RolloutActive           = controlplane.RolloutActive
	RolloutPending          = controlplane.RolloutPending
	RolloutCandidateHealthy = controlplane.RolloutCandidateHealthy
	RolloutSwitching        = controlplane.RolloutSwitching
	RolloutCandidateFailed  = controlplane.RolloutCandidateFailed
	RolloutRollingBack      = controlplane.RolloutRollingBack
	RolloutRolledBack       = controlplane.RolloutRolledBack
)

// Service and handler placement.
type (
	PlacementRecord      = controlplane.PlacementRecord
	PlacementView        = controlplane.PlacementView
	HandlerRegistration  = controlplane.HandlerRegistration
	NodeHandlers         = controlplane.NodeHandlers
	HandlerNode          = controlplane.HandlerNode
	HandlerPlacement     = controlplane.HandlerPlacement
	LeaseView            = controlplane.LeaseView
	HandlerPlacementView = controlplane.HandlerPlacementView
	LostPlacement        = controlplane.LostPlacement
)

// Domain errors keep their identity, so errors.Is matches either name.
var (
	ErrMembershipRecordInvalid     = controlplane.ErrMembershipRecordInvalid
	ErrClusterForming              = controlplane.ErrClusterForming
	ErrClusterSettling             = controlplane.ErrClusterSettling
	ErrControlPlaneUnavailable     = controlplane.ErrControlPlaneUnavailable
	ErrDesiredDeploymentInvalid    = controlplane.ErrDesiredDeploymentInvalid
	ErrDeploymentArtifactInvalid   = controlplane.ErrDeploymentArtifactInvalid
	ErrDeploymentArtifactChanged   = controlplane.ErrDeploymentArtifactChanged
	ErrRolloutInvalid              = controlplane.ErrRolloutInvalid
	ErrIngressInvalid              = controlplane.ErrIngressInvalid
	ErrIngressChanged              = controlplane.ErrIngressChanged
	ErrRolloutGeneration           = controlplane.ErrRolloutGeneration
	ErrRolloutChanged              = controlplane.ErrRolloutChanged
	ErrUpgradeInvalid              = controlplane.ErrUpgradeInvalid
	ErrPlacementRecordInvalid      = controlplane.ErrPlacementRecordInvalid
	ErrPlacementUnavailable        = controlplane.ErrPlacementUnavailable
	ErrServiceNotPlaced            = controlplane.ErrServiceNotPlaced
	ErrPlacementChanged            = controlplane.ErrPlacementChanged
	ErrHandlerNotPlaced            = controlplane.ErrHandlerNotPlaced
	ErrHandlerPlacementUnavailable = controlplane.ErrHandlerPlacementUnavailable
	ErrHandlerPlacementInvalid     = controlplane.ErrHandlerPlacementInvalid
)

// ClusterFormingError describes a cluster that has joined nodes of needed.
func ClusterFormingError(joined, needed int) error {
	return controlplane.ClusterFormingError(joined, needed)
}

// ClusterSettlingError describes a control plane with more voters than nodes.
func ClusterSettlingError(voters, nodes int) error {
	return controlplane.ClusterSettlingError(voters, nodes)
}
