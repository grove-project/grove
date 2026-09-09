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

	"github.com/grove-project/grove"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	// DesiredBucket is the authoritative Grove desired-deployment KV bucket.
	DesiredBucket = "GROVE_DESIRED"
	// DesiredReplicas is the number of JetStream replicas maintained for
	// authoritative desired deployments.
	DesiredReplicas = 3

	desiredKeyPrefix   = "applications."
	desiredSubjectRoot = "_GROVE.system.desired."
	desiredCommandRoot = "_GROVE.system.desired_commands."
	desiredRetryDelay  = 50 * time.Millisecond
)

var (
	// ErrDesiredDeploymentInvalid is returned for incomplete or ambiguous
	// desired deployment records.
	ErrDesiredDeploymentInvalid = errors.New("grove desired deployment is invalid")
	// ErrDesiredRequired is returned when an operation has no desired view.
	ErrDesiredRequired    = errors.New("grove desired deployment view is required")
	errDesiredWatchClosed = errors.New("grove desired deployment watch closed")
)

// DesiredComponent assigns one application service to a logical Grovlet.
type DesiredComponent struct {
	// ServiceID is the stable application-owned service identifier.
	ServiceID grove.ServiceID `json:"service_id"`
	// NodeID is the logical Grovlet intended to run the service.
	NodeID string `json:"node_id"`
}

// DesiredDeployment is the minimum current intent for one Grove application.
type DesiredDeployment struct {
	// ApplicationID is the stable application-owned deployment identifier.
	ApplicationID string `json:"application_id"`
	// Version labels the single current version represented by this MVP record.
	Version string `json:"version"`
	// Components contains the intended service assignments.
	Components []DesiredComponent `json:"components"`
}

// DesiredView is one Grovlet's watcher-derived desired deployment state.
type DesiredView struct {
	// Ready reports whether the initial JetStream/KV watch snapshot completed.
	Ready bool `json:"ready"`
	// Deployments contains records sorted by application ID.
	Deployments []DesiredDeployment `json:"deployments"`
	// Error describes the latest transient initialization or watch failure.
	Error string `json:"error,omitempty"`
}

// Desired maintains one watcher-derived view of authoritative deployment intent.
type Desired struct {
	mu   sync.RWMutex
	view DesiredView
}

// NewDesired creates an uninitialized desired deployment observer.
func NewDesired() *Desired {
	return &Desired{view: DesiredView{Deployments: []DesiredDeployment{}}}
}

// DesiredKey returns the authoritative KV key for applicationID.
func DesiredKey(applicationID string) string { return desiredKeyPrefix + applicationID }

// DesiredSubject returns the System NATS query subject for nodeID's local view.
func DesiredSubject(nodeID string) string { return desiredSubjectRoot + nodeID }

// Run maintains the watched desired deployment view until ctx ends.
func (d *Desired) Run(ctx context.Context, transport *Transport) error {
	for {
		err := d.watch(ctx, transport)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		d.setUnavailable(err)
		timer := time.NewTimer(desiredRetryDelay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

func (d *Desired) watch(ctx context.Context, transport *Transport) error {
	js, err := jetstream.New(transport.connection)
	if err != nil {
		return fmt.Errorf("new JetStream client: %w", err)
	}
	setupCtx, cancel := operationContext(ctx)
	kv, err := js.CreateOrUpdateKeyValue(setupCtx, jetstream.KeyValueConfig{
		Bucket: DesiredBucket, Description: "Authoritative Grove desired deployments",
		History: 1, Storage: jetstream.FileStorage, Replicas: DesiredReplicas,
	})
	cancel()
	if err != nil {
		return fmt.Errorf("create desired deployment bucket: %w", err)
	}
	watcher, err := kv.WatchAll(ctx)
	if err != nil {
		return fmt.Errorf("watch desired deployment bucket: %w", err)
	}
	defer watcher.Stop()
	records := make(map[string]DesiredDeployment)
	initialized := false
	for {
		select {
		case entry, ok := <-watcher.Updates():
			if !ok {
				return errDesiredWatchClosed
			}
			if entry == nil {
				initialized = true
				d.setReady(records)
				continue
			}
			applicationID, err := applicationIDFromDesiredKey(entry.Key())
			if err != nil {
				return err
			}
			if entry.Operation() == jetstream.KeyValueDelete || entry.Operation() == jetstream.KeyValuePurge {
				delete(records, applicationID)
			} else {
				var deployment DesiredDeployment
				if err := json.Unmarshal(entry.Value(), &deployment); err != nil {
					return fmt.Errorf("decode desired deployment %q: %w", entry.Key(), err)
				}
				deployment, err = validateDesiredDeployment(deployment)
				if err != nil || deployment.ApplicationID != applicationID {
					return fmt.Errorf("validate desired deployment %q: %w", entry.Key(), errors.Join(ErrDesiredDeploymentInvalid, err))
				}
				records[applicationID] = deployment
			}
			if initialized {
				d.setReady(records)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Put validates and writes deployment to authoritative JetStream/KV state.
func (d *Desired) Put(ctx context.Context, transport *Transport, deployment DesiredDeployment) error {
	deployment, err := validateDesiredDeployment(deployment)
	if err != nil {
		return &Error{Operation: "validate desired deployment", Err: err}
	}
	encoded, err := json.Marshal(deployment)
	if err != nil {
		return &Error{Operation: "encode desired deployment", Err: err}
	}
	js, err := jetstream.New(transport.connection)
	if err != nil {
		return &Error{Operation: "write desired deployment", Err: err}
	}
	kv, err := js.KeyValue(ctx, DesiredBucket)
	if err != nil {
		return &Error{Operation: "write desired deployment", Err: err}
	}
	if _, err := kv.Put(ctx, DesiredKey(deployment.ApplicationID), encoded); err != nil {
		return &Error{Operation: "write desired deployment", Err: err}
	}
	return nil
}

func validateDesiredDeployment(deployment DesiredDeployment) (DesiredDeployment, error) {
	if deployment.ApplicationID == "" || strings.Contains(deployment.ApplicationID, ".") || deployment.Version == "" || len(deployment.Components) == 0 {
		return DesiredDeployment{}, ErrDesiredDeploymentInvalid
	}
	components := append([]DesiredComponent(nil), deployment.Components...)
	seen := make(map[grove.ServiceID]struct{}, len(components))
	for _, component := range components {
		if component.ServiceID == 0 || component.NodeID == "" {
			return DesiredDeployment{}, ErrDesiredDeploymentInvalid
		}
		if _, exists := seen[component.ServiceID]; exists {
			return DesiredDeployment{}, ErrDesiredDeploymentInvalid
		}
		seen[component.ServiceID] = struct{}{}
	}
	sort.Slice(components, func(i, j int) bool { return components[i].ServiceID < components[j].ServiceID })
	deployment.Components = components
	return deployment, nil
}

func applicationIDFromDesiredKey(key string) (string, error) {
	if !strings.HasPrefix(key, desiredKeyPrefix) || len(key) == len(desiredKeyPrefix) {
		return "", ErrDesiredDeploymentInvalid
	}
	return key[len(desiredKeyPrefix):], nil
}

// Snapshot returns an isolated copy of the current desired deployment view.
func (d *Desired) Snapshot() DesiredView {
	d.mu.RLock()
	defer d.mu.RUnlock()
	deployments := make([]DesiredDeployment, len(d.view.Deployments))
	for i, deployment := range d.view.Deployments {
		deployments[i] = deployment
		deployments[i].Components = append([]DesiredComponent(nil), deployment.Components...)
	}
	return DesiredView{Ready: d.view.Ready, Deployments: deployments, Error: d.view.Error}
}

func (d *Desired) setReady(records map[string]DesiredDeployment) {
	deployments := make([]DesiredDeployment, 0, len(records))
	for _, deployment := range records {
		deployments = append(deployments, deployment)
	}
	sort.Slice(deployments, func(i, j int) bool { return deployments[i].ApplicationID < deployments[j].ApplicationID })
	d.mu.Lock()
	d.view = DesiredView{Ready: true, Deployments: deployments}
	d.mu.Unlock()
}

func (d *Desired) setUnavailable(err error) {
	d.mu.Lock()
	d.view.Ready = false
	d.view.Error = err.Error()
	d.mu.Unlock()
}

type desiredCommandResponse struct {
	Error string `json:"error,omitempty"`
}

// ServeDesired registers nodeID's desired-state read and write endpoints.
func (t *Transport) ServeDesired(ctx context.Context, nodeID string, desired *Desired) error {
	if desired == nil {
		return &Error{Operation: "serve Grove desired deployments", Err: ErrDesiredRequired}
	}
	if _, err := t.connection.Subscribe(DesiredSubject(nodeID), func(message *nats.Msg) { respondJSON(message, desired.Snapshot()) }); err != nil {
		return &Error{Operation: "subscribe Grove desired deployment view", Err: err}
	}
	if _, err := t.connection.Subscribe(desiredCommandRoot+nodeID, func(message *nats.Msg) {
		var deployment DesiredDeployment
		if err := json.Unmarshal(message.Data, &deployment); err != nil {
			respondJSON(message, desiredCommandResponse{Error: err.Error()})
			return
		}
		response := desiredCommandResponse{}
		if err := desired.Put(ctx, t, deployment); err != nil {
			response.Error = err.Error()
		}
		respondJSON(message, response)
	}); err != nil {
		return &Error{Operation: "subscribe Grove desired deployment commands", Err: err}
	}
	flushCtx, cancel := operationContext(ctx)
	defer cancel()
	if err := t.connection.FlushWithContext(flushCtx); err != nil {
		return &Error{Operation: "activate Grove desired deployment endpoints", Err: err}
	}
	return nil
}

// RequestDesired requests nodeID's locally observed desired deployment view.
func (t *Transport) RequestDesired(ctx context.Context, nodeID string) (DesiredView, error) {
	message, err := t.connection.RequestWithContext(ctx, DesiredSubject(nodeID), nil)
	if err != nil {
		return DesiredView{}, &Error{Operation: "request Grove desired deployments", Err: err}
	}
	var view DesiredView
	if err := json.Unmarshal(message.Data, &view); err != nil {
		return DesiredView{}, &Error{Operation: "decode Grove desired deployments", Err: err}
	}
	return view, nil
}

// PutDesired asks nodeID to write deployment to authoritative control state.
func (t *Transport) PutDesired(ctx context.Context, nodeID string, deployment DesiredDeployment) error {
	encoded, err := json.Marshal(deployment)
	if err != nil {
		return &Error{Operation: "encode Grove desired deployment command", Err: err}
	}
	message, err := t.connection.RequestWithContext(ctx, desiredCommandRoot+nodeID, encoded)
	if err != nil {
		return &Error{Operation: "request Grove desired deployment command", Err: err}
	}
	var response desiredCommandResponse
	if err := json.Unmarshal(message.Data, &response); err != nil {
		return &Error{Operation: "decode Grove desired deployment command", Err: err}
	}
	if response.Error != "" {
		return &Error{Operation: "run Grove desired deployment command", Err: errors.New(response.Error)}
	}
	return nil
}
