package multiraft

import (
	"time"

	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

type Options struct {
	RootDir            string // RootDir is the root directory for all raft data
	Peers              []Peer
	PeerID             uint64
	Addr               string // Addr is the address for raft transport example: tcp://0.0.0.0:11000
	StateMachine       StateMachine
	Transporter        Transporter
	ReplicaRaftStorage ReplicaRaftStorage
	LeaderChange       func(replicaID uint32, newLeaderID, oldLeaderID uint64)
}

func NewOptions() *Options {
	return &Options{}
}

type RaftOptions struct {
	Peers []Peer
	*raft.Config
	// Transporter  Transporter
	RaftStorage  RaftStorage
	DataDir      string
	LeaderChange func(newLeaderID, oldLeaderID uint64)
	Heartbeat    time.Duration                     // raft heartbeat interval
	OnApply      func(enties []raftpb.Entry) error // apply enties to state machine
	OnSend       func(msgs raftpb.Message) error   // send msgs to peers
}

func NewRaftOptions() *RaftOptions {

	return &RaftOptions{
		Heartbeat: 100 * time.Millisecond,
		Config: &raft.Config{
			AsyncStorageWrites:       true,
			ElectionTick:             4,
			HeartbeatTick:            2,
			PreVote:                  true,
			CheckQuorum:              true,
			MaxInflightMsgs:          4096 / 8,
			MaxSizePerMsg:            1 * 1024 * 1024,
			MaxCommittedSizePerReady: 2048,
		},
	}
}

type ReplicaOptions struct {
	ReplicaID          uint32
	PeerID             uint64
	Peers              []Peer
	MaxReplicaCount    uint32
	ReplicaRaftStorage ReplicaRaftStorage
	StateMachine       StateMachine
	Transporter        Transporter
	DataDir            string
	LeaderChange       func(newLeaderID, oldLeaderID uint64)
	// *RaftOptions
}

func NewReplicaOptions() *ReplicaOptions {
	return &ReplicaOptions{
		MaxReplicaCount: 3,
	}
}
