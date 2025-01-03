package types

import (
	"fmt"
	"math"
	"time"
)

type Reason uint8

const (
	ReasonUnknown Reason = iota
	// ReasonOk 同意
	ReasonOk
	// ReasonError 错误
	ReasonError
	// ReasonTrunctate 日志需要截断
	ReasonTrunctate
	// ReasonOnlySync 只是同步, 不做截断判断
	ReasonOnlySync
)

func (r Reason) Uint8() uint8 {
	return uint8(r)
}

func (r Reason) String() string {
	switch r {
	case ReasonOk:
		return "ReasonOk"
	case ReasonError:
		return "ReasonError"
	case ReasonTrunctate:
		return "ReasonTrunctate"
	case ReasonOnlySync:
		return "ReasonOnlySync"
	default:
		return fmt.Sprintf("ReasonUnknown[%d]", r)
	}
}

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

	// 不参与编码
	TermStartIndexInfo *TermStartIndexInfo
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

func (e Event) Size() uint64 {

	return 0
}

type Log struct {
	Id    uint64
	Index uint64 // 日志下标
	Term  uint32 // 领导任期
	Data  []byte // 日志数据

	// 不参与编码
	Time time.Time // 日志时间
}

type Config struct {
	MigrateFrom uint64   // 迁移源节点
	MigrateTo   uint64   // 迁移目标节点
	Replicas    []uint64 // 副本集合（不包含节点自己）
	Learners    []uint64 // 学习节点集合
	Role        Role     // 节点角色
	Term        uint32   // 领导任期
	Version     uint64   // 配置版本

	// 不参与编码
	Leader uint64 // 领导ID
}

type Role uint8

const (
	RoleUnknown Role = iota
	// RoleFollower 跟随者
	RoleFollower
	// RoleCandidate 候选人
	RoleCandidate
	// RoleLeader 领导者
	RoleLeader
	// RoleLearner 学习者
	RoleLearner
)

// 任期对应的开始日志下标
type TermStartIndexInfo struct {
	Term  uint32
	Index uint64
}

// 本节点
const LocalNode = math.MaxUint64
