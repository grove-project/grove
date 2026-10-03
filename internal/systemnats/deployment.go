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
	// DeploymentBucket is the authoritative Grove artifact and rollout KV
	// bucket.
	DeploymentBucket = "GROVE_DEPLOYMENTS"
	// DeploymentReplicas is the number of JetStream replicas maintained for
	// authoritative deployment state.
	DeploymentReplicas = 3

	deploymentIngressKey          = "ingress"
	deploymentArtifactKeyPrefix   = "artifacts."
	deploymentRolloutKeyPrefix    = "rollouts."
	deploymentSubjectRoot         = "_GROVE.system.deployments."
	deploymentArtifactCommandRoot = "_GROVE.system.deployment_artifacts."
	deploymentRolloutCommandRoot  = "_GROVE.system.rollouts."
	deploymentRetryDelay          = 50 * time.Millisecond
	deploymentWriteTimeout        = 30 * time.Second
)

var (
	// ErrDeploymentsRequired is returned when an operation has no deployment
	// state view.
	ErrDeploymentsRequired   = errors.New("grove deployment view is required")
	errDeploymentWatchClosed = errors.New("grove deployment watch closed")
)

// Deployments maintains one watcher-derived view of authoritative deployment
// state.
type Deployments struct {
	mu   sync.RWMutex
	view DeploymentView
}

// NewDeployments creates an uninitialized deployment-state observer.
func NewDeployments() *Deployments {
	return &Deployments{view: DeploymentView{Artifacts: []DeploymentArtifact{}, Rollouts: []Rollout{}}}
}

// DeploymentArtifactKey returns the immutable KV key for artifactDigest.
func DeploymentArtifactKey(artifactDigest string) (string, error) {
	digest, err := controlplane.SHA256DigestSuffix(artifactDigest)
	if err != nil {
		return "", err
	}
	return deploymentArtifactKeyPrefix + digest, nil
}

// DeploymentRolloutKey returns the authoritative KV key for applicationID.
func DeploymentRolloutKey(applicationID string) (string, error) {
	if !controlplane.ValidApplicationID(applicationID) {
		return "", ErrRolloutInvalid
	}
	return deploymentRolloutKeyPrefix + applicationID, nil
}

// DeploymentSubject returns the System NATS query subject for nodeID's local
// deployment view.
func DeploymentSubject(nodeID string) string { return deploymentSubjectRoot + nodeID }

// Run maintains the watched deployment view until ctx ends.
func (d *Deployments) Run(ctx context.Context, transport *Transport) error {
	for {
		err := d.watch(ctx, transport)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		d.setUnavailable(err)
		timer := time.NewTimer(deploymentRetryDelay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

func (d *Deployments) watch(ctx context.Context, transport *Transport) error {
	js, err := jetstream.New(transport.connection)
	if err != nil {
		return fmt.Errorf("new JetStream client: %w", err)
	}
	setupCtx, cancel := operationContext(ctx)
	kv, err := openOrCreateKeyValue(setupCtx, js, deploymentKeyValueConfig(controlStateBootstrapReplicas))
	cancel()
	if err != nil {
		return fmt.Errorf("create deployment bucket: %w", err)
	}
	watcher, err := kv.WatchAll(ctx)
	if err != nil {
		return fmt.Errorf("watch deployment bucket: %w", err)
	}
	defer watcher.Stop()
	artifacts := make(map[string]DeploymentArtifact)
	rollouts := make(map[string]Rollout)
	ingress := ""
	initialized := false
	for {
		select {
		case entry, ok := <-watcher.Updates():
			if !ok {
				return errDeploymentWatchClosed
			}
			if entry == nil {
				initialized = true
				d.setReady(artifacts, rollouts, ingress)
				continue
			}
			if entry.Key() == deploymentIngressKey {
				ingress = ""
				if entry.Operation() == jetstream.KeyValuePut {
					ingress = string(entry.Value())
				}
				if initialized {
					d.setReady(artifacts, rollouts, ingress)
				}
				continue
			}
			if err := applyDeploymentEntry(entry, artifacts, rollouts); err != nil {
				return err
			}
			if initialized {
				d.setReady(artifacts, rollouts, ingress)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func applyDeploymentEntry(entry jetstream.KeyValueEntry, artifacts map[string]DeploymentArtifact, rollouts map[string]Rollout) error {
	key := entry.Key()
	deleted := entry.Operation() == jetstream.KeyValueDelete || entry.Operation() == jetstream.KeyValuePurge
	switch {
	case strings.HasPrefix(key, deploymentArtifactKeyPrefix):
		digest := "sha256:" + strings.TrimPrefix(key, deploymentArtifactKeyPrefix)
		if _, err := controlplane.SHA256DigestSuffix(digest); err != nil {
			return fmt.Errorf("validate deployment artifact key %q: %w", key, err)
		}
		if deleted {
			delete(artifacts, digest)
			return nil
		}
		artifact, err := decodeDeploymentArtifact(key, entry.Value())
		if err != nil {
			return err
		}
		artifacts[digest] = artifact
		return nil
	case strings.HasPrefix(key, deploymentRolloutKeyPrefix):
		applicationID := strings.TrimPrefix(key, deploymentRolloutKeyPrefix)
		if !controlplane.ValidApplicationID(applicationID) {
			return fmt.Errorf("validate rollout key %q: %w", key, ErrRolloutInvalid)
		}
		if deleted {
			delete(rollouts, applicationID)
			return nil
		}
		rollout, err := decodeRollout(key, entry.Value())
		if err != nil {
			return err
		}
		rollouts[applicationID] = rollout
		return nil
	default:
		return fmt.Errorf("validate deployment key %q: %w", key, ErrDeploymentArtifactInvalid)
	}
}

// PutArtifact creates immutable artifact metadata or accepts an identical
// retry. An artifact digest can never be overwritten with different metadata.
func (d *Deployments) PutArtifact(ctx context.Context, transport *Transport, artifact DeploymentArtifact) error {
	if err := controlplane.ValidateDeploymentArtifact(artifact); err != nil {
		return &Error{Operation: "validate deployment artifact", Err: err}
	}
	encoded, err := json.Marshal(artifact)
	if err != nil {
		return &Error{Operation: "encode deployment artifact", Err: err}
	}
	kv, err := deploymentKV(ctx, transport)
	if err != nil {
		return &Error{Operation: "write deployment artifact", Err: err}
	}
	key, _ := DeploymentArtifactKey(artifact.ArtifactDigest)
	if _, err := kv.Create(ctx, key, encoded); err == nil {
		return nil
	} else if !errors.Is(err, jetstream.ErrKeyExists) {
		return &Error{Operation: "write deployment artifact", Err: err}
	}
	entry, err := kv.Get(ctx, key)
	if err != nil {
		return &Error{Operation: "read deployment artifact", Err: err}
	}
	observed, err := decodeDeploymentArtifact(key, entry.Value())
	if err != nil {
		return &Error{Operation: "read deployment artifact", Err: err}
	}
	if observed != artifact {
		return &Error{Operation: "compare deployment artifact", Err: ErrDeploymentArtifactChanged}
	}
	return nil
}

// ingressWriteAttemptTimeout bounds one PutIngress attempt. A write published
// while the deployment stream is being resized can be lost without a reply, so
// an attempt must give up and let the caller retry instead of waiting out the
// caller's whole deadline (grove#42).
const ingressWriteAttemptTimeout = 2 * time.Second

// PutIngress records the cluster-wide ingress address, or accepts an identical
// retry. The first address wins; a different one is ErrIngressChanged. Each
// call is one bounded attempt; callers retry transient failures.
func (d *Deployments) PutIngress(ctx context.Context, transport *Transport, address string) error {
	if !controlplane.ValidIngressAddress(address) {
		return &Error{Operation: "validate ingress address", Err: ErrIngressInvalid}
	}
	ctx, cancel := context.WithTimeout(ctx, ingressWriteAttemptTimeout)
	defer cancel()
	kv, err := deploymentKV(ctx, transport)
	if err != nil {
		return &Error{Operation: "write ingress address", Err: err}
	}
	if _, err := kv.Create(ctx, deploymentIngressKey, []byte(address)); err == nil {
		return nil
	} else if !errors.Is(err, jetstream.ErrKeyExists) {
		return &Error{Operation: "write ingress address", Err: err}
	}
	entry, err := kv.Get(ctx, deploymentIngressKey)
	if err != nil {
		return &Error{Operation: "read ingress address", Err: err}
	}
	if string(entry.Value()) != address {
		return &Error{Operation: "compare ingress address", Err: ErrIngressChanged}
	}
	return nil
}

// PutRollout commits the first or next rollout generation after verifying all
// referenced immutable artifacts.
func (d *Deployments) PutRollout(ctx context.Context, transport *Transport, rollout Rollout) error {
	rollout, err := controlplane.ValidateRollout(rollout)
	if err != nil {
		return &Error{Operation: "validate rollout", Err: err}
	}
	kv, err := deploymentKV(ctx, transport)
	if err != nil {
		return &Error{Operation: "write rollout", Err: err}
	}
	if err := validateRolloutArtifacts(ctx, kv, rollout); err != nil {
		return &Error{Operation: "validate rollout artifacts", Err: err}
	}
	encoded, err := json.Marshal(rollout)
	if err != nil {
		return &Error{Operation: "encode rollout", Err: err}
	}
	key, _ := DeploymentRolloutKey(rollout.ApplicationID)
	entry, err := kv.Get(ctx, key)
	exists := !errors.Is(err, jetstream.ErrKeyNotFound)
	if err != nil && exists {
		return &Error{Operation: "read rollout", Err: err}
	}
	var stored Rollout
	if exists {
		if stored, err = decodeRollout(key, entry.Value()); err != nil {
			return &Error{Operation: "read rollout", Err: err}
		}
	}
	write, err := controlplane.CheckRolloutCommit(stored, exists, rollout)
	if err != nil {
		operation := "advance rollout"
		if !exists {
			operation = "create rollout"
		}
		return &Error{Operation: operation, Err: err}
	}
	if !write {
		return nil
	}
	if !exists {
		if _, err := kv.Create(ctx, key, encoded); err != nil {
			if errors.Is(err, jetstream.ErrKeyExists) {
				return &Error{Operation: "create rollout", Err: ErrRolloutChanged}
			}
			return &Error{Operation: "create rollout", Err: err}
		}
		return nil
	}
	if _, err := kv.Update(ctx, key, encoded, entry.Revision()); err != nil {
		if errors.Is(err, jetstream.ErrKeyExists) {
			return &Error{Operation: "advance rollout", Err: ErrRolloutChanged}
		}
		return &Error{Operation: "advance rollout", Err: err}
	}
	return nil
}

func deploymentKV(ctx context.Context, transport *Transport) (jetstream.KeyValue, error) {
	js, err := jetstream.New(transport.connection)
	if err != nil {
		return nil, err
	}
	return js.KeyValue(ctx, DeploymentBucket)
}

func validateRolloutArtifacts(ctx context.Context, kv jetstream.KeyValue, rollout Rollout) error {
	current, err := getDeploymentArtifact(ctx, kv, rollout.CurrentArtifactDigest)
	if err != nil {
		return err
	}
	if rollout.CandidateArtifactDigest == "" {
		return controlplane.CheckRolloutArtifacts(rollout, current, nil)
	}
	candidate, err := getDeploymentArtifact(ctx, kv, rollout.CandidateArtifactDigest)
	if err != nil {
		return err
	}
	return controlplane.CheckRolloutArtifacts(rollout, current, &candidate)
}

func getDeploymentArtifact(ctx context.Context, kv jetstream.KeyValue, digest string) (DeploymentArtifact, error) {
	key, err := DeploymentArtifactKey(digest)
	if err != nil {
		return DeploymentArtifact{}, err
	}
	entry, err := kv.Get(ctx, key)
	if err != nil {
		return DeploymentArtifact{}, err
	}
	return decodeDeploymentArtifact(key, entry.Value())
}

func decodeDeploymentArtifact(key string, value []byte) (DeploymentArtifact, error) {
	var artifact DeploymentArtifact
	if err := json.Unmarshal(value, &artifact); err != nil {
		return DeploymentArtifact{}, fmt.Errorf("decode deployment artifact %q: %w", key, err)
	}
	if err := controlplane.ValidateDeploymentArtifact(artifact); err != nil {
		return DeploymentArtifact{}, fmt.Errorf("validate deployment artifact %q: %w", key, err)
	}
	want, _ := DeploymentArtifactKey(artifact.ArtifactDigest)
	if key != want {
		return DeploymentArtifact{}, fmt.Errorf("validate deployment artifact key %q: %w", key, ErrDeploymentArtifactInvalid)
	}
	return artifact, nil
}

func decodeRollout(key string, value []byte) (Rollout, error) {
	var rollout Rollout
	if err := json.Unmarshal(value, &rollout); err != nil {
		return Rollout{}, fmt.Errorf("decode rollout %q: %w", key, err)
	}
	rollout, err := controlplane.ValidateRollout(rollout)
	if err != nil {
		return Rollout{}, fmt.Errorf("validate rollout %q: %w", key, err)
	}
	want, _ := DeploymentRolloutKey(rollout.ApplicationID)
	if key != want {
		return Rollout{}, fmt.Errorf("validate rollout key %q: %w", key, ErrRolloutInvalid)
	}
	return rollout, nil
}

// Snapshot returns an isolated, deterministically ordered deployment view.
func (d *Deployments) Snapshot() DeploymentView {
	d.mu.RLock()
	defer d.mu.RUnlock()
	artifacts := make([]DeploymentArtifact, len(d.view.Artifacts))
	copy(artifacts, d.view.Artifacts)
	rollouts := make([]Rollout, len(d.view.Rollouts))
	for i, rollout := range d.view.Rollouts {
		rollouts[i] = controlplane.CloneRollout(rollout)
	}
	return DeploymentView{Ready: d.view.Ready, Artifacts: artifacts, Rollouts: rollouts, Ingress: d.view.Ingress, Error: d.view.Error}
}

func (d *Deployments) setReady(artifacts map[string]DeploymentArtifact, rollouts map[string]Rollout, ingress string) {
	view := DeploymentView{
		Ready:     true,
		Ingress:   ingress,
		Artifacts: make([]DeploymentArtifact, 0, len(artifacts)),
		Rollouts:  make([]Rollout, 0, len(rollouts)),
	}
	for _, artifact := range artifacts {
		view.Artifacts = append(view.Artifacts, artifact)
	}
	for _, rollout := range rollouts {
		view.Rollouts = append(view.Rollouts, controlplane.CloneRollout(rollout))
	}
	sort.Slice(view.Artifacts, func(i, j int) bool { return view.Artifacts[i].ArtifactDigest < view.Artifacts[j].ArtifactDigest })
	sort.Slice(view.Rollouts, func(i, j int) bool { return view.Rollouts[i].ApplicationID < view.Rollouts[j].ApplicationID })
	d.mu.Lock()
	d.view = view
	d.mu.Unlock()
}

func (d *Deployments) setUnavailable(err error) {
	d.mu.Lock()
	d.view.Ready = false
	d.view.Error = err.Error()
	d.mu.Unlock()
}

type deploymentCommandResponse struct {
	Error string `json:"error,omitempty"`
}

// ServeDeployments registers nodeID's deployment-state read and write
// endpoints.
func (t *Transport) ServeDeployments(ctx context.Context, nodeID string, deployments *Deployments) error {
	if deployments == nil {
		return &Error{Operation: "serve Grove deployments", Err: ErrDeploymentsRequired}
	}
	if _, err := t.connection.Subscribe(DeploymentSubject(nodeID), func(message *nats.Msg) { respondJSON(message, deployments.Snapshot()) }); err != nil {
		return &Error{Operation: "subscribe Grove deployment view", Err: err}
	}
	if _, err := t.connection.Subscribe(deploymentArtifactCommandRoot+nodeID, func(message *nats.Msg) {
		var artifact DeploymentArtifact
		if err := json.Unmarshal(message.Data, &artifact); err != nil {
			respondJSON(message, deploymentCommandResponse{Error: err.Error()})
			return
		}
		response := deploymentCommandResponse{}
		if err := deployments.PutArtifact(ctx, t, artifact); err != nil {
			response.Error = err.Error()
		}
		respondJSON(message, response)
	}); err != nil {
		return &Error{Operation: "subscribe Grove deployment artifact commands", Err: err}
	}
	if _, err := t.connection.Subscribe(deploymentRolloutCommandRoot+nodeID, func(message *nats.Msg) {
		var rollout Rollout
		if err := json.Unmarshal(message.Data, &rollout); err != nil {
			respondJSON(message, deploymentCommandResponse{Error: err.Error()})
			return
		}
		response := deploymentCommandResponse{}
		if err := deployments.PutRollout(ctx, t, rollout); err != nil {
			response.Error = err.Error()
		}
		respondJSON(message, response)
	}); err != nil {
		return &Error{Operation: "subscribe Grove rollout commands", Err: err}
	}
	flushCtx, cancel := operationContext(ctx)
	defer cancel()
	if err := t.connection.FlushWithContext(flushCtx); err != nil {
		return &Error{Operation: "activate Grove deployment endpoints", Err: err}
	}
	return nil
}

// RequestDeployments requests nodeID's locally observed deployment view.
func (t *Transport) RequestDeployments(ctx context.Context, nodeID string) (DeploymentView, error) {
	message, err := t.connection.RequestWithContext(ctx, DeploymentSubject(nodeID), nil)
	if err != nil {
		return DeploymentView{}, &Error{Operation: "request Grove deployments", Err: err}
	}
	var view DeploymentView
	if err := json.Unmarshal(message.Data, &view); err != nil {
		return DeploymentView{}, &Error{Operation: "decode Grove deployments", Err: err}
	}
	return view, nil
}

// PutDeploymentArtifact asks nodeID to persist immutable artifact metadata.
func (t *Transport) PutDeploymentArtifact(ctx context.Context, nodeID string, artifact DeploymentArtifact) error {
	return t.requestDeploymentWrite(ctx, deploymentArtifactCommandRoot+nodeID, artifact, "deployment artifact")
}

// PutRollout asks nodeID to commit a rollout generation.
func (t *Transport) PutRollout(ctx context.Context, nodeID string, rollout Rollout) error {
	return t.requestDeploymentWrite(ctx, deploymentRolloutCommandRoot+nodeID, rollout, "rollout")
}

func (t *Transport) requestDeploymentWrite(ctx context.Context, subject string, value any, operation string) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return &Error{Operation: "encode Grove " + operation + " command", Err: err}
	}
	writeCtx := ctx
	cancel := func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		writeCtx, cancel = context.WithTimeout(ctx, deploymentWriteTimeout)
	}
	defer cancel()
	ticker := time.NewTicker(deploymentRetryDelay)
	defer ticker.Stop()
	var lastErr error
	for {
		message, requestErr := t.connection.RequestWithContext(writeCtx, subject, encoded)
		if requestErr == nil {
			var response deploymentCommandResponse
			if err := json.Unmarshal(message.Data, &response); err != nil {
				return &Error{Operation: "decode Grove " + operation + " command", Err: err}
			}
			if response.Error == "" {
				return nil
			}
			requestErr = errors.New(response.Error)
			if !transientControlStateError(requestErr) {
				return &Error{Operation: "run Grove " + operation + " command", Err: requestErr}
			}
		}
		lastErr = requestErr
		select {
		case <-ticker.C:
		case <-writeCtx.Done():
			return &Error{Operation: "run Grove " + operation + " command", Err: errors.Join(lastErr, writeCtx.Err())}
		}
	}
}

func transientControlStateError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, nats.ErrTimeout) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "context deadline exceeded") ||
		strings.Contains(message, "no response from stream") ||
		strings.Contains(message, "jetstream system temporarily unavailable")
}
