package store

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/cockroachdb/pebble/v2"
)

// RetentionScanResult describes the continuous expired prefix found by a scan.
type RetentionScanResult struct {
	// FromSeq is the normalized sequence where the scan started.
	FromSeq uint64
	// ThroughSeq is the highest continuous expired sequence found.
	ThroughSeq uint64
	// Count is the number of expired rows included in the continuous prefix.
	Count int
}

// LoadRetentionState loads local durable retention progress. Missing state is a
// zero boundary because channels adopt retention only after authoritative meta.
func (s *ChannelStore) LoadRetentionState() (retentionState, error) {
	if err := s.validate(); err != nil {
		return retentionState{}, err
	}
	state, _, err := s.loadRetentionState()
	return state, err
}

func (s *ChannelStore) loadRetentionState() (retentionState, bool, error) {
	value, closer, err := s.engine.db.Get(encodeRetentionStateKey(s.key))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return retentionState{}, false, nil
		}
		return retentionState{}, false, err
	}
	defer closer.Close()
	state, err := decodeRetentionState(value)
	if err != nil {
		return retentionState{}, false, err
	}
	return state, true, nil
}

func (s *ChannelStore) writeRetentionState(batch *pebble.Batch, state retentionState) error {
	if err := s.validate(); err != nil {
		return err
	}
	if batch == nil {
		return channel.ErrInvalidArgument
	}
	if err := validateRetentionState(state); err != nil {
		return err
	}
	return batch.Set(encodeRetentionStateKey(s.key), encodeRetentionState(state), pebble.NoSync)
}

// ScanExpiredMessagePrefix scans the contiguous local message prefix whose
// positive timestamps are at or before cutoff.
func (s *ChannelStore) ScanExpiredMessagePrefix(fromSeq uint64, cutoff time.Time, limit int) (RetentionScanResult, error) {
	if err := s.validate(); err != nil {
		return RetentionScanResult{}, err
	}
	if fromSeq == 0 {
		fromSeq = 1
	}
	result := RetentionScanResult{FromSeq: fromSeq}
	if limit <= 0 {
		return result, nil
	}

	table := s.messageTable()
	if err := table.validate(); err != nil {
		return RetentionScanResult{}, err
	}

	prefix := encodeTableStatePrefix(table.channelKey, TableIDMessage)
	iter, err := table.db.NewIter(&pebble.IterOptions{
		LowerBound: encodeTableStateKey(table.channelKey, TableIDMessage, fromSeq, messagePrimaryFamilyID),
		UpperBound: keyUpperBound(prefix),
	})
	if err != nil {
		return RetentionScanResult{}, err
	}
	defer iter.Close()

	expectedSeq := fromSeq
	primaryFamily := canonicalMessageTable().Families[0]
	for valid := iter.First(); valid && result.Count < limit; valid = iter.Next() {
		seq, familyID, err := decodeTableStateKey(iter.Key(), table.channelKey, TableIDMessage)
		if err != nil {
			return RetentionScanResult{}, err
		}
		if seq < expectedSeq {
			continue
		}
		if seq > expectedSeq {
			break
		}
		if familyID != messagePrimaryFamilyID {
			return RetentionScanResult{}, channel.ErrCorruptState
		}

		row := messageRow{MessageSeq: seq}
		if err := decodeMessageFamilyInto(&row, primaryFamily, iter.Value()); err != nil {
			return RetentionScanResult{}, err
		}
		if row.MessageID == 0 {
			return RetentionScanResult{}, channel.ErrCorruptValue
		}
		if row.Timestamp <= 0 || time.Unix(int64(row.Timestamp), 0).After(cutoff) {
			break
		}

		result.ThroughSeq = seq
		result.Count++
		expectedSeq = seq + 1
	}
	if err := iter.Error(); err != nil {
		return RetentionScanResult{}, err
	}
	return result, nil
}

// AdoptRetentionBoundary durably records a local retention boundary and moves
// the replay cursor past data that authoritative retention made unavailable.
func (s *ChannelStore) AdoptRetentionBoundary(ctx context.Context, throughSeq uint64, cursorName string) error {
	if throughSeq == 0 {
		return channel.ErrInvalidArgument
	}
	if err := s.validateCursorName(cursorName); err != nil {
		return err
	}
	if err := contextErr(ctx); err != nil {
		return err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := contextErr(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	leoBefore, err := s.leoLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}

	state, err := s.LoadRetentionState()
	if err != nil {
		return err
	}
	nextState := state
	nextState.LocalRetentionThroughSeq = maxUint64(nextState.LocalRetentionThroughSeq, throughSeq)
	nextState.RetainedMaxSeq = maxUint64(nextState.RetainedMaxSeq, maxUint64(leoBefore, throughSeq))

	s.cursorMu.Lock()
	defer s.cursorMu.Unlock()

	cursorSeq, cursorExists, err := s.loadCommittedDispatchCursor(cursorName)
	if err != nil {
		return err
	}
	cursorTarget := nextState.LocalRetentionThroughSeq
	nextCursorSeq := cursorSeq
	if !cursorExists || cursorSeq < cursorTarget {
		nextCursorSeq = cursorTarget
	}
	if nextState == state && cursorExists && cursorSeq >= cursorTarget {
		return nil
	}

	batch := s.engine.db.NewBatch()
	defer batch.Close()
	if nextState != state {
		if err := s.writeRetentionState(batch, nextState); err != nil {
			return err
		}
	}
	if !cursorExists || cursorSeq < cursorTarget {
		if err := s.writeCommittedDispatchCursor(batch, cursorName, nextCursorSeq); err != nil {
			return err
		}
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return err
	}
	s.recordDurableCommit()
	s.publishRetentionLEOFloor(nextState.RetainedMaxSeq)
	return nil
}

// TrimMessagesThrough atomically removes local message rows and indexes through
// the retained prefix already adopted by this store.
func (s *ChannelStore) TrimMessagesThrough(ctx context.Context, throughSeq uint64) error {
	if throughSeq == 0 {
		return channel.ErrInvalidArgument
	}
	if err := s.validate(); err != nil {
		return err
	}
	if err := contextErr(ctx); err != nil {
		return err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := contextErr(ctx); err != nil {
		return err
	}

	s.mu.Lock()
	leoBefore, err := s.leoLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}

	state, err := s.LoadRetentionState()
	if err != nil {
		return err
	}

	batch := s.engine.db.NewBatch()
	defer batch.Close()

	deletedThroughSeq, deleted, err := s.messageTable().deletePrefixThrough(batch, throughSeq)
	if err != nil {
		return err
	}
	if deleted == 0 {
		return nil
	}

	nextState := state
	nextState.PhysicalRetentionThroughSeq = maxUint64(nextState.PhysicalRetentionThroughSeq, deletedThroughSeq)
	if nextState.PhysicalRetentionThroughSeq > nextState.LocalRetentionThroughSeq {
		return channel.ErrCorruptState
	}
	nextState.RetainedMaxSeq = maxUint64(nextState.RetainedMaxSeq, leoBefore)
	if err := s.writeRetentionState(batch, nextState); err != nil {
		return err
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return err
	}

	s.recordDurableCommit()
	s.publishRetentionLEOFloor(maxUint64(leoBefore, nextState.RetainedMaxSeq))
	return nil
}

func (s *ChannelStore) retentionStateAfterTruncate(to uint64) (retentionState, bool, error) {
	state, exists, err := s.loadRetentionState()
	if err != nil || !exists {
		return retentionState{}, false, err
	}
	if to < state.LocalRetentionThroughSeq {
		return retentionState{}, false, channel.ErrCorruptState
	}
	next := state
	if next.RetainedMaxSeq > to {
		next.RetainedMaxSeq = to
	}
	if next == state {
		return retentionState{}, false, nil
	}
	return next, true, nil
}

func (s *ChannelStore) publishRetentionLEOFloor(retainedMaxSeq uint64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if retainedMaxSeq > s.leo.Load() {
		s.leo.Store(retainedMaxSeq)
	}
	s.loaded.Store(true)
	s.mu.Unlock()
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func maxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
