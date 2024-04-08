package trace

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

type clusterMetrics struct {
	wklog.Log
	ctx  context.Context
	opts *Options
	// message
	messageIncomingBytes metric.Int64Counter
	messageOutgoingBytes metric.Int64Counter
	messageIncomingCount metric.Int64Counter
	messageOutgoingCount metric.Int64Counter

	messageConcurrency metric.Int64UpDownCounter

	// sendPacket
	sendPacketIncomingBytes metric.Int64Counter
	sendPacketIncomingCount metric.Int64Counter
	sendPacketOutgoingBytes metric.Int64Counter
	sendPacketOutgoingCount metric.Int64Counter

	// channel log
	channelLogIncomingBytes metric.Int64Counter
	channelLogIncomingCount metric.Int64Counter
	channelLogOutgoingBytes metric.Int64Counter
	channelLogOutgoingCount metric.Int64Counter

	// msg sync
	msgSyncIncomingBytes        metric.Int64Counter
	msgSyncOutgoingBytes        metric.Int64Counter
	msgSyncIncomingCount        metric.Int64Counter
	msgSyncOutgoingCount        metric.Int64Counter
	msgSyncRespIncomingBytes    metric.Int64Counter
	msgSyncRespOutgoingBytes    metric.Int64Counter
	msgSyncRespIncomingCount    metric.Int64Counter
	msgSyncRespOutgoingCount    metric.Int64Counter
	channelMsgSyncIncomingCount metric.Int64Counter
	channelMsgSyncOutgoingCount metric.Int64Counter
	channelMsgSyncIncomingBytes metric.Int64Counter
	channelMsgSyncOutgoingBytes metric.Int64Counter
	slotMsgSyncIncomingCount    metric.Int64Counter
	slotMsgSyncOutgoingCount    metric.Int64Counter
	slotMsgSyncIncomingBytes    metric.Int64Counter
	slotMsgSyncOutgoingBytes    metric.Int64Counter

	// cluster ping
	clusterPingIncomingBytes metric.Int64Counter
	clusterPingIncomingCount metric.Int64Counter
	clusterPingOutgoingBytes metric.Int64Counter
	clusterPingOutgoingCount metric.Int64Counter

	// cluster pong
	clusterPongIncomingBytes metric.Int64Counter
	clusterPongIncomingCount metric.Int64Counter
	clusterPongOutgoingBytes metric.Int64Counter
	clusterPongOutgoingCount metric.Int64Counter

	// inbound flight
	inboundFlightMessageCount metric.Int64UpDownCounter
	inboundFlightMessageBytes metric.Int64UpDownCounter
}

func newClusterMetrics(opts *Options) IClusterMetrics {
	c := &clusterMetrics{
		Log:  wklog.NewWKLog("clusterMetrics"),
		ctx:  context.Background(),
		opts: opts,
	}

	// message
	c.messageIncomingBytes = NewInt64Counter("cluster_message_incoming_bytes")
	c.messageIncomingCount = NewInt64Counter("cluster_message_incoming_count")

	c.messageOutgoingBytes = NewInt64Counter("cluster_message_outgoing_bytes")
	c.messageOutgoingCount = NewInt64Counter("cluster_message_outgoing_count")

	c.messageConcurrency = NewInt64UpDownCounter("cluster_message_concurrency")

	// sendpacket
	c.sendPacketIncomingBytes = NewInt64Counter("cluster_sendpacket_incoming_bytes")
	c.sendPacketIncomingCount = NewInt64Counter("cluster_sendpacket_incoming_count")
	c.sendPacketOutgoingBytes = NewInt64Counter("cluster_sendpacket_outgoing_bytes")
	c.sendPacketOutgoingCount = NewInt64Counter("cluster_sendpacket_outgoing_count")

	// channel log
	c.channelLogIncomingBytes = NewInt64Counter("cluster_channel_log_incoming_bytes")
	c.channelLogIncomingCount = NewInt64Counter("cluster_channel_log_incoming_count")
	c.channelLogOutgoingBytes = NewInt64Counter("cluster_channel_log_outgoing_bytes")
	c.channelLogOutgoingCount = NewInt64Counter("cluster_channel_log_outgoing_count")

	// msg sync
	c.msgSyncIncomingBytes = NewInt64Counter("cluster_msg_sync_incoming_bytes")
	c.msgSyncOutgoingBytes = NewInt64Counter("cluster_msg_sync_outgoing_bytes")
	c.msgSyncIncomingCount = NewInt64Counter("cluster_msg_sync_incoming_count")
	c.msgSyncOutgoingCount = NewInt64Counter("cluster_msg_sync_outgoing_count")
	c.msgSyncRespIncomingBytes = NewInt64Counter("cluster_msg_syncresp_Incoming_bytes")
	c.msgSyncRespOutgoingBytes = NewInt64Counter("cluster_msg_syncresp_outgoing_bytes")
	c.msgSyncRespIncomingCount = NewInt64Counter("cluster_msg_syncresp_Incoming_count")
	c.msgSyncRespOutgoingCount = NewInt64Counter("cluster_msg_syncresp_outgoing_count")
	c.channelMsgSyncIncomingBytes = NewInt64Counter("cluster_channel_msg_sync_incoming_bytes")
	c.channelMsgSyncOutgoingBytes = NewInt64Counter("cluster_channel_msg_sync_outgoing_bytes")
	c.channelMsgSyncIncomingCount = NewInt64Counter("cluster_channel_msg_sync_incoming_count")
	c.channelMsgSyncOutgoingCount = NewInt64Counter("cluster_channel_msg_sync_outgoing_count")
	c.slotMsgSyncIncomingBytes = NewInt64Counter("cluster_slot_msg_sync_incoming_bytes")
	c.slotMsgSyncOutgoingBytes = NewInt64Counter("cluster_slot_msg_sync_outgoing_bytes")
	c.slotMsgSyncIncomingCount = NewInt64Counter("cluster_slot_msg_sync_incoming_count")
	c.slotMsgSyncOutgoingCount = NewInt64Counter("cluster_slot_msg_sync_outgoing_count")

	// cluster ping
	c.clusterPingIncomingBytes = NewInt64Counter("cluster_clusterping_incoming_bytes")
	c.clusterPingIncomingCount = NewInt64Counter("cluster_clusterping_incoming_count")
	c.clusterPingOutgoingBytes = NewInt64Counter("cluster_clusterping_outgoing_bytes")
	c.clusterPingOutgoingCount = NewInt64Counter("cluster_clusterping_outgoing_count")

	// cluster pong
	c.clusterPongIncomingBytes = NewInt64Counter("cluster_clusterpong_incoming_bytes")
	c.clusterPongIncomingCount = NewInt64Counter("cluster_clusterpong_incoming_count")
	c.clusterPongOutgoingBytes = NewInt64Counter("cluster_clusterpong_outgoing_bytes")
	c.clusterPongOutgoingCount = NewInt64Counter("cluster_clusterpong_outgoing_count")

	// RequestGoroutinePoolRunningCount
	var err error
	requestGoroutinePoolRunningCount, err := meter.Int64ObservableUpDownCounter("cluster_request_goroutine_pool_running_count")
	if err != nil {
		c.Panic("cluster_request_goroutine_pool_running_count error", zap.Error(err))
	}

	messageGoroutinePoolRunningCount, err := meter.Int64ObservableUpDownCounter("cluster_message_goroutine_pool_running_count")
	if err != nil {
		c.Panic("cluster_message_goroutine_pool_running_count error", zap.Error(err))
	}

	_, err = meter.RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(requestGoroutinePoolRunningCount, opts.RequestPoolRunning())
		obs.ObserveInt64(messageGoroutinePoolRunningCount, opts.MessagePoolRunning())
		return nil
	}, requestGoroutinePoolRunningCount, messageGoroutinePoolRunningCount)
	if err != nil {
		c.Panic("register callback error", zap.Error(err))
	}

	// inbound flight
	c.inboundFlightMessageCount = NewInt64UpDownCounter("cluster_inbound_flight_message_count")
	c.inboundFlightMessageBytes = NewInt64UpDownCounter("cluster_inbound_flight_message_bytes")

	return c
}

func (c *clusterMetrics) MessageIncomingBytesAdd(v int64) {
	c.messageIncomingBytes.Add(c.ctx, v)
}
func (c *clusterMetrics) MessageOutgoingBytesAdd(v int64) {
	c.messageOutgoingBytes.Add(c.ctx, v)
}
func (c *clusterMetrics) MessageIncomingCountAdd(v int64) {
	c.messageIncomingCount.Add(c.ctx, v)
}
func (c *clusterMetrics) MessageOutgoingCountAdd(v int64) {
	c.messageOutgoingCount.Add(c.ctx, v)
}

func (c *clusterMetrics) MessageConcurrencyAdd(v int64) {
	c.messageConcurrency.Add(c.ctx, v)
}

func (c *clusterMetrics) SendPacketIncomingBytesAdd(v int64) {
	c.sendPacketIncomingBytes.Add(c.ctx, v)
}

func (c *clusterMetrics) SendPacketOutgoingBytesAdd(v int64) {
	c.sendPacketOutgoingBytes.Add(c.ctx, v)
}

func (c *clusterMetrics) SendPacketIncomingCountAdd(v int64) {
	c.sendPacketIncomingCount.Add(c.ctx, v)
}

func (c *clusterMetrics) SendPacketOutgoingCountAdd(v int64) {
	c.sendPacketOutgoingCount.Add(c.ctx, v)
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
	c.clusterPongIncomingBytes.Add(c.ctx, v)

}
func (c *clusterMetrics) MsgClusterPongIncomingCountAdd(kind ClusterKind, v int64) {
	c.clusterPongIncomingCount.Add(c.ctx, v)
}

func (c *clusterMetrics) MsgClusterPongOutgoingBytesAdd(kind ClusterKind, v int64) {
	c.clusterPongOutgoingBytes.Add(c.ctx, v)
}
func (c *clusterMetrics) MsgClusterPongOutgoingCountAdd(kind ClusterKind, v int64) {
	c.clusterPongOutgoingCount.Add(c.ctx, v)
}

func (c *clusterMetrics) MsgClusterPingIncomingBytesAdd(kind ClusterKind, v int64) {
	c.clusterPingIncomingBytes.Add(c.ctx, v)

}
func (c *clusterMetrics) MsgClusterPingIncomingCountAdd(kind ClusterKind, v int64) {
	c.clusterPingIncomingCount.Add(c.ctx, v)
}

func (c *clusterMetrics) MsgClusterPingOutgoingBytesAdd(kind ClusterKind, v int64) {
	c.clusterPingOutgoingBytes.Add(c.ctx, v)
}

func (c *clusterMetrics) MsgClusterPingOutgoingCountAdd(kind ClusterKind, v int64) {
	c.clusterPingOutgoingCount.Add(c.ctx, v)
}

func (c *clusterMetrics) MsgSyncIncomingBytesAdd(kind ClusterKind, v int64) {
	c.msgSyncIncomingBytes.Add(c.ctx, v)

	switch kind {
	case ClusterKindChannel:
		c.channelMsgSyncIncomingBytes.Add(c.ctx, v)
	case ClusterKindSlot:
		c.slotMsgSyncIncomingBytes.Add(c.ctx, v)
	}
}

func (c *clusterMetrics) MsgSyncOutgoingBytesAdd(kind ClusterKind, v int64) {
	c.msgSyncOutgoingBytes.Add(c.ctx, v)

	switch kind {
	case ClusterKindChannel:
		c.channelMsgSyncOutgoingBytes.Add(c.ctx, v)
	case ClusterKindSlot:
		c.slotMsgSyncOutgoingBytes.Add(c.ctx, v)
	}
}

func (c *clusterMetrics) MsgSyncIncomingCountAdd(kind ClusterKind, v int64) {
	c.msgSyncIncomingCount.Add(c.ctx, v)

	switch kind {
	case ClusterKindChannel:
		c.channelMsgSyncIncomingCount.Add(c.ctx, v)
	case ClusterKindSlot:
		c.slotMsgSyncIncomingCount.Add(c.ctx, v)
	}
}

func (c *clusterMetrics) MsgSyncOutgoingCountAdd(kind ClusterKind, v int64) {
	c.msgSyncOutgoingCount.Add(c.ctx, v)

	switch kind {
	case ClusterKindChannel:
		c.channelMsgSyncOutgoingCount.Add(c.ctx, v)
	case ClusterKindSlot:
		c.slotMsgSyncOutgoingCount.Add(c.ctx, v)
	}
}

func (c *clusterMetrics) MsgSyncRespIncomingBytesAdd(kind ClusterKind, v int64) {
	c.msgSyncRespIncomingBytes.Add(c.ctx, v)
}
func (c *clusterMetrics) MsgSyncRespIncomingCountAdd(kind ClusterKind, v int64) {
	c.msgSyncRespIncomingCount.Add(c.ctx, v)
}

func (c *clusterMetrics) MsgSyncRespOutgoingBytesAdd(kind ClusterKind, v int64) {
	c.msgSyncRespOutgoingBytes.Add(c.ctx, v)
}
func (c *clusterMetrics) MsgSyncRespOutgoingCountAdd(kind ClusterKind, v int64) {
	c.msgSyncRespOutgoingCount.Add(c.ctx, v)
}

func (c *clusterMetrics) LogIncomingBytesAdd(kind ClusterKind, v int64) {
	c.channelLogIncomingBytes.Add(c.ctx, v)
}

func (c *clusterMetrics) LogIncomingCountAdd(kind ClusterKind, v int64) {
	c.channelLogIncomingCount.Add(c.ctx, v)
}

func (c *clusterMetrics) LogOutgoingBytesAdd(kind ClusterKind, v int64) {
	c.channelLogOutgoingBytes.Add(c.ctx, v)
}

func (c *clusterMetrics) LogOutgoingCountAdd(kind ClusterKind, v int64) {
	c.channelLogOutgoingCount.Add(c.ctx, v)
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

func (c *clusterMetrics) ChannelReplicaActiveCountAdd(v int64) {

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

// InboundFlightMessageCountAdd 入站飞行消息数量
func (c *clusterMetrics) InboundFlightMessageCountAdd(v int64) {
	c.inboundFlightMessageCount.Add(c.ctx, v)
}

// InboundFlightMessageBytesAdd 入站飞行消息流量
func (c *clusterMetrics) InboundFlightMessageBytesAdd(v int64) {
	c.inboundFlightMessageBytes.Add(c.ctx, v)
}
