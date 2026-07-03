package raft

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/raft/raftstore"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
	"go.etcd.io/raft/v3/raftpb"
)

// maybeSnapshot snapshots only materialized Controller state, then compacts WAL entries already covered by catch-up retention.
func (s *Service) maybeSnapshot(ctx context.Context, store *raftstore.Store, applied uint64) error {
	if s.cfg.SnapshotCount == 0 || applied < s.cfg.SnapshotCount {
		return nil
	}
	snap, err := store.Snapshot()
	if err != nil {
		return err
	}
	if applied < snap.Metadata.Index+s.cfg.SnapshotCount {
		return nil
	}
	now := time.Now()
	s.snapshotMu.Lock()
	if !s.lastSnapshot.IsZero() && now.Sub(s.lastSnapshot) < s.cfg.SnapshotMinInterval {
		s.snapshotMu.Unlock()
		return nil
	}
	s.lastSnapshot = now
	s.snapshotMu.Unlock()

	_, err = s.compactLogAt(ctx, store, applied, LogCompactionTriggerAutomatic)
	return err
}

func (s *Service) compactLogNow(ctx context.Context, store *raftstore.Store, trigger string) (LogCompactionResult, error) {
	if store == nil {
		err := ErrNotStarted
		result := LogCompactionResult{NodeID: s.cfg.NodeID, SkippedReason: LogCompactionSkipNotStarted, Error: err.Error()}
		s.recordCompactionStatus(trigger, result, err)
		return result, err
	}
	return s.compactLogAt(ctx, store, store.AppliedIndex(), trigger)
}

func (s *Service) compactLogAt(ctx context.Context, store *raftstore.Store, applied uint64, trigger string) (LogCompactionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	result := LogCompactionResult{NodeID: s.cfg.NodeID, AppliedIndex: applied}
	snap, err := store.Snapshot()
	if err != nil {
		result.Error = err.Error()
		s.recordCompactionStatus(trigger, result, err)
		return result, err
	}
	result.BeforeSnapshotIndex = snap.Metadata.Index
	result.AfterSnapshotIndex = snap.Metadata.Index
	if applied == 0 {
		result.SkippedReason = LogCompactionSkipNoAppliedIndex
		s.recordCompactionStatus(trigger, result, nil)
		return result, nil
	}
	if applied <= snap.Metadata.Index {
		result.SkippedReason = LogCompactionSkipUpToDate
		s.recordCompactionStatus(trigger, result, nil)
		return result, nil
	}
	st := s.cfg.StateMachine.Snapshot(ctx)
	if st.Revision == 0 {
		result.SkippedReason = LogCompactionSkipNoMaterializedState
		s.recordCompactionStatus(trigger, result, nil)
		return result, nil
	}
	if st.AppliedRaftIndex < applied {
		st.AppliedRaftIndex = applied
	}
	data, err := state.Encode(st)
	if err != nil {
		result.Error = err.Error()
		s.recordCompactionStatus(trigger, result, err)
		return result, err
	}
	term, err := store.Term(applied)
	if err != nil {
		term = s.Status().Term
	}
	_, conf, err := store.InitialState()
	if err != nil {
		result.Error = err.Error()
		s.recordCompactionStatus(trigger, result, err)
		return result, err
	}
	if err := store.SaveSnapshot(ctx, raftpb.Snapshot{Data: data, Metadata: raftpb.SnapshotMetadata{Index: applied, Term: term, ConfState: conf}}); err != nil {
		result.Error = err.Error()
		s.recordCompactionStatus(trigger, result, err)
		return result, err
	}
	result.Compacted = true
	result.AfterSnapshotIndex = applied
	if applied > s.cfg.SnapshotCatchUpEntries {
		if err := store.Compact(ctx, applied-s.cfg.SnapshotCatchUpEntries); err != nil {
			result.Error = err.Error()
			s.recordCompactionStatus(trigger, result, err)
			return result, err
		}
	}
	s.recordCompactionStatus(trigger, result, nil)
	return result, nil
}
