package cluster

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
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

func traceOutgoingMessage(kind trace.ClusterKind, msgType replica.MsgType, size int64) {

	switch msgType {
	case replica.MsgSync:
		trace.GlobalTrace.Metrics.Cluster().MsgSyncOutgoingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgSyncOutgoingBytesAdd(kind, size)
	case replica.MsgPing:
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPingOutgoingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPingOutgoingBytesAdd(kind, size)
	case replica.MsgPong:
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPongOutgoingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPongOutgoingBytesAdd(kind, size)
	case replica.MsgLeaderTermStartIndexReq:
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexReqOutgoingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexReqOutgoingBytesAdd(kind, size)
	case replica.MsgLeaderTermStartIndexResp:
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexRespOutgoingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexRespOutgoingBytesAdd(kind, size)

	}

}

func traceIncomingMessage(kind trace.ClusterKind, msgType replica.MsgType, size int64) {
	switch msgType {
	case replica.MsgSync:
		trace.GlobalTrace.Metrics.Cluster().MsgSyncIncomingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgSyncIncomingBytesAdd(kind, size)

	case replica.MsgPing:
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPingIncomingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPingIncomingBytesAdd(kind, size)
	case replica.MsgPong:
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPongIncomingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPongIncomingBytesAdd(kind, size)
	case replica.MsgLeaderTermStartIndexReq:
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexReqIncomingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexReqIncomingBytesAdd(kind, size)
	case replica.MsgLeaderTermStartIndexResp:
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexRespIncomingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexRespIncomingBytesAdd(kind, size)

	}
}
