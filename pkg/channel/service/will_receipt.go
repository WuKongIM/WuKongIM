package service

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/reactor"
)

// ReadWillReceipt reserves runtime ownership through a fenced checkpoint read.
// The cluster entry must surround this facade with fresh Slot authority checks.
func (c *cluster) ReadWillReceipt(ctx context.Context, q ch.WillReceiptRequest) (ch.WillReceiptResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !q.Valid() {
		return ch.WillReceiptResult{}, ch.ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return ch.WillReceiptResult{}, err
	}
	key := ch.ChannelKeyForID(q.ChannelID)
	release, err := c.group.ReserveAppend(key)
	if err != nil {
		return ch.WillReceiptResult{}, err
	}
	defer release()
	future, err := c.group.Submit(ctx, key, reactor.Event{Kind: reactor.EventWillReceipt, Key: key, Context: ctx, WillReceipt: q})
	if err != nil {
		return ch.WillReceiptResult{}, err
	}
	result, err := future.Await(ctx)
	if err != nil {
		return ch.WillReceiptResult{}, err
	}
	if err = ctx.Err(); err != nil {
		return ch.WillReceiptResult{}, err
	}
	return result.WillReceipt, nil
}

var _ ch.WillReceiptReader = (*cluster)(nil)
