package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
)

const statusRequestTimeout = 500 * time.Millisecond

func newStatusReader(
	transport *systemnats.Transport,
	nodeID string,
	localArtifact ArtifactStatus,
) StatusReader {
	return func(ctx context.Context) (ClusterStatus, error) {
		clusterCtx, clusterCancel := context.WithTimeout(ctx, statusRequestTimeout)
		cluster, err := transport.RequestClusterView(clusterCtx, nodeID)
		clusterCancel()
		if err != nil {
			return ClusterStatus{}, fmt.Errorf("request cluster view: %w", err)
		}
		placementCtx, placementCancel := context.WithTimeout(ctx, statusRequestTimeout)
		placement, err := transport.RequestPlacement(placementCtx, nodeID)
		placementCancel()
		if err != nil {
			return ClusterStatus{}, fmt.Errorf("request placement view: %w", err)
		}
		deploymentCtx, deploymentCancel := context.WithTimeout(ctx, statusRequestTimeout)
		deployments, err := transport.RequestDeployments(deploymentCtx, nodeID)
		deploymentCancel()
		if err != nil {
			return ClusterStatus{}, fmt.Errorf("request deployment view: %w", err)
		}

		components := make(map[string]systemnats.ComponentView, len(cluster.Nodes))
		componentErrors := make(map[string]string)
		for _, node := range cluster.Nodes {
			componentCtx, componentCancel := context.WithTimeout(ctx, statusRequestTimeout)
			view, requestErr := transport.RequestComponents(componentCtx, node.NodeID)
			componentCancel()
			if requestErr != nil {
				componentErrors[node.NodeID] = requestErr.Error()
				continue
			}
			components[node.NodeID] = view
		}
		return buildStatus(cluster, placement, deployments, components, componentErrors, localArtifact), nil
	}
}

func buildStatus(
	cluster systemnats.ClusterView,
	placement systemnats.PlacementView,
	deployments systemnats.DeploymentView,
	components map[string]systemnats.ComponentView,
	componentErrors map[string]string,
	localArtifact ArtifactStatus,
) ClusterStatus {
	status := ClusterStatus{
		Health:         "healthy",
		Ready:          cluster.Ready && placement.Ready && deployments.Ready,
		Nodes:          make([]NodeStatus, 0, len(cluster.Nodes)),
		Placements:     make([]PlacementStatus, 0, len(placement.Placements)),
		ActiveArtifact: &localArtifact,
	}
	for _, node := range cluster.Nodes {
		nodeStatus := NodeStatus{
			NodeID:     node.NodeID,
			Health:     string(node.Health),
			LastSeen:   node.LastSeen,
			Components: []ComponentStatus{},
			Error:      componentErrors[node.NodeID],
		}
		for _, component := range components[node.NodeID].Components {
			nodeStatus.Components = append(nodeStatus.Components, ComponentStatus{
				ServiceID: component.ServiceID,
				Name:      component.Name,
				WorkerID:  component.WorkerID,
				State:     string(component.State),
				Error:     component.Error,
			})
		}
		status.Nodes = append(status.Nodes, nodeStatus)
		if node.Health != systemnats.HealthHealthy {
			status.Health = "degraded"
		}
	}
	for _, record := range placement.Placements {
		placementStatus := PlacementStatus{
			ServiceID:         record.ServiceID,
			Name:              applicationServiceName(record.ServiceID),
			NodeID:            record.NodeID,
			InvocationSubject: record.InvocationSubject,
			ArtifactDigest:    record.ArtifactDigest,
			Health:            "unavailable",
		}
		for _, component := range components[record.NodeID].Components {
			if component.ServiceID == record.ServiceID && component.InvocationSubject == record.InvocationSubject {
				placementStatus.Health = string(component.State)
				break
			}
		}
		if placementStatus.Health != string(systemnats.ComponentHealthy) && placementStatus.Health != string(systemnats.ComponentDebugging) {
			status.Health = "degraded"
		}
		status.Placements = append(status.Placements, placementStatus)
	}
	if !status.Ready || len(status.Nodes) == 0 || len(status.Placements) == 0 {
		status.Health = "degraded"
	}

	artifacts := make(map[string]ArtifactStatus, len(deployments.Artifacts))
	for _, deploymentArtifact := range deployments.Artifacts {
		artifacts[deploymentArtifact.ArtifactDigest] = ArtifactStatus{
			ApplicationID:  deploymentArtifact.ApplicationID,
			CodeVersion:    deploymentArtifact.CodeVersion,
			ArtifactDigest: deploymentArtifact.ArtifactDigest,
			ConfigRevision: deploymentArtifact.ConfigRevision,
			ConfigDigest:   deploymentArtifact.ConfigDigest,
		}
	}
	for _, rollout := range deployments.Rollouts {
		if rollout.ApplicationID != localArtifact.ApplicationID {
			continue
		}
		view := RolloutStatus{Generation: rollout.Generation, Phase: string(rollout.Phase)}
		if rollout.Failure != nil {
			view.Failure = &RolloutFailure{
				Code:      rollout.Failure.Code,
				Component: rollout.Failure.Component,
				Field:     rollout.Failure.Field,
				Message:   rollout.Failure.Message,
			}
		}
		status.Rollout = &view
		if active, ok := artifacts[rollout.CurrentArtifactDigest]; ok {
			status.ActiveArtifact = &active
		}
		if candidate, ok := artifacts[rollout.CandidateArtifactDigest]; ok {
			status.CandidateArtifact = &candidate
		}
		break
	}
	return status
}

func applicationServiceName(serviceID grove.ServiceID) string {
	if component, ok := activeApplication.componentByID(serviceID); ok {
		return component.Name
	}
	return fmt.Sprintf("Service %d", serviceID)
}
