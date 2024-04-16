package cluster

import (
	"fmt"
	"strconv"
	"strings"
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
	builder.WriteByte(channelType)
	builder.WriteString("@")
	builder.WriteString(channelId)
	return builder.String()

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
