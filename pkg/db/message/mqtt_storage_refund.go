package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/commit"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

// mqttStoragePendingRefund is node-bounded evidence for one unknown physical
// retirement or cancellation. Its debit stays charged until exact atomic
// deletion proof is observed, independently of Channel handle eviction.
type mqttStoragePendingRefund struct {
	db             *MessageDB
	key            ChannelKey
	proof          MQTTReplayRetirementProof
	through, bytes uint64
	// nonce selects cancellation evidence; zero selects retirement evidence.
	nonce    uint64
	manifest DurableProposalManifest
	id       ChannelID
	// preparation can transfer its retained debit to proved durable charge rows.
	preparation bool
}

// reconcileRefund runs under the budget mutex. The independently verified
// committed marker must cover the exact attempted deletion before RAM is debited.
func (b *mqttStorageBudget) reconcileRefund() error {
	p := b.pendingRefund
	if p == nil {
		return nil
	}
	if p.nonce != 0 {
		t, found, err := loadMQTTStorageTicket(p.db.engine, p.key)
		if err != nil {
			return err
		}
		if p.preparation && found && t.Nonce == p.nonce && t.Manifest == p.manifest && (t.Phase == mqttStoragePrepared || t.Phase == mqttStorageConsumed) {
			var total uint64
			for position := p.manifest.BaseOffset + 1; position <= p.manifest.LastOffset; position++ {
				k := mqttStorageChargeKey(p.key, position)
				v, present, err := p.db.engine.Get(k)
				if err != nil {
					return err
				}
				if present {
					n, err := decodeMQTTStorageCharge(k, v)
					if err != nil {
						return err
					}
					if n > p.bytes-total {
						return dberrors.ErrCorruptState
					}
					total += n
				}
				if position == p.manifest.LastOffset {
					break
				}
			}
			if total != p.bytes {
				return dberrors.ErrCorruptState
			}
			b.pendingRefund = nil
			return nil
		}
		if !found || t.Phase != mqttStorageCanceled || t.Nonce != p.nonce || t.Manifest != p.manifest {
			return nil
		}
		for position := p.manifest.BaseOffset + 1; position <= p.manifest.LastOffset; position++ {
			_, present, err := p.db.engine.Get(mqttStorageChargeKey(p.key, position))
			if err != nil {
				return err
			}
			if present {
				return nil
			}
			if position == p.manifest.LastOffset {
				break
			}
		}
	} else {
		r, found, err := loadMQTTReplayRetired(p.db.engine, p.key)
		if err != nil {
			return err
		}
		if !found || r.deletedThrough < p.through || !validMQTTRetirementAdvance(p.proof.Retirement, r.proof.Retirement) {
			return nil
		}
	}
	if p.bytes > b.used {
		b.readyErr = dberrors.ErrCorruptState
		return b.readyErr
	}
	b.used -= p.bytes
	b.pendingRefund = nil
	b.event("retired")
	return nil
}

// commitMQTTStorageRetirement serializes the physical deletion and its uncertain
// debit. Existing exact retries can settle it; unrelated GC never overwrites it.
func (e *channelEntry) commitMQTTStorageRetirement(ctx context.Context, batch *engine.Batch, proof MQTTReplayRetirementProof, through, n uint64) error {
	b := e.db.mqttStorage
	if b == nil {
		return batch.Commit(true)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	defer b.observe()
	if err := b.reconcileRefund(); err != nil {
		return err
	}
	if old := b.pendingRefund; old != nil {
		if old.nonce != 0 || old.key != e.key || old.proof != proof || old.through != through {
			return channel.ErrBackpressured
		}
		n = max(n, old.bytes)
	}
	if n > b.used {
		return dberrors.ErrCorruptState
	}
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if err := batch.Commit(true); err != nil {
		b.pendingRefund = &mqttStoragePendingRefund{db: e.db, key: e.key, proof: proof, through: through, bytes: n}
		return err
	}
	if n > 0 {
		// gofail: var wkMQTTStorageRetirementAfterCommit bool
		// if wkMQTTStorageRetirementAfterCommit { b.pendingRefund=&mqttStoragePendingRefund{db:e.db,key:e.key,proof:proof,through:through,bytes:n}; return context.DeadlineExceeded }
	}
	b.used -= n
	b.pendingRefund = nil
	if n > 0 {
		b.event("retired")
	}
	return nil
}

// commitMQTTStorageCancellation transfers an attempted debit to the node owner
// before returning any unknown result. submitted distinguishes pre-commit refusal
// from an attempt whose uncertainty must outlive the Channel handle.
func (e *channelEntry) commitMQTTStorageCancellation(ctx context.Context, batch *engine.Batch, manifest DurableProposalManifest, nonce, n uint64) (submitted bool, err error) {
	b := e.db.mqttStorage
	b.mu.Lock()
	defer b.mu.Unlock()
	defer b.observe()
	if err = b.reconcileRefund(); err != nil {
		return false, err
	}
	if old := b.pendingRefund; old != nil {
		if old.key != e.key || old.nonce != nonce || old.manifest != manifest {
			return false, channel.ErrBackpressured
		}
		n = max(n, old.bytes)
	}
	if n > b.used {
		return false, dberrors.ErrCorruptState
	}
	if err = ctxErr(ctx); err != nil {
		return false, err
	}
	pending := &mqttStoragePendingRefund{db: e.db, key: e.key, id: e.id, manifest: manifest, nonce: nonce, bytes: n}
	if err = batch.Commit(true); err != nil {
		b.pendingRefund = pending
		return true, err
	}
	if n > 0 {
		// gofail: var wkMQTTStorageCancelAfterCommit bool
		// if wkMQTTStorageCancelAfterCommit { b.pendingRefund=pending; return true, context.DeadlineExceeded }
	}
	b.used -= n
	b.pendingRefund = nil
	if n > 0 {
		b.event("retired")
	}
	return true, nil
}

// commitMQTTStoragePreparation retains unknown debit on the node before a
// Channel handle can close. The existing cancellation owner can then settle it.
func (e *channelEntry) commitMQTTStoragePreparation(ctx context.Context, batch *engine.Batch, manifest DurableProposalManifest, nonce, n uint64) (submitted bool, err error) {
	b := e.db.mqttStorage
	b.mu.Lock()
	defer b.mu.Unlock()
	defer b.observe()
	if err = b.reconcileRefund(); err != nil {
		return false, err
	}
	if b.pendingRefund != nil {
		return false, channel.ErrBackpressured
	}
	if err = ctxErr(ctx); err != nil {
		return false, err
	}
	pending := &mqttStoragePendingRefund{db: e.db, key: e.key, id: e.id, manifest: manifest, nonce: nonce, bytes: n, preparation: true}
	if n > 0 {
		// gofail: var wkMQTTStoragePrepareBeforeCommit bool
		// if wkMQTTStoragePrepareBeforeCommit { b.pendingRefund=pending; return true, context.DeadlineExceeded }
	}
	if err = batch.Commit(true); err != nil {
		if n > 0 {
			b.pendingRefund = pending
		}
		return true, err
	}
	return true, nil
}

// retryMQTTStorageCancellation runs on the existing node periodic owner. It
// skips busy Channel/budget locks and transfers physical work to the existing
// commit owner. Caller expiry never releases its canonical pins or charged proof.
func (e *Engine) retryMQTTStorageCancellation(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	b := e.db.mqttStorage
	if !b.mu.TryLock() {
		return channel.ErrBackpressured
	}
	err := b.reconcileRefund()
	p := b.pendingRefund
	b.mu.Unlock()
	if err != nil || p == nil || p.nonce == 0 {
		return err
	}
	s, err := e.ForChannel(channel.ChannelKey(p.key), channel.ChannelID{ID: p.id.ID, Type: p.id.Type})
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.beginUse(); err != nil {
		return err
	}
	defer s.endUse()
	if !s.log.appendMu.TryLock() {
		return channel.ErrBackpressured
	}
	if !s.log.checkpointMu.TryLock() {
		s.log.appendMu.Unlock()
		return channel.ErrBackpressured
	}
	transferred := false
	defer func() {
		if !transferred {
			s.log.checkpointMu.Unlock()
			s.log.appendMu.Unlock()
		}
	}()
	if err := ctxErr(ctx); err != nil {
		return err
	}
	old, found, err := loadMQTTStorageTicket(s.log.db.engine, s.log.key)
	if err != nil {
		return toChannelError(err)
	}
	out, charges, err := s.planMQTTStorageCancellationLocked(p.manifest, p.nonce, old, found)
	if err != nil || out.Canceled || out.Prepared {
		return err
	}
	if !b.mu.TryLock() {
		return channel.ErrBackpressured
	}
	err = b.reconcileRefund()
	current := b.pendingRefund == p
	b.mu.Unlock()
	if err != nil || !current {
		return err
	}
	e.mu.Lock()
	committer := e.committer
	e.mu.Unlock()
	if committer == nil {
		return channel.ErrClosed // No synchronous physical fallback on the health owner.
	}
	entry := s.log.channelEntry
	// The ownership constructor releases these locks even if retaining a pin fails.
	transferred = true
	ownership, err := newCommitOwnership(entry.db.registry, []*channelEntry{entry}, []*channelEntry{entry})
	if err != nil {
		return toChannelError(err)
	}
	request := commit.Request{
		Lane:      commit.Lane{Name: commitLaneFollowerApply, Priority: commit.PriorityNormal},
		Partition: string(p.key) + ":mqtt_storage_cancel", Records: len(charges) + 1,
		Build: func(batch *engine.Batch) error {
			_, err := stageMQTTStorageCancellation(batch, p.key, p.manifest, p.nonce, charges)
			return err
		},
		Publish: func() error {
			b.mu.Lock()
			defer b.mu.Unlock()
			defer b.observe()
			return b.reconcileRefund()
		},
		Finalize: ownership.finalize,
	}
	return toChannelError(committer.Submit(ctx, request))
}

// guardMQTTStorageReplacement prevents two physical refund owners from
// claiming the same Channel charges. Callers already hold its append lock.
func (b *mqttStorageBudget) guardMQTTStorageReplacement(key ChannelKey) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.reconcileRefund(); err != nil {
		return err
	}
	if p := b.pendingRefund; p != nil && p.key == key {
		return channel.ErrBackpressured
	}
	return nil
}
