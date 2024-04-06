package replica

import (
	"errors"
	"fmt"
	"math"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type logEncodingSize uint64

func LogsSize(logs []Log) logEncodingSize {
	var size logEncodingSize
	for _, log := range logs {
		size += logEncodingSize(log.LogSize())
	}
	return size
}

func limitSize(logs []Log, maxSize logEncodingSize) []Log {
	if len(logs) == 0 {
		return logs
	}
	size := logs[0].LogSize()
	for limit := 1; limit < len(logs); limit++ {
		size += logs[limit].LogSize()
		if logEncodingSize(size) > maxSize {
			return logs[:limit]
		}
	}
	return logs
}

func extend(dst, vals []Log) []Log {
	need := len(dst) + len(vals)
	if need <= cap(dst) {
		return append(dst, vals...) // does not allocate
	}
	buf := make([]Log, need, need) // allocates precisely what's needed
	copy(buf, dst)
	copy(buf[len(dst):], vals)
	return buf
}

const noLimit = math.MaxUint64

var (
	ErrProposalDropped              = errors.New("replica proposal dropped")
	ErrLeaderTermStartIndexNotFound = errors.New("leader term start index not found")
	ErrCompacted                    = errors.New("log compacted")
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
	MsgUnknown          MsgType = iota // 未知
	MsgAppointLeaderReq                // 任命领导请求 （上级领导发送任命消息给各节点，如果任命的是自己则变成领导，否者变成追随者，会一直尝试发送并且收到大多数响应后停止发送）
	// MsgAppointLeaderResp                       // 任命领导响应 (上级领导)
	MsgPropose // 提案（领导）
	// MsgNotifySync                 // 通知追随者同步日志（领导）
	// MsgNotifySyncAck              // 通知追随者同步日志回执（领导）
	MsgSync                     // 同步日志 （追随者）
	MsgSyncGet                  // 同步日志获取（领导，本地）
	MsgSyncGetResp              // 同步日志获取响应（追随者）
	MsgSyncResp                 // 同步日志响应（领导）
	MsgLeaderTermStartIndexReq  // 领导任期开始偏移量请求 （追随者）
	MsgLeaderTermStartIndexResp // 领导任期开始偏移量响应（领导）
	MsgStoreAppend              // 存储追加日志
	MsgStoreAppendResp          // 存储追加日志响应
	MsgApplyLogsReq             // 应用日志请求
	MsgApplyLogsResp            // 应用日志响应
	MsgPing                     // ping
	MsgPong
)

func (m MsgType) String() string {
	switch m {
	case MsgUnknown:
		return "MsgUnknown[0]"
	case MsgAppointLeaderReq:
		return "MsgAppointLeaderReq"
	// case MsgAppointLeaderResp:
	// return "MsgAppointLeaderResp"
	case MsgPropose:
		return "MsgPropose"
	case MsgSync:
		return "MsgSync"
	case MsgSyncGet:
		return "MsgSyncGet"
	case MsgSyncGetResp:
		return "MsgSyncGetResp"
	case MsgSyncResp:
		return "MsgSyncResp"
	case MsgLeaderTermStartIndexReq:
		return "MsgLeaderTermStartIndexReq"
	case MsgLeaderTermStartIndexResp:
		return "MsgLeaderTermStartIndexResp"
	case MsgApplyLogsReq:
		return "MsgApplyLogsReq"
	case MsgApplyLogsResp:
		return "MsgApplyLogsResp"
	case MsgPing:
		return "MsgPing"
	case MsgPong:
		return "MsgPong"
	case MsgStoreAppend:
		return "MsgStoreAppend"
	case MsgStoreAppendResp:
		return "MsgStoreAppendResp"
	default:
		return fmt.Sprintf("MsgUnkown[%d]", m)
	}
}

type Message struct {
	Id                uint64  // 消息id
	MsgType           MsgType // 消息类型
	From              uint64
	To                uint64
	Term              uint32 // 领导任期
	AppointmentLeader uint64 // 任命的领导
	Logs              []Log
	Reject            bool // 拒绝
	Index             uint64
	CommittedIndex    uint64 // 已提交日志下标

	Responses     []Message
	ApplyingIndex uint64 // 应用中的下表
	AppliedIndex  uint64

	// 追踪
	TraceIDs [][16]byte // 追踪ID
	SpanIDs  [][8]byte  // 跨度ID
}

func (m Message) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(m.Id)
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
	enc.WriteInt16(len(m.TraceIDs))
	for i, traceID := range m.TraceIDs {
		enc.WriteBytes(traceID[:])
		enc.WriteBytes(m.SpanIDs[i][:])
	}
	return enc.Bytes(), nil
}

func (m Message) Size() int {
	size := 8 + 2 + 8 + 8 + 4 + 8 + 1 + 8 + 8 + 4 // id + msgType + from + to + term + appointmentLeader + reject + index + committedIndex + logsLen
	for _, l := range m.Logs {
		size += l.LogSize()
	}
	size += 2 // traceIDsLen
	size += len(m.TraceIDs) * 16
	size += len(m.SpanIDs) * 8
	return size

}

func UnmarshalMessage(data []byte) (Message, error) {
	m := Message{}
	dec := wkproto.NewDecoder(data)
	var err error
	if m.Id, err = dec.Uint64(); err != nil {
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
	traceIDsLen, err := dec.Int16()
	if err != nil {
		return m, err
	}
	for i := int16(0); i < traceIDsLen; i++ {
		traceIDBytes, err := dec.Bytes(16)
		if err != nil {
			return m, err
		}
		spanIDBytes, err := dec.Bytes(8)
		if err != nil {
			return m, err
		}
		var traceID [16]byte
		copy(traceID[:], traceIDBytes)
		m.TraceIDs = append(m.TraceIDs, traceID)

		var spanID [8]byte
		copy(spanID[:], spanIDBytes)
		m.SpanIDs = append(m.SpanIDs, spanID)

	}
	return m, nil
}

type HardState struct {
	LeaderId uint64 // 领导ID
	Term     uint32 // 领导任期
}

var EmptyHardState = HardState{}

func IsEmptyHardState(hs HardState) bool {
	return hs.LeaderId == 0 && hs.Term == 0
}

type Ready struct {
	HardState HardState
	Messages  []Message
}

func IsEmptyReady(rd Ready) bool {
	return len(rd.Messages) == 0 && IsEmptyHardState(rd.HardState)
}

type Log struct {
	MessageId uint64
	Index     uint64 // 日志下标
	Term      uint32 // 领导任期
	Data      []byte // 日志数据
}

func (l *Log) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(l.MessageId)
	enc.WriteUint64(l.Index)
	enc.WriteUint32(l.Term)
	enc.WriteBinary(l.Data)
	return enc.Bytes(), nil
}

func (l *Log) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error

	if l.MessageId, err = dec.Uint64(); err != nil {
		return err
	}

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

func (s *SyncInfo) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(s.NodeID)
	enc.WriteUint64(s.LastSyncLogIndex)
	enc.WriteUint64(s.LastSyncTime)
	return enc.Bytes(), nil
}

func (s *SyncInfo) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if s.NodeID, err = dec.Uint64(); err != nil {
		return err
	}
	if s.LastSyncLogIndex, err = dec.Uint64(); err != nil {
		return err
	}
	if s.LastSyncTime, err = dec.Uint64(); err != nil {
		return err
	}
	return nil
}

func PrintMessages(msgs []Message) {
	for _, m := range msgs {
		fmt.Printf("id:%d, type:%s, from:%d, to:%d, term:%d, appointmentLeader:%d, reject:%v, index:%d, committedIndex:%d, logs:%d\n",
			m.Id, m.MsgType.String(), m.From, m.To, m.Term, m.AppointmentLeader, m.Reject, m.Index, m.CommittedIndex, len(m.Logs))
	}
}
