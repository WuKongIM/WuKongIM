package service

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
)

// appendCancelCleanupTimeout bounds best-effort waiter cleanup after caller cancellation.
const appendCancelCleanupTimeout = time.Second

type appendStageObserver interface {
	ObserveChannelAppendStage(stage string, result string, d time.Duration)
}

func (c *cluster) Append(ctx context.Context, req ch.AppendRequest) (ch.AppendResult, error) {
	batch, err := c.AppendBatch(ctx, ch.AppendBatchRequest{ChannelID: req.ChannelID, Messages: []ch.Message{req.Message}, CommitMode: req.CommitMode, ExpectedChannelEpoch: req.ExpectedChannelEpoch, ExpectedLeaderEpoch: req.ExpectedLeaderEpoch})
	if err != nil {
		return ch.AppendResult{}, err
	}
	if len(batch.Items) == 0 {
		return ch.AppendResult{}, nil
	}
	item := batch.Items[0]
	if item.Err != nil {
		return ch.AppendResult{}, item.Err
	}
	return ch.AppendResult{MessageID: item.MessageID, MessageSeq: item.MessageSeq, Message: item.Message}, nil
}

func (c *cluster) AppendBatch(ctx context.Context, req ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	key := ch.ChannelKeyForID(req.ChannelID)
	started := time.Now()
	releaseAppend, err := c.group.ReserveAppend(key)
	c.observeAppendStage("runtime_append_reserve_wait", err, time.Since(started))
	if err != nil {
		return ch.AppendBatchResult{}, err
	}
	defer releaseAppend()
	opID := c.group.NextOpID()
	started = time.Now()
	future, err := c.group.Submit(ctx, key, reactor.Event{Kind: reactor.EventAppend, Key: key, Append: req, Context: ctx, OpID: opID})
	c.observeAppendStage("runtime_append_submit", err, time.Since(started))
	if err != nil {
		return ch.AppendBatchResult{}, err
	}
	started = time.Now()
	result, completed := awaitAppendFuture(ctx, future)
	if completed {
		c.observeAppendStage("runtime_append_wait", result.Err, time.Since(started))
		if result.Err != nil {
			return ch.AppendBatchResult{}, result.Err
		}
		return result.AppendBatch, nil
	}
	c.observeAppendStage("runtime_append_wait", ctx.Err(), time.Since(started))
	// Cancellation after mailbox admission is cooperative; durable writes already started are not cancelled.
	cleanup, err := c.group.Submit(context.Background(), key, reactor.Event{Kind: reactor.EventCancelWaiter, Key: key, CancelOp: opID, CancelErr: ctx.Err()})
	if err == nil {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), appendCancelCleanupTimeout)
		_, err = cleanup.Await(cleanupCtx)
		cleanupCancel()
	}
	if err != nil {
		future.Complete(reactor.Result{Err: ctx.Err()})
	}
	return ch.AppendBatchResult{}, ctx.Err()
}

func awaitAppendFuture(ctx context.Context, future *reactor.Future) (reactor.Result, bool) {
	select {
	case <-future.Done():
		return future.Result(), true
	default:
	}
	select {
	case <-future.Done():
		return future.Result(), true
	case <-ctx.Done():
		select {
		case <-future.Done():
			return future.Result(), true
		default:
			return reactor.Result{}, false
		}
	}
}

func (c *cluster) observeAppendStage(stage string, err error, d time.Duration) {
	if c == nil || c.observer == nil {
		return
	}
	observer, ok := c.observer.(appendStageObserver)
	if !ok {
		return
	}
	if d < 0 {
		d = 0
	}
	result := "ok"
	if err != nil {
		result = "err"
	}
	observer.ObserveChannelAppendStage(stage, result, d)
}
