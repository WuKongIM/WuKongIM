package trace

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type clusterMetrics struct {
	wklog.Log
	ctx  context.Context
	opts *Options
	// message
	messageIncomingBytes atomic.Int64
	messageOutgoingBytes atomic.Int64
	messageIncomingCount atomic.Int64
	messageOutgoingCount atomic.Int64

	channelMsgIncomingBytes atomic.Int64
	channelMsgOutgoingBytes atomic.Int64
	channelMsgIncomingCount atomic.Int64
	channelMsgOutgoingCount atomic.Int64

	messageConcurrency atomic.Int64

	// sendPacket
	sendPacketIncomingBytes atomic.Int64
	sendPacketIncomingCount atomic.Int64
	sendPacketOutgoingBytes atomic.Int64
	sendPacketOutgoingCount atomic.Int64

	// channel
	channelActiveCount metric.Int64UpDownCounter

	// channel log
	channelLogIncomingBytes atomic.Int64
	channelLogIncomingCount atomic.Int64
	channelLogOutgoingBytes atomic.Int64
	channelLogOutgoingCount atomic.Int64

	// msg sync
	msgSyncIncomingBytes        atomic.Int64
	msgSyncOutgoingBytes        atomic.Int64
	msgSyncIncomingCount        atomic.Int64
	msgSyncOutgoingCount        atomic.Int64
	msgSyncRespIncomingBytes    atomic.Int64
	msgSyncRespOutgoingBytes    atomic.Int64
	msgSyncRespIncomingCount    atomic.Int64
	msgSyncRespOutgoingCount    atomic.Int64
	channelMsgSyncIncomingCount atomic.Int64
	channelMsgSyncOutgoingCount atomic.Int64
	channelMsgSyncIncomingBytes atomic.Int64
	channelMsgSyncOutgoingBytes atomic.Int64
	slotMsgSyncIncomingCount    atomic.Int64
	slotMsgSyncOutgoingCount    atomic.Int64
	slotMsgSyncIncomingBytes    atomic.Int64
	slotMsgSyncOutgoingBytes    atomic.Int64

	// cluster ping
	clusterPingIncomingBytes atomic.Int64
	clusterPingIncomingCount atomic.Int64
	clusterPingOutgoingBytes atomic.Int64
	clusterPingOutgoingCount atomic.Int64

	// cluster pong
	clusterPongIncomingBytes atomic.Int64
	clusterPongIncomingCount atomic.Int64
	clusterPongOutgoingBytes atomic.Int64
	clusterPongOutgoingCount atomic.Int64

	// inbound flight
	inboundFlightMessageCount metric.Int64UpDownCounter
	inboundFlightMessageBytes metric.Int64UpDownCounter

	// outbound flight
	outboundFlightMessageCount metric.Int64UpDownCounter
	outboundFlightMessageBytes metric.Int64UpDownCounter

	// propose
	channelProposeLatency           metric.Int64Histogram
	channelProposeCount             atomic.Int64 // 频道提案数量
	channelProposeFailedCount       atomic.Int64 // 频道提案失败数量
	channelProposeLatencyUnder500ms atomic.Int64 // 小于500ms的频道提案
	channelProposeLatencyOver500ms  atomic.Int64 // 超过500ms的频道提案

	slotProposeLatency metric.Int64Histogram

	// node
	observerNodeRequesting func() int64 // 节点请求中的数量
	observerNodeSending    func() int64 // 节点发送中的数量
}

func newClusterMetrics(opts *Options) IClusterMetrics {
	c := &clusterMetrics{
		Log:  wklog.NewWKLog("clusterMetrics"),
		ctx:  context.Background(),
		opts: opts,
	}

	// message
	msgIncomingBytes := NewInt64ObservableCounter("cluster_msg_incoming_bytes")
	msgOutgoingBytes := NewInt64ObservableCounter("cluster_msg_outgoing_bytes")
	msgIncomingCount := NewInt64ObservableCounter("cluster_msg_incoming_count")
	msgOutgoingCount := NewInt64ObservableCounter("cluster_msg_outgoing_count")

	// channel message
	channelMsgIncomingBytes := NewInt64ObservableCounter("cluster_channel_msg_incoming_bytes")
	channelMsgOutgoingBytes := NewInt64ObservableCounter("cluster_channel_msg_outgoing_bytes")
	channelMsgIncomingCount := NewInt64ObservableCounter("cluster_channel_msg_incoming_count")
	channelMsgOutgoingCount := NewInt64ObservableCounter("cluster_channel_msg_outgoing_count")

	messageConcurrency := NewInt64ObservableCounter("cluster_message_concurrency")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(msgIncomingBytes, c.messageIncomingBytes.Load())
		obs.ObserveInt64(msgOutgoingBytes, c.messageOutgoingBytes.Load())
		obs.ObserveInt64(msgIncomingCount, c.messageIncomingCount.Load())
		obs.ObserveInt64(msgOutgoingCount, c.messageOutgoingCount.Load())
		obs.ObserveInt64(messageConcurrency, c.messageConcurrency.Load())
		obs.ObserveInt64(channelMsgIncomingBytes, c.channelMsgIncomingBytes.Load())
		obs.ObserveInt64(channelMsgOutgoingBytes, c.channelMsgOutgoingBytes.Load())
		obs.ObserveInt64(channelMsgIncomingCount, c.channelMsgIncomingCount.Load())
		obs.ObserveInt64(channelMsgOutgoingCount, c.channelMsgOutgoingCount.Load())
		return nil
	}, msgIncomingBytes, msgOutgoingBytes, msgIncomingCount, msgOutgoingCount, messageConcurrency, channelMsgIncomingBytes, channelMsgOutgoingBytes, channelMsgIncomingCount, channelMsgOutgoingCount)

	// sendpacket
	sendPacketIncomingBytes := NewInt64ObservableCounter("cluster_sendpacket_incoming_bytes")
	sendPacketIncomingCount := NewInt64ObservableCounter("cluster_sendpacket_incoming_count")
	sendPacketOutgoingBytes := NewInt64ObservableCounter("cluster_sendpacket_outgoing_bytes")
	sendPacketOutgoingCount := NewInt64ObservableCounter("cluster_sendpacket_outgoing_count")
	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(sendPacketIncomingBytes, c.sendPacketIncomingBytes.Load())
		obs.ObserveInt64(sendPacketIncomingCount, c.sendPacketIncomingCount.Load())
		obs.ObserveInt64(sendPacketOutgoingBytes, c.sendPacketOutgoingBytes.Load())
		obs.ObserveInt64(sendPacketOutgoingCount, c.sendPacketOutgoingCount.Load())
		return nil
	}, sendPacketIncomingBytes, sendPacketIncomingCount, sendPacketOutgoingBytes, sendPacketOutgoingCount)
	// channel log
	channelLogIncomingBytes := NewInt64ObservableCounter("cluster_channel_log_incoming_bytes")
	channelLogIncomingCount := NewInt64ObservableCounter("cluster_channel_log_incoming_count")
	channelLogOutgoingBytes := NewInt64ObservableCounter("cluster_channel_log_outgoing_bytes")
	channelLogOutgoingCount := NewInt64ObservableCounter("cluster_channel_log_outgoing_count")

	c.channelActiveCount = NewInt64UpDownCounter("cluster_channel_active_count")
	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(channelLogIncomingBytes, c.channelLogIncomingBytes.Load())
		obs.ObserveInt64(channelLogIncomingCount, c.channelLogIncomingCount.Load())
		obs.ObserveInt64(channelLogOutgoingBytes, c.channelLogOutgoingBytes.Load())
		obs.ObserveInt64(channelLogOutgoingCount, c.channelLogOutgoingCount.Load())
		return nil
	}, channelLogIncomingBytes, channelLogIncomingCount, channelLogOutgoingBytes, channelLogOutgoingCount)

	// msg sync
	msgSyncIncomingBytes := NewInt64ObservableCounter("cluster_msg_sync_incoming_bytes")
	msgSyncOutgoingBytes := NewInt64ObservableCounter("cluster_msg_sync_outgoing_bytes")
	msgSyncIncomingCount := NewInt64ObservableCounter("cluster_msg_sync_incoming_count")
	msgSyncOutgoingCount := NewInt64ObservableCounter("cluster_msg_sync_outgoing_count")
	msgSyncRespIncomingBytes := NewInt64ObservableCounter("cluster_msg_syncresp_Incoming_bytes")
	msgSyncRespOutgoingBytes := NewInt64ObservableCounter("cluster_msg_syncresp_outgoing_bytes")
	msgSyncRespIncomingCount := NewInt64ObservableCounter("cluster_msg_syncresp_Incoming_count")
	msgSyncRespOutgoingCount := NewInt64ObservableCounter("cluster_msg_syncresp_outgoing_count")
	channelMsgSyncIncomingBytes := NewInt64ObservableCounter("cluster_channel_msg_sync_incoming_bytes")
	channelMsgSyncOutgoingBytes := NewInt64ObservableCounter("cluster_channel_msg_sync_outgoing_bytes")
	channelMsgSyncIncomingCount := NewInt64ObservableCounter("cluster_channel_msg_sync_incoming_count")
	channelMsgSyncOutgoingCount := NewInt64ObservableCounter("cluster_channel_msg_sync_outgoing_count")
	slotMsgSyncIncomingBytes := NewInt64ObservableCounter("cluster_slot_msg_sync_incoming_bytes")
	slotMsgSyncOutgoingBytes := NewInt64ObservableCounter("cluster_slot_msg_sync_outgoing_bytes")
	slotMsgSyncIncomingCount := NewInt64ObservableCounter("cluster_slot_msg_sync_incoming_count")
	slotMsgSyncOutgoingCount := NewInt64ObservableCounter("cluster_slot_msg_sync_outgoing_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(msgSyncIncomingBytes, c.msgSyncIncomingBytes.Load())
		obs.ObserveInt64(msgSyncOutgoingBytes, c.msgSyncOutgoingBytes.Load())
		obs.ObserveInt64(msgSyncIncomingCount, c.msgSyncIncomingCount.Load())
		obs.ObserveInt64(msgSyncOutgoingCount, c.msgSyncOutgoingCount.Load())
		obs.ObserveInt64(msgSyncRespIncomingBytes, c.msgSyncRespIncomingBytes.Load())
		obs.ObserveInt64(msgSyncRespOutgoingBytes, c.msgSyncRespOutgoingBytes.Load())
		obs.ObserveInt64(msgSyncRespIncomingCount, c.msgSyncRespIncomingCount.Load())
		obs.ObserveInt64(msgSyncRespOutgoingCount, c.msgSyncRespOutgoingCount.Load())
		obs.ObserveInt64(channelMsgSyncIncomingBytes, c.channelMsgSyncIncomingBytes.Load())
		obs.ObserveInt64(channelMsgSyncOutgoingBytes, c.channelMsgSyncOutgoingBytes.Load())
		obs.ObserveInt64(channelMsgSyncIncomingCount, c.channelMsgSyncIncomingCount.Load())
		obs.ObserveInt64(channelMsgSyncOutgoingCount, c.channelMsgSyncOutgoingCount.Load())
		obs.ObserveInt64(slotMsgSyncIncomingBytes, c.slotMsgSyncIncomingBytes.Load())
		obs.ObserveInt64(slotMsgSyncOutgoingBytes, c.slotMsgSyncOutgoingBytes.Load())
		obs.ObserveInt64(slotMsgSyncIncomingCount, c.slotMsgSyncIncomingCount.Load())
		obs.ObserveInt64(slotMsgSyncOutgoingCount, c.slotMsgSyncOutgoingCount.Load())
		return nil
	}, msgSyncIncomingBytes, msgSyncOutgoingBytes, msgSyncIncomingCount, msgSyncOutgoingCount, msgSyncRespIncomingBytes, msgSyncRespOutgoingBytes, msgSyncRespIncomingCount, msgSyncRespOutgoingCount, channelMsgSyncIncomingBytes, channelMsgSyncOutgoingBytes, channelMsgSyncIncomingCount, channelMsgSyncOutgoingCount, slotMsgSyncIncomingBytes, slotMsgSyncOutgoingBytes, slotMsgSyncIncomingCount, slotMsgSyncOutgoingCount)

	// cluster ping
	clusterPingIncomingBytes := NewInt64ObservableCounter("cluster_msg_ping_incoming_bytes")
	clusterPingIncomingCount := NewInt64ObservableCounter("cluster_msg_ping_incoming_count")
	clusterPingOutgoingBytes := NewInt64ObservableCounter("cluster_msg_ping_outgoing_bytes")
	clusterPingOutgoingCount := NewInt64ObservableCounter("cluster_msg_ping_outgoing_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(clusterPingIncomingBytes, c.clusterPingIncomingBytes.Load())
		obs.ObserveInt64(clusterPingIncomingCount, c.clusterPingIncomingCount.Load())
		obs.ObserveInt64(clusterPingOutgoingBytes, c.clusterPingOutgoingBytes.Load())
		obs.ObserveInt64(clusterPingOutgoingCount, c.clusterPingOutgoingCount.Load())
		return nil
	}, clusterPingIncomingBytes, clusterPingIncomingCount, clusterPingOutgoingBytes, clusterPingOutgoingCount)

	// cluster pong
	clusterPongIncomingBytes := NewInt64ObservableCounter("cluster_msg_pong_incoming_bytes")
	clusterPongIncomingCount := NewInt64ObservableCounter("cluster_msg_pong_incoming_count")
	clusterPongOutgoingBytes := NewInt64ObservableCounter("cluster_msg_pong_outgoing_bytes")
	clusterPongOutgoingCount := NewInt64ObservableCounter("cluster_msg_pong_outgoing_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(clusterPongIncomingBytes, c.clusterPongIncomingBytes.Load())
		obs.ObserveInt64(clusterPongIncomingCount, c.clusterPongIncomingCount.Load())
		obs.ObserveInt64(clusterPongOutgoingBytes, c.clusterPongOutgoingBytes.Load())
		obs.ObserveInt64(clusterPongOutgoingCount, c.clusterPongOutgoingCount.Load())

		return nil
	}, clusterPongIncomingBytes, clusterPongIncomingCount, clusterPongOutgoingBytes, clusterPongOutgoingCount)

	var err error
	// propose
	c.channelProposeLatency, err = meter.Int64Histogram(
		"cluster_channel_propose_latency",
	)
	if err != nil {
		c.Panic("cluster_channel_propose_latency error", zap.Error(err))
	}
	c.slotProposeLatency, err = meter.Int64Histogram(
		"cluster_slot_propose_latency",
	)
	if err != nil {
		c.Panic("cluster_slot_propose_latency error", zap.Error(err))

	}
	channelProposeCount := NewInt64ObservableCounter("cluster_channel_propose_count")
	channelProposeFailedCount := NewInt64ObservableCounter("cluster_channel_propose_failed_count")
	channelProposeLatencyOver500ms := NewInt64ObservableCounter("cluster_channel_propose_latency_over_500ms")
	channelProposeLatencyUnder500ms := NewInt64ObservableCounter("cluster_channel_propose_latency_under_500ms")
	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(channelProposeCount, c.channelProposeCount.Load())
		obs.ObserveInt64(channelProposeFailedCount, c.channelProposeFailedCount.Load())
		obs.ObserveInt64(channelProposeLatencyOver500ms, c.channelProposeLatencyOver500ms.Load())
		obs.ObserveInt64(channelProposeLatencyUnder500ms, c.channelProposeLatencyUnder500ms.Load())
		return nil
	}, channelProposeCount, channelProposeFailedCount, channelProposeLatencyUnder500ms, channelProposeLatencyOver500ms)

	// node
	nodeRequestingCount := NewInt64ObservableCounter("cluster_node_requesting_count")
	nodeSendingCount := NewInt64ObservableCounter("cluster_node_sending_count")
	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		if c.observerNodeRequesting != nil {
			obs.ObserveInt64(nodeRequestingCount, c.observerNodeRequesting())
		}

		if c.observerNodeSending != nil {
			obs.ObserveInt64(nodeSendingCount, c.observerNodeSending())
		}

		return nil
	}, nodeRequestingCount, nodeSendingCount)

	return c
}

func (c *clusterMetrics) MessageIncomingBytesAdd(kind ClusterKind, v int64) {
	c.messageIncomingBytes.Add(v)
	switch kind {
	case ClusterKindChannel:
		c.channelMsgIncomingBytes.Add(v)
	}
}
func (c *clusterMetrics) MessageOutgoingBytesAdd(kind ClusterKind, v int64) {
	c.messageOutgoingBytes.Add(v)
	switch kind {
	case ClusterKindChannel:
		c.channelMsgOutgoingBytes.Add(v)
	}
}
func (c *clusterMetrics) MessageIncomingCountAdd(kind ClusterKind, v int64) {
	c.messageIncomingCount.Add(v)
	switch kind {
	case ClusterKindChannel:
		c.channelMsgIncomingCount.Add(v)
	}
}
func (c *clusterMetrics) MessageOutgoingCountAdd(kind ClusterKind, v int64) {
	c.messageOutgoingCount.Add(v)
	switch kind {
	case ClusterKindChannel:
		c.channelMsgOutgoingCount.Add(v)
	}
}

func (c *clusterMetrics) MessageConcurrencyAdd(v int64) {
	c.messageConcurrency.Add(v)
}

func (c *clusterMetrics) SendPacketIncomingBytesAdd(v int64) {
	c.sendPacketIncomingBytes.Add(v)
}

func (c *clusterMetrics) SendPacketOutgoingBytesAdd(v int64) {
	c.sendPacketOutgoingBytes.Add(v)
}

func (c *clusterMetrics) SendPacketIncomingCountAdd(v int64) {
	c.sendPacketIncomingCount.Add(v)
}

func (c *clusterMetrics) SendPacketOutgoingCountAdd(v int64) {
	c.sendPacketOutgoingCount.Add(v)
}

func (c *clusterMetrics) RecvPacketIncomingBytesAdd(v int64) {

}

func (c *clusterMetrics) RecvPacketOutgoingBytesAdd(v int64) {

}

func (c *clusterMetrics) RecvPacketIncomingCountAdd(v int64) {

}

func (c *clusterMetrics) RecvPacketOutgoingCountAdd(v int64) {

}

func (c *clusterMetrics) MsgClusterPongIncomingBytesAdd(kind ClusterKind, v int64) {
	c.clusterPongIncomingBytes.Add(v)

}
func (c *clusterMetrics) MsgClusterPongIncomingCountAdd(kind ClusterKind, v int64) {
	c.clusterPongIncomingCount.Add(v)
}

func (c *clusterMetrics) MsgClusterPongOutgoingBytesAdd(kind ClusterKind, v int64) {
	c.clusterPongOutgoingBytes.Add(v)
}
func (c *clusterMetrics) MsgClusterPongOutgoingCountAdd(kind ClusterKind, v int64) {
	c.clusterPongOutgoingCount.Add(v)
}

func (c *clusterMetrics) MsgClusterPingIncomingBytesAdd(kind ClusterKind, v int64) {
	c.clusterPingIncomingBytes.Add(v)

}
func (c *clusterMetrics) MsgClusterPingIncomingCountAdd(kind ClusterKind, v int64) {
	c.clusterPingIncomingCount.Add(v)
}

func (c *clusterMetrics) MsgClusterPingOutgoingBytesAdd(kind ClusterKind, v int64) {
	c.clusterPingOutgoingBytes.Add(v)
}

func (c *clusterMetrics) MsgClusterPingOutgoingCountAdd(kind ClusterKind, v int64) {
	c.clusterPingOutgoingCount.Add(v)
}

func (c *clusterMetrics) MsgSyncIncomingBytesAdd(kind ClusterKind, v int64) {
	c.msgSyncIncomingBytes.Add(v)

	switch kind {
	case ClusterKindChannel:
		c.channelMsgSyncIncomingBytes.Add(v)
	case ClusterKindSlot:
		c.slotMsgSyncIncomingBytes.Add(v)
	}
}

func (c *clusterMetrics) MsgSyncOutgoingBytesAdd(kind ClusterKind, v int64) {
	c.msgSyncOutgoingBytes.Add(v)

	switch kind {
	case ClusterKindChannel:
		c.channelMsgSyncOutgoingBytes.Add(v)
	case ClusterKindSlot:
		c.slotMsgSyncOutgoingBytes.Add(v)
	}
}

func (c *clusterMetrics) MsgSyncIncomingCountAdd(kind ClusterKind, v int64) {
	c.msgSyncIncomingCount.Add(v)

	switch kind {
	case ClusterKindChannel:
		c.channelMsgSyncIncomingCount.Add(v)
	case ClusterKindSlot:
		c.slotMsgSyncIncomingCount.Add(v)
	}
}

func (c *clusterMetrics) MsgSyncOutgoingCountAdd(kind ClusterKind, v int64) {
	c.msgSyncOutgoingCount.Add(v)

	switch kind {
	case ClusterKindChannel:
		c.channelMsgSyncOutgoingCount.Add(v)
	case ClusterKindSlot:
		c.slotMsgSyncOutgoingCount.Add(v)
	}
}

func (c *clusterMetrics) MsgSyncRespIncomingBytesAdd(kind ClusterKind, v int64) {
	c.msgSyncRespIncomingBytes.Add(v)
}
func (c *clusterMetrics) MsgSyncRespIncomingCountAdd(kind ClusterKind, v int64) {
	c.msgSyncRespIncomingCount.Add(v)
}

func (c *clusterMetrics) MsgSyncRespOutgoingBytesAdd(kind ClusterKind, v int64) {
	c.msgSyncRespOutgoingBytes.Add(v)
}
func (c *clusterMetrics) MsgSyncRespOutgoingCountAdd(kind ClusterKind, v int64) {
	c.msgSyncRespOutgoingCount.Add(v)
}

func (c *clusterMetrics) LogIncomingBytesAdd(kind ClusterKind, v int64) {
	c.channelLogIncomingBytes.Add(v)
}

func (c *clusterMetrics) LogIncomingCountAdd(kind ClusterKind, v int64) {
	c.channelLogIncomingCount.Add(v)
}

func (c *clusterMetrics) LogOutgoingBytesAdd(kind ClusterKind, v int64) {
	c.channelLogOutgoingBytes.Add(v)
}

func (c *clusterMetrics) LogOutgoingCountAdd(kind ClusterKind, v int64) {
	c.channelLogOutgoingCount.Add(v)
}

func (c *clusterMetrics) MsgLeaderTermStartIndexReqIncomingBytesAdd(kind ClusterKind, v int64) {

}

func (c *clusterMetrics) MsgLeaderTermStartIndexReqIncomingCountAdd(kind ClusterKind, v int64) {

}

func (c *clusterMetrics) MsgLeaderTermStartIndexReqOutgoingBytesAdd(kind ClusterKind, v int64) {

}

func (c *clusterMetrics) MsgLeaderTermStartIndexReqOutgoingCountAdd(kind ClusterKind, v int64) {

}

func (c *clusterMetrics) MsgLeaderTermStartIndexRespIncomingBytesAdd(kind ClusterKind, v int64) {

}

func (c *clusterMetrics) MsgLeaderTermStartIndexRespIncomingCountAdd(kind ClusterKind, v int64) {

}

func (c *clusterMetrics) MsgLeaderTermStartIndexRespOutgoingBytesAdd(kind ClusterKind, v int64) {

}

func (c *clusterMetrics) MsgLeaderTermStartIndexRespOutgoingCountAdd(kind ClusterKind, v int64) {

}
func (c *clusterMetrics) ForwardProposeBytesAdd(v int64) {

}

func (c *clusterMetrics) ForwardProposeCountAdd(v int64) {

}

func (c *clusterMetrics) ForwardProposeRespBytesAdd(v int64) {

}

func (c *clusterMetrics) ForwardProposeRespCountAdd(v int64) {

}

func (c *clusterMetrics) ForwardConnPingBytesAdd(v int64) {

}

func (c *clusterMetrics) ForwardConnPingCountAdd(v int64) {

}

func (c *clusterMetrics) ForwardConnPongBytesAdd(v int64) {

}

func (c *clusterMetrics) ForwardConnPongCountAdd(v int64) {

}

func (c *clusterMetrics) ChannelActiveCountAdd(v int64) {
	c.channelActiveCount.Add(c.ctx, v)
}

func (c *clusterMetrics) ChannelElectionCountAdd(v int64) {

}

func (c *clusterMetrics) ChannelElectionSuccessCountAdd(v int64) {

}

func (c *clusterMetrics) ChannelElectionFailCountAdd(v int64) {

}

func (c *clusterMetrics) SlotElectionCountAdd(v int64) {

}

func (c *clusterMetrics) SlotElectionSuccessCountAdd(v int64) {

}

func (c *clusterMetrics) SlotElectionFailCountAdd(v int64) {

}

func (c *clusterMetrics) ProposeLatencyAdd(kind ClusterKind, v int64) {
	switch kind {
	case ClusterKindChannel:
		c.channelProposeLatency.Record(c.ctx, v)
		if v > 500 {
			c.channelProposeLatencyOver500ms.Add(1)
		} else {
			c.channelProposeLatencyUnder500ms.Add(1)
		}
		c.channelProposeCount.Add(1)
	case ClusterKindSlot:
		c.slotProposeLatency.Record(c.ctx, v)
	}
}

func (c *clusterMetrics) ProposeFailedCountAdd(kind ClusterKind, v int64) {
	switch kind {
	case ClusterKindChannel:
		c.channelProposeFailedCount.Add(v)
	case ClusterKindSlot:
	}
}

func (c *clusterMetrics) ObserverNodeRequesting(f func() int64) {
	c.observerNodeRequesting = f
}

func (c *clusterMetrics) ObserverNodeSending(f func() int64) {
	c.observerNodeSending = f
}
