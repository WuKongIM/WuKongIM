package multiraft

import (
	"context"
	"slices"

	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

type storageAdapter struct {
	storage Storage
	memory  *loadedMemoryStorage
}

func newStorageAdapter(storage Storage) *storageAdapter {
	return &storageAdapter{storage: storage}
}

type loadedMemoryStorage struct {
	*raft.MemoryStorage
	durable   Storage
	confState raftpb.ConfState
}

func newLoadedMemoryStorage(memory *raft.MemoryStorage, durable Storage, confState raftpb.ConfState) *loadedMemoryStorage {
	return &loadedMemoryStorage{
		MemoryStorage: memory,
		durable:       durable,
		confState:     cloneConfState(confState),
	}
}

func (s *loadedMemoryStorage) InitialState() (raftpb.HardState, raftpb.ConfState, error) {
	hardState, _, err := s.MemoryStorage.InitialState()
	if err != nil {
		return raftpb.HardState{}, raftpb.ConfState{}, err
	}
	return hardState, cloneConfState(s.confState), nil
}

func (s *loadedMemoryStorage) ApplySnapshot(snapshot raftpb.Snapshot) error {
	if err := s.MemoryStorage.ApplySnapshot(snapshotWithoutData(snapshot)); err != nil {
		return err
	}
	s.confState = cloneConfState(snapshot.Metadata.ConfState)
	return nil
}

// CreateSnapshot keeps only the Raft boundary in memory. The durable Slot
// storage remains the sole owner of the potentially large FSM payload.
func (s *loadedMemoryStorage) CreateSnapshot(index uint64, confState *raftpb.ConfState, _ []byte) (raftpb.Snapshot, error) {
	return s.MemoryStorage.CreateSnapshot(index, confState, nil)
}

// Snapshot loads the payload only when Raft must transfer a snapshot to a
// lagging peer. The boundary must match the metadata retained in memory.
func (s *loadedMemoryStorage) Snapshot() (raftpb.Snapshot, error) {
	metadata, err := s.MemoryStorage.Snapshot()
	if err != nil || raft.IsEmptySnap(metadata) {
		return metadata, err
	}
	durable, err := s.durable.Snapshot(context.Background())
	if err != nil {
		return raftpb.Snapshot{}, err
	}
	if durable.Metadata.Index != metadata.Metadata.Index ||
		durable.Metadata.Term != metadata.Metadata.Term ||
		!sameConfState(durable.Metadata.ConfState, metadata.Metadata.ConfState) {
		return raftpb.Snapshot{}, raft.ErrSnapshotTemporarilyUnavailable
	}
	return durable, nil
}

func snapshotWithoutData(snapshot raftpb.Snapshot) raftpb.Snapshot {
	snapshot.Data = nil
	snapshot.Metadata.ConfState = cloneConfState(snapshot.Metadata.ConfState)
	return snapshot
}

func sameConfState(left, right raftpb.ConfState) bool {
	return left.AutoLeave == right.AutoLeave &&
		slices.Equal(left.Voters, right.Voters) &&
		slices.Equal(left.Learners, right.Learners) &&
		slices.Equal(left.VotersOutgoing, right.VotersOutgoing) &&
		slices.Equal(left.LearnersNext, right.LearnersNext)
}
