package channels

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestSlotMetaSourcePipelinesBoundedBatchesForOneSlot(t *testing.T) {
	const wantMaxInFlight = 4
	store := newBlockingConcurrentRuntimeMetaBatchStore()
	router := fixedRuntimeMetaBatchRouter{route: routing.Route{
		HashSlot: 7, SlotID: 3, Leader: 1, LeaderTerm: 4, ConfigEpoch: 2, Revision: 9,
	}}
	source := NewSlotMetaSource(store, SlotMetaSourceOptions{
		Router: router, BatchStore: store,
		Placement: fakePlacementResolver{placement: ChannelPlacement{
			Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, MinISR: 2,
		}},
	})
	t.Cleanup(func() {
		store.release()
		if err := source.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	results := make(chan error, wantMaxInFlight+1)
	startEnsure := func(i int) {
		id := ch.ChannelID{ID: fmt.Sprintf("pipelined-cold-channel-%d", i), Type: 1}
		go func() {
			_, err := source.EnsureChannelMeta(context.Background(), id)
			results <- err
		}()
	}
	for i := 0; i < wantMaxInFlight; i++ {
		startEnsure(i)
		store.waitForStarted(t, 1)
	}
	startEnsure(wantMaxInFlight)
	select {
	case <-store.started:
		t.Fatalf("started more than %d concurrent metadata batches", wantMaxInFlight)
	case <-time.After(50 * time.Millisecond):
	}
	if got := store.maxActive(); got != wantMaxInFlight {
		t.Fatalf("maximum active metadata batches = %d, want %d", got, wantMaxInFlight)
	}

	store.release()
	for i := 0; i < wantMaxInFlight+1; i++ {
		if err := <-results; err != nil {
			t.Fatalf("EnsureChannelMeta(call=%d) error = %v", i, err)
		}
	}
}

func TestSlotMetaSourceCloseJoinsAllPipelinedBatches(t *testing.T) {
	const inFlight = 4
	store := newBlockingConcurrentRuntimeMetaBatchStore()
	router := fixedRuntimeMetaBatchRouter{route: routing.Route{
		HashSlot: 7, SlotID: 3, Leader: 1, LeaderTerm: 4, ConfigEpoch: 2, Revision: 9,
	}}
	source := NewSlotMetaSource(store, SlotMetaSourceOptions{
		Router: router, BatchStore: store,
		Placement: fakePlacementResolver{placement: ChannelPlacement{
			Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, MinISR: 2,
		}},
	})
	t.Cleanup(store.release)

	results := make(chan error, inFlight)
	for i := 0; i < inFlight; i++ {
		id := ch.ChannelID{ID: fmt.Sprintf("closing-pipelined-channel-%d", i), Type: 1}
		go func() {
			_, err := source.EnsureChannelMeta(context.Background(), id)
			results <- err
		}()
		store.waitForStarted(t, 1)
	}

	closed := make(chan error, 1)
	go func() { closed <- source.Close() }()
	deadline := time.Now().Add(time.Second)
	for {
		source.batcher.mu.Lock()
		stopping := source.batcher.stopping
		source.batcher.mu.Unlock()
		if stopping {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Close() did not close metadata admission")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-closed:
		t.Fatalf("Close() returned before pipelined batches completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	_, err := source.EnsureChannelMeta(context.Background(), ch.ChannelID{ID: "after-close-started", Type: 1})
	if !errors.Is(err, ErrMetaCreateStopped) {
		t.Fatalf("EnsureChannelMeta(after Close started) error = %v, want ErrMetaCreateStopped", err)
	}

	store.release()
	for i := 0; i < inFlight; i++ {
		if err := <-results; err != nil {
			t.Fatalf("EnsureChannelMeta(call=%d) error = %v", i, err)
		}
	}
	if err := <-closed; err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestServiceCloseStopsSlotMetaSourceBatchAdmission(t *testing.T) {
	store := &runtimeMetaReaderFake{err: metadb.ErrNotFound}
	source := NewSlotMetaSource(store, withTestMetaBatch(store, SlotMetaSourceOptions{
		DefaultReplicas: []ch.NodeID{1, 2, 3}, DefaultMinISR: 2,
	}))
	service, err := NewService(Config{Runtime: &fakeRuntime{}, LocalNode: 1, MetaSource: source})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	_, err = source.EnsureChannelMeta(context.Background(), ch.ChannelID{ID: "after-close", Type: 1})
	if !errors.Is(err, ErrMetaCreateStopped) {
		t.Fatalf("EnsureChannelMeta(after Close) error = %v, want ErrMetaCreateStopped", err)
	}
}

func TestSlotMetaSourceCoalescesConcurrentEnsureForSameMissingChannel(t *testing.T) {
	id := ch.ChannelID{ID: "coalesced-cold-channel", Type: 1}
	store := newBlockingRuntimeMetaBatchStore()
	observer := newBlockingMetaCreateBatchObserver()
	source := NewSlotMetaSource(store, SlotMetaSourceOptions{
		Router:     fixedRuntimeMetaBatchRouter{route: routing.Route{HashSlot: 7, SlotID: 3, Leader: 1, LeaderTerm: 4, ConfigEpoch: 2, Revision: 9}},
		BatchStore: store,
		Placement: fakePlacementResolver{placement: ChannelPlacement{
			Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, MinISR: 2,
		}},
		BatchObserver: observer,
	})
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	first := make(chan ensureMetaResult, 1)
	go func() {
		meta, err := source.EnsureChannelMeta(context.Background(), id)
		first <- ensureMetaResult{meta: meta, err: err}
	}()
	<-store.firstBatchStarted

	second := make(chan ensureMetaResult, 1)
	go func() {
		meta, err := source.EnsureChannelMeta(context.Background(), id)
		second <- ensureMetaResult{meta: meta, err: err}
	}()
	<-observer.coalesced
	close(store.releaseFirstBatch)

	for index, result := range []ensureMetaResult{<-first, <-second} {
		if result.err != nil {
			t.Fatalf("EnsureChannelMeta(call=%d) error = %v", index+1, result.err)
		}
		if result.meta.ID != id || result.meta.Leader != 1 || result.meta.MinISR != 2 {
			t.Fatalf("EnsureChannelMeta(call=%d) meta = %#v", index+1, result.meta)
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.createBatches) != 1 || len(store.createBatches[0]) != 1 {
		t.Fatalf("create batches = %#v, want one proposal containing one logical create", store.createBatches)
	}
	if store.createBatches[0][0].Meta.ChannelID != id.ID {
		t.Fatalf("created identity = %#v, want %v", store.createBatches[0][0], id)
	}
}

func TestSlotMetaSourceReroutesQueuedCreateBeforeSubmission(t *testing.T) {
	id := ch.ChannelID{ID: "rerouted-cold-channel", Type: 1}
	store := newBlockingRuntimeMetaBatchStore()
	close(store.releaseFirstBatch)
	router := &changingRuntimeMetaBatchRouter{
		initial: routing.Route{HashSlot: 7, SlotID: 3, Leader: 1, LeaderTerm: 4, ConfigEpoch: 2, Revision: 9},
		latest:  routing.Route{HashSlot: 8, SlotID: 4, Leader: 2, LeaderTerm: 5, ConfigEpoch: 3, Revision: 10},
	}
	source := NewSlotMetaSource(store, SlotMetaSourceOptions{
		Router: router, BatchStore: store,
		Placement: fakePlacementResolver{placement: ChannelPlacement{
			Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, MinISR: 2,
		}},
	})
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	meta, err := source.EnsureChannelMeta(context.Background(), id)
	if err != nil {
		t.Fatalf("EnsureChannelMeta() error = %v", err)
	}
	if meta.ID != id {
		t.Fatalf("EnsureChannelMeta() meta = %#v, want %v", meta, id)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.createBatches) != 1 || len(store.createBatches[0]) != 1 || store.createBatches[0][0].HashSlot != router.latest.HashSlot {
		t.Fatalf("create batches = %#v, want one proposal on latest route %#v", store.createBatches, router.latest)
	}
}

func TestSlotMetaSourceRereadsAuthoritativeRowsBeforeRetryingUncertainCreate(t *testing.T) {
	id := ch.ChannelID{ID: "uncertain-created-channel", Type: 1}
	store := &uncertainAppliedRuntimeMetaBatchStore{}
	router := fixedRuntimeMetaBatchRouter{route: routing.Route{
		HashSlot: 7, SlotID: 3, Leader: 1, LeaderTerm: 4, ConfigEpoch: 2, Revision: 9,
	}}
	source := NewSlotMetaSource(store, SlotMetaSourceOptions{
		Router: router, BatchStore: store,
		Placement: fakePlacementResolver{placement: ChannelPlacement{
			Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, MinISR: 2,
		}},
	})
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	meta, err := source.EnsureChannelMeta(context.Background(), id)
	if err != nil {
		t.Fatalf("EnsureChannelMeta() error = %v", err)
	}
	if meta.ID != id || meta.Leader != 1 {
		t.Fatalf("EnsureChannelMeta() meta = %#v, want authoritative applied row", meta)
	}
	if store.createCalls != 1 || store.readCalls != 1 {
		t.Fatalf("create calls=%d read calls=%d, want one uncertain proposal then one authoritative reread", store.createCalls, store.readCalls)
	}
}

func TestSlotMetaSourceRetriesOnlyAuthoritativelyMissingUncertainCreates(t *testing.T) {
	id := ch.ChannelID{ID: "proven-missing-retry", Type: 1}
	store := &uncertainMissingOnceRuntimeMetaBatchStore{}
	router := fixedRuntimeMetaBatchRouter{route: routing.Route{
		HashSlot: 7, SlotID: 3, Leader: 1, LeaderTerm: 4, ConfigEpoch: 2, Revision: 9,
	}}
	source := NewSlotMetaSource(store, SlotMetaSourceOptions{
		Router: router, BatchStore: store,
		Placement: fakePlacementResolver{placement: ChannelPlacement{
			Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, MinISR: 2,
		}},
	})
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	meta, err := source.EnsureChannelMeta(context.Background(), id)
	if err != nil {
		t.Fatalf("EnsureChannelMeta() error = %v", err)
	}
	if meta.ID != id {
		t.Fatalf("EnsureChannelMeta() meta = %#v, want %v", meta, id)
	}
	if store.createCalls != 2 || store.readCalls != 2 {
		t.Fatalf("create calls=%d read calls=%d, want reread-before-retry then one successful retry", store.createCalls, store.readCalls)
	}
}

func TestSlotMetaSourceDoesNotRetryUncertainCreateAfterCorruptAuthoritativeRead(t *testing.T) {
	id := ch.ChannelID{ID: "uncertain-corrupt-read", Type: 1}
	store := &uncertainCorruptRuntimeMetaBatchStore{}
	router := fixedRuntimeMetaBatchRouter{route: routing.Route{
		HashSlot: 7, SlotID: 3, Leader: 1, LeaderTerm: 4, ConfigEpoch: 2, Revision: 9,
	}}
	source := NewSlotMetaSource(store, SlotMetaSourceOptions{
		Router: router, BatchStore: store,
		Placement: fakePlacementResolver{placement: ChannelPlacement{
			Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, MinISR: 2,
		}},
	})
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	_, err := source.EnsureChannelMeta(context.Background(), id)
	if !errors.Is(err, metadb.ErrCorruptValue) {
		t.Fatalf("EnsureChannelMeta() error = %v, want ErrCorruptValue", err)
	}
	if store.createCalls != 1 || store.readCalls != 1 {
		t.Fatalf("create calls=%d read calls=%d, want no retry after corrupt authoritative read", store.createCalls, store.readCalls)
	}
}

func TestSlotMetaSourceRebuildsPlacementFromLatestSnapshotAtSubmission(t *testing.T) {
	id := ch.ChannelID{ID: "latest-placement", Type: 1}
	store := newBlockingRuntimeMetaBatchStore()
	close(store.releaseFirstBatch)
	router := newBlockingRuntimeMetaBatchRouter(routing.Route{
		HashSlot: 7, SlotID: 3, Leader: 1, LeaderTerm: 4, ConfigEpoch: 2, Revision: 9,
	})
	placement := &refreshingPlacementResolver{placement: ChannelPlacement{
		Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, MinISR: 2,
	}}
	source := NewSlotMetaSource(store, SlotMetaSourceOptions{
		Router: router, BatchStore: store, Placement: placement,
	})
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	done := make(chan ensureMetaResult, 1)
	go func() {
		meta, err := source.EnsureChannelMeta(context.Background(), id)
		done <- ensureMetaResult{meta: meta, err: err}
	}()
	<-router.batchRouteStarted
	placement.set(ChannelPlacement{Leader: 2, Replicas: []ch.NodeID{2, 3, 4}, MinISR: 2})
	close(router.releaseBatchRoute)

	result := <-done
	if result.err != nil {
		t.Fatalf("EnsureChannelMeta() error = %v", result.err)
	}
	if result.meta.Leader != 2 || !equalNodeIDs(result.meta.Replicas, []ch.NodeID{2, 3, 4}) {
		t.Fatalf("EnsureChannelMeta() meta = %#v, want latest placement leader=2 replicas=2,3,4", result.meta)
	}
}

func TestSlotMetaSourceBuildsSubmittedCandidatesThroughBatchPlacementSnapshot(t *testing.T) {
	id := ch.ChannelID{ID: "batch-placement-only", Type: 1}
	store := newBlockingRuntimeMetaBatchStore()
	close(store.releaseFirstBatch)
	route := routing.Route{HashSlot: 7, SlotID: 3, Leader: 1, LeaderTerm: 4, ConfigEpoch: 2, Revision: 9}
	placement := &batchOnlyPlacementResolver{placement: ChannelPlacement{
		Leader: 2, Replicas: []ch.NodeID{2, 3, 4}, MinISR: 2,
	}}
	source := NewSlotMetaSource(store, SlotMetaSourceOptions{
		Router: fixedRuntimeMetaBatchRouter{route: route}, BatchStore: store, Placement: placement,
	})
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	meta, err := source.EnsureChannelMeta(context.Background(), id)
	if err != nil {
		t.Fatalf("EnsureChannelMeta() error = %v", err)
	}
	if placement.batchCalls != 1 || placement.scalarCalls != 0 {
		t.Fatalf("placement batch calls=%d scalar calls=%d, want one batch snapshot and no scalar lookup", placement.batchCalls, placement.scalarCalls)
	}
	if meta.Leader != 2 || !equalNodeIDs(meta.Replicas, []ch.NodeID{2, 3, 4}) {
		t.Fatalf("EnsureChannelMeta() meta = %#v, want batch placement", meta)
	}
}

type ensureMetaResult struct {
	meta ch.Meta
	err  error
}

type fixedRuntimeMetaBatchRouter struct {
	route routing.Route
}

type changingRuntimeMetaBatchRouter struct {
	mu             sync.Mutex
	initial        routing.Route
	latest         routing.Route
	routeKeysCalls int
}

type blockingRuntimeMetaBatchRouter struct {
	route             routing.Route
	batchRouteStarted chan struct{}
	releaseBatchRoute chan struct{}
	once              sync.Once
}

func newBlockingRuntimeMetaBatchRouter(route routing.Route) *blockingRuntimeMetaBatchRouter {
	return &blockingRuntimeMetaBatchRouter{
		route: route, batchRouteStarted: make(chan struct{}), releaseBatchRoute: make(chan struct{}),
	}
}

func (r *blockingRuntimeMetaBatchRouter) RouteKey(string) (routing.Route, error) {
	return r.route, nil
}

func (r *blockingRuntimeMetaBatchRouter) RouteKeys(keys []string) ([]routing.Route, error) {
	r.once.Do(func() { close(r.batchRouteStarted) })
	<-r.releaseBatchRoute
	routes := make([]routing.Route, len(keys))
	for i := range routes {
		routes[i] = r.route
	}
	return routes, nil
}

type refreshingPlacementResolver struct {
	mu        sync.Mutex
	placement ChannelPlacement
}

type batchOnlyPlacementResolver struct {
	placement   ChannelPlacement
	batchCalls  int
	scalarCalls int
}

func (r *batchOnlyPlacementResolver) ResolveChannelPlacement(context.Context, ch.ChannelID) (ChannelPlacement, error) {
	r.scalarCalls++
	return ChannelPlacement{}, errors.New("scalar placement must not be used for a submitted batch")
}

func (r *batchOnlyPlacementResolver) ResolveChannelPlacementBatch(_ context.Context, ids []ch.ChannelID, routes []routing.Route) ([]ChannelPlacement, error) {
	r.batchCalls++
	if len(ids) != len(routes) {
		return nil, errors.New("unaligned placement batch")
	}
	placements := make([]ChannelPlacement, len(ids))
	for i := range placements {
		placements[i] = r.placement
		placements[i].Replicas = append([]ch.NodeID(nil), r.placement.Replicas...)
	}
	return placements, nil
}

func (r *refreshingPlacementResolver) ResolveChannelPlacement(context.Context, ch.ChannelID) (ChannelPlacement, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	placement := r.placement
	placement.Replicas = append([]ch.NodeID(nil), placement.Replicas...)
	return placement, nil
}

func (r *refreshingPlacementResolver) ResolveChannelPlacementBatch(_ context.Context, ids []ch.ChannelID, routes []routing.Route) ([]ChannelPlacement, error) {
	if len(ids) != len(routes) {
		return nil, errors.New("unaligned placement batch")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	placements := make([]ChannelPlacement, len(ids))
	for i := range placements {
		placements[i] = r.placement
		placements[i].Replicas = append([]ch.NodeID(nil), r.placement.Replicas...)
	}
	return placements, nil
}

func (r *refreshingPlacementResolver) set(placement ChannelPlacement) {
	r.mu.Lock()
	r.placement = placement
	r.mu.Unlock()
}

func (r *changingRuntimeMetaBatchRouter) RouteKey(string) (routing.Route, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.routeKeysCalls == 0 {
		return r.initial, nil
	}
	return r.latest, nil
}

func (r *changingRuntimeMetaBatchRouter) RouteKeys(keys []string) ([]routing.Route, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routeKeysCalls++
	routes := make([]routing.Route, len(keys))
	for i := range routes {
		routes[i] = r.latest
	}
	return routes, nil
}

func (r fixedRuntimeMetaBatchRouter) RouteKey(string) (routing.Route, error) {
	return r.route, nil
}

func (r fixedRuntimeMetaBatchRouter) RouteKeys(keys []string) ([]routing.Route, error) {
	routes := make([]routing.Route, len(keys))
	for i := range routes {
		routes[i] = r.route
	}
	return routes, nil
}

type blockingRuntimeMetaBatchStore struct {
	mu                sync.Mutex
	rows              map[metadb.ChannelKey]metadb.ChannelRuntimeMeta
	createBatches     [][]RuntimeMetaCreateItem
	firstBatchStarted chan struct{}
	releaseFirstBatch chan struct{}
}

type blockingConcurrentRuntimeMetaBatchStore struct {
	mu          sync.Mutex
	rows        map[metadb.ChannelKey]metadb.ChannelRuntimeMeta
	started     chan struct{}
	releaseOnce sync.Once
	releaseCh   chan struct{}
	active      int
	max         int
}

type uncertainAppliedRuntimeMetaBatchStore struct {
	row         metadb.ChannelRuntimeMeta
	createCalls int
	readCalls   int
}

type uncertainMissingOnceRuntimeMetaBatchStore struct {
	row         metadb.ChannelRuntimeMeta
	createCalls int
	readCalls   int
}

type uncertainCorruptRuntimeMetaBatchStore struct {
	createCalls int
	readCalls   int
}

func (s *uncertainCorruptRuntimeMetaBatchStore) GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error) {
	return metadb.ChannelRuntimeMeta{}, metadb.ErrNotFound
}

func (s *uncertainCorruptRuntimeMetaBatchStore) CreateChannelRuntimeMetaBatch(context.Context, routing.Route, []RuntimeMetaCreateItem) ([]RuntimeMetaCreateResult, error) {
	s.createCalls++
	return nil, context.DeadlineExceeded
}

func (s *uncertainCorruptRuntimeMetaBatchStore) BatchGetChannelRuntimeMetas(context.Context, routing.Route, []RuntimeMetaCreateItem) ([]RuntimeMetaReadResult, error) {
	s.readCalls++
	return []RuntimeMetaReadResult{{Err: metadb.ErrCorruptValue}}, nil
}

func (s *uncertainMissingOnceRuntimeMetaBatchStore) GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error) {
	return metadb.ChannelRuntimeMeta{}, metadb.ErrNotFound
}

func (s *uncertainMissingOnceRuntimeMetaBatchStore) CreateChannelRuntimeMetaBatch(_ context.Context, _ routing.Route, items []RuntimeMetaCreateItem) ([]RuntimeMetaCreateResult, error) {
	s.createCalls++
	if s.createCalls == 1 {
		return nil, context.DeadlineExceeded
	}
	item := items[0]
	s.row = metadb.NormalizeChannelRuntimeMeta(item.Meta)
	return []RuntimeMetaCreateResult{{
		HashSlot: item.HashSlot, ChannelID: item.Meta.ChannelID, ChannelType: item.Meta.ChannelType, Created: true,
	}}, nil
}

func (s *uncertainMissingOnceRuntimeMetaBatchStore) BatchGetChannelRuntimeMetas(_ context.Context, _ routing.Route, _ []RuntimeMetaCreateItem) ([]RuntimeMetaReadResult, error) {
	s.readCalls++
	if s.readCalls == 1 {
		return []RuntimeMetaReadResult{{Err: metadb.ErrNotFound}}, nil
	}
	return []RuntimeMetaReadResult{{Meta: s.row}}, nil
}

func (s *uncertainAppliedRuntimeMetaBatchStore) GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error) {
	return metadb.ChannelRuntimeMeta{}, metadb.ErrNotFound
}

func (s *uncertainAppliedRuntimeMetaBatchStore) CreateChannelRuntimeMetaBatch(_ context.Context, _ routing.Route, items []RuntimeMetaCreateItem) ([]RuntimeMetaCreateResult, error) {
	s.createCalls++
	s.row = metadb.NormalizeChannelRuntimeMeta(items[0].Meta)
	return nil, context.DeadlineExceeded
}

func (s *uncertainAppliedRuntimeMetaBatchStore) BatchGetChannelRuntimeMetas(_ context.Context, _ routing.Route, items []RuntimeMetaCreateItem) ([]RuntimeMetaReadResult, error) {
	s.readCalls++
	return []RuntimeMetaReadResult{{Meta: s.row}}, nil
}

func newBlockingRuntimeMetaBatchStore() *blockingRuntimeMetaBatchStore {
	return &blockingRuntimeMetaBatchStore{
		rows:              make(map[metadb.ChannelKey]metadb.ChannelRuntimeMeta),
		firstBatchStarted: make(chan struct{}),
		releaseFirstBatch: make(chan struct{}),
	}
}

func newBlockingConcurrentRuntimeMetaBatchStore() *blockingConcurrentRuntimeMetaBatchStore {
	return &blockingConcurrentRuntimeMetaBatchStore{
		rows: make(map[metadb.ChannelKey]metadb.ChannelRuntimeMeta), started: make(chan struct{}, 16), releaseCh: make(chan struct{}),
	}
}

func (s *blockingConcurrentRuntimeMetaBatchStore) GetChannelRuntimeMeta(_ context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, ok := s.rows[metadb.ChannelKey{ChannelID: channelID, ChannelType: channelType}]
	if !ok {
		return metadb.ChannelRuntimeMeta{}, metadb.ErrNotFound
	}
	return meta, nil
}

func (s *blockingConcurrentRuntimeMetaBatchStore) CreateChannelRuntimeMetaBatch(_ context.Context, _ routing.Route, items []RuntimeMetaCreateItem) ([]RuntimeMetaCreateResult, error) {
	s.mu.Lock()
	s.active++
	if s.active > s.max {
		s.max = s.active
	}
	s.mu.Unlock()
	s.started <- struct{}{}
	<-s.releaseCh

	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
	results := make([]RuntimeMetaCreateResult, len(items))
	for i, item := range items {
		key := metadb.ChannelKey{ChannelID: item.Meta.ChannelID, ChannelType: item.Meta.ChannelType}
		_, existed := s.rows[key]
		if !existed {
			s.rows[key] = metadb.NormalizeChannelRuntimeMeta(item.Meta)
		}
		results[i] = RuntimeMetaCreateResult{
			HashSlot: item.HashSlot, ChannelID: key.ChannelID, ChannelType: key.ChannelType, Created: !existed,
		}
	}
	return results, nil
}

func (s *blockingConcurrentRuntimeMetaBatchStore) BatchGetChannelRuntimeMetas(_ context.Context, _ routing.Route, items []RuntimeMetaCreateItem) ([]RuntimeMetaReadResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	results := make([]RuntimeMetaReadResult, len(items))
	for i, item := range items {
		meta, ok := s.rows[metadb.ChannelKey{ChannelID: item.Meta.ChannelID, ChannelType: item.Meta.ChannelType}]
		if !ok {
			results[i].Err = metadb.ErrNotFound
			continue
		}
		results[i].Meta = meta
	}
	return results, nil
}

func (s *blockingConcurrentRuntimeMetaBatchStore) waitForStarted(t *testing.T, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		select {
		case <-s.started:
		case <-time.After(time.Second):
			t.Fatalf("started metadata batches = %d, want at least %d", i, count)
		}
	}
}

func (s *blockingConcurrentRuntimeMetaBatchStore) release() {
	s.releaseOnce.Do(func() { close(s.releaseCh) })
}

func (s *blockingConcurrentRuntimeMetaBatchStore) maxActive() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.max
}

func (s *blockingRuntimeMetaBatchStore) GetChannelRuntimeMeta(_ context.Context, channelID string, channelType int64) (metadb.ChannelRuntimeMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, ok := s.rows[metadb.ChannelKey{ChannelID: channelID, ChannelType: channelType}]
	if !ok {
		return metadb.ChannelRuntimeMeta{}, metadb.ErrNotFound
	}
	return meta, nil
}

func (s *blockingRuntimeMetaBatchStore) CreateChannelRuntimeMetaBatch(_ context.Context, _ routing.Route, items []RuntimeMetaCreateItem) ([]RuntimeMetaCreateResult, error) {
	s.mu.Lock()
	batch := append([]RuntimeMetaCreateItem(nil), items...)
	s.createBatches = append(s.createBatches, batch)
	first := len(s.createBatches) == 1
	s.mu.Unlock()
	if first {
		close(s.firstBatchStarted)
		<-s.releaseFirstBatch
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	results := make([]RuntimeMetaCreateResult, len(items))
	for i, item := range items {
		key := metadb.ChannelKey{ChannelID: item.Meta.ChannelID, ChannelType: item.Meta.ChannelType}
		_, existed := s.rows[key]
		if !existed {
			s.rows[key] = metadb.NormalizeChannelRuntimeMeta(item.Meta)
		}
		results[i] = RuntimeMetaCreateResult{
			HashSlot: item.HashSlot, ChannelID: key.ChannelID, ChannelType: key.ChannelType, Created: !existed,
		}
	}
	return results, nil
}

func (s *blockingRuntimeMetaBatchStore) BatchGetChannelRuntimeMetas(_ context.Context, _ routing.Route, items []RuntimeMetaCreateItem) ([]RuntimeMetaReadResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	results := make([]RuntimeMetaReadResult, len(items))
	for i, item := range items {
		meta, ok := s.rows[metadb.ChannelKey{ChannelID: item.Meta.ChannelID, ChannelType: item.Meta.ChannelType}]
		if !ok {
			results[i].Err = metadb.ErrNotFound
			continue
		}
		results[i].Meta = meta
	}
	return results, nil
}

type blockingMetaCreateBatchObserver struct {
	once      sync.Once
	coalesced chan struct{}
}

func newBlockingMetaCreateBatchObserver() *blockingMetaCreateBatchObserver {
	return &blockingMetaCreateBatchObserver{coalesced: make(chan struct{})}
}

func (o *blockingMetaCreateBatchObserver) ObserveChannelMetaCreateCoalesced(uint32) {
	o.once.Do(func() { close(o.coalesced) })
}

func (o *blockingMetaCreateBatchObserver) SetChannelMetaCreateQueueDepth(uint32, int) {}

func (o *blockingMetaCreateBatchObserver) ObserveChannelMetaCreateBatch(uint32, string, int) {}
