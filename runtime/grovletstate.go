package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
)

// resolveIngress makes the cluster's ingress address known to this node. The
// founding node records it in control state; every other node reads it, so
// recovery can move ingress onto any survivor. A node that names no component
// joins a cluster founded this way by hosting every non-ingress component.
func resolveIngress(ctx context.Context, cfg config, transport *systemnats.Transport, deployments *systemnats.Deployments) (config, error) {
	if !cfg.hostAll && !cfg.adoptCluster {
		return cfg, nil
	}
	hasIngress := false
	for _, component := range activeApplication.Components {
		hasIngress = hasIngress || component.HTTPHandler != nil
	}
	if !hasIngress {
		return cfg, nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		view := deployments.Snapshot()
		if view.Ready && cfg.hostAll && view.Ingress != cfg.ingressAddress {
			lastErr = deployments.PutIngress(waitCtx, transport, cfg.ingressAddress)
			view = deployments.Snapshot()
		}
		ingressKnown := view.Ready && view.Ingress != ""
		if view.Ready && !ingressKnown && cfg.adoptCluster {
			// No ingress recorded yet. A runtime-placed founder records its
			// ingress before any placement, so placements without an ingress
			// mean an explicitly placed cluster; no placements means the
			// founder may still be recording it, so keep waiting (grove#40).
			placed, err := systemnats.RecordedPlacements(waitCtx, transport)
			if err != nil {
				lastErr = err
			} else if len(placed) != 0 {
				return cfg, nil // not a runtime-placed cluster: host nothing
			}
		}
		if ingressKnown {
			for _, component := range activeApplication.Components {
				if component.HTTPHandler != nil {
					if configuredValue(cfg.componentListeners, component.Kind) == "" {
						cfg.componentListeners = append(cfg.componentListeners, component.Kind+"="+view.Ingress)
					}
				} else if cfg.adoptCluster {
					cfg.componentKinds = append(cfg.componentKinds, component.Kind)
				}
			}
			return cfg, nil
		}
		select {
		case <-ticker.C:
		case <-waitCtx.Done():
			return cfg, fmt.Errorf("wait for cluster ingress address: %w", errors.Join(lastErr, waitCtx.Err()))
		}
	}
}

func systemNATSStateExists(runtimeDir string) bool {
	_, err := os.Stat(filepath.Join(runtimeDir, "system-nats"))
	return err == nil
}

func applicationPlacements(cfg config) []systemnats.PlacementRecord {
	placements := make([]systemnats.PlacementRecord, 0, len(cfg.componentKinds))
	for _, kind := range cfg.componentKinds {
		component, _ := activeApplication.componentByKind(kind)
		placements = append(placements, systemnats.PlacementRecord{
			ServiceID:         component.ServiceID,
			NodeID:            cfg.nodeID,
			InvocationSubject: componentInvocationSubject(cfg.systemNATSSubject, component.ServiceID),
			ArtifactDigest:    cfg.applicationArtifactDigest,
		})
	}
	return placements
}

func applicationComponentSpecs(cfg config) []componentSpec {
	return componentSpecsForKinds(cfg, cfg.componentKinds)
}

func applicationRecoveryComponentSpecs(cfg config) []componentSpec {
	kinds := make([]string, 0, len(activeApplication.Components))
	for _, component := range activeApplication.Components {
		if component.HTTPHandler != nil && configuredValue(cfg.componentListeners, component.Kind) == "" {
			continue
		}
		kinds = append(kinds, component.Kind)
	}
	return componentSpecsForKinds(cfg, kinds)
}

func componentSpecsForKinds(cfg config, kinds []string) []componentSpec {
	components := make([]componentSpec, 0, len(kinds))
	for _, kind := range kinds {
		component, _ := activeApplication.componentByKind(kind)
		components = append(components, componentSpec{
			serviceID:      component.ServiceID,
			name:           component.Name,
			kind:           component.Kind,
			subject:        componentInvocationSubject(cfg.systemNATSSubject, component.ServiceID),
			artifactDigest: cfg.applicationArtifactDigest,
			codeVersion:    cfg.applicationCodeVersion,
			listenAddress:  configuredValue(cfg.componentListeners, kind),
			options:        configuredOptions(cfg.componentOptions, kind),
			mode:           componentExecutionMode(cfg, kind),
		})
	}
	return components
}

// componentExecutionMode is the initial execution policy: every component
// runs in the node's shared application runtime unless it is explicitly
// isolated.
func componentExecutionMode(cfg config, kind string) systemnats.ExecutionMode {
	if slices.Contains(cfg.isolatedComponents, kind) {
		return systemnats.ExecutionIsolatedProcess
	}
	return systemnats.ExecutionInProcess
}

func applicationPlacedServiceIDs(cfg config) []grove.ServiceID {
	serviceIDs := make([]grove.ServiceID, 0, len(cfg.componentKinds))
	for _, kind := range cfg.componentKinds {
		component, _ := activeApplication.componentByKind(kind)
		serviceIDs = append(serviceIDs, component.ServiceID)
	}
	return serviceIDs
}

func applicationStartupState(cfg config, desired systemnats.DesiredView) ([]systemnats.PlacementRecord, []grove.ServiceID) {
	if cfg.systemNATSRecovery && desired.Ready && len(desired.Deployments) != 0 {
		return nil, nil
	}
	if cfg.adoptCluster {
		return nil, applicationPlacedServiceIDs(cfg)
	}
	return applicationPlacements(cfg), applicationPlacedServiceIDs(cfg)
}

// rejoinRuntimePlacedCluster lets a restarted founding node rejoin a cluster
// whose services recovery already moved to other nodes. It must not reclaim
// those placements or bind the ingress a survivor now serves (grove#41), so
// it joins like any other node: it hosts every non-ingress component without
// claiming placement, and keeps the ingress listener only so recovery can move
// the ingress back if its current node fails.
func rejoinRuntimePlacedCluster(ctx context.Context, cfg config, transport *systemnats.Transport) (config, error) {
	records, err := systemnats.RecordedPlacements(ctx, transport)
	if err != nil {
		return cfg, fmt.Errorf("read placements before rejoining: %w", err)
	}
	movedAway := slices.ContainsFunc(records, func(record systemnats.PlacementRecord) bool {
		return record.NodeID != cfg.nodeID
	})
	if !movedAway {
		return cfg, nil
	}
	cfg.hostAll = false
	cfg.adoptCluster = true
	cfg.componentKinds = slices.DeleteFunc(slices.Clone(cfg.componentKinds), func(kind string) bool {
		component, _ := activeApplication.componentByKind(kind)
		return component.HTTPHandler != nil
	})
	return cfg, nil
}

func waitForDesiredState(ctx context.Context, desired *systemnats.Desired) (systemnats.DesiredView, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		view := desired.Snapshot()
		if view.Ready {
			return view, nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return systemnats.DesiredView{}, fmt.Errorf("wait for desired deployment state: %w", ctx.Err())
		}
	}
}
