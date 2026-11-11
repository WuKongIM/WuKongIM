package message

import (
	"context"
	"errors"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

// RegisterMQTTStorage publishes this node's reconstructed debt before write
// readiness. It does not require other nodes to finish or grant new capacity:
// existing responsibilities must remain recoverable even above lowered limits.
// Periodic maintenance still owns refunds and subsequent grant reconciliation.
func (e *Engine) RegisterMQTTStorage(ctx context.Context) error {
	if e == nil || e.db == nil {
		return channel.ErrClosed
	}
	if err := ctxErr(ctx); err != nil {
		return err
	}
	b := e.db.mqttStorage
	if b == nil {
		return nil
	}
	if !b.mu.TryLock() {
		return channel.ErrBackpressured
	}
	defer b.mu.Unlock()
	defer b.observe()
	if b.readyErr != nil {
		return b.readyErr
	}
	err := b.register(ctx)
	// The ledger records initial debt even when it refuses grant growth. Only
	// the exact initialized receipt sets registered; uncertainty remains closed.
	if b.registered && (err == nil || errors.Is(err, channel.ErrBackpressured)) {
		return nil
	}
	if err != nil {
		return err
	}
	return channel.ErrBackpressured
}
