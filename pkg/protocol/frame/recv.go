package frame

import (
	"fmt"
	"strconv"

	"github.com/valyala/bytebufferpool"
)

type StreamFlag uint8

const (
	StreamFlagStart StreamFlag = 0 // 开始
	StreamFlagIng   StreamFlag = 1 // 进行中
	StreamFlagEnd   StreamFlag = 2 // 结束
)

// RecvPacket 收到消息的包
type RecvPacket struct {
	Framer
	Setting     Setting
	MsgKey      string // 用于验证此消息是否合法（仿中间人篡改）
	Expire      uint32 // 消息过期时间 0 表示永不过期
	MessageID   int64  // 服务端的消息ID(全局唯一)
	MessageSeq  uint64 // 消息序列号 （用户唯一，有序递增）
	ClientMsgNo string // 客户端唯一标示

	// 以下三个字段在5版本后不再支持
	StreamNo   string     // 流式编号
	StreamId   uint64     // 流式序列号
	StreamFlag StreamFlag // 流式标示

	Timestamp   int32  // 服务器消息时间戳(10位，到秒)
	ChannelID   string // 频道ID
	ChannelType uint8  // 频道类型
	Topic       string // 话题ID
	FromUID     string // 发送者UID
	Payload     []byte // 消息内容

	// ---------- 以下不参与编码 ------------
	ClientSeq uint64 // 客户端提供的序列号，在客户端内唯一
}

func (r *RecvPacket) Reset() {
	r.Framer.FrameType = UNKNOWN
	r.Framer.RemainingLength = 0
	r.Framer.NoPersist = false
	r.Framer.RedDot = false
	r.Framer.SyncOnce = false
	r.Framer.DUP = false
	r.Framer.HasServerVersion = false
	r.Framer.End = false
	r.Framer.FrameSize = 0
	r.Setting = 0
	r.MsgKey = ""
	r.Expire = 0
	r.MessageID = 0
	r.MessageSeq = 0
	r.ClientMsgNo = ""
	r.StreamNo = ""
	r.StreamId = 0
	r.StreamFlag = 0
	r.Timestamp = 0
	r.ChannelID = ""
	r.ChannelType = 0
	r.Topic = ""
	r.FromUID = ""
	r.Payload = nil
	r.ClientSeq = 0
}

// GetPacketType 获得包类型
func (r *RecvPacket) GetFrameType() FrameType {
	return RECV
}

func (r *RecvPacket) VerityString() string {
	// 从池中获取一个字节缓冲区
	buf := bytebufferpool.Get()
	defer bytebufferpool.Put(buf) // 使用完成后归还到池中
	buf.Reset()
	r.VerityBytes(buf)
	return string(buf.Bytes())
}

func (r *RecvPacket) VerityBytes(buf *bytebufferpool.ByteBuffer) {
	buf.B = strconv.AppendInt(buf.B, r.MessageID, 10)
	buf.B = strconv.AppendUint(buf.B, uint64(r.MessageSeq), 10)
	buf.B = append(buf.B, r.ClientMsgNo...)
	buf.B = strconv.AppendInt(buf.B, int64(r.Timestamp), 10)
	buf.B = append(buf.B, r.FromUID...)
	buf.B = append(buf.B, r.ChannelID...)
	buf.B = strconv.AppendInt(buf.B, int64(r.ChannelType), 10)
	buf.B = append(buf.B, r.Payload...)
}

func (r *RecvPacket) String() string {
	return fmt.Sprintf("recv Header:%s Setting:%d MessageID:%d MessageSeq:%d Timestamp:%d Expire:%d FromUid:%s ChannelID:%s ChannelType:%d Topic:%s Payload:%s", r.Framer, r.Setting, r.MessageID, r.MessageSeq, r.Timestamp, r.Expire, r.FromUID, r.ChannelID, r.ChannelType, r.Topic, string(r.Payload))
}
