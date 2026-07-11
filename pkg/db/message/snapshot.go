package message

import (
	"bytes"
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
)

// LoadSnapshotPayload loads the latest durable snapshot payload.
func (l *ChannelLog) LoadSnapshotPayload(ctx context.Context) ([]byte, bool, error) {
	if err := l.beginUse(); err != nil {
		return nil, false, err
	}
	defer l.endUse()
	return l.loadSnapshotPayload(ctx)
}

func (l *ChannelLog) loadSnapshotPayload(ctx context.Context) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	value, ok, err := l.db.engine.Get(encodeSnapshotKey(l.key))
	if err != nil || !ok {
		return nil, ok, err
	}
	return value, true, nil
}

// StoreSnapshotPayload stores the latest durable snapshot payload.
func (l *ChannelLog) StoreSnapshotPayload(ctx context.Context, payload []byte) error {
	if err := l.beginUse(); err != nil {
		return err
	}
	defer l.endUse()
	return l.storeSnapshotPayload(ctx, payload)
}

func (l *ChannelLog) storeSnapshotPayload(ctx context.Context, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	batch := l.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(encodeSnapshotKey(l.key), append([]byte(nil), payload...)); err != nil {
		return err
	}
	if err := l.stageCatalog(batch); err != nil {
		return err
	}
	return batch.Commit(true)
}

// InstallSnapshot atomically stores snapshot payload, checkpoint, and epoch history.
func (l *ChannelLog) InstallSnapshot(ctx context.Context, snap Snapshot, checkpoint Checkpoint, point EpochPoint) (uint64, error) {
	if err := l.beginUse(); err != nil {
		return 0, err
	}
	defer l.endUse()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	l.appendMu.Lock()
	defer l.appendMu.Unlock()
	l.checkpointMu.Lock()
	defer l.checkpointMu.Unlock()
	leo, err := l.loadLEOLocked(ctx)
	if err != nil {
		return 0, err
	}
	return l.installSnapshotLocked(ctx, snap, checkpoint, point, leo)
}

func (l *ChannelLog) installSnapshotLocked(ctx context.Context, snap Snapshot, checkpoint Checkpoint, point EpochPoint, leo uint64) (uint64, error) {
	if snap.EndOffset > leo {
		return 0, fmt.Errorf("%w: snapshot end %d > leo %d", dberrors.ErrCorruptState, snap.EndOffset, leo)
	}
	if checkpoint.LogStartOffset != snap.EndOffset || checkpoint.HW != snap.EndOffset {
		return 0, dberrors.ErrCorruptState
	}
	if checkpoint.Epoch != snap.Epoch || point.Epoch != snap.Epoch || point.StartOffset > snap.EndOffset {
		return 0, dberrors.ErrCorruptState
	}
	if err := l.validateCheckpointMonotonicLocked(ctx, checkpoint, snap.EndOffset, leo); err != nil {
		return 0, err
	}

	batch := l.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(encodeSnapshotKey(l.key), append([]byte(nil), snap.Payload...)); err != nil {
		return 0, err
	}
	if err := batch.Set(encodeCheckpointKey(l.key), encodeCheckpoint(checkpoint)); err != nil {
		return 0, err
	}
	prefix := encodeHistoryPrefix(l.key)
	span := keycodec.NewPrefixSpan(prefix)
	if snap.EndOffset != ^uint64(0) {
		if err := batch.DeleteRange(engine.Span{Start: encodeHistoryOffsetKey(l.key, snap.EndOffset+1), End: span.End}); err != nil {
			return 0, err
		}
	}
	if err := l.writeHistoryPoint(batch, point); err != nil {
		return 0, err
	}
	if err := l.stageCatalog(batch); err != nil {
		return 0, err
	}
	if err := batch.Commit(true); err != nil {
		return 0, err
	}
	payload, ok, err := l.loadSnapshotPayload(ctx)
	if err != nil {
		return 0, err
	}
	if !ok || !bytes.Equal(payload, snap.Payload) {
		return 0, dberrors.ErrCorruptState
	}
	return snap.EndOffset, nil
}
