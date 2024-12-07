package server

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

var defaultProtoVersion uint8 = 4
var defaultWkproto = wkproto.New()

var EmptyReactorChannelMessage = ReactorChannelMessage{}

type ReactorChannelMessage struct {
	FromConnId   int64  // 发送者连接ID
	FromUid      string // 发送者
	FromDeviceId string // 发送者设备ID
	FromNodeId   uint64 // 如果不为0，则表示此消息是从其他节点转发过来的
	MessageId    int64
	MessageSeq   uint32
	SendPacket   *wkproto.SendPacket
	IsEncrypt    bool // SendPacket的payload是否加密
	ReasonCode   wkproto.ReasonCode
	Index        uint64
}

func (r *ReactorChannelMessage) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteInt64(r.FromConnId)
	enc.WriteString(r.FromUid)
	enc.WriteString(r.FromDeviceId)
	enc.WriteUint64(r.FromNodeId)
	enc.WriteInt64(r.MessageId)
	enc.WriteUint8(wkutil.BoolToUint8(r.IsEncrypt))
	enc.WriteUint8(uint8(r.ReasonCode))

	var packetData []byte
	var err error
	if r.SendPacket != nil {
		packetData, err = defaultWkproto.EncodeFrame(r.SendPacket, defaultProtoVersion)
		if err != nil {
			return nil, err
		}
	}
	enc.WriteBinary(packetData)

	return enc.Bytes(), nil
}

func (r *ReactorChannelMessage) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error

	if r.FromConnId, err = dec.Int64(); err != nil {
		return err
	}
	if r.FromUid, err = dec.String(); err != nil {
		return err
	}
	if r.FromDeviceId, err = dec.String(); err != nil {
		return err
	}

	if r.FromNodeId, err = dec.Uint64(); err != nil {
		return err
	}

	if r.MessageId, err = dec.Int64(); err != nil {
		return err
	}

	var isEncrypt uint8
	if isEncrypt, err = dec.Uint8(); err != nil {
		return err
	}
	r.IsEncrypt = wkutil.Uint8ToBool(isEncrypt)

	var reasonCode uint8
	if reasonCode, err = dec.Uint8(); err != nil {
		return err
	}
	r.ReasonCode = wkproto.ReasonCode(reasonCode)

	var packetData []byte
	if packetData, err = dec.Binary(); err != nil {
		return err
	}
	if len(packetData) > 0 {
		r.SendPacket = &wkproto.SendPacket{}
		packet, _, err := defaultWkproto.DecodeFrame(packetData, defaultProtoVersion)
		if err != nil {
			return err
		}
		r.SendPacket = packet.(*wkproto.SendPacket)
	}

	return nil
}

func (m *ReactorChannelMessage) Size() uint64 {
	size := uint64(0)
	size += 8 // FromConnId
	size += (uint64(len(m.FromUid)) + 2)
	size += (uint64(len(m.FromDeviceId)) + 2)
	size += 8 // FromNodeId
	size += 8 // MessageId
	size += 1 // IsEncrypt
	size += 1 // IsSystem
	size += 1 // ReasonCode
	if m.SendPacket != nil {
		size += (1 + uint64(m.SendPacket.GetRemainingLength()) + 2)
	}
	return size
}

type ChannelFowardReq struct {
	ChannelId   string // 频道ID
	ChannelType uint8  // 频道类型
	Messages    []ReactorChannelMessage
}

func (r ChannelFowardReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(r.ChannelId)
	enc.WriteUint8(r.ChannelType)
	enc.WriteUint32(uint32(len(r.Messages)))
	for _, m := range r.Messages {
		data, err := m.Marshal()
		if err != nil {
			return nil, err
		}
		enc.WriteBinary(data)
	}
	return enc.Bytes(), nil
}

func (r *ChannelFowardReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if r.ChannelId, err = dec.String(); err != nil {
		return err
	}
	if r.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}
	count, err := dec.Uint32()
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	for i := 0; i < int(count); i++ {
		m := ReactorChannelMessage{}
		messageData, err := dec.Binary()
		if err != nil {
			return err
		}
		if err = m.Unmarshal(messageData); err != nil {
			return err
		}
		r.Messages = append(r.Messages, m)
	}
	return nil

}

type ReactorChannelMessageSet []ReactorChannelMessage

func (rs ReactorChannelMessageSet) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()

	enc.WriteUint32(uint32(len(rs)))

	for _, r := range rs {
		enc.WriteInt64(r.FromConnId)
		enc.WriteString(r.FromUid)
		enc.WriteString(r.FromDeviceId)
		enc.WriteUint64(r.FromNodeId)
		enc.WriteInt64(r.MessageId)
		enc.WriteUint32(r.MessageSeq)

		var packetData []byte
		var err error
		if r.SendPacket != nil {
			packetData, err = defaultWkproto.EncodeFrame(r.SendPacket, defaultProtoVersion)
			if err != nil {
				return nil, err
			}
		}
		enc.WriteBinary(packetData)
	}

	return enc.Bytes(), nil
}

func (rs *ReactorChannelMessageSet) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	count, err := dec.Uint32()
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}

	for i := 0; i < int(count); i++ {
		r := ReactorChannelMessage{}
		if r.FromConnId, err = dec.Int64(); err != nil {
			return err
		}
		if r.FromUid, err = dec.String(); err != nil {
			return err
		}
		if r.FromDeviceId, err = dec.String(); err != nil {
			return err
		}
		if r.FromNodeId, err = dec.Uint64(); err != nil {
			return err
		}
		if r.MessageId, err = dec.Int64(); err != nil {
			return err
		}
		if r.MessageSeq, err = dec.Uint32(); err != nil {
			return err
		}

		// 读取SendPacket
		packetData, err := dec.Binary()
		if err != nil {
			return err
		}
		packet, _, err := defaultWkproto.DecodeFrame(packetData, defaultProtoVersion)
		if err != nil {
			return err
		}
		r.SendPacket = packet.(*wkproto.SendPacket)

		*rs = append(*rs, r)
	}
	return nil
}

var EmptyReactorUserMessage = ReactorUserMessage{}

type ReactorUserMessage struct {
	FromNodeId uint64        // 源节点Id
	ConnId     int64         // 连接id
	DeviceId   string        // 设备ID
	InPacket   wkproto.Frame // 输入的包
	FrameType  wkproto.FrameType
	OutBytes   []byte // 需要输出的字节
	Index      uint64 // 消息下标

}

// 这个大小是不准确的，只是一个大概的值，目的是计算传输的数据量
func (m *ReactorUserMessage) Size() uint64 {
	var size uint64 = 0
	size += 8 // ConnId
	size += (uint64(len(m.DeviceId)) + 2)
	if m.InPacket != nil {
		size += (1 + uint64(m.InPacket.GetRemainingLength()) + 2)
	} else {
		size += (1 + uint64(len(m.OutBytes))) + 2
	}
	size += 8 // index

	return size
}

func (m *ReactorUserMessage) MarshalWithEncoder(encoder *wkproto.Encoder) error {
	encoder.WriteUint64(m.FromNodeId)
	encoder.WriteInt64(m.ConnId)
	encoder.WriteString(m.DeviceId)

	var packetData []byte
	var err error
	if m.InPacket != nil {
		packetData, err = defaultWkproto.EncodeFrame(m.InPacket, defaultProtoVersion)
		if err != nil {
			return err
		}
	}
	if len(packetData) > 0 {
		encoder.WriteUint8(1) // 有包数据
		encoder.WriteBinary(packetData)
	} else {
		encoder.WriteUint8(0) // 没包数据
		encoder.WriteUint8(uint8(m.FrameType))
		encoder.WriteBinary(m.OutBytes)
	}
	return nil
}

func (m *ReactorUserMessage) UnmarshalWithDecoder(decoder *wkproto.Decoder) error {
	var err error
	if m.FromNodeId, err = decoder.Uint64(); err != nil {
		return err
	}

	if m.ConnId, err = decoder.Int64(); err != nil {
		return err
	}
	if m.DeviceId, err = decoder.String(); err != nil {
		return err
	}

	hasPacket, err := decoder.Uint8()
	if err != nil {
		return err
	}
	if hasPacket == 1 {
		packetData, err := decoder.Binary()
		if err != nil {
			return err
		}
		packet, _, err := defaultWkproto.DecodeFrame(packetData, defaultProtoVersion)
		if err != nil {
			return err
		}
		m.InPacket = packet
	} else {
		frameType, err := decoder.Uint8()
		if err != nil {
			return err
		}
		m.FrameType = wkproto.FrameType(frameType)
		m.OutBytes, err = decoder.Binary()
		if err != nil {
			return err
		}
	}
	return nil

}

type ChannelAction struct {
	ActionType ChannelActionType
	Index      uint64
	EndIndex   uint64
	Reason     Reason
	ReasonCode wkproto.ReasonCode
	Messages   []ReactorChannelMessage
	LeaderId   uint64 // 频道领导节点ID

	UniqueNo string
}

// MessageResp 消息返回
type MessageResp struct {
	Header       MessageHeader      `json:"header"`                // 消息头
	Setting      uint8              `json:"setting"`               // 设置
	MessageId    int64              `json:"message_id"`            // 服务端的消息ID(全局唯一)
	MessageIdStr string             `json:"message_idstr"`         // 服务端的消息ID(全局唯一)
	ClientMsgNo  string             `json:"client_msg_no"`         // 客户端消息唯一编号
	StreamNo     string             `json:"stream_no,omitempty"`   // 流编号
	StreamSeq    uint32             `json:"stream_seq,omitempty"`  // 流序号
	StreamFlag   wkproto.StreamFlag `json:"stream_flag,omitempty"` // 流标记
	MessageSeq   uint64             `json:"message_seq"`           // 消息序列号 （用户唯一，有序递增）
	FromUID      string             `json:"from_uid"`              // 发送者UID
	ChannelID    string             `json:"channel_id"`            // 频道ID
	ChannelType  uint8              `json:"channel_type"`          // 频道类型
	Topic        string             `json:"topic,omitempty"`       // 话题ID
	Expire       uint32             `json:"expire"`                // 消息过期时间
	Timestamp    int32              `json:"timestamp"`             // 服务器消息时间戳(10位，到秒)
	Payload      []byte             `json:"payload"`               // 消息内容
	// Streams      []*StreamItemResp  `json:"streams,omitempty"`     // 消息流内容
}

func (m *MessageResp) from(messageD wkdb.Message, s *Server) {

	fromUid := messageD.FromUID

	if fromUid == s.opts.SystemUID {
		fromUid = ""
	}

	m.Header.NoPersist = wkutil.BoolToInt(messageD.NoPersist)
	m.Header.RedDot = wkutil.BoolToInt(messageD.RedDot)
	m.Header.SyncOnce = wkutil.BoolToInt(messageD.SyncOnce)
	m.Setting = messageD.Setting.Uint8()
	m.MessageId = messageD.MessageID
	m.MessageIdStr = strconv.FormatInt(messageD.MessageID, 10)
	m.ClientMsgNo = messageD.ClientMsgNo
	m.StreamNo = messageD.StreamNo
	m.StreamSeq = messageD.StreamSeq
	m.StreamFlag = messageD.StreamFlag
	m.MessageSeq = uint64(messageD.MessageSeq)
	m.FromUID = fromUid
	m.Expire = messageD.Expire
	m.Timestamp = messageD.Timestamp

	realChannelID := messageD.ChannelID
	if messageD.ChannelType == wkproto.ChannelTypePerson {
		if strings.Contains(messageD.ChannelID, "@") {
			channelIDs := strings.Split(messageD.ChannelID, "@")
			for _, channelID := range channelIDs {
				if fromUid != channelID {
					realChannelID = channelID
				}
			}
		}
	}
	m.ChannelID = realChannelID
	m.ChannelType = messageD.ChannelType
	m.Topic = messageD.Topic
	m.Payload = messageD.Payload
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

type ChannelInfoResp struct {
	Large   int `json:"large"`   // 是否是超大群
	Ban     int `json:"ban"`     // 是否封禁频道（封禁后此频道所有人都将不能发消息，除了系统账号）
	Disband int `json:"disband"` // 是否解散频道
}

func (c ChannelInfoResp) ToChannelInfo() *wkdb.ChannelInfo {
	return &wkdb.ChannelInfo{
		Large: c.Large == 1,
		Ban:   c.Ban == 1,
	}
}

type ForwardSendackPacket struct {
	Uid      string
	DeviceId string
	ConnId   int64
	Sendack  *wkproto.SendackPacket
}

type ForwardSendackPacketSet []*ForwardSendackPacket

func (rs ForwardSendackPacketSet) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()

	enc.WriteUint32(uint32(len(rs)))

	for _, r := range rs {
		enc.WriteString(r.Uid)
		enc.WriteString(r.DeviceId)
		enc.WriteInt64(r.ConnId)
		sendackData, err := defaultWkproto.EncodeFrame(r.Sendack, defaultProtoVersion)
		if err != nil {
			return nil, err
		}
		enc.WriteBinary(sendackData)
	}

	return enc.Bytes(), nil
}

func (rs *ForwardSendackPacketSet) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	count, err := dec.Uint32()
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}

	for i := 0; i < int(count); i++ {
		r := &ForwardSendackPacket{}
		if r.Uid, err = dec.String(); err != nil {
			return err
		}
		if r.DeviceId, err = dec.String(); err != nil {
			return err
		}
		if r.ConnId, err = dec.Int64(); err != nil {
			return err
		}
		var sendackData []byte
		if sendackData, err = dec.Binary(); err != nil {
			return err
		}
		sendack, _, err := defaultWkproto.DecodeFrame(sendackData, defaultProtoVersion)
		if err != nil {
			return err
		}
		r.Sendack = sendack.(*wkproto.SendackPacket)
		*rs = append(*rs, r)
	}
	return nil

}

type everyScheduler struct {
	Interval time.Duration
}

func (s *everyScheduler) Next(prev time.Time) time.Time {
	return prev.Add(s.Interval)
}

type FowardWriteReqSlice []*FowardWriteReq

func (f FowardWriteReqSlice) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint32(uint32(len(f)))
	for _, r := range f {
		enc.WriteString(r.Uid)
		enc.WriteString(r.DeviceId)
		enc.WriteInt64(r.ConnId)
		enc.WriteUint32(r.RecvFrameCount)
		enc.WriteInt32(int32(len(r.Data)))
		enc.WriteBytes(r.Data)
	}
	return enc.Bytes(), nil
}

func (f *FowardWriteReqSlice) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	count, err := dec.Uint32()
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}

	for i := 0; i < int(count); i++ {
		r := &FowardWriteReq{}
		if r.Uid, err = dec.String(); err != nil {
			return err
		}
		if r.DeviceId, err = dec.String(); err != nil {
			return err
		}
		if r.ConnId, err = dec.Int64(); err != nil {
			return err
		}
		if r.RecvFrameCount, err = dec.Uint32(); err != nil {
			return err
		}
		dataLen, err := dec.Int32()
		if err != nil {
			return err
		}
		r.Data, err = dec.Bytes(int(dataLen))
		if err != nil {
			return err
		}

		*f = append(*f, r)
	}
	return nil
}

// 转发写请求
type FowardWriteReq struct {
	Uid            string
	DeviceId       string
	ConnId         int64
	RecvFrameCount uint32 // recv消息数量 (统计用)
	Data           []byte

	// 以下字段内部使用不编码
	localConnId int64 // 本地连接ID
}

func (f *FowardWriteReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(f.Uid)
	enc.WriteString(f.DeviceId)
	enc.WriteInt64(f.ConnId)
	enc.WriteUint32(f.RecvFrameCount)
	enc.WriteBytes(f.Data)
	return enc.Bytes(), nil
}

func (f *FowardWriteReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if f.Uid, err = dec.String(); err != nil {
		return err
	}
	if f.DeviceId, err = dec.String(); err != nil {
		return err
	}
	if f.ConnId, err = dec.Int64(); err != nil {
		return err
	}

	if f.RecvFrameCount, err = dec.Uint32(); err != nil {
		return err
	}

	if f.Data, err = dec.BinaryAll(); err != nil {
		return err
	}

	return nil
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

type channelReq struct {
	ChannelId   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
}

func (c *channelReq) Marshal() []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()

	enc.WriteString(c.ChannelId)
	enc.WriteUint8(c.ChannelType)
	return enc.Bytes()
}

func (c *channelReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if c.ChannelId, err = dec.String(); err != nil {
		return err
	}
	if c.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}
	return nil
}

type syncUserConversationResp struct {
	ChannelId       string         `json:"channel_id"`         // 频道ID
	ChannelType     uint8          `json:"channel_type"`       // 频道类型
	Unread          int            `json:"unread"`             // 未读消息
	Timestamp       int64          `json:"timestamp"`          // 最后一次会话时间
	LastMsgSeq      uint32         `json:"last_msg_seq"`       // 最后一条消息seq
	LastClientMsgNo string         `json:"last_client_msg_no"` // 最后一次消息客户端编号
	OffsetMsgSeq    int64          `json:"offset_msg_seq"`     // 偏移位的消息seq
	ReadedToMsgSeq  uint32         `json:"readed_to_msg_seq"`  // 已读至的消息seq
	Version         int64          `json:"version"`            // 数据版本
	Recents         []*MessageResp `json:"recents"`            // 最近N条消息
}

func newSyncUserConversationResp(conversation wkdb.Conversation) *syncUserConversationResp {
	realChannelId := conversation.ChannelId
	if conversation.ChannelType == wkproto.ChannelTypePerson {
		from, to := GetFromUIDAndToUIDWith(conversation.ChannelId)
		if from == conversation.Uid {
			realChannelId = to
		} else {
			realChannelId = from
		}
	}
	return &syncUserConversationResp{
		ChannelId:      realChannelId,
		ChannelType:    conversation.ChannelType,
		Unread:         int(conversation.UnreadCount),
		ReadedToMsgSeq: uint32(conversation.ReadToMsgSeq),
	}
}

type channelRecentMessageReq struct {
	ChannelId   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
	LastMsgSeq  uint64 `json:"last_msg_seq"`
}

type channelRecentMessage struct {
	ChannelId   string         `json:"channel_id"`
	ChannelType uint8          `json:"channel_type"`
	Messages    []*MessageResp `json:"messages"`
}

type MessageRespSlice []*MessageResp

func (m MessageRespSlice) Len() int { return len(m) }

func (m MessageRespSlice) Swap(i, j int) { m[i], m[j] = m[j], m[i] }

func (m MessageRespSlice) Less(i, j int) bool { return m[i].MessageSeq < m[j].MessageSeq }

// ChannelCreateReq 频道创建请求
type ChannelCreateReq struct {
	ChannelInfoReq
	Reset       int      `json:"reset"`       // 是否重置订阅者 （0.不重置 1.重置），选择重置，将删除原来的所有成员
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
	if IsSpecialChar(r.ChannelID) {
		return errors.New("频道ID不能包含特殊字符！")
	}
	return nil
}

type subscriberAddReq struct {
	ChannelId      string   `json:"channel_id"`      // 频道ID
	ChannelType    uint8    `json:"channel_type"`    // 频道类型
	Reset          int      `json:"reset"`           // 是否重置订阅者 （0.不重置 1.重置），选择重置，将删除原来的所有成员
	TempSubscriber int      `json:"temp_subscriber"` //  是否是临时订阅者 (1. 是 0. 否)
	Subscribers    []string `json:"subscribers"`     // 订阅者
}

func (s subscriberAddReq) Check() error {
	if strings.TrimSpace(s.ChannelId) == "" {
		return errors.New("频道ID不能为空！")
	}
	if IsSpecialChar(s.ChannelId) {
		return errors.New("频道ID不能包含特殊字符！")
	}
	if stringArrayIsEmpty(s.Subscribers) {
		return errors.New("订阅者不能为空！")
	}
	return nil
}

type subscriberGetReq struct {
	ChannelId   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
}

func (s *subscriberGetReq) Marshal() []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()

	enc.WriteString(s.ChannelId)
	enc.WriteUint8(s.ChannelType)

	return enc.Bytes()
}

func (s *subscriberGetReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if s.ChannelId, err = dec.String(); err != nil {
		return err
	}
	if s.ChannelType, err = dec.Uint8(); err != nil {
		return err
	}
	return nil
}

type subscriberGetResp []string

func (s subscriberGetResp) Marshal() []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()

	enc.WriteUint32(uint32(len(s)))
	for _, uid := range s {
		enc.WriteString(uid)
	}

	return enc.Bytes()
}

func (s *subscriberGetResp) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	count, err := dec.Uint32()
	if err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	for i := 0; i < int(count); i++ {
		uid, err := dec.String()
		if err != nil {
			return err
		}
		*s = append(*s, uid)
	}
	return nil
}

type subscriberRemoveReq struct {
	ChannelId      string   `json:"channel_id"`
	ChannelType    uint8    `json:"channel_type"`
	TempSubscriber int      `json:"temp_subscriber"` //  是否是临时订阅者 (1. 是 0. 否)
	Subscribers    []string `json:"subscribers"`
}

func (s subscriberRemoveReq) Check() error {
	if strings.TrimSpace(s.ChannelId) == "" {
		return errors.New("频道ID不能为空！")
	}
	if IsSpecialChar(s.ChannelId) {
		return errors.New("频道ID不能包含特殊字符！")
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
	ChannelId   string   `json:"channel_id"`   // 频道ID
	ChannelType uint8    `json:"channel_type"` // 频道类型
	UIDs        []string `json:"uids"`         // 订阅者
}

func (r blacklistReq) Check() error {
	if r.ChannelId == "" {
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
	ChannelId   string `json:"channel_id"`   // 频道ID
	ChannelType uint8  `json:"channel_type"` // 频道类型
}

type whitelistReq struct {
	ChannelId   string   `json:"channel_id"`   // 频道ID
	ChannelType uint8    `json:"channel_type"` // 频道类型
	UIDs        []string `json:"uids"`         // 订阅者
}

func (r whitelistReq) Check() error {
	if r.ChannelId == "" {
		return errors.New("channel_id不能为空！")
	}
	if IsSpecialChar(r.ChannelId) {
		return errors.New("频道ID不能包含特殊字符！")
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
	MessageSeq uint64 `json:"message_seq"` // 客户端最大消息序列号
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

var emptySyncMessageResp = syncMessageResp{
	StartMessageSeq: 0,
	EndMessageSeq:   0,
	More:            0,
	Messages:        make([]*MessageResp, 0),
}

type syncMessageResp struct {
	StartMessageSeq uint64         `json:"start_message_seq"` // 开始序列号
	EndMessageSeq   uint64         `json:"end_message_seq"`   // 结束序列号
	More            int            `json:"more"`              // 是否还有更多 1.是 0.否
	Messages        []*MessageResp `json:"messages"`          // 消息数据
}

type syncackReq struct {
	// 用户uid
	UID string `json:"uid"`
	// 最后一次同步的message_seq
	LastMessageSeq uint64 `json:"last_message_seq"`
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

// ChannelInfoReq ChannelInfoReq
type ChannelInfoReq struct {
	ChannelID   string `json:"channel_id"`   // 频道ID
	ChannelType uint8  `json:"channel_type"` // 频道类型
	Large       int    `json:"large"`        // 是否是超大群
	Ban         int    `json:"ban"`          // 是否封禁频道（封禁后此频道所有人都将不能发消息，除了系统账号）
	Disband     int    `json:"disband"`      // 是否解散频道
}

func (c ChannelInfoReq) ToChannelInfo() wkdb.ChannelInfo {
	createdAt := time.Now()
	updatedAt := time.Now()
	return wkdb.ChannelInfo{
		ChannelId:   c.ChannelID,
		ChannelType: c.ChannelType,
		Large:       c.Large == 1,
		Ban:         c.Ban == 1,
		Disband:     c.Disband == 1,
		CreatedAt:   &createdAt,
		UpdatedAt:   &updatedAt,
	}
}

// MessageSendReq 消息发送请求
type MessageSendReq struct {
	Header      MessageHeader `json:"header"`        // 消息头
	ClientMsgNo string        `json:"client_msg_no"` // 客户端消息编号（相同编号，客户端只会显示一条）
	StreamNo    string        `json:"stream_no"`     // 消息流编号
	FromUID     string        `json:"from_uid"`      // 发送者UID
	ChannelID   string        `json:"channel_id"`    // 频道ID
	ChannelType uint8         `json:"channel_type"`  // 频道类型
	Expire      uint32        `json:"expire"`        // 消息过期时间
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

type allowSendReq struct {
	From string `json:"from"` // 发送者
	To   string `json:"to"`   // 接收者
}

func (a *allowSendReq) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if a.From, err = dec.String(); err != nil {
		return err
	}
	if a.To, err = dec.String(); err != nil {
		return err
	}
	return nil
}

func (a *allowSendReq) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteString(a.From)
	enc.WriteString(a.To)
	return enc.Bytes(), nil
}

type reactorStreamMessage struct {
}

func (r *reactorStreamMessage) size() uint64 {
	return 0
}

type streamAction struct {
	actionType StreamActionType
	msgs       []*reactorStreamMessage
}

type tmpSubscriberSetReq struct {
	ChannelId string   `json:"channel_id"` // 频道ID
	Uids      []string `json:"uids"`       // 订阅者
}

func (r tmpSubscriberSetReq) Check() error {
	if r.ChannelId == "" {
		return errors.New("channel_id不能为空！")
	}
	if IsSpecialChar(r.ChannelId) {
		return errors.New("频道ID不能包含特殊字符！")
	}
	if len(r.Uids) <= 0 {
		return errors.New("uids不能为空！")
	}
	return nil
}

const (
	defaultSlotId uint32 = 0 // 默认slotId
)
