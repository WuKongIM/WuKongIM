package replication

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

func TestRuntimeDefaultBatchItemsUseHighRateWireBound(t *testing.T) {
	t.Parallel()

	cfg := normalizeRuntimeConfig(RuntimeConfig{})
	if cfg.BatchItems != MaxExchangeBatchItems {
		t.Fatalf("default batch items = %d, want wire bound %d", cfg.BatchItems, MaxExchangeBatchItems)
	}
	if cfg.BatchItems != 256 {
		t.Fatalf("default batch items = %d, want 256 for the high-rate quorum profile", cfg.BatchItems)
	}
	if cfg.PeerTargetFlight != 16 {
		t.Fatalf("default peer target flight = %d, want 16 for independent quorum exchanges to one follower", cfg.PeerTargetFlight)
	}
	if cfg.TrailingFlushInterval != 100*time.Millisecond {
		t.Fatalf("default trailing flush interval = %v, want 100ms for bounded post-quorum batching", cfg.TrailingFlushInterval)
	}
	if cfg.ReplicaHedgeDelay != 25*time.Millisecond {
		t.Fatalf("default replica hedge delay = %v, want 25ms", cfg.ReplicaHedgeDelay)
	}
	if cfg.MaxVoters != 3 {
		t.Fatalf("default max voters = %d, want 3", cfg.MaxVoters)
	}
}

func TestRuntimeCarriesConfiguredReplicaHedgeDelayToDurabilityDispatcher(t *testing.T) {
	store, err := NewStoreAdapter(StoreAdapterConfig{
		Factory: channelstore.NewMemoryFactory(), MaxBatchItems: MaxExchangeBatchItems, MaxBatchBytes: MaxExchangeBatchBytes,
	})
	if err != nil {
		t.Fatalf("NewStoreAdapter() error = %v", err)
	}
	runtime, err := NewRuntime(RuntimeConfig{
		LocalNode: 1, Store: store, Link: &recordingPeerLink{}, Goroutines: goruntimeregistry.New(),
		ReplicaHedgeDelay: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Errorf("Runtime.Close() error = %v", err)
		}
	})
	dispatcher, ok := runtime.log.cfg.Durability.(*batchingDurabilityDispatcher)
	if !ok {
		t.Fatalf("durability dispatcher = %T, want *batchingDurabilityDispatcher", runtime.log.cfg.Durability)
	}
	if got := dispatcher.replicaHedgeDelay(); got != 25*time.Millisecond {
		t.Fatalf("replica hedge delay = %v, want 25ms", got)
	}
}

func TestRuntimeLocalDurabilityObservesEndToEndStage(t *testing.T) {
	request := testReplicateRequest(t, "1:local-observe", "local-observe", 1, []byte("payload"))
	store := &recordingReplicaStore{results: []MutationResult{{Outcome: ch.AppendOutcomeDurable, LastOffset: request.Manifest.LastOffset}}}
	observer := &recordingReplicationStageObserver{}
	local := &runtimeLocalDurability{
		runtime: &Runtime{ctx: context.Background()}, store: store, timeout: time.Second, observer: observer,
	}
	var completed durabilityCompletion
	err := local.runBatch(context.Background(), []localDurabilityItem{{
		proposal: durableProposal{
			first: request.Manifest.BaseOffset + 1, last: request.Manifest.LastOffset,
			channelKey: request.ChannelKey, channelID: request.ChannelID, leader: request.Leader,
			manifest: request.Manifest, records: request.Records,
		},
		complete: func(got durabilityCompletion) { completed = got }, submittedAt: time.Now(),
	}})
	if err != nil {
		t.Fatalf("runBatch() error = %v", err)
	}
	if !completed.outcome.Durable() || completed.err != nil {
		t.Fatalf("completion = %+v, want durable", completed)
	}
	if len(store.batches) != 1 || len(store.batches[0]) != 1 || store.batches[0][0].Class != MutationClassLeaderQuorum {
		t.Fatalf("local store mutations = %+v, want one leader-quorum mutation", store.batches)
	}
	stages := observer.snapshot()
	for _, stage := range []string{"quorum_local_queue", "quorum_local_store", "quorum_local_end_to_end"} {
		if !hasReplicationStage(stages, stage, "ok") {
			t.Fatalf("replication stages = %+v, want %s/ok", stages, stage)
		}
	}
}

func TestRuntimeReplicatesIndependentChannelsConcurrentlyToOneFollower(t *testing.T) {
	t.Parallel()

	router := &runtimeTestRouter{servers: make(map[ch.NodeID]*ExchangeServer)}
	started := make(chan ch.NodeID, 4)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)

	runtimes := make(map[ch.NodeID]*Runtime, 3)
	for _, node := range []ch.NodeID{1, 2, 3} {
		store, err := NewStoreAdapter(StoreAdapterConfig{
			Factory: channelstore.NewMemoryFactory(), MaxBatchItems: MaxExchangeBatchItems, MaxBatchBytes: MaxExchangeBatchBytes,
		})
		if err != nil {
			t.Fatalf("NewStoreAdapter(node=%d) error = %v", node, err)
		}
		var link PeerLink = runtimeTestLink{from: node, router: router}
		if node == 1 {
			link = &blockingReplicateRuntimeLink{base: link, started: started, release: release}
		}
		runtime, err := NewRuntime(RuntimeConfig{
			LocalNode: node, Store: store, Link: link, Goroutines: goruntimeregistry.New(),
		})
		if err != nil {
			t.Fatalf("NewRuntime(node=%d) error = %v", node, err)
		}
		router.register(node, runtime.ExchangeServer())
		runtimes[node] = runtime
	}
	t.Cleanup(func() {
		unblock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, node := range []ch.NodeID{1, 2, 3} {
			if err := runtimes[node].Close(ctx); err != nil {
				t.Errorf("Runtime.Close(node=%d) error = %v", node, err)
			}
		}
	})

	firstKey := ch.ChannelKey("1:parallel-a")
	secondKey := ch.ChannelKey("1:parallel-b")
	if preferredFollowerIndex(firstKey, 2) != preferredFollowerIndex(secondKey, 2) {
		secondKey += "a"
	}
	authorities := []Authority{
		{
			Key: firstKey, ChannelID: ch.ChannelID{ID: "parallel-a", Type: 1},
			ID:     AuthorityID{ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7},
			Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2,
		},
		{
			Key: secondKey, ChannelID: ch.ChannelID{ID: "parallel-b", Type: 1},
			ID:     AuthorityID{ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7},
			Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2,
		},
	}
	for _, authority := range authorities {
		if _, err := runtimes[1].Log().Install(context.Background(), authority); err != nil {
			t.Fatalf("Install(%s) error = %v", authority.Key, err)
		}
	}

	committed := make(chan error, len(authorities))
	commit := func(index int) {
		authority := authorities[index]
		_, err := runtimes[1].Log().Commit(context.Background(), Proposal{
			Key: authority.Key, Expected: authority.ID, CommandID: ch.CommandID{31: byte(index + 1)},
			Records: []ch.Record{{
				ID: uint64(index + 1), Epoch: authority.ID.ChannelEpoch, FromUID: "sender", ClientMsgNo: "parallel",
				Payload: []byte("payload"), SizeBytes: len("payload"), ServerTimestampMS: int64(index + 1),
			}},
		})
		committed <- err
	}
	go commit(0)
	waitForReplicateStarts(t, started, 1, time.Second)
	go commit(1)
	if got := waitForReplicateStarts(t, started, 1, 250*time.Millisecond); got != 1 {
		unblock()
		for range authorities {
			<-committed
		}
		t.Fatalf("independent Channel follower exchanges started = %d, want 1 before the first Channel was released", got)
	}
	unblock()
	for range authorities {
		if err := <-committed; err != nil {
			t.Fatalf("Commit() error = %v", err)
		}
	}
}

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

func TestRuntimeRetainsEveryFollowerRepairWhenExecutionQueueIsSaturated(t *testing.T) {
	const channelCount = 66

	router := &runtimeTestRouter{servers: make(map[ch.NodeID]*ExchangeServer), rejectReplicate: make(map[ch.NodeID]int)}
	runtimes := make(map[ch.NodeID]*Runtime, 3)
	stores := make(map[ch.NodeID]ReplicaStore, 3)
	var leaderStore *blockingRepairLoadStore
	repairStarted := make(chan struct{})
	releaseRepair := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(releaseRepair) }) }
	t.Cleanup(releaseAll)
	for _, node := range []ch.NodeID{1, 2, 3} {
		base, err := NewStoreAdapter(StoreAdapterConfig{
			Factory: channelstore.NewMemoryFactory(), MaxBatchItems: MaxExchangeBatchItems, MaxBatchBytes: 4 << 20,
		})
		if err != nil {
			t.Fatalf("NewStoreAdapter(node=%d) error = %v", node, err)
		}
		var store ReplicaStore = base
		if node == 1 {
			leaderStore = &blockingRepairLoadStore{base: base, started: repairStarted, release: releaseRepair}
			store = leaderStore
		}
		runtime, err := NewRuntime(RuntimeConfig{
			LocalNode: node, Store: store, Link: runtimeTestLink{from: node, router: router}, Goroutines: goruntimeregistry.New(),
			RepairWorkers: 1, ReplicaHedgeDelay: time.Nanosecond,
		})
		if err != nil {
			t.Fatalf("NewRuntime(node=%d) error = %v", node, err)
		}
		router.register(node, runtime.ExchangeServer())
		runtimes[node] = runtime
		stores[node] = store
	}
	t.Cleanup(func() {
		releaseAll()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, node := range []ch.NodeID{1, 2, 3} {
			if err := runtimes[node].Close(ctx); err != nil {
				t.Errorf("Runtime.Close(node=%d) error = %v", node, err)
			}
		}
	})

	authorities := make([]Authority, channelCount)
	for i := range authorities {
		channelID := fmt.Sprintf("repair-saturation-%03d", i)
		authorities[i] = Authority{
			Key: ch.ChannelKey("1:" + channelID), ChannelID: ch.ChannelID{ID: channelID, Type: 1},
			ID:     AuthorityID{ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7},
			Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2,
		}
		if _, err := runtimes[1].Log().Install(context.Background(), authorities[i]); err != nil {
			t.Fatalf("Install(%d) error = %v", i, err)
		}
	}
	commit := func(index int) {
		router.rejectNextReplicate(2)
		_, err := runtimes[1].Log().Commit(context.Background(), Proposal{
			Key: authorities[index].Key, Expected: authorities[index].ID, CommandID: ch.CommandID{29: byte(index + 1), 31: 77},
			Records: []ch.Record{{
				ID: uint64(index + 1), Epoch: authorities[index].ID.ChannelEpoch, FromUID: "sender",
				ClientMsgNo: fmt.Sprintf("repair-saturation-%03d", index), Payload: []byte("payload"),
				SizeBytes: len("payload"), ServerTimestampMS: int64(index + 1),
			}},
		})
		if err != nil {
			t.Fatalf("Commit(%d) error = %v", index, err)
		}
		if err := runtimes[1].peers.flushDeferred(); err != nil {
			t.Fatalf("flushDeferred(%d) error = %v", index, err)
		}
	}

	leaderStore.block.Store(true)
	commit(0)
	select {
	case <-repairStarted:
	case <-time.After(time.Second):
		t.Fatal("first follower repair did not enter the blocked execution owner")
	}
	for i := 1; i < channelCount; i++ {
		commit(i)
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtimes[1].repairs.pendingCount() < channelCount {
		if time.Now().After(deadline) {
			t.Fatalf("retained repairs = %d, want %d", runtimes[1].repairs.pendingCount(), channelCount)
		}
		time.Sleep(time.Millisecond)
	}

	releaseAll()
	waitForRuntimeReplicaBatchLEO(t, stores[2], authorities, 1)
}

func TestRuntimeRepairOwnerDropsSupersededAuthorityFollowers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := &runtimeRepairOwner{
		ctx: ctx, maxPending: 3, pending: make(map[runtimeRepairKey]*runtimeRepairEntry), notify: make(chan struct{}, 1),
	}
	first := Authority{
		Key: "1:repair-authority", ChannelID: ch.ChannelID{ID: "repair-authority", Type: 1},
		ID: AuthorityID{ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1}, Leader: 1,
		Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2,
	}
	owner.InstallAuthority(first)
	owner.RecordFollowerRepair(testFollowerRepair(first, 2))
	owner.RecordFollowerRepair(testFollowerRepair(first, 3))
	if got := owner.pendingCount(); got != 2 {
		t.Fatalf("first-authority repairs = %d, want 2", got)
	}

	next := first
	next.ID = AuthorityID{ChannelEpoch: 2, LeaderTerm: 2, FenceVersion: 2}
	next.Voters = []ch.NodeID{1, 4, 5}
	owner.InstallAuthority(next)
	if got := owner.pendingCount(); got != 0 {
		t.Fatalf("repairs after authority replacement = %d, want 0", got)
	}
	owner.RecordFollowerRepair(testFollowerRepair(first, 2))
	owner.RecordFollowerRepair(testFollowerRepair(next, 4))
	owner.RecordFollowerRepair(testFollowerRepair(next, 5))
	if got := owner.pendingCount(); got != 2 {
		t.Fatalf("current-authority repairs = %d, want 2 after stale repair rejection", got)
	}
}

func TestRuntimeRepairOwnerIgnoresSupersededWorkerCompletionForReusedFollower(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := &runtimeRepairOwner{
		ctx: ctx, maxPending: 3, pending: make(map[runtimeRepairKey]*runtimeRepairEntry), notify: make(chan struct{}, 1),
	}
	first := Authority{
		Key: "1:repair-reused-follower", ChannelID: ch.ChannelID{ID: "repair-reused-follower", Type: 1},
		ID: AuthorityID{ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1}, Leader: 1,
		Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2,
	}
	owner.InstallAuthority(first)
	owner.RecordFollowerRepair(testFollowerRepair(first, 2))
	key, _, _, version, ok := owner.take()
	if !ok {
		t.Fatal("first authority repair was not runnable")
	}
	oldEntry := owner.pending[key]

	next := first
	next.ID = AuthorityID{ChannelEpoch: 2, LeaderTerm: 2, FenceVersion: 2}
	next.Voters = []ch.NodeID{1, 2, 4}
	owner.InstallAuthority(next)
	owner.RecordFollowerRepair(testFollowerRepair(next, 2))
	newEntry := owner.pending[key]
	if newEntry == nil || newEntry == oldEntry {
		t.Fatalf("new authority entry = %p, old = %p", newEntry, oldEntry)
	}

	owner.finish(key, oldEntry, version, true)
	if got := owner.pending[key]; got != newEntry {
		t.Fatalf("entry after superseded worker completion = %p, want current %p", got, newEntry)
	}
}

func testFollowerRepair(authority Authority, follower ch.NodeID) followerRepair {
	return followerRepair{
		channelKey: authority.Key, channelID: authority.ChannelID, leader: authority.Leader, follower: follower, needFrom: 1,
		manifest: ch.ProposalManifest{
			ChannelEpoch: authority.ID.ChannelEpoch, LeaderTerm: authority.ID.LeaderTerm,
			FenceVersion: authority.ID.FenceVersion, LastOffset: 1,
		},
	}
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

func waitForRuntimeReplicaBatchLEO(t *testing.T, store ReplicaStore, authorities []Authority, want uint64) {
	t.Helper()
	items := make([]LoadRequest, len(authorities))
	for i, authority := range authorities {
		items[i] = LoadRequest{ChannelKey: authority.Key, ChannelID: authority.ChannelID}
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		loaded, err := store.Load(context.Background(), LoadBatch{Items: items})
		if err == nil && len(loaded.Items) == len(items) {
			complete := true
			for _, item := range loaded.Items {
				if item.Err != nil || item.State.LEO != want {
					complete = false
					break
				}
			}
			if complete {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("replica batch = %+v, error %v; want every durable LEO %d", loaded, err, want)
		}
		time.Sleep(time.Millisecond)
	}
}

type blockingRepairLoadStore struct {
	base    ReplicaStore
	started chan struct{}
	release <-chan struct{}
	once    sync.Once
	block   atomic.Bool
}

func (s *blockingRepairLoadStore) Load(ctx context.Context, batch LoadBatch) (LoadBatchResult, error) {
	if !s.block.Load() {
		return s.base.Load(ctx, batch)
	}
	s.once.Do(func() { close(s.started) })
	select {
	case <-ctx.Done():
		return LoadBatchResult{}, ctx.Err()
	case <-s.release:
		return s.base.Load(ctx, batch)
	}
}

func (s *blockingRepairLoadStore) Sync(ctx context.Context, mutations []Mutation) []MutationResult {
	return s.base.Sync(ctx, mutations)
}

func (s *blockingRepairLoadStore) Replace(ctx context.Context, replacements []RecoveryReplacement) []RecoveryReplacementResult {
	return s.base.Replace(ctx, replacements)
}

func (s *blockingRepairLoadStore) Fetch(ctx context.Context, ranges []FetchRange) []FetchRangeResult {
	return s.base.Fetch(ctx, ranges)
}

func (s *blockingRepairLoadStore) LookupCommands(ctx context.Context, lookups []CommandLookup) []CommandLookupResult {
	return s.base.(commandStore).LookupCommands(ctx, lookups)
}

func waitForReplicateStarts(t *testing.T, started <-chan ch.NodeID, want int, timeout time.Duration) int {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	got := 0
	for got < want {
		select {
		case <-started:
			got++
		case <-timer.C:
			return got
		}
	}
	return got
}

type blockingReplicateRuntimeLink struct {
	base    PeerLink
	started chan<- ch.NodeID
	release <-chan struct{}
}

func (l *blockingReplicateRuntimeLink) Exchange(ctx context.Context, target ch.NodeID, batch ExchangeBatch) (ExchangeBatchResult, error) {
	if len(batch.Items) > 0 && batch.Items[0].Kind == ExchangeReplicate {
		select {
		case l.started <- target:
		case <-ctx.Done():
			return ExchangeBatchResult{}, ctx.Err()
		}
		select {
		case <-l.release:
		case <-ctx.Done():
			return ExchangeBatchResult{}, ctx.Err()
		}
	}
	return l.base.Exchange(ctx, target, batch)
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
