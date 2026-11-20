//go:build integration

package replication_test

import (
	"context"
	"errors"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

// Duplicate identities retain input-order fencing, and one rejected item must
// not turn a valid sibling into either a failure or a fabricated success.
func TestPromiseBatchKeepsOrderedFencesAndAlignedFailures(t *testing.T) {
	factory := store.NewMessageDBFactory(t.TempDir())
	t.Cleanup(func() {
		if err := factory.Close(); err != nil {
			t.Error(err)
		}
	})
	adapter, err := replication.NewStoreAdapter(replication.StoreAdapterConfig{Factory: factory, MaxBatchItems: 8, MaxBatchBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	request := func(name string, term uint64) replication.LoadRequest {
		id := ch.ChannelID{ID: name, Type: 1}
		return replication.LoadRequest{ChannelKey: ch.ChannelKeyForID(id), ChannelID: id,
			Promise: replication.AuthorityID{ChannelEpoch: 1, LeaderTerm: term, FenceVersion: 1}}
	}
	batch := replication.LoadBatch{Items: []replication.LoadRequest{
		request("ordered", 2), request("ordered", 3), request("ordered", 2),
		{}, request("sibling", 1), request("ordered", 3),
	}}
	result, err := adapter.Load(context.Background(), batch)
	if err != nil || len(result.Items) != len(batch.Items) {
		t.Fatalf("batch result invalid: %v", err)
	}
	for i, expected := range []error{nil, nil, ch.ErrStaleMeta, ch.ErrInvalidConfig, nil, nil} {
		if !errors.Is(result.Items[i].Err, expected) {
			t.Fatalf("item %d: got %v, want %v", i, result.Items[i].Err, expected)
		}
	}
	channel, err := factory.ChannelStore(batch.Items[0].ChannelKey, batch.Items[0].ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	promise, present, readErr := channel.(store.AuthorityPromiser).AuthorityPromise(context.Background())
	closeErr := channel.Close()
	if readErr != nil || closeErr != nil || !present || promise != (store.AuthorityFence{ChannelEpoch: 1, LeaderTerm: 3, FenceVersion: 1}) {
		t.Fatal("ordered batch lost its highest durable promise")
	}
	if leases := factory.ChannelEntryMetricsSnapshot(); leases.ActiveEntries != 0 || leases.OutstandingLeases != 0 || leases.BackgroundPins != 0 {
		t.Fatal("batch retained storage ownership")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := adapter.Load(ctx, batch); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled admission: %v", err)
	}
	if err := factory.Close(); err != nil {
		t.Fatal(err)
	}
	result, err = adapter.Load(context.Background(), replication.LoadBatch{Items: []replication.LoadRequest{request("closed", 4)}})
	if err != nil || len(result.Items) != 1 || !errors.Is(result.Items[0].Err, ch.ErrClosed) {
		t.Fatal("closed factory accepted a promise")
	}
	t.Log(`{"ordered_fences":true,"aligned_failures":true,"cancellation":true,"closed_rejected":true,"leases_released":true}`)
}
