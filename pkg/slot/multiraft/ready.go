package multiraft

import (
	"context"

	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

func (s *storageAdapter) load(ctx context.Context) (BootstrapState, raftpb.Snapshot, *loadedMemoryStorage, error) {
	state, err := s.storage.InitialState(ctx)
	if err != nil {
		return BootstrapState{}, raftpb.Snapshot{}, nil, err
	}

	memory := raft.NewMemoryStorage()

	snap, err := s.storage.Snapshot(ctx)
	if err != nil {
		return BootstrapState{}, raftpb.Snapshot{}, nil, err
	}
	if !raft.IsEmptySnap(snap) {
		if err := memory.ApplySnapshot(snap); err != nil {
			return BootstrapState{}, raftpb.Snapshot{}, nil, err
		}
	}

	first, err := s.storage.FirstIndex(ctx)
	if err != nil {
		return BootstrapState{}, raftpb.Snapshot{}, nil, err
	}
	last, err := s.storage.LastIndex(ctx)
	if err != nil {
		return BootstrapState{}, raftpb.Snapshot{}, nil, err
	}
	if last >= first && last > 0 {
		entries, err := s.storage.Entries(ctx, first, last+1, maxSizePerMsg(0))
		if err != nil {
			return BootstrapState{}, raftpb.Snapshot{}, nil, err
		}
		if len(entries) > 0 {
			if err := memory.Append(entries); err != nil {
				return BootstrapState{}, raftpb.Snapshot{}, nil, err
			}
		}
	}

	if !raft.IsEmptyHardState(state.HardState) {
		if err := memory.SetHardState(state.HardState); err != nil {
			return BootstrapState{}, raftpb.Snapshot{}, nil, err
		}
	}

	return state, snap, newLoadedMemoryStorage(memory, state.ConfState), nil
}

func (s *storageAdapter) persistReady(ctx context.Context, ready raft.Ready) error {
	persist := PersistentState{}
	needsSave := false

	if !raft.IsEmptyHardState(ready.HardState) {
		hs := ready.HardState
		persist.HardState = &hs
		needsSave = true
	}
	if len(ready.Entries) > 0 {
		persist.Entries = append([]raftpb.Entry(nil), ready.Entries...)
		needsSave = true
	}
	if !raft.IsEmptySnap(ready.Snapshot) {
		snap := ready.Snapshot
		persist.Snapshot = &snap
		needsSave = true
	}

	if needsSave {
		if err := s.storage.Save(ctx, persist); err != nil {
			return err
		}
	}

	if persist.Snapshot != nil {
		if err := s.memory.ApplySnapshot(*persist.Snapshot); err != nil {
			return err
		}
	}
	if len(persist.Entries) > 0 {
		if err := s.memory.Append(persist.Entries); err != nil {
			return err
		}
	}
	if persist.HardState != nil {
		if err := s.memory.SetHardState(*persist.HardState); err != nil {
			return err
		}
	}
	return nil
}
