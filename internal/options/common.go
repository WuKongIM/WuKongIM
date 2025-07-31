package options

import (
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

// GetFakeChannelIDWith GetFakeChannelIDWith
func GetFakeChannelIDWith(fromUID, toUID string) string {
	// TODO：这里可能会出现相等的情况 ，如果相等可以截取一部分再做hash直到不相等，后续完善
	fromUIDHash := wkutil.HashCrc32(fromUID)
	toUIDHash := wkutil.HashCrc32(toUID)
	if fromUIDHash > toUIDHash {
		return fromUID + "@" + toUID
	}
	if fromUID != toUID && fromUIDHash == toUIDHash {
		wklog.Warn("生成的fromUID的Hash和toUID的Hash是相同的！！", zap.Uint32("fromUIDHash", fromUIDHash), zap.Uint32("toUIDHash", toUIDHash), zap.String("fromUID", fromUID), zap.String("toUID", toUID))

	}
	return toUID + "@" + fromUID
}

func GetFromUIDAndToUIDWith(channelId string) (string, string) {
	channelIDs := strings.Split(channelId, "@")
	if len(channelIDs) == 2 {
		return channelIDs[0], channelIDs[1]
	}
	return "", ""
}

// GetAgentChannelIDWith 获取Agent频道ID
func GetAgentChannelIDWith(uid, agentUID string) string {
	return uid + "@" + agentUID
}

// GetUidAndAgentUIDWith 获取用户ID和AgentID
func GetUidAndAgentUIDWith(channelId string) (uid string, agentUID string) {
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

// 判断字符串是否存在特殊字符
func IsSpecialChar(s string) bool {
	return strings.Contains(s, "@") || strings.Contains(s, "#") || strings.Contains(s, "&")
}
