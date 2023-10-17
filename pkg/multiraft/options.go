package multiraft

import (
	"go.etcd.io/raft/v3"
)

type Options struct {
	SlotCount int // slot count

}

func NewOptions() *Options {
	return &Options{
		SlotCount: 256,
	}
}

type Option func(opts *Options)

func WithSlotCount(slotCount int) Option {
	return func(opts *Options) {
		opts.SlotCount = slotCount
	}
}

type ReplicaOptions struct {
	ReplicaID uint32
}

type RaftOptions struct {
	*raft.Config
	StateMachine StateMachine
	Transporter  Transporter
	SateStorage  SateStorage
	DataDir      string
}

func NewRaftOptions() *RaftOptions {

	return &RaftOptions{
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
