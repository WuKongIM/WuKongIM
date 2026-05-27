package raft

import (
	"context"
	"fmt"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft/raftstore"
	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
	etcdraft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

type runStartupState struct {
	HardState    raftpb.HardState
	ConfState    raftpb.ConfState
	Snapshot     raftpb.Snapshot
	LastIndex    uint64
	AppliedIndex uint64
}

func (s *Service) newRawNode(store etcdraft.Storage, startup runStartupState) (*etcdraft.RawNode, error) {
	applied := startup.AppliedIndex
	if !etcdraft.IsEmptySnap(startup.Snapshot) && startup.Snapshot.Metadata.Index > applied {
		applied = startup.Snapshot.Metadata.Index
	}
	return etcdraft.NewRawNode(&etcdraft.Config{
		ID:                       s.cfg.NodeID,
		ElectionTick:             electionTick,
		HeartbeatTick:            heartbeatTick,
		Storage:                  store,
		Applied:                  applied,
		MaxSizePerMsg:            math.MaxUint64,
		MaxCommittedSizePerReady: math.MaxUint64,
		MaxInflightMsgs:          256,
		CheckQuorum:              true,
		PreVote:                  true,
	})
}

// recoverStartup rebuilds the materialized state file from snapshots and committed WAL entries before RawNode starts.
func (s *Service) recoverStartup(ctx context.Context, store *raftstore.Store) (runStartupState, error) {
	if err := s.cfg.StateMachine.Load(ctx); err != nil {
		return runStartupState{}, err
	}
	hs, cs, err := store.InitialState()
	if err != nil {
		return runStartupState{}, err
	}
	snap, err := store.Snapshot()
	if err != nil {
		return runStartupState{}, err
	}
	last, err := store.LastIndex()
	if err != nil {
		return runStartupState{}, err
	}
	stateSnap := s.cfg.StateMachine.Snapshot(ctx)
	if stateSnap.Revision == 0 && !etcdraft.IsEmptySnap(snap) && len(snap.Data) > 0 {
		restored, err := state.Decode(snap.Data)
		if err != nil {
			return runStartupState{}, err
		}
		if restored.AppliedRaftIndex == 0 || restored.AppliedRaftIndex < snap.Metadata.Index {
			restored.AppliedRaftIndex = snap.Metadata.Index
		}
		if err := s.cfg.StateMachine.Restore(ctx, restored); err != nil {
			return runStartupState{}, err
		}
		stateSnap = s.cfg.StateMachine.Snapshot(ctx)
	}
	if stateSnap.Revision != 0 {
		// cluster-state.json is only materialized state. Recovery trusts the
		// Controller Raft WAL commit boundary and applied metadata as authoritative.
		if stateSnap.AppliedRaftIndex > hs.Commit {
			return runStartupState{}, fmt.Errorf("controllerv2/raft: state file applied raft index %d is ahead of committed raft index %d", stateSnap.AppliedRaftIndex, hs.Commit)
		}
		if stateSnap.AppliedRaftIndex > last {
			return runStartupState{}, fmt.Errorf("controllerv2/raft: state file applied raft index %d is ahead of local last raft index %d", stateSnap.AppliedRaftIndex, last)
		}
	}
	replayFrom := store.AppliedIndex() + 1
	if stateSnap.Revision != 0 {
		replayFrom = stateSnap.AppliedRaftIndex + 1
	}
	if hs.Commit >= replayFrom {
		entries, err := store.Entries(replayFrom, hs.Commit+1, 0)
		if err != nil {
			return runStartupState{}, err
		}
		replayer := newApplyScheduler(applySchedulerConfig{MaxEntries: s.cfg.MaxApplyBatchEntries, MaxBytes: s.cfg.MaxApplyBatchBytes, MaxDelay: s.cfg.MaxApplyDelay}, s.cfg.StateMachine, store, nil)
		if err := replayer.applyEntries(ctx, entries, nil); err != nil {
			return runStartupState{}, err
		}
	}
	return runStartupState{HardState: hs, ConfState: cs, Snapshot: snap, LastIndex: last, AppliedIndex: store.AppliedIndex()}, nil
}
