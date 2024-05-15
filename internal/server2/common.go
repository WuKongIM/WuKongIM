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

type ChannelActionType int

const (
	// ChannelActionSend 发送
	ChannelActionSend ChannelActionType = iota
	// ChannelActionPermission 权限判断
	ChannelActionPermission
	// ChannelActionPermissionResp 权限判断返回
	ChannelActionPermissionResp
	// ChannelActionStorage 存储消息
	ChannelActionStorage
	// ChannelActionTypeStorageResp 存储消息返回
	ChannelActionStorageResp
	// ChannelActionDeliver 消息投递
	ChannelActionDeliver
	// ChannelActionDeliverResp 消息投递返回
	ChannelActionDeliverResp
	ChannelActionSendack     // 发送ack
	ChannelActionSendackResp // 发送ack返回
	ChannelActionJoin        // 加入频道
	ChannelActionLeave       // 离开频道

)

func (c ChannelActionType) String() string {
	switch c {
	case ChannelActionSend:
		return "ChannelActionSend"
	case ChannelActionPermission:
		return "ChannelActionPermission"
	case ChannelActionPermissionResp:
		return "ChannelActionPermissionResp"
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
	case ChannelActionSendackResp:
		return "ChannelActionSendackResp"
	case ChannelActionJoin:
		return "ChannelActionJoin"
	case ChannelActionLeave:
		return "ChannelActionLeave"
	}
	return "unknow"
}

type UserActionType int

const (
	UserActionTypeNone    UserActionType = iota
	UserActionProcess                    // 处理消息
	UserActionProcessResp                // 处理消息返回
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

type channelRole int

const (
	channelRoleUnknow = iota
	channelRoleLeader // 领导
	channelRoleProxy  // 代理
)

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
