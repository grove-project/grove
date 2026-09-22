package systemnats

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/grove-project/grove"
	"github.com/nats-io/nats.go/jetstream"
)

func TestRefreshPlacementRecordsDiscoversAuthoritativeKeysFromEmptyView(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	server, err := StartClusterServer(ctx, ClusterConfig{
		Name:              "node-1",
		Host:              "127.0.0.1",
		RouteHost:         "127.0.0.1",
		JetStreamStoreDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Shutdown)
	transport, err := Connect(ctx, server.URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.Close)
	js, err := jetstream.New(transport.connection)
	if err != nil {
		t.Fatal(err)
	}
	kv, err := js.CreateKeyValue(ctx, placementKeyValueConfig(1))
	if err != nil {
		t.Fatal(err)
	}
	want := PlacementRecord{
		ServiceID:         2,
		NodeID:            "node-4",
		InvocationSubject: "_GROVE.system.application.node-4.service.2",
		ArtifactDigest:    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Put(ctx, PlacementKey(want.ServiceID), encoded); err != nil {
		t.Fatal(err)
	}

	got, err := refreshPlacementRecords(ctx, kv, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[want.ServiceID] != want {
		t.Fatalf("refreshed placement = %#v; want %#v", got, want)
	}
}

func TestRefreshPlacementRecordsRetainsKnownKeysMissingFromListSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	server, err := StartClusterServer(ctx, ClusterConfig{
		Name:              "node-1",
		Host:              "127.0.0.1",
		RouteHost:         "127.0.0.1",
		JetStreamStoreDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Shutdown)
	transport, err := Connect(ctx, server.URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.Close)
	js, err := jetstream.New(transport.connection)
	if err != nil {
		t.Fatal(err)
	}
	kv, err := js.CreateKeyValue(ctx, placementKeyValueConfig(1))
	if err != nil {
		t.Fatal(err)
	}
	records := []PlacementRecord{
		{
			ServiceID:         1,
			NodeID:            "node-4",
			InvocationSubject: "_GROVE.system.application.node-4.service.1",
			ArtifactDigest:    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			ServiceID:         2,
			NodeID:            "node-5",
			InvocationSubject: "_GROVE.system.application.node-5.service.2",
			ArtifactDigest:    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	}
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := kv.Put(ctx, PlacementKey(record.ServiceID), encoded); err != nil {
			t.Fatal(err)
		}
	}
	partial := partialKeysKeyValue{
		KeyValue: kv,
		keys:     []string{PlacementKey(records[1].ServiceID)},
	}
	got, err := refreshPlacementRecords(ctx, partial, map[grove.ServiceID]PlacementRecord{
		records[0].ServiceID: records[0],
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range records {
		if got[want.ServiceID] != want {
			t.Fatalf("refreshed placement = %#v; missing %#v", got, want)
		}
	}
}

type partialKeysKeyValue struct {
	jetstream.KeyValue
	keys []string
}

func (p partialKeysKeyValue) Keys(context.Context, ...jetstream.WatchOpt) ([]string, error) {
	return append([]string(nil), p.keys...), nil
}
