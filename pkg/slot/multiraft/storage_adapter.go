package multiraft

import (
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
	confState raftpb.ConfState
}

func newLoadedMemoryStorage(memory *raft.MemoryStorage, confState raftpb.ConfState) *loadedMemoryStorage {
	return &loadedMemoryStorage{
		MemoryStorage: memory,
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
	if err := s.MemoryStorage.ApplySnapshot(snapshot); err != nil {
		return err
	}
	s.confState = cloneConfState(snapshot.Metadata.ConfState)
	return nil
}
