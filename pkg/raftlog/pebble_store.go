package raftlog

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/cockroachdb/pebble/v2"
	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

type pebbleStore struct {
	db    *DB
	scope Scope
}

func (s *pebbleStore) InitialState(ctx context.Context) (multiraft.BootstrapState, error) {
	_ = ctx

	meta, ok, err := s.ensureMeta()
	if err != nil {
		return multiraft.BootstrapState{}, err
	}
	if ok {
		hs, err := s.loadHardState()
		if err != nil {
			return multiraft.BootstrapState{}, err
		}
		return multiraft.BootstrapState{
			HardState:    hs,
			ConfState:    cloneConfState(meta.ConfState),
			AppliedIndex: meta.AppliedIndex,
		}, nil
	}

	snap, err := s.loadSnapshot()
	if err != nil {
		return multiraft.BootstrapState{}, err
	}
	hs, err := s.loadHardState()
	if err != nil {
		return multiraft.BootstrapState{}, err
	}
	appliedIndex, err := s.loadAppliedIndex()
	if err != nil {
		return multiraft.BootstrapState{}, err
	}
	entries, err := s.loadEntries(0, 0)
	if err != nil {
		return multiraft.BootstrapState{}, err
	}
	confState, err := deriveConfState(snap, entries, hs.Commit)
	if err != nil {
		return multiraft.BootstrapState{}, err
	}
	return multiraft.BootstrapState{
		HardState:    hs,
		ConfState:    confState,
		AppliedIndex: appliedIndex,
	}, nil
}

func (s *pebbleStore) Entries(ctx context.Context, lo, hi, maxSize uint64) ([]raftpb.Entry, error) {
	_ = ctx

	entries, err := s.loadEntries(lo, hi)
	if err != nil {
		return nil, err
	}

	var (
		out  []raftpb.Entry
		size uint64
	)
	for _, entry := range entries {
		if maxSize > 0 && len(out) > 0 && size+uint64(entry.Size()) > maxSize {
			break
		}
		size += uint64(entry.Size())
		out = append(out, cloneEntry(entry))
	}
	return out, nil
}

func (s *pebbleStore) Term(ctx context.Context, index uint64) (uint64, error) {
	_ = ctx

	var entry raftpb.Entry
	if err := s.loadProto(encodeEntryKey(s.scope, index), &entry); err != nil {
		return 0, err
	}
	if entry.Index == index {
		return entry.Term, nil
	}

	snap, err := s.loadSnapshot()
	if err != nil {
		return 0, err
	}
	if snap.Metadata.Index == index {
		return snap.Metadata.Term, nil
	}
	return 0, nil
}

func (s *pebbleStore) FirstIndex(ctx context.Context) (uint64, error) {
	_ = ctx

	meta, ok, err := s.ensureMeta()
	if err != nil {
		return 0, err
	}
	if ok {
		return meta.FirstIndex, nil
	}

	iter, err := s.db.db.NewIter(&pebble.IterOptions{
		LowerBound: encodeEntryPrefix(s.scope),
		UpperBound: encodeEntryPrefixEnd(s.scope),
	})
	if err != nil {
		return 0, err
	}
	defer iter.Close()

	if iter.First() {
		entry, err := decodeEntryValue(iter)
		if err != nil {
			return 0, err
		}
		return entry.Index, nil
	}

	snap, err := s.loadSnapshot()
	if err != nil {
		return 0, err
	}
	if !raft.IsEmptySnap(snap) {
		return snap.Metadata.Index + 1, nil
	}
	return 1, nil
}

func (s *pebbleStore) LastIndex(ctx context.Context) (uint64, error) {
	_ = ctx

	meta, ok, err := s.ensureMeta()
	if err != nil {
		return 0, err
	}
	if ok {
		return meta.LastIndex, nil
	}

	iter, err := s.db.db.NewIter(&pebble.IterOptions{
		LowerBound: encodeEntryPrefix(s.scope),
		UpperBound: encodeEntryPrefixEnd(s.scope),
	})
	if err != nil {
		return 0, err
	}
	defer iter.Close()

	if iter.Last() {
		entry, err := decodeEntryValue(iter)
		if err != nil {
			return 0, err
		}
		return entry.Index, nil
	}

	snap, err := s.loadSnapshot()
	if err != nil {
		return 0, err
	}
	return snap.Metadata.Index, nil
}

func (s *pebbleStore) Snapshot(ctx context.Context) (raftpb.Snapshot, error) {
	_ = ctx
	return s.loadSnapshot()
}

func (s *pebbleStore) Save(ctx context.Context, st multiraft.PersistentState) error {
	_ = ctx

	req := &writeRequest{
		scope: s.scope,
		op:    saveOp{state: st},
		done:  make(chan error, 1),
	}
	return s.db.submitWrite(req)
}

func (s *pebbleStore) MarkApplied(ctx context.Context, index uint64) error {
	_ = ctx

	req := &writeRequest{
		scope: s.scope,
		op:    markAppliedOp{index: index},
		done:  make(chan error, 1),
	}
	return s.db.submitWrite(req)
}
