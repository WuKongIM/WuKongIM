package raft

import "fmt"

type EventType uint16

const (
	// Unknown 未知
	Unknown EventType = iota
	// 重新选举
	Campaign
	// Ping 心跳， leader -> follower, follower节点收到心跳后需要发起Sync来响应
	Ping
	// ConfChange 配置变更， all node
	ConfChange
	// Propose 提案， leader
	Propose
	// NotifySync 通知副本过来同步日志 leader -> follower
	NotifySync
	// SyncReq 同步 ， follower -> leader
	SyncReq
	// GetLogsReq 获取日志请求， local event
	GetLogsReq
	// GetLogsResp 获取日志数据响应， local event
	GetLogsResp
	// SyncResp 同步响应， leader -> follower
	SyncResp
	// VoteReq 投票， candidate
	VoteReq
	// VoteResp 投票响应， all node -> candidate
	VoteResp
	// StoreReq 存储日志, local event
	StoreReq
	// StoreResp 存储日志响应, local event
	StoreResp
	// ApplyReq 应用日志， local event
	ApplyReq
	// ApplyResp 应用日志响应， local event
	ApplyResp
)

func (e EventType) String() string {
	switch e {
	case Unknown:
		return "Unknown"
	case Campaign:
		return "Campaign"
	case Ping:
		return "Ping"
	case ConfChange:
		return "ConfChange"
	case Propose:
		return "Propose"
	case NotifySync:
		return "NotifySync"
	case SyncReq:
		return "SyncReq"
	case GetLogsReq:
		return "GetLogsReq"
	case GetLogsResp:
		return "GetLogsResp"
	case SyncResp:
		return "SyncResp"
	case VoteReq:
		return "VoteReq"
	case VoteResp:
		return "VoteResp"
	case StoreReq:
		return "StoreReq"
	case StoreResp:
		return "StoreResp"
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
	Logs []Log
	// Reason 原因
	Reason Reason
	// StoredIndex 已存储日志下标
	StoredIndex uint64

	// 最新的日志任期（与Term不同。Term是当前服务的任期）
	LastLogTerm uint32

	Config Config
}

func (e Event) String() string {
	var str string

	// 事件类型
	str += fmt.Sprintf("Type: %v ", e.Type)

	// 事件发起者
	if e.From != 0 {
		str += fmt.Sprintf("From: %d ", e.From)
	}

	// 事件接收者
	if e.To != 0 {
		str += fmt.Sprintf("To: %d ", e.To)
	}

	// 任期
	if e.Term != 0 {
		str += fmt.Sprintf("Term: %d ", e.Term)
	}

	// 日志下标
	if e.Index != 0 {
		str += fmt.Sprintf("Index: %d ", e.Index)
	}

	// 已提交日志下标
	if e.CommittedIndex != 0 {
		str += fmt.Sprintf("CommittedIndex: %d ", e.CommittedIndex)
	}

	// 日志内容
	if len(e.Logs) > 0 {
		str += fmt.Sprintf("Logs: %v ", e.Logs)
	}

	// 是否拒绝
	str += fmt.Sprintf("Reason: %v ", e.Reason)

	return str
}
