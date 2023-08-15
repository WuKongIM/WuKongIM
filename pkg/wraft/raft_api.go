package wraft

import (
	"go.etcd.io/raft/v3/raftpb"
)

type Storage interface {
	// Save function saves ents and state to the underlying stable storage.
	// Save MUST block until st and ents are on stable storage.
	Save(st raftpb.HardState, ents []raftpb.Entry) error
	// SaveSnap function saves snapshot to the underlying stable storage.
	SaveSnap(snap raftpb.Snapshot) error
	// Close closes the Storage and performs finalization.
	Close() error
	// Release releases the locked wal files older than the provided snapshot.
	Release(snap raftpb.Snapshot) error
	// Sync WAL
	Sync() error
}

type RaftReadyHandler struct {
	GetLead              func() (lead uint64)
	UpdateLead           func(lead uint64)
	UpdateLeadership     func(newLeader bool)
	UpdateCommittedIndex func(uint64)
}

// ToApply contains entries, snapshot to be applied. Once
// an toApply is consumed, the entries will be persisted to
// raft storage concurrently; the application must read
// notifyc before assuming the raft messages are stable.
type ToApply struct {
	Entries  []raftpb.Entry
	Snapshot raftpb.Snapshot
	// notifyc synchronizes etcd server applies with the raft node
	Notifyc chan struct{}
	// raftAdvancedC notifies EtcdServer.apply that
	// 'raftLog.applied' has advanced by r.Advance
	// it should be used only when entries contain raftpb.EntryConfChange
	RaftAdvancedC <-chan struct{}
}

func (r *RaftNode) Applyc() chan ToApply {

	return r.applyc
}
