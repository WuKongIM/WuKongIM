package server

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

type ClusterMsgType uint32

const (
	// 频道消息转发
	ClusterMsgTypeChannelForward ClusterMsgType = 1001
)

type channelRole int

const (
	channelRoleUnknow = iota
	channelRoleLeader // 领导 （领导负责频道数据的真实处理）
	channelRoleProxy  // 代理 （代理不处理数据，只将数据转发给领导）
)

type ChannelActionType int

const (
	ChannelActionUnknow ChannelActionType = iota
	// ChannelActionInit 频道初始化
	ChannelActionInit
	// ChannelActionInitResp 频道初始化返回
	ChannelActionInitResp
	// ChannelActionSend 发送
	ChannelActionSend

	// payload解密
	ChannelActionPayloadDecrypt
	ChannelActionPayloadDecryptResp

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
	// ChannelForward 转发消息给领导
	ChannelActionForward
	// ChannelActionForwardResp 转发消息给领导返回
	ChannelActionForwardResp
	ChannelActionLeaderChange // 领导变更
	ChannelActionSendack      // 发送ack
	ChannelActionJoin         // 加入频道
	ChannelActionLeave        // 离开频道

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

	}
	return "unknow"
}

type UserActionType int

const (
	UserActionTypeNone UserActionType = iota
	UserActionInit                    // 初始化
	UserActionInitResp                // 初始化返回
	UserActionSend                    // 发送消息
	UserActionPing                    // 发送ping消息
	UserActionPingResp
	UserActionRecvack            // 发送recvack消息
	UserActionRecvackResp        // 发送recvack消息返回
	UserActionForwardRecvack     // 转发recvack包给领导节点
	UserActionForwardRecvackResp // 转发返回
	UserActionRecv               // 接收消息
	UserActionRecvResp           // 接受消息返回
)

func (u UserActionType) String() string {
	switch u {
	case UserActionInit:
		return "UserActionInit"
	case UserActionSend:
		return "UserActionSend"
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
	case UserActionForwardRecvack:
		return "UserActionForwardRecvack"
	case UserActionForwardRecvackResp:
		return "UserActionForwardRecvackResp"

	}
	return "unknow"
}

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

func GetFromUIDAndToUIDWith(channelID string) (string, string) {
	channelIDs := strings.Split(channelID, "@")
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

func ChannelToKey(channelId string, channelType uint8) string {
	var builder strings.Builder
	builder.WriteString(strconv.Itoa(int(channelType)))
	builder.WriteString("-")
	builder.WriteString(channelId)
	return builder.String()

}

func ChannelFromlKey(channelKey string) (string, uint8) {
	channels := strings.Split(channelKey, "-")
	if len(channels) == 2 {
		channelTypeI, _ := strconv.Atoi(channels[0])
		return channels[1], uint8(channelTypeI)
	} else if len(channels) > 2 {
		channelTypeI, _ := strconv.Atoi(channels[0])
		return strings.Join(channels[1:], ""), uint8(channelTypeI)

	}
	return "", 0
}

func parseAddr(addr string) (string, int64) {
	addrPairs := strings.Split(addr, ":")
	if len(addrPairs) < 2 {
		return "", 0
	}
	portInt64, _ := strconv.ParseInt(addrPairs[len(addrPairs)-1], 10, 64)
	return addrPairs[0], portInt64
}

func BindJSON(obj any, c *wkhttp.Context) ([]byte, error) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, err
	}
	if err := wkutil.ReadJSONByByte(bodyBytes, obj); err != nil {
		return nil, err
	}
	return bodyBytes, nil
}

// 频道状态
type channelStatus int

const (
	channelStatusUninitialized channelStatus = iota // 未初始化
	channelStatusInitializing                       // 初始化中
	channelStatusInitialized                        // 初始化完成
)

// 用户状态
type userStatus int

const (
	userStatusUninitialized userStatus = iota // 未初始化
	userStatusInitializing                    // 初始化中
	userStatusInitialized                     // 初始化完成
)

type userRole int

const (
	userRoleUnknow = iota
	userRoleLeader // 领导 （领导负责用户数据的真实处理）
	userRoleProxy  // 代理 （代理不处理逻辑，只将数据转发给领导）
)
