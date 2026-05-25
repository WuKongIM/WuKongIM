package raftstore

import "go.etcd.io/raft/v3/raftpb"

func cloneEntry(entry raftpb.Entry) raftpb.Entry {
	cloned := entry
	cloned.Data = append([]byte(nil), entry.Data...)
	return cloned
}

func cloneEntries(entries []raftpb.Entry) []raftpb.Entry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]raftpb.Entry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, cloneEntry(entry))
	}
	return out
}

func cloneConfState(state raftpb.ConfState) raftpb.ConfState {
	cloned := state
	cloned.Voters = append([]uint64(nil), state.Voters...)
	cloned.Learners = append([]uint64(nil), state.Learners...)
	cloned.VotersOutgoing = append([]uint64(nil), state.VotersOutgoing...)
	cloned.LearnersNext = append([]uint64(nil), state.LearnersNext...)
	return cloned
}

func cloneSnapshotMetadata(meta raftpb.SnapshotMetadata) raftpb.SnapshotMetadata {
	meta.ConfState = cloneConfState(meta.ConfState)
	return meta
}

func cloneSnapshot(snap raftpb.Snapshot) raftpb.Snapshot {
	return raftpb.Snapshot{Data: append([]byte(nil), snap.Data...), Metadata: cloneSnapshotMetadata(snap.Metadata)}
}
