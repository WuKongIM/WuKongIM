package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// TruncateFrom removes all message rows and indexes at or after fromSeq.
func (l *ChannelLog) TruncateFrom(ctx context.Context, fromSeq uint64) error {
	if err := l.beginUse(); err != nil {
		return err
	}
	defer l.endUse()
	if err := ctx.Err(); err != nil {
		return err
	}
	if fromSeq == 0 {
		fromSeq = 1
	}

	l.appendMu.Lock()
	defer l.appendMu.Unlock()
	l.checkpointMu.Lock()
	defer l.checkpointMu.Unlock()

	leo, err := l.loadLEOLocked(ctx)
	if err != nil {
		return err
	}
	if fromSeq > leo {
		return nil
	}
	messages, err := l.readForward(ctx, fromSeq, 0, ReadOptions{})
	if err != nil {
		return err
	}

	batch := l.db.engine.NewBatch()
	defer batch.Close()
	if err := l.channelEntry.stageTruncateDurableProposals(ctx, batch, fromSeq-1); err != nil {
		return err
	}
	for _, msg := range messages {
		if err := l.stageDeleteMessage(batch, msg); err != nil {
			return err
		}
	}
	if err := l.stageCatalog(batch); err != nil {
		return err
	}
	storageChange, err := l.channelEntry.stageMQTTStorageReplacement(ctx, batch, fromSeq-1, nil, nil)
	if err != nil {
		return err
	}
	defer storageChange.cancel()
	storageChange.submitted = true
	if err := batch.Commit(true); err != nil {
		return err
	}
	l.leo.Store(fromSeq - 1)
	l.loaded.Store(true)
	l.clearDurableProposalTailLocked()
	return storageChange.finish(ctx)
}

func (l *ChannelLog) stageDeleteMessage(batch *engine.Batch, msg Message) error {
	return l.stageMessageDeletion(batch, msg, false)
}

// Prefix retention preserves compact Will receipts; suffix rollback removes them.
func (l *ChannelLog) stageMessageDeletion(batch *engine.Batch, msg Message, preserveWill bool) error {
	identity, err := rowIdempotencyKey(msg.FromUID, msg.ClientMsgNo, msg.PublicationMetadata)
	if err != nil {
		return err
	}
	if identity.ServerWillKey != "" && preserveWill {
		if err := l.stageRetainedWillReceipt(batch, msg, identity); err != nil {
			return err
		}
	}
	if err := batch.Delete(nonBusinessIndexKey(l.key, msg.MessageSeq)); err != nil {
		return err
	}
	if err := batch.Delete(encodeMessageRowKey(l.key, msg.MessageSeq, messageHeaderFamilyID)); err != nil {
		return err
	}
	if msg.MessageID != 0 {
		if err := batch.Delete(encodeGlobalMessageIDIndexKey(msg.MessageID)); err != nil {
			return err
		}
	}
	if msg.ClientMsgNo != "" && (msg.FromUID == "" || identity.ServerWillKey != "") {
		if err := batch.Delete(encodeMessageClientMsgNoIndexKey(l.key, msg.ClientMsgNo, msg.MessageSeq)); err != nil {
			return err
		}
	}
	if identity.ServerWillKey != "" && !preserveWill {
		if err := batch.Delete(willReceiptKey(l.key, identity)); err != nil {
			return err
		}
	}
	if identity.valid() {
		if err := batch.Delete(l.idempotencyStorageKey(identity)); err != nil {
			return err
		}
	}
	if msg.FromUID != "" {
		if err := batch.Delete(encodeMessageSenderSeqIndexKey(l.key, msg.FromUID, msg.MessageSeq)); err != nil {
			return err
		}
	}
	return nil
}
