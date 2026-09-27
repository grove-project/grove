package runtime

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/systemnats"
)

const recoveryInterval = 50 * time.Millisecond

var errRecoveryComponentUnavailable = errors.New("replacement Grovlet cannot run placed component")

type serviceRecovery struct {
	nodeID     string
	health     *systemnats.Health
	placement  *systemnats.Placement
	components *componentManager
	transport  *systemnats.Transport
}

func newServiceRecovery(
	nodeID string,
	health *systemnats.Health,
	placement *systemnats.Placement,
	components *componentManager,
	transport *systemnats.Transport,
) *serviceRecovery {
	return &serviceRecovery{
		nodeID:     nodeID,
		health:     health,
		placement:  placement,
		components: components,
		transport:  transport,
	}
}

func (r *serviceRecovery) Run(ctx context.Context) error {
	ticker := time.NewTicker(recoveryInterval)
	defer ticker.Stop()
	for {
		_ = r.reconcile(ctx)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// reconcile recovers every placement owned by a lost node. Starting the
// components is local work and never waits on the control plane, so ingress is
// serving again as soon as the loss is observed; committing the new placement
// records needs the replicated control state and waits for its leader.
func (r *serviceRecovery) reconcile(ctx context.Context) error {
	records := selectRecoveries(r.nodeID, r.health.Snapshot(), r.placement.Snapshot())
	if len(records) == 0 {
		return nil
	}
	sort.SliceStable(records, func(i, j int) bool {
		return isIngressService(records[i].ServiceID) && !isIngressService(records[j].ServiceID)
	})
	var errs []error
	started := make(map[grove.ServiceID]bool, len(records))
	claimable := make(map[grove.ServiceID]bool, len(records))
	for _, current := range records {
		didStart, ready, err := r.ensureStarted(ctx, current.ServiceID)
		started[current.ServiceID], claimable[current.ServiceID] = didStart, ready
		errs = append(errs, err)
	}
	if !r.placement.Snapshot().Ready {
		// No control-plane leader: fail fast instead of blocking on writes.
		return errors.Join(append(errs, systemnats.ErrControlPlaneUnavailable)...)
	}
	for _, current := range records {
		if claimable[current.ServiceID] {
			errs = append(errs, r.claim(ctx, current, started[current.ServiceID]))
		}
	}
	return errors.Join(errs...)
}

// ensureStarted starts serviceID locally if it is not running. ready reports
// whether the component is running and its placement can be claimed.
func (r *serviceRecovery) ensureStarted(ctx context.Context, serviceID grove.ServiceID) (started, ready bool, err error) {
	component, ok := findComponent(r.components.SnapshotComponents(), serviceID)
	if !ok || component.InvocationSubject == "" {
		return false, false, errRecoveryComponentUnavailable
	}
	switch component.State {
	case systemnats.ComponentStopped, systemnats.ComponentFailed:
		if err := r.components.StartComponent(ctx, serviceID); err != nil {
			return false, false, err
		}
		component, ok = findComponent(r.components.SnapshotComponents(), serviceID)
		if !ok || component.State != systemnats.ComponentHealthy {
			return true, false, errRecoveryComponentUnavailable
		}
		return true, true, nil
	case systemnats.ComponentHealthy, systemnats.ComponentDebugging:
		return false, true, nil
	case systemnats.ComponentStarting, systemnats.ComponentStopping:
		return false, false, nil
	default:
		return false, false, errRecoveryComponentUnavailable
	}
}

// claim commits this node as the new owner of a lost node's placement.
func (r *serviceRecovery) claim(ctx context.Context, current systemnats.PlacementRecord, started bool) error {
	component, ok := findComponent(r.components.SnapshotComponents(), current.ServiceID)
	if !ok || component.InvocationSubject == "" {
		return errRecoveryComponentUnavailable
	}
	replacement := systemnats.PlacementRecord{
		ServiceID:         current.ServiceID,
		NodeID:            r.nodeID,
		InvocationSubject: component.InvocationSubject,
		ArtifactDigest:    current.ArtifactDigest,
	}
	observed, err := r.placement.Replace(ctx, r.transport, current, replacement)
	if err == nil {
		return nil
	}
	if started && observed.NodeID != r.nodeID {
		_ = r.components.StopComponent(ctx, current.ServiceID)
	}
	if errors.Is(err, systemnats.ErrPlacementChanged) {
		return nil
	}
	return err
}

func isIngressService(serviceID grove.ServiceID) bool {
	component, ok := activeApplication.componentByID(serviceID)
	return ok && component.HTTPHandler != nil
}

// selectRecoveries returns every placement on a node observed to have failed,
// for the coordinating (first healthy) node only. It uses the last-known
// placement records, so recovery can start components while the control
// plane has no leader.
func selectRecoveries(
	nodeID string,
	cluster systemnats.ClusterView,
	placement systemnats.PlacementView,
) []systemnats.PlacementRecord {
	if !cluster.Ready {
		return nil
	}
	healthByNode := make(map[string]systemnats.ClusterNode, len(cluster.Nodes))
	coordinator := ""
	for _, node := range cluster.Nodes {
		healthByNode[node.NodeID] = node
		if node.Health == systemnats.HealthHealthy && (coordinator == "" || node.NodeID < coordinator) {
			coordinator = node.NodeID
		}
	}
	if coordinator == "" || coordinator != nodeID {
		return nil
	}
	var records []systemnats.PlacementRecord
	for _, record := range placement.Placements {
		node, exists := healthByNode[record.NodeID]
		if exists && node.Health == systemnats.HealthUnavailable && node.LastSeen != "" {
			records = append(records, record)
		}
	}
	return records
}

func selectRecovery(
	nodeID string,
	cluster systemnats.ClusterView,
	placement systemnats.PlacementView,
) (systemnats.PlacementRecord, bool) {
	if !cluster.Ready || !placement.Ready {
		return systemnats.PlacementRecord{}, false
	}
	healthByNode := make(map[string]systemnats.ClusterNode, len(cluster.Nodes))
	coordinator := ""
	for _, node := range cluster.Nodes {
		healthByNode[node.NodeID] = node
		if node.Health == systemnats.HealthHealthy && (coordinator == "" || node.NodeID < coordinator) {
			coordinator = node.NodeID
		}
	}
	if coordinator == "" || coordinator != nodeID {
		return systemnats.PlacementRecord{}, false
	}
	for _, record := range placement.Placements {
		node, exists := healthByNode[record.NodeID]
		if exists && node.Health == systemnats.HealthUnavailable && node.LastSeen != "" {
			return record, true
		}
	}
	return systemnats.PlacementRecord{}, false
}

func findComponent(view systemnats.ComponentView, serviceID grove.ServiceID) (systemnats.ComponentStatus, bool) {
	for _, component := range view.Components {
		if component.ServiceID == serviceID {
			return component, true
		}
	}
	return systemnats.ComponentStatus{}, false
}
