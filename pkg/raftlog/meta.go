package raftlog

import (
	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/confchange"
	"go.etcd.io/raft/v3/raftpb"
	"go.etcd.io/raft/v3/tracker"
)

func cloneEntry(entry raftpb.Entry) raftpb.Entry {
	cloned := entry
	if len(entry.Data) > 0 {
		cloned.Data = append([]byte(nil), entry.Data...)
	}
	return cloned
}

func cloneSnapshot(snapshot raftpb.Snapshot) raftpb.Snapshot {
	cloned := snapshot
	if len(snapshot.Data) > 0 {
		cloned.Data = append([]byte(nil), snapshot.Data...)
	}
	cloned.Metadata.ConfState = cloneConfState(snapshot.Metadata.ConfState)
	return cloned
}

func cloneConfState(state raftpb.ConfState) raftpb.ConfState {
	cloned := state
	if len(state.Voters) > 0 {
		cloned.Voters = append([]uint64(nil), state.Voters...)
	}
	if len(state.Learners) > 0 {
		cloned.Learners = append([]uint64(nil), state.Learners...)
	}
	if len(state.VotersOutgoing) > 0 {
		cloned.VotersOutgoing = append([]uint64(nil), state.VotersOutgoing...)
	}
	if len(state.LearnersNext) > 0 {
		cloned.LearnersNext = append([]uint64(nil), state.LearnersNext...)
	}
	return cloned
}

func replaceEntriesFromIndex(existing []raftpb.Entry, first uint64, incoming []raftpb.Entry) []raftpb.Entry {
	result := make([]raftpb.Entry, 0, len(existing)+len(incoming))
	for _, entry := range existing {
		if entry.Index >= first {
			break
		}
		result = append(result, cloneEntry(entry))
	}
	for _, entry := range incoming {
		result = append(result, cloneEntry(entry))
	}
	return result
}

func trimEntriesAfterSnapshot(existing []raftpb.Entry, snapshotIndex uint64) []raftpb.Entry {
	result := make([]raftpb.Entry, 0, len(existing))
	for _, entry := range existing {
		if entry.Index <= snapshotIndex {
			continue
		}
		result = append(result, cloneEntry(entry))
	}
	return result
}

func deriveConfState(snapshot raftpb.Snapshot, entries []raftpb.Entry, committed uint64) (raftpb.ConfState, error) {
	var (
		base       raftpb.ConfState
		lastIndex  uint64
		progress   = tracker.MakeProgressTracker(1, 0)
		cfg        tracker.Config
		progresses tracker.ProgressMap
		err        error
	)

	if !raft.IsEmptySnap(snapshot) {
		base = cloneConfState(snapshot.Metadata.ConfState)
		lastIndex = snapshot.Metadata.Index
	}

	if !isZeroConfState(base) {
		cfg, progresses, err = confchange.Restore(confchange.Changer{
			Tracker:   progress,
			LastIndex: lastIndex,
		}, base)
		if err != nil {
			return raftpb.ConfState{}, err
		}
		progress.Config = cfg
		progress.Progress = progresses
	}

	if committed < lastIndex {
		committed = lastIndex
	}
	for _, entry := range entries {
		if entry.Index <= lastIndex || entry.Index > committed {
			continue
		}

		switch entry.Type {
		case raftpb.EntryConfChange:
			var cc raftpb.ConfChange
			if err := cc.Unmarshal(entry.Data); err != nil {
				return raftpb.ConfState{}, err
			}
			nextCfg, nextProgress, err := applyConfChange(progress, entry.Index, cc.AsV2())
			if err != nil {
				return raftpb.ConfState{}, err
			}
			progress.Config = nextCfg
			progress.Progress = nextProgress
		case raftpb.EntryConfChangeV2:
			var cc raftpb.ConfChangeV2
			if err := cc.Unmarshal(entry.Data); err != nil {
				return raftpb.ConfState{}, err
			}
			nextCfg, nextProgress, err := applyConfChange(progress, entry.Index, cc)
			if err != nil {
				return raftpb.ConfState{}, err
			}
			progress.Config = nextCfg
			progress.Progress = nextProgress
		}
	}

	return cloneConfState(progress.ConfState()), nil
}

func applyConfChange(
	progress tracker.ProgressTracker,
	lastIndex uint64,
	change raftpb.ConfChangeV2,
) (tracker.Config, tracker.ProgressMap, error) {
	changer := confchange.Changer{
		Tracker:   progress,
		LastIndex: lastIndex,
	}

	if change.LeaveJoint() {
		return changer.LeaveJoint()
	}
	if autoLeave, ok := change.EnterJoint(); ok {
		return changer.EnterJoint(autoLeave, change.Changes...)
	}
	return changer.Simple(change.Changes...)
}

func isZeroConfState(state raftpb.ConfState) bool {
	return len(state.Voters) == 0 &&
		len(state.Learners) == 0 &&
		len(state.VotersOutgoing) == 0 &&
		len(state.LearnersNext) == 0 &&
		!state.AutoLeave
}

func updateLogMeta(meta *logMeta, snapshot raftpb.Snapshot, entries []raftpb.Entry, committed uint64) error {
	if meta == nil {
		return nil
	}

	meta.SnapshotIndex = snapshot.Metadata.Index
	meta.SnapshotTerm = snapshot.Metadata.Term

	confState, err := deriveConfState(snapshot, entries, committed)
	if err != nil {
		return err
	}
	meta.ConfState = cloneConfState(confState)

	switch {
	case len(entries) > 0:
		meta.FirstIndex = entries[0].Index
		meta.LastIndex = entries[len(entries)-1].Index
	case !raft.IsEmptySnap(snapshot):
		meta.FirstIndex = snapshot.Metadata.Index + 1
		meta.LastIndex = snapshot.Metadata.Index
	default:
		meta.FirstIndex = 1
		meta.LastIndex = 0
	}

	return nil
}
