package store

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/cockroachdb/pebble/v2"
)

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
