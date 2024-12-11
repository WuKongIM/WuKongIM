package replica

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/valyala/fastrand"
)

type MsgType uint16

const (
	MsgUnknown              MsgType = iota // 未知
	MsgInit                                // 初始化
	MsgInitResp                            // 初始化响应
	MsgLogConflictCheck                    // 日志冲突检查
	MsgLogConflictCheckResp                // 日志冲突检查响应
	MsgVoteReq                             // 请求投票
	MsgVoteResp                            // 请求投票响应
	MsgPropose                             // 提案（领导）
	MsgHup                                 // 开始选举
	// MsgNotifySync                 // 通知追随者同步日志（领导）
	// MsgNotifySyncAck              // 通知追随者同步日志回执（领导）
	MsgSyncReq                  // 同步日志 （追随者）
	MsgSyncGet                  // 日志获取（领导，本地）
	MsgSyncGetResp              // 日志获取响应（追随者）
	MsgSyncResp                 // 同步日志响应（领导）
	MsgSyncTimeout              // 同步超时
	MsgLeaderTermStartIndexReq  // 领导任期开始偏移量请求 （追随者）
	MsgLeaderTermStartIndexResp // 领导任期开始偏移量响应（领导）
	MsgStoreAppend              // 存储追加日志
	MsgStoreAppendResp          // 存储追加日志响应
	MsgApplyLogs                // 应用日志请求
	MsgApplyLogsResp            // 应用日志响应
	MsgBeat                     // 触发ping
	MsgPing                     // ping
	MsgPong                     // pong
	MsgConfigReq                // 从节点请求领导的集群配置
	MsgConfigResp               //从领导返回集群配置
	MsgConfigChange             // 配置变更
	MsgLearnerToFollower        // 学习者转成追随者
	MsgLearnerToLeader          // 学习者转成领导者
	MsgFollowerToLeader         // 追随者转成领导者
	MsgSpeedLevelSet            // 设置速度
	MsgSpeedLevelChange         // 速度变更
	MsgChangeRole               // 变更角色
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
	case MsgLearnerToFollower:
		return "MsgLearnerToFollower"
	case MsgLearnerToLeader:
		return "MsgLearnerToLeader"
	case MsgSpeedLevelSet:
		return "MsgSpeedLevelSet"
	case MsgSpeedLevelChange:
		return "MsgSpeedLevelChange"
	case MsgConfigChange:
		return "MsgConfigChange"
	case MsgInit:
		return "MsgInit"
	case MsgInitResp:
		return "MsgInitResp"
	case MsgLogConflictCheck:
		return "MsgLogConflictCheck"
	case MsgLogConflictCheckResp:
		return "MsgLogConflictCheckResp"
	case MsgSyncTimeout:
		return "MsgSyncTimeout"
	case MsgChangeRole:
		return "MsgChangeRole"
	case MsgFollowerToLeader:
		return "MsgFollowerToLeader"
	default:
		return fmt.Sprintf("MsgUnkown[%d]", m)
	}
}

const (
	None uint64 = 0
	All  uint64 = math.MaxUint64 - 1

	NoConflict uint64 = math.MaxUint64 - 1 // 没有冲突
)

type SpeedLevel uint8

const (
	LevelFast    SpeedLevel = iota // 最快速度
	LevelNormal                    // 正常速度
	LevelMiddle                    // 中等速度
	LevelSlow                      // 慢速度
	LevelSlowest                   // 最慢速度
	LevelStop                      // 停止
)

func (s SpeedLevel) String() string {
	switch s {
	case LevelFast:
		return "LevelFast"
	case LevelNormal:
		return "LevelNormal"
	case LevelMiddle:
		return "LevelMiddle"
	case LevelSlow:
		return "LevelSlow"
	case LevelSlowest:
		return "LevelSlowest"
	case LevelStop:
		return "LevelStop"
	default:
		return fmt.Sprintf("LevelUnkown[%d]", s)
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

	SpeedLevel  SpeedLevel
	Reject      bool   // 拒绝
	ConfVersion uint64 // 配置版本
	Logs        []Log

	// 不参与编码
	HandlerNo    string // handler唯一编号
	LearnerId    uint64
	FollowerId   uint64
	AppliedIndex uint64
	Role         Role // 节点角色

	Config      Config // 配置
	AppliedSize uint64
}

func (m Message) Size() int {
	size := 2 + 8 + 8 + 4 + 8 + 8 + 1 + 1 + 8 // msgType + from + to + term   + index + committedIndex + speedLevel +reject + confVersion
	for _, l := range m.Logs {
		size += 4 // log len
		size += l.LogSize()
	}
	return size

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
		binary.BigEndian.PutUint32(resultBytes[offset:], uint32(len(logData)))
		offset += 4
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
		logLen := binary.BigEndian.Uint32(data[offset : offset+4])
		offset += 4
		l := Log{}
		if err := l.Unmarshal(data[offset : offset+int(logLen)]); err != nil {
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

type Log struct {
	Id    uint64
	Index uint64 // 日志下标
	Term  uint32 // 领导任期
	Data  []byte // 日志数据

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

type LogSet []Log

func (l LogSet) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(uint32(len(l)))
	for _, log := range l {
		logData, err := log.Marshal()
		if err != nil {
			return nil, err
		}
		enc.WriteBytes(logData)
	}
	return enc.Bytes(), nil

}

func (l *LogSet) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	var size uint32
	if size, err = dec.Uint32(); err != nil {
		return err
	}
	if size == 0 {
		return nil
	}
	*l = make([]Log, size)

	logDatas, err := dec.BinaryAll()
	if err != nil {
		return err
	}

	offset := 0

	for i := 0; i < int(size); i++ {
		log := Log{}
		if err := log.Unmarshal(logDatas[offset:]); err != nil {
			return err
		}
		(*l)[i] = log
		offset += log.LogSize()
	}

	return nil

}

type HardState struct {
	LeaderId    uint64 // 领导ID
	Term        uint32 // 领导任期
	ConfVersion uint64
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

type Role uint8

const (
	RoleUnknown   Role = iota // 未知
	RoleFollower              // 追随者
	RoleCandidate             // 候选者
	RoleLeader                // 领导
	RoleLearner               // 学习者

)

func (r Role) String() string {
	switch r {
	case RoleUnknown:
		return "RoleUnknown"
	case RoleFollower:
		return "RoleFollower"
	case RoleCandidate:
		return "RoleCandidate"
	case RoleLeader:
		return "RoleLeader"
	case RoleLearner:
		return "RoleLearner"
	default:
		return fmt.Sprintf("RoleUnkown[%d]", r)
	}

}

func IsEmptyConfig(c Config) bool {
	return c.MigrateFrom == 0 && c.MigrateTo == 0 && c.Role == RoleUnknown && c.Term == 0 && c.Leader == 0 && c.Version == 0 && len(c.Replicas) == 0 && len(c.Learners) == 0
}

type Config struct {
	MigrateFrom uint64   // 迁移源节点
	MigrateTo   uint64   // 迁移目标节点
	Replicas    []uint64 // 副本集合（包含当前节点自己）
	Learners    []uint64 // 学习节点集合
	Role        Role     // 节点角色
	Term        uint32   // 领导任期
	Version     uint64   // 配置版本

	// 不参与编码
	Leader uint64 // 领导ID
}

func NewConfig() *Config {
	return &Config{}
}

func (c *Config) Marshal() ([]byte, error) {

	enc := wkproto.NewEncoder()
	defer enc.End()

	enc.WriteUint64(c.MigrateFrom)
	enc.WriteUint64(c.MigrateTo)

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

	if c.MigrateFrom, err = dec.Uint64(); err != nil {
		return err
	}

	if c.MigrateTo, err = dec.Uint64(); err != nil {
		return err
	}

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

func (c *Config) String() string {
	return fmt.Sprintf("Config{MigrateFrom:%d,MigrateTo:%d,Replicas:%v,Learners:%v,Role:%s,Term:%d,Version:%d,Leader:%d}", c.MigrateFrom, c.MigrateTo, c.Replicas, c.Learners, c.Role, c.Term, c.Version, c.Leader)

}

type Status int

const (
	StatusUnready         Status = iota // 未准备
	StatusLogCoflictCheck               // 日志冲突检查
	StatusReady                         // 准备就绪

)

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

type logEncodingSize uint64

func LogsSize(logs []Log) logEncodingSize {
	var size logEncodingSize
	for _, log := range logs {
		size += logEncodingSize(log.LogSize())
	}
	return size
}

func limitSize(logs []Log, maxSize logEncodingSize) ([]Log, bool) {
	if len(logs) == 0 {
		return logs, false
	}
	size := logs[0].LogSize()
	for limit := 1; limit < len(logs); limit++ {
		size += logs[limit].LogSize()
		if logEncodingSize(size) > maxSize {
			return logs[:limit], true
		}
	}
	return logs, false
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

// tickExponentialBackoff 函数，根据重试次数返回延迟时间
// retries 重试次数
// baseDelay 基础延迟时间
// maxDelay 最大延迟时间，返回的延迟时间不会超过这个值
func tickExponentialBackoff(retries int, baseDelay, maxDelay int) int {

	// 重试次数小于3，返回基础延迟时间
	if retries < 3 {
		return baseDelay
	}

	// 超过三次后按照指数级延迟 计算指数退避延迟时间
	exp := retries - 3 + 1
	delay := baseDelay * (1 << exp) // 2^exp * baseDelay

	// 可能添加一个随机因子，防止所有请求同时重试
	p := float64(fastrand.Uint32()) / (1 << 32)
	jitter := int(p * float64(delay)) // 抖动范围，抖动范围为0~delay
	delay = delay + jitter

	// 限制最大延迟时间
	if delay > maxDelay {
		delay = maxDelay
	}

	return delay
}

type SyncInfo struct {
	LastSyncIndex uint64 //最后一次来同步日志的下标（最新日志 + 1）
	SyncTick      int    // 同步计时器
}

type ReadyState struct {
	processing bool // 处理中
	willRetry  bool // 将要重试
	retryTick  int  // 重试计时，超过一定tick数后，将会重试
	retryCount int  // 连续重试次数

	retryIntervalTick int // 默认配置的重试间隔

	currentIntervalTick int // 重试间隔tick数，retryTick达到这个值后才重试
}

// NewReadyState retryIntervalTick为默认重试间隔
func NewReadyState(retryIntervalTick int) *ReadyState {

	return &ReadyState{
		retryIntervalTick:   retryIntervalTick,
		currentIntervalTick: retryIntervalTick,
	}
}

// 开始处理
func (r *ReadyState) StartProcessing() {
	r.processing = true
}

// 处理成功
func (r *ReadyState) ProcessSuccess() {
	r.processing = false
	r.willRetry = false
	r.retryCount = 0
}

// 处理失败
func (r *ReadyState) ProcessFail() {
	r.willRetry = true
	r.retryTick = 0
	r.retryCount++

	// 重试间隔指数退避
	r.currentIntervalTick = tickExponentialBackoff(r.retryCount, r.retryIntervalTick, r.retryIntervalTick*10)
}

// 是处理中
func (r *ReadyState) IsProcessing() bool {

	return r.processing
}

// 是否在重试
func (r *ReadyState) IsRetry() bool {
	return r.willRetry
}

// tick
func (r *ReadyState) Tick() {
	if r.willRetry {
		r.retryTick++
		if r.retryTick >= r.currentIntervalTick {
			r.willRetry = false
			r.retryTick = 0
			r.processing = false
		}
	}
}

func (r *ReadyState) Reset() {
	r.processing = false
	r.willRetry = false
	r.retryTick = 0
	r.retryCount = 0
	r.currentIntervalTick = r.retryIntervalTick
}

type ReadyTimeoutState struct {
	processing              bool // 处理中
	idleTick                int  // 空闲tick数量,当发起请求后，空闲指定tick数后没有收到回应则重新发起
	timeoutIntervalTick     int  // 超时tick数,当idleTick达到这个数后重新发起
	nextTimeoutIntervalTick int  // 下次超时间隔
	timeoutCount            int  // 超时次数
	timeoutTickCount        int  // 超时tick计数

	timeoutCallback func() // 超时回调

	intervalTick int // 请求间隔tick数

	wklog.Log
}

func NewReadyTimeoutState(logPrefix string, timeoutIntervalTick int, intervalTick int, timeoutCallback func()) *ReadyTimeoutState {

	rt := &ReadyTimeoutState{
		timeoutIntervalTick:     timeoutIntervalTick,
		nextTimeoutIntervalTick: timeoutIntervalTick,
		timeoutCallback:         timeoutCallback,
		intervalTick:            intervalTick,
		Log:                     wklog.NewWKLog(logPrefix),
	}

	return rt
}

// 开始处理
func (r *ReadyTimeoutState) StartProcessing() {
	r.processing = true
	r.idleTick = 0
	r.timeoutTickCount = 0
	r.nextTimeoutIntervalTick = r.timeoutIntervalTick
}

// 处理成功
func (r *ReadyTimeoutState) ProcessSuccess() {
	r.processing = false
	r.idleTick = 0
	r.timeoutCount = 0
	r.timeoutTickCount = 0
	r.nextTimeoutIntervalTick = r.timeoutIntervalTick
}

// 处理失败
func (r *ReadyTimeoutState) ProcessFail() {
	r.processing = false
	r.idleTick = 0
	r.timeoutTickCount = 0
	r.Delay()
}

// 立马重试
func (r *ReadyTimeoutState) Immediately() {
	r.idleTick = r.intervalTick
}

// 延迟
func (r *ReadyTimeoutState) Delay() {
	r.idleTick = 0
}

// 是处理中
func (r *ReadyTimeoutState) IsProcessing() bool {

	return r.processing
}

func (r *ReadyTimeoutState) Allow() bool {
	return r.idleTick >= r.intervalTick
}

func (r *ReadyTimeoutState) Tick() {

	// 超时逻辑
	if r.processing {
		r.timeoutTickCount++
		if r.timeoutTickCount > r.nextTimeoutIntervalTick {

			r.timeoutTickCount = 0
			r.processing = false
			r.timeoutCount++
			// r.nextTimeoutIntervalTick = tickExponentialBackoff(r.timeoutCount, r.timeoutIntervalTick, r.timeoutIntervalTick*4)
			if r.timeoutCallback != nil {
				r.timeoutCallback()
			}
		}
	} else {
		r.idleTick++
	}
}

func (r *ReadyTimeoutState) Reset() {
	r.idleTick = 0
	r.nextTimeoutIntervalTick = r.timeoutIntervalTick
	r.timeoutCount = 0
	r.timeoutTickCount = 0
	r.processing = false
}

func (r *ReadyTimeoutState) SetIntervalTick(intervalTick int) {
	r.intervalTick = intervalTick
}
