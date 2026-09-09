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
)

var (
	// ErrPlacementRecordInvalid is returned when a placement record lacks a
	// service, selected node, or invocation subject.
	ErrPlacementRecordInvalid = errors.New("grove placement record is invalid")
	// ErrPlacementRequired is returned when an operation has no placement view.
	ErrPlacementRequired = errors.New("grove placement view is required")
	// ErrPlacementUnavailable is returned before the authoritative placement
	// view has initialized.
	ErrPlacementUnavailable = errors.New("grove placement is unavailable")
	// ErrServiceNotPlaced is returned when no authoritative placement exists for
	// a requested service.
	ErrServiceNotPlaced     = errors.New("grove service is not placed")
	errPlacementWatchClosed = errors.New("grove placement watch closed")
)

// PlacementRecord selects the Grovlet and invocation subject for one service.
type PlacementRecord struct {
	// ServiceID is the stable application-owned service identifier.
	ServiceID grove.ServiceID `json:"service_id"`
	// NodeID is the selected Grovlet's stable logical identity.
	NodeID string `json:"node_id"`
	// InvocationSubject is the selected Grovlet's System NATS call endpoint.
	InvocationSubject string `json:"invocation_subject"`
}

// PlacementView is one Grovlet's current observation of authoritative service
// placement.
type PlacementView struct {
	// Ready reports whether the initial JetStream/KV watch snapshot completed.
	Ready bool `json:"ready"`
	// Placements contains observed records sorted by service ID.
	Placements []PlacementRecord `json:"placements"`
	// Error describes the latest transient initialization or watch failure.
	Error string `json:"error,omitempty"`
}

// Placement maintains one watcher-derived local view of the authoritative
// JetStream/KV placement bucket.
type Placement struct {
	records []PlacementRecord
	mu      sync.RWMutex
	view    PlacementView
}

// NewPlacement creates a placement observer and any explicit assignments it
// should write. An empty record set creates an observation-only view.
func NewPlacement(records []PlacementRecord) (*Placement, error) {
	seen := make(map[grove.ServiceID]struct{}, len(records))
	owned := make([]PlacementRecord, len(records))
	for i, record := range records {
		if record.ServiceID == 0 || record.NodeID == "" || record.InvocationSubject == "" {
			return nil, &Error{Operation: "configure Grove placement", Err: ErrPlacementRecordInvalid}
		}
		if _, exists := seen[record.ServiceID]; exists {
			return nil, &Error{Operation: "configure Grove placement", Err: ErrPlacementRecordInvalid}
		}
		seen[record.ServiceID] = struct{}{}
		owned[i] = record
	}
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

func (p *Placement) watch(ctx context.Context, transport *Transport) error {
	js, err := jetstream.New(transport.connection)
	if err != nil {
		return fmt.Errorf("new JetStream client: %w", err)
	}
	setupCtx, cancel := operationContext(ctx)
	kv, err := js.CreateOrUpdateKeyValue(setupCtx, jetstream.KeyValueConfig{
		Bucket:      PlacementBucket,
		Description: "Authoritative Grove service placement",
		History:     1,
		Storage:     jetstream.FileStorage,
		Replicas:    PlacementReplicas,
	})
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

	records := make(map[grove.ServiceID]PlacementRecord)
	initialized := false
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
				var record PlacementRecord
				if err := json.Unmarshal(entry.Value(), &record); err != nil {
					return fmt.Errorf("decode placement record %q: %w", entry.Key(), err)
				}
				if record.ServiceID != serviceID || record.NodeID == "" || record.InvocationSubject == "" {
					return fmt.Errorf("validate placement record %q: %w", entry.Key(), ErrPlacementRecordInvalid)
				}
				records[serviceID] = record
			}
			if initialized {
				p.setReady(records)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
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
	return PlacementView{
		Ready:      p.view.Ready,
		Placements: placements,
		Error:      p.view.Error,
	}
}

// Lookup returns the authoritative destination for serviceID from the current
// watched view.
func (p *Placement) Lookup(serviceID grove.ServiceID) (PlacementRecord, error) {
	view := p.Snapshot()
	if !view.Ready {
		return PlacementRecord{}, &Error{Operation: "resolve Grove placement", Err: ErrPlacementUnavailable}
	}
	for _, record := range view.Placements {
		if record.ServiceID == serviceID {
			return record, nil
		}
	}
	return PlacementRecord{}, &Error{Operation: "resolve Grove placement", Err: ErrServiceNotPlaced}
}

func (p *Placement) setReady(records map[grove.ServiceID]PlacementRecord) {
	placements := make([]PlacementRecord, 0, len(records))
	for _, record := range records {
		placements = append(placements, record)
	}
	sort.Slice(placements, func(i, j int) bool {
		return placements[i].ServiceID < placements[j].ServiceID
	})
	p.mu.Lock()
	p.view = PlacementView{Ready: true, Placements: placements}
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
	if !view.Ready {
		return grove.ResponseEnvelope{}, &Error{Operation: "resolve Grove placement", Err: ErrPlacementUnavailable}
	}
	for _, record := range view.Placements {
		if record.ServiceID == request.ServiceID {
			response, err := r.transport.Request(ctx, record.InvocationSubject, request)
			if err != nil {
				return grove.ResponseEnvelope{}, fmt.Errorf("request: %w: %w", grove.ErrTransportFailure, err)
			}
			return response, nil
		}
	}
	return grove.ResponseEnvelope{}, &Error{Operation: "resolve Grove placement", Err: ErrServiceNotPlaced}
}
