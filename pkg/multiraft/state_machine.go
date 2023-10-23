package multiraft

import "go.etcd.io/raft/v3/raftpb"

type StateMachine interface {
	Apply(replicaID uint32, enties []raftpb.Entry) error
}
