package raft

import (
	etcdraft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

// stepRaftInput reports the received input's byte cost to the Ready turn owner.
func stepRaftInput(raw *etcdraft.RawNode, msg raftpb.Message) (uint64, error) {
	// Step may rewrite shared entry headers (for example a rejected ConfChange).
	// The turn must charge what arrived, even when the protocol clears payloads.
	bytes := uint64(msg.Size())
	return bytes, raw.Step(msg)
}
