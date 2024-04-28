package replica

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sync"

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
	RoleFollower  Role = iota // 追随者
	RoleCandidate             // 候选者
	RoleLeader                // 领导
	RoleLearner               // 学习者
)

type MsgType uint16

const (
	MsgUnknown  MsgType = iota // 未知
	MsgVoteReq                 // 请求投票
	MsgVoteResp                // 请求投票响应
	MsgPropose                 // 提案（领导）
	MsgHup                     // 开始选举
	// MsgNotifySync                 // 通知追随者同步日志（领导）
	// MsgNotifySyncAck              // 通知追随者同步日志回执（领导）
	MsgSyncReq                  // 同步日志 （追随者）
	MsgSyncGet                  // 同步日志获取（领导，本地）
	MsgSyncGetResp              // 同步日志获取响应（追随者）
	MsgSyncResp                 // 同步日志响应（领导）
	MsgLeaderTermStartIndexReq  // 领导任期开始偏移量请求 （追随者）
	MsgLeaderTermStartIndexResp // 领导任期开始偏移量响应（领导）
	MsgStoreAppend              // 存储追加日志
	MsgStoreAppendResp          // 存储追加日志响应
	MsgApplyLogs                // 应用日志请求
	MsgApplyLogsResp            // 应用日志响应
	MsgBeat                     // 触发ping
	MsgPing                     // ping
	MsgPong                     // pong
	MsgConfigReq                // 配置请求
	MsgConfigResp               // 配置响应
	MsgMaxValue
)

func (m MsgType) String() string {
	switch m {
	case MsgUnknown:
		return "MsgUnknown[0]"
	case MsgVoteReq:
		return "MsgVoteReq"
	case MsgVoteResp:
		return "MsgVoteResp"
	case MsgHup:
		return "MsgHup"
	case MsgPropose:
		return "MsgPropose"
	case MsgSyncReq:
		return "MsgSyncReq"
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
	case MsgApplyLogs:
		return "MsgApplyLogs"
	case MsgApplyLogsResp:
		return "MsgApplyLogsResp"
	case MsgBeat:
		return "MsgBeat"
	case MsgPing:
		return "MsgPing"
	case MsgPong:
		return "MsgPong"
	case MsgStoreAppend:
		return "MsgStoreAppend"
	case MsgStoreAppendResp:
		return "MsgStoreAppendResp"
	case MsgConfigReq:
		return "MsgConfigReq"
	case MsgConfigResp:
		return "MsgConfigResp"
	default:
		return fmt.Sprintf("MsgUnkown[%d]", m)
	}
}

var EmptyMessage = Message{}

type Message struct {
	MsgType        MsgType // 消息类型
	From           uint64
	To             uint64
	Term           uint32 // 领导任期
	Index          uint64
	CommittedIndex uint64 // 已提交日志下标

	ApplyingIndex uint64 // 应用中的下表
	AppliedIndex  uint64

	SpeedLevel  SpeedLevel // 只有msgSync和ping才编码
	Reject      bool       // 拒绝
	ConfVersion uint64     // 配置版本
	Logs        []Log
}

func (m Message) Marshal() ([]byte, error) {

	if m.MsgType == MsgSyncReq {
		return m.MarshalMsgSync(), nil
	}
	resultBytes := make([]byte, m.Size())
	binary.BigEndian.PutUint16(resultBytes[0:], uint16(m.MsgType))
	binary.BigEndian.PutUint64(resultBytes[2:], m.From)
	binary.BigEndian.PutUint64(resultBytes[10:], m.To)
	binary.BigEndian.PutUint32(resultBytes[18:], m.Term)
	binary.BigEndian.PutUint64(resultBytes[22:], m.Index)
	binary.BigEndian.PutUint64(resultBytes[30:], m.CommittedIndex)
	resultBytes[38] = byte(m.SpeedLevel)
	var b byte = 0
	if m.Reject {
		b = 1
	}
	resultBytes[39] = b

	binary.BigEndian.PutUint64(resultBytes[40:], m.ConfVersion)

	offset := 48
	for _, l := range m.Logs {
		logData, err := l.Marshal()
		if err != nil {
			return nil, err
		}
		binary.BigEndian.PutUint16(resultBytes[offset:], uint16(len(logData)))
		offset += 2
		copy(resultBytes[offset:], logData)
		offset += len(logData)
	}

	return resultBytes, nil
}

func (m Message) MarshalMsgSync() []byte {
	resultBytes := make([]byte, MsgSyncFixSize())
	binary.BigEndian.PutUint16(resultBytes[0:2], uint16(m.MsgType))
	binary.BigEndian.PutUint64(resultBytes[2:10], m.From)
	binary.BigEndian.PutUint64(resultBytes[10:18], m.To)
	binary.BigEndian.PutUint64(resultBytes[18:26], m.Index)
	resultBytes[26] = byte(m.SpeedLevel)
	return resultBytes
}

func UnmarshalMessage(data []byte) (Message, error) {

	msgType := binary.BigEndian.Uint16(data[0:2])
	if msgType == uint16(MsgSyncReq) {
		return UnmarshalMessageSync(data)
	}

	m := Message{}
	m.MsgType = MsgType(msgType)
	m.From = binary.BigEndian.Uint64(data[2:10])
	m.To = binary.BigEndian.Uint64(data[10:18])
	m.Term = binary.BigEndian.Uint32(data[18:22])
	m.Index = binary.BigEndian.Uint64(data[22:30])
	m.CommittedIndex = binary.BigEndian.Uint64(data[30:38])
	m.SpeedLevel = SpeedLevel(data[38])
	m.Reject = data[39] == 1
	m.ConfVersion = binary.BigEndian.Uint64(data[40:48])

	offset := 48
	for offset < len(data) {
		logLen := binary.BigEndian.Uint16(data[offset : offset+2])
		offset += 2
		l := Log{}
		if err := l.Unmarshal(data[offset : offset+int(logLen)]); err != nil {
			fmt.Println("unmarshal log error:", err, len(data), offset, logLen)
			return m, err
		}
		m.Logs = append(m.Logs, l)
		offset += int(logLen)
	}
	return m, nil
}

func UnmarshalMessageSync(data []byte) (Message, error) {
	m := Message{}
	m.MsgType = MsgSyncReq
	m.From = binary.BigEndian.Uint64(data[2:10])
	m.To = binary.BigEndian.Uint64(data[10:18])
	m.Index = binary.BigEndian.Uint64(data[18:26])
	m.SpeedLevel = SpeedLevel(data[26])
	return m, nil
}

func MsgSyncFixSize() int {
	return 2 + 8 + 8 + 8 + 1 // msgType + from + to + index + speedLevel
}

// func (m Message) MarshalWithEncoder(enc *wkproto.Encoder) error {

// 	resultBytes := make([]byte, m.Size())
// 	binary.BigEndian.PutUint16(resultBytes[0:], uint16(m.MsgType))
// 	binary.BigEndian.PutUint64(resultBytes[2:], m.From)
// 	binary.BigEndian.PutUint64(resultBytes[10:], m.To)
// 	binary.BigEndian.PutUint32(resultBytes[18:], m.Term)
// 	binary.BigEndian.PutUint64(resultBytes[22:], m.Index)
// 	binary.BigEndian.PutUint64(resultBytes[30:], m.CommittedIndex)

// 	for i, l := range m.Logs {
// 		logData, err := l.Marshal()
// 		if err != nil {
// 			return err
// 		}
// 		binary.BigEndian.PutUint16(resultBytes[38+i*2:], uint16(len(logData)))
// 		copy(resultBytes[38+i*2+2:], logData)
// 	}

// 	// enc.WriteUint16(uint16(m.MsgType))
// 	// enc.WriteUint64(m.From)
// 	// enc.WriteUint64(m.To)
// 	// enc.WriteUint32(m.Term)
// 	// enc.WriteUint64(m.Index)
// 	// enc.WriteUint64(m.CommittedIndex)

// 	// enc.WriteUint32(uint32(len(m.Logs)))
// 	// for _, l := range m.Logs {
// 	// 	logData, err := l.Marshal()
// 	// 	if err != nil {
// 	// 		return err
// 	// 	}
// 	// 	enc.WriteBinary(logData)
// 	// }
// 	return resultBytes, nil
// }

func (m Message) Size() int {
	size := 2 + 8 + 8 + 4 + 8 + 8 + 1 + 1 + 8 // msgType + from + to + term   + index + committedIndex + speedLevel +reject + confVersion
	for _, l := range m.Logs {
		size += 2 // log len
		size += l.LogSize()
	}
	return size

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

var EmptyLog = Log{}

func IsEmptyLog(v Log) bool {
	return v.Id == 0 && v.Index == 0 && v.Term == 0 && len(v.Data) == 0
}

type Log struct {
	Id    uint64
	Index uint64 // 日志下标
	Term  uint32 // 领导任期
	Data  []byte // 日志数据
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
	return 8 + 8 + 4 + len(l.Data) // messageId + index + term  + data
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
		fmt.Printf("type:%s, from:%d, to:%d, term:%d, index:%d, committedIndex:%d, logs:%d\n",
			m.MsgType.String(), m.From, m.To, m.Term, m.Index, m.CommittedIndex, len(m.Logs))
	}
}

var globalRand = &lockedRand{}

type lockedRand struct {
	mu sync.Mutex
}

func (r *lockedRand) Intn(n int) int {
	r.mu.Lock()
	v, _ := rand.Int(rand.Reader, big.NewInt(int64(n)))
	r.mu.Unlock()
	return int(v.Int64())
}

type SpeedLevel uint8

const (
	LevelFast    SpeedLevel = iota // 最快速度
	LevelNormal                    // 正常速度
	LevelMiddle                    // 中等速度
	LevelSlow                      // 慢速度
	LevelSlowest                   // 最慢速度
	LevelStop                      // 停止
)

type Config struct {
	Replicas []uint64 // 副本集合（包含当前节点自己）
	Learners []uint64 // 学习节点集合
	Version  uint64   // 配置版本
}

func NewConfig() *Config {
	return &Config{}
}

func (c *Config) Marshal() ([]byte, error) {

	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(uint32(len(c.Replicas)))
	for _, id := range c.Replicas {
		enc.WriteUint64(id)
	}
	enc.WriteUint32(uint32(len(c.Learners)))
	for _, id := range c.Learners {
		enc.WriteUint64(id)
	}
	enc.WriteUint64(c.Version)
	return enc.Bytes(), nil
}

func (c *Config) Unmarshal(data []byte) error {

	dec := wkproto.NewDecoder(data)
	var err error
	var size uint32
	if size, err = dec.Uint32(); err != nil {
		return err
	}
	c.Replicas = make([]uint64, size)
	for i := 0; i < int(size); i++ {
		if c.Replicas[i], err = dec.Uint64(); err != nil {
			return err
		}
	}

	if size, err = dec.Uint32(); err != nil {
		return err
	}
	c.Learners = make([]uint64, size)
	for i := 0; i < int(size); i++ {
		if c.Learners[i], err = dec.Uint64(); err != nil {
			return err
		}
	}

	if c.Version, err = dec.Uint64(); err != nil {
		return err
	}
	return nil

}
