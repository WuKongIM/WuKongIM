package server

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

var (
	gitCommit string
	VERSION   = "4.0.0" // 服务器版本
)

func myUptime(d time.Duration) string {
	// Just use total seconds for uptime, and display days / years
	tsecs := d / time.Second
	tmins := tsecs / 60
	thrs := tmins / 60
	tdays := thrs / 24
	tyrs := tdays / 365

	if tyrs > 0 {
		return fmt.Sprintf("%dy%dd%dh%dm%ds", tyrs, tdays%365, thrs%24, tmins%60, tsecs%60)
	}
	if tdays > 0 {
		return fmt.Sprintf("%dd%dh%dm%ds", tdays, thrs%24, tmins%60, tsecs%60)
	}
	if thrs > 0 {
		return fmt.Sprintf("%dh%dm%ds", thrs, tmins%60, tsecs%60)
	}
	if tmins > 0 {
		return fmt.Sprintf("%dm%ds", tmins, tsecs%60)
	}
	return fmt.Sprintf("%ds", tsecs)
}

const (
	aesKeyKey      = "aesKey"
	aesIVKey       = "aesIV"
	deviceLevelKey = "deviceLevel"
)

const (
	// ChannelTypePerson 个人频道
	ChannelTypePerson uint8 = 1
	// ChannelTypeGroup 群频道
	ChannelTypeGroup           uint8 = 2 // 群组频道
	ChannelTypeCustomerService uint8 = 3 // 客服频道
	ChannelTypeCommunity       uint8 = 4 // 社区频道
	ChannelTypeCommunityTopic  uint8 = 5 // 社区话题频道
	ChannelTypeInfo            uint8 = 6 // 资讯频道（有临时订阅者的概念，查看资讯的时候加入临时订阅，退出资讯的时候退出临时订阅）
)

// GetFakeChannelIDWith GetFakeChannelIDWith
func GetFakeChannelIDWith(fromUID, toUID string) string {
	// TODO：这里可能会出现相等的情况 ，如果相等可以截取一部分再做hash直到不相等，后续完善
	fromUIDHash := wkutil.HashCrc32(fromUID)
	toUIDHash := wkutil.HashCrc32(toUID)
	if fromUIDHash > toUIDHash {
		return fmt.Sprintf("%s@%s", fromUID, toUID)
	}
	if fromUIDHash == toUIDHash {
		wklog.Warn("生成的fromUID的Hash和toUID的Hash是相同的！！", zap.Uint32("fromUIDHash", fromUIDHash), zap.Uint32("toUIDHash", toUIDHash), zap.String("fromUID", fromUID), zap.String("toUID", toUID))
	}

	return fmt.Sprintf("%s@%s", toUID, fromUID)
}
