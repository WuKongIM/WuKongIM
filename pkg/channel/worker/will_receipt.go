package worker

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

// StoreWillReceiptTask carries the reactor's captured HW, never a caller bound.
type StoreWillReceiptTask struct {
	Request          ch.WillReceiptRequest
	CommittedThrough uint64
}

// StoreWillReceiptResult preserves the captured boundary for completion fencing.
type StoreWillReceiptResult struct{ Result ch.WillReceiptResult }

func runStoreWillReceipt(ctx context.Context, deps Deps, task Task) Result {
	out := Result{Kind: task.Kind, Fence: task.Fence}
	q := task.StoreWillReceipt
	if deps.Stores == nil || q == nil || !q.Request.Valid() || task.Fence.ChannelKey != ch.ChannelKeyForID(q.Request.ChannelID) {
		return invalidResult(task)
	}
	if err := ctx.Err(); err != nil {
		out.Err = err
		return out
	}
	lease, err := deps.Stores.ChannelStore(task.Fence.ChannelKey, q.Request.ChannelID)
	if err != nil {
		out.Err = err
		return out
	}
	if lease == nil {
		return invalidResult(task)
	}
	defer func() { _ = lease.Close() }()
	reader, ok := lease.(store.WillReceiptLookup)
	if !ok {
		return invalidResult(task)
	}
	if err = lease.StoreCheckpoint(ctx, ch.Checkpoint{HW: q.CommittedThrough}); err != nil {
		out.Err = err
		return out
	}
	receipt, found, err := reader.LookupWillReceipt(ctx, q.Request.FromUID, q.Request.ServerWillKey)
	if err != nil {
		out.Err = err
		return out
	}
	if err = ctx.Err(); err != nil {
		out.Err = err
		return out
	}
	result := ch.WillReceiptResult{CommittedThrough: q.CommittedThrough, Found: found, Receipt: receipt}
	if !result.Valid() {
		out.Err = ch.ErrLogConflict
		return out
	}
	out.StoreWillReceipt = &StoreWillReceiptResult{Result: result}
	return out
}
