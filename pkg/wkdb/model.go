package wkdb

import (
	"fmt"
	"strconv"
	"strings"
	"time"

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
	Term    uint64 // raft term
	version uint8  // 数据协议版本
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

	fixVersion := uint8(100) // 借助一个固定的版本号，用于兼容老版本编码

	var newEncode = false // 是否是新版本编码
	if version > fixVersion {
		version = version - fixVersion
		newEncode = true
	}

	var recvPacketData []byte
	if newEncode {

		// 数据版本，暂时为默认的0，后续数据协议升级时，可以根据此版本号进行兼容处理
		if _, err := dec.Uint8(); err != nil {
			return err
		}
		recvPacketDataLen, err := dec.Uint32()
		if err != nil {
			return err
		}
		recvPacketData, err = dec.Bytes(int(recvPacketDataLen))
		if err != nil {
			return err
		}
	} else {
		recvPacketData, err = dec.Binary()
		if err != nil {
			return err
		}
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

	// TODO: 这里wkproto.LatestVersion + fixVersion是为了兼容老版本编码，如果第一个版本号大于100，那么就是新版本编码
	// 所以这里wkproto.LatestVersion不能大于156，否则uint8会溢出，实际中wkproto.LatestVersion应该不会太大
	fixVersion := uint8(100)

	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint8(wkproto.LatestVersion + fixVersion)
	enc.WriteUint8(m.version)
	enc.WriteUint32(uint32(len(data)))
	enc.WriteBytes(data)
	enc.WriteUint64(m.Term)
	return enc.Bytes(), nil
}

var EmptyDevice = Device{}

func IsEmptyDevice(d Device) bool {
	return d.Uid == ""
}

type Device struct {
	Id           uint64     `json:"id,omitempty"`
	Uid          string     `json:"uid,omitempty"`            // 用户唯一uid
	Token        string     `json:"token,omitempty"`          // 设备token
	DeviceFlag   uint64     `json:"device_flag,omitempty"`    // 设备标记 (TODO: 这里deviceFlag弄成uint64是为了以后扩展)
	DeviceLevel  uint8      `json:"device_level,omitempty"`   // 设备等级
	ConnCount    uint32     `json:"conn_count,omitempty"`     // 连接数量
	SendMsgCount uint64     `json:"send_msg_count,omitempty"` // 发送消息数量
	RecvMsgCount uint64     `json:"recv_msg_count,omitempty"` // 接收消息数量
	SendMsgBytes uint64     `json:"send_msg_bytes,omitempty"` // 发送消息字节数
	RecvMsgBytes uint64     `json:"recv_msg_bytes,omitempty"` // 接收消息字节数
	CreatedAt    *time.Time `json:"created_at,omitempty"`     // 创建时间
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`     // 更新时间
}

var EmptyUser = User{}

func IsEmptyUser(u User) bool {
	return u.Uid == ""
}

type User struct {
	Id                uint64     `json:"id,omitempty"`
	Uid               string     `json:"uid,omitempty"`                 // 用户uid
	DeviceCount       uint32     `json:"device_count,omitempty"`        // 设备数量
	OnlineDeviceCount uint32     `json:"online_device_count,omitempty"` // 在线设备数量
	ConnCount         uint32     `json:"conn_count,omitempty"`          // 连接数量
	SendMsgCount      uint64     `json:"send_msg_count,omitempty"`      // 发送消息数量
	RecvMsgCount      uint64     `json:"recv_msg_count,omitempty"`      // 接收消息数量
	SendMsgBytes      uint64     `json:"send_msg_bytes,omitempty"`      // 发送消息字节数
	RecvMsgBytes      uint64     `json:"recv_msg_bytes,omitempty"`      // 接收消息字节数
	PluginNo          string     `json:"plugin_no,omitempty"`           // 插件编号
	CreatedAt         *time.Time `json:"created_at,omitempty"`          // 创建时间
	UpdatedAt         *time.Time `json:"updated_at,omitempty"`          // 更新时间
}

var EmptyChannelInfo = ChannelInfo{}

type ChannelInfo struct {
	Id              uint64 `json:"id,omitempty"`               // ID
	ChannelId       string `json:"channel_id,omitempty"`       // 频道ID
	ChannelType     uint8  `json:"channel_type,omitempty"`     // 频道类型
	Ban             bool   `json:"ban,omitempty"`              // 是否被封
	Large           bool   `json:"large,omitempty"`            // 是否是超大群
	Disband         bool   `json:"disband,omitempty"`          // 是否解散
	SubscriberCount int    `json:"subscriber_count,omitempty"` // 订阅者数量
	DenylistCount   int    `json:"denylist_count,omitempty"`   // 黑名单数量
	AllowlistCount  int    `json:"allowlist_count,omitempty"`  // 白名单数量
	LastMsgSeq      uint64 `json:"last_msg_seq,omitempty"`     // 最新消息序号
	LastMsgTime     uint64 `json:"last_msg_time,omitempty"`    // 最后一次消息时间
	Webhook         string `json:"webhook,omitempty"`          // webhook地址
	// 是否禁止发送消息 （0.不禁止 1.禁止），禁止后，频道内所有成员都不能发送消息,个人频道能收消息，但不能发消息
	SendBan bool `json:"send_ban,omitempty"`
	// 是否允许陌生人发送消息（0.不允许 1.允许）（此配置目前只支持个人频道）
	// 个人频道：如果AllowStranger为1，则陌生人可以给当前用户发消息
	AllowStranger bool       `json:"allow_stranger,omitempty"`
	CreatedAt     *time.Time `json:"created_at,omitempty"` // 创建时间
	UpdatedAt     *time.Time `json:"updated_at,omitempty"` // 更新时间
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

// func (c *ChannelInfo) Unmarshal(data []byte) error {
// 	dec := wkproto.NewDecoder(data)

// 	var err error
// 	if c.ChannelId, err = dec.String(); err != nil {
// 		return err
// 	}
// 	if c.ChannelType, err = dec.Uint8(); err != nil {
// 		return err
// 	}
// 	var ban uint8
// 	if ban, err = dec.Uint8(); err != nil {
// 		return err
// 	}
// 	var large uint8
// 	if large, err = dec.Uint8(); err != nil {
// 		return err
// 	}
// 	var disband uint8
// 	if disband, err = dec.Uint8(); err != nil {
// 		return err
// 	}

// 	c.Ban = wkutil.Uint8ToBool(ban)
// 	c.Large = wkutil.Uint8ToBool(large)
// 	c.Disband = wkutil.Uint8ToBool(disband)

// 	var createdAt uint64
// 	if createdAt, err = dec.Uint64(); err != nil {
// 		return err
// 	}
// 	if createdAt > 0 {
// 		ct := time.Unix(int64(createdAt/1e9), int64(createdAt%1e9))
// 		c.CreatedAt = &ct
// 	}
// 	var updatedAt uint64
// 	if updatedAt, err = dec.Uint64(); err != nil {
// 		return err
// 	}
// 	if updatedAt > 0 {
// 		ct := time.Unix(int64(updatedAt/1e9), int64(updatedAt%1e9))
// 		c.UpdatedAt = &ct
// 	}

// 	return nil
// }

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
	Id              uint64           `json:"id,omitempty"`
	Uid             string           `json:"uid,omitempty"`                // 用户uid
	Type            ConversationType `json:"type,omitempty"`               // 会话类型
	ChannelId       string           `json:"channel_id,omitempty"`         // 频道id
	ChannelType     uint8            `json:"channel_type,omitempty"`       // 频道类型
	UnreadCount     uint32           `json:"unread_count,omitempty"`       // 未读消息数量（这个可以用户自己设置）
	ReadToMsgSeq    uint64           `json:"readed_to_msg_seq,omitempty"`  // 已经读至的消息序号
	DeletedAtMsgSeq uint64           `json:"deleted_at_msg_seq,omitempty"` // 最近会话在记录的这个msgSeq位置被删除（删除消息包含这个msgSeq）
	CreatedAt       *time.Time       `json:"created_at,omitempty"`         // 创建时间
	UpdatedAt       *time.Time       `json:"updated_at,omitempty"`         // 更新时间
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
	enc.WriteUint64(c.ReadToMsgSeq)

	if c.CreatedAt != nil {
		enc.WriteUint64(uint64(c.CreatedAt.UnixNano()))
	} else {
		enc.WriteUint64(0)
	}

	if c.UpdatedAt != nil {
		enc.WriteUint64(uint64(c.UpdatedAt.UnixNano()))
	} else {
		enc.WriteUint64(0)
	}

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
	if c.ReadToMsgSeq, err = dec.Uint64(); err != nil {
		return err
	}

	var createdAt uint64
	if createdAt, err = dec.Uint64(); err != nil {
		return err
	}
	if createdAt > 0 {
		ct := time.Unix(int64(createdAt/1e9), int64(createdAt%1e9))
		c.CreatedAt = &ct
	}

	var updatedAt uint64
	if updatedAt, err = dec.Uint64(); err != nil {
		return err
	}
	if updatedAt > 0 {
		ct := time.Unix(int64(updatedAt/1e9), int64(updatedAt%1e9))
		c.UpdatedAt = &ct
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

func (c *ConversationSet) Unmarshal(data []byte) error {
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
		*c = append(*c, v)
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
	Id              uint64               `json:"id,omitempty"`                // ID
	ChannelId       string               `json:"channel_id,omitempty"`        // 频道ID
	ChannelType     uint8                `json:"channel_type,omitempty"`      // 频道类型
	ReplicaMaxCount uint16               `json:"replica_max_count,omitempty"` // 副本最大数量
	Replicas        []uint64             `json:"replicas,omitempty"`          // 副本节点ID集合
	Learners        []uint64             `json:"learners,omitempty"`          // 学习者节点ID集合
	LeaderId        uint64               `json:"learder_id,omitempty"`        // 领导者ID
	Term            uint32               `json:"term,omitempty"`              // 任期
	MigrateFrom     uint64               `json:"migrate_from,omitempty"`      // 迁移源
	MigrateTo       uint64               `json:"migrate_to,omitempty"`        // 迁移目标
	Status          ChannelClusterStatus `json:"status,omitempty"`            // 状态
	ConfVersion     uint64               `json:"conf_version,omitempty"`      // 配置文件版本号
	CreatedAt       *time.Time           `json:"created_at,omitempty"`        // 创建时间
	UpdatedAt       *time.Time           `json:"updated_at,omitempty"`        // 更新时间

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
		CreatedAt:       c.CreatedAt,
		UpdatedAt:       c.UpdatedAt,
	}
}

func (c *ChannelClusterConfig) Equal(cfg ChannelClusterConfig) bool {
	if c.Id != cfg.Id {
		return false
	}
	if c.ChannelId != cfg.ChannelId {
		return false
	}
	if c.ChannelType != cfg.ChannelType {
		return false
	}
	if c.ReplicaMaxCount != cfg.ReplicaMaxCount {
		return false
	}
	if len(c.Replicas) != len(cfg.Replicas) {
		return false
	}
	for i, replica := range c.Replicas {
		if replica != cfg.Replicas[i] {
			return false
		}
	}
	if len(c.Learners) != len(cfg.Learners) {
		return false
	}
	for i, learner := range c.Learners {
		if learner != cfg.Learners[i] {
			return false
		}
	}
	if c.LeaderId != cfg.LeaderId {
		return false
	}
	if c.Term != cfg.Term {
		return false
	}
	if c.MigrateFrom != cfg.MigrateFrom {
		return false
	}
	if c.MigrateTo != cfg.MigrateTo {
		return false
	}
	if c.Status != cfg.Status {
		return false
	}
	if c.ConfVersion != cfg.ConfVersion {
		return false
	}
	return true
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
	if c.CreatedAt != nil {
		enc.WriteUint64(uint64(c.CreatedAt.UnixNano()))
	} else {
		enc.WriteUint64(0)
	}
	if c.UpdatedAt != nil {
		enc.WriteUint64(uint64(c.UpdatedAt.UnixNano()))
	} else {
		enc.WriteUint64(0)
	}
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

	var createdAt uint64
	if createdAt, err = dec.Uint64(); err != nil {
		return err
	}
	if createdAt > 0 {
		ct := time.Unix(int64(createdAt/1e9), int64(createdAt%1e9))
		c.CreatedAt = &ct
	}
	var updatedAt uint64
	if updatedAt, err = dec.Uint64(); err != nil {
		return err
	}
	if updatedAt > 0 {
		ct := time.Unix(int64(updatedAt/1e9), int64(updatedAt%1e9))
		c.UpdatedAt = &ct
	}

	return nil
}

func (c *ChannelClusterConfig) String() string {
	return fmt.Sprintf("ChannelId: %s, ChannelType: %d, ReplicaMaxCount: %d, Replicas: %v, Learners: %v MigrateFrom: %d MigrateTo: %d LeaderId: %d, Term: %d",
		c.ChannelId, c.ChannelType, c.ReplicaMaxCount, c.Replicas, c.Learners, c.MigrateFrom, c.MigrateTo, c.LeaderId, c.Term)
}

// 批量更新会话
type BatchUpdateConversationModel struct {
	Uids        map[string]uint64 `json:"uids,omitempty"` // 用户uid和对应的已读消息的messageSeq
	ChannelId   string            `json:"channel_id,omitempty"`
	ChannelType uint8             `json:"channel_type,omitempty"`
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
	for uid := range u.Uids {
		size = size + 2 + len(uid) + 8 // string len + uid + messageSeq
	}
	size = size + 2 + len(u.ChannelId) + 1 // string len + channel id + channel type
	return size
}

type SetChannelLastMessageSeqReq struct {
	ChannelId   string `json:"channel_id,omitempty"`
	ChannelType uint8  `json:"channel_type,omitempty"`
	Seq         uint64 `json:"seq,omitempty"`
}

type AppendMessagesReq struct {
	ChannelId   string    `json:"channel_id,omitempty"`
	ChannelType uint8     `json:"channel_type,omitempty"`
	Messages    []Message `json:"messages,omitempty"`
}

func ChannelToKey(channelId string, channelType uint8) string {
	var b strings.Builder
	b.WriteString(channelId)
	b.WriteString("-")
	b.WriteString(strconv.FormatInt(int64(channelType), 10))
	return b.String()
}

type Channel struct {
	ChannelId   string `json:"channel_id,omitempty"`
	ChannelType uint8  `json:"channel_type,omitempty"`
}

type Member struct {
	Id        uint64     `json:"id"`
	Uid       string     `json:"uid"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`

	version uint16 // 数据版本
}

func (m *Member) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()

	enc.WriteUint16(m.version) // 数据版本

	enc.WriteUint64(m.Id)
	enc.WriteString(m.Uid)
	if m.CreatedAt != nil {
		enc.WriteUint64(uint64(m.CreatedAt.UnixNano()))
	} else {
		enc.WriteUint64(0)
	}
	if m.UpdatedAt != nil {
		enc.WriteUint64(uint64(m.UpdatedAt.UnixNano()))
	} else {
		enc.WriteUint64(0)
	}
	return enc.Bytes(), nil
}

func (m *Member) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error

	if m.version, err = dec.Uint16(); err != nil {
		return err
	}

	if m.Id, err = dec.Uint64(); err != nil {
		return err
	}
	if m.Uid, err = dec.String(); err != nil {
		return err
	}
	var createdAt uint64
	if createdAt, err = dec.Uint64(); err != nil {
		return err
	}
	if createdAt > 0 {
		ct := time.Unix(int64(createdAt/1e9), int64(createdAt%1e9))
		m.CreatedAt = &ct
	}
	var updatedAt uint64
	if updatedAt, err = dec.Uint64(); err != nil {
		return err
	}
	if updatedAt > 0 {
		ct := time.Unix(int64(updatedAt/1e9), int64(updatedAt%1e9))
		m.UpdatedAt = &ct
	}
	return nil
}

type Tester struct {
	Id        uint64
	No        string
	Addr      string
	CreatedAt *time.Time
	UpdatedAt *time.Time
}

func (t *Tester) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint64(t.Id)
	enc.WriteString(t.No)
	enc.WriteString(t.Addr)
	return enc.Bytes(), nil
}

func (t *Tester) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if t.Id, err = dec.Uint64(); err != nil {
		return err
	}
	if t.No, err = dec.String(); err != nil {
		return err
	}
	if t.Addr, err = dec.String(); err != nil {
		return err
	}
	return nil
}

// 插件状态
type PluginStatus uint32

const (
	// PluginStatusUnset 未设置
	PluginStatusUnset PluginStatus = iota
	// PluginStatusEnable 启用
	PluginStatusEnable
	// PluginStatusDisabled 禁用
	PluginStatusDisabled
)

type Plugin struct {
	No             string
	Name           string
	ConfigTemplate []byte
	CreatedAt      *time.Time
	UpdatedAt      *time.Time
	Status         PluginStatus
	Version        string
	Methods        []string
	Priority       uint32
	Config         map[string]interface{}
}

func IsEmptyPlugin(p Plugin) bool {
	return p.No == ""
}

type PluginUser struct {
	Uid       string
	PluginNo  string
	CreatedAt *time.Time
	UpdatedAt *time.Time
}

type SearchPluginUserReq struct {
	PluginNo string
	Uid      string
}
