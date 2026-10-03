package runtime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/grove-project/grove/internal/artifact"
	"github.com/grove-project/grove/internal/inspect"
	"github.com/grove-project/grove/internal/systemnats"
)

// clusterReader holds the console's one System NATS connection for
// inspecting an attached cluster. It reconnects when the cluster's System
// NATS URL changes, for example after a durable restart.
type clusterReader struct {
	mu        sync.Mutex
	url       string
	transport *systemnats.Transport
}

func (r *clusterReader) connect(ctx context.Context, url string) (*systemnats.Transport, error) {
	if url == "" {
		return nil, errors.New("the cluster's System NATS URL is not known yet")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.transport != nil && r.url == url {
		return r.transport, nil
	}
	if r.transport != nil {
		r.transport.Close()
		r.transport = nil
	}
	transport, err := systemnats.Connect(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("connect to application cluster: %w", err)
	}
	r.url, r.transport = url, transport
	return transport, nil
}

func (r *clusterReader) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.transport != nil {
		r.transport.Close()
		r.transport = nil
	}
}

// inspectionTarget is what the console needs to read one cluster, copied so
// that the read itself runs without the controller's lock.
type inspectionTarget struct {
	reader   *clusterReader
	url      string
	nodes    []string
	artifact ArtifactStatus
}

// target copies what reading cluster needs. Callers hold the controller's
// lock or operationMu, which serialize changes to the cluster's fields.
func (cluster *applicationCluster) target() inspectionTarget {
	return inspectionTarget{
		reader:   &cluster.reader,
		url:      cluster.systemNATSURL,
		nodes:    cluster.inspectNodes(),
		artifact: artifactStatus(cluster.artifact),
	}
}

// inspectNodes returns the nodes to read the cluster through: the nodes this
// process hosts, then nodes known from discovery, without a node the
// resilience scenario killed.
func (cluster *applicationCluster) inspectNodes() []string {
	var nodes []string
	if cluster.local != nil {
		nodes = cluster.local.NodeIDs()
	}
	for _, nodeID := range cluster.discoveredNodes {
		if !slices.Contains(nodes, nodeID) {
			nodes = append(nodes, nodeID)
		}
	}
	return slices.DeleteFunc(nodes, func(nodeID string) bool { return nodeID == cluster.failedNodeID })
}

// inspector reads the cluster through its control plane, never through the
// application's ingress.
func (t inspectionTarget) inspector(ctx context.Context) (*inspect.Inspector, error) {
	transport, err := t.reader.connect(ctx, t.url)
	if err != nil {
		return nil, err
	}
	return inspect.New(transport, applicationInspection(t.artifact), t.nodes...), nil
}

func (t inspectionTarget) status(ctx context.Context) (ClusterStatus, error) {
	inspector, err := t.inspector(ctx)
	if err != nil {
		return ClusterStatus{}, err
	}
	return inspector.Status(ctx)
}

// waitForClusterStatus reads cluster's status until accept returns true. The
// caller holds operationMu.
func waitForClusterStatus(ctx context.Context, cluster *applicationCluster, accept func(ClusterStatus) bool) (ClusterStatus, error) {
	ticker := time.NewTicker(applicationConditionInterval)
	defer ticker.Stop()
	var last ClusterStatus
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		status, err := cluster.target().status(attemptCtx)
		cancel()
		if err == nil {
			last = status
			if accept(status) {
				return status, nil
			}
		}
		lastErr = err
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return last, fmt.Errorf("status=%#v: %w", last, errors.Join(lastErr, ctx.Err()))
		}
	}
}

func artifactStatus(inspection artifact.Inspection) ArtifactStatus {
	status := ArtifactStatus{
		ApplicationID:  inspection.Manifest.ApplicationID,
		CodeVersion:    inspection.Manifest.CodeVersion,
		ArtifactDigest: inspection.ArtifactDigest,
	}
	if inspection.Config != nil {
		status.ConfigRevision = inspection.Config.Revision
		status.ConfigDigest = inspection.Config.Digest
	}
	return status
}
