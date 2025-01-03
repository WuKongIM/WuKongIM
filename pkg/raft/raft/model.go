package raft

import "github.com/WuKongIM/WuKongIM/pkg/raft/types"

type stepReq struct {
	event types.Event
	resp  chan error
}
