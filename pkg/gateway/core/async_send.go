package core

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/workqueue"
)

const (
	asyncSendPanicValueMaxLen        = 256
	asyncSendOrderingShardsPerWorker = 4
)

var asyncSendQueuePublicationRevision atomic.Uint64

func nextAsyncSendQueuePublicationRevision() uint64 {
	for {
		if revision := asyncSendQueuePublicationRevision.Add(1); revision != 0 {
			return revision
		}
	}
}

// sendExecutor admits SEND frames into a bounded workqueue-backed shard mailbox.
type sendExecutor struct {
	// server owns session and gateway state needed by send tasks.
	server *Server
	// workers is the normalized maximum concurrent send worker count.
	workers int
	// shards is the bounded logical ordering partition count. It is deliberately
	// larger than workers so unrelated sessions do not share a worker-local
	// head-of-line queue while each session still remains strictly ordered.
	shards int
	// capacity is the normalized maximum admitted send backlog.
	capacity int
	// shardCapacity is the per-shard mailbox admission bound.
	shardCapacity int
	// queueMu linearizes aggregate queue occupancy with its publication revision.
	queueMu sync.Mutex
	// queued tracks SEND tasks accepted by gateway but not yet entering dispatch.
	queued int64
	// queueRevision orders absolute observations across executor generations.
	queueRevision uint64
	// shardQueued tracks per-shard accepted tasks before dispatch begins.
	shardQueued []atomic.Int64
	// closed prevents new send admission after shutdown.
	closed atomic.Bool
	// admissionMu linearizes closing SEND admission with accepted task ownership.
	admissionMu sync.Mutex
	// admitted owns every caller that crossed SEND admission until its task has
	// completed dispatch or the mailbox rejects it before ownership transfer.
	admitted sync.WaitGroup
	// drainOnce starts the non-cancelable accepted-work drain at most once.
	drainOnce sync.Once
	// drained closes after every SEND admitted before closure completes dispatch.
	drained chan struct{}
	// closeOnce releases the mailbox only after the accepted-work drain. It
	// prevents Stop callers with expired release budgets from canceling work.
	closeOnce sync.Once
	// mailbox owns shard-local scheduling and worker execution.
	mailbox *workqueue.ShardedMailbox[asyncDispatchTask]
	// releaseTimeout bounds graceful mailbox pool release.
	releaseTimeout time.Duration
	// panicC records worker panics for package tests and diagnostics.
	panicC chan any
	// goroutines owns the bounded drain/release waiters outside the mailbox pool.
	goroutines *goruntimeregistry.Registry
}

func newSendExecutor(s *Server, opts gatewaytypes.RuntimeOptions) (*sendExecutor, error) {
	opts = gatewaytypes.NormalizeRuntimeOptions(opts)
	limits := gatewaySendBatchLimits(s)
	shards := asyncSendLogicalShardCount(opts.AsyncSendWorkers, opts.AsyncSendQueueCapacity, limits.maxRecords)
	e := &sendExecutor{
		server:         s,
		workers:        opts.AsyncSendWorkers,
		shards:         shards,
		capacity:       opts.AsyncSendQueueCapacity,
		shardCapacity:  asyncSendShardCapacity(opts.AsyncSendQueueCapacity, shards),
		shardQueued:    make([]atomic.Int64, shards),
		releaseTimeout: opts.AsyncPoolReleaseTimeout,
		panicC:         make(chan any, 1),
		drained:        make(chan struct{}),
		goroutines:     opts.Goroutines,
	}

	mailbox, err := workqueue.NewShardedMailbox[asyncDispatchTask](workqueue.ShardedMailboxConfig{
		Name:              "gateway-send",
		Goroutines:        opts.Goroutines,
		Task:              goruntimeregistry.TaskGatewayAsyncDispatch,
		Shards:            e.shards,
		Workers:           e.workers,
		QueueSizePerShard: e.shardCapacity,
		BatchMaxItems:     limits.maxRecords,
		BatchMaxWait:      limits.maxWait,
		ReleaseTimeout:    opts.AsyncPoolReleaseTimeout,
	}, e.handleMailboxBatch)
	if err != nil {
		return nil, err
	}
	e.mailbox = mailbox
	return e, nil
}

func gatewaySendBatchLimits(s *Server) asyncSendBatchLimits {
	if s == nil {
		return asyncSendBatchLimitsFromOptions(gatewaytypes.SessionOptions{})
	}
	return asyncSendBatchLimitsFromOptions(s.options.DefaultSession)
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

func asyncSendLogicalShardCount(workers, totalCapacity, minShardCapacity int) int {
	if workers <= 0 {
		workers = 1
	}
	if totalCapacity <= 0 {
		totalCapacity = 1
	}
	if workers == 1 {
		return 1
	}
	if minShardCapacity <= 0 {
		minShardCapacity = 1
	}
	maxShards := totalCapacity
	if totalCapacity >= minShardCapacity {
		maxShards = totalCapacity / minShardCapacity
	}
	// Division before multiplication keeps the calculation overflow-safe while
	// bounding allocated shard queues by the configured global item capacity.
	// A shard must still admit at least one configured SEND batch so increasing
	// workers cannot silently reduce one session's bounded burst capacity.
	if workers <= totalCapacity/asyncSendOrderingShardsPerWorker {
		return min(workers*asyncSendOrderingShardsPerWorker, maxShards)
	}
	return min(totalCapacity, maxShards)
}

func (e *sendExecutor) submit(state *sessionState, replyToken string, send *frame.SendPacket) bool {
	if e == nil || e.mailbox == nil || send == nil || e.shards <= 0 {
		return false
	}
	e.admissionMu.Lock()
	if e.closed.Load() {
		e.admissionMu.Unlock()
		return false
	}
	e.admitted.Add(1)
	e.admissionMu.Unlock()
	shard := asyncSendShardIndex(state, send, e.shards)
	if !e.reserve() {
		e.completeAdmission()
		return false
	}
	if !e.reserveShard(shard) {
		e.consume(1)
		e.completeAdmission()
		return false
	}

	task := asyncDispatchTask{
		state:      state,
		replyToken: replyToken,
		frame:      cloneAsyncSendFrame(send, stateOwnsDecodedFrames(state)),
		enqueuedAt: time.Now(),
	}
	if err := e.mailbox.SubmitHash(context.Background(), uint64(shard), task); err != nil {
		e.consumeShard(shard, 1)
		e.consume(1)
		e.completeAdmission()
		return false
	}
	return true
}

func (e *sendExecutor) stop() {
	if e == nil || e.mailbox == nil {
		return
	}
	// Stop reuses the terminal drain: accepted SEND work is never dropped just
	// because a caller's earlier deadline elapsed.
	ctx, cancel := context.WithTimeout(context.Background(), e.releaseTimeout)
	err := e.drain(ctx)
	cancel()
	if err != nil {
		e.closeMailboxAfterDrain()
		return
	}
	e.closeMailboxAfterDrain()
}

func (e *sendExecutor) closeMailboxAfterDrain() {
	if e == nil || e.mailbox == nil {
		return
	}
	e.closeOnce.Do(func() {
		goruntimeregistry.SafeGo(e.goroutines, goruntimeregistry.TaskGatewayAsyncDrain, func() {
			<-e.drained
			_ = e.mailbox.Close(context.Background())
			e.resetDepths()
		})
	})
}

// drain closes SEND admission and waits for every already accepted mailbox
// task. The background drain is deliberately independent from caller context:
// a timeout stops only that caller's wait, so a later DrainSends call can
// observe the same accepted work finishing without a reset or drop.
func (e *sendExecutor) drain(ctx context.Context) error {
	if e == nil || e.mailbox == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	e.admissionMu.Lock()
	e.closed.Store(true)
	e.admissionMu.Unlock()
	e.drainOnce.Do(func() {
		goruntimeregistry.SafeGo(e.goroutines, goruntimeregistry.TaskGatewayAsyncDrain, func() {
			e.admitted.Wait()
			close(e.drained)
		})
	})
	select {
	case <-e.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *sendExecutor) completeAdmission() {
	if e == nil {
		return
	}
	e.admitted.Done()
}

func (e *sendExecutor) depth() int {
	if e == nil {
		return 0
	}
	e.queueMu.Lock()
	defer e.queueMu.Unlock()
	return int(e.queued)
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
	e.queueMu.Lock()
	defer e.queueMu.Unlock()
	if e.queued < 0 || e.queued >= int64(e.capacity) {
		return false
	}
	e.queued++
	e.queueRevision = nextAsyncSendQueuePublicationRevision()
	return true
}

func (e *sendExecutor) reserveShard(shard int) bool {
	if e == nil || shard < 0 || shard >= len(e.shardQueued) {
		return false
	}
	for {
		queued := e.shardQueued[shard].Load()
		if queued < 0 || queued >= int64(e.shardCapacity) {
			return false
		}
		if e.shardQueued[shard].CompareAndSwap(queued, queued+1) {
			return true
		}
	}
}

func (e *sendExecutor) handleMailboxBatch(_ context.Context, batch workqueue.MailboxBatch[asyncDispatchTask]) error {
	if e == nil || len(batch.Items) == 0 {
		return nil
	}
	e.consumeShard(batch.Shard, len(batch.Items))
	e.consume(len(batch.Items))
	defer func() {
		for range batch.Items {
			e.completeAdmission()
		}
	}()
	e.dispatchMailboxBatch(batch.Items)
	return nil
}

func (e *sendExecutor) dispatchMailboxBatch(items []asyncDispatchTask) {
	limits := gatewaySendBatchLimits(e.server)
	if limits.maxRecords <= 0 {
		limits.maxRecords = 1
	}
	start := 0
	byteCount := 0
	for i, task := range items {
		taskBytes := asyncDispatchTaskByteCount(task)
		if i > start && limits.maxBytes > 0 && byteCount+taskBytes > limits.maxBytes {
			e.dispatchBatchSafely(items[start:i])
			start = i
			byteCount = 0
		}
		byteCount += taskBytes
		if i-start+1 >= limits.maxRecords {
			e.dispatchBatchSafely(items[start : i+1])
			start = i + 1
			byteCount = 0
		}
	}
	if start < len(items) {
		e.dispatchBatchSafely(items[start:])
	}
}

func (e *sendExecutor) dispatchBatchSafely(batch []asyncDispatchTask) {
	if e == nil || len(batch) == 0 {
		return
	}
	defer func() {
		if v := recover(); v != nil {
			e.recordPanic(v, firstAsyncDispatchTask(batch))
		}
	}()
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
	e.queueMu.Lock()
	defer e.queueMu.Unlock()
	e.queued -= int64(count)
	if e.queued < 0 {
		e.queued = 0
	}
	e.queueRevision = nextAsyncSendQueuePublicationRevision()
}

func (e *sendExecutor) consumeShard(shard int, count int) {
	if e == nil || count <= 0 || shard < 0 || shard >= len(e.shardQueued) {
		return
	}
	remaining := e.shardQueued[shard].Add(-int64(count))
	if remaining >= 0 {
		return
	}
	e.shardQueued[shard].Add(-remaining)
}

func (e *sendExecutor) resetDepths() {
	if e == nil {
		return
	}
	e.queueMu.Lock()
	e.queued = 0
	e.queueRevision = nextAsyncSendQueuePublicationRevision()
	e.queueMu.Unlock()
	for i := range e.shardQueued {
		e.shardQueued[i].Store(0)
	}
}

// queueSnapshot captures occupancy and its ordering revision under one lock.
func (e *sendExecutor) queueSnapshot() gatewaytypes.AsyncSendQueueEvent {
	if e == nil {
		return gatewaytypes.AsyncSendQueueEvent{}
	}
	e.queueMu.Lock()
	defer e.queueMu.Unlock()
	return gatewaytypes.AsyncSendQueueEvent{
		Depth:    int(e.queued),
		Capacity: e.capacity,
		Revision: e.queueRevision,
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
