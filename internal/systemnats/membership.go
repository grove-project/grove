package systemnats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/grove-project/grove/internal/controlplane"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	// MembershipBucket is the authoritative Grove node-membership KV bucket.
	MembershipBucket = "GROVE_MEMBERSHIP"
	// MembershipReplicas is the number of JetStream replicas maintained for
	// authoritative Grove membership.
	MembershipReplicas = controlplane.MaxControlStateReplicas

	membershipKeyPrefix   = "nodes."
	membershipSubjectRoot = "_GROVE.system.membership."
	membershipRetryDelay  = 50 * time.Millisecond
	membershipReplicaPoll = 250 * time.Millisecond
)

var (
	// ErrMembershipRequired is returned when an operation has no membership
	// view.
	ErrMembershipRequired    = errors.New("grove membership view is required")
	errMembershipWatchClosed = errors.New("grove membership watch closed")
)

// BeginLeave records graceful shutdown intent while the node is still able to
// participate in the replicated control plane.
func (m *Membership) BeginLeave(ctx context.Context, transport *Transport) error {
	m.mu.Lock()
	m.record.Leaving = true
	record := m.record
	knownMembers := append([]MembershipRecord(nil), m.view.Members...)
	m.mu.Unlock()
	encoded, err := json.Marshal(record)
	if err != nil {
		return &Error{Operation: "mark Grove membership leaving", Err: err}
	}
	js, err := jetstream.New(transport.connection)
	if err != nil {
		return &Error{Operation: "mark Grove membership leaving", Err: err}
	}
	kv, err := js.KeyValue(ctx, MembershipBucket)
	if err != nil {
		return &Error{Operation: "mark Grove membership leaving", Err: err}
	}
	if _, err := kv.Put(ctx, MembershipKey(record.NodeID), encoded); err != nil {
		return &Error{Operation: "mark Grove membership leaving", Err: err}
	}
	records, err := readKnownMembershipRecords(ctx, kv, knownMembers, record)
	if err != nil {
		return &Error{Operation: "read Grove membership for graceful replica shrink", Err: err}
	}
	if err := waitForControlStateReplicas(ctx, js, records, true); err != nil {
		return &Error{Operation: "shrink Grove control-state replicas", Err: err}
	}
	return nil
}

func readKnownMembershipRecords(
	ctx context.Context,
	kv jetstream.KeyValue,
	known []MembershipRecord,
	local MembershipRecord,
) (map[string]MembershipRecord, error) {
	records := make(map[string]MembershipRecord, len(known)+1)
	records[local.NodeID] = local
	for _, expected := range known {
		entry, err := kv.Get(ctx, MembershipKey(expected.NodeID))
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			delete(records, expected.NodeID)
			continue
		}
		if err != nil {
			return nil, err
		}
		var observed MembershipRecord
		if err := json.Unmarshal(entry.Value(), &observed); err != nil {
			return nil, fmt.Errorf("decode membership record %q: %w", entry.Key(), err)
		}
		if entry.Key() != MembershipKey(observed.NodeID) || !controlplane.ValidMembershipRecord(observed) {
			return nil, fmt.Errorf("validate membership record %q: %w", entry.Key(), ErrMembershipRecordInvalid)
		}
		records[observed.NodeID] = observed
	}
	return records, nil
}

// AllLeaving reports whether every still-registered member has announced a
// graceful shutdown. Whole-cluster shutdown preserves membership for restart.
func (m *Membership) AllLeaving(
	ctx context.Context,
	transport *Transport,
	members []MembershipRecord,
) (bool, error) {
	js, err := jetstream.New(transport.connection)
	if err != nil {
		return false, &Error{Operation: "read leaving Grove membership", Err: err}
	}
	kv, err := js.KeyValue(ctx, MembershipBucket)
	if err != nil {
		return false, &Error{Operation: "read leaving Grove membership", Err: err}
	}
	registered := 0
	for _, member := range members {
		entry, err := kv.Get(ctx, MembershipKey(member.NodeID))
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			continue
		}
		if err != nil {
			return false, &Error{Operation: "read leaving Grove membership", Err: err}
		}
		var record MembershipRecord
		if err := json.Unmarshal(entry.Value(), &record); err != nil {
			return false, &Error{Operation: "decode leaving Grove membership", Err: err}
		}
		registered++
		if !record.Leaving {
			return false, nil
		}
	}
	return registered != 0, nil
}

// Membership maintains one watcher-derived local view of the authoritative
// JetStream/KV membership bucket.
type Membership struct {
	record             MembershipRecord
	mu                 sync.RWMutex
	view               MembershipView
	reconciledReplicas int
	retired            bool
}

// NewMembership creates the membership observer for record.
func NewMembership(record MembershipRecord) (*Membership, error) {
	if !controlplane.ValidMembershipRecord(record) {
		return nil, &Error{Operation: "configure Grove membership", Err: ErrMembershipRecordInvalid}
	}
	return &Membership{
		record: record,
		view:   MembershipView{Members: []MembershipRecord{}},
	}, nil
}

// MembershipKey returns the authoritative KV key for nodeID.
func MembershipKey(nodeID string) string {
	return membershipKeyPrefix + nodeID
}

// MembershipSubject returns the System NATS query subject for nodeID's local
// observed membership view.
func MembershipSubject(nodeID string) string {
	return membershipSubjectRoot + nodeID
}

// Run registers the local node and maintains its watched membership view until
// ctx ends. Transient JetStream initialization failures are reflected in
// Snapshot and retried while the process remains active.
func (m *Membership) Run(ctx context.Context, transport *Transport) error {
	for {
		err := m.watch(ctx, transport)
		if err == nil {
			return nil
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		m.setUnavailable(err)
		timer := time.NewTimer(membershipRetryDelay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

func (m *Membership) watch(ctx context.Context, transport *Transport) error {
	js, err := jetstream.New(transport.connection)
	if err != nil {
		return fmt.Errorf("new JetStream client: %w", err)
	}
	setupCtx, cancel := operationContext(ctx)
	kv, err := openOrCreateKeyValue(setupCtx, js, membershipKeyValueConfig(controlStateBootstrapReplicas))
	if err != nil {
		cancel()
		return fmt.Errorf("create membership bucket: %w", err)
	}
	m.mu.Lock()
	if m.retired {
		m.mu.Unlock()
		cancel()
		return nil
	}
	record := m.record
	encoded, err := json.Marshal(record)
	if err != nil {
		m.mu.Unlock()
		cancel()
		return fmt.Errorf("encode membership record: %w", err)
	}
	if _, err := kv.Put(setupCtx, MembershipKey(record.NodeID), encoded); err != nil {
		m.mu.Unlock()
		cancel()
		return fmt.Errorf("register membership record: %w", err)
	}
	m.mu.Unlock()
	cancel()

	watcher, err := kv.WatchAll(ctx)
	if err != nil {
		return fmt.Errorf("watch membership bucket: %w", err)
	}
	defer watcher.Stop()
	replicaPoll := time.NewTicker(membershipReplicaPoll)
	defer replicaPoll.Stop()

	records := make(map[string]MembershipRecord)
	initialized := false
	for {
		select {
		case entry, ok := <-watcher.Updates():
			if !ok {
				return errMembershipWatchClosed
			}
			if entry == nil {
				initialized = true
				if err := m.reconcileControlState(ctx, js, records); err != nil {
					return err
				}
				m.setReady(records)
				continue
			}
			if !strings.HasPrefix(entry.Key(), membershipKeyPrefix) || len(entry.Key()) == len(membershipKeyPrefix) {
				return fmt.Errorf("validate membership key %q: %w", entry.Key(), ErrMembershipRecordInvalid)
			}
			if entry.Operation() == jetstream.KeyValueDelete || entry.Operation() == jetstream.KeyValuePurge {
				delete(records, entry.Key()[len(membershipKeyPrefix):])
			} else {
				var record MembershipRecord
				if err := json.Unmarshal(entry.Value(), &record); err != nil {
					return fmt.Errorf("decode membership record %q: %w", entry.Key(), err)
				}
				if entry.Key() != MembershipKey(record.NodeID) || !controlplane.ValidMembershipRecord(record) {
					return fmt.Errorf("validate membership record %q: %w", entry.Key(), ErrMembershipRecordInvalid)
				}
				records[record.NodeID] = record
			}
			if initialized {
				if err := m.reconcileControlState(ctx, js, records); err != nil {
					return err
				}
				m.setReady(records)
			}
		case <-replicaPoll.C:
			if initialized {
				readCtx, readCancel := context.WithTimeout(ctx, controlStateOperationTimeout)
				authoritative, err := readMembershipRecords(readCtx, kv)
				readCancel()
				if err != nil {
					return err
				}
				if equalMembershipRecords(records, authoritative) {
					continue
				}
				records = authoritative
				if err := m.reconcileControlState(ctx, js, records); err != nil {
					return err
				}
				m.setReady(records)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (m *Membership) reconcileControlState(
	ctx context.Context,
	js jetstream.JetStream,
	records map[string]MembershipRecord,
) error {
	replicas := controlplane.ControlStateReplicas(records)
	m.mu.RLock()
	reconciled := m.reconciledReplicas
	m.mu.RUnlock()
	if reconciled == replicas {
		return nil
	}
	changed, err := reconcileControlStateReplicas(ctx, js, records, false)
	if err != nil {
		return err
	}
	// changed reports whether this round actually resized a bucket (or found
	// a concurrent Grovlet doing so): only then is there anything to wait to
	// catch up. Without it, a node that has never reconciled before (a
	// brand-new join) would always wait here even when the target replica
	// count already matched every bucket's configuration. An already-offline
	// peer that was never evicted (an abruptly failed node's membership
	// record is never marked Leaving; see MembershipRecord.Leaving) would
	// then never report Current, wedging that node's own bootstrap forever
	// even though the control state itself needed no reconciliation.
	if changed {
		if err := waitForControlStateReplicas(ctx, js, records, false); err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.reconciledReplicas = replicas
	m.mu.Unlock()
	return nil
}

func equalMembershipRecords(a, b map[string]MembershipRecord) bool {
	if len(a) != len(b) {
		return false
	}
	for nodeID, record := range a {
		if b[nodeID] != record {
			return false
		}
	}
	return true
}

func readMembershipRecords(ctx context.Context, kv jetstream.KeyValue) (map[string]MembershipRecord, error) {
	keys, err := kv.Keys(ctx)
	if errors.Is(err, jetstream.ErrNoKeysFound) {
		return map[string]MembershipRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	records := make(map[string]MembershipRecord, len(keys))
	for _, key := range keys {
		if !strings.HasPrefix(key, membershipKeyPrefix) || len(key) == len(membershipKeyPrefix) {
			return nil, fmt.Errorf("validate membership key %q: %w", key, ErrMembershipRecordInvalid)
		}
		entry, err := kv.Get(ctx, key)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var record MembershipRecord
		if err := json.Unmarshal(entry.Value(), &record); err != nil {
			return nil, fmt.Errorf("decode membership record %q: %w", key, err)
		}
		if key != MembershipKey(record.NodeID) || !controlplane.ValidMembershipRecord(record) {
			return nil, fmt.Errorf("validate membership record %q: %w", key, ErrMembershipRecordInvalid)
		}
		records[record.NodeID] = record
	}
	return records, nil
}

// Leave removes the local node from authoritative membership. Callers use
// this only for a graceful retirement; an abruptly failed node remains in
// membership so its loss stays observable.
func (m *Membership) Leave(ctx context.Context, transport *Transport) error {
	js, err := jetstream.New(transport.connection)
	if err != nil {
		return &Error{Operation: "retire Grove membership", Err: err}
	}
	kv, err := js.KeyValue(ctx, MembershipBucket)
	if err != nil {
		return &Error{Operation: "retire Grove membership", Err: err}
	}
	m.mu.Lock()
	nodeID := m.record.NodeID
	m.retired = true
	if err := kv.Delete(ctx, MembershipKey(nodeID)); err != nil {
		m.mu.Unlock()
		return &Error{Operation: "retire Grove membership", Err: err}
	}
	members := m.view.Members[:0]
	for _, member := range m.view.Members {
		if member.NodeID != nodeID {
			members = append(members, member)
		}
	}
	m.view.Members = members
	m.mu.Unlock()
	return nil
}

// Snapshot returns a copy of the current deterministically ordered view.
func (m *Membership) Snapshot() MembershipView {
	m.mu.RLock()
	defer m.mu.RUnlock()
	members := make([]MembershipRecord, len(m.view.Members))
	copy(members, m.view.Members)
	return MembershipView{
		Ready:   m.view.Ready,
		Members: members,
		Error:   m.view.Error,
	}
}

func (m *Membership) setReady(records map[string]MembershipRecord) {
	members := make([]MembershipRecord, 0, len(records))
	for _, record := range records {
		members = append(members, record)
	}
	sort.Slice(members, func(i, j int) bool {
		return members[i].NodeID < members[j].NodeID
	})
	m.mu.Lock()
	m.view = MembershipView{Ready: true, Members: members}
	m.mu.Unlock()
}

func (m *Membership) setUnavailable(err error) {
	m.mu.Lock()
	m.view.Error = err.Error()
	m.mu.Unlock()
}

// ServeMembership registers the machine-readable membership endpoint for
// nodeID and waits until its subscription is active.
func (t *Transport) ServeMembership(
	ctx context.Context,
	nodeID string,
	membership *Membership,
) error {
	if membership == nil {
		return &Error{Operation: "serve Grove membership", Err: ErrMembershipRequired}
	}
	if _, err := t.connection.Subscribe(MembershipSubject(nodeID), func(message *nats.Msg) {
		encoded, err := json.Marshal(membership.Snapshot())
		if err != nil {
			return
		}
		_ = message.Respond(encoded)
	}); err != nil {
		return &Error{Operation: "subscribe Grove membership endpoint", Err: err}
	}
	flushCtx, cancel := operationContext(ctx)
	defer cancel()
	if err := t.connection.FlushWithContext(flushCtx); err != nil {
		return &Error{Operation: "activate Grove membership endpoint", Err: err}
	}
	return nil
}

// RequestMembership requests nodeID's machine-readable local membership view.
func (t *Transport) RequestMembership(ctx context.Context, nodeID string) (MembershipView, error) {
	message, err := t.connection.RequestWithContext(ctx, MembershipSubject(nodeID), nil)
	if err != nil {
		return MembershipView{}, &Error{Operation: "request Grove membership", Err: err}
	}
	var view MembershipView
	if err := json.Unmarshal(message.Data, &view); err != nil {
		return MembershipView{}, &Error{Operation: "decode Grove membership", Err: err}
	}
	return view, nil
}
