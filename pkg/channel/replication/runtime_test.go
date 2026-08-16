package replication

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

func TestRuntimeOwnsThreeNodeInstallAndQuorumCommit(t *testing.T) {
	t.Parallel()

	router := &runtimeTestRouter{servers: make(map[ch.NodeID]*ExchangeServer)}
	runtimes := make(map[ch.NodeID]*Runtime, 3)
	stores := make(map[ch.NodeID]ReplicaStore, 3)
	for _, node := range []ch.NodeID{1, 2, 3} {
		store, err := NewStoreAdapter(StoreAdapterConfig{
			Factory: channelstore.NewMemoryFactory(), MaxBatchItems: 64, MaxBatchBytes: 4 << 20,
		})
		if err != nil {
			t.Fatalf("NewStoreAdapter(node=%d) error = %v", node, err)
		}
		runtime, err := NewRuntime(RuntimeConfig{
			LocalNode:  node,
			Store:      store,
			Link:       runtimeTestLink{from: node, router: router},
			Goroutines: goruntimeregistry.New(),
		})
		if err != nil {
			t.Fatalf("NewRuntime(node=%d) error = %v", node, err)
		}
		router.register(node, runtime.ExchangeServer())
		runtimes[node] = runtime
		stores[node] = store
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, node := range []ch.NodeID{1, 2, 3} {
			if err := runtimes[node].Close(ctx); err != nil {
				t.Errorf("Runtime.Close(node=%d) error = %v", node, err)
			}
		}
	})

	authority := Authority{
		Key: "1:runtime-quorum", ChannelID: ch.ChannelID{ID: "runtime-quorum", Type: 1},
		ID:     AuthorityID{ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7},
		Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2,
	}
	installed, err := runtimes[1].Log().Install(context.Background(), authority)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if installed.LEO != 0 || installed.HW != 0 || installed.Authority != authority.ID {
		t.Fatalf("Install() = %+v, want quorum-proved empty frontier", installed)
	}

	proposal := Proposal{
		Key: authority.Key, Expected: authority.ID, CommandID: ch.CommandID{31: 41},
		Records: []ch.Record{{
			ID: 101, Epoch: authority.ID.ChannelEpoch, FromUID: "sender", ClientMsgNo: "runtime-101",
			Payload: []byte("payload"), SizeBytes: len("payload"), ServerTimestampMS: 101,
		}},
	}
	receipt, err := runtimes[1].Log().Commit(context.Background(), proposal)
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if receipt.First != 1 || receipt.Last != 1 || receipt.HW != 1 || receipt.CommandID != proposal.CommandID {
		t.Fatalf("Commit() = %+v, want exact range 1", receipt)
	}

	for _, node := range []ch.NodeID{1, 2, 3} {
		waitForRuntimeReplicaLEO(t, stores[node], authority, 1)
	}
}

func TestRuntimeRepairsFollowerGapFromLeaderDurableStore(t *testing.T) {
	t.Parallel()

	router := &runtimeTestRouter{servers: make(map[ch.NodeID]*ExchangeServer), rejectReplicate: make(map[ch.NodeID]int)}
	runtimes := make(map[ch.NodeID]*Runtime, 3)
	stores := make(map[ch.NodeID]ReplicaStore, 3)
	for _, node := range []ch.NodeID{1, 2, 3} {
		store, err := NewStoreAdapter(StoreAdapterConfig{
			Factory: channelstore.NewMemoryFactory(), MaxBatchItems: 64, MaxBatchBytes: 4 << 20,
		})
		if err != nil {
			t.Fatalf("NewStoreAdapter(node=%d) error = %v", node, err)
		}
		runtime, err := NewRuntime(RuntimeConfig{
			LocalNode: node, Store: store, Link: runtimeTestLink{from: node, router: router}, Goroutines: goruntimeregistry.New(),
		})
		if err != nil {
			t.Fatalf("NewRuntime(node=%d) error = %v", node, err)
		}
		router.register(node, runtime.ExchangeServer())
		runtimes[node] = runtime
		stores[node] = store
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, node := range []ch.NodeID{1, 2, 3} {
			if err := runtimes[node].Close(ctx); err != nil {
				t.Errorf("Runtime.Close(node=%d) error = %v", node, err)
			}
		}
	})

	authority := Authority{
		Key: "1:runtime-repair", ChannelID: ch.ChannelID{ID: "runtime-repair", Type: 1},
		ID:     AuthorityID{ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7},
		Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2,
	}
	if _, err := runtimes[1].Log().Install(context.Background(), authority); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	router.rejectNextReplicate(2)
	for index := 0; index < 2; index++ {
		proposal := Proposal{
			Key: authority.Key, Expected: authority.ID, CommandID: ch.CommandID{30: byte(index + 1), 31: 51},
			Records: []ch.Record{{
				ID: uint64(201 + index), Epoch: authority.ID.ChannelEpoch, FromUID: "sender",
				ClientMsgNo: "repair-" + strconv.Itoa(index), Payload: []byte("payload"),
				SizeBytes: len("payload"), ServerTimestampMS: int64(201 + index),
			}},
		}
		if _, err := runtimes[1].Log().Commit(context.Background(), proposal); err != nil {
			t.Fatalf("Commit(%d) error = %v", index, err)
		}
	}
	waitForRuntimeReplicaLEO(t, stores[2], authority, 2)
}

func waitForRuntimeReplicaLEO(t *testing.T, store ReplicaStore, authority Authority, want uint64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		loaded, err := store.Load(context.Background(), LoadBatch{Items: []LoadRequest{{
			ChannelKey: authority.Key, ChannelID: authority.ChannelID,
		}}})
		if err == nil && len(loaded.Items) == 1 && loaded.Items[0].Err == nil && loaded.Items[0].State.LEO == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("replica state = %+v, error %v; want durable LEO %d", loaded, err, want)
		}
		time.Sleep(time.Millisecond)
	}
}

type runtimeTestRouter struct {
	mu              sync.RWMutex
	servers         map[ch.NodeID]*ExchangeServer
	rejectReplicate map[ch.NodeID]int
}

func (r *runtimeTestRouter) rejectNextReplicate(node ch.NodeID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rejectReplicate[node]++
}

func (r *runtimeTestRouter) register(node ch.NodeID, server *ExchangeServer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.servers[node] = server
}

func (r *runtimeTestRouter) exchange(ctx context.Context, from, target ch.NodeID, batch ExchangeBatch) (ExchangeBatchResult, error) {
	r.mu.Lock()
	server := r.servers[target]
	if len(batch.Items) == 1 && batch.Items[0].Kind == ExchangeReplicate && r.rejectReplicate[target] > 0 {
		r.rejectReplicate[target]--
		item := batch.Items[0]
		r.mu.Unlock()
		return ExchangeBatchResult{Version: ExchangeVersion, Items: []ExchangeItemResult{{
			RequestID: item.RequestID, Replicate: ReplicateResult{Status: ReplicateBackpressured},
		}}}, nil
	}
	r.mu.Unlock()
	if server == nil {
		return ExchangeBatchResult{}, ch.ErrNotReady
	}
	return server.Handle(ctx, from, batch)
}

type runtimeTestLink struct {
	from   ch.NodeID
	router *runtimeTestRouter
}

func (l runtimeTestLink) Exchange(ctx context.Context, target ch.NodeID, batch ExchangeBatch) (ExchangeBatchResult, error) {
	return l.router.exchange(ctx, l.from, target, batch)
}
