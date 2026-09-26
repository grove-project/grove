package systemnats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/placement"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	// HandlerBucket is the authoritative Grove handler-placement KV bucket. It
	// holds node handler registrations, per-handler placements, and exclusive
	// capability leases.
	HandlerBucket = "GROVE_HANDLERS"

	handlerNodeKeyPrefix      = "nodes."
	handlerPlacementKeyPrefix = "placements."
	handlerLeaseKeyPrefix     = "leases."

	defaultHandlerLeaseTTL       = 3 * time.Second
	defaultHandlerReconcileEvery = 100 * time.Millisecond
)

var (
	// ErrHandlerNotPlaced is returned when a handler has no live placement.
	ErrHandlerNotPlaced = errors.New("grove handler is not placed")
	// ErrHandlerPlacementUnavailable is returned before the handler-placement
	// view or the live-node view has initialized.
	ErrHandlerPlacementUnavailable = errors.New("grove handler placement is unavailable")
	// ErrHandlerPlacementInvalid is returned for an invalid registration.
	ErrHandlerPlacementInvalid = errors.New("grove handler placement configuration is invalid")
)

// HandlerRegistration declares one handler a node can run.
type HandlerRegistration struct {
	Service grove.ServiceID `json:"service"`
	Method  grove.MethodID  `json:"method"`
	// Exclusive requests exactly one active owner cluster-wide.
	Exclusive bool `json:"exclusive,omitempty"`
	// Capability names the exclusive capability the handler's workload claims
	// with grove.Exclusive. It is required when Exclusive is set.
	Capability string `json:"capability,omitempty"`
	// InvocationSubject overrides the node's default subject for this handler,
	// for nodes that host different services behind different endpoints.
	InvocationSubject string `json:"invocation_subject,omitempty"`
}

func (r HandlerRegistration) id() placement.Handler {
	return placement.Handler{Service: r.Service, Method: r.Method}
}

// NodeHandlers is one node's published registrations.
type NodeHandlers struct {
	NodeID            string                `json:"node_id"`
	InvocationSubject string                `json:"invocation_subject"`
	Handlers          []HandlerRegistration `json:"handlers"`
}

// HandlerNode is one concrete placement of a handler.
type HandlerNode struct {
	NodeID            string `json:"node_id"`
	InvocationSubject string `json:"invocation_subject"`
}

// HandlerPlacement is the authoritative placement of one handler.
type HandlerPlacement struct {
	Service    grove.ServiceID `json:"service"`
	Method     grove.MethodID  `json:"method"`
	Exclusive  bool            `json:"exclusive,omitempty"`
	Capability string          `json:"capability,omitempty"`
	// Epoch fences exclusive ownership; it increases whenever the owner moves.
	Epoch uint64        `json:"epoch"`
	Nodes []HandlerNode `json:"nodes"`
}

func (p HandlerPlacement) id() placement.Handler {
	return placement.Handler{Service: p.Service, Method: p.Method}
}

func (p HandlerPlacement) nodeIDs() []string {
	ids := make([]string, len(p.Nodes))
	for i, n := range p.Nodes {
		ids[i] = n.NodeID
	}
	return ids
}

// capabilityLease is the stored claim on an exclusive capability. Beat changes
// on every renewal so observers can tell a live owner from a silent one.
type capabilityLease struct {
	Holder   string `json:"holder"`
	Epoch    uint64 `json:"epoch"`
	Beat     uint64 `json:"beat"`
	Released bool   `json:"released,omitempty"`
}

// LeaseView is one observed exclusive capability lease.
type LeaseView struct {
	Capability string `json:"capability"`
	Holder     string `json:"holder"`
	Epoch      uint64 `json:"epoch"`
}

// HandlerPlacementView is one node's observation of handler placement.
type HandlerPlacementView struct {
	Ready      bool               `json:"ready"`
	Nodes      []NodeHandlers     `json:"nodes"`
	Placements []HandlerPlacement `json:"placements"`
	Leases     []LeaseView        `json:"leases"`
	// Lost lists placements whose node is no longer live. Resolved views drop
	// them from Placements and report them here so recovery stays visible.
	Lost  []LostPlacement `json:"lost,omitempty"`
	Error string          `json:"error,omitempty"`
}

// LostPlacement is a placement removed from routing because its node is not
// live; it stays visible until reconciliation replaces it.
type LostPlacement struct {
	Service grove.ServiceID `json:"service"`
	Method  grove.MethodID  `json:"method"`
	NodeID  string          `json:"node_id"`
}

// HandlerPlacementConfig configures HandlerPlacements. Zero timing fields use
// defaults.
type HandlerPlacementConfig struct {
	NodeID            string
	InvocationSubject string
	Handlers          []HandlerRegistration
	// Live reports the node IDs currently considered healthy and whether that
	// view has initialized. It is typically derived from Health.
	Live func() ([]string, bool)
	// Membership, when set, lets this node keep the handler bucket's
	// replication in step with cluster size, as for the other control state.
	Membership *Membership
	// LeaseTTL bounds how long an owner keeps acting without a successful
	// renewal. A successor waits at least this long before taking over.
	LeaseTTL       time.Duration
	ReconcileEvery time.Duration
	// Now is the monotonic time source; nil uses time.Now.
	Now func() time.Time
}

type observedLease struct {
	record    capabilityLease
	revision  uint64
	firstSeen time.Time
}

type heldLease struct {
	capability string
	handler    placement.Handler
	epoch      uint64
	beat       uint64
	revision   uint64
	renewedAt  time.Time
	lost       bool
	released   bool
}

// HandlerPlacements runs Option-A reconciliation: every node watches the same
// KV state and computes the same deterministic placement, and compare-and-swap
// writes make concurrent reconcilers converge on a single result.
type HandlerPlacements struct {
	cfg HandlerPlacementConfig

	mu     sync.RWMutex
	view   HandlerPlacementView
	leases map[string]observedLease
	held   map[string]*heldLease
	// revisions are the KV revisions of the placement keys read from the
	// watch, used as CAS preconditions.
	revisions map[string]uint64

	dirty    bool
	kv       jetstream.KeyValue
	selector placement.Selector
}

// NewHandlerPlacements validates cfg and creates a handler placement observer.
func NewHandlerPlacements(cfg HandlerPlacementConfig) (*HandlerPlacements, error) {
	if cfg.NodeID == "" || cfg.InvocationSubject == "" || cfg.Live == nil {
		return nil, &Error{Operation: "configure Grove handler placement", Err: ErrHandlerPlacementInvalid}
	}
	if err := validateRegistrations(cfg.Handlers); err != nil {
		return nil, err
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = defaultHandlerLeaseTTL
	}
	if cfg.ReconcileEvery <= 0 {
		cfg.ReconcileEvery = defaultHandlerReconcileEvery
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &HandlerPlacements{
		cfg:       cfg,
		view:      HandlerPlacementView{Nodes: []NodeHandlers{}, Placements: []HandlerPlacement{}, Leases: []LeaseView{}},
		leases:    make(map[string]observedLease),
		held:      make(map[string]*heldLease),
		revisions: make(map[string]uint64),
	}, nil
}

func validateRegistrations(handlers []HandlerRegistration) error {
	seen := make(map[placement.Handler]bool)
	for _, r := range handlers {
		if r.Service == 0 || seen[r.id()] || (r.Exclusive && !validCapabilityName(r.Capability)) {
			return &Error{Operation: "configure Grove handler placement", Err: ErrHandlerPlacementInvalid}
		}
		seen[r.id()] = true
	}
	return nil
}

// SetHandlers replaces this node's registrations, for example as hosted
// components start and stop. The change is published on the next reconcile.
func (h *HandlerPlacements) SetHandlers(handlers []HandlerRegistration) error {
	if err := validateRegistrations(handlers); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if reflect.DeepEqual(h.cfg.Handlers, handlers) {
		return nil
	}
	h.cfg.Handlers = append([]HandlerRegistration(nil), handlers...)
	h.dirty = true
	return nil
}

func validCapabilityName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_/=", r)) {
			return false
		}
	}
	return true
}

// HandlerPlacementKey returns the KV key for one handler's placement.
func HandlerPlacementKey(service grove.ServiceID, method grove.MethodID) string {
	return handlerPlacementKeyPrefix + strconv.FormatUint(uint64(service), 10) + "." + strconv.FormatUint(uint64(method), 10)
}

func handlerFromPlacementKey(key string) (placement.Handler, error) {
	parts := strings.Split(strings.TrimPrefix(key, handlerPlacementKeyPrefix), ".")
	if !strings.HasPrefix(key, handlerPlacementKeyPrefix) || len(parts) != 2 {
		return placement.Handler{}, fmt.Errorf("validate handler placement key %q", key)
	}
	service, err1 := strconv.ParseUint(parts[0], 10, 32)
	method, err2 := strconv.ParseUint(parts[1], 10, 32)
	if err1 != nil || err2 != nil {
		return placement.Handler{}, fmt.Errorf("validate handler placement key %q", key)
	}
	return placement.Handler{Service: grove.ServiceID(service), Method: grove.MethodID(method)}, nil
}

// Run publishes this node's registrations, watches authoritative handler
// state, reconciles placements, and renews held leases until ctx ends.
func (h *HandlerPlacements) Run(ctx context.Context, transport *Transport) error {
	for {
		err := h.run(ctx, transport)
		if err == nil || ctx.Err() != nil {
			return ctx.Err()
		}
		h.mu.Lock()
		h.view.Error = err.Error()
		h.mu.Unlock()
		select {
		case <-time.After(placementRetryDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (h *HandlerPlacements) run(ctx context.Context, transport *Transport) error {
	js, err := jetstream.New(transport.connection)
	if err != nil {
		return fmt.Errorf("new JetStream client: %w", err)
	}
	setupCtx, cancel := operationContext(ctx)
	kv, err := openOrCreateKeyValue(setupCtx, js, jetstream.KeyValueConfig{
		Bucket:      HandlerBucket,
		Description: "Authoritative Grove handler placement",
		History:     1,
		Storage:     jetstream.FileStorage,
		Replicas:    controlStateBootstrapReplicas,
	})
	if err != nil {
		cancel()
		return fmt.Errorf("open handler placement bucket: %w", err)
	}
	h.mu.Lock()
	h.kv = kv
	h.mu.Unlock()
	err = h.publishRegistrations(setupCtx, kv)
	cancel()
	if err != nil {
		return err
	}
	// A graceful exit withdraws the registration so peers stop placing here.
	defer func() {
		withdrawCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultOperationTimeout)
		defer cancel()
		_ = kv.Delete(withdrawCtx, handlerNodeKeyPrefix+h.cfg.NodeID)
	}()

	watcher, err := kv.WatchAll(ctx)
	if err != nil {
		return fmt.Errorf("watch handler placement bucket: %w", err)
	}
	defer watcher.Stop()

	nodes := make(map[string]NodeHandlers)
	placements := make(map[placement.Handler]HandlerPlacement)
	initialized := false
	tick := time.NewTicker(h.cfg.ReconcileEvery)
	defer tick.Stop()
	replicaCheck := time.NewTicker(time.Second)
	defer replicaCheck.Stop()
	refresh := time.NewTicker(placementRefreshInterval)
	defer refresh.Stop()
	for {
		select {
		case entry, ok := <-watcher.Updates():
			if !ok {
				return errPlacementWatchClosed
			}
			if entry == nil {
				initialized = true
			} else if err := h.apply(entry, nodes, placements); err != nil {
				return err
			}
			if initialized {
				h.publish(nodes, placements)
			}
		case <-replicaCheck.C:
			h.reconcileReplicas(ctx, js)
		case <-refresh.C:
			if !initialized {
				continue
			}
			// An ordered KV watch can stay open across a stream-leader change
			// without delivering later writes; point reads are the authority.
			refreshCtx, cancel := operationContext(ctx)
			err := h.resync(refreshCtx, kv, nodes, placements)
			cancel()
			if err != nil {
				continue
			}
			h.publish(nodes, placements)
		case <-tick.C:
			if !initialized {
				continue
			}
			h.mu.RLock()
			dirty := h.dirty
			h.mu.RUnlock()
			if dirty {
				opCtx, cancel := operationContext(ctx)
				_ = h.publishRegistrations(opCtx, kv)
				cancel()
			}
			h.reconcile(ctx, kv, nodes, placements)
			h.renew(ctx, kv)
			h.publish(nodes, placements)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// reconcileReplicas grows the bucket's replication with membership, exactly as
// the membership watcher does for the buckets it created before this one.
func (h *HandlerPlacements) reconcileReplicas(ctx context.Context, js jetstream.JetStream) {
	if h.cfg.Membership == nil {
		return
	}
	view := h.cfg.Membership.Snapshot()
	if !view.Ready {
		return
	}
	records := make(map[string]MembershipRecord, len(view.Members))
	for _, member := range view.Members {
		records[member.NodeID] = member
	}
	_ = reconcileControlStateReplicas(ctx, js, records, false)
}

func (h *HandlerPlacements) publishRegistrations(ctx context.Context, kv jetstream.KeyValue) error {
	h.mu.Lock()
	registration, err := json.Marshal(NodeHandlers{
		NodeID: h.cfg.NodeID, InvocationSubject: h.cfg.InvocationSubject, Handlers: append([]HandlerRegistration(nil), h.cfg.Handlers...),
	})
	h.dirty = false
	h.mu.Unlock()
	if err != nil {
		return err
	}
	if _, err := kv.Put(ctx, handlerNodeKeyPrefix+h.cfg.NodeID, registration); err != nil {
		h.mu.Lock()
		h.dirty = true
		h.mu.Unlock()
		return fmt.Errorf("publish handler registrations: %w", err)
	}
	return nil
}

// deletedEntry lets a missing key flow through apply as a deletion.
type deletedEntry struct {
	jetstream.KeyValueEntry
	key string
}

func (e deletedEntry) Key() string                     { return e.key }
func (e deletedEntry) Value() []byte                   { return nil }
func (e deletedEntry) Revision() uint64                { return 0 }
func (e deletedEntry) Operation() jetstream.KeyValueOp { return jetstream.KeyValueDelete }

// resync rebuilds the local view from point reads of every key in the bucket
// plus every key already known, so both missed writes and missed deletions
// converge. It leaves the view unchanged if any read fails.
func (h *HandlerPlacements) resync(
	ctx context.Context,
	kv jetstream.KeyValue,
	nodes map[string]NodeHandlers,
	placements map[placement.Handler]HandlerPlacement,
) error {
	keys, err := kv.Keys(ctx)
	if err != nil && !errors.Is(err, jetstream.ErrNoKeysFound) {
		return err
	}
	// A key listing can be incomplete while a stream moves leaders, so union it
	// with what is already known.
	candidates := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		candidates[key] = struct{}{}
	}
	for id := range nodes {
		candidates[handlerNodeKeyPrefix+id] = struct{}{}
	}
	for id := range placements {
		candidates[HandlerPlacementKey(id.Service, id.Method)] = struct{}{}
	}
	h.mu.RLock()
	for capability := range h.leases {
		candidates[handlerLeaseKeyPrefix+capability] = struct{}{}
	}
	h.mu.RUnlock()

	entries := make([]jetstream.KeyValueEntry, 0, len(candidates))
	for key := range candidates {
		entry, err := kv.Get(ctx, key)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			entries = append(entries, deletedEntry{key: key})
			continue
		}
		if err != nil {
			return err
		}
		entries = append(entries, entry)
	}
	for _, entry := range entries {
		if err := h.apply(entry, nodes, placements); err != nil {
			return err
		}
	}
	return nil
}

func (h *HandlerPlacements) apply(
	entry jetstream.KeyValueEntry,
	nodes map[string]NodeHandlers,
	placements map[placement.Handler]HandlerPlacement,
) error {
	key := entry.Key()
	deleted := entry.Operation() == jetstream.KeyValueDelete || entry.Operation() == jetstream.KeyValuePurge
	switch {
	case strings.HasPrefix(key, handlerNodeKeyPrefix):
		id := strings.TrimPrefix(key, handlerNodeKeyPrefix)
		if deleted {
			delete(nodes, id)
			return nil
		}
		var record NodeHandlers
		if err := json.Unmarshal(entry.Value(), &record); err != nil || record.NodeID != id {
			return fmt.Errorf("decode node handlers %q: %w", key, errors.Join(ErrHandlerPlacementInvalid, err))
		}
		nodes[id] = record
	case strings.HasPrefix(key, handlerPlacementKeyPrefix):
		id, err := handlerFromPlacementKey(key)
		if err != nil {
			return err
		}
		h.mu.Lock()
		if deleted {
			delete(h.revisions, key)
		} else {
			h.revisions[key] = entry.Revision()
		}
		h.mu.Unlock()
		if deleted {
			delete(placements, id)
			return nil
		}
		var record HandlerPlacement
		if err := json.Unmarshal(entry.Value(), &record); err != nil || record.id() != id {
			return fmt.Errorf("decode handler placement %q: %w", key, errors.Join(ErrHandlerPlacementInvalid, err))
		}
		placements[id] = record
	case strings.HasPrefix(key, handlerLeaseKeyPrefix):
		capability := strings.TrimPrefix(key, handlerLeaseKeyPrefix)
		h.mu.Lock()
		defer h.mu.Unlock()
		if deleted {
			delete(h.leases, capability)
			return nil
		}
		var record capabilityLease
		if err := json.Unmarshal(entry.Value(), &record); err != nil {
			return fmt.Errorf("decode capability lease %q: %w", key, err)
		}
		// Staleness is measured from when this observer first saw a revision,
		// on its own clock, so no clocks need to agree across nodes.
		if prev, ok := h.leases[capability]; !ok || prev.revision != entry.Revision() {
			h.leases[capability] = observedLease{record: record, revision: entry.Revision(), firstSeen: h.cfg.Now()}
		}
	}
	return nil
}

// reconcile computes the deterministic desired placement from live nodes and
// CAS-writes any difference. Losing a race is fine: the winner's result is
// what this node would have written.
func (h *HandlerPlacements) reconcile(
	ctx context.Context,
	kv jetstream.KeyValue,
	nodes map[string]NodeHandlers,
	placements map[placement.Handler]HandlerPlacement,
) {
	live, ready := h.cfg.Live()
	if !ready {
		return
	}
	liveSet := make(map[string]bool, len(live))
	for _, id := range live {
		liveSet[id] = true
	}
	topology := placement.Topology{Current: make(map[placement.Handler][]string, len(placements))}
	subjects := make(map[string]map[placement.Handler]string)
	meta := make(map[placement.Handler]HandlerRegistration)
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if !liveSet[id] {
			continue
		}
		record := nodes[id]
		subjects[id] = make(map[placement.Handler]string, len(record.Handlers))
		node := placement.Node{ID: id, Handlers: make(map[placement.Handler]placement.Scaling, len(record.Handlers))}
		for _, r := range record.Handlers {
			scaling := placement.Automatic
			if r.Exclusive {
				scaling = placement.Exclusive
			}
			node.Handlers[r.id()] = scaling
			subjects[id][r.id()] = cmpSubject(r.InvocationSubject, record.InvocationSubject)
			meta[r.id()] = r
		}
		topology.Nodes = append(topology.Nodes, node)
	}
	for id, record := range placements {
		topology.Current[id] = record.nodeIDs()
	}
	desired := placement.Place(topology)

	opCtx, cancel := operationContext(ctx)
	defer cancel()
	for id, target := range desired {
		current, exists := placements[id]
		if exists && equalStrings(current.nodeIDs(), target) {
			continue
		}
		next := HandlerPlacement{
			Service: id.Service, Method: id.Method,
			Exclusive: meta[id].Exclusive, Capability: meta[id].Capability, Epoch: current.Epoch,
		}
		if next.Exclusive {
			// Epochs only grow, even if the placement key was deleted and
			// recreated: the lease remembers the highest epoch ever claimed.
			floor := current.Epoch
			h.mu.RLock()
			if lease, ok := h.leases[next.Capability]; ok && lease.record.Epoch > floor {
				floor = lease.record.Epoch
			}
			h.mu.RUnlock()
			next.Epoch = placement.NextEpoch(current.nodeIDs(), target, floor)
		}
		for _, nodeID := range target {
			next.Nodes = append(next.Nodes, HandlerNode{NodeID: nodeID, InvocationSubject: subjects[nodeID][id]})
		}
		h.writePlacement(opCtx, kv, id, next, exists)
	}
	for id := range placements {
		if _, wanted := desired[id]; !wanted {
			h.mu.RLock()
			revision := h.revisions[HandlerPlacementKey(id.Service, id.Method)]
			h.mu.RUnlock()
			_ = kv.Delete(opCtx, HandlerPlacementKey(id.Service, id.Method), jetstream.LastRevision(revision))
		}
	}
}

func (h *HandlerPlacements) writePlacement(
	ctx context.Context,
	kv jetstream.KeyValue,
	id placement.Handler,
	next HandlerPlacement,
	exists bool,
) {
	encoded, err := json.Marshal(next)
	if err != nil {
		return
	}
	key := HandlerPlacementKey(id.Service, id.Method)
	if !exists {
		_, _ = kv.Create(ctx, key, encoded)
		return
	}
	h.mu.RLock()
	revision := h.revisions[key]
	h.mu.RUnlock()
	_, _ = kv.Update(ctx, key, encoded, revision)
}

// renew extends every held lease, or marks it lost when another holder has
// taken the capability.
func (h *HandlerPlacements) renew(ctx context.Context, kv jetstream.KeyValue) {
	h.mu.Lock()
	held := make([]*heldLease, 0, len(h.held))
	for _, lease := range h.held {
		if !lease.lost && !lease.released {
			held = append(held, lease)
		}
	}
	h.mu.Unlock()
	for _, lease := range held {
		if !h.ownsPlacement(lease) {
			// Placement moved on: stop renewing so the successor can claim.
			h.mu.Lock()
			lease.lost = true
			h.mu.Unlock()
			continue
		}
		record := capabilityLease{Holder: h.cfg.NodeID, Epoch: lease.epoch, Beat: lease.beat + 1}
		encoded, _ := json.Marshal(record)
		// Stamp before sending so the local expiry is never later than the
		// moment observers first see the new revision.
		sentAt := h.cfg.Now()
		opCtx, cancel := operationContext(ctx)
		revision, err := kv.Update(opCtx, handlerLeaseKeyPrefix+lease.capability, encoded, lease.revision)
		cancel()
		h.mu.Lock()
		switch {
		case err == nil:
			lease.beat, lease.revision, lease.renewedAt = record.Beat, revision, sentAt
		case errors.Is(err, jetstream.ErrKeyExists):
			lease.lost = true
		}
		h.mu.Unlock()
	}
}

func (h *HandlerPlacements) publish(nodes map[string]NodeHandlers, placements map[placement.Handler]HandlerPlacement) {
	view := HandlerPlacementView{
		Ready:      true,
		Nodes:      make([]NodeHandlers, 0, len(nodes)),
		Placements: make([]HandlerPlacement, 0, len(placements)),
		Leases:     []LeaseView{},
	}
	for _, n := range nodes {
		view.Nodes = append(view.Nodes, n)
	}
	sort.Slice(view.Nodes, func(i, j int) bool { return view.Nodes[i].NodeID < view.Nodes[j].NodeID })
	for _, p := range placements {
		view.Placements = append(view.Placements, p)
	}
	sort.Slice(view.Placements, func(i, j int) bool {
		a, b := view.Placements[i], view.Placements[j]
		return a.Service < b.Service || a.Service == b.Service && a.Method < b.Method
	})
	h.mu.Lock()
	for capability, l := range h.leases {
		view.Leases = append(view.Leases, LeaseView{Capability: capability, Holder: l.record.Holder, Epoch: l.record.Epoch})
	}
	sort.Slice(view.Leases, func(i, j int) bool { return view.Leases[i].Capability < view.Leases[j].Capability })
	h.view = view
	h.mu.Unlock()
}

func cmpSubject(specific, fallback string) string {
	if specific != "" {
		return specific
	}
	return fallback
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Snapshot returns a copy of the current observed view.
func (h *HandlerPlacements) Snapshot() HandlerPlacementView {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var copied HandlerPlacementView
	encoded, _ := json.Marshal(h.view)
	_ = json.Unmarshal(encoded, &copied)
	return copied
}

// Lookup returns the handler's placement restricted to currently live nodes.
func (h *HandlerPlacements) Lookup(service grove.ServiceID, method grove.MethodID) (HandlerPlacement, error) {
	live, ready := h.cfg.Live()
	view := h.Snapshot()
	if !view.Ready || !ready {
		return HandlerPlacement{}, &Error{Operation: "resolve Grove handler placement", Err: ErrHandlerPlacementUnavailable}
	}
	liveSet := make(map[string]bool, len(live))
	for _, id := range live {
		liveSet[id] = true
	}
	for _, p := range view.Placements {
		if p.Service != service || p.Method != method {
			continue
		}
		healthy := p.Nodes[:0:0]
		for _, n := range p.Nodes {
			if liveSet[n.NodeID] {
				healthy = append(healthy, n)
			}
		}
		if len(healthy) == 0 {
			break
		}
		p.Nodes = healthy
		return p, nil
	}
	return HandlerPlacement{}, &Error{Operation: "resolve Grove handler placement", Err: ErrHandlerNotPlaced}
}

// AcquireExclusive implements grove.ExclusiveProvider. It waits for a previous
// owner's lease to expire, then claims the capability with a fenced CAS write.
// It returns an unheld lease when this node is not the placed owner.
func (h *HandlerPlacements) AcquireExclusive(ctx context.Context, capability string) (grove.Lease, error) {
	ticker := time.NewTicker(h.cfg.ReconcileEvery / 2)
	defer ticker.Stop()
	for {
		lease, done, err := h.TryAcquireExclusive(ctx, capability)
		if done || err != nil {
			return lease, err
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

type claimHandle struct {
	owner *HandlerPlacements
	lease *heldLease
}

func (c claimHandle) Held() bool { return c.owner.holds(c.lease) }
func (c claimHandle) Release() {
	c.owner.mu.Lock()
	c.lease.released = true
	c.owner.mu.Unlock()
}

type deniedHandle struct{}

func (deniedHandle) Held() bool { return false }
func (deniedHandle) Release()   {}

// holds reports whether the lease may still act: renewed within the TTL, not
// superseded, and still the placement's owner at the same epoch.
// ownsPlacement reports whether the observed placement still names this node
// as the owner at the lease's epoch.
func (h *HandlerPlacements) ownsPlacement(lease *heldLease) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.ownsPlacementLocked(lease)
}

func (h *HandlerPlacements) ownsPlacementLocked(lease *heldLease) bool {
	for _, p := range h.view.Placements {
		if p.id() == lease.handler {
			return p.Epoch == lease.epoch && len(p.Nodes) == 1 && p.Nodes[0].NodeID == h.cfg.NodeID
		}
	}
	return false
}

func (h *HandlerPlacements) holds(lease *heldLease) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if lease.lost || lease.released || h.cfg.Now().Sub(lease.renewedAt) >= h.cfg.LeaseTTL {
		return false
	}
	return h.ownsPlacementLocked(lease)
}

// TryAcquireExclusive makes one non-blocking claim attempt. It reports
// done=true with a lease (held or denied) once the outcome is decided, and
// done=false while a previous owner's lease is still running out.
func (h *HandlerPlacements) TryAcquireExclusive(ctx context.Context, capability string) (grove.Lease, bool, error) {
	return h.tryClaim(ctx, capability)
}

// tryClaim reports done=true with a lease (held or denied) once the outcome is
// decided, and done=false while a previous owner's lease is still running out.
func (h *HandlerPlacements) tryClaim(ctx context.Context, capability string) (grove.Lease, bool, error) {
	h.mu.Lock()
	var owned HandlerPlacement
	found := false
	for _, p := range h.view.Placements {
		if p.Exclusive && p.Capability == capability {
			owned, found = p, true
		}
	}
	if !found || len(owned.Nodes) != 1 || owned.Nodes[0].NodeID != h.cfg.NodeID {
		h.mu.Unlock()
		return deniedHandle{}, true, nil
	}
	if existing := h.held[capability]; existing != nil && !existing.lost && !existing.released && existing.epoch == owned.Epoch {
		h.mu.Unlock()
		return claimHandle{owner: h, lease: existing}, true, nil
	}
	observed, seen := h.leases[capability]
	now := h.cfg.Now()
	switch {
	case seen && observed.record.Holder != h.cfg.NodeID && !observed.record.Released &&
		now.Sub(observed.firstSeen) < h.cfg.LeaseTTL:
		// The previous owner may still be acting until its own TTL runs out.
		h.mu.Unlock()
		return nil, false, nil
	}
	h.mu.Unlock()

	record := capabilityLease{Holder: h.cfg.NodeID, Epoch: owned.Epoch, Beat: 1}
	encoded, _ := json.Marshal(record)
	key := handlerLeaseKeyPrefix + capability
	stamp := h.cfg.Now()
	opCtx, cancel := operationContext(ctx)
	defer cancel()
	var revision uint64
	var err error
	if seen {
		revision, err = h.updateLease(opCtx, key, encoded, observed.revision)
	} else {
		revision, err = h.createLease(opCtx, key, encoded)
	}
	if errors.Is(err, jetstream.ErrKeyExists) || errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, false, nil // lost the claim race; re-evaluate on the next tick
	}
	if err != nil {
		return nil, false, err
	}
	lease := &heldLease{
		capability: capability, handler: owned.id(), epoch: owned.Epoch,
		beat: record.Beat, revision: revision, renewedAt: stamp,
	}
	h.mu.Lock()
	h.held[capability] = lease
	h.mu.Unlock()
	return claimHandle{owner: h, lease: lease}, true, nil
}

func (h *HandlerPlacements) bucket() (jetstream.KeyValue, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.kv == nil {
		return nil, &Error{Operation: "claim Grove capability", Err: ErrHandlerPlacementUnavailable}
	}
	return h.kv, nil
}

func (h *HandlerPlacements) createLease(ctx context.Context, key string, value []byte) (uint64, error) {
	kv, err := h.bucket()
	if err != nil {
		return 0, err
	}
	return kv.Create(ctx, key, value)
}

func (h *HandlerPlacements) updateLease(ctx context.Context, key string, value []byte, revision uint64) (uint64, error) {
	kv, err := h.bucket()
	if err != nil {
		return 0, err
	}
	return kv.Update(ctx, key, value, revision)
}

// Dispatch serves request against dispatcher with this node's exclusive
// capability provider attached, so handlers can call grove.Exclusive.
func (h *HandlerPlacements) Dispatch(dispatcher *grove.Dispatcher) Handler {
	return func(ctx context.Context, request grove.RequestEnvelope) grove.ResponseEnvelope {
		return dispatcher.Dispatch(grove.WithExclusiveProvider(ctx, h), request)
	}
}

// HandlerPlacementClient creates a Grove Client that resolves each call to one
// of the handler's live placements, spreading calls round-robin.
func (t *Transport) HandlerPlacementClient(h *HandlerPlacements) (*grove.Client, error) {
	if h == nil {
		return nil, &Error{Operation: "create handler-routed Grove client", Err: ErrPlacementRequired}
	}
	return grove.NewRoutedClient(handlerRouter{transport: t, placements: h})
}

type handlerRouter struct {
	transport  *Transport
	placements *HandlerPlacements
}

func (r handlerRouter) Route(ctx context.Context, request grove.RequestEnvelope) (grove.ResponseEnvelope, error) {
	found, err := r.placements.Lookup(request.ServiceID, request.MethodID)
	if err != nil {
		return grove.ResponseEnvelope{}, err
	}
	return requestPlaced(ctx, r.transport, &r.placements.selector, found, request)
}

// requestPlaced sends request to one of p's nodes, chosen round-robin. A node
// with no responders (for example a killed process whose health has not yet
// expired) never received the request, so retrying the remaining placements is
// safe even for non-idempotent handlers. Any other failure is returned as is.
func requestPlaced(
	ctx context.Context,
	transport *Transport,
	selector *placement.Selector,
	p HandlerPlacement,
	request grove.RequestEnvelope,
) (grove.ResponseEnvelope, error) {
	remaining := append([]HandlerNode(nil), p.Nodes...)
	for len(remaining) > 0 {
		ids := make([]string, len(remaining))
		for i, n := range remaining {
			ids[i] = n.NodeID
		}
		target, _ := selector.Pick(p.id(), ids)
		for i, n := range remaining {
			if n.NodeID != target {
				continue
			}
			response, err := transport.Request(ctx, n.InvocationSubject, request)
			if err == nil {
				return response, nil
			}
			if errors.Is(err, nats.ErrNoResponders) && len(remaining) > 1 {
				remaining = append(remaining[:i], remaining[i+1:]...)
				break
			}
			return grove.ResponseEnvelope{}, fmt.Errorf("request: %w: %w", grove.ErrTransportFailure, err)
		}
	}
	return grove.ResponseEnvelope{}, &Error{Operation: "resolve Grove handler placement", Err: ErrHandlerNotPlaced}
}
