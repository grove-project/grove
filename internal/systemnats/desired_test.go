package systemnats_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/grove-project/grove/internal/systemnats"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func TestDesiredDeploymentConvergesThroughControlAPI(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	servers, transports := startPlacementCluster(t, ctx)
	desired := make([]*systemnats.Desired, len(transports))
	runCtx, cancelRun := context.WithCancel(t.Context())
	var runs sync.WaitGroup
	for i := range transports {
		desired[i] = systemnats.NewDesired()
		nodeID := fmt.Sprintf("node-%d", i+1)
		if err := transports[i].ServeDesired(ctx, nodeID, desired[i]); err != nil {
			t.Fatal(err)
		}
		runs.Add(1)
		go func() {
			defer runs.Done()
			if err := desired[i].Run(runCtx, transports[i]); err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("desired observer: %v", err)
			}
		}()
	}
	t.Cleanup(func() { cancelRun(); runs.Wait() })
	want := systemnats.DesiredDeployment{
		ApplicationID:  "grove-shop",
		Version:        "current",
		ArtifactDigest: testDigest("a"),
		Components: []systemnats.DesiredComponent{
			{ServiceID: 1, NodeID: "node-1"},
			{ServiceID: 2, NodeID: "node-2"},
		},
	}
	if err := waitForDesired(ctx, transports[0], "node-1", nil); err != nil {
		t.Fatal(err)
	}
	if err := transports[2].PutDesired(ctx, "node-1", want); err != nil {
		t.Fatal(err)
	}
	for i := range transports {
		if err := waitForDesired(ctx, transports[i], fmt.Sprintf("node-%d", i+1), []systemnats.DesiredDeployment{want}); err != nil {
			t.Fatal(err)
		}
	}
	connection, err := nats.Connect(servers[0].URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	kv, err := js.KeyValue(ctx, systemnats.DesiredBucket)
	if err != nil {
		t.Fatal(err)
	}
	status, err := kv.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Config().Replicas != systemnats.DesiredReplicas {
		t.Errorf("desired replicas = %d; want %d", status.Config().Replicas, systemnats.DesiredReplicas)
	}
}

func waitForDesired(ctx context.Context, transport *systemnats.Transport, nodeID string, want []systemnats.DesiredDeployment) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		view, err := transport.RequestDesired(ctx, nodeID)
		if err == nil && view.Ready && slices.EqualFunc(view.Deployments, want, func(a, b systemnats.DesiredDeployment) bool {
			return a.ApplicationID == b.ApplicationID && a.Version == b.Version && a.ArtifactDigest == b.ArtifactDigest && slices.Equal(a.Components, b.Components)
		}) {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		}
	}
}

func TestDesiredDeploymentRejectsInvalidRecords(t *testing.T) {
	invalid := []systemnats.DesiredDeployment{
		{Version: "current", ArtifactDigest: testDigest("a"), Components: []systemnats.DesiredComponent{{ServiceID: 1, NodeID: "node-1"}}},
		{ApplicationID: "grove-shop", ArtifactDigest: testDigest("a"), Components: []systemnats.DesiredComponent{{ServiceID: 1, NodeID: "node-1"}}},
		{ApplicationID: "grove-shop", Version: "current", ArtifactDigest: testDigest("a")},
		{ApplicationID: "grove-shop", Version: "current", ArtifactDigest: testDigest("a"), Components: []systemnats.DesiredComponent{{NodeID: "node-1"}}},
		{ApplicationID: "grove-shop", Version: "current", Components: []systemnats.DesiredComponent{{ServiceID: 1, NodeID: "node-1"}}},
	}
	for _, deployment := range invalid {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := systemnats.NewDesired().Put(ctx, nil, deployment); !errors.Is(err, systemnats.ErrDesiredDeploymentInvalid) {
			t.Errorf("Put(%#v) error = %v; want %v", deployment, err, systemnats.ErrDesiredDeploymentInvalid)
		}
	}
}
