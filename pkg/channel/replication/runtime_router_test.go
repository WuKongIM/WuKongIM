package replication

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

func TestRuntimeRepairFixtureRejectsCoalescedReplicas(t *testing.T) {
	router, store := newRuntimeRepairFixtureRouter(t)
	executor := &manualPeerExecutor{}
	batcher, err := newManualPeerBatcher(peerBatcherConfig{
		Link: runtimeTestLink{from: 1, router: router}, Executor: executor,
		OwnerContext: context.Background(), ExchangeTimeout: time.Second,
		MaxBatchItems: 4, MaxBatchBytes: 4096,
		MaxQueuedItems: 8, MaxQueuedBytes: 8192, MaxTargetQueuedItems: 4, MaxTargetQueuedBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	repairs := &recordingFollowerRepairSink{}
	dispatcher := &batchingDurabilityDispatcher{peers: batcher, repairs: repairs}
	requests := []ReplicateRequest{
		testReplicateRequest(t, "1:fixture-a", "fixture-a", 1, []byte("first")),
		testReplicateRequest(t, "1:fixture-b", "fixture-b", 2, []byte("second")),
	}
	for range requests {
		router.rejectNextReplicate(2)
	}
	// Queue both trailing writes before execution so legal coalescing is deterministic.
	for round := 0; round < 2; round++ {
		completions := make([]durabilityCompletion, 0, len(requests))
		for _, request := range requests {
			proposal := durableProposal{
				channelKey: request.ChannelKey, channelID: request.ChannelID, leader: request.Leader,
				first: 1, last: 1, manifest: request.Manifest, records: request.Records,
			}
			if err := dispatcher.submitReplicaDeferred(context.Background(), 2, proposal, func(got durabilityCompletion) {
				completions = append(completions, got)
			}); err != nil {
				t.Fatal(err)
			}
		}
		if err := batcher.flushDeferred(); err != nil {
			t.Fatal(err)
		}
		for executor.Len() > 0 {
			executor.RunNext()
		}
		if len(completions) != len(requests) {
			t.Fatalf("round %d completions = %d, want %d", round, len(completions), len(requests))
		}
		for index, got := range completions {
			if round == 0 && !errors.Is(got.err, ch.ErrBackpressured) {
				t.Errorf("coalesced replica %d error = %v, want injected backpressure", index, got.err)
			}
			if round == 1 && (got.err != nil || !got.outcome.Durable()) {
				t.Errorf("retry replica %d completion = %+v, want durable after one consumed fault", index, got)
			}
		}
		for _, request := range requests {
			loaded, err := store.Load(context.Background(), LoadBatch{Items: []LoadRequest{{ChannelKey: request.ChannelKey, ChannelID: request.ChannelID}}})
			if err != nil || len(loaded.Items) != 1 || loaded.Items[0].Err != nil || loaded.Items[0].State.LEO != uint64(round) {
				t.Errorf("round %d follower %s = %+v, error %v; want LEO %d", round, request.ChannelKey, loaded, err, round)
			}
		}
		gotRepairs := repairs.snapshot()
		if len(gotRepairs) != len(requests) {
			t.Fatalf("round %d repair obligations = %d, want %d", round, len(gotRepairs), len(requests))
		}
		for index, repair := range gotRepairs {
			if repair.channelKey != requests[index].ChannelKey || repair.follower != 2 || repair.needFrom != 1 {
				t.Errorf("repair %d = %+v, want exact rejected channel/follower", index, repair)
			}
		}
	}
}

func TestRuntimeRepairFixturePreservesUnrejectedBatchItems(t *testing.T) {
	router, store := newRuntimeRepairFixtureRouter(t)
	first := testReplicateRequest(t, "1:fixture-rejected", "fixture-rejected", 1, []byte("rejected"))
	second := testReplicateRequest(t, "1:fixture-accepted", "fixture-accepted", 2, []byte("accepted"))
	probe := ProbeRequest{ChannelKey: "1:fixture-probe", ChannelID: ch.ChannelID{ID: "fixture-probe", Type: 1}, Leader: 1, Follower: 2}
	router.rejectNextReplicate(2)
	result, err := router.exchange(context.Background(), 1, 2, ExchangeBatch{Version: ExchangeVersion, Items: []ExchangeItem{
		{RequestID: 11, Kind: ExchangeReplicate, Replicate: &first},
		{RequestID: 12, Kind: ExchangeProbe, Probe: &probe},
		{RequestID: 13, Kind: ExchangeReplicate, Replicate: &second},
	}})
	if err != nil || result.Version != ExchangeVersion || len(result.Items) != 3 {
		t.Fatalf("mixed exchange = %+v, error %v", result, err)
	}
	if result.Items[0].RequestID != 11 || result.Items[0].Replicate.Status != ReplicateBackpressured ||
		result.Items[1].RequestID != 12 || !reflect.DeepEqual(result.Items[1].Probe.Proof, probeProofFor(probe)) ||
		result.Items[2].RequestID != 13 || result.Items[2].Replicate.Status != ReplicateDurable {
		t.Fatalf("mixed results = %+v, want one rejection and unchanged aligned probe/replication", result.Items)
	}
	for index, request := range []ReplicateRequest{first, second} {
		loaded, err := store.Load(context.Background(), LoadBatch{Items: []LoadRequest{{ChannelKey: request.ChannelKey, ChannelID: request.ChannelID}}})
		if err != nil || len(loaded.Items) != 1 || loaded.Items[0].State.LEO != uint64(index) {
			t.Fatalf("follower %s = %+v, error %v; want LEO %d", request.ChannelKey, loaded, err, index)
		}
	}
}

func newRuntimeRepairFixtureRouter(t *testing.T) (*runtimeTestRouter, ReplicaStore) {
	t.Helper()
	store, err := NewStoreAdapter(StoreAdapterConfig{Factory: channelstore.NewMemoryFactory(), MaxBatchItems: 4, MaxBatchBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewExchangeServer(ExchangeServerConfig{LocalNode: 2, Store: store, MaxBatchItems: 4, MaxBatchBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	return &runtimeTestRouter{servers: map[ch.NodeID]*ExchangeServer{2: server}, rejectReplicate: make(map[ch.NodeID]int)}, store
}
