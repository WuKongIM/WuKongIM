package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// ApplyFetch stores fetched records and optional system state in one batch.
func (l *ChannelLog) ApplyFetch(ctx context.Context, req ApplyFetchRequest) (AppendResult, error) {
	if err := ctx.Err(); err != nil {
		return AppendResult{}, err
	}
	if l == nil || l.db == nil || l.db.engine == nil {
		return AppendResult{}, dberrors.ErrClosed
	}

	l.appendMu.Lock()
	defer l.appendMu.Unlock()

	rows, result, err := l.prepareAppendRowsLocked(ctx, req.Records, AppendOptions{
		Mode:    AppendTrustedContiguous,
		BaseSeq: req.BaseSeq,
	})
	if err != nil {
		return AppendResult{}, err
	}
	visibleLEO := l.leo.Load()
	if result.Count > 0 {
		visibleLEO = result.LastSeq
	}
	if req.Checkpoint != nil {
		if err := l.validateCheckpointMonotonic(ctx, *req.Checkpoint, visibleLEO, visibleLEO); err != nil {
			return AppendResult{}, err
		}
	}
	shouldWriteEpoch := false
	if req.EpochPoint != nil {
		points, ok, err := l.LoadHistory(ctx)
		if err != nil {
			return AppendResult{}, err
		}
		if !ok {
			points = nil
		}
		shouldWriteEpoch, err = shouldAppendHistoryPoint(points, *req.EpochPoint)
		if err != nil {
			return AppendResult{}, err
		}
	}
	if len(rows) == 0 && req.Checkpoint == nil && !shouldWriteEpoch {
		return AppendResult{}, nil
	}

	batch := l.db.engine.NewBatch()
	defer batch.Close()
	if err := l.stageMessageRows(batch, rows); err != nil {
		return AppendResult{}, err
	}
	if req.Checkpoint != nil {
		if err := batch.Set(encodeCheckpointKey(l.key), encodeCheckpoint(*req.Checkpoint)); err != nil {
			return AppendResult{}, err
		}
	}
	if shouldWriteEpoch {
		if err := l.writeHistoryPoint(batch, *req.EpochPoint); err != nil {
			return AppendResult{}, err
		}
	}
	if err := l.stageCatalog(batch); err != nil {
		return AppendResult{}, err
	}
	if err := batch.Commit(true); err != nil {
		return AppendResult{}, err
	}
	l.publishAppendLocked(result)
	return result, nil
}
