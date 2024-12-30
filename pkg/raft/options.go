package raft

type Options struct {
	// NodeId 节点ID
	NodeId uint64
	// SyncInterval 同步间隔, 单位: tick, 表示多少个tick发起一次同步
	SyncInterval int
	// ElectionOn 是否开启选举， 如果开启选举，那么节点之间会自己选举出一个领导者，默认为false
	ElectionOn bool

	// HeartbeatInterval 心跳间隔tick次数, 就是tick触发几次算一次心跳，一般为1 一次tick算一次心跳
	HeartbeatInterval int
	// ElectionInterval 选举间隔tick次数，超过此tick数则发起选举
	ElectionInterval int
	// Replicas 副本的节点id，不包含节点自己
	Replicas []uint64

	Log struct {
		// AppliedIndex 已应用的日志下标
		AppliedIndex uint64
		// LastIndex 最新日志下标
		LastIndex uint64
		// LastTerm 最新任期
		LastTerm uint32
	}
}

func NewOptions() *Options {
	return &Options{
		SyncInterval:      1,
		ElectionOn:        false,
		HeartbeatInterval: 1,
		ElectionInterval:  10,
	}
}
