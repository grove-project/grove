package systemnats

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

const (
	controlStateBootstrapReplicas = 1
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

func controlStateReplicaCount(records map[string]MembershipRecord) int {
	active := 0
	for _, record := range records {
		if !record.Leaving {
			active++
		}
	}
	if active < controlStateBootstrapReplicas {
		return controlStateBootstrapReplicas
	}
	if active > MembershipReplicas {
		return MembershipReplicas
	}
	return active
}

func reconcileControlStateReplicas(
	ctx context.Context,
	js jetstream.JetStream,
	records map[string]MembershipRecord,
	allowShrink bool,
) error {
	replicas := controlStateReplicaCount(records)
	for _, bucket := range []string{
		MembershipBucket,
		PlacementBucket,
		DesiredBucket,
		DeploymentBucket,
	} {
		operationCtx, cancel := context.WithTimeout(ctx, controlStateOperationTimeout)
		kv, err := js.KeyValue(operationCtx, bucket)
		if errors.Is(err, jetstream.ErrBucketNotFound) {
			cancel()
			continue
		}
		if err != nil {
			cancel()
			return fmt.Errorf("open %s bucket for replica reconciliation: %w", bucket, err)
		}
		status, err := kv.Status(operationCtx)
		if err != nil {
			cancel()
			return fmt.Errorf("read %s bucket replica configuration: %w", bucket, err)
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
			return fmt.Errorf("resize %s bucket to %d replicas: %w", bucket, replicas, err)
		}
		cancel()
	}
	return nil
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
		lastErr = reconcileControlStateReplicas(ctx, js, records, allowShrink)
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
	for _, bucket := range []string{
		MembershipBucket,
		PlacementBucket,
		DesiredBucket,
		DeploymentBucket,
	} {
		operationCtx, cancel := context.WithTimeout(ctx, controlStateOperationTimeout)
		stream, err := js.Stream(operationCtx, "KV_"+bucket)
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
