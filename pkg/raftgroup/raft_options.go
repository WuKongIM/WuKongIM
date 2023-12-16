package raftgroup

import (
	"context"
	"time"

	"go.etcd.io/raft/v3/raftpb"
)

type RaftOption func(*RaftOptions)

type RaftOptions struct {
	NodeID       uint64                                                           // 节点ID
	ShardID      uint32                                                           // 分区ID
	Storage      IRaftStorage                                                     // storage
	Addr         string                                                           // 监听地址 格式 ip:port
	OnSend       func(ctx context.Context, raft *Raft, msgs raftpb.Message) error // send msgs to peers
	LeaderChange func(newLeaderID, oldLeaderID uint64)
	OnApply      func(raft *Raft, enties []raftpb.Entry) error
	Members      []Member
	Applied      uint64 // 已应用的日志索引

	Heartbeat     time.Duration // raft heartbeat interval
	HeartbeatTick int           // raft heartbeat tick
	ElectionTick  int           // raft election tick

	onSetReady            func(raft *Raft) // 设置就绪
	OnRaftTimeoutCampaign func(r *Raft)    // 选举超时

}

func NewRaftOptions() *RaftOptions {
	return &RaftOptions{
		HeartbeatTick: 1,
		ElectionTick:  10,
		Heartbeat:     100 * time.Millisecond,
	}
}

func RaftOptionWithMembers(members []Member) RaftOption {
	return func(opts *RaftOptions) {
		opts.Members = members
	}
}

type Member struct {
	NodeID     uint64 // 节点ID
	ShardID    uint32 // 分区ID
	ServerAddr string // 成员连接地址（节点连接地址）
}
