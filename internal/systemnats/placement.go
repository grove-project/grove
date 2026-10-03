package systemnats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grove-project/grove"
	"github.com/grove-project/grove/internal/controlplane"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	// PlacementBucket is the authoritative Grove service-placement KV bucket.
	PlacementBucket = "GROVE_PLACEMENT"
	// PlacementReplicas is the number of JetStream replicas maintained for
	// authoritative Grove service placement.
	PlacementReplicas = 3

	placementKeyPrefix   = "services."
	placementSubjectRoot = "_GROVE.system.placement."
	placementRetryDelay  = 50 * time.Millisecond
	// placementRefreshFailuresUntilUnavailable consecutive failed refreshes
	// (about one second) mean the control plane has lost its leader.
	placementRefreshFailuresUntilUnavailable = 4
	placementRefreshInterval                 = 250 * time.Millisecond
)

var (
	// ErrPlacementRequired is returned when an operation has no placement view.
	ErrPlacementRequired    = errors.New("grove placement view is required")
	errPlacementWatchClosed = errors.New("grove placement watch closed")
)

// Placement maintains one watcher-derived local view of the authoritative
// JetStream/KV placement bucket.
type Placement struct {
	records []PlacementRecord
	mu      sync.RWMutex
	view    PlacementView
	gate    func() error
}

// SetGate makes the view report not-ready with the gate's error while the
// gate fails, for example while the cluster has too few nodes to serve.
func (p *Placement) SetGate(gate func() error) {
	p.mu.Lock()
	p.gate = gate
	p.mu.Unlock()
}

// NewPlacement creates a placement observer and any explicit assignments it
// should write. An empty record set creates an observation-only view.
func NewPlacement(records []PlacementRecord) (*Placement, error) {
	if err := controlplane.ValidatePlacementRecords(records); err != nil {
		return nil, &Error{Operation: "configure Grove placement", Err: err}
	}
	owned := append([]PlacementRecord(nil), records...)
	return &Placement{
		records: owned,
		view:    PlacementView{Placements: []PlacementRecord{}},
	}, nil
}

// PlacementKey returns the authoritative KV key for serviceID.
func PlacementKey(serviceID grove.ServiceID) string {
	return placementKeyPrefix + strconv.FormatUint(uint64(serviceID), 10)
}

// PlacementSubject returns the System NATS query subject for nodeID's local
// observed placement view.
func PlacementSubject(nodeID string) string {
	return placementSubjectRoot + nodeID
}

// Run writes configured assignments and maintains the watched placement view
// until ctx ends. Transient JetStream initialization failures are reflected in
// Snapshot and retried while the process remains active.
func (p *Placement) Run(ctx context.Context, transport *Transport) error {
	for {
		err := p.watch(ctx, transport)
		if err == nil {
			return nil
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		p.setUnavailable(err)
		timer := time.NewTimer(placementRetryDelay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

// placementReadAttemptTimeout bounds one RecordedPlacements attempt. Like a
// PutIngress write (grove#42), a JetStream request sent while the placement
// stream is being resized or re-electing its leader after a cluster restart
// can be lost without a reply, so an attempt gives up and is retried instead
// of waiting out the caller's whole deadline.
const placementReadAttemptTimeout = 2 * time.Second

// RecordedPlacements reads every placement record in the cluster's control
// state. It never creates the placement bucket; a missing bucket has none.
// An attempt that gets no reply is retried until ctx ends.
func RecordedPlacements(ctx context.Context, transport *Transport) ([]PlacementRecord, error) {
	js, err := jetstream.New(transport.connection)
	if err != nil {
		return nil, fmt.Errorf("new JetStream client: %w", err)
	}
	operationCtx, cancel := operationContext(ctx)
	defer cancel()
	for {
		attemptCtx, cancelAttempt := context.WithTimeout(operationCtx, placementReadAttemptTimeout)
		records, err := readPlacements(attemptCtx, js)
		cancelAttempt()
		if err == nil || operationCtx.Err() != nil || !errors.Is(err, context.DeadlineExceeded) {
			return records, err
		}
	}
}

func readPlacements(ctx context.Context, js jetstream.JetStream) ([]PlacementRecord, error) {
	kv, err := js.KeyValue(ctx, PlacementBucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open placement bucket: %w", err)
	}
	keys, err := kv.ListKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list placement records: %w", err)
	}
	defer keys.Stop()
	var records []PlacementRecord
	for key := range keys.Keys() {
		entry, err := kv.Get(ctx, key)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read placement record %q: %w", key, err)
		}
		record, err := decodePlacementRecord(key, entry.Value())
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	// The key lister closes its channel when ctx ends, which would otherwise
	// read as a complete, possibly empty, list.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list placement records: %w", err)
	}
	return records, nil
}

func (p *Placement) watch(ctx context.Context, transport *Transport) error {
	js, err := jetstream.New(transport.connection)
	if err != nil {
		return fmt.Errorf("new JetStream client: %w", err)
	}
	setupCtx, cancel := operationContext(ctx)
	kv, err := openOrCreateKeyValue(setupCtx, js, placementKeyValueConfig(controlStateBootstrapReplicas))
	if err != nil {
		cancel()
		return fmt.Errorf("create placement bucket: %w", err)
	}
	for _, record := range p.records {
		encoded, err := json.Marshal(record)
		if err != nil {
			cancel()
			return fmt.Errorf("encode placement record: %w", err)
		}
		if _, err := kv.Put(setupCtx, PlacementKey(record.ServiceID), encoded); err != nil {
			cancel()
			return fmt.Errorf("write placement record: %w", err)
		}
	}
	cancel()

	watcher, err := kv.WatchAll(ctx)
	if err != nil {
		return fmt.Errorf("watch placement bucket: %w", err)
	}
	defer watcher.Stop()
	refresh := time.NewTicker(placementRefreshInterval)
	defer refresh.Stop()

	records := make(map[grove.ServiceID]PlacementRecord)
	initialized := false
	refreshFailures := 0
	for {
		select {
		case entry, ok := <-watcher.Updates():
			if !ok {
				return errPlacementWatchClosed
			}
			if entry == nil {
				initialized = true
				p.setReady(records)
				continue
			}
			serviceID, err := serviceIDFromPlacementKey(entry.Key())
			if err != nil {
				return err
			}
			if entry.Operation() == jetstream.KeyValueDelete || entry.Operation() == jetstream.KeyValuePurge {
				delete(records, serviceID)
			} else {
				record, err := decodePlacementRecord(entry.Key(), entry.Value())
				if err != nil {
					return err
				}
				records[serviceID] = record
			}
			if initialized {
				p.setReady(records)
			}
		case <-refresh.C:
			if !initialized {
				continue
			}
			refreshCtx, cancel := operationContext(ctx)
			refreshed, err := refreshPlacementRecords(refreshCtx, kv, records)
			cancel()
			if err != nil {
				refreshFailures++
				if refreshFailures >= placementRefreshFailuresUntilUnavailable {
					// The control plane has had no leader for about a second:
					// stop serving a view nobody can confirm or repair.
					p.setUnavailable(fmt.Errorf("%w: %w", ErrControlPlaneUnavailable, err))
				} else {
					p.setRefreshError(records, err)
				}
				continue
			}
			refreshFailures = 0
			records = refreshed
			p.setReady(records)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// The refresh repairs a known-key view when an ordered KV consumer remains
// open across stream-leader failover without delivering the replacement.
func refreshPlacementRecords(
	ctx context.Context,
	kv jetstream.KeyValue,
	records map[grove.ServiceID]PlacementRecord,
) (map[grove.ServiceID]PlacementRecord, error) {
	keys, err := kv.Keys(ctx)
	if errors.Is(err, jetstream.ErrNoKeysFound) {
		keys = nil
	}
	if err != nil {
		if !errors.Is(err, jetstream.ErrNoKeysFound) {
			return nil, fmt.Errorf("list placement records: %w", err)
		}
	}
	// A key-list snapshot can be temporarily incomplete while a KV stream is
	// moving leaders. Union it with the current view, then use point reads as
	// the authority for both additions and deletions.
	candidates := make(map[string]struct{}, len(keys)+len(records))
	for _, key := range keys {
		candidates[key] = struct{}{}
	}
	for serviceID := range records {
		candidates[PlacementKey(serviceID)] = struct{}{}
	}
	refreshed := make(map[grove.ServiceID]PlacementRecord, len(candidates))
	for key := range candidates {
		entry, err := kv.Get(ctx, key)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("refresh placement record %q: %w", key, err)
		}
		record, err := decodePlacementRecord(key, entry.Value())
		if err != nil {
			return nil, err
		}
		refreshed[record.ServiceID] = record
	}
	return refreshed, nil
}

func serviceIDFromPlacementKey(key string) (grove.ServiceID, error) {
	if !strings.HasPrefix(key, placementKeyPrefix) || len(key) == len(placementKeyPrefix) {
		return 0, fmt.Errorf("validate placement key %q: %w", key, ErrPlacementRecordInvalid)
	}
	value, err := strconv.ParseUint(key[len(placementKeyPrefix):], 10, 32)
	if err != nil || value == 0 {
		return 0, fmt.Errorf("validate placement key %q: %w", key, errors.Join(ErrPlacementRecordInvalid, err))
	}
	return grove.ServiceID(value), nil
}

// Snapshot returns a copy of the current deterministically ordered view.
func (p *Placement) Snapshot() PlacementView {
	p.mu.RLock()
	defer p.mu.RUnlock()
	placements := make([]PlacementRecord, len(p.view.Placements))
	copy(placements, p.view.Placements)
	view := PlacementView{
		Ready:      p.view.Ready,
		Placements: placements,
		Error:      p.view.Error,
	}
	if p.gate != nil {
		if err := p.gate(); err != nil {
			view.Ready = false
			view.Error = err.Error()
		}
	}
	return view
}

// Lookup returns the authoritative destination for serviceID from the current
// watched view.
func (p *Placement) Lookup(serviceID grove.ServiceID) (PlacementRecord, error) {
	record, err := controlplane.FindPlacement(p.Snapshot(), serviceID)
	if err != nil {
		return PlacementRecord{}, &Error{Operation: "resolve Grove placement", Err: err}
	}
	return record, nil
}

// Replace atomically changes current to replacement in the authoritative
// placement bucket. The returned record is the authoritative value observed
// when the comparison fails.
func (p *Placement) Replace(
	ctx context.Context,
	transport *Transport,
	current PlacementRecord,
	replacement PlacementRecord,
) (PlacementRecord, error) {
	if _, err := controlplane.CheckPlacementReplacement(current, current, replacement); err != nil {
		return PlacementRecord{}, &Error{Operation: "replace Grove placement", Err: err}
	}
	js, err := jetstream.New(transport.connection)
	if err != nil {
		return PlacementRecord{}, &Error{Operation: "replace Grove placement", Err: err}
	}
	kv, err := js.KeyValue(ctx, PlacementBucket)
	if err != nil {
		return PlacementRecord{}, &Error{Operation: "replace Grove placement", Err: err}
	}
	key := PlacementKey(current.ServiceID)
	entry, err := kv.Get(ctx, key)
	if err != nil {
		return PlacementRecord{}, &Error{Operation: "read Grove placement for replacement", Err: err}
	}
	observed, err := decodePlacementRecord(key, entry.Value())
	if err != nil {
		return PlacementRecord{}, &Error{Operation: "read Grove placement for replacement", Err: err}
	}
	if write, err := controlplane.CheckPlacementReplacement(observed, current, replacement); err != nil {
		return observed, &Error{Operation: "compare Grove placement for replacement", Err: err}
	} else if !write {
		return observed, nil
	}
	encoded, err := json.Marshal(replacement)
	if err != nil {
		return PlacementRecord{}, &Error{Operation: "encode Grove placement replacement", Err: err}
	}
	if _, err := kv.Update(ctx, key, encoded, entry.Revision()); err != nil {
		if errors.Is(err, jetstream.ErrKeyExists) {
			latest, getErr := kv.Get(ctx, key)
			if getErr != nil {
				return PlacementRecord{}, &Error{Operation: "read changed Grove placement", Err: getErr}
			}
			observed, decodeErr := decodePlacementRecord(key, latest.Value())
			if decodeErr != nil {
				return PlacementRecord{}, &Error{Operation: "read changed Grove placement", Err: decodeErr}
			}
			return observed, &Error{Operation: "compare Grove placement for replacement", Err: ErrPlacementChanged}
		}
		return PlacementRecord{}, &Error{Operation: "write Grove placement replacement", Err: err}
	}
	return replacement, nil
}

func decodePlacementRecord(key string, value []byte) (PlacementRecord, error) {
	serviceID, err := serviceIDFromPlacementKey(key)
	if err != nil {
		return PlacementRecord{}, err
	}
	var record PlacementRecord
	if err := json.Unmarshal(value, &record); err != nil {
		return PlacementRecord{}, fmt.Errorf("decode placement record %q: %w", key, err)
	}
	if record.ServiceID != serviceID || !controlplane.ValidPlacementRecord(record) {
		return PlacementRecord{}, fmt.Errorf("validate placement record %q: %w", key, ErrPlacementRecordInvalid)
	}
	return record, nil
}

func (p *Placement) setReady(records map[grove.ServiceID]PlacementRecord) {
	p.setObserved(records, "")
}

func (p *Placement) setRefreshError(records map[grove.ServiceID]PlacementRecord, err error) {
	p.setObserved(records, err.Error())
}

func (p *Placement) setObserved(records map[grove.ServiceID]PlacementRecord, observedError string) {
	placements := make([]PlacementRecord, 0, len(records))
	for _, record := range records {
		placements = append(placements, record)
	}
	sort.Slice(placements, func(i, j int) bool {
		return placements[i].ServiceID < placements[j].ServiceID
	})
	p.mu.Lock()
	p.view = PlacementView{Ready: true, Placements: placements, Error: observedError}
	p.mu.Unlock()
}

func (p *Placement) setUnavailable(err error) {
	p.mu.Lock()
	p.view.Ready = false
	p.view.Error = err.Error()
	p.mu.Unlock()
}

// ServePlacement registers nodeID's machine-readable placement endpoint and
// waits until its subscription is active.
func (t *Transport) ServePlacement(ctx context.Context, nodeID string, placement *Placement) error {
	if placement == nil {
		return &Error{Operation: "serve Grove placement", Err: ErrPlacementRequired}
	}
	if _, err := t.connection.Subscribe(PlacementSubject(nodeID), func(message *nats.Msg) {
		encoded, err := json.Marshal(placement.Snapshot())
		if err != nil {
			return
		}
		_ = message.Respond(encoded)
	}); err != nil {
		return &Error{Operation: "subscribe Grove placement endpoint", Err: err}
	}
	flushCtx, cancel := operationContext(ctx)
	defer cancel()
	if err := t.connection.FlushWithContext(flushCtx); err != nil {
		return &Error{Operation: "activate Grove placement endpoint", Err: err}
	}
	return nil
}

// RequestPlacement requests nodeID's machine-readable local placement view.
func (t *Transport) RequestPlacement(ctx context.Context, nodeID string) (PlacementView, error) {
	message, err := t.connection.RequestWithContext(ctx, PlacementSubject(nodeID), nil)
	if err != nil {
		return PlacementView{}, &Error{Operation: "request Grove placement", Err: err}
	}
	var view PlacementView
	if err := json.Unmarshal(message.Data, &view); err != nil {
		return PlacementView{}, &Error{Operation: "decode Grove placement", Err: err}
	}
	return view, nil
}

// PlacementClient creates a Grove Client that resolves every service through
// placement before using this System NATS connection.
func (t *Transport) PlacementClient(placement *Placement) (*grove.Client, error) {
	if placement == nil {
		return nil, &Error{Operation: "create placement-routed Grove client", Err: ErrPlacementRequired}
	}
	return grove.NewRoutedClient(placementRouter{transport: t, placement: placement})
}

// ObservedPlacementClient creates a Grove Client that resolves services from
// nodeID's machine-readable placement endpoint before each call.
func (t *Transport) ObservedPlacementClient(nodeID string) (*grove.Client, error) {
	if nodeID == "" {
		return nil, &Error{Operation: "create observed-placement Grove client", Err: ErrPlacementRequired}
	}
	return grove.NewRoutedClient(observedPlacementRouter{transport: t, nodeID: nodeID})
}

// ObservedPlacementRouter is the router behind ObservedPlacementClient, for
// composing with handler-level routing.
func (t *Transport) ObservedPlacementRouter(nodeID string) grove.Router {
	return observedPlacementRouter{transport: t, nodeID: nodeID}
}

type placementRouter struct {
	transport *Transport
	placement *Placement
}

func (r placementRouter) Route(
	ctx context.Context,
	request grove.RequestEnvelope,
) (grove.ResponseEnvelope, error) {
	record, err := r.placement.Lookup(request.ServiceID)
	if err != nil {
		return grove.ResponseEnvelope{}, err
	}
	response, err := r.transport.Request(ctx, record.InvocationSubject, request)
	if err != nil {
		return grove.ResponseEnvelope{}, fmt.Errorf("request: %w: %w", grove.ErrTransportFailure, err)
	}
	return response, nil
}

type observedPlacementRouter struct {
	transport *Transport
	nodeID    string
}

func (r observedPlacementRouter) Route(
	ctx context.Context,
	request grove.RequestEnvelope,
) (grove.ResponseEnvelope, error) {
	view, err := r.transport.RequestPlacement(ctx, r.nodeID)
	if err != nil {
		return grove.ResponseEnvelope{}, err
	}
	record, err := controlplane.FindPlacement(view, request.ServiceID)
	if err != nil {
		return grove.ResponseEnvelope{}, &Error{Operation: "resolve Grove placement", Err: err}
	}
	response, err := r.transport.Request(ctx, record.InvocationSubject, request)
	if err != nil {
		return grove.ResponseEnvelope{}, fmt.Errorf("request: %w: %w", grove.ErrTransportFailure, err)
	}
	return response, nil
}
