package raftlog

import (
	"context"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

type memoryStore struct {
	mu       sync.Mutex
	state    multiraft.BootstrapState
	entries  []raftpb.Entry
	snapshot raftpb.Snapshot
}

func NewMemory() multiraft.Storage {
	return &memoryStore{}
}

func (m *memoryStore) InitialState(ctx context.Context) (multiraft.BootstrapState, error) {
	_ = ctx

	m.mu.Lock()
	defer m.mu.Unlock()

	state := m.state
	confState, err := deriveConfState(m.snapshot, m.entries, state.HardState.Commit)
	if err != nil {
		return multiraft.BootstrapState{}, err
	}
	state.ConfState = confState
	return state, nil
}

func (m *memoryStore) Entries(ctx context.Context, lo, hi, maxSize uint64) ([]raftpb.Entry, error) {
	_ = ctx

	m.mu.Lock()
	defer m.mu.Unlock()

	var (
		out  []raftpb.Entry
		size uint64
	)
	for _, entry := range m.entries {
		if entry.Index < lo || entry.Index >= hi {
			continue
		}
		if maxSize > 0 && len(out) > 0 && size+uint64(entry.Size()) > maxSize {
			break
		}
		size += uint64(entry.Size())
		out = append(out, cloneEntry(entry))
	}
	return out, nil
}

func (m *memoryStore) Term(ctx context.Context, index uint64) (uint64, error) {
	_ = ctx

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, entry := range m.entries {
		if entry.Index == index {
			return entry.Term, nil
		}
	}
	if m.snapshot.Metadata.Index == index {
		return m.snapshot.Metadata.Term, nil
	}
	return 0, nil
}

func (m *memoryStore) FirstIndex(ctx context.Context) (uint64, error) {
	_ = ctx

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.entries) > 0 {
		return m.entries[0].Index, nil
	}
	if !raft.IsEmptySnap(m.snapshot) {
		return m.snapshot.Metadata.Index + 1, nil
	}
	return 1, nil
}

func (m *memoryStore) LastIndex(ctx context.Context) (uint64, error) {
	_ = ctx

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.entries) > 0 {
		return m.entries[len(m.entries)-1].Index, nil
	}
	return m.snapshot.Metadata.Index, nil
}

func (m *memoryStore) Snapshot(ctx context.Context) (raftpb.Snapshot, error) {
	_ = ctx

	m.mu.Lock()
	defer m.mu.Unlock()

	return cloneSnapshot(m.snapshot), nil
}

func (m *memoryStore) Save(ctx context.Context, st multiraft.PersistentState) error {
	_ = ctx

	m.mu.Lock()
	defer m.mu.Unlock()

	if st.HardState != nil {
		m.state.HardState = *st.HardState
	}
	if st.Snapshot != nil {
		m.snapshot = cloneSnapshot(*st.Snapshot)
		m.entries = trimEntriesAfterSnapshot(m.entries, st.Snapshot.Metadata.Index)
		if m.state.HardState.Commit < st.Snapshot.Metadata.Index {
			m.state.HardState.Commit = st.Snapshot.Metadata.Index
		}
	}
	if len(st.Entries) > 0 {
		first := st.Entries[0].Index
		m.entries = replaceEntriesFromIndex(m.entries, first, st.Entries)
	}
	return nil
}

func (m *memoryStore) MarkApplied(ctx context.Context, index uint64) error {
	_ = ctx

	m.mu.Lock()
	defer m.mu.Unlock()

	m.state.AppliedIndex = index
	return nil
}
