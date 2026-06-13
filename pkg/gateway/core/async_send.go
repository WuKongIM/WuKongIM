package core

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/panjf2000/ants/v2"
)

const asyncSendPanicValueMaxLen = 256

// sendExecutor admits SEND frames into bounded shard mailboxes and drains them on ants workers.
type sendExecutor struct {
	// server owns session and gateway state needed by send tasks.
	server *Server
	// workers is the normalized send worker count.
	workers int
	// capacity is the normalized maximum admitted send backlog.
	capacity int
	// queued tracks admitted send tasks awaiting execution.
	queued atomic.Int64
	// closed prevents new send admission after shutdown.
	closed atomic.Bool
	// pool runs shard drain tasks on bounded ants workers.
	pool *ants.PoolWithFuncGeneric[*asyncSendShard]
	// shards split SEND mailboxes by gateway session.
	shards []*asyncSendShard
	// releaseTimeout bounds graceful ants pool release.
	releaseTimeout time.Duration
	// stopOnce makes executor shutdown idempotent.
	stopOnce sync.Once
	// wg tracks scheduled shard drains that must complete before pool release.
	wg sync.WaitGroup
	// panicC records worker panics for package tests and diagnostics.
	panicC chan any
}

// asyncSendShard owns one bounded SEND mailbox and its scheduled state.
type asyncSendShard struct {
	executor *sendExecutor
	tasks    chan asyncDispatchTask

	mu         sync.Mutex
	scheduled  bool
	drainOwned bool
	draining   bool
	closed     bool
}

func newSendExecutor(s *Server, opts gatewaytypes.RuntimeOptions) (*sendExecutor, error) {
	opts = gatewaytypes.NormalizeRuntimeOptions(opts)
	e := &sendExecutor{
		server:         s,
		workers:        opts.AsyncSendWorkers,
		capacity:       opts.AsyncSendQueueCapacity,
		releaseTimeout: opts.AsyncPoolReleaseTimeout,
		panicC:         make(chan any, 1),
	}

	shardCapacity := asyncSendShardCapacity(e.capacity, e.workers)
	e.shards = make([]*asyncSendShard, e.workers)
	for i := range e.shards {
		e.shards[i] = &asyncSendShard{
			executor: e,
			tasks:    make(chan asyncDispatchTask, shardCapacity),
		}
	}

	pool, err := ants.NewPoolWithFuncGeneric[*asyncSendShard](
		e.workers,
		e.drainScheduledShard,
		ants.WithNonblocking(true),
		ants.WithDisablePurge(true),
		ants.WithPanicHandler(func(v any) {
			e.recordPanic(v, asyncDispatchTask{})
		}),
	)
	if err != nil {
		for _, shard := range e.shards {
			close(shard.tasks)
			shard.closed = true
		}
		return nil, err
	}
	e.pool = pool
	return e, nil
}

func asyncSendShardCapacity(totalCapacity, shards int) int {
	if shards <= 0 {
		shards = 1
	}
	if totalCapacity <= 0 {
		totalCapacity = 1
	}
	capacity := (totalCapacity + shards - 1) / shards
	if capacity <= 0 {
		return 1
	}
	return capacity
}

func (e *sendExecutor) submit(state *sessionState, replyToken string, send *frame.SendPacket) bool {
	if e == nil || send == nil || e.closed.Load() || len(e.shards) == 0 {
		return false
	}
	if !e.reserve() {
		return false
	}

	shard := e.shardForSend(state, send)
	if shard == nil {
		e.consume(1)
		return false
	}

	var shouldSchedule bool
	shard.mu.Lock()
	if shard.closed || len(shard.tasks) >= cap(shard.tasks) {
		shard.mu.Unlock()
		e.consume(1)
		return false
	}
	task := asyncDispatchTask{
		state:      state,
		replyToken: replyToken,
		frame:      cloneAsyncSendFrame(send, stateOwnsDecodedFrames(state)),
		enqueuedAt: time.Now(),
	}
	shard.tasks <- task
	if !shard.scheduled && e.server != nil {
		e.wg.Add(1)
		shard.scheduled = true
		shard.drainOwned = true
		shouldSchedule = true
	}
	shard.mu.Unlock()

	if shouldSchedule {
		e.schedule(shard)
	}
	return true
}

func (e *sendExecutor) stop() {
	if e == nil {
		return
	}
	e.stopOnce.Do(func() {
		e.closed.Store(true)
		for _, shard := range e.shards {
			if shard == nil {
				continue
			}
			shard.mu.Lock()
			if !shard.closed {
				shard.closed = true
				close(shard.tasks)
			}
			shard.mu.Unlock()
		}
		if e.server != nil {
			for _, shard := range e.shards {
				if e.claimShardDrainForStop(shard) {
					e.drainShard(shard)
					continue
				}
			}
			e.waitForDrainOrTimeout()
		} else {
			for _, shard := range e.shards {
				e.drainClosedShard(shard)
			}
			if e.pool != nil {
				_ = e.pool.ReleaseTimeout(e.releaseTimeout)
			}
		}
	})
}

func (e *sendExecutor) depth() int {
	if e == nil {
		return 0
	}
	return int(e.queued.Load())
}

func (e *sendExecutor) totalCapacity() int {
	if e == nil {
		return 0
	}
	return e.capacity
}

func (e *sendExecutor) reserve() bool {
	if e == nil {
		return false
	}
	for {
		queued := e.queued.Load()
		if queued < 0 || queued >= int64(e.capacity) {
			return false
		}
		if e.queued.CompareAndSwap(queued, queued+1) {
			return true
		}
	}
}

func (e *sendExecutor) schedule(shard *asyncSendShard) {
	if e == nil || shard == nil {
		return
	}
	e.invokeScheduledShard(shard)
}

func (e *sendExecutor) invokeScheduledShard(shard *asyncSendShard) {
	err := e.pool.Invoke(shard)
	switch {
	case err == nil:
		return
	case isAntsBusy(err):
		time.AfterFunc(time.Millisecond, func() {
			e.invokeScheduledShard(shard)
		})
	case isAntsStopped(err):
		if e.releaseDrainOwnership(shard) {
			e.drainOrDiscardShard(shard)
			e.wg.Done()
		}
	default:
		e.recordPanic(err, asyncDispatchTask{})
		if e.releaseDrainOwnership(shard) {
			e.wg.Done()
		}
	}
}

func (e *sendExecutor) drainScheduledShard(shard *asyncSendShard) {
	if !e.beginShardDrain(shard) {
		return
	}
	defer e.finishShardDrain(shard)
	e.drainShard(shard)
}

func (e *sendExecutor) drainShard(shard *asyncSendShard) {
	if e == nil || shard == nil {
		return
	}
	defer func() {
		if v := recover(); v != nil {
			e.recordPanic(v, asyncDispatchTask{})
			e.markShardUnscheduled(shard)
		}
	}()

	collector := newAsyncSendBatchCollector(shard.tasks, asyncSendBatchLimitsFromOptions(e.server.options.DefaultSession))
	for {
		batch, ok := e.nextShardBatch(collector, shard)
		if !ok {
			return
		}
		e.dispatchBatchSafely(batch)
	}
}

func (e *sendExecutor) nextShardBatch(collector *asyncSendBatchCollector, shard *asyncSendShard) ([]asyncDispatchTask, bool) {
	if collector == nil || shard == nil {
		return nil, false
	}
	if collector.hasPending {
		task, ok := collector.nextTask()
		if !ok {
			return nil, false
		}
		return collector.collect(task), true
	}

	select {
	case task, ok := <-shard.tasks:
		if !ok {
			return nil, false
		}
		return collector.collect(task), true
	default:
	}

	shard.mu.Lock()
	if shard.closed {
		shard.mu.Unlock()
		return nil, false
	}
	if len(shard.tasks) == 0 {
		shard.mu.Unlock()
		return nil, false
	}
	shard.mu.Unlock()
	return nil, true
}

func (e *sendExecutor) dispatchBatchSafely(batch []asyncDispatchTask) {
	if e == nil || len(batch) == 0 {
		return
	}
	consumed := false
	defer func() {
		if v := recover(); v != nil {
			if !consumed {
				e.consume(len(batch))
				if e.server != nil {
					e.server.observeAsyncSendQueue(e)
				}
			}
			e.recordPanic(v, firstAsyncDispatchTask(batch))
		}
	}()
	e.consume(len(batch))
	consumed = true
	e.dispatchBatch(batch)
}

func (e *sendExecutor) dispatchBatch(batch []asyncDispatchTask) {
	if e == nil || len(batch) == 0 {
		return
	}
	if e.server == nil {
		return
	}
	e.server.observeAsyncSendQueue(e)
	e.server.observeAsyncSendBatch(batch)
	if e.server.dispatchSendBatch(batch) {
		return
	}
	for _, task := range batch {
		e.server.recordAsyncDispatchWait(task)
		if err := e.server.dispatchFrame(task.state, task.replyToken, task.frame); err != nil {
			e.server.handleHandlerError(task.state, err)
		}
	}
}

func firstAsyncDispatchTask(batch []asyncDispatchTask) asyncDispatchTask {
	if len(batch) == 0 {
		return asyncDispatchTask{}
	}
	return batch[0]
}

func (e *sendExecutor) consume(count int) {
	if e == nil || count <= 0 {
		return
	}
	remaining := e.queued.Add(-int64(count))
	if remaining >= 0 {
		return
	}
	e.queued.Add(-remaining)
}

func (e *sendExecutor) shardForSend(state *sessionState, send *frame.SendPacket) *asyncSendShard {
	if e == nil || len(e.shards) == 0 {
		return nil
	}
	return e.shards[asyncSendShardIndex(state, send, len(e.shards))]
}

func (e *sendExecutor) beginShardDrain(shard *asyncSendShard) bool {
	if shard == nil {
		return false
	}
	shard.mu.Lock()
	defer shard.mu.Unlock()
	if !shard.drainOwned {
		return false
	}
	shard.draining = true
	return true
}

func (e *sendExecutor) finishShardDrain(shard *asyncSendShard) {
	if shard == nil {
		return
	}
	var shouldSchedule bool
	shard.mu.Lock()
	shard.draining = false
	if !shard.closed && len(shard.tasks) > 0 {
		shouldSchedule = true
		shard.scheduled = true
		shard.drainOwned = true
	} else {
		shard.scheduled = false
		shard.drainOwned = false
	}
	shard.mu.Unlock()
	if shouldSchedule {
		e.schedule(shard)
		return
	}
	e.wg.Done()
}

func (e *sendExecutor) markShardUnscheduled(shard *asyncSendShard) {
	if shard == nil {
		return
	}
	shard.mu.Lock()
	shard.scheduled = false
	shard.drainOwned = false
	shard.draining = false
	shard.mu.Unlock()
}

func (e *sendExecutor) claimShardDrainForStop(shard *asyncSendShard) bool {
	if shard == nil {
		return false
	}
	shard.mu.Lock()
	defer shard.mu.Unlock()
	if shard.drainOwned {
		if shard.draining {
			return false
		}
		shard.scheduled = false
		shard.drainOwned = false
		e.wg.Done()
		return true
	}
	return true
}

func (e *sendExecutor) releaseDrainOwnership(shard *asyncSendShard) bool {
	if shard == nil {
		return false
	}
	shard.mu.Lock()
	defer shard.mu.Unlock()
	if !shard.drainOwned {
		return false
	}
	shard.scheduled = false
	shard.drainOwned = false
	shard.draining = false
	return true
}

func (e *sendExecutor) drainOrDiscardShard(shard *asyncSendShard) {
	if e == nil || shard == nil {
		return
	}
	if e.server != nil {
		e.drainShard(shard)
		return
	}
	e.drainClosedShard(shard)
}

func (e *sendExecutor) waitForDrainOrTimeout() {
	if e == nil {
		return
	}
	drainDone := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(drainDone)
	}()
	releaseDone := make(chan struct{})
	if e.pool != nil {
		go func() {
			_ = e.pool.ReleaseTimeout(e.releaseTimeout)
			close(releaseDone)
		}()
	} else {
		close(releaseDone)
	}
	timer := time.NewTimer(e.releaseTimeout)
	defer timer.Stop()
	for drainDone != nil || releaseDone != nil {
		select {
		case <-drainDone:
			drainDone = nil
		case <-releaseDone:
			releaseDone = nil
		case <-timer.C:
			return
		}
	}
}

func (e *sendExecutor) drainClosedShard(shard *asyncSendShard) {
	if e == nil || shard == nil {
		return
	}
	for {
		select {
		case _, ok := <-shard.tasks:
			if !ok {
				return
			}
			e.consume(1)
		default:
			return
		}
	}
}

func (e *sendExecutor) recordPanic(v any, task asyncDispatchTask) {
	if e == nil {
		return
	}
	select {
	case e.panicC <- v:
	default:
	}
	defer func() {
		_ = recover()
	}()
	e.logPanic(v, task)
}

func (e *sendExecutor) logPanic(v any, task asyncDispatchTask) {
	if e == nil || e.server == nil || e.server.options.Logger == nil {
		return
	}
	fields := []wklog.Field{
		wklog.String("panic", boundedAsyncSendPanicValue(v)),
	}
	if task.state != nil && task.state.listener != nil {
		fields = append(fields, wklog.String("listener", task.state.listener.options.Name))
	}
	if send, ok := task.frame.(*frame.SendPacket); ok && send != nil {
		fields = append(fields, wklog.String("channel_id", send.ChannelID), wklog.String("client_msg_no", send.ClientMsgNo))
	}
	e.server.options.Logger.Warn("gateway async send task panic", fields...)
}

func boundedAsyncSendPanicValue(v any) string {
	text := fmt.Sprint(v)
	if len(text) <= asyncSendPanicValueMaxLen {
		return text
	}
	return text[:asyncSendPanicValueMaxLen]
}
