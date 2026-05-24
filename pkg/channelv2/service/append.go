package service

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
)

// appendCancelCleanupTimeout bounds best-effort waiter cleanup after caller cancellation.
const appendCancelCleanupTimeout = time.Second

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
	releaseAppend, err := c.group.ReserveAppend(key)
	if err != nil {
		return ch.AppendBatchResult{}, err
	}
	defer releaseAppend()
	if err := c.ensureAppendChannelState(ctx, key, req.ChannelID); err != nil {
		return ch.AppendBatchResult{}, err
	}
	opID := c.group.NextOpID()
	future, err := c.group.Submit(ctx, key, reactor.Event{Kind: reactor.EventAppend, Key: key, Append: req, Context: ctx, OpID: opID})
	if err != nil {
		return ch.AppendBatchResult{}, err
	}
	resultCh := make(chan reactor.Result, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := future.Await(context.Background())
		resultCh <- result
		errCh <- err
	}()
	select {
	case result := <-resultCh:
		err := <-errCh
		if err != nil {
			return ch.AppendBatchResult{}, err
		}
		return result.AppendBatch, nil
	case <-ctx.Done():
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
}

func (c *cluster) ensureAppendChannelState(ctx context.Context, key ch.ChannelKey, id ch.ChannelID) error {
	loaded, err := c.group.HasChannelState(ctx, key)
	if err != nil {
		return err
	}
	if loaded {
		return nil
	}
	if c.metaResolver == nil {
		return ch.ErrChannelNotFound
	}
	meta, err := c.metaResolver.ResolveChannelMeta(ctx, id)
	if err != nil {
		return err
	}
	if meta.ID == (ch.ChannelID{}) {
		meta.ID = id
	}
	if meta.Key == "" {
		meta.Key = ch.ChannelKeyForID(meta.ID)
	}
	if meta.Key != key {
		return ch.ErrStaleMeta
	}
	return c.applyMeta(ctx, meta)
}
