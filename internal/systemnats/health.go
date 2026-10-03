package systemnats

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/grove-project/grove/internal/controlplane"
	"github.com/nats-io/nats.go"
)

const (
	heartbeatSubjectRoot = "_GROVE.system.heartbeat."
	clusterSubjectRoot   = "_GROVE.system.cluster."

	defaultHeartbeatInterval = 100 * time.Millisecond
	defaultUnavailableAfter  = 500 * time.Millisecond
)

var (
	// ErrHealthNodeIDRequired is returned when a health tracker has no local
	// node identity.
	ErrHealthNodeIDRequired = errors.New("health node ID is required")
	// ErrHealthMembershipRequired is returned when a health tracker has no
	// authoritative membership observer.
	ErrHealthMembershipRequired = errors.New("health membership view is required")
	// ErrHealthConfigInvalid is returned when heartbeat timing is negative.
	ErrHealthConfigInvalid = errors.New("health timing configuration is invalid")
	// ErrHealthRequired is returned when a cluster-view endpoint has no health
	// tracker to report.
	ErrHealthRequired = errors.New("health tracker is required")
)

// HealthConfig controls ephemeral heartbeat publication and failure
// observation.
type HealthConfig struct {
	// MinNodes is the number of registered logical nodes a cluster needs
	// before it serves. Below it the view reports ErrClusterForming.
	MinNodes int
	// Settled, when set, is asked with the registered node count whether the
	// control plane is ready to serve; an error keeps the view not-ready.
	Settled func(nodes int) error
	// HeartbeatInterval is the period between local heartbeat messages.
	HeartbeatInterval time.Duration
	// UnavailableAfter is the maximum receiver-side age considered healthy.
	UnavailableAfter time.Duration
}

type heartbeat struct {
	NodeID string    `json:"node_id"`
	SentAt time.Time `json:"sent_at"`
}

// Health exchanges ephemeral heartbeats and derives liveness for the
// authoritative membership observed by one Grovlet.
type Health struct {
	nodeID     string
	membership *Membership
	config     HealthConfig
	mu         sync.RWMutex
	lastSeen   map[string]time.Time
	view       ClusterView
}

// NewHealth creates a health tracker for nodeID. Zero timing fields use the
// MVP defaults.
func NewHealth(nodeID string, membership *Membership, cfg HealthConfig) (*Health, error) {
	if nodeID == "" {
		return nil, &Error{Operation: "configure node health", Err: ErrHealthNodeIDRequired}
	}
	if membership == nil {
		return nil, &Error{Operation: "configure node health", Err: ErrHealthMembershipRequired}
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = defaultHeartbeatInterval
	}
	if cfg.UnavailableAfter == 0 {
		cfg.UnavailableAfter = defaultUnavailableAfter
	}
	if cfg.HeartbeatInterval < 0 || cfg.UnavailableAfter < 0 {
		return nil, &Error{Operation: "configure node health", Err: ErrHealthConfigInvalid}
	}
	return &Health{
		nodeID:     nodeID,
		membership: membership,
		config:     cfg,
		lastSeen:   make(map[string]time.Time),
		view:       ClusterView{Nodes: []ClusterNode{}},
	}, nil
}

// Run exchanges heartbeats and refreshes the derived cluster view until ctx
// ends.
func (h *Health) Run(ctx context.Context, transport *Transport) error {
	subscription, err := transport.connection.Subscribe(heartbeatSubjectRoot+">", func(message *nats.Msg) {
		var beat heartbeat
		if err := json.Unmarshal(message.Data, &beat); err != nil {
			return
		}
		if message.Subject != heartbeatSubjectRoot+beat.NodeID || beat.NodeID == "" {
			return
		}
		h.record(beat.NodeID, time.Now().UTC())
	})
	if err != nil {
		return &Error{Operation: "subscribe Grove heartbeats", Err: err}
	}
	defer subscription.Unsubscribe()
	flushCtx, cancel := operationContext(ctx)
	if err := transport.connection.FlushWithContext(flushCtx); err != nil {
		cancel()
		return &Error{Operation: "activate Grove heartbeat subscription", Err: err}
	}
	cancel()

	if err := h.publish(ctx, transport); err != nil {
		return err
	}
	h.evaluate(time.Now().UTC())
	ticker := time.NewTicker(h.config.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			if err := h.publish(ctx, transport); err != nil {
				return err
			}
			h.evaluate(now.UTC())
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (h *Health) publish(ctx context.Context, transport *Transport) error {
	encoded, err := json.Marshal(heartbeat{NodeID: h.nodeID, SentAt: time.Now().UTC()})
	if err != nil {
		return &Error{Operation: "encode Grove heartbeat", Err: err}
	}
	if err := transport.connection.Publish(heartbeatSubjectRoot+h.nodeID, encoded); err != nil {
		return &Error{Operation: "publish Grove heartbeat", Err: err}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (h *Health) record(nodeID string, receivedAt time.Time) {
	h.mu.Lock()
	h.lastSeen[nodeID] = receivedAt
	h.mu.Unlock()
	h.evaluate(receivedAt)
}

func (h *Health) evaluate(now time.Time) {
	membership := h.membership.Snapshot()
	h.mu.RLock()
	view := controlplane.EvaluateHealth(membership, h.lastSeen, now, h.config.UnavailableAfter)
	h.mu.RUnlock()

	h.mu.Lock()
	h.view = view
	h.mu.Unlock()
}

// Snapshot returns a copy of the current deterministic cluster view.
func (h *Health) Snapshot() ClusterView {
	h.mu.RLock()
	defer h.mu.RUnlock()
	nodes := make([]ClusterNode, len(h.view.Nodes))
	copy(nodes, h.view.Nodes)
	view := ClusterView{Ready: h.view.Ready, Nodes: nodes, Error: h.view.Error}
	view = controlplane.GateClusterView(view, h.config.MinNodes, h.config.Settled)
	return view
}

// ClusterSubject returns the System NATS query subject for nodeID's local
// cluster view.
func ClusterSubject(nodeID string) string {
	return clusterSubjectRoot + nodeID
}

// ServeClusterView registers nodeID's machine-readable cluster-view endpoint
// and waits until its subscription is active.
func (t *Transport) ServeClusterView(ctx context.Context, nodeID string, health *Health) error {
	if health == nil {
		return &Error{Operation: "serve Grove cluster view", Err: ErrHealthRequired}
	}
	if _, err := t.connection.Subscribe(ClusterSubject(nodeID), func(message *nats.Msg) {
		encoded, err := json.Marshal(health.Snapshot())
		if err != nil {
			return
		}
		_ = message.Respond(encoded)
	}); err != nil {
		return &Error{Operation: "subscribe Grove cluster-view endpoint", Err: err}
	}
	flushCtx, cancel := operationContext(ctx)
	defer cancel()
	if err := t.connection.FlushWithContext(flushCtx); err != nil {
		return &Error{Operation: "activate Grove cluster-view endpoint", Err: err}
	}
	return nil
}

// RequestClusterView requests nodeID's machine-readable local cluster view.
func (t *Transport) RequestClusterView(ctx context.Context, nodeID string) (ClusterView, error) {
	message, err := t.connection.RequestWithContext(ctx, ClusterSubject(nodeID), nil)
	if err != nil {
		return ClusterView{}, &Error{Operation: "request Grove cluster view", Err: err}
	}
	var view ClusterView
	if err := json.Unmarshal(message.Data, &view); err != nil {
		return ClusterView{}, &Error{Operation: "decode Grove cluster view", Err: err}
	}
	return view, nil
}
