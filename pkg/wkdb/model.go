package wkdb

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

var (
	proto = wkproto.New()
)

var EmptyMessage = Message{}

func IsEmptyMessage(m Message) bool {
	return m.MessageID == 0 && m.MessageSeq == 0
}

type Message struct {
	wkproto.RecvPacket
	Term uint64 // raft term
}

func (m *Message) Unmarshal(data []byte) error {

	dec := wkproto.NewDecoder(data)
	var (
		version uint8
		err     error
	)
	if version, err = dec.Uint8(); err != nil {
		return err
	}

	recvPacketData, err := dec.Binary()
	if err != nil {
		return err
	}

	f, _, err := proto.DecodeFrame(recvPacketData, version)
	if err != nil {
		return err
	}
	rcv := f.(*wkproto.RecvPacket)
	m.RecvPacket = *rcv
	if m.Term, err = dec.Uint64(); err != nil {
		return err
	}

	return nil
}

func (m *Message) Marshal() ([]byte, error) {
	data, err := proto.EncodeFrame(&m.RecvPacket, wkproto.LatestVersion)
	if err != nil {
		return nil, err
	}

	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint8(wkproto.LatestVersion)
	enc.WriteBinary(data)
	enc.WriteUint64(m.Term)
	return enc.Bytes(), nil
}

var EmptyDevice = Device{}

type Device struct {
	Id           uint64
	Uid          string    // 用户唯一uid
	Token        string    // 设备token
	DeviceFlag   uint64    // 设备标记 (TODO: 这里deviceFlag弄成uint64是为了以后扩展)
	DeviceLevel  uint8     // 设备等级
	ConnCount    uint32    // 连接数量
	SendMsgCount uint64    // 发送消息数量
	RecvMsgCount uint64    // 接收消息数量
	SendMsgBytes uint64    // 发送消息字节数
	RecvMsgBytes uint64    // 接收消息字节数
	CreatedAt    time.Time // 创建时间
	UpdatedAt    time.Time // 更新时间
}

var EmptyUser = User{}

type User struct {
	Id                uint64
	Uid               string    // 用户uid
	DeviceCount       uint32    // 设备数量
	OnlineDeviceCount uint32    // 在线设备数量
	ConnCount         uint32    // 连接数量
	SendMsgCount      uint64    // 发送消息数量
	RecvMsgCount      uint64    // 接收消息数量
	SendMsgBytes      uint64    // 发送消息字节数
	RecvMsgBytes      uint64    // 接收消息字节数
	CreatedAt         time.Time // 创建时间
	UpdatedAt         time.Time // 更新时间
}

var EmptyChannelInfo = ChannelInfo{}

type ChannelInfo struct {
	Id              uint64 // ID
	ChannelId       string // 频道ID
	ChannelType     uint8  // 频道类型
	Ban             bool   // 是否被封
	Large           bool   // 是否是超大群
	Disband         bool   // 是否解散
	SubscriberCount int    // 订阅者数量
	DenylistCount   int    // 黑名单数量
	AllowlistCount  int    // 白名单数量
	LastMsgSeq      uint64 // 最新消息序号
	LastMsgTime     uint64 // 最后一次消息时间
}

func NewChannelInfo(channelId string, channelType uint8) ChannelInfo {
	return ChannelInfo{
		ChannelId:   channelId,
		ChannelType: channelType,
	}

}

func IsEmptyChannelInfo(c ChannelInfo) bool {
	return strings.TrimSpace(c.ChannelId) == ""
}

func (c *ChannelInfo) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(c.ChannelId)
	enc.WriteUint8(c.ChannelType)
	enc.WriteUint8(wkutil.BoolToUint8(c.Ban))
	enc.WriteUint8(wkutil.BoolToUint8(c.Large))
	enc.WriteUint8(wkutil.BoolToUint8(c.Disband))
	return enc.Bytes(), nil
}

func (c *ChannelInfo) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.ChannelId, err = dec.String(); err != nil {
		return err
	}
	if c.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}
	var ban uint8
	if ban, err = dec.Uint8(); err != nil {
		return err
	}
	var large uint8
	if large, err = dec.Uint8(); err != nil {
		return err
	}
	var disband uint8
	if disband, err = dec.Uint8(); err != nil {
		return err
	}

	c.Ban = wkutil.Uint8ToBool(ban)
	c.Large = wkutil.Uint8ToBool(large)

	c.Disband = wkutil.Uint8ToBool(disband)
	return nil
}

var EmptyConversation = Conversation{}

func IsEmptyConversation(c Conversation) bool {
	return c.ChannelId == ""
}

var EmptySession = Session{}

func IsEmptySession(s Session) bool {
	return s.ChannelId == ""
}

// 会话类型
type SessionType uint8

const (
	// SessionTypeChat 聊天
	SessionTypeChat SessionType = iota

	// SessionTypeCMD 指令
	SessionTypeCMD
)

type Session struct {
	Id          uint64
	SessionType SessionType
	Uid         string
	ChannelId   string
	ChannelType uint8
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (s *Session) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(s.Id)
	enc.WriteUint8(uint8(s.SessionType))
	enc.WriteString(s.Uid)
	enc.WriteString(s.ChannelId)
	enc.WriteUint8(s.ChannelType)
	enc.WriteInt64(s.CreatedAt.Unix())
	enc.WriteInt64(s.UpdatedAt.Unix())
	return enc.Bytes(), nil
}

func (s *Session) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if s.Id, err = dec.Uint64(); err != nil {
		return err
	}

	var sessionType uint8
	if sessionType, err = dec.Uint8(); err != nil {
		return err
	}
	s.SessionType = SessionType(sessionType)

	if s.Uid, err = dec.String(); err != nil {
		return err
	}
	if s.ChannelId, err = dec.String(); err != nil {
		return err
	}
	if s.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}
	var createdAt int64
	if createdAt, err = dec.Int64(); err != nil {
		return err
	}
	s.CreatedAt = time.Unix(createdAt, 0)
	var updatedAt int64
	if updatedAt, err = dec.Int64(); err != nil {
		return err
	}
	s.UpdatedAt = time.Unix(updatedAt, 0)
	return nil

}

type ConversationType uint8

const (
	// ConversationTypeChat 聊天
	ConversationTypeChat ConversationType = iota

	// ConversationTypeCMD 指令
	ConversationTypeCMD
)

// Conversation Conversation
type Conversation struct {
	Id             uint64
	Uid            string           // 用户uid
	Type           ConversationType // 会话类型
	ChannelId      string           // 频道id
	ChannelType    uint8            // 频道类型
	UnreadCount    uint32           // 未读消息数量（这个可以用户自己设置）
	ReadedToMsgSeq uint64           // 已经读至的消息序号

	CreatedAt time.Time // 创建时间
	UpdatedAt time.Time // 更新时间
}

func (c *Conversation) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(c.Id)
	enc.WriteString(c.Uid)
	enc.WriteUint8(uint8(c.Type))
	enc.WriteString(c.ChannelId)
	enc.WriteUint8(c.ChannelType)
	enc.WriteUint32(c.UnreadCount)
	enc.WriteUint64(c.ReadedToMsgSeq)

	return enc.Bytes(), nil
}

func (c *Conversation) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.Id, err = dec.Uint64(); err != nil {
		return err
	}

	if c.Uid, err = dec.String(); err != nil {
		return err
	}

	var t uint8
	if t, err = dec.Uint8(); err != nil {
		return err
	}
	c.Type = ConversationType(t)

	if c.ChannelId, err = dec.String(); err != nil {
		return err
	}

	if c.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}

	if c.UnreadCount, err = dec.Uint32(); err != nil {
		return err
	}
	if c.ReadedToMsgSeq, err = dec.Uint64(); err != nil {
		return err
	}

	return nil
}

type ConversationSet []Conversation

func (c ConversationSet) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint16(uint16(len(c)))
	for _, v := range c {
		data, err := v.Marshal()
		if err != nil {
			return nil, err
		}
		enc.WriteBinary(data)
	}
	return enc.Bytes(), nil
}

func (c ConversationSet) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	var size uint16
	if size, err = dec.Uint16(); err != nil {
		return err
	}

	for i := 0; i < int(size); i++ {
		v := Conversation{}
		data, err := dec.Binary()
		if err != nil {
			return err
		}
		if err = v.Unmarshal(data); err != nil {
			return err
		}
		c = append(c, v)
	}
	return nil
}

var EmptyChannelClusterConfig = ChannelClusterConfig{}

func IsEmptyChannelClusterConfig(cfg ChannelClusterConfig) bool {
	return strings.TrimSpace(cfg.ChannelId) == ""
}

type ChannelClusterStatus uint8

const (
	ChannelClusterStatusNormal    ChannelClusterStatus = iota // 正常
	ChannelClusterStatusCandidate                             // 选举中
	// ChannelClusterStatusLeaderTransfer                             // 领导者转移
)

// 频道分布式配置
type ChannelClusterConfig struct {
	Id              uint64               // ID
	ChannelId       string               // 频道ID
	ChannelType     uint8                // 频道类型
	ReplicaMaxCount uint16               // 副本最大数量
	Replicas        []uint64             // 副本节点ID集合
	Learners        []uint64             // 学习者节点ID集合
	LeaderId        uint64               // 领导者ID
	Term            uint32               // 任期
	MigrateFrom     uint64               // 迁移源
	MigrateTo       uint64               // 迁移目标
	Status          ChannelClusterStatus // 状态
	ConfVersion     uint64               // 配置文件版本号

	version uint16 // 数据协议版本
}

func (c *ChannelClusterConfig) Clone() ChannelClusterConfig {
	return ChannelClusterConfig{
		Id:              c.Id,
		ChannelId:       c.ChannelId,
		ChannelType:     c.ChannelType,
		ReplicaMaxCount: c.ReplicaMaxCount,
		Replicas:        c.Replicas,
		Learners:        c.Learners,
		LeaderId:        c.LeaderId,
		Term:            c.Term,
		MigrateFrom:     c.MigrateFrom,
		MigrateTo:       c.MigrateTo,
		Status:          c.Status,
		ConfVersion:     c.ConfVersion,
		version:         c.version,
	}
}

func (c *ChannelClusterConfig) Marshal() ([]byte, error) {
	c.version = 1
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint16(c.version)
	enc.WriteString(c.ChannelId)
	enc.WriteUint8(c.ChannelType)
	enc.WriteUint16(c.ReplicaMaxCount)
	enc.WriteUint16(uint16(len(c.Replicas)))
	if len(c.Replicas) > 0 {
		for _, replica := range c.Replicas {
			enc.WriteUint64(replica)
		}
	}
	enc.WriteUint16(uint16(len(c.Learners)))
	if len(c.Learners) > 0 {
		for _, learner := range c.Learners {
			enc.WriteUint64(learner)
		}
	}
	enc.WriteUint64(c.LeaderId)
	enc.WriteUint32(c.Term)
	enc.WriteUint64(c.MigrateFrom)
	enc.WriteUint64(c.MigrateTo)
	enc.WriteUint8(uint8(c.Status))
	enc.WriteUint64(c.ConfVersion)
	return enc.Bytes(), nil
}

func (c *ChannelClusterConfig) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.version, err = dec.Uint16(); err != nil {
		return err
	}
	if c.ChannelId, err = dec.String(); err != nil {
		return err
	}
	if c.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}
	if c.ReplicaMaxCount, err = dec.Uint16(); err != nil {
		return err
	}
	var replicasLen uint16
	if replicasLen, err = dec.Uint16(); err != nil {
		return err
	}
	if replicasLen > 0 {
		c.Replicas = make([]uint64, replicasLen)
		for i := uint16(0); i < replicasLen; i++ {
			if c.Replicas[i], err = dec.Uint64(); err != nil {
				return err
			}
		}
	}

	var learnersLen uint16
	if learnersLen, err = dec.Uint16(); err != nil {
		return err
	}

	if learnersLen > 0 {
		c.Learners = make([]uint64, learnersLen)
		for i := uint16(0); i < learnersLen; i++ {
			if c.Learners[i], err = dec.Uint64(); err != nil {
				return err
			}
		}
	}

	if c.LeaderId, err = dec.Uint64(); err != nil {
		return err
	}
	if c.Term, err = dec.Uint32(); err != nil {
		return err
	}
	if c.MigrateFrom, err = dec.Uint64(); err != nil {
		return err
	}

	if c.MigrateTo, err = dec.Uint64(); err != nil {
		return err
	}

	var status uint8
	if status, err = dec.Uint8(); err != nil {
		return err
	}
	c.Status = ChannelClusterStatus(status)

	if c.ConfVersion, err = dec.Uint64(); err != nil {
		return err
	}

	return nil
}

func (c *ChannelClusterConfig) String() string {
	return fmt.Sprintf("ChannelId: %s, ChannelType: %d, ReplicaMaxCount: %d, Replicas: %v, Learners: %v LeaderId: %d, Term: %d",
		c.ChannelId, c.ChannelType, c.ReplicaMaxCount, c.Replicas, c.Learners, c.LeaderId, c.Term)
}

// 批量更新会话
type BatchUpdateConversationModel struct {
	Uids        map[string]uint64 // 用户uid和对应的已读消息的messageSeq
	ChannelId   string
	ChannelType uint8
}

func (u *BatchUpdateConversationModel) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint16(uint16(len(u.Uids)))
	for uid, messageSeq := range u.Uids {
		enc.WriteString(uid)
		enc.WriteUint64(messageSeq)
	}
	enc.WriteString(u.ChannelId)
	enc.WriteUint8(u.ChannelType)
	return enc.Bytes(), nil
}

func (u *BatchUpdateConversationModel) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	var size uint16
	if size, err = dec.Uint16(); err != nil {
		return err
	}
	for i := 0; i < int(size); i++ {
		uid, err := dec.String()
		if err != nil {
			return err
		}
		messageSeq, err := dec.Uint64()
		if err != nil {
			return err
		}
		u.Uids[uid] = messageSeq
	}
	if u.ChannelId, err = dec.String(); err != nil {
		return err
	}
	if u.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}
	return nil
}

func (u *BatchUpdateConversationModel) Size() int {
	size := 2 // uid len
	for uid, _ := range u.Uids {
		size = size + 2 + len(uid) + 8 // string len + uid + messageSeq
	}
	size = size + 2 + len(u.ChannelId) + 1 // string len + channel id + channel type
	return size
}

type SetChannelLastMessageSeqReq struct {
	ChannelId   string
	ChannelType uint8
	Seq         uint64
}

type AppendMessagesReq struct {
	ChannelId   string
	ChannelType uint8
	Messages    []Message
}

func ChannelToKey(channelId string, channelType uint8) string {
	var b strings.Builder
	b.WriteString(channelId)
	b.WriteString("-")
	b.WriteString(strconv.FormatInt(int64(channelType), 10))
	return b.String()
}

func channelFromKey(key string) (channelId string, channelType uint8) {
	idx := strings.Index(key, "-")
	if idx == -1 {
		return
	}
	channelId = key[:idx]
	channelTypeI64, _ := strconv.ParseUint(key[idx+1:], 10, 8)
	channelType = uint8(channelTypeI64)
	return
}

type Channel struct {
	ChannelId   string
	ChannelType uint8
}
