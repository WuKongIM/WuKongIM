package core

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/workqueue"
)

const asyncSendPanicValueMaxLen = 256

// sendExecutor admits SEND frames into a bounded workqueue-backed shard mailbox.
type sendExecutor struct {
	// server owns session and gateway state needed by send tasks.
	server *Server
	// workers is the normalized send worker count and shard count.
	workers int
	// capacity is the normalized maximum admitted send backlog.
	capacity int
	// shardCapacity is the per-shard mailbox admission bound.
	shardCapacity int
	// queued tracks SEND tasks accepted by gateway but not yet entering dispatch.
	queued atomic.Int64
	// shardQueued tracks per-shard accepted tasks before dispatch begins.
	shardQueued []atomic.Int64
	// closed prevents new send admission after shutdown.
	closed atomic.Bool
	// mailbox owns shard-local scheduling and worker execution.
	mailbox *workqueue.ShardedMailbox[asyncDispatchTask]
	// releaseTimeout bounds graceful mailbox pool release.
	releaseTimeout time.Duration
	// panicC records worker panics for package tests and diagnostics.
	panicC chan any
}

func newSendExecutor(s *Server, opts gatewaytypes.RuntimeOptions) (*sendExecutor, error) {
	opts = gatewaytypes.NormalizeRuntimeOptions(opts)
	e := &sendExecutor{
		server:         s,
		workers:        opts.AsyncSendWorkers,
		capacity:       opts.AsyncSendQueueCapacity,
		shardCapacity:  asyncSendShardCapacity(opts.AsyncSendQueueCapacity, opts.AsyncSendWorkers),
		shardQueued:    make([]atomic.Int64, opts.AsyncSendWorkers),
		releaseTimeout: opts.AsyncPoolReleaseTimeout,
		panicC:         make(chan any, 1),
	}

	limits := gatewaySendBatchLimits(s)
	mailbox, err := workqueue.NewShardedMailbox[asyncDispatchTask](workqueue.ShardedMailboxConfig{
		Name:              "gateway-send",
		Goroutines:        opts.Goroutines,
		Task:              goruntimeregistry.TaskGatewayAsyncDispatch,
		Shards:            e.workers,
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

func (e *sendExecutor) submit(state *sessionState, replyToken string, send *frame.SendPacket) bool {
	if e == nil || e.mailbox == nil || send == nil || e.closed.Load() || e.workers <= 0 {
		return false
	}
	shard := asyncSendShardIndex(state, send, e.workers)
	if !e.reserve() {
		return false
	}
	if !e.reserveShard(shard) {
		e.consume(1)
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
		return false
	}
	return true
}

func (e *sendExecutor) stop() {
	if e == nil || e.mailbox == nil {
		return
	}
	e.closed.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), e.releaseTimeout)
	defer cancel()
	if err := e.mailbox.Close(ctx); err != nil {
		e.resetDepths()
	}
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
	remaining := e.queued.Add(-int64(count))
	if remaining >= 0 {
		return
	}
	e.queued.Add(-remaining)
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
	e.queued.Store(0)
	for i := range e.shardQueued {
		e.shardQueued[i].Store(0)
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
