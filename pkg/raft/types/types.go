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

	// LearnerToLeaderReq 学习者转领导者 leader
	LearnerToLeaderReq
	// LearnerToLeaderResp 学习者转领导者响应 leader
	LearnerToLeaderResp

	// LearnerToFollowerReq 学习者转跟随者 leader
	LearnerToFollowerReq
	// LearnerToFollowerResp 学习者转跟随者响应 leader
	LearnerToFollowerResp

	// FollowerToLeaderReq 跟随者转领导者 leader
	FollowerToLeaderReq
	// FollowerToLeaderResp 跟随者转领导者响应 leader
	FollowerToLeaderResp
	// ConfigReq 配置请求
	ConfigReq
	// ConfigResp 配置响应
	ConfigResp
	// Destory 销毁节点
	Destory
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
	case LearnerToLeaderReq:
		return "LearnerToLeaderReq"
	case LearnerToLeaderResp:
		return "LearnerToLeaderResp"
	case LearnerToFollowerReq:
		return "LearnerToFollowerReq"
	case LearnerToFollowerResp:
		return "LearnerToFollowerReq"
	case FollowerToLeaderReq:
		return "FollowerToLeaderReq"
	case FollowerToLeaderResp:
		return "FollowerToLeaderResp"
	case ConfigReq:
		return "ConfigReq"
	case ConfigResp:
		return "ConfigResp"
	case Destory:
		return "Destory"
	default:
		return "Unknown"
	}
}

type Speed uint8

const (
	// SpeedFast 快速
	SpeedFast Speed = iota
	// SpeedSuspend 暂停
	SpeedSuspend
)

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
	// StoredIndex 已存储日志下标
	StoredIndex uint64
	// 最新的日志任期（与Term不同。Term是当前服务的任期）
	LastLogTerm uint32
	// 配置版本
	ConfigVersion uint64
	Config        Config
	// Logs 日志
	Logs []Log

	// Reason 原因
	Reason Reason

	// Speed 同步速度
	Speed Speed

	// 不参与编码
	TermStartIndexInfo *TermStartIndexInfo
	StartIndex         uint64
	EndIndex           uint64
}

const (
	typeFlag uint32 = 1 << iota
	fromFlag
	toFlag
	termFlag
	indexFlag
	committedIndexFlag
	storedIndexFlag
	lastLogTermFlag
	configVersionFlag
	configFlag
	logsFlag
	reasonFlag
	speedFlag
)

func (e Event) Marshal() ([]byte, error) {
	flag := e.getFlag()

	enc := wkproto.NewEncoder()
	defer enc.End()

	enc.WriteUint32(flag)

	if flag&typeFlag != 0 {
		enc.WriteUint16(uint16(e.Type))
	}
	if flag&fromFlag != 0 {
		enc.WriteUint64(e.From)
	}
	if flag&toFlag != 0 {
		enc.WriteUint64(e.To)
	}
	if flag&termFlag != 0 {
		enc.WriteUint32(e.Term)
	}
	if flag&indexFlag != 0 {
		enc.WriteUint64(e.Index)
	}
	if flag&committedIndexFlag != 0 {
		enc.WriteUint64(e.CommittedIndex)
	}
	if flag&storedIndexFlag != 0 {
		enc.WriteUint64(e.StoredIndex)
	}
	if flag&lastLogTermFlag != 0 {
		enc.WriteUint32(e.LastLogTerm)
	}

	if flag&configVersionFlag != 0 {
		enc.WriteUint64(e.ConfigVersion)
	}
	if flag&configFlag != 0 {
		enc.WriteUint64(e.Config.MigrateFrom)
		enc.WriteUint64(e.Config.MigrateTo)
		enc.WriteUint32(uint32(len(e.Config.Replicas)))
		for _, v := range e.Config.Replicas {
			enc.WriteUint64(v)
		}
		enc.WriteUint32(uint32(len(e.Config.Learners)))
		for _, v := range e.Config.Learners {
			enc.WriteUint64(v)
		}
		enc.WriteUint8(uint8(e.Config.Role))
		enc.WriteUint32(e.Config.Term)
		enc.WriteUint64(e.Config.Version)
		enc.WriteUint64(e.Config.Leader)
	}
	if flag&logsFlag != 0 {
		enc.WriteUint32(uint32(len(e.Logs)))
		for _, v := range e.Logs {
			data, err := v.Marshal()
			if err != nil {
				return nil, err
			}
			enc.WriteUint32(uint32(len(data)))
			enc.WriteBytes(data)
		}
	}
	if flag&reasonFlag != 0 {
		enc.WriteUint8(uint8(e.Reason))
	}
	if flag&speedFlag != 0 {
		enc.WriteUint8(uint8(e.Speed))
	}

	return enc.Bytes(), nil
}

func (e *Event) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	flag, err := dec.Uint32()
	if err != nil {
		return err
	}
	if flag&typeFlag != 0 {
		t, err := dec.Uint16()
		if err != nil {
			return err
		}
		e.Type = EventType(t)
	}
	if flag&fromFlag != 0 {
		from, err := dec.Uint64()
		if err != nil {
			return err
		}
		e.From = from
	}
	if flag&toFlag != 0 {
		to, err := dec.Uint64()
		if err != nil {
			return err
		}
		e.To = to
	}
	if flag&termFlag != 0 {
		term, err := dec.Uint32()
		if err != nil {
			return err
		}
		e.Term = term
	}
	if flag&indexFlag != 0 {
		index, err := dec.Uint64()
		if err != nil {
			return err
		}
		e.Index = index
	}
	if flag&committedIndexFlag != 0 {
		committedIndex, err := dec.Uint64()
		if err != nil {
			return err
		}
		e.CommittedIndex = committedIndex
	}
	if flag&storedIndexFlag != 0 {
		storedIndex, err := dec.Uint64()
		if err != nil {
			return err
		}
		e.StoredIndex = storedIndex
	}
	if flag&lastLogTermFlag != 0 {
		lastLogTerm, err := dec.Uint32()
		if err != nil {
			return err
		}
		e.LastLogTerm = lastLogTerm
	}

	if flag&configVersionFlag != 0 {
		configVersion, err := dec.Uint64()
		if err != nil {
			return err
		}
		e.ConfigVersion = configVersion
	}

	if flag&configFlag != 0 {
		var config Config
		if config.MigrateFrom, err = dec.Uint64(); err != nil {
			return err
		}
		if config.MigrateTo, err = dec.Uint64(); err != nil {
			return err
		}
		replicasLen, err := dec.Uint32()
		if err != nil {
			return err
		}
		for i := 0; i < int(replicasLen); i++ {
			replica, err := dec.Uint64()
			if err != nil {
				return err
			}
			config.Replicas = append(config.Replicas, replica)
		}
		learnersLen, err := dec.Uint32()
		if err != nil {
			return err
		}
		for i := 0; i < int(learnersLen); i++ {
			learner, err := dec.Uint64()
			if err != nil {
				return err
			}
			config.Learners = append(config.Learners, learner)
		}
		role, err := dec.Uint8()
		if err != nil {
			return err
		}
		config.Role = Role(role)
		if config.Term, err = dec.Uint32(); err != nil {
			return err
		}
		if config.Version, err = dec.Uint64(); err != nil {
			return err
		}

		if config.Leader, err = dec.Uint64(); err != nil {
			return err
		}

		e.Config = config
	}
	if flag&logsFlag != 0 {
		logsLen, err := dec.Uint32()
		if err != nil {
			return err
		}
		for i := 0; i < int(logsLen); i++ {
			var log Log
			dataLen, err := dec.Uint32()
			if err != nil {
				return err
			}

			data, err := dec.Bytes(int(dataLen))
			if err != nil {
				return err
			}
			if err := log.Unmarshal(data); err != nil {
				return err
			}
			e.Logs = append(e.Logs, log)
		}
	}
	if flag&reasonFlag != 0 {
		reason, err := dec.Uint8()
		if err != nil {
			return err
		}
		e.Reason = Reason(reason)
	}

	if flag&speedFlag != 0 {
		speed, err := dec.Uint8()
		if err != nil {
			return err
		}
		e.Speed = Speed(speed)
	}
	return nil
}

func (e Event) getFlag() uint32 {
	var flag uint32
	if e.Type != Unknown {
		flag |= typeFlag
	}
	if e.From != 0 {
		flag |= fromFlag
	}
	if e.To != 0 {
		flag |= toFlag
	}
	if e.Term != 0 {
		flag |= termFlag
	}
	if e.Index != 0 {
		flag |= indexFlag
	}
	if e.CommittedIndex != 0 {
		flag |= committedIndexFlag
	}
	if e.StoredIndex != 0 {
		flag |= storedIndexFlag
	}
	if e.LastLogTerm != 0 {
		flag |= lastLogTermFlag
	}
	if e.ConfigVersion != 0 {
		flag |= configVersionFlag
	}
	if !e.Config.IsEmpty() {
		flag |= configFlag
	}
	if len(e.Logs) > 0 {
		flag |= logsFlag
	}
	if e.Reason != ReasonUnknown {
		flag |= reasonFlag
	}
	if e.Speed != SpeedFast {
		flag |= speedFlag
	}
	return flag
}

func (e Event) Size() uint64 {

	size := uint64(4) // flag
	if e.Type != Unknown {
		size += 2
	}
	if e.From != 0 {
		size += 8
	}
	if e.To != 0 {
		size += 8
	}
	if e.Term != 0 {
		size += 4
	}
	if e.Index != 0 {
		size += 8
	}
	if e.CommittedIndex != 0 {
		size += 8
	}
	if e.StoredIndex != 0 {
		size += 8
	}
	if e.LastLogTerm != 0 {
		size += 4
	}
	if e.ConfigVersion != 0 {
		size += 8
	}
	if !e.Config.IsEmpty() {
		size += e.Config.Size()
	}
	if len(e.Logs) > 0 {
		size += 4
		for _, v := range e.Logs {
			size += (uint64(4) + uint64(v.LogSize()))
		}
	}
	if e.Reason != ReasonUnknown {
		size += 1
	}
	if e.Speed != SpeedFast {
		size += 1
	}
	return size
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
	Leader      uint64   // 领导ID
}

// Clone 返回 Config 结构体的一个深拷贝
func (c Config) Clone() Config {
	return Config{
		MigrateFrom: c.MigrateFrom,
		MigrateTo:   c.MigrateTo,
		Replicas:    append([]uint64{}, c.Replicas...), // 深拷贝切片
		Learners:    append([]uint64{}, c.Learners...), // 深拷贝切片
		Role:        c.Role,
		Term:        c.Term,
		Version:     c.Version,
		Leader:      c.Leader,
	}
}

func (c Config) Size() uint64 {
	var size uint64
	size += 8 + 8 + 4 + 4 + 8 + 8
	size += 4 + uint64(len(c.Replicas))*8
	size += 4 + uint64(len(c.Learners))*8
	return size
}

func (c Config) String() string {

	return fmt.Sprintf("MigrateFrom: %d, MigrateTo: %d, Replicas: %v, Learners: %v, Role: %s, Term: %d, Version: %d, Leader: %d", c.MigrateFrom, c.MigrateTo, c.Replicas, c.Learners, c.Role, c.Term, c.Version, c.Leader)
}

func (c Config) IsEmpty() bool {
	return c.MigrateFrom == 0 && c.MigrateTo == 0 && len(c.Replicas) == 0 && len(c.Learners) == 0 && c.Role == RoleUnknown && c.Term == 0 && c.Version == 0
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

func (t *TermStartIndexInfo) Clone() *TermStartIndexInfo {
	return &TermStartIndexInfo{
		Term:  t.Term,
		Index: t.Index,
	}
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
