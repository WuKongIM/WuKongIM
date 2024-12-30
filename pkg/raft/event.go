package raft

type EventType uint16

const (
	// Unknown 未知
	Unknown EventType = iota
	// Ping 心跳， leader -> follower, follower节点收到心跳后需要发起Sync来响应
	Ping
	// ConfChange 配置变更， all node
	ConfChange
	// Propose 提案， leader
	Propose
	// SyncReq 同步 ， follower -> leader
	SyncReq
	// SyncResp 同步响应， leader -> follower
	SyncResp
	// VoteReq 投票， candidate
	VoteReq
	// VoteResp 投票响应， all node -> candidate
	VoteResp
	// StorageReq 存储日志, local event
	StorageReq
	// StorageResp 存储日志响应, local event
	StorageResp
	// ApplyReq 应用日志， local event
	ApplyReq
	// ApplyResp 应用日志响应， local event
	ApplyResp
)

func (e EventType) String() string {
	switch e {
	case Unknown:
		return "Unknown"
	case Ping:
		return "Ping"
	case ConfChange:
		return "ConfChange"
	case Propose:
		return "Propose"
	case SyncReq:
		return "SyncReq"
	case SyncResp:
		return "SyncResp"
	case VoteReq:
		return "VoteReq"
	case VoteResp:
		return "VoteResp"
	case StorageReq:
		return "StorageReq"
	case StorageResp:
		return "StorageResp"
	case ApplyReq:
		return "ApplyReq"
	case ApplyResp:
		return "ApplyResp"
	default:
		return "Unknown"
	}
}

type Event struct {
	// Type 事件类型
	Type EventType
	// From 事件发起者
	From uint64
	// To 事件接收者
	To uint64
	// Term 任期
	Term uint32
	// Index 日志下标
	Index uint64
	// CommittedIndex 已提交日志下标
	CommittedIndex uint64
	// Logs 日志
	Logs []*Log
	// Reject 是否拒绝
	Reject bool

	Config Config
}
