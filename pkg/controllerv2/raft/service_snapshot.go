package raft

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft/raftstore"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	"go.etcd.io/raft/v3/raftpb"
)

// maybeSnapshot snapshots only materialized ControllerV2 state, then compacts WAL entries already covered by catch-up retention.
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

	st := s.cfg.StateMachine.Snapshot(ctx)
	if st.Revision == 0 {
		return nil
	}
	data, err := state.Encode(st)
	if err != nil {
		return err
	}
	term, err := store.Term(applied)
	if err != nil {
		term = s.Status().Term
	}
	_, conf, err := store.InitialState()
	if err != nil {
		return err
	}
	if err := store.SaveSnapshot(ctx, raftpb.Snapshot{Data: data, Metadata: raftpb.SnapshotMetadata{Index: applied, Term: term, ConfState: conf}}); err != nil {
		return err
	}
	if applied > s.cfg.SnapshotCatchUpEntries {
		return store.Compact(ctx, applied-s.cfg.SnapshotCatchUpEntries)
	}
	return nil
}
