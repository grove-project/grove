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

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	// MembershipBucket is the authoritative Grove node-membership KV bucket.
	MembershipBucket = "GROVE_MEMBERSHIP"
	// MembershipReplicas is the number of JetStream replicas maintained for
	// authoritative Grove membership.
	MembershipReplicas = 3

	membershipKeyPrefix   = "nodes."
	membershipSubjectRoot = "_GROVE.system.membership."
	membershipRetryDelay  = 50 * time.Millisecond
)

var (
	// ErrMembershipRecordInvalid is returned when a membership record lacks its
	// explicit logical identity or advertised endpoint.
	ErrMembershipRecordInvalid = errors.New("grove membership record is invalid")
	// ErrMembershipRequired is returned when a membership endpoint has no view
	// to report.
	ErrMembershipRequired    = errors.New("grove membership view is required")
	errMembershipWatchClosed = errors.New("grove membership watch closed")
)

// MembershipRecord is the minimum authoritative identity stored for one Grove
// node. Liveness and health are intentionally not part of this record.
type MembershipRecord struct {
	// NodeID is the node's stable logical identity.
	NodeID string `json:"node_id"`
	// AdvertisedEndpoint is the node's Grove transport endpoint.
	AdvertisedEndpoint string `json:"advertised_endpoint"`
}

// MembershipView is one Grovlet's current observation of authoritative
// membership state.
type MembershipView struct {
	// Ready reports whether the initial JetStream/KV watch snapshot completed.
	Ready bool `json:"ready"`
	// Members contains the observed records sorted by node ID.
	Members []MembershipRecord `json:"members"`
	// Error describes the latest transient initialization or watch failure.
	Error string `json:"error,omitempty"`
}

// Membership maintains one watcher-derived local view of the authoritative
// JetStream/KV membership bucket.
type Membership struct {
	record MembershipRecord
	mu     sync.RWMutex
	view   MembershipView
}

// NewMembership creates the membership observer for record.
func NewMembership(record MembershipRecord) (*Membership, error) {
	if record.NodeID == "" || record.AdvertisedEndpoint == "" {
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
	kv, err := js.CreateOrUpdateKeyValue(setupCtx, jetstream.KeyValueConfig{
		Bucket:      MembershipBucket,
		Description: "Authoritative Grove node membership",
		History:     1,
		Storage:     jetstream.FileStorage,
		Replicas:    MembershipReplicas,
	})
	if err != nil {
		cancel()
		return fmt.Errorf("create membership bucket: %w", err)
	}
	encoded, err := json.Marshal(m.record)
	if err != nil {
		cancel()
		return fmt.Errorf("encode membership record: %w", err)
	}
	if _, err := kv.Put(setupCtx, MembershipKey(m.record.NodeID), encoded); err != nil {
		cancel()
		return fmt.Errorf("register membership record: %w", err)
	}
	cancel()

	watcher, err := kv.WatchAll(ctx)
	if err != nil {
		return fmt.Errorf("watch membership bucket: %w", err)
	}
	defer watcher.Stop()

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
				if entry.Key() != MembershipKey(record.NodeID) || record.AdvertisedEndpoint == "" {
					return fmt.Errorf("validate membership record %q: %w", entry.Key(), ErrMembershipRecordInvalid)
				}
				records[record.NodeID] = record
			}
			if initialized {
				m.setReady(records)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
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
	m.view.Ready = false
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
