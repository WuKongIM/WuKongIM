package server

import (
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

var defaultWkproto = wkproto.New()

type ReactorChannelMessage struct {
	Persist      bool   // 是否持久化
	FromUid      string // 发送者
	FromDeviceId string // 发送者设备ID
	MessageId    int64
	MessageSeq   uint32
	SendPacket   *wkproto.SendPacket
	ReasonCode   wkproto.ReasonCode
	Index        uint64
}

func (r *ReactorChannelMessage) Marshal() ([]byte, error) {
	enc := wkproto.NewEncoder()
	defer enc.End()

	enc.WriteUint8(wkutil.BoolToUint8(r.Persist))
	enc.WriteString(r.FromUid)
	enc.WriteString(r.FromDeviceId)
	enc.WriteInt64(r.MessageId)
	enc.WriteUint32(r.MessageSeq)

	var packetData []byte
	var err error
	if r.SendPacket != nil {
		packetData, err = defaultWkproto.EncodeFrame(r.SendPacket, wkproto.LatestVersion)
		if err != nil {
			return nil, err
		}
	}
	enc.WriteBinary(packetData)
	enc.WriteUint8(uint8(r.ReasonCode))
	enc.WriteUint64(r.Index)

	return enc.Bytes(), nil
}

func (m *ReactorChannelMessage) Size() uint64 {
	size := uint64(0)

	size += 1
	size += uint64(len(m.FromUid)) + 2
	size += uint64(len(m.FromDeviceId)) + 2
	size += 8 // messageId
	size += 4 // messageSeq
	if m.SendPacket != nil {
		size += uint64(m.SendPacket.RemainingLength) + 2
	} else {
		size += 2
	}
	size += 1 // reasonCode
	size += 8 // index
	return size
}

type ReactorUserMessage struct {
	ConnId         int64         // 连接id
	Uid            string        // 用户ID
	DeviceId       string        // 设备ID
	InPacket       wkproto.Frame // 输入的包
	OutBytes       []byte        // 需要输出的字节
	Index          uint64        // 消息下标
	SenderDeviceId string        // 发送者设备ID
}

func (m *ReactorUserMessage) Size() uint64 {

	return 0
}

type ChannelAction struct {
	ActionType ChannelActionType
	Index      uint64
	EndIndex   uint64
	Reason     Reason
	ReasonCode wkproto.ReasonCode
	Messages   []*ReactorChannelMessage
}

type Message struct {
	wkdb.Message
	ToUID          string             // 接受者
	Subscribers    []string           // 订阅者 如果此字段有值 则表示消息只发送给指定的订阅者
	fromDeviceFlag wkproto.DeviceFlag // 发送者设备标示
	fromDeviceID   string             // 发送者设备ID
	// term
	term uint64 // 当前领导term
	// 重试相同的toDeviceID
	toDeviceID string // 指定设备ID
	large      bool   // 是否是超大群
	// ------- 优先队列用到 ------
	index      int   //在切片中的索引值
	pri        int64 // 优先级的时间点 值越小越优先
	retryCount int   // 当前重试次数
}

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

func (m *MessageResp) from(messageD wkdb.Message, store *clusterstore.Store) {
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
	m.MessageSeq = uint64(messageD.MessageSeq)
	m.FromUID = messageD.FromUID
	m.Expire = messageD.Expire
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

	// if strings.TrimSpace(messageD.StreamNo) != "" && store != nil {
	// 	streamItems, err := store.GetStreamItems(GetFakeChannelIDWith(messageD.FromUID, messageD.ChannelID), messageD.ChannelType, messageD.StreamNo)
	// 	if err != nil {
	// 		wklog.Error("获取streamItems失败！", zap.Error(err))
	// 	}
	// 	if len(streamItems) > 0 {
	// 		streamItemResps := make([]*StreamItemResp, 0, len(streamItems))
	// 		for _, streamItem := range streamItems {
	// 			streamItemResps = append(streamItemResps, newStreamItemResp(streamItem))
	// 		}
	// 		m.Streams = streamItemResps
	// 	}
	// }
}

// type StreamItemResp struct {
// 	StreamSeq   uint32 `json:"stream_seq"`    // 流序号
// 	ClientMsgNo string `json:"client_msg_no"` // 客户端消息唯一编号
// 	Blob        []byte `json:"blob"`          // 消息内容
// }

// func newStreamItemResp(m *wkstore.StreamItem) *StreamItemResp {

// 	return &StreamItemResp{
// 		StreamSeq:   m.StreamSeq,
// 		ClientMsgNo: m.ClientMsgNo,
// 		Blob:        m.Blob,
// 	}
// }

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

type everyScheduler struct {
	Interval time.Duration
}

func (s *everyScheduler) Next(prev time.Time) time.Time {
	return prev.Add(s.Interval)
}
