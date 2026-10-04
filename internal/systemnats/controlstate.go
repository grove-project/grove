package systemnats

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/grove-project/grove/internal/controlplane"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	controlStateBootstrapReplicas = controlplane.MinControlStateReplicas
	controlStateOperationTimeout  = time.Second
)

func membershipKeyValueConfig(replicas int) jetstream.KeyValueConfig {
	return jetstream.KeyValueConfig{
		Bucket:      MembershipBucket,
		Description: "Authoritative Grove node membership",
		History:     1,
		Storage:     jetstream.FileStorage,
		Replicas:    replicas,
	}
}

func placementKeyValueConfig(replicas int) jetstream.KeyValueConfig {
	return jetstream.KeyValueConfig{
		Bucket:      PlacementBucket,
		Description: "Authoritative Grove service placement",
		History:     1,
		Storage:     jetstream.FileStorage,
		Replicas:    replicas,
	}
}

func desiredKeyValueConfig(replicas int) jetstream.KeyValueConfig {
	return jetstream.KeyValueConfig{
		Bucket:      DesiredBucket,
		Description: "Authoritative Grove desired deployments",
		History:     1,
		Storage:     jetstream.FileStorage,
		Replicas:    replicas,
	}
}

func deploymentKeyValueConfig(replicas int) jetstream.KeyValueConfig {
	return jetstream.KeyValueConfig{
		Bucket:      DeploymentBucket,
		Description: "Authoritative Grove deployment artifacts and rollouts",
		History:     1,
		Storage:     jetstream.FileStorage,
		Replicas:    replicas,
	}
}

// controlStateBuckets are the replicated control-state buckets. The handler
// bucket exists only once an application declares handler-level placement,
// and the files bucket only once an application writes a Grove file.
var controlStateBuckets = []string{
	MembershipBucket,
	PlacementBucket,
	DesiredBucket,
	DeploymentBucket,
	HandlerBucket,
	FilesBucket,
}

// optionalControlStream reports whether a missing stream for bucket is
// expected rather than a failure.
func optionalControlStream(bucket string, err error) bool {
	return (bucket == HandlerBucket || bucket == FilesBucket) && errors.Is(err, jetstream.ErrStreamNotFound)
}

func openOrCreateKeyValue(
	ctx context.Context,
	js jetstream.JetStream,
	cfg jetstream.KeyValueConfig,
) (jetstream.KeyValue, error) {
	kv, err := js.KeyValue(ctx, cfg.Bucket)
	if err == nil {
		return kv, nil
	}
	if !errors.Is(err, jetstream.ErrBucketNotFound) {
		return nil, err
	}
	cfg.Replicas = controlStateBootstrapReplicas
	kv, err = js.CreateKeyValue(ctx, cfg)
	if err == nil {
		return kv, nil
	}
	if errors.Is(err, jetstream.ErrBucketExists) {
		return js.KeyValue(ctx, cfg.Bucket)
	}
	return nil, err
}

// reconcileControlStateReplicas resizes every control-state bucket to the
// replica count records calls for. It reports changed=true when some bucket's
// configured replica count was not already at that target (whether this call
// performed the resize itself or observed that a concurrent Grovlet already
// had), so callers can tell a genuine resize apart from a no-op reconcile.
func reconcileControlStateReplicas(
	ctx context.Context,
	js jetstream.JetStream,
	records map[string]MembershipRecord,
	allowShrink bool,
) (changed bool, err error) {
	replicas := controlplane.ControlStateReplicas(records)
	for _, bucket := range controlStateBuckets {
		operationCtx, cancel := context.WithTimeout(ctx, controlStateOperationTimeout)
		kv, err := js.KeyValue(operationCtx, bucket)
		if errors.Is(err, jetstream.ErrBucketNotFound) {
			cancel()
			continue
		}
		if err != nil {
			cancel()
			return changed, fmt.Errorf("open %s bucket for replica reconciliation: %w", bucket, err)
		}
		status, err := kv.Status(operationCtx)
		if err != nil {
			cancel()
			return changed, fmt.Errorf("read %s bucket replica configuration: %w", bucket, err)
		}
		cfg := status.Config()
		if cfg.Replicas == replicas {
			cancel()
			continue
		}
		if !allowShrink && cfg.Replicas > replicas {
			cancel()
			continue
		}
		changed = true
		cfg.Replicas = replicas
		if _, err := js.UpdateKeyValue(operationCtx, cfg); err != nil {
			// Another Grovlet may have completed the same idempotent resize.
			cancel()
			verifyCtx, verifyCancel := context.WithTimeout(ctx, controlStateOperationTimeout)
			current, currentErr := js.KeyValue(verifyCtx, bucket)
			if currentErr == nil {
				currentStatus, statusErr := current.Status(verifyCtx)
				if statusErr == nil && currentStatus.Config().Replicas == replicas {
					verifyCancel()
					continue
				}
			}
			verifyCancel()
			return changed, fmt.Errorf("resize %s bucket to %d replicas: %w", bucket, replicas, err)
		}
		cancel()
	}
	return changed, nil
}

func waitForControlStateReplicas(
	ctx context.Context,
	js jetstream.JetStream,
	records map[string]MembershipRecord,
	allowShrink bool,
) error {
	ticker := time.NewTicker(membershipRetryDelay)
	defer ticker.Stop()
	var lastErr error
	for {
		_, lastErr = reconcileControlStateReplicas(ctx, js, records, allowShrink)
		if lastErr == nil {
			lastErr = controlStateStreamsCurrent(ctx, js)
		}
		if lastErr == nil {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return errors.Join(lastErr, ctx.Err())
		}
	}
}

func controlStateStreamsCurrent(ctx context.Context, js jetstream.JetStream) error {
	for _, bucket := range controlStateBuckets {
		operationCtx, cancel := context.WithTimeout(ctx, controlStateOperationTimeout)
		stream, err := js.Stream(operationCtx, "KV_"+bucket)
		if optionalControlStream(bucket, err) {
			cancel()
			continue
		}
		if err == nil {
			var info *jetstream.StreamInfo
			info, err = stream.Info(operationCtx)
			if err == nil && !controlStreamCurrent(info.Cluster) {
				err = errors.New("control stream replicas are not current")
			}
		}
		cancel()
		if err != nil {
			return fmt.Errorf("wait for %s replicas: %w", bucket, err)
		}
	}
	return nil
}

func controlStreamCurrent(cluster *jetstream.ClusterInfo) bool {
	if cluster == nil || cluster.Leader == "" {
		return false
	}
	for _, replica := range cluster.Replicas {
		if replica.Offline || !replica.Current {
			return false
		}
	}
	return true
}
