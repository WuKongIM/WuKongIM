package cluster

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	errCircuitBreakerNotReady error = fmt.Errorf("circuit breaker not ready")
	errRateLimited                  = fmt.Errorf("rate limited")
	errChanIsFull                   = fmt.Errorf("channel is full")
)

func SlotIdToKey(slotId uint32) string {
	return strconv.FormatUint(uint64(slotId), 10)
}

func ChannelToKey(channelId string, channelType uint8) string {
	var builder strings.Builder
	builder.WriteString(strconv.Itoa(int(channelType)))
	builder.WriteString("#")
	builder.WriteString(channelId)
	return builder.String()

}

func ChannelFromlKey(channelKey string) (string, uint8) {
	channels := strings.Split(channelKey, "#")
	if len(channels) == 2 {
		channelTypeI, _ := strconv.Atoi(channels[0])
		return channels[1], uint8(channelTypeI)
	} else if len(channels) > 2 {
		channelTypeI, _ := strconv.Atoi(channels[0])
		return strings.Join(channels[1:], ""), uint8(channelTypeI)

	}
	return "", 0
}

// 分区类型
type ShardType uint8

const (
	ShardTypeUnknown ShardType = iota // 未知
	ShardTypeSlot                     // slot分区
	ShardTypeChannel                  // channel分区
	ShardTypeConfig                   // 配置
)

const (
	MsgTypeUnknown uint32 = iota
	MsgTypeSlot
	MsgTypeChannel
	MsgTypeConfig
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
