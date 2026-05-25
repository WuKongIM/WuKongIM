package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// TruncateFrom removes all message rows and indexes at or after fromSeq.
func (l *ChannelLog) TruncateFrom(ctx context.Context, fromSeq uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l == nil || l.db == nil || l.db.engine == nil {
		return dberrors.ErrClosed
	}
	if fromSeq == 0 {
		fromSeq = 1
	}

	l.appendMu.Lock()
	defer l.appendMu.Unlock()

	leo, err := l.loadLEOLocked(ctx)
	if err != nil {
		return err
	}
	if fromSeq > leo {
		return nil
	}
	messages, err := l.Read(ctx, fromSeq, ReadOptions{})
	if err != nil {
		return err
	}

	batch := l.db.engine.NewBatch()
	defer batch.Close()
	for _, msg := range messages {
		if err := l.stageDeleteMessage(batch, msg); err != nil {
			return err
		}
	}
	if err := l.stageCatalog(batch); err != nil {
		return err
	}
	if err := batch.Commit(true); err != nil {
		return err
	}
	l.leo.Store(fromSeq - 1)
	l.loaded.Store(true)
	return nil
}

func (l *ChannelLog) stageDeleteMessage(batch *engine.Batch, msg Message) error {
	if err := batch.Delete(encodeMessageRowKey(l.key, msg.MessageSeq, messageHeaderFamilyID)); err != nil {
		return err
	}
	if err := batch.Delete(encodeMessageRowKey(l.key, msg.MessageSeq, messagePayloadFamilyID)); err != nil {
		return err
	}
	if msg.MessageID != 0 {
		if err := batch.Delete(encodeMessageIDIndexKey(l.key, msg.MessageID)); err != nil {
			return err
		}
	}
	if msg.ClientMsgNo != "" {
		if err := batch.Delete(encodeMessageClientMsgNoIndexKey(l.key, msg.ClientMsgNo, msg.MessageSeq)); err != nil {
			return err
		}
	}
	if msg.FromUID != "" && msg.ClientMsgNo != "" {
		if err := batch.Delete(encodeMessageIdempotencyIndexKey(l.key, msg.FromUID, msg.ClientMsgNo)); err != nil {
			return err
		}
	}
	return nil
}
