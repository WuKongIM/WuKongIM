package server

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

type ClusterMsgType uint32

const (
	// 节点ping
	ClusterMsgTypeNodePing ClusterMsgType = 1001
	// 节点Pong
	ClusterMsgTypeNodePong ClusterMsgType = 1002
)

type channelRole int

const (
	channelRoleUnknown = iota
	channelRoleLeader  // 领导 （领导负责频道数据的真实处理）
	channelRoleProxy   // 代理 （代理不处理数据，只将数据转发给领导）
)

type ChannelActionType int

const (
	ChannelActionUnknown ChannelActionType = iota
	// ChannelActionInit 频道初始化
	ChannelActionInit
	// ChannelActionInitResp 频道初始化返回
	ChannelActionInitResp
	// ChannelActionSend 发送
	ChannelActionSend
	// payload解密
	ChannelActionPayloadDecrypt
	ChannelActionPayloadDecryptResp

	// 流消息payload解密
	ChannelActionStreamPayloadDecrypt
	ChannelActionStreamPayloadDecryptResp

	// ChannelActionPermissionCheck 权限检查
	ChannelActionPermissionCheck
	// ChannelActionPermissionCheckResp 权限判断返回
	ChannelActionPermissionCheckResp
	// ChannelActionStorage 存储消息
	ChannelActionStorage
	// ChannelActionTypeStorageResp 存储消息返回
	ChannelActionStorageResp
	// ChannelActionDeliver 消息投递
	ChannelActionDeliver
	// ChannelActionDeliverResp 消息投递返回
	ChannelActionDeliverResp

	// 流消息投递
	ChannelActionStreamDeliver
	ChannelActionStreamDeliverResp

	// ChannelForward 转发消息给领导
	ChannelActionForward
	// ChannelActionForwardResp 转发消息给领导返回
	ChannelActionForwardResp

	// 流消息转发
	ChannelActionStreamForward
	ChannelActionStreamForwardResp

	ChannelActionLeaderChange // 领导变更
	ChannelActionSendack      // 发送ack
	ChannelActionSendackResp  // 发送ack返回
	ChannelActionJoin         // 加入频道
	ChannelActionLeave        // 离开频道
	ChannelActionClose        // 关闭频道
	ChannelActionCheckTag     // 定时检查tag的有效性

)

func (c ChannelActionType) String() string {
	switch c {
	case ChannelActionInit:
		return "ChannelActionInit"
	case ChannelActionSend:
		return "ChannelActionSend"
	case ChannelActionPermissionCheck:
		return "ChannelActionPermissionCheck"
	case ChannelActionPermissionCheckResp:
		return "ChannelActionPermissionCheckResp"
	case ChannelActionStorage:
		return "ChannelActionStorage"
	case ChannelActionStorageResp:
		return "ChannelActionStorageResp"
	case ChannelActionDeliver:
		return "ChannelActionDeliver"
	case ChannelActionDeliverResp:
		return "ChannelActionDeliverResp"
	case ChannelActionSendack:
		return "ChannelActionSendack"
	case ChannelActionJoin:
		return "ChannelActionJoin"
	case ChannelActionLeave:
		return "ChannelActionLeave"
	case ChannelActionForward:
		return "ChannelActionForward"
	case ChannelActionForwardResp:
		return "ChannelActionForwardResp"
	case ChannelActionLeaderChange:
		return "ChannelActionLeaderChange"
	case ChannelActionPayloadDecrypt:
		return "ChannelActionPayloadDecrypt"
	case ChannelActionPayloadDecryptResp:
		return "ChannelActionPayloadDecryptResp"
	case ChannelActionSendackResp:
		return "ChannelActionSendackResp"
	case ChannelActionInitResp:
		return "ChannelActionInitResp"
	case ChannelActionClose:
		return "ChannelActionClose"
	case ChannelActionCheckTag:
		return "ChannelActionCheckTag"

	}
	return fmt.Sprintf("Unknow(%d)", c)
}

type UserActionType uint8

const (
	UserActionTypeNone UserActionType = iota
	UserActionInit                    // 初始化
	UserActionInitResp                // 初始化返回
	UserActionConnect                 // 连接

	UserActionAuth     // 认证
	UserActionAuthResp // 认证返回

	UserActionSend // 发送消息
	UserActionPing // 发送ping消息
	UserActionPingResp
	UserActionRecvack            // 发送recvack消息
	UserActionRecvackResp        // 发送recvack消息返回
	UserActionForwardRecvackResp // 转发返回
	UserActionRecv               // 接收消息
	UserActionRecvResp           // 接受消息返回

	UserActionForward     // 转发action
	UserActionForwardResp // 转发action返回

	UserActionLeaderChange // 领导变更

	UserActionNodePing         // 用户节点ping, 用户的领导发送给追随者的ping
	UserActionNodePong         // 用户节点pong, 用户的追随者返回给领导的pong
	UserActionProxyNodeTimeout // 代理节点超时

	UserActionClose //关闭

	UserActionCheckLeader // 检查领导

)

func (u UserActionType) String() string {
	switch u {
	case UserActionInit:
		return "UserActionInit"
	case UserActionSend:
		return "UserActionSend"
	case UserActionInitResp:
		return "UserActionInitResp"
	case UserActionPing:
		return "UserActionPing"
	case UserActionPingResp:
		return "UserActionPingResp"
	case UserActionRecvack:
		return "UserActionRecvack"
	case UserActionRecvackResp:
		return "UserActionRecvackResp"
	case UserActionRecv:
		return "UserActionRecv"
	case UserActionRecvResp:
		return "UserActionRecvResp"
	case UserActionForward:
		return "UserActionForward"
	case UserActionForwardRecvackResp:
		return "UserActionForwardRecvackResp"
	case UserActionForwardResp:
		return "UserActionForwardResp"
	case UserActionLeaderChange:
		return "UserActionLeaderChange"
	case UserActionConnect:
		return "UserActionConnect"
	case UserActionAuth:
		return "UserActionAuth"
	case UserActionAuthResp:
		return "UserActionAuthResp"
	case UserActionNodePing:
		return "UserActionNodePing"
	case UserActionNodePong:
		return "UserActionNodePong"
	case UserActionProxyNodeTimeout:
		return "UserActionProxyNodeTimeout"
	case UserActionClose:
		return "UserActionClose"
	case UserActionCheckLeader:
		return "UserActionCheckLeader"

	}
	return "unknow"
}

type StreamActionType uint8

const (
	StreamActionTypeNone StreamActionType = iota
)

// GetFakeChannelIDWith GetFakeChannelIDWith
func GetFakeChannelIDWith(fromUID, toUID string) string {
	// TODO：这里可能会出现相等的情况 ，如果相等可以截取一部分再做hash直到不相等，后续完善
	fromUIDHash := wkutil.HashCrc32(fromUID)
	toUIDHash := wkutil.HashCrc32(toUID)
	if fromUIDHash > toUIDHash {
		return fmt.Sprintf("%s@%s", fromUID, toUID)
	}
	if fromUID != toUID && fromUIDHash == toUIDHash {
		wklog.Warn("生成的fromUID的Hash和toUID的Hash是相同的！！", zap.Uint32("fromUIDHash", fromUIDHash), zap.Uint32("toUIDHash", toUIDHash), zap.String("fromUID", fromUID), zap.String("toUID", toUID))

	}
	return fmt.Sprintf("%s@%s", toUID, fromUID)
}

func GetFromUIDAndToUIDWith(channelId string) (string, string) {
	channelIDs := strings.Split(channelId, "@")
	if len(channelIDs) == 2 {
		return channelIDs[0], channelIDs[1]
	}
	return "", ""
}

// GetCommunityTopicParentChannelID 获取社区话题频道的父频道ID
func GetCommunityTopicParentChannelID(channelID string) string {
	channelIDs := strings.Split(channelID, "@")
	if len(channelIDs) == 2 {
		return channelIDs[0]
	}
	return ""
}

type Reason int

const (
	ReasonNone Reason = iota
	ReasonSuccess
	ReasonError
	ReasonTimeout
)

func parseAddr(addr string) (string, int64) {
	addrPairs := strings.Split(addr, ":")
	if len(addrPairs) < 2 {
		return "", 0
	}
	portInt64, _ := strconv.ParseInt(addrPairs[len(addrPairs)-1], 10, 64)
	return addrPairs[0], portInt64
}
