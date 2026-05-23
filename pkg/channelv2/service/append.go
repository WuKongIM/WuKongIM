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
	future, err := c.group.Submit(ctx, key, reactor.Event{Kind: reactor.EventAppend, Key: key, Append: req})
	if err != nil {
		return ch.AppendBatchResult{}, err
	}
	result, err := future.Await(ctx)
	if err != nil {
		return ch.AppendBatchResult{}, err
	}
	return result.AppendBatch, nil
}
