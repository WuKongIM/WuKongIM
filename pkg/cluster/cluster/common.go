package cluster

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
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

var (
	errCircuitBreakerNotReady error = fmt.Errorf("circuit breaker not ready")
	errRateLimited                  = fmt.Errorf("rate limited")
	errChanIsFull                   = fmt.Errorf("channel is full")
)

func traceOutgoingMessage(kind trace.ClusterKind, m replica.Message) {

	switch m.MsgType {
	case replica.MsgSyncResp:
		var logTotalSize int64
		for _, log := range m.Logs {
			logTotalSize += int64(log.LogSize())
		}
		trace.GlobalTrace.Metrics.Cluster().LogOutgoingCountAdd(kind, int64(len(m.Logs)))
		trace.GlobalTrace.Metrics.Cluster().LogOutgoingBytesAdd(kind, logTotalSize)
	case replica.MsgSync:
		trace.GlobalTrace.Metrics.Cluster().MsgSyncOutgoingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgSyncOutgoingBytesAdd(kind, int64(m.Size()))
	case replica.MsgPing:
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPingOutgoingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPingOutgoingBytesAdd(kind, int64(m.Size()))
	case replica.MsgPong:
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPongOutgoingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPongOutgoingBytesAdd(kind, int64(m.Size()))
	case replica.MsgLeaderTermStartIndexReq:
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexReqOutgoingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexReqOutgoingBytesAdd(kind, int64(m.Size()))
	case replica.MsgLeaderTermStartIndexResp:
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexRespOutgoingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexRespOutgoingBytesAdd(kind, int64(m.Size()))

	}

}

func traceIncomingMessage(kind trace.ClusterKind, m replica.Message) {
	switch m.MsgType {
	case replica.MsgSyncResp:
		var logTotalSize int64
		for _, log := range m.Logs {
			logTotalSize += int64(log.LogSize())
		}
		trace.GlobalTrace.Metrics.Cluster().LogIncomingCountAdd(kind, int64(len(m.Logs)))
		trace.GlobalTrace.Metrics.Cluster().LogIncomingBytesAdd(kind, logTotalSize)
	case replica.MsgSync:
		trace.GlobalTrace.Metrics.Cluster().MsgSyncIncomingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgSyncIncomingBytesAdd(kind, int64(m.Size()))

	case replica.MsgPing:
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPingIncomingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPingIncomingBytesAdd(kind, int64(m.Size()))
	case replica.MsgPong:
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPongIncomingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgClusterPongIncomingBytesAdd(kind, int64(m.Size()))
	case replica.MsgLeaderTermStartIndexReq:
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexReqIncomingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexReqIncomingBytesAdd(kind, int64(m.Size()))
	case replica.MsgLeaderTermStartIndexResp:
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexRespIncomingCountAdd(kind, 1)
		trace.GlobalTrace.Metrics.Cluster().MsgLeaderTermStartIndexRespIncomingBytesAdd(kind, int64(m.Size()))

	}
}
