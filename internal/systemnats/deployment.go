package systemnats

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

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

	deploymentArtifactKeyPrefix   = "artifacts."
	deploymentRolloutKeyPrefix    = "rollouts."
	deploymentSubjectRoot         = "_GROVE.system.deployments."
	deploymentArtifactCommandRoot = "_GROVE.system.deployment_artifacts."
	deploymentRolloutCommandRoot  = "_GROVE.system.rollouts."
	deploymentRetryDelay          = 50 * time.Millisecond
)

var (
	// ErrDeploymentArtifactInvalid is returned for incomplete or malformed
	// immutable artifact metadata.
	ErrDeploymentArtifactInvalid = errors.New("grove deployment artifact is invalid")
	// ErrDeploymentArtifactChanged is returned when an existing artifact digest
	// is associated with different metadata.
	ErrDeploymentArtifactChanged = errors.New("grove deployment artifact metadata changed")
	// ErrRolloutInvalid is returned for incomplete or inconsistent rollout
	// state.
	ErrRolloutInvalid = errors.New("grove rollout is invalid")
	// ErrRolloutGeneration is returned when a write does not create the next
	// rollout generation.
	ErrRolloutGeneration = errors.New("grove rollout generation is not next")
	// ErrRolloutChanged is returned when another writer changes a rollout before
	// the requested generation is committed.
	ErrRolloutChanged = errors.New("grove rollout changed")
	// ErrDeploymentsRequired is returned when an operation has no deployment
	// state view.
	ErrDeploymentsRequired   = errors.New("grove deployment view is required")
	errDeploymentWatchClosed = errors.New("grove deployment watch closed")
)

// RolloutPhase is the durable operator-facing phase of a rollout or node.
type RolloutPhase string

const (
	// RolloutActive means the current artifact is authoritative with no pending
	// successor.
	RolloutActive RolloutPhase = "active"
	// RolloutPending means a candidate is recorded but has not been launched or
	// handed ownership.
	RolloutPending RolloutPhase = "pending"
	// RolloutCandidateHealthy means every candidate runtime reported readiness
	// for the exact candidate artifact while the current artifact remains active.
	RolloutCandidateHealthy RolloutPhase = "candidate-healthy"
	// RolloutSwitching means candidate health passed and authoritative service
	// placement is being moved to the candidate artifact.
	RolloutSwitching RolloutPhase = "switching"
)

// DeploymentArtifact is immutable identity metadata for one configured Grove
// application artifact. Configuration bytes remain embedded in the artifact.
type DeploymentArtifact struct {
	ApplicationID  string `json:"application_id"`
	CodeVersion    string `json:"code_version"`
	CodeDigest     string `json:"code_digest"`
	ConfigRevision string `json:"config_revision"`
	ConfigDigest   string `json:"config_digest"`
	ArtifactDigest string `json:"artifact_digest"`
	ClusterID      string `json:"cluster_id"`
	NodeClass      string `json:"node_class,omitempty"`
	NodeZone       string `json:"node_zone,omitempty"`
}

// RolloutNodeProgress records one node's immutable current/candidate identity
// and progress. Later handoff tasks advance the phase and failure fields.
type RolloutNodeProgress struct {
	NodeID                  string       `json:"node_id"`
	CurrentArtifactDigest   string       `json:"current_artifact_digest"`
	CandidateArtifactDigest string       `json:"candidate_artifact_digest,omitempty"`
	Phase                   RolloutPhase `json:"phase"`
	Failure                 string       `json:"failure,omitempty"`
}

// Rollout records one durable generation for an application and cluster.
type Rollout struct {
	ApplicationID           string                `json:"application_id"`
	ClusterID               string                `json:"cluster_id"`
	RolloutID               string                `json:"rollout_id"`
	Generation              uint64                `json:"generation"`
	CurrentArtifactDigest   string                `json:"current_artifact_digest"`
	CandidateArtifactDigest string                `json:"candidate_artifact_digest,omitempty"`
	Phase                   RolloutPhase          `json:"phase"`
	Nodes                   []RolloutNodeProgress `json:"nodes"`
}

// DeploymentView is one Grovlet's watcher-derived view of artifact and rollout
// control state.
type DeploymentView struct {
	Ready     bool                 `json:"ready"`
	Artifacts []DeploymentArtifact `json:"artifacts"`
	Rollouts  []Rollout            `json:"rollouts"`
	Error     string               `json:"error,omitempty"`
}

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
	digest, err := sha256DigestSuffix(artifactDigest)
	if err != nil {
		return "", err
	}
	return deploymentArtifactKeyPrefix + digest, nil
}

// DeploymentRolloutKey returns the authoritative KV key for applicationID.
func DeploymentRolloutKey(applicationID string) (string, error) {
	if !validApplicationID(applicationID) {
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
	kv, err := js.CreateOrUpdateKeyValue(setupCtx, jetstream.KeyValueConfig{
		Bucket: DeploymentBucket, Description: "Authoritative Grove deployment artifacts and rollouts",
		History: 1, Storage: jetstream.FileStorage, Replicas: DeploymentReplicas,
	})
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
	initialized := false
	for {
		select {
		case entry, ok := <-watcher.Updates():
			if !ok {
				return errDeploymentWatchClosed
			}
			if entry == nil {
				initialized = true
				d.setReady(artifacts, rollouts)
				continue
			}
			if err := applyDeploymentEntry(entry, artifacts, rollouts); err != nil {
				return err
			}
			if initialized {
				d.setReady(artifacts, rollouts)
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
		if _, err := sha256DigestSuffix(digest); err != nil {
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
		if !validApplicationID(applicationID) {
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
	if err := validateDeploymentArtifact(artifact); err != nil {
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

// PutRollout commits the first or next rollout generation after verifying all
// referenced immutable artifacts.
func (d *Deployments) PutRollout(ctx context.Context, transport *Transport, rollout Rollout) error {
	rollout, err := validateRollout(rollout)
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
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		if rollout.Generation != 1 {
			return &Error{Operation: "create rollout", Err: ErrRolloutGeneration}
		}
		if _, err := kv.Create(ctx, key, encoded); err != nil {
			if errors.Is(err, jetstream.ErrKeyExists) {
				return &Error{Operation: "create rollout", Err: ErrRolloutChanged}
			}
			return &Error{Operation: "create rollout", Err: err}
		}
		return nil
	}
	if err != nil {
		return &Error{Operation: "read rollout", Err: err}
	}
	observed, err := decodeRollout(key, entry.Value())
	if err != nil {
		return &Error{Operation: "read rollout", Err: err}
	}
	if equalRollout(observed, rollout) {
		return nil
	}
	if rollout.Generation != observed.Generation+1 {
		return &Error{Operation: "advance rollout", Err: ErrRolloutGeneration}
	}
	if rollout.ClusterID != observed.ClusterID || rollout.RolloutID == observed.RolloutID || !validRolloutTransition(observed, rollout) {
		return &Error{Operation: "advance rollout", Err: ErrRolloutInvalid}
	}
	if _, err := kv.Update(ctx, key, encoded, entry.Revision()); err != nil {
		if errors.Is(err, jetstream.ErrKeyExists) {
			return &Error{Operation: "advance rollout", Err: ErrRolloutChanged}
		}
		return &Error{Operation: "advance rollout", Err: err}
	}
	return nil
}

func validRolloutTransition(current, next Rollout) bool {
	sameArtifacts := next.CurrentArtifactDigest == current.CurrentArtifactDigest && next.CandidateArtifactDigest == current.CandidateArtifactDigest
	sameTargets := slices.EqualFunc(current.Nodes, next.Nodes, func(a, b RolloutNodeProgress) bool {
		return a.NodeID == b.NodeID
	})
	switch current.Phase {
	case RolloutActive:
		return next.Phase == RolloutPending && sameTargets && next.CurrentArtifactDigest == current.CurrentArtifactDigest && next.CandidateArtifactDigest != ""
	case RolloutPending:
		return next.Phase == RolloutCandidateHealthy && sameArtifacts && sameTargets
	case RolloutCandidateHealthy:
		return next.Phase == RolloutSwitching && sameArtifacts && sameTargets
	case RolloutSwitching:
		return next.Phase == RolloutActive && sameTargets && next.CurrentArtifactDigest == current.CandidateArtifactDigest && next.CandidateArtifactDigest == ""
	default:
		return false
	}
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
	if current.ApplicationID != rollout.ApplicationID || current.ClusterID != rollout.ClusterID {
		return ErrRolloutInvalid
	}
	if rollout.CandidateArtifactDigest == "" {
		return nil
	}
	candidate, err := getDeploymentArtifact(ctx, kv, rollout.CandidateArtifactDigest)
	if err != nil {
		return err
	}
	if candidate.ApplicationID != rollout.ApplicationID || candidate.ClusterID != rollout.ClusterID {
		return ErrRolloutInvalid
	}
	return nil
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

func validateDeploymentArtifact(artifact DeploymentArtifact) error {
	if !validApplicationID(artifact.ApplicationID) || artifact.CodeVersion == "" || artifact.ConfigRevision == "" || artifact.ClusterID == "" {
		return ErrDeploymentArtifactInvalid
	}
	for _, digest := range []string{artifact.CodeDigest, artifact.ConfigDigest, artifact.ArtifactDigest} {
		if _, err := sha256DigestSuffix(digest); err != nil {
			return ErrDeploymentArtifactInvalid
		}
	}
	return nil
}

func validateRollout(rollout Rollout) (Rollout, error) {
	if !validApplicationID(rollout.ApplicationID) || rollout.ClusterID == "" || rollout.RolloutID == "" || rollout.Generation == 0 || rollout.Phase == "" || len(rollout.Nodes) == 0 {
		return Rollout{}, ErrRolloutInvalid
	}
	if _, err := sha256DigestSuffix(rollout.CurrentArtifactDigest); err != nil {
		return Rollout{}, ErrRolloutInvalid
	}
	if rollout.CandidateArtifactDigest != "" {
		if _, err := sha256DigestSuffix(rollout.CandidateArtifactDigest); err != nil || rollout.CandidateArtifactDigest == rollout.CurrentArtifactDigest {
			return Rollout{}, ErrRolloutInvalid
		}
	}
	switch rollout.Phase {
	case RolloutActive:
		if rollout.CandidateArtifactDigest != "" {
			return Rollout{}, ErrRolloutInvalid
		}
	case RolloutPending, RolloutCandidateHealthy, RolloutSwitching:
		if rollout.CandidateArtifactDigest == "" {
			return Rollout{}, ErrRolloutInvalid
		}
	default:
		return Rollout{}, ErrRolloutInvalid
	}
	nodes := append([]RolloutNodeProgress(nil), rollout.Nodes...)
	seen := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if node.NodeID == "" || node.CurrentArtifactDigest != rollout.CurrentArtifactDigest || node.CandidateArtifactDigest != rollout.CandidateArtifactDigest || node.Phase != rollout.Phase {
			return Rollout{}, ErrRolloutInvalid
		}
		if _, exists := seen[node.NodeID]; exists {
			return Rollout{}, ErrRolloutInvalid
		}
		seen[node.NodeID] = struct{}{}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	rollout.Nodes = nodes
	return rollout, nil
}

func validApplicationID(applicationID string) bool {
	return applicationID != "" && !strings.Contains(applicationID, ".")
}

func sha256DigestSuffix(digest string) (string, error) {
	const prefix = "sha256:"
	if !strings.HasPrefix(digest, prefix) || len(digest) != len(prefix)+64 {
		return "", ErrDeploymentArtifactInvalid
	}
	suffix := strings.TrimPrefix(digest, prefix)
	if suffix != strings.ToLower(suffix) {
		return "", ErrDeploymentArtifactInvalid
	}
	if _, err := hex.DecodeString(suffix); err != nil {
		return "", ErrDeploymentArtifactInvalid
	}
	return suffix, nil
}

func validSHA256Digest(digest string) bool {
	_, err := sha256DigestSuffix(digest)
	return err == nil
}

func decodeDeploymentArtifact(key string, value []byte) (DeploymentArtifact, error) {
	var artifact DeploymentArtifact
	if err := json.Unmarshal(value, &artifact); err != nil {
		return DeploymentArtifact{}, fmt.Errorf("decode deployment artifact %q: %w", key, err)
	}
	if err := validateDeploymentArtifact(artifact); err != nil {
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
	rollout, err := validateRollout(rollout)
	if err != nil {
		return Rollout{}, fmt.Errorf("validate rollout %q: %w", key, err)
	}
	want, _ := DeploymentRolloutKey(rollout.ApplicationID)
	if key != want {
		return Rollout{}, fmt.Errorf("validate rollout key %q: %w", key, ErrRolloutInvalid)
	}
	return rollout, nil
}

func equalRollout(a, b Rollout) bool {
	return a.ApplicationID == b.ApplicationID && a.ClusterID == b.ClusterID && a.RolloutID == b.RolloutID && a.Generation == b.Generation && a.CurrentArtifactDigest == b.CurrentArtifactDigest && a.CandidateArtifactDigest == b.CandidateArtifactDigest && a.Phase == b.Phase && slices.Equal(a.Nodes, b.Nodes)
}

// Snapshot returns an isolated, deterministically ordered deployment view.
func (d *Deployments) Snapshot() DeploymentView {
	d.mu.RLock()
	defer d.mu.RUnlock()
	artifacts := make([]DeploymentArtifact, len(d.view.Artifacts))
	copy(artifacts, d.view.Artifacts)
	rollouts := make([]Rollout, len(d.view.Rollouts))
	for i, rollout := range d.view.Rollouts {
		rollouts[i] = rollout
		rollouts[i].Nodes = append([]RolloutNodeProgress(nil), rollout.Nodes...)
	}
	return DeploymentView{Ready: d.view.Ready, Artifacts: artifacts, Rollouts: rollouts, Error: d.view.Error}
}

func (d *Deployments) setReady(artifacts map[string]DeploymentArtifact, rollouts map[string]Rollout) {
	view := DeploymentView{
		Ready:     true,
		Artifacts: make([]DeploymentArtifact, 0, len(artifacts)),
		Rollouts:  make([]Rollout, 0, len(rollouts)),
	}
	for _, artifact := range artifacts {
		view.Artifacts = append(view.Artifacts, artifact)
	}
	for _, rollout := range rollouts {
		rollout.Nodes = append([]RolloutNodeProgress(nil), rollout.Nodes...)
		view.Rollouts = append(view.Rollouts, rollout)
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
	message, err := t.connection.RequestWithContext(ctx, subject, encoded)
	if err != nil {
		return &Error{Operation: "request Grove " + operation + " command", Err: err}
	}
	var response deploymentCommandResponse
	if err := json.Unmarshal(message.Data, &response); err != nil {
		return &Error{Operation: "decode Grove " + operation + " command", Err: err}
	}
	if response.Error != "" {
		return &Error{Operation: "run Grove " + operation + " command", Err: errors.New(response.Error)}
	}
	return nil
}
