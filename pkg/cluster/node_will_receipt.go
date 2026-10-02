package cluster

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// ReadChannelWillReceipt routes immutable publication evidence through current
// Slot and recovered Channel authority. Absence grants no retry authorization.
func (n *Node) ReadChannelWillReceipt(ctx context.Context, q ch.WillReceiptRequest) (ch.WillReceiptResult, error) {
	if err := ctxErr(ctx); err != nil {
		return ch.WillReceiptResult{}, err
	}
	if err := n.ensureForeground(); err != nil {
		return ch.WillReceiptResult{}, err
	}
	if n.channels == nil {
		return ch.WillReceiptResult{}, ErrNotStarted
	}
	reader, ok := n.channels.(ch.WillReceiptReader)
	if !ok {
		return ch.WillReceiptResult{}, ch.ErrInvalidConfig
	}
	return reader.ReadWillReceipt(ctx, q)
}
