// Package raft wraps ControllerV2 Raft coordination.
//
// The service owns one etcd RawNode, persists Ready entries to a dedicated
// ControllerV2 WAL before Advance, and applies committed commands through a
// FIFO scheduler. The scheduler batches normal command entries, persists
// cluster-state.json once per batch through the FSM, advances applied metadata
// in raftstore, and triggers ControllerV2 snapshots plus WAL compaction.
//
// A single local voter is still treated as a single-node cluster; it uses the
// same WAL, scheduler, snapshot, and apply path as any other Controller voter.
package raft
