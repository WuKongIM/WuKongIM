package multiraft

import "go.etcd.io/raft/v3/raftpb"

type StateMachine interface {
	Apply(enties []raftpb.Entry) error
}
