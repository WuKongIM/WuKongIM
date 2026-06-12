package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
)

const (
	rpcBatchMaxItems         = 64
	rpcBatchMaxWait          = 250 * time.Microsecond
	storeAppendBatchMaxItems = 64
	storeAppendBatchMaxWait  = 250 * time.Microsecond
	storeApplyBatchMaxItems  = 64
	storeApplyBatchMaxWait   = 250 * time.Microsecond
)

// QueueObserver receives bounded worker queue depth samples.
type QueueObserver interface {
	SetWorkerQueueDepth(pool string, depth int)
}

// InflightObserver receives current and peak running worker counts.
type InflightObserver interface {
	SetWorkerInflight(pool string, inflight int)
	SetWorkerInflightPeak(pool string, peak int)
}

// QueueCapacityObserver receives configured worker queue capacity and worker count.
// Implementations are called synchronously from pool paths and should be concurrency-safe and non-blocking.
type QueueCapacityObserver interface {
	SetWorkerQueueCapacity(pool string, capacity int)
	SetWorkerWorkers(pool string, workers int)
}

// AdmissionObserver receives bounded worker enqueue outcomes.
// Implementations are called synchronously from Submit and should be concurrency-safe and non-blocking.
type AdmissionObserver interface {
	ObserveWorkerAdmission(pool string, result string)
}

// WaitObserver receives queue wait time for accepted worker tasks.
// Implementations are called synchronously from worker goroutines and should be concurrency-safe and non-blocking.
type WaitObserver interface {
	ObserveWorkerWait(pool string, kind TaskKind, d time.Duration)
}

// TaskObserver receives execution duration for worker tasks with pool context.
// Implementations are called synchronously from worker goroutines and should be concurrency-safe and non-blocking.
type TaskObserver interface {
	ObserveWorkerTask(pool string, kind TaskKind, err error, d time.Duration)
}

// BatchObserver receives worker-side batch observations.
// Implementations are called synchronously from worker goroutines and should be concurrency-safe and non-blocking.
type BatchObserver interface {
	ObserveWorkerBatch(pool string, kind TaskKind, items int, err error)
}

// queuedTask carries enqueue timing for wait-duration observation.
type queuedTask struct {
	task       Task
	enqueuedAt time.Time
}

type rpcBatchKey struct {
	kind TaskKind
	node ch.NodeID
}

type noopQueueObserver struct{}

func (noopQueueObserver) SetWorkerQueueDepth(pool string, depth int) {}

// PoolConfig defines worker and queue limits for one bounded pool.
type PoolConfig struct {
	Name      string
	Workers   int
	QueueSize int
}

// Pool runs blocking tasks with bounded admission and ants-backed execution.
type Pool struct {
	cfg    PoolConfig
	deps   Deps
	sink   CompletionSink
	queue  chan queuedTask
	stop   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	exec   *executor

	obsMu sync.RWMutex
	obs   QueueObserver

	inflight     atomic.Int64
	inflightPeak atomic.Int64
	once         sync.Once
	closeErr     error
	dispatchWG   sync.WaitGroup
	taskWG       sync.WaitGroup
}

// NewPool starts a bounded worker pool.
func NewPool(cfg PoolConfig, deps Deps, sink CompletionSink) (*Pool, error) {
	if cfg.Workers <= 0 || cfg.QueueSize <= 0 || sink == nil {
		return nil, ch.ErrInvalidConfig
	}
	exec, err := newExecutor(cfg.Workers)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &Pool{
		cfg:    cfg,
		deps:   deps,
		sink:   sink,
		queue:  make(chan queuedTask, cfg.QueueSize),
		stop:   make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
		exec:   exec,
		obs:    noopQueueObserver{},
	}
	p.dispatchWG.Add(1)
	go p.dispatch()
	return p, nil
}

// SetQueueObserver replaces the queue pressure observer; nil restores the no-op observer.
func (p *Pool) SetQueueObserver(observer QueueObserver) {
	if p == nil {
		return
	}
	if observer == nil {
		observer = noopQueueObserver{}
	}
	p.obsMu.Lock()
	p.obs = observer
	p.obsMu.Unlock()
	p.observeQueueCapacity()
	p.observeWorkers()
	p.observeQueueDepth()
}

func (p *Pool) observer() QueueObserver {
	if p == nil {
		return noopQueueObserver{}
	}
	p.obsMu.RLock()
	observer := p.obs
	p.obsMu.RUnlock()
	if observer == nil {
		return noopQueueObserver{}
	}
	return observer
}

// Submit enqueues task or returns a typed backpressure/closed error.
func (p *Pool) Submit(ctx context.Context, task Task) error {
	if p == nil {
		return ch.ErrClosed
	}
	result := "ok"
	defer func() {
		p.observeAdmission(result)
		p.observeQueueDepth()
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-p.stop:
		result = "closed"
		return ch.ErrClosed
	default:
	}
	select {
	case <-ctx.Done():
		result = workerAdmissionResult(ctx.Err())
		return ctx.Err()
	default:
	}
	queued := queuedTask{task: task, enqueuedAt: time.Now()}
	select {
	case p.queue <- queued:
		return nil
	case <-p.stop:
		result = "closed"
		return ch.ErrClosed
	case <-ctx.Done():
		result = workerAdmissionResult(ctx.Err())
		return ctx.Err()
	default:
		result = "full"
		return ch.ErrBackpressured
	}
}

// Close cancels running tasks, completes queued tasks as closed, and releases the executor.
func (p *Pool) Close() error {
	if p == nil {
		return nil
	}
	p.once.Do(func() {
		p.cancel()
		close(p.stop)
		p.dispatchWG.Wait()
		p.completeQueuedClosed()
		p.taskWG.Wait()
		p.closeErr = p.exec.close()
	})
	return p.closeErr
}

// Name returns the configured pool name.
func (p *Pool) Name() string {
	if p == nil {
		return ""
	}
	return p.cfg.Name
}

// QueueDepth returns the number of tasks waiting in the pool queue.
func (p *Pool) QueueDepth() int {
	if p == nil {
		return 0
	}
	return len(p.queue)
}

func (p *Pool) observeQueueDepth() {
	p.observer().SetWorkerQueueDepth(p.cfg.Name, len(p.queue))
}

func (p *Pool) observeQueueCapacity() {
	obs, ok := p.observer().(QueueCapacityObserver)
	if !ok {
		return
	}
	obs.SetWorkerQueueCapacity(p.cfg.Name, p.cfg.QueueSize)
}

func (p *Pool) observeWorkers() {
	obs, ok := p.observer().(QueueCapacityObserver)
	if !ok {
		return
	}
	obs.SetWorkerWorkers(p.cfg.Name, p.cfg.Workers)
}

func (p *Pool) observeAdmission(result string) {
	obs, ok := p.observer().(AdmissionObserver)
	if !ok {
		return
	}
	obs.ObserveWorkerAdmission(p.cfg.Name, result)
}

func (p *Pool) observeWait(kind TaskKind, d time.Duration) {
	obs, ok := p.observer().(WaitObserver)
	if !ok {
		return
	}
	obs.ObserveWorkerWait(p.cfg.Name, kind, nonNegativeDuration(d))
}

func (p *Pool) observeTask(kind TaskKind, err error, d time.Duration) {
	obs, ok := p.observer().(TaskObserver)
	if !ok {
		return
	}
	obs.ObserveWorkerTask(p.cfg.Name, kind, err, nonNegativeDuration(d))
}

func (p *Pool) observeBatch(kind TaskKind, items int, err error) {
	obs, ok := p.observer().(BatchObserver)
	if !ok || items <= 0 {
		return
	}
	obs.ObserveWorkerBatch(p.cfg.Name, kind, items, err)
}

func (p *Pool) observeInflight(inflight int) {
	obs, ok := p.observer().(InflightObserver)
	if !ok {
		return
	}
	obs.SetWorkerInflight(p.cfg.Name, inflight)
	peak := p.updateInflightPeak(inflight)
	obs.SetWorkerInflightPeak(p.cfg.Name, peak)
}

func (p *Pool) updateInflightPeak(inflight int) int {
	value := int64(inflight)
	for {
		peak := p.inflightPeak.Load()
		if value <= peak {
			return int(peak)
		}
		if p.inflightPeak.CompareAndSwap(peak, value) {
			return inflight
		}
	}
}

func (p *Pool) taskGroups(first queuedTask) [][]queuedTask {
	items := []queuedTask{first}
	switch {
	case p.canCollectRPCBatch(first.task):
		p.collectBatchItems(&items, rpcBatchMaxItems, rpcBatchMaxWait)
		return groupRPCBatchItems(items)
	case p.canCollectStoreAppendBatch(first.task):
		p.collectBatchItems(&items, storeAppendBatchMaxItems, storeAppendBatchMaxWait)
		return groupStoreBatchItems(items, TaskStoreAppend)
	case p.canCollectStoreApplyBatch(first.task):
		p.collectBatchItems(&items, storeApplyBatchMaxItems, storeApplyBatchMaxWait)
		return groupStoreBatchItems(items, TaskStoreApply)
	default:
		return [][]queuedTask{{first}}
	}
}

func (p *Pool) canCollectRPCBatch(task Task) bool {
	if _, ok := p.deps.Transport.(transport.BatchClient); !ok {
		return false
	}
	_, ok := rpcBatchKeyFor(task)
	return ok
}

func (p *Pool) canCollectStoreAppendBatch(task Task) bool {
	if _, ok := p.deps.Stores.(store.LeaderAppendBatcher); !ok {
		return false
	}
	return task.Kind == TaskStoreAppend
}

func (p *Pool) canCollectStoreApplyBatch(task Task) bool {
	if _, ok := p.deps.Stores.(store.FollowerApplyBatcher); !ok {
		return false
	}
	return task.Kind == TaskStoreApply
}

func (p *Pool) collectBatchItems(items *[]queuedTask, maxItems int, maxWait time.Duration) {
	p.drainReadyBatchItems(items, maxItems)
	if len(*items) > 1 || len(*items) >= maxItems {
		return
	}
	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	select {
	case queued := <-p.queue:
		*items = append(*items, queued)
		p.observeQueueDepth()
		p.drainReadyBatchItems(items, maxItems)
	case <-timer.C:
	case <-p.stop:
	}
}

func (p *Pool) drainReadyBatchItems(items *[]queuedTask, maxItems int) {
	for len(*items) < maxItems {
		select {
		case queued := <-p.queue:
			*items = append(*items, queued)
			p.observeQueueDepth()
		default:
			return
		}
	}
}

func groupRPCBatchItems(items []queuedTask) [][]queuedTask {
	groups := make([][]queuedTask, 0, len(items))
	used := make([]bool, len(items))
	for i, item := range items {
		if used[i] {
			continue
		}
		key, ok := rpcBatchKeyFor(item.task)
		if !ok {
			used[i] = true
			groups = append(groups, []queuedTask{item})
			continue
		}
		group := []queuedTask{item}
		indexes := []int{i}
		for j := i + 1; j < len(items); j++ {
			if used[j] {
				continue
			}
			other, ok := rpcBatchKeyFor(items[j].task)
			if !ok || other != key {
				continue
			}
			group = append(group, items[j])
			indexes = append(indexes, j)
		}
		for _, index := range indexes {
			used[index] = true
		}
		groups = append(groups, group)
	}
	return groups
}

func groupStoreBatchItems(items []queuedTask, kind TaskKind) [][]queuedTask {
	groups := make([][]queuedTask, 0, len(items))
	used := make([]bool, len(items))
	for i, item := range items {
		if used[i] {
			continue
		}
		if item.task.Kind != kind {
			used[i] = true
			groups = append(groups, []queuedTask{item})
			continue
		}
		group := []queuedTask{item}
		indexes := []int{i}
		keys := map[ch.ChannelKey]struct{}{item.task.Fence.ChannelKey: {}}
		for j := i + 1; j < len(items); j++ {
			if used[j] || items[j].task.Kind != kind {
				continue
			}
			key := items[j].task.Fence.ChannelKey
			if _, ok := keys[key]; ok {
				continue
			}
			group = append(group, items[j])
			indexes = append(indexes, j)
			keys[key] = struct{}{}
		}
		for _, index := range indexes {
			used[index] = true
		}
		groups = append(groups, group)
	}
	return groups
}

func rpcBatchKeyFor(task Task) (rpcBatchKey, bool) {
	switch task.Kind {
	case TaskRPCPull:
		if task.RPCPull == nil {
			return rpcBatchKey{}, false
		}
		return rpcBatchKey{kind: task.Kind, node: task.RPCPull.Node}, true
	case TaskRPCPullHint:
		if task.RPCPullHint == nil {
			return rpcBatchKey{}, false
		}
		return rpcBatchKey{kind: task.Kind, node: task.RPCPullHint.Node}, true
	default:
		return rpcBatchKey{}, false
	}
}

func (p *Pool) runTaskGroup(group []queuedTask) {
	if len(group) == 0 {
		return
	}
	for _, queued := range group {
		p.observeWait(queued.task.Kind, time.Since(queued.enqueuedAt))
	}
	running := int(p.inflight.Add(1))
	p.observeInflight(running)
	started := time.Now()
	results := p.runQueuedGroup(group)
	duration := nonNegativeDuration(time.Since(started))
	for i := range results {
		results[i].Duration = duration
		p.observeTask(results[i].Kind, results[i].Err, results[i].Duration)
	}
	running = int(p.inflight.Add(-1))
	p.observeInflight(running)
	for _, result := range results {
		p.sink.Complete(result)
	}
}

func (p *Pool) runQueuedGroup(group []queuedTask) []Result {
	if len(group) > 1 {
		key, ok := rpcBatchKeyFor(group[0].task)
		batchClient, batchOK := p.deps.Transport.(transport.BatchClient)
		if ok && batchOK {
			switch key.kind {
			case TaskRPCPull:
				return p.runRPCPullBatch(group, batchClient, key.node)
			case TaskRPCPullHint:
				return p.runRPCPullHintBatch(group, batchClient, key.node)
			}
		}
		if group[0].task.Kind == TaskStoreAppend {
			if batcher, ok := p.deps.Stores.(store.LeaderAppendBatcher); ok {
				return p.runStoreAppendBatch(group, batcher)
			}
		}
		if group[0].task.Kind == TaskStoreApply {
			if batcher, ok := p.deps.Stores.(store.FollowerApplyBatcher); ok {
				return p.runStoreApplyBatch(group, batcher)
			}
		}
	}
	results := make([]Result, 0, len(group))
	for _, queued := range group {
		results = append(results, queued.task.Run(p.ctx, p.deps))
	}
	return results
}

func (p *Pool) runStoreAppendBatch(group []queuedTask, batcher store.LeaderAppendBatcher) []Result {
	results := make([]Result, len(group))
	items := make([]store.AppendLeaderBatchItem, 0, len(group))
	active := make([]int, 0, len(group))
	for i, queued := range group {
		results[i] = Result{Kind: queued.task.Kind, Fence: queued.task.Fence, StoreAppend: &StoreAppendResult{}}
		if err := taskContextDoneErr(queued.task); err != nil {
			results[i].Err = err
			continue
		}
		if queued.task.StoreAppend == nil {
			results[i] = invalidResult(queued.task)
			continue
		}
		payload := queued.task.StoreAppend
		items = append(items, store.AppendLeaderBatchItem{
			ChannelKey: queued.task.Fence.ChannelKey,
			ChannelID:  payload.ChannelID,
			Request:    store.AppendLeaderRequest{Records: payload.Records, Sync: payload.Sync},
		})
		active = append(active, i)
	}
	if len(active) == 0 {
		return results
	}
	if len(active) == 1 {
		index := active[0]
		results[index] = group[index].task.Run(p.ctx, p.deps)
		return results
	}
	ctx, cancel := batchTaskContext(p.ctx, group, active)
	defer cancel()
	batchResults := batcher.AppendLeaderBatch(ctx, items)
	if len(batchResults) != len(active) {
		p.observeBatch(TaskStoreAppend, len(active), ch.ErrInvalidConfig)
		for _, index := range active {
			results[index].Err = ch.ErrInvalidConfig
		}
		return results
	}
	batchErr := firstStoreAppendBatchErr(batchResults)
	p.observeBatch(TaskStoreAppend, len(active), batchErr)
	for i, index := range active {
		item := batchResults[i]
		results[index].Err = batchContextErr(group[index].task, ctx, item.Err)
		results[index].StoreAppend = &StoreAppendResult{BaseOffset: item.BaseOffset, LastOffset: item.LastOffset}
	}
	return results
}

func (p *Pool) runStoreApplyBatch(group []queuedTask, batcher store.FollowerApplyBatcher) []Result {
	results := make([]Result, len(group))
	items := make([]store.ApplyFollowerBatchItem, 0, len(group))
	active := make([]int, 0, len(group))
	for i, queued := range group {
		results[i] = Result{Kind: queued.task.Kind, Fence: queued.task.Fence, StoreApply: &StoreApplyResult{}}
		if err := taskContextDoneErr(queued.task); err != nil {
			results[i].Err = err
			continue
		}
		if queued.task.StoreApply == nil {
			results[i] = invalidResult(queued.task)
			continue
		}
		payload := queued.task.StoreApply
		items = append(items, store.ApplyFollowerBatchItem{
			ChannelKey: queued.task.Fence.ChannelKey,
			ChannelID:  payload.ChannelID,
			Request:    store.ApplyFollowerRequest{Records: payload.Records, LeaderHW: payload.LeaderHW},
		})
		active = append(active, i)
	}
	if len(active) == 0 {
		return results
	}
	if len(active) == 1 {
		index := active[0]
		results[index] = group[index].task.Run(p.ctx, p.deps)
		return results
	}
	ctx, cancel := batchTaskContext(p.ctx, group, active)
	defer cancel()
	batchResults := batcher.ApplyFollowerBatch(ctx, items)
	if len(batchResults) != len(active) {
		p.observeBatch(TaskStoreApply, len(active), ch.ErrInvalidConfig)
		for _, index := range active {
			results[index].Err = ch.ErrInvalidConfig
		}
		return results
	}
	batchErr := firstStoreApplyBatchErr(batchResults)
	p.observeBatch(TaskStoreApply, len(active), batchErr)
	for i, index := range active {
		item := batchResults[i]
		results[index].Err = batchContextErr(group[index].task, ctx, item.Err)
		results[index].StoreApply = &StoreApplyResult{LEO: item.LEO}
	}
	return results
}

func (p *Pool) runRPCPullBatch(group []queuedTask, batchClient transport.BatchClient, node ch.NodeID) []Result {
	results := make([]Result, len(group))
	requests := make([]transport.PullRequest, 0, len(group))
	active := make([]int, 0, len(group))
	for i, queued := range group {
		results[i] = Result{Kind: queued.task.Kind, Fence: queued.task.Fence, RPCPull: &RPCPullResult{}}
		if err := taskContextDoneErr(queued.task); err != nil {
			results[i].Err = err
			continue
		}
		if queued.task.RPCPull == nil {
			results[i] = invalidResult(queued.task)
			continue
		}
		requests = append(requests, queued.task.RPCPull.Request)
		active = append(active, i)
	}
	if len(active) == 0 {
		return results
	}
	if len(active) == 1 {
		index := active[0]
		results[index] = group[index].task.Run(p.ctx, p.deps)
		return results
	}
	ctx, cancel := batchTaskContext(p.ctx, group, active)
	defer cancel()
	resp, err := batchClient.PullBatch(ctx, node, transport.PullBatchRequest{Items: requests})
	if err != nil {
		p.observeBatch(TaskRPCPull, len(active), err)
		for _, index := range active {
			results[index].Err = batchContextErr(group[index].task, ctx, err)
		}
		return results
	}
	if len(resp.Items) != len(active) {
		p.observeBatch(TaskRPCPull, len(active), ch.ErrInvalidConfig)
		for _, index := range active {
			results[index].Err = ch.ErrInvalidConfig
		}
		return results
	}
	p.observeBatch(TaskRPCPull, len(active), nil)
	for i, index := range active {
		item := resp.Items[i]
		results[index].Err = batchContextErr(group[index].task, ctx, item.Err)
		results[index].RPCPull = &RPCPullResult{Response: item.Response}
	}
	return results
}

func (p *Pool) runRPCPullHintBatch(group []queuedTask, batchClient transport.BatchClient, node ch.NodeID) []Result {
	results := make([]Result, len(group))
	requests := make([]transport.PullHintRequest, 0, len(group))
	active := make([]int, 0, len(group))
	for i, queued := range group {
		results[i] = Result{Kind: queued.task.Kind, Fence: queued.task.Fence, RPCPullHint: &RPCPullHintResult{}}
		if err := taskContextDoneErr(queued.task); err != nil {
			results[i].Err = err
			continue
		}
		if queued.task.RPCPullHint == nil {
			results[i] = invalidResult(queued.task)
			continue
		}
		requests = append(requests, queued.task.RPCPullHint.Request)
		active = append(active, i)
	}
	if len(active) == 0 {
		return results
	}
	if len(active) == 1 {
		index := active[0]
		results[index] = group[index].task.Run(p.ctx, p.deps)
		return results
	}
	ctx, cancel := batchTaskContext(p.ctx, group, active)
	defer cancel()
	resp, err := batchClient.PullHintBatch(ctx, node, transport.PullHintBatchRequest{Items: requests})
	if err != nil {
		p.observeBatch(TaskRPCPullHint, len(active), err)
		for _, index := range active {
			results[index].Err = batchContextErr(group[index].task, ctx, err)
		}
		return results
	}
	if len(resp.Items) != len(active) {
		p.observeBatch(TaskRPCPullHint, len(active), ch.ErrInvalidConfig)
		for _, index := range active {
			results[index].Err = ch.ErrInvalidConfig
		}
		return results
	}
	p.observeBatch(TaskRPCPullHint, len(active), nil)
	for i, index := range active {
		results[index].Err = batchContextErr(group[index].task, ctx, resp.Items[i].Err)
		results[index].RPCPullHint = &RPCPullHintResult{}
	}
	return results
}

func firstStoreAppendBatchErr(results []store.AppendLeaderBatchResult) error {
	for _, result := range results {
		if result.Err != nil {
			return result.Err
		}
	}
	return nil
}

func firstStoreApplyBatchErr(results []store.ApplyFollowerBatchResult) error {
	for _, result := range results {
		if result.Err != nil {
			return result.Err
		}
	}
	return nil
}

func batchTaskContext(parent context.Context, group []queuedTask, active []int) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	var deadline time.Time
	hasDeadline := false
	for _, index := range active {
		taskCtx := group[index].task.Context
		if taskCtx == nil {
			continue
		}
		next, ok := taskCtx.Deadline()
		if !ok {
			continue
		}
		if !hasDeadline || next.Before(deadline) {
			deadline = next
			hasDeadline = true
		}
	}
	if !hasDeadline {
		return parent, func() {}
	}
	return context.WithDeadline(parent, deadline)
}

func batchContextErr(task Task, ctx context.Context, err error) error {
	if taskErr := taskContextDoneErr(task); taskErr != nil {
		return taskErr
	}
	if err == context.Canceled {
		if cause := context.Cause(ctx); cause != nil && cause != context.Canceled {
			return cause
		}
	}
	return err
}

func taskContextDoneErr(task Task) error {
	if task.Context == nil || task.Context.Err() == nil {
		return nil
	}
	return contextFromTaskCause(task.Context)
}

func workerAdmissionResult(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "other"
	}
}

func nonNegativeDuration(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}
