package frame

import "fmt"

// Framer 包的基础framer
type Framer struct {
	FrameType        FrameType
	RemainingLength  uint32 // 控制报文总长度等于固定报头的长度加上剩余长度
	NoPersist        bool   // 是否不持久化
	RedDot           bool   // 是否显示红点
	SyncOnce         bool   // 此消息只被同步或被消费一次
	DUP              bool   // 是否是重发消息
	HasServerVersion bool   // 是否有服务端版本 connack包用到
	End              bool   // 消息块：是否是最后一包
	FrameSize        int64
}

// GetFrameType GetFrameType
func (f Framer) GetFrameType() FrameType {
	return f.FrameType
}

func (f Framer) GetFrameSize() int64 {
	return f.FrameSize
}

// GetRemainingLength 包剩余长度
func (f Framer) GetRemainingLength() uint32 {
	return f.RemainingLength
}

// GetNoPersist 是否不持久化
func (f Framer) GetNoPersist() bool {
	return f.NoPersist
}

// GetRedDot 是否显示红点
func (f Framer) GetRedDot() bool {
	return f.RedDot
}

// GetsyncOnce 是否只被同步一次
func (f Framer) GetsyncOnce() bool {
	return f.SyncOnce
}

// GetDUP 是否是重发消息
func (f Framer) GetDUP() bool {
	return f.DUP
}

func (f Framer) GetHasServerVersion() bool {
	return f.HasServerVersion
}

func (f Framer) GetEnd() bool {
	return f.End
}

func (f Framer) String() string {
	return fmt.Sprintf("packetType: %s remainingLength:%d NoPersist:%v redDot:%v syncOnce:%v DUP:%v", f.GetFrameType().String(), f.RemainingLength, f.NoPersist, f.RedDot, f.SyncOnce, f.DUP)
}

// FrameType 包类型
type FrameType uint8

// 包类型
const (
	UNKNOWN    FrameType = iota // 保留位
	CONNECT                     // 客户端请求连接到服务器(c2s)
	CONNACK                     // 服务端收到连接请求后确认的报文(s2c)
	SEND                        // 发送消息(c2s)
	SENDACK                     // 收到消息确认的报文(s2c)
	RECV                        // 收取消息(s2c)
	RECVACK                     // 收取消息确认(c2s)
	PING                        // ping请求
	PONG                        // 对ping请求的相应
	DISCONNECT                  // 请求断开连接
	SUB                         // 订阅
	SUBACK                      // 订阅确认
	EVENT                       // 事件
)

func (p FrameType) String() string {
	switch p {
	case CONNECT:
		return "CONNECT"
	case CONNACK:
		return "CONNACK"
	case SEND:
		return "SEND"
	case SENDACK:
		return "SENDACK"
	case RECV:
		return "RECV"
	case RECVACK:
		return "RECVACK"
	case PING:
		return "PING"
	case PONG:
		return "PONG"
	case DISCONNECT:
		return "DISCONNECT"
	case SUB:
		return "SUB"
	case SUBACK:
		return "SUBACK"
	case EVENT:
		return "EVENT"
	}
	return fmt.Sprintf("UNKNOWN[%d]", p)
}

// ReasonCode 原因码
type ReasonCode uint8

const (
	// ReasonUnknown 未知错误
	ReasonUnknown ReasonCode = iota
	// ReasonSuccess 成功
	ReasonSuccess
	// ReasonAuthFail 认证失败
	ReasonAuthFail
	// ReasonSubscriberNotExist 订阅者在频道内不存在
	ReasonSubscriberNotExist
	// ReasonInBlacklist 在黑名单列表里
	ReasonInBlacklist
	// ReasonChannelNotExist 频道不存在
	ReasonChannelNotExist
	// ReasonUserNotOnNode 用户没在节点上
	ReasonUserNotOnNode
	// ReasonSenderOffline // 发送者离线了，这条消息将发不成功
	ReasonSenderOffline
	// ReasonMsgKeyError 消息key错误 说明消息不合法
	ReasonMsgKeyError
	// ReasonPayloadDecodeError payload解码失败
	ReasonPayloadDecodeError
	// ReasonForwardSendPacketError 转发发送包失败
	ReasonForwardSendPacketError
	// ReasonNotAllowSend 不允许发送消息
	ReasonNotAllowSend
	// ReasonConnectKick 连接被踢
	ReasonConnectKick
	// ReasonNotInWhitelist 没在白名单内
	ReasonNotInWhitelist
	// 查询用户token错误
	ReasonQueryTokenError
	// 系统错误
	ReasonSystemError
	// 错误的频道ID
	ReasonChannelIDError
	// ReasonNodeMatchError 节点匹配错误
	ReasonNodeMatchError
	// ReasonNodeNotMatch 节点不匹配
	ReasonNodeNotMatch
	ReasonBan                   // 频道被封禁
	ReasonNotSupportHeader      // 不支持的header
	ReasonClientKeyIsEmpty      // clientKey 是空的
	ReasonRateLimit             // 速率限制
	ReasonNotSupportChannelType // 不支持的频道类型
	ReasonDisband               // 频道已解散
	ReasonSendBan               // 发送被封禁
	ReasonChannelDeleting       // 频道删除中
	ReasonProtocolUpgradeRequired
	ReasonIdempotencyConflict
	ReasonMessageSeqExhausted
)

func (r ReasonCode) String() string {
	switch r {
	case ReasonUnknown:
		return "ReasonUnknown"
	case ReasonSuccess:
		return "ReasonSuccess"
	case ReasonAuthFail:
		return "ReasonAuthFail"
	case ReasonSubscriberNotExist:
		return "ReasonSubscriberNotExist"
	case ReasonNotAllowSend:
		return "ReasonNotAllowSend"
	case ReasonInBlacklist:
		return "ReasonInBlacklist"
	case ReasonChannelNotExist:
		return "ReasonChannelNotExist"
	case ReasonUserNotOnNode:
		return "ReasonUserNotOnNode"
	case ReasonSenderOffline:
		return "ReasonSenderOffline"
	case ReasonMsgKeyError:
		return "ReasonMsgKeyError"
	case ReasonPayloadDecodeError:
		return "ReasonPayloadDecodeError"
	case ReasonForwardSendPacketError:
		return "ReasonForwardSendPacketError"
	case ReasonConnectKick:
		return "ReasonConnectKick"
	case ReasonNotInWhitelist:
		return "ReasonNotInWhitelist"
	case ReasonQueryTokenError:
		return "ReasonQueryTokenError"
	case ReasonSystemError:
		return "ReasonSystemError"
	case ReasonChannelIDError:
		return "ReasonChannelIDError"
	case ReasonClientKeyIsEmpty:
		return "ReasonClientKeyIsEmpty"
	case ReasonRateLimit:
		return "ReasonRateLimit"
	case ReasonDisband:
		return "ReasonDisband"
	case ReasonNotSupportHeader:
		return "ReasonNotSupportHeader"
	case ReasonNodeMatchError:
		return "ReasonNodeMatchError"
	case ReasonNodeNotMatch:
		return "ReasonNodeNotMatch"
	case ReasonBan:
		return "ReasonBan"
	case ReasonNotSupportChannelType:
		return "ReasonNotSupportChannelType"
	case ReasonSendBan:
		return "ReasonSendBan"
	case ReasonChannelDeleting:
		return "ReasonChannelDeleting"
	case ReasonProtocolUpgradeRequired:
		return "ReasonProtocolUpgradeRequired"
	case ReasonIdempotencyConflict:
		return "ReasonIdempotencyConflict"
	case ReasonMessageSeqExhausted:
		return "ReasonMessageSeqExhausted"
	}
	return fmt.Sprintf("UNKNOWN[%d]", r)
}

// Byte 转换为byte
func (r ReasonCode) Byte() byte {
	return byte(r)
}

// DeviceFlag 设备类型
type DeviceFlag uint8

const (
	// APP APP
	APP DeviceFlag = iota
	// WEB WEB
	WEB = 1
	// PC PC
	PC = 2
	// SYSTEM 系统
	SYSTEM = 99
)

// DeviceLevel 设备等级
type DeviceLevel uint8

const (
	// DeviceLevelSlave 从设备
	DeviceLevelSlave DeviceLevel = 0
	// DeviceLevelMaster 主设备
	DeviceLevelMaster DeviceLevel = 1
)

func (r DeviceLevel) String() string {
	switch r {
	case DeviceLevelMaster:
		return "Master"
	case DeviceLevelSlave:
		return "Slave"
	}
	return fmt.Sprintf("Unknown[%d]", r)
}

// ToUint8 转换为uint8
func (r DeviceFlag) ToUint8() uint8 {
	return uint8(r)
}

func (r DeviceFlag) String() string {
	switch r {
	case APP:
		return "APP"
	case WEB:
		return "WEB"
	case SYSTEM:
		return "SYSTEM"
	}
	return fmt.Sprintf("%d", r)
}

// Frame 接口
type Frame interface {
	GetFrameType() FrameType
	GetRemainingLength() uint32
	// GetPersist 是否存储
	GetNoPersist() bool
	// GetRedDot 是否显示红点
	GetRedDot() bool
	// GetsyncOnce 是否只被同步一次
	GetsyncOnce() bool
	// 是否是重发的消息
	GetDUP() bool
	GetFrameSize() int64       // 总个frame的大小（不参与编码解码）
	GetHasServerVersion() bool // 是否有服务端版本 connack包用到

	GetEnd() bool
}

type Channel struct {
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
}

const (
	SettingByteSize          = 1 // setting固定大小
	StringFixLenByteSize     = 2 // 字符串可变大小
	ClientSeqByteSize        = 4 // clientSeq的大小
	ChannelTypeByteSize      = 1 // channelType的大小
	VersionByteSize          = 1 // version的大小
	DeviceFlagByteSize       = 1
	ClientTimestampByteSize  = 8
	TimeDiffByteSize         = 8
	ReasonCodeByteSize       = 1
	MessageIDByteSize        = 8
	MessageSeqLegacyByteSize = 4
	MessageSeqU64ByteSize    = 8
	MessageSeqByteSize       = MessageSeqU64ByteSize
	TimestampByteSize        = 4
	BigTimestampByteSize     = 8
	ActionByteSize           = 1
	StreamIdByteSize         = 8
	StreamFlagByteSize       = 1
	ExpireByteSize           = 4
	NodeIdByteSize           = 8
	ChunkIDByteSize          = 8
	EndReasonByteSize        = 1
)

const (
	// ChannelTypePerson 个人频道
	ChannelTypePerson uint8 = 1
	// ChannelTypeGroup 群频道
	ChannelTypeGroup uint8 = 2 // 群组频道
	// ChannelTypeCustomerService 客服频道
	ChannelTypeCustomerService uint8 = 3 // 客服频道
	// ChannelTypeCommunity 社区频道
	ChannelTypeCommunity uint8 = 4 // 社区频道
	// ChannelTypeCommunityTopic 社区话题频道
	ChannelTypeCommunityTopic uint8 = 5 // 社区话题频道
	// ChannelTypeInfo 资讯频道
	ChannelTypeInfo uint8 = 6 // 资讯频道（有临时订阅者的概念，查看资讯的时候加入临时订阅，退出资讯的时候退出临时订阅）
	// ChannelTypeData 数据频道
	ChannelTypeData uint8 = 7 // 数据频道
	// ChannelTypeTemp 临时频道
	ChannelTypeTemp uint8 = 8 // 临时频道
	// ChannelTypeLive 直播频道
	ChannelTypeLive uint8 = 9 // 直播频道(直播频道不会保存最近会话数据)
	// ChannelTypeVisitors 访客频道
	ChannelTypeVisitors uint8 = 10 // 访客频道 (频道id即为访客id，此频道只支持一个访客订阅者，多个客服订阅者，ChannelTypeCustomerService频道已过时，使用ChannelTypeVisitors代替)
	// ChannelTypeAgent 单聊Agent频道
	ChannelTypeAgent uint8 = 11 // 单聊Agent频道（AI Agent频道，频道ID内部结构为UID@AgentID的结构，类似单聊频道，此频道会针对于AI Agent场景做优化）
	// ChannelTypeAgentGroup 群聊Agent频道
	ChannelTypeAgentGroup uint8 = 12 // 群聊Agent频道（AI Agent群聊频道，类似群聊频道，此频道会针对于多Agent协同场景做优化）
)

const (
	LegacyMessageSeqVersion = 5
	MessageSeqU64Version    = 6
	// LatestVersion 最新版本
	LatestVersion = MessageSeqU64Version
)
