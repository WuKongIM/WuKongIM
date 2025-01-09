package raftgroup

import "errors"

var (
	ErrGroupStopped = errors.New("raft group is stopped")
)
