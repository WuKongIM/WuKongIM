// Package raft wraps ControllerV2 Raft coordination.
//
// The service owns one etcd RawNode, WAL-backed log storage, Ready persistence,
// Raft message send order, scheduled apply, snapshots, and compaction. The
// scheduler batches normal command entries, persists cluster-state.json once per
// batch through the FSM, advances applied metadata in raftstore, and triggers
// ControllerV2 snapshots plus WAL compaction.
//
// A single local voter is still treated as a single-node cluster; it uses the
// same WAL, scheduler, snapshot, and apply path as any other Controller voter.
package raft
