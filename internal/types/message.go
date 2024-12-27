package types

import (
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

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

func (m *MessageResp) From(messageD wkdb.Message, systemUid string) {

	fromUid := messageD.FromUID

	if fromUid == systemUid {
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

// MessageHeader Message header
type MessageHeader struct {
	NoPersist int `json:"no_persist"` // Is it not persistent
	RedDot    int `json:"red_dot"`    // Whether to show red dot
	SyncOnce  int `json:"sync_once"`  // This message is only synchronized or consumed once
}

type MessageRespSlice []*MessageResp

func (m MessageRespSlice) Len() int { return len(m) }

func (m MessageRespSlice) Swap(i, j int) { m[i], m[j] = m[j], m[i] }

func (m MessageRespSlice) Less(i, j int) bool { return m[i].MessageSeq < m[j].MessageSeq }

// 重试消息
type RetryMessage struct {
	RecvPacket  *wkproto.RecvPacket // 接受包数据
	ChannelId   string              // 频道id
	ChannelType uint8               // 频道类型
	Uid         string              // 用户id
	FromNode    uint64              // 来源节点
	ConnId      int64               // 需要接受的连接id
	MessageId   int64               // 消息id
	Retry       int                 // 重试次数
	Index       int                 //在切片中的索引值
	Pri         int64               // 优先级的时间点 值越小越优先
}
