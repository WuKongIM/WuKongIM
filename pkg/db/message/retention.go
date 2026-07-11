package message

import (
	"context"
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

const retentionStateVersion byte = 1

// LoadRetentionState loads durable local retention progress.
func (l *ChannelLog) LoadRetentionState(ctx context.Context) (RetentionState, bool, error) {
	if err := l.beginUse(); err != nil {
		return RetentionState{}, false, err
	}
	defer l.endUse()
	return l.loadRetentionState(ctx)
}

func (l *ChannelLog) loadRetentionState(ctx context.Context) (RetentionState, bool, error) {
	if err := ctx.Err(); err != nil {
		return RetentionState{}, false, err
	}
	value, ok, err := l.db.engine.Get(encodeRetentionStateKey(l.key))
	if err != nil || !ok {
		return RetentionState{}, ok, err
	}
	state, err := decodeRetentionState(value)
	if err != nil {
		return RetentionState{}, false, err
	}
	return state, true, nil
}

// StoreRetentionState stores durable local retention progress.
func (l *ChannelLog) StoreRetentionState(ctx context.Context, state RetentionState) error {
	if err := l.beginUse(); err != nil {
		return err
	}
	defer l.endUse()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRetentionState(state); err != nil {
		return err
	}
	batch := l.db.engine.NewBatch()
	defer batch.Close()
	if err := batch.Set(encodeRetentionStateKey(l.key), encodeRetentionState(state)); err != nil {
		return err
	}
	if err := l.stageCatalog(batch); err != nil {
		return err
	}
	return batch.Commit(true)
}

// TrimPrefixThrough physically deletes message rows at or below throughSeq.
func (l *ChannelLog) TrimPrefixThrough(ctx context.Context, throughSeq uint64) (RetentionTrimResult, error) {
	if err := l.beginUse(); err != nil {
		return RetentionTrimResult{}, err
	}
	defer l.endUse()
	return l.trimPrefixThroughLimit(ctx, throughSeq, RetentionTrimOptions{}, true)
}

// TrimPrefixThroughLimit physically deletes a bounded message prefix at or below throughSeq.
func (l *ChannelLog) TrimPrefixThroughLimit(ctx context.Context, throughSeq uint64, opts RetentionTrimOptions) (RetentionTrimResult, error) {
	if err := l.beginUse(); err != nil {
		return RetentionTrimResult{}, err
	}
	defer l.endUse()
	return l.trimPrefixThroughLimit(ctx, throughSeq, opts, true)
}

func (l *ChannelLog) trimPrefixThroughLimit(ctx context.Context, throughSeq uint64, opts RetentionTrimOptions, adoptBoundary bool) (RetentionTrimResult, error) {
	if err := ctx.Err(); err != nil {
		return RetentionTrimResult{}, err
	}
	if throughSeq == 0 {
		return RetentionTrimResult{}, nil
	}

	l.appendMu.Lock()
	defer l.appendMu.Unlock()

	leo, err := l.loadLEOLocked(ctx)
	if err != nil {
		return RetentionTrimResult{}, err
	}
	state, ok, err := l.loadRetentionState(ctx)
	if err != nil {
		return RetentionTrimResult{}, err
	}
	if !ok {
		state = RetentionState{}
	}
	if !adoptBoundary && throughSeq > state.LocalRetentionThroughSeq {
		return RetentionTrimResult{}, dberrors.ErrCorruptState
	}

	startSeq := state.PhysicalRetentionThroughSeq + 1
	if startSeq == 0 {
		return RetentionTrimResult{}, dberrors.ErrCorruptState
	}

	readOpts := ReadOptions{MaxBytes: opts.MaxBytes}
	if opts.MaxMessages > 0 {
		readOpts.Limit = opts.MaxMessages + 1
	}
	rows, err := l.readRows(ctx, startSeq, throughSeq, readOpts)
	if err != nil {
		return RetentionTrimResult{}, err
	}
	deleteRows := rows
	result := RetentionTrimResult{}
	if opts.MaxMessages > 0 && len(deleteRows) > opts.MaxMessages {
		result.More = true
		deleteRows = deleteRows[:opts.MaxMessages]
	}
	if opts.MaxBytes > 0 && len(deleteRows) > 0 && deleteRows[len(deleteRows)-1].MessageSeq < throughSeq {
		result.More = true
	}

	next := state
	if adoptBoundary && throughSeq > next.LocalRetentionThroughSeq {
		next.LocalRetentionThroughSeq = throughSeq
	}
	if adoptBoundary && throughSeq > next.RetainedMaxSeq {
		next.RetainedMaxSeq = throughSeq
	}
	if leo > next.RetainedMaxSeq {
		next.RetainedMaxSeq = leo
	}

	batch := l.db.engine.NewBatch()
	defer batch.Close()
	for _, row := range deleteRows {
		msg := messageFromRow(row)
		if err := l.stageDeleteMessage(batch, msg); err != nil {
			return RetentionTrimResult{}, err
		}
		result.DeletedThroughSeq = msg.MessageSeq
		result.Deleted++
	}
	if result.DeletedThroughSeq > next.PhysicalRetentionThroughSeq {
		next.PhysicalRetentionThroughSeq = result.DeletedThroughSeq
	}
	if err := validateRetentionState(next); err != nil {
		return RetentionTrimResult{}, err
	}
	if err := batch.Set(encodeRetentionStateKey(l.key), encodeRetentionState(next)); err != nil {
		return RetentionTrimResult{}, err
	}
	if err := l.stageCatalog(batch); err != nil {
		return RetentionTrimResult{}, err
	}
	if err := batch.Commit(true); err != nil {
		return RetentionTrimResult{}, err
	}
	l.leo.Store(maxUint64(leo, next.RetainedMaxSeq))
	l.loaded.Store(true)
	return result, nil
}

func encodeRetentionState(state RetentionState) []byte {
	value := make([]byte, 0, 25)
	value = append(value, retentionStateVersion)
	value = binary.BigEndian.AppendUint64(value, state.LocalRetentionThroughSeq)
	value = binary.BigEndian.AppendUint64(value, state.PhysicalRetentionThroughSeq)
	value = binary.BigEndian.AppendUint64(value, state.RetainedMaxSeq)
	return value
}

func decodeRetentionState(value []byte) (RetentionState, error) {
	if len(value) != 25 || value[0] != retentionStateVersion {
		return RetentionState{}, dberrors.ErrCorruptValue
	}
	state := RetentionState{
		LocalRetentionThroughSeq:    binary.BigEndian.Uint64(value[1:9]),
		PhysicalRetentionThroughSeq: binary.BigEndian.Uint64(value[9:17]),
		RetainedMaxSeq:              binary.BigEndian.Uint64(value[17:25]),
	}
	if err := validateRetentionState(state); err != nil {
		return RetentionState{}, err
	}
	return state, nil
}

func validateRetentionState(state RetentionState) error {
	if state.LocalRetentionThroughSeq == 0 && state.RetainedMaxSeq > 0 {
		return dberrors.ErrCorruptValue
	}
	if state.PhysicalRetentionThroughSeq > state.LocalRetentionThroughSeq {
		return dberrors.ErrCorruptValue
	}
	if state.LocalRetentionThroughSeq > 0 && state.RetainedMaxSeq < state.LocalRetentionThroughSeq {
		return dberrors.ErrCorruptValue
	}
	return nil
}
