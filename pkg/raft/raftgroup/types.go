package raftgroup

import "github.com/WuKongIM/WuKongIM/pkg/raft/types"

type Event struct {
	RaftKey string
	types.Event
	WaitC chan error
}
