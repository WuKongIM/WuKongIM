package service

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
)

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
			_, _ = cleanup.Await(context.Background())
		}
		return ch.AppendBatchResult{}, ctx.Err()
	}
}
