package replica

import (
	"errors"
	"math"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

var (
	ErrProposalDropped              = errors.New("replica proposal dropped")
	ErrLeaderTermStartIndexNotFound = errors.New("leader term start index not found")
)

type Role uint8

const (
	None uint64 = 0
	All  uint64 = math.MaxUint64 - 1
)

const (
	RoleFollower Role = iota
	RoleCandidate
	RoleLeader
)

type MsgType uint16

const (
	MsgUnknown                  MsgType = iota // 未知
	MsgAppointLeaderReq                        // 任命领导请求 （上级领导发送任命消息给各节点，如果任命的是自己则变成领导，否者变成追随者，会一直尝试发送并且收到大多数响应后停止发送）
	MsgAppointLeaderResp                       // 任命领导响应 (上级领导)
	MsgPropose                                 // 提案（领导）
	MsgNotifySync                              // 通知追随者同步日志（领导）
	MsgSync                                    // 同步日志 （追随者）
	MsgSyncResp                                // 同步日志响应（领导）
	MsgLeaderTermStartIndexReq                 // 领导任期开始偏移量请求 （追随者）
	MsgLeaderTermStartIndexResp                // 领导任期开始偏移量响应（领导）
	MsgApplyLogsReq                            // 应用日志请求
	MsgApplyLogsResp                           // 应用日志响应

)

func (m MsgType) String() string {
	switch m {
	case MsgUnknown:
		return "MsgUnknown"
	case MsgAppointLeaderReq:
		return "MsgAppointLeaderReq"
	case MsgAppointLeaderResp:
		return "MsgAppointLeaderResp"
	case MsgPropose:
		return "MsgPropose"
	case MsgNotifySync:
		return "MsgNotifySync"
	case MsgSyncResp:
		return "MsgSyncResp"
	case MsgLeaderTermStartIndexResp:
		return "MsgLeaderTermStartIndexResp"
	case MsgSync:
		return "MsgSync"
	case MsgLeaderTermStartIndexReq:
		return "MsgLeaderTermStartIndexReq"
	case MsgApplyLogsReq:
		return "MsgApplyLogsReq"
	case MsgApplyLogsResp:
		return "MsgApplyLogsResp"
	default:
		return "unknown"
	}
}

type Message struct {
	No                string  // 消息编号，用户消息重复判断，不需要判断的此字段为空
	MsgType           MsgType // 消息类型
	From              uint64
	To                uint64
	Term              uint32 // 领导任期
	AppointmentLeader uint64 // 任命的领导
	Logs              []Log
	Reject            bool // 拒绝
	Index             uint64
	CommittedIndex    uint64 // 已提交日志下标
}

func (m Message) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(m.No)
	enc.WriteUint16(uint16(m.MsgType))
	enc.WriteUint64(m.From)
	enc.WriteUint64(m.To)
	enc.WriteUint32(m.Term)
	enc.WriteUint64(m.AppointmentLeader)
	var rejectI uint8
	if m.Reject {
		rejectI = 1
	}
	enc.WriteUint8(rejectI)
	enc.WriteUint64(m.Index)
	enc.WriteUint64(m.CommittedIndex)

	enc.WriteUint32(uint32(len(m.Logs)))
	for _, l := range m.Logs {
		logData, err := l.Marshal()
		if err != nil {
			return nil, err
		}
		enc.WriteBinary(logData)
	}
	return enc.Bytes(), nil
}

func UnmarshalMessage(data []byte) (Message, error) {
	m := Message{}
	dec := wkproto.NewDecoder(data)
	var err error
	if m.No, err = dec.String(); err != nil {
		return m, err
	}
	var msgType uint16
	if msgType, err = dec.Uint16(); err != nil {
		return m, err
	}
	m.MsgType = MsgType(msgType)
	if m.From, err = dec.Uint64(); err != nil {
		return m, err
	}
	if m.To, err = dec.Uint64(); err != nil {
		return m, err
	}
	if m.Term, err = dec.Uint32(); err != nil {
		return m, err
	}
	if m.AppointmentLeader, err = dec.Uint64(); err != nil {
		return m, err
	}
	var rejectI uint8
	if rejectI, err = dec.Uint8(); err != nil {
		return m, err
	}
	if rejectI == 1 {
		m.Reject = true
	}
	if m.Index, err = dec.Uint64(); err != nil {
		return m, err
	}
	if m.CommittedIndex, err = dec.Uint64(); err != nil {
		return m, err
	}
	logsLen, err := dec.Uint32()
	if err != nil {
		return m, err
	}
	for i := uint32(0); i < logsLen; i++ {
		logData, err := dec.Binary()
		if err != nil {
			return m, err
		}
		l := &Log{}
		if err := l.Unmarshal(logData); err != nil {
			return m, err
		}
		m.Logs = append(m.Logs, *l)
	}
	return m, nil
}

type Ready struct {
	Messages []Message
}

func IsEmptyReady(rd Ready) bool {
	return len(rd.Messages) == 0
}

type Log struct {
	Index uint64 // 日志下标
	Term  uint32 // 领导任期
	Data  []byte // 日志数据
}

func (l *Log) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(l.Index)
	enc.WriteUint32(l.Term)
	enc.WriteBinary(l.Data)
	return enc.Bytes(), nil
}

func (l *Log) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if l.Index, err = dec.Uint64(); err != nil {
		return err
	}
	if l.Term, err = dec.Uint32(); err != nil {
		return err
	}
	if l.Data, err = dec.Binary(); err != nil {
		return err
	}
	return nil
}

func (l *Log) LogSize() int {
	return 8 + 4 + len(l.Data)
}

// 同步信息
type SyncInfo struct {
	NodeID           uint64 // 节点ID
	LastSyncLogIndex uint64 // 最后一次来同步日志的下标（一般最新日志 + 1）
	LastSyncTime     uint64 // 最后一次同步时间
}
