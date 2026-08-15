package channels

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

const (
	metaCreateBatchMaxItems = 64
	metaCreateQueueMaxItems = 256
	metaCreateBatchTimeout  = 2 * time.Second
	metaCreateRerouteMax    = 2
)

var (
	// ErrMetaCreateBackpressured reports a full per-Slot unique create queue.
	ErrMetaCreateBackpressured = errors.New("channel metadata create backpressured")
	// ErrMetaCreateStopped reports admission after the node-owned batcher stops.
	ErrMetaCreateStopped      = errors.New("channel metadata create batcher stopped")
	errMetaCreateReroute      = errors.New("channel metadata create route changed before submission")
	errMetaCreateRetryMissing = errors.New("channel metadata create uncertain result is authoritatively missing")
)

type metaCreateBatcher struct {
	router     RuntimeMetaBatchRouter
	store      RuntimeMetaBatchStore
	observer   MetaCreateBatchObserver
	goroutines *goruntimeregistry.Registry
	build      runtimeMetaCreateBatchBuilder
	stage      func(string, string, time.Duration)

	mu       sync.Mutex
	owners   map[uint32]*metaCreateSlotOwner
	stopping bool
	rootCtx  context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

type runtimeMetaCreatePlanItem struct {
	id    ch.ChannelID
	route routing.Route
}

type runtimeMetaCreateBatchBuilder func(context.Context, []runtimeMetaCreatePlanItem) ([]RuntimeMetaCreateItem, error)

func newMetaCreateBatcher(router RuntimeMetaBatchRouter, store RuntimeMetaBatchStore, observer MetaCreateBatchObserver, goroutines *goruntimeregistry.Registry, build runtimeMetaCreateBatchBuilder, stage func(string, string, time.Duration)) *metaCreateBatcher {
	ctx, cancel := context.WithCancel(context.Background())
	return &metaCreateBatcher{
		router: router, store: store, observer: observer, goroutines: goroutines, build: build, stage: stage,
		owners: make(map[uint32]*metaCreateSlotOwner), rootCtx: ctx, cancel: cancel,
	}
}

func (b *metaCreateBatcher) ensure(ctx context.Context, id ch.ChannelID) metaCreateEnsureResult {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctxErr(ctx); err != nil {
		return metaCreateEnsureResult{err: err}
	}
	waitCtx, cancel := context.WithTimeout(ctx, metaCreateBatchTimeout)
	defer cancel()
	for attempt := 0; ; attempt++ {
		result := b.ensureOnce(waitCtx, id)
		reroute := errors.Is(result.err, errMetaCreateReroute)
		retryMissing := errors.Is(result.err, errMetaCreateRetryMissing)
		if !reroute && !retryMissing {
			return result
		}
		if attempt >= metaCreateRerouteMax {
			if reroute {
				result.err = fmt.Errorf("%w: runtime metadata route changed after %d retries", metadb.ErrStaleMeta, metaCreateRerouteMax)
			} else {
				result.err = fmt.Errorf("runtime metadata create remained missing after %d retries: %w", metaCreateRerouteMax, result.createErr)
			}
			result.createErr = result.err
			return result
		}
	}
}

func (b *metaCreateBatcher) ensureOnce(ctx context.Context, id ch.ChannelID) metaCreateEnsureResult {
	route, err := b.router.RouteKey(id.ID)
	if err != nil {
		return metaCreateEnsureResult{err: err, createErr: err}
	}
	owner, err := b.owner(route.SlotID)
	if err != nil {
		return metaCreateEnsureResult{err: err, createErr: err}
	}
	waiter, err := owner.admit(id, route.HashSlot)
	if err != nil {
		return metaCreateEnsureResult{err: err, createErr: err}
	}
	select {
	case result := <-waiter.result:
		return result
	case <-ctx.Done():
		owner.detach(waiter)
		return metaCreateEnsureResult{err: ctx.Err()}
	}
}

func (b *metaCreateBatcher) owner(slotID uint32) (*metaCreateSlotOwner, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopping {
		return nil, ErrMetaCreateStopped
	}
	if owner := b.owners[slotID]; owner != nil {
		return owner, nil
	}
	owner := &metaCreateSlotOwner{
		batcher: b, slotID: slotID, entries: make(map[metadb.ChannelKey]*metaCreateEntry),
		wake: make(chan struct{}, 1), done: make(chan struct{}),
	}
	b.owners[slotID] = owner
	b.wg.Add(1)
	goruntimeregistry.SafeGo(b.goroutines, goruntimeregistry.TaskClusterMetaCreateBatch, owner.run)
	return owner, nil
}

func (b *metaCreateBatcher) close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	if b.stopping {
		b.mu.Unlock()
		return nil
	}
	b.stopping = true
	owners := make([]*metaCreateSlotOwner, 0, len(b.owners))
	for _, owner := range b.owners {
		owners = append(owners, owner)
	}
	b.mu.Unlock()
	for _, owner := range owners {
		owner.stop()
	}

	timer := time.NewTimer(metaCreateBatchTimeout)
	defer timer.Stop()
	for _, owner := range owners {
		select {
		case <-owner.done:
		case <-timer.C:
			b.cancel()
			return context.DeadlineExceeded
		}
	}
	b.cancel()
	b.wg.Wait()
	return nil
}

type metaCreateSlotOwner struct {
	batcher *metaCreateBatcher
	slotID  uint32

	mu       sync.Mutex
	entries  map[metadb.ChannelKey]*metaCreateEntry
	queue    []*metaCreateEntry
	inFlight bool
	stopping bool
	wake     chan struct{}
	done     chan struct{}
}

type metaCreateEntry struct {
	key      metadb.ChannelKey
	item     RuntimeMetaCreateItem
	route    routing.Route
	waiters  map[*metaCreateWaiter]struct{}
	inFlight bool
}

type metaCreateWaiter struct {
	entry  *metaCreateEntry
	result chan metaCreateEnsureResult
}

type metaCreateEnsureResult struct {
	meta      metadb.ChannelRuntimeMeta
	err       error
	createErr error
}

func (o *metaCreateSlotOwner) admit(id ch.ChannelID, hashSlot uint16) (*metaCreateWaiter, error) {
	key := metadb.ChannelKey{ChannelID: id.ID, ChannelType: int64(id.Type)}
	item := RuntimeMetaCreateItem{
		HashSlot: hashSlot,
		Meta:     metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: int64(id.Type)},
	}
	waiter := &metaCreateWaiter{result: make(chan metaCreateEnsureResult, 1)}
	o.mu.Lock()
	if o.stopping {
		o.mu.Unlock()
		return nil, ErrMetaCreateStopped
	}
	if entry := o.entries[key]; entry != nil {
		waiter.entry = entry
		entry.waiters[waiter] = struct{}{}
		o.mu.Unlock()
		if o.batcher.observer != nil {
			o.batcher.observer.ObserveChannelMetaCreateCoalesced(o.slotID)
		}
		return waiter, nil
	}
	if len(o.queue) >= metaCreateQueueMaxItems {
		o.mu.Unlock()
		return nil, ErrMetaCreateBackpressured
	}
	entry := &metaCreateEntry{key: key, item: item, waiters: map[*metaCreateWaiter]struct{}{waiter: {}}}
	waiter.entry = entry
	o.entries[key] = entry
	o.queue = append(o.queue, entry)
	depth := len(o.queue)
	o.mu.Unlock()
	o.observeQueueDepth(depth)
	o.signal()
	return waiter, nil
}

func (o *metaCreateSlotOwner) detach(waiter *metaCreateWaiter) {
	if waiter == nil || waiter.entry == nil {
		return
	}
	o.mu.Lock()
	entry := waiter.entry
	current := o.entries[entry.key]
	if current != entry {
		o.mu.Unlock()
		return
	}
	delete(entry.waiters, waiter)
	if len(entry.waiters) == 0 && !entry.inFlight {
		delete(o.entries, entry.key)
		for i, queued := range o.queue {
			if queued == entry {
				copy(o.queue[i:], o.queue[i+1:])
				o.queue[len(o.queue)-1] = nil
				o.queue = o.queue[:len(o.queue)-1]
				break
			}
		}
	}
	depth := len(o.queue)
	o.mu.Unlock()
	o.observeQueueDepth(depth)
}

func (o *metaCreateSlotOwner) signal() {
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

func (o *metaCreateSlotOwner) run() {
	defer o.batcher.wg.Done()
	defer close(o.done)
	for {
		<-o.wake
		for {
			batch, stop := o.takeBatch()
			if len(batch) == 0 {
				if stop {
					return
				}
				break
			}
			results := o.submit(batch)
			o.finish(batch, results)
		}
	}
}

func (o *metaCreateSlotOwner) takeBatch() ([]*metaCreateEntry, bool) {
	o.mu.Lock()
	if len(o.queue) == 0 {
		stop := o.stopping && !o.inFlight
		o.mu.Unlock()
		return nil, stop
	}
	count := len(o.queue)
	if count > metaCreateBatchMaxItems {
		count = metaCreateBatchMaxItems
	}
	batch := append([]*metaCreateEntry(nil), o.queue[:count]...)
	copy(o.queue, o.queue[count:])
	for i := len(o.queue) - count; i < len(o.queue); i++ {
		if i >= 0 {
			o.queue[i] = nil
		}
	}
	o.queue = o.queue[:len(o.queue)-count]
	for _, entry := range batch {
		entry.inFlight = true
	}
	o.inFlight = true
	depth := len(o.queue)
	o.mu.Unlock()
	o.observeQueueDepth(depth)
	return batch, false
}

func (o *metaCreateSlotOwner) submit(batch []*metaCreateEntry) map[metadb.ChannelKey]metaCreateEnsureResult {
	results := make(map[metadb.ChannelKey]metaCreateEnsureResult, len(batch))
	buildStarted := time.Now()
	keys := make([]string, len(batch))
	for i, entry := range batch {
		keys[i] = entry.item.Meta.ChannelID
	}
	routes, err := o.batcher.router.RouteKeys(keys)
	if err != nil || len(routes) != len(batch) {
		if err == nil {
			err = fmt.Errorf("%w: aligned runtime metadata routes", metadb.ErrCorruptValue)
		} else if errors.Is(err, ch.ErrStaleMeta) || errors.Is(err, metadb.ErrStaleMeta) {
			err = fmt.Errorf("%w: %w", errMetaCreateReroute, err)
		}
		o.observeStage(channelMetaStageCreateBuild, err, buildStarted)
		return metaCreateBatchBuildErrorResults(batch, err)
	}
	for i, route := range routes {
		if route.SlotID != o.slotID || !sameRuntimeMetaBatchRoute(routes[0], route) {
			err = fmt.Errorf("%w: %w", errMetaCreateReroute, metadb.ErrStaleMeta)
			o.observeStage(channelMetaStageCreateBuild, err, buildStarted)
			return metaCreateBatchBuildErrorResults(batch, err)
		}
		batch[i].item.HashSlot = route.HashSlot
		batch[i].route = route
	}
	sort.Slice(batch, func(i, j int) bool {
		left, right := batch[i].item, batch[j].item
		if left.HashSlot != right.HashSlot {
			return left.HashSlot < right.HashSlot
		}
		if left.Meta.ChannelType != right.Meta.ChannelType {
			return left.Meta.ChannelType < right.Meta.ChannelType
		}
		return left.Meta.ChannelID < right.Meta.ChannelID
	})
	ctx, cancel := context.WithTimeout(o.batcher.rootCtx, metaCreateBatchTimeout)
	defer cancel()
	plans := make([]runtimeMetaCreatePlanItem, len(batch))
	for i, entry := range batch {
		plans[i] = runtimeMetaCreatePlanItem{
			id:    ch.ChannelID{ID: entry.key.ChannelID, Type: uint8(entry.key.ChannelType)},
			route: entry.route,
		}
	}
	items, buildErr := o.batcher.build(ctx, plans)
	if buildErr == nil {
		buildErr = validateRuntimeMetaCreateItems(plans, items)
	}
	if buildErr != nil {
		if errors.Is(buildErr, ch.ErrStaleMeta) || errors.Is(buildErr, metadb.ErrStaleMeta) {
			buildErr = fmt.Errorf("%w: %w", errMetaCreateReroute, buildErr)
		}
		o.observeStage(channelMetaStageCreateBuild, buildErr, buildStarted)
		o.observeBatch("error", len(batch))
		return metaCreateBatchBuildErrorResults(batch, buildErr)
	}
	o.observeStage(channelMetaStageCreateBuild, nil, buildStarted)
	for i := range batch {
		batch[i].item = items[i]
	}
	createStarted := time.Now()
	created, createErr := o.batcher.store.CreateChannelRuntimeMetaBatch(ctx, routes[0], items)
	if createErr == nil {
		createErr = validateRuntimeMetaCreateResults(items, created)
	}
	o.observeStage(channelMetaStageCreatePropose, createErr, createStarted)
	readStarted := time.Now()
	reads, readErr := o.batcher.store.BatchGetChannelRuntimeMetas(ctx, routes[0], items)
	if readErr == nil && len(reads) != len(items) {
		readErr = fmt.Errorf("%w: aligned runtime metadata reread", metadb.ErrCorruptValue)
	}
	if readErr != nil {
		o.observeStage(channelMetaStageFinalRead, readErr, readStarted)
		o.observeBatch("error", len(items))
		if createErr != nil {
			return metaCreateBatchCreateAndReadErrorResults(batch, createErr, readErr)
		}
		return metaCreateBatchReadErrorResults(batch, readErr)
	}
	allFound := true
	var readStageErr error
	for i, read := range reads {
		if read.Err != nil {
			allFound = false
			err := read.Err
			if readStageErr == nil || !errors.Is(read.Err, metadb.ErrNotFound) {
				readStageErr = read.Err
			}
			if createErr != nil && errors.Is(read.Err, metadb.ErrNotFound) {
				err = fmt.Errorf("%w: %w", errMetaCreateRetryMissing, createErr)
			}
			results[batch[i].key] = metaCreateEnsureResult{err: err, createErr: createErr}
			continue
		}
		if read.Meta.ChannelID != items[i].Meta.ChannelID || read.Meta.ChannelType != items[i].Meta.ChannelType {
			allFound = false
			readStageErr = metadb.ErrCorruptValue
			results[batch[i].key] = metaCreateEnsureResult{err: metadb.ErrCorruptValue, createErr: createErr}
			continue
		}
		results[batch[i].key] = metaCreateEnsureResult{meta: read.Meta, createErr: createErr}
	}
	o.observeStage(channelMetaStageFinalRead, readStageErr, readStarted)
	result := "ok"
	if createErr != nil && allFound {
		result = "recovered"
	} else if !allFound {
		result = "error"
	}
	o.observeBatch(result, len(items))
	return results
}

func (o *metaCreateSlotOwner) observeStage(stage string, err error, started time.Time) {
	if o == nil || o.batcher == nil || o.batcher.stage == nil {
		return
	}
	o.batcher.stage(stage, metaStageResult(err), time.Since(started))
}

func sameRuntimeMetaBatchRoute(left, right routing.Route) bool {
	return left.SlotID == right.SlotID && left.Leader == right.Leader &&
		left.LeaderTerm == right.LeaderTerm && left.ConfigEpoch == right.ConfigEpoch &&
		left.Revision == right.Revision
}

func validateRuntimeMetaCreateResults(items []RuntimeMetaCreateItem, results []RuntimeMetaCreateResult) error {
	if len(results) != len(items) {
		return fmt.Errorf("%w: aligned runtime metadata create results", metadb.ErrCorruptValue)
	}
	for i, result := range results {
		item := items[i]
		if result.HashSlot != item.HashSlot || result.ChannelID != item.Meta.ChannelID || result.ChannelType != item.Meta.ChannelType {
			return fmt.Errorf("%w: runtime metadata create result identity", metadb.ErrCorruptValue)
		}
	}
	return nil
}

func validateRuntimeMetaCreateItems(plans []runtimeMetaCreatePlanItem, items []RuntimeMetaCreateItem) error {
	if len(items) != len(plans) {
		return fmt.Errorf("%w: aligned runtime metadata create plan", metadb.ErrCorruptValue)
	}
	for i, item := range items {
		plan := plans[i]
		if item.HashSlot != plan.route.HashSlot || item.Meta.ChannelID != plan.id.ID || item.Meta.ChannelType != int64(plan.id.Type) {
			return fmt.Errorf("%w: runtime metadata create plan identity", metadb.ErrCorruptValue)
		}
	}
	return nil
}

func metaCreateBatchBuildErrorResults(batch []*metaCreateEntry, err error) map[metadb.ChannelKey]metaCreateEnsureResult {
	results := make(map[metadb.ChannelKey]metaCreateEnsureResult, len(batch))
	for _, entry := range batch {
		results[entry.key] = metaCreateEnsureResult{err: err}
	}
	return results
}

func metaCreateBatchReadErrorResults(batch []*metaCreateEntry, err error) map[metadb.ChannelKey]metaCreateEnsureResult {
	results := make(map[metadb.ChannelKey]metaCreateEnsureResult, len(batch))
	for _, entry := range batch {
		results[entry.key] = metaCreateEnsureResult{err: err}
	}
	return results
}

func metaCreateBatchCreateAndReadErrorResults(batch []*metaCreateEntry, createErr, readErr error) map[metadb.ChannelKey]metaCreateEnsureResult {
	results := make(map[metadb.ChannelKey]metaCreateEnsureResult, len(batch))
	for _, entry := range batch {
		results[entry.key] = metaCreateEnsureResult{err: errors.Join(createErr, readErr), createErr: createErr}
	}
	return results
}

func (o *metaCreateSlotOwner) finish(batch []*metaCreateEntry, results map[metadb.ChannelKey]metaCreateEnsureResult) {
	var deliveries []struct {
		waiter *metaCreateWaiter
		result metaCreateEnsureResult
	}
	o.mu.Lock()
	for _, entry := range batch {
		result, ok := results[entry.key]
		if !ok {
			result.err = metadb.ErrCorruptValue
		}
		for waiter := range entry.waiters {
			deliveries = append(deliveries, struct {
				waiter *metaCreateWaiter
				result metaCreateEnsureResult
			}{waiter: waiter, result: result})
		}
		delete(o.entries, entry.key)
	}
	o.inFlight = false
	stopping := o.stopping
	hasQueued := len(o.queue) > 0
	o.mu.Unlock()
	for _, delivery := range deliveries {
		delivery.waiter.result <- delivery.result
	}
	if hasQueued || stopping {
		o.signal()
	}
}

func (o *metaCreateSlotOwner) stop() {
	var waiters []*metaCreateWaiter
	o.mu.Lock()
	if o.stopping {
		o.mu.Unlock()
		return
	}
	o.stopping = true
	for _, entry := range o.queue {
		for waiter := range entry.waiters {
			waiters = append(waiters, waiter)
		}
		delete(o.entries, entry.key)
	}
	o.queue = nil
	inFlight := o.inFlight
	o.mu.Unlock()
	o.observeQueueDepth(0)
	for _, waiter := range waiters {
		waiter.result <- metaCreateEnsureResult{err: ErrMetaCreateStopped}
	}
	if !inFlight {
		o.signal()
	}
}

func (o *metaCreateSlotOwner) observeQueueDepth(depth int) {
	if o.batcher.observer != nil {
		o.batcher.observer.SetChannelMetaCreateQueueDepth(o.slotID, depth)
	}
}

func (o *metaCreateSlotOwner) observeBatch(result string, items int) {
	if o.batcher.observer != nil {
		o.batcher.observer.ObserveChannelMetaCreateBatch(o.slotID, result, items)
	}
}
