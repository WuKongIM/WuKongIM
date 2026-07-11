package message

import "context"

// ApplyFetch stores fetched records and optional system state in one batch.
func (l *ChannelLog) ApplyFetch(ctx context.Context, req ApplyFetchRequest) (AppendResult, error) {
	if err := l.beginUse(); err != nil {
		return AppendResult{}, err
	}
	defer l.endUse()
	if err := ctx.Err(); err != nil {
		return AppendResult{}, err
	}
	l.appendMu.Lock()
	defer l.appendMu.Unlock()
	if req.Checkpoint != nil {
		l.checkpointMu.Lock()
		defer l.checkpointMu.Unlock()
	}

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
		if err := l.validateCheckpointMonotonicLocked(ctx, *req.Checkpoint, visibleLEO, visibleLEO); err != nil {
			return AppendResult{}, err
		}
	}
	shouldWriteEpoch := false
	if req.EpochPoint != nil {
		points, ok, err := l.loadHistory(ctx)
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
