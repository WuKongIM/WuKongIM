package types

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/raft/track"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type Reason uint8

const (
	ReasonUnknown Reason = iota
	// ReasonOk 同意
	ReasonOk
	// ReasonError 错误
	ReasonError
	// ReasonTruncate 日志需要截断
	ReasonTruncate
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
	case ReasonTruncate:
		return "ReasonTruncate"
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
	// SendPropose 发送提案， follower -> leader
	SendPropose
	SendProposeResp
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
	// TruncateReq 裁剪请求
	TruncateReq
	// TruncateResp 裁剪返回
	TruncateResp
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
	// NodeOfflineReq 节点下线请求
	NodeOfflineReq
	// NodeOfflineResp 节点下线响应
	NodeOfflineResp
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
	case SendPropose:
		return "SendPropose"
	case SendProposeResp:
		return "SendProposeResp"
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
	case TruncateReq:
		return "TruncateReq"
	case TruncateResp:
		return "TruncateResp"
	case NodeOfflineReq:
		return "NodeOfflineReq"
	case NodeOfflineResp:
		return "NodeOfflineResp"
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
	StartIndex         uint64
	EndIndex           uint64
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

	// 日志轨迹记录
	Record track.Record
	// 不参与编码
	Time time.Time // 日志时间
}

func (l *Log) Marshal() ([]byte, error) {
	resultBytes := make([]byte, l.LogSize())
	binary.BigEndian.PutUint64(resultBytes[0:8], l.Id)
	binary.BigEndian.PutUint64(resultBytes[8:16], l.Index)
	binary.BigEndian.PutUint32(resultBytes[16:20], l.Term)
	copy(resultBytes[20:], l.Data)
	return resultBytes, nil
}

func (l *Log) Unmarshal(data []byte) error {
	if len(data) < 20 {
		return fmt.Errorf("log data is too short[%d]", len(data))
	}
	l.Id = binary.BigEndian.Uint64(data[0:8])
	l.Index = binary.BigEndian.Uint64(data[8:16])
	l.Term = binary.BigEndian.Uint32(data[16:20])
	l.Data = data[20:]
	return nil
}

func (l *Log) LogSize() int {
	return 8 + 8 + 4 + len(l.Data) // id + index + term  + data
}

var EmptyLog = Log{}

func IsEmptyLog(v Log) bool {
	return v.Id == 0 && v.Index == 0 && v.Term == 0 && len(v.Data) == 0
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

type ConfigEventType uint8

const (
	ConfigEventUnknown ConfigEventType = iota
)

type ConfigEvent struct {
	Type ConfigEventType
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

func (r Role) String() string {
	switch r {
	case RoleFollower:
		return "Follower"
	case RoleCandidate:
		return "Candidate"
	case RoleLeader:
		return "Leader"
	case RoleLearner:
		return "Learner"
	default:
		return "Unknown"
	}
}

// 任期对应的开始日志下标
type TermStartIndexInfo struct {
	Term  uint32
	Index uint64
}

// 本节点
const LocalNode = math.MaxUint64

type RaftState struct {
	// LastLogIndex 最后一个日志的下标
	LastLogIndex uint64
	// LastTerm 最后一个日志的任期
	LastTerm uint32
	// AppliedIndex 已应用的日志下标
	AppliedIndex uint64
}

// ProposeResp 提案返回
type ProposeResp struct {
	Id    uint64
	Index uint64
}

type ProposeRespSet []*ProposeResp

func (p ProposeRespSet) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	enc.WriteUint32(uint32(len(p)))
	for _, v := range p {
		enc.WriteUint64(v.Id)
		enc.WriteUint64(v.Index)
	}
	return enc.Bytes(), nil
}

func (p *ProposeRespSet) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	var size uint32
	if size, err = dec.Uint32(); err != nil {
		return err
	}
	for i := 0; i < int(size); i++ {
		var resp ProposeResp
		id, err := dec.Uint64()
		if err != nil {
			return err
		}
		resp.Id = id
		index, err := dec.Uint64()
		if err != nil {
			return err
		}
		resp.Index = index
		*p = append(*p, &resp)
	}
	return nil
}

// ProposeReq 提案请求
type ProposeReq struct {
	Id   uint64
	Data []byte
}

func (p *ProposeReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	enc.WriteUint64(p.Id)
	enc.WriteBytes(p.Data)
	return enc.Bytes(), nil
}

func (p *ProposeReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if p.Id, err = dec.Uint64(); err != nil {
		return err
	}
	if p.Data, err = dec.BinaryAll(); err != nil {
		return err
	}
	return nil
}

type ProposeReqSet []ProposeReq

func (p ProposeReqSet) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(uint32(len(p)))
	for _, v := range p {
		enc.WriteUint64(v.Id)
		enc.WriteUint32(uint32(len(v.Data)))
		enc.WriteBytes(v.Data)
	}
	return enc.Bytes(), nil
}

func (p *ProposeReqSet) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	var size uint32
	if size, err = dec.Uint32(); err != nil {
		return err
	}
	for i := 0; i < int(size); i++ {
		var req ProposeReq
		id, err := dec.Uint64()
		if err != nil {
			return err
		}
		req.Id = id
		dataLen, err := dec.Uint32()
		if err != nil {
			return err
		}
		req.Data, err = dec.Bytes(int(dataLen))
		if err != nil {
			return err
		}
		*p = append(*p, req)
	}
	return nil
}

type ProposeForwardReq struct {
	Key string
	ProposeReqSet
}

func (p ProposeForwardReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	enc.WriteString(p.Key)
	data, err := p.ProposeReqSet.Marshal()
	if err != nil {
		return nil, err
	}
	enc.WriteBytes(data)
	return enc.Bytes(), nil
}

func (p *ProposeForwardReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if p.Key, err = dec.String(); err != nil {
		return err
	}
	data, err = dec.BinaryAll()
	if err != nil {
		return err
	}
	return p.ProposeReqSet.Unmarshal(data)
}

type ProposeForwardResp struct {
	Key string
	ProposeRespSet
}

func (p ProposeForwardResp) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(p.Key)
	data, err := p.ProposeRespSet.Marshal()
	if err != nil {
		return nil, err
	}
	enc.WriteBytes(data)
	return enc.Bytes(), nil
}

func (p *ProposeForwardResp) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if p.Key, err = dec.String(); err != nil {
		return err
	}
	data, err = dec.BinaryAll()
	if err != nil {
		return err
	}
	return p.ProposeRespSet.Unmarshal(data)
}
