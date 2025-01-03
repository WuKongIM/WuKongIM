package raftgroup

import "github.com/WuKongIM/WuKongIM/pkg/raft/types"

type IStorage interface {
	AppendLogs(raft IRaft, logs []types.Log, termStartIndexInfo *types.TermStartIndexInfo) error
}
