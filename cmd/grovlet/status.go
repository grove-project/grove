package main

import (
	"context"
	"fmt"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/demo/groveshop"
	"github.com/grove-project/grove/internal/systemnats"
)

const groveShopStatusRequestTimeout = 500 * time.Millisecond

func newGroveShopStatusReader(
	transport *systemnats.Transport,
	nodeID string,
	localArtifact groveshop.ArtifactStatusView,
) groveshop.StatusReader {
	return func(ctx context.Context) (groveshop.ClusterStatusView, error) {
		clusterCtx, clusterCancel := context.WithTimeout(ctx, groveShopStatusRequestTimeout)
		cluster, err := transport.RequestClusterView(clusterCtx, nodeID)
		clusterCancel()
		if err != nil {
			return groveshop.ClusterStatusView{}, fmt.Errorf("request cluster view: %w", err)
		}
		placementCtx, placementCancel := context.WithTimeout(ctx, groveShopStatusRequestTimeout)
		placement, err := transport.RequestPlacement(placementCtx, nodeID)
		placementCancel()
		if err != nil {
			return groveshop.ClusterStatusView{}, fmt.Errorf("request placement view: %w", err)
		}
		deploymentCtx, deploymentCancel := context.WithTimeout(ctx, groveShopStatusRequestTimeout)
		deployments, err := transport.RequestDeployments(deploymentCtx, nodeID)
		deploymentCancel()
		if err != nil {
			return groveshop.ClusterStatusView{}, fmt.Errorf("request deployment view: %w", err)
		}

		components := make(map[string]systemnats.ComponentView, len(cluster.Nodes))
		componentErrors := make(map[string]string)
		for _, node := range cluster.Nodes {
			componentCtx, componentCancel := context.WithTimeout(ctx, groveShopStatusRequestTimeout)
			view, requestErr := transport.RequestComponents(componentCtx, node.NodeID)
			componentCancel()
			if requestErr != nil {
				componentErrors[node.NodeID] = requestErr.Error()
				continue
			}
			components[node.NodeID] = view
		}
		return buildGroveShopStatus(cluster, placement, deployments, components, componentErrors, localArtifact), nil
	}
}

func buildGroveShopStatus(
	cluster systemnats.ClusterView,
	placement systemnats.PlacementView,
	deployments systemnats.DeploymentView,
	components map[string]systemnats.ComponentView,
	componentErrors map[string]string,
	localArtifact groveshop.ArtifactStatusView,
) groveshop.ClusterStatusView {
	status := groveshop.ClusterStatusView{
		Health:         "healthy",
		Ready:          cluster.Ready && placement.Ready && deployments.Ready,
		Nodes:          make([]groveshop.NodeStatusView, 0, len(cluster.Nodes)),
		Placements:     make([]groveshop.PlacementStatusView, 0, len(placement.Placements)),
		ActiveArtifact: &localArtifact,
	}
	for _, node := range cluster.Nodes {
		nodeStatus := groveshop.NodeStatusView{
			NodeID:     node.NodeID,
			Health:     string(node.Health),
			Components: []groveshop.ComponentStatusView{},
			Error:      componentErrors[node.NodeID],
		}
		for _, component := range components[node.NodeID].Components {
			nodeStatus.Components = append(nodeStatus.Components, groveshop.ComponentStatusView{
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
		placementStatus := groveshop.PlacementStatusView{
			ServiceID:         record.ServiceID,
			Name:              groveShopServiceName(record.ServiceID),
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

	artifacts := make(map[string]groveshop.ArtifactStatusView, len(deployments.Artifacts))
	for _, deploymentArtifact := range deployments.Artifacts {
		artifacts[deploymentArtifact.ArtifactDigest] = groveshop.ArtifactStatusView{
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
		view := groveshop.RolloutStatusView{Generation: rollout.Generation, Phase: string(rollout.Phase)}
		if rollout.Failure != nil {
			view.Failure = &groveshop.RolloutFailureView{
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

func groveShopServiceName(serviceID grove.ServiceID) string {
	switch serviceID {
	case groveshop.ServiceOrders:
		return "Orders"
	case groveshop.ServiceInventory:
		return "Inventory"
	case groveshop.ServicePayment:
		return "Payment"
	case groveshop.ServiceShipping:
		return "Shipping"
	case groveshop.ServiceWeb:
		return "Web"
	default:
		return fmt.Sprintf("Service %d", serviceID)
	}
}
