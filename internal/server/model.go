package server

import (
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type everyScheduler struct {
	Interval time.Duration
}

func (s *everyScheduler) Next(prev time.Time) time.Time {
	return prev.Add(s.Interval)
}

type Message struct {
	*wkproto.RecvPacket
	ToUID          string             // 接受者
	Subscribers    []string           // 订阅者 如果此字段有值 则表示消息只发送给指定的订阅者
	fromDeviceFlag wkproto.DeviceFlag // 发送者设备标示
	fromDeviceID   string             // 发送者设备ID
	// 重试相同的clientID
	toClientID int64 // 指定接收客户端的ID
	large      bool  // 是否是超大群
	// ------- 优先队列用到 ------
	index      int   //在切片中的索引值
	pri        int64 // 优先级的时间点 值越小越优先
	retryCount int   // 当前重试次数
}

func (m *Message) GetMessageID() int64 {
	return m.MessageID
}

func (m *Message) SetSeq(seq uint32) {
	m.MessageSeq = seq
}

func (m *Message) GetSeq() uint32 {
	return m.MessageSeq
}

func (m *Message) Encode() []byte {
	var version uint8 = 0
	data := MarshalMessage(version, m)
	return wkstore.EncodeMessage(m.MessageSeq, data)
}

func (m *Message) Decode(msg []byte) error {
	messageSeq, data, err := wkstore.DecodeMessage(msg)
	if err != nil {
		return err
	}
	err = UnmarshalMessage(data, m)
	m.MessageSeq = messageSeq
	return err
}

func (m *Message) StreamStart() bool {
	if strings.TrimSpace(m.StreamNo) == "" {
		return false
	}
	return m.StreamFlag == wkproto.StreamFlagStart
}

func (m *Message) StreamIng() bool {
	if strings.TrimSpace(m.StreamNo) == "" {
		return false
	}
	return m.StreamFlag == wkproto.StreamFlagIng
}

func (m *Message) DeepCopy() (*Message, error) {
	dst := &Message{
		RecvPacket: &wkproto.RecvPacket{
			Framer:      m.Framer,
			Setting:     m.Setting,
			MsgKey:      m.MsgKey,
			MessageID:   m.MessageID,
			MessageSeq:  m.MessageSeq,
			ClientMsgNo: m.ClientMsgNo,
			StreamNo:    m.StreamNo,
			StreamSeq:   m.StreamSeq,
			StreamFlag:  m.StreamFlag,
			Timestamp:   m.Timestamp,
			ChannelID:   m.ChannelID,
			ChannelType: m.ChannelType,
			Topic:       m.Topic,
			FromUID:     m.FromUID,
			Payload:     m.Payload,
			ClientSeq:   m.ClientSeq,
		},
		ToUID:       m.ToUID,
		Subscribers: m.Subscribers,
	}
	dst.fromDeviceID = m.fromDeviceID
	dst.fromDeviceFlag = m.fromDeviceFlag
	dst.toClientID = m.toClientID
	dst.large = m.large
	dst.index = m.index
	dst.pri = m.pri
	dst.retryCount = m.retryCount
	return dst, nil
}

// MarshalMessage MarshalMessage
func MarshalMessage(version uint8, m *Message) []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteByte(wkproto.ToFixHeaderUint8(m.RecvPacket))
	enc.WriteUint8(version)
	enc.WriteByte(m.Setting.Uint8())
	enc.WriteInt64(m.MessageID)
	enc.WriteUint32(m.MessageSeq)
	enc.WriteString(m.ClientMsgNo)
	if m.Setting.IsSet(wkproto.SettingStream) {
		enc.WriteString(m.StreamNo)
		enc.WriteUint32(m.StreamSeq)
		enc.WriteUint8(uint8(m.StreamFlag))
	}
	enc.WriteInt32(m.Timestamp)
	enc.WriteString(m.FromUID)
	enc.WriteString(m.ChannelID)
	enc.WriteUint8(m.ChannelType)
	enc.WriteBytes(m.Payload)
	return enc.Bytes()
}

// UnmarshalMessage UnmarshalMessage
func UnmarshalMessage(data []byte, m *Message) error {
	dec := wkproto.NewDecoder(data)

	// header
	var err error
	var header uint8
	if header, err = dec.Uint8(); err != nil {
		return err
	}
	recvPacket := &wkproto.RecvPacket{}
	framer := wkproto.FramerFromUint8(header)
	if _, err = dec.Uint8(); err != nil {
		return err
	}
	recvPacket.Framer = framer

	// setting
	var setting uint8
	if setting, err = dec.Uint8(); err != nil {
		return err
	}
	m.RecvPacket = recvPacket

	m.Setting = wkproto.Setting(setting)

	// messageID
	if m.MessageID, err = dec.Int64(); err != nil {
		return err
	}

	// MessageSeq
	if m.MessageSeq, err = dec.Uint32(); err != nil {
		return err
	}
	// ClientMsgNo
	if m.ClientMsgNo, err = dec.String(); err != nil {
		return err
	}
	// StreamNo
	if m.Setting.IsSet(wkproto.SettingStream) {
		if m.StreamNo, err = dec.String(); err != nil {
			return err
		}
		if m.StreamSeq, err = dec.Uint32(); err != nil {
			return err
		}
		var streamFlag uint8
		if streamFlag, err = dec.Uint8(); err != nil {
			return err
		}
		m.StreamFlag = wkproto.StreamFlag(streamFlag)
	}
	// Timestamp
	if m.Timestamp, err = dec.Int32(); err != nil {
		return err
	}

	// FromUID
	if m.FromUID, err = dec.String(); err != nil {
		return err
	}
	// if m.QueueUID, err = dec.String(); err != nil {
	// 	return err
	// }

	// ChannelID
	if m.ChannelID, err = dec.String(); err != nil {
		return err
	}

	// ChannelType
	if m.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}
	// Payload
	if m.Payload, err = dec.BinaryAll(); err != nil {
		return err
	}
	return nil
}

type conversationResp struct {
	ChannelID   string       `json:"channel_id"`   // 频道ID
	ChannelType uint8        `json:"channel_type"` // 频道类型
	Unread      int          `json:"unread"`       // 未读数
	Timestamp   int64        `json:"timestamp"`
	LastMessage *MessageResp `json:"last_message"` // 最后一条消息
}

// MessageRespSlice MessageRespSlice
type MessageRespSlice []*MessageResp

func (m MessageRespSlice) Len() int { return len(m) }

func (m MessageRespSlice) Swap(i, j int) { m[i], m[j] = m[j], m[i] }

func (m MessageRespSlice) Less(i, j int) bool { return m[i].MessageSeq < m[j].MessageSeq }

// MessageResp 消息返回
type MessageResp struct {
	Header       MessageHeader      `json:"header"`                // 消息头
	Setting      uint8              `json:"setting"`               // 设置
	MessageID    int64              `json:"message_id"`            // 服务端的消息ID(全局唯一)
	MessageIDStr string             `json:"message_idstr"`         // 服务端的消息ID(全局唯一)
	ClientMsgNo  string             `json:"client_msg_no"`         // 客户端消息唯一编号
	StreamNo     string             `json:"stream_no,omitempty"`   // 流编号
	StreamSeq    uint32             `json:"stream_seq,omitempty"`  // 流序号
	StreamFlag   wkproto.StreamFlag `json:"stream_flag,omitempty"` // 流标记
	MessageSeq   uint32             `json:"message_seq"`           // 消息序列号 （用户唯一，有序递增）
	FromUID      string             `json:"from_uid"`              // 发送者UID
	ChannelID    string             `json:"channel_id"`            // 频道ID
	ChannelType  uint8              `json:"channel_type"`          // 频道类型
	Topic        string             `json:"topic,omitempty"`       // 话题ID
	Timestamp    int32              `json:"timestamp"`             // 服务器消息时间戳(10位，到秒)
	Payload      []byte             `json:"payload"`               // 消息内容
	Streams      []*StreamItemResp  `json:"streams,omitempty"`     // 消息流内容
}

func (m *MessageResp) from(messageD *Message, store wkstore.Store) {
	m.Header.NoPersist = wkutil.BoolToInt(messageD.NoPersist)
	m.Header.RedDot = wkutil.BoolToInt(messageD.RedDot)
	m.Header.SyncOnce = wkutil.BoolToInt(messageD.SyncOnce)
	m.Setting = messageD.Setting.Uint8()
	m.MessageID = messageD.MessageID
	m.MessageIDStr = strconv.FormatInt(messageD.MessageID, 10)
	m.ClientMsgNo = messageD.ClientMsgNo
	m.StreamNo = messageD.StreamNo
	m.StreamSeq = messageD.StreamSeq
	m.StreamFlag = messageD.StreamFlag
	m.MessageSeq = messageD.MessageSeq
	m.FromUID = messageD.FromUID
	m.Timestamp = messageD.Timestamp

	realChannelID := messageD.ChannelID
	if messageD.ChannelType == wkproto.ChannelTypePerson {
		if strings.Contains(messageD.ChannelID, "@") {
			channelIDs := strings.Split(messageD.ChannelID, "@")
			for _, channelID := range channelIDs {
				if messageD.FromUID != channelID {
					realChannelID = channelID
				}
			}
		}
	}
	m.ChannelID = realChannelID
	m.ChannelType = messageD.ChannelType
	m.Topic = messageD.Topic
	m.Payload = messageD.Payload

	if strings.TrimSpace(messageD.StreamNo) != "" && store != nil {
		streamItems, err := store.GetStreamItems(messageD.ChannelID, messageD.ChannelType, messageD.StreamNo)
		if err != nil {
			wklog.Error("获取streamItems失败！", zap.Error(err))
		}
		if len(streamItems) > 0 {
			streamItemResps := make([]*StreamItemResp, 0, len(streamItems))
			for _, streamItem := range streamItems {
				streamItemResps = append(streamItemResps, newStreamItemResp(streamItem))
			}
			m.Streams = streamItemResps
		}
	}
}

type StreamItemResp struct {
	StreamSeq   uint32 `json:"stream_seq"`    // 流序号
	ClientMsgNo string `json:"client_msg_no"` // 客户端消息唯一编号
	Blob        []byte `json:"blob"`          // 消息内容
}

func newStreamItemResp(m *wkstore.StreamItem) *StreamItemResp {

	return &StreamItemResp{
		StreamSeq:   m.StreamSeq,
		ClientMsgNo: m.ClientMsgNo,
		Blob:        m.Blob,
	}
}

type MessageOfflineNotify struct {
	MessageResp
	ToUIDs          []string `json:"to_uids"`
	Compress        string   `json:"compress,omitempty"`         // 压缩ToUIDs 如果为空 表示不压缩 为gzip则采用gzip压缩
	CompresssToUIDs []byte   `json:"compress_to_uids,omitempty"` // 已压缩的to_uids
	SourceID        int64    `json:"source_id,omitempty"`        // 来源节点ID
}

// MessageHeader Message header
type MessageHeader struct {
	NoPersist int `json:"no_persist"` // Is it not persistent
	RedDot    int `json:"red_dot"`    // Whether to show red dot
	SyncOnce  int `json:"sync_once"`  // This message is only synchronized or consumed once
}

type clearConversationUnreadReq struct {
	UID         string `json:"uid"`
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
	MessageSeq  uint32 `json:"message_seq"` // messageSeq 只有超大群才会传 因为超大群最近会话服务器不会维护，需要客户端传递messageSeq进行主动维护
}

func (req clearConversationUnreadReq) Check() error {
	if req.UID == "" {
		return errors.New("uid cannot be empty")
	}
	if req.ChannelID == "" || req.ChannelType == 0 {
		return errors.New("channel_id or channel_type cannot be empty")
	}
	return nil
}

type deleteChannelReq struct {
	UID         string `json:"uid"`
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
}

func (req deleteChannelReq) Check() error {
	if len(req.UID) <= 0 {
		return errors.New("Uid cannot be empty")
	}
	if req.ChannelID == "" || req.ChannelType == 0 {
		return errors.New("channel_id or channel_type cannot be empty")
	}
	return nil
}

type syncUserConversationResp struct {
	ChannelID       string         `json:"channel_id"`         // 频道ID
	ChannelType     uint8          `json:"channel_type"`       // 频道类型
	Unread          int            `json:"unread"`             // 未读消息
	Timestamp       int64          `json:"timestamp"`          // 最后一次会话时间
	LastMsgSeq      uint32         `json:"last_msg_seq"`       // 最后一条消息seq
	LastClientMsgNo string         `json:"last_client_msg_no"` // 最后一次消息客户端编号
	OffsetMsgSeq    int64          `json:"offset_msg_seq"`     // 偏移位的消息seq
	Version         int64          `json:"version"`            // 数据版本
	Recents         []*MessageResp `json:"recents"`            // 最近N条消息
}

func newSyncUserConversationResp(conversation *wkstore.Conversation) *syncUserConversationResp {

	return &syncUserConversationResp{
		ChannelID:       conversation.ChannelID,
		ChannelType:     conversation.ChannelType,
		Unread:          conversation.UnreadCount,
		Timestamp:       conversation.Timestamp,
		LastMsgSeq:      conversation.LastMsgSeq,
		LastClientMsgNo: conversation.LastClientMsgNo,
		Version:         conversation.Version,
	}
}

type channelRecentMessageReq struct {
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
	LastMsgSeq  uint32 `json:"last_msg_seq"`
}

type channelRecentMessage struct {
	ChannelID   string         `json:"channel_id"`
	ChannelType uint8          `json:"channel_type"`
	Messages    []*MessageResp `json:"messages"`
}

// MessageSendReq 消息发送请求
type MessageSendReq struct {
	Header      MessageHeader `json:"header"`        // 消息头
	ClientMsgNo string        `json:"client_msg_no"` // 客户端消息编号（相同编号，客户端只会显示一条）
	StreamNo    string        `json:"stream_no"`     // 消息流编号
	FromUID     string        `json:"from_uid"`      // 发送者UID
	ChannelID   string        `json:"channel_id"`    // 频道ID
	ChannelType uint8         `json:"channel_type"`  // 频道类型
	Subscribers []string      `json:"subscribers"`   // 订阅者 如果此字段有值，表示消息只发给指定的订阅者
	Payload     []byte        `json:"payload"`       // 消息内容
}

// Check 检查输入
func (m MessageSendReq) Check() error {
	if m.Payload == nil || len(m.Payload) <= 0 {
		return errors.New("payload不能为空！")
	}
	return nil
}

// ChannelInfoReq ChannelInfoReq
type ChannelInfoReq struct {
	ChannelID   string `json:"channel_id"`   // 频道ID
	ChannelType uint8  `json:"channel_type"` // 频道类型
	Large       int    `json:"large"`        // 是否是超大群
	Ban         int    `json:"ban"`          // 是否封禁频道（封禁后此频道所有人都将不能发消息，除了系统账号）
}

func (c ChannelInfoReq) ToChannelInfo() *wkstore.ChannelInfo {
	return &wkstore.ChannelInfo{
		ChannelID:   c.ChannelID,
		ChannelType: c.ChannelType,
		Large:       c.Large == 1,
		Ban:         c.Ban == 1,
	}
}

type ChannelInfoResp struct {
	Large int `json:"large"` // 是否是超大群
	Ban   int `json:"ban"`   // 是否封禁频道（封禁后此频道所有人都将不能发消息，除了系统账号）
}

func (c ChannelInfoResp) ToChannelInfo() *wkstore.ChannelInfo {
	return &wkstore.ChannelInfo{
		Large: c.Large == 1,
		Ban:   c.Ban == 1,
	}
}

// ChannelCreateReq 频道创建请求
type ChannelCreateReq struct {
	ChannelInfoReq
	Subscribers []string `json:"subscribers"` // 订阅者
}

// Check 检查请求参数
func (r ChannelCreateReq) Check() error {
	if strings.TrimSpace(r.ChannelID) == "" {
		return errors.New("频道ID不能为空！")
	}
	if r.ChannelType == 0 {
		return errors.New("频道类型错误！")
	}
	return nil
}

type subscriberAddReq struct {
	ChannelID      string   `json:"channel_id"`      // 频道ID
	ChannelType    uint8    `json:"channel_type"`    // 频道类型
	Reset          int      `json:"reset"`           // 是否重置订阅者 （0.不重置 1.重置），选择重置，将删除原来的所有成员
	TempSubscriber int      `json:"temp_subscriber"` //  是否是临时订阅者 (1. 是 0. 否)
	Subscribers    []string `json:"subscribers"`     // 订阅者
}

func (s subscriberAddReq) Check() error {
	if strings.TrimSpace(s.ChannelID) == "" {
		return errors.New("频道ID不能为空！")
	}
	if stringArrayIsEmpty(s.Subscribers) {
		return errors.New("订阅者不能为空！")
	}
	return nil
}

type subscriberRemoveReq struct {
	ChannelID      string   `json:"channel_id"`
	ChannelType    uint8    `json:"channel_type"`
	TempSubscriber int      `json:"temp_subscriber"` //  是否是临时订阅者 (1. 是 0. 否)
	Subscribers    []string `json:"subscribers"`
}

func (s subscriberRemoveReq) Check() error {
	if strings.TrimSpace(s.ChannelID) == "" {
		return errors.New("频道ID不能为空！")
	}
	if stringArrayIsEmpty(s.Subscribers) {
		return errors.New("订阅者不能为空！")
	}
	return nil
}

func stringArrayIsEmpty(array []string) bool {
	if len(array) == 0 {
		return true
	}
	emptyCount := 0
	for _, a := range array {
		if strings.TrimSpace(a) == "" {
			emptyCount++
		}
	}
	return emptyCount >= len(array)
}

type blacklistReq struct {
	ChannelID   string   `json:"channel_id"`   // 频道ID
	ChannelType uint8    `json:"channel_type"` // 频道类型
	UIDs        []string `json:"uids"`         // 订阅者
}

func (r blacklistReq) Check() error {
	if r.ChannelID == "" {
		return errors.New("channel_id不能为空！")
	}
	if r.ChannelType == 0 {
		return errors.New("频道类型不能为0！")
	}
	if len(r.UIDs) <= 0 {
		return errors.New("uids不能为空！")
	}
	return nil
}

// ChannelDeleteReq 删除频道请求
type ChannelDeleteReq struct {
	ChannelID   string `json:"channel_id"`   // 频道ID
	ChannelType uint8  `json:"channel_type"` // 频道类型
}

type whitelistReq struct {
	ChannelID   string   `json:"channel_id"`   // 频道ID
	ChannelType uint8    `json:"channel_type"` // 频道类型
	UIDs        []string `json:"uids"`         // 订阅者
}

func (r whitelistReq) Check() error {
	if r.ChannelID == "" {
		return errors.New("channel_id不能为空！")
	}
	if r.ChannelType == 0 {
		return errors.New("频道类型不能为0！")
	}
	if stringArrayIsEmpty(r.UIDs) {
		return errors.New("uids不能为空！")
	}
	return nil
}

type syncReq struct {
	UID        string `json:"uid"`         // 用户uid
	MessageSeq uint32 `json:"message_seq"` // 客户端最大消息序列号
	Limit      int    `json:"limit"`       // 消息数量限制
}

func (r syncReq) Check() error {
	if strings.TrimSpace(r.UID) == "" {
		return errors.New("用户uid不能为空！")
	}
	if r.Limit < 0 {
		return errors.New("limit不能为负数！")
	}
	return nil
}

type syncMessageResp struct {
	StartMessageSeq uint32         `json:"start_message_seq"` // 开始序列号
	EndMessageSeq   uint32         `json:"end_message_seq"`   // 结束序列号
	More            int            `json:"more"`              // 是否还有更多 1.是 0.否
	Messages        []*MessageResp `json:"messages"`          // 消息数据
}

type syncackReq struct {
	// 用户uid
	UID string `json:"uid"`
	// 最后一次同步的message_seq
	LastMessageSeq uint32 `json:"last_message_seq"`
}

func (s syncackReq) Check() error {
	if strings.TrimSpace(s.UID) == "" {
		return errors.New("用户UID不能为空！")
	}
	if s.LastMessageSeq == 0 {
		return errors.New("最后一次messageSeq不能为0！")
	}
	return nil
}

type messageStreamStartReq struct {
	Header      MessageHeader `json:"header"`        // 消息头
	ClientMsgNo string        `json:"client_msg_no"` // 客户端消息编号（相同编号，客户端只会显示一条）
	FromUID     string        `json:"from_uid"`      // 发送者UID
	ChannelID   string        `json:"channel_id"`    // 频道ID
	ChannelType uint8         `json:"channel_type"`  // 频道类型
	Payload     []byte        `json:"payload"`       // 消息内容
}

type messageStreamEndReq struct {
	StreamNo    string `json:"stream_no"`    // 消息流编号
	ChannelID   string `json:"channel_id"`   // 频道ID
	ChannelType uint8  `json:"channel_type"` // 频道类型
}
