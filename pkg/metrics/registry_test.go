package metrics

import (
	"sync"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestGatewayMetricsTrackConnectionAndTraffic(t *testing.T) {
	reg := New(1, "node-1")

	reg.Gateway.ConnectionOpened("tcp")
	reg.Gateway.Auth("ok", "none", 25*time.Millisecond)
	reg.Gateway.Auth("fail", "activation_error", 30*time.Millisecond)
	reg.Gateway.Auth("fail", "activation_route_not_ready", 32*time.Millisecond)
	reg.Gateway.Auth("fail", "raw error text with uid", 35*time.Millisecond)
	reg.Gateway.MessageReceived("tcp", 12)
	reg.Gateway.MessageDelivered("tcp", 18)
	reg.Gateway.FrameHandled("SEND", 5*time.Millisecond)
	reg.Gateway.SetAsyncSendQueue(3, 1024)
	reg.Gateway.ObserveAsyncSendDispatchWait("wkproto", 2*time.Millisecond)
	reg.Gateway.ObserveAsyncSendBatch(8, 256, 500*time.Microsecond)
	reg.Gateway.Sendack("system_error", "batch_result_error", "timeout")
	reg.Gateway.Sendack("surprising raw reason", "uid-leaking source", "uid-leaking error")
	reg.Gateway.ConnectionClosed("tcp")
	reg.Gateway.ConnectionClosedReason("tcp", "peer_closed")

	families, err := reg.Gather()
	require.NoError(t, err)

	active := requireMetricFamily(t, families, "wukongim_gateway_connections_active")
	require.Len(t, active.GetMetric(), 1)
	requireMetricLabels(t, active.GetMetric()[0], map[string]string{
		"node_id":   "1",
		"node_name": "node-1",
		"protocol":  "tcp",
	})
	require.Equal(t, float64(0), active.GetMetric()[0].GetGauge().GetValue())

	receivedBytes := requireMetricFamily(t, families, "wukongim_gateway_messages_received_bytes_total")
	require.Len(t, receivedBytes.GetMetric(), 1)
	requireMetricLabels(t, receivedBytes.GetMetric()[0], map[string]string{
		"node_id":   "1",
		"node_name": "node-1",
		"protocol":  "tcp",
	})
	require.Equal(t, float64(12), receivedBytes.GetMetric()[0].GetCounter().GetValue())

	closes := requireMetricFamily(t, families, "wukongim_gateway_connection_closes_total")
	require.Len(t, closes.GetMetric(), 1)
	requireMetricLabels(t, closes.GetMetric()[0], map[string]string{
		"node_id":   "1",
		"node_name": "node-1",
		"protocol":  "tcp",
		"reason":    "peer_closed",
	})
	require.Equal(t, float64(1), closes.GetMetric()[0].GetCounter().GetValue())

	deliveredTotal := requireMetricFamily(t, families, "wukongim_gateway_messages_delivered_total")
	require.Len(t, deliveredTotal.GetMetric(), 1)
	requireMetricLabels(t, deliveredTotal.GetMetric()[0], map[string]string{
		"node_id":   "1",
		"node_name": "node-1",
		"protocol":  "tcp",
	})
	require.Equal(t, float64(1), deliveredTotal.GetMetric()[0].GetCounter().GetValue())

	queueDepth := requireMetricFamily(t, families, "wukongim_gateway_async_send_queue_depth")
	require.Len(t, queueDepth.GetMetric(), 1)
	require.Equal(t, float64(3), queueDepth.GetMetric()[0].GetGauge().GetValue())

	queueCapacity := requireMetricFamily(t, families, "wukongim_gateway_async_send_queue_capacity")
	require.Len(t, queueCapacity.GetMetric(), 1)
	require.Equal(t, float64(1024), queueCapacity.GetMetric()[0].GetGauge().GetValue())

	wait := requireMetricFamily(t, families, "wukongim_gateway_async_send_dispatch_wait_duration_seconds")
	require.Len(t, wait.GetMetric(), 1)
	requireMetricLabels(t, wait.GetMetric()[0], map[string]string{
		"node_id":   "1",
		"node_name": "node-1",
		"protocol":  "wkproto",
	})

	authTotal := requireMetricFamily(t, families, "wukongim_gateway_auth_total")
	require.Equal(t, float64(1), findMetricByLabels(t, authTotal, map[string]string{
		"node_id":   "1",
		"node_name": "node-1",
		"status":    "ok",
		"failure":   "none",
	}).GetCounter().GetValue())
	require.Equal(t, float64(1), findMetricByLabels(t, authTotal, map[string]string{
		"node_id":   "1",
		"node_name": "node-1",
		"status":    "fail",
		"failure":   "activation_error",
	}).GetCounter().GetValue())
	require.Equal(t, float64(1), findMetricByLabels(t, authTotal, map[string]string{
		"node_id":   "1",
		"node_name": "node-1",
		"status":    "fail",
		"failure":   "activation_route_not_ready",
	}).GetCounter().GetValue())
	require.Equal(t, float64(1), findMetricByLabels(t, authTotal, map[string]string{
		"node_id":   "1",
		"node_name": "node-1",
		"status":    "fail",
		"failure":   "unknown",
	}).GetCounter().GetValue())

	authDuration := requireMetricFamily(t, families, "wukongim_gateway_auth_duration_seconds")
	findMetricByLabels(t, authDuration, map[string]string{
		"node_id":   "1",
		"node_name": "node-1",
		"status":    "ok",
		"failure":   "none",
	})
	findMetricByLabels(t, authDuration, map[string]string{
		"node_id":   "1",
		"node_name": "node-1",
		"status":    "fail",
		"failure":   "activation_error",
	})
	findMetricByLabels(t, authDuration, map[string]string{
		"node_id":   "1",
		"node_name": "node-1",
		"status":    "fail",
		"failure":   "activation_route_not_ready",
	})
	findMetricByLabels(t, authDuration, map[string]string{
		"node_id":   "1",
		"node_name": "node-1",
		"status":    "fail",
		"failure":   "unknown",
	})

	requireMetricFamily(t, families, "wukongim_gateway_async_send_batch_records")
	requireMetricFamily(t, families, "wukongim_gateway_async_send_batch_bytes")
	requireMetricFamily(t, families, "wukongim_gateway_async_send_batch_wait_duration_seconds")

	sendacks := requireMetricFamily(t, families, "wukongim_gateway_sendacks_total")
	require.Equal(t, float64(1), findMetricByLabels(t, sendacks, map[string]string{
		"node_id":   "1",
		"node_name": "node-1",
		"reason":    "system_error",
		"source":    "batch_result_error",
		"class":     "timeout",
	}).GetCounter().GetValue())
	require.Equal(t, float64(1), findMetricByLabels(t, sendacks, map[string]string{
		"node_id":   "1",
		"node_name": "node-1",
		"reason":    "unknown",
		"source":    "unknown",
		"class":     "unknown",
	}).GetCounter().GetValue())
}

func TestChannelMetricsTrackAppendFetchAndActiveChannels(t *testing.T) {
	reg := New(7, "node-7")

	reg.Channel.ObserveAppend("ok", 15*time.Millisecond)
	reg.Channel.ObserveFetch(8 * time.Millisecond)
	reg.Channel.SetActiveChannels(3)
	reg.Channel.SetMaxChannels(10)
	reg.Channel.ObserveActivationRejected("too_many_channels")
	reg.Channel.ObserveIdleEvict()
	reg.Channel.SetExecutionQueueDepth(5)
	reg.Channel.ObserveExecutionEnqueue("ok")
	reg.Channel.ObserveExecutionEnqueue("queue_full")
	reg.Channel.SetExecutionWorkerBusyRatio(0.25)
	reg.Channel.ObserveExecutionMailboxWait(3 * time.Millisecond)

	families, err := reg.Gather()
	require.NoError(t, err)

	appendTotal := requireMetricFamily(t, families, "wukongim_channel_append_total")
	require.Len(t, appendTotal.GetMetric(), 1)
	requireMetricLabels(t, appendTotal.GetMetric()[0], map[string]string{
		"node_id":   "7",
		"node_name": "node-7",
		"result":    "ok",
	})
	require.Equal(t, float64(1), appendTotal.GetMetric()[0].GetCounter().GetValue())

	active := requireMetricFamily(t, families, "wukongim_channel_active_channels")
	require.Len(t, active.GetMetric(), 1)
	requireMetricLabels(t, active.GetMetric()[0], map[string]string{
		"node_id":   "7",
		"node_name": "node-7",
	})
	require.Equal(t, float64(3), active.GetMetric()[0].GetGauge().GetValue())

	maxChannels := requireMetricFamily(t, families, "wukongim_channel_max_channels")
	require.Len(t, maxChannels.GetMetric(), 1)
	requireMetricLabels(t, maxChannels.GetMetric()[0], map[string]string{
		"node_id":   "7",
		"node_name": "node-7",
	})
	require.Equal(t, float64(10), maxChannels.GetMetric()[0].GetGauge().GetValue())

	rejected := requireMetricFamily(t, families, "wukongim_channel_activation_rejected_total")
	require.Len(t, rejected.GetMetric(), 1)
	requireMetricLabels(t, rejected.GetMetric()[0], map[string]string{
		"node_id":   "7",
		"node_name": "node-7",
		"reason":    "too_many_channels",
	})
	require.Equal(t, float64(1), rejected.GetMetric()[0].GetCounter().GetValue())

	evicted := requireMetricFamily(t, families, "wukongim_channel_idle_evictions_total")
	require.Len(t, evicted.GetMetric(), 1)
	requireMetricLabels(t, evicted.GetMetric()[0], map[string]string{
		"node_id":   "7",
		"node_name": "node-7",
	})
	require.Equal(t, float64(1), evicted.GetMetric()[0].GetCounter().GetValue())

	queueDepth := requireMetricFamily(t, families, "wukongim_channel_execution_queue_depth")
	require.Len(t, queueDepth.GetMetric(), 1)
	require.Equal(t, float64(5), queueDepth.GetMetric()[0].GetGauge().GetValue())

	enqueue := requireMetricFamily(t, families, "wukongim_channel_execution_enqueue_total")
	require.Len(t, enqueue.GetMetric(), 2)

	busy := requireMetricFamily(t, families, "wukongim_channel_execution_worker_busy_ratio")
	require.Len(t, busy.GetMetric(), 1)
	require.Equal(t, 0.25, busy.GetMetric()[0].GetGauge().GetValue())

	wait := requireMetricFamily(t, families, "wukongim_channel_execution_mailbox_wait_duration_seconds")
	require.Len(t, wait.GetMetric(), 1)
}

func TestChannelV2MetricsTrackReactorAndWorkerRuntime(t *testing.T) {
	reg := New(8, "node-8")

	reg.ChannelV2.SetReactorMailboxDepth(2, "normal", 9)
	reg.ChannelV2.SetWorkerQueueDepth("store_append", 4)
	reg.ChannelV2.ObserveAppendBatch(16, 1024, 3*time.Millisecond)
	reg.ChannelV2.ObserveAppendLatency("local", 7*time.Millisecond)
	reg.ChannelV2.ObserveAppendStage("meta_apply", "ok", 5*time.Millisecond)
	reg.ChannelV2.ObserveAppendWaitStage("store_append_wait", "quorum", "ok", 17*time.Millisecond)
	reg.ChannelV2.ObserveReplicationStage("follower_pull_rpc", "ok", 19*time.Millisecond)
	reg.ChannelV2.ObserveWorkerResult("store_append", "ok", 11*time.Millisecond)
	reg.ChannelV2.ObserveWorkerResult("rpc_pull", "ok", 13*time.Millisecond)
	reg.ChannelV2.SetChannelRuntimeCount(2, "leader", 17)
	reg.ChannelV2.ObserveChannelActivationRejected("max_channels")
	reg.ChannelV2.SetFollowerParkedCount(2, 11)
	reg.ChannelV2.ObserveFollowerRecoveryProbe("ok")
	reg.ChannelV2.ObservePull("ok", true)
	reg.ChannelV2.ObservePullHint("append", "err", "stale_meta")
	reg.ChannelV2.ObservePullHintReceived("append", "meta_resolve", "err", "channel_not_found")
	reg.ChannelV2.SetPendingMetaCount(2, 3)
	reg.ChannelV2.ObservePendingMeta("released", "timeout")
	reg.ChannelV2.ObserveNeedMetaPull("retry", "other")
	reg.ChannelV2.ObserveMetaCache("hit")
	reg.ChannelV2.ObserveMetaCache("invalidate")

	families, err := reg.Gather()
	require.NoError(t, err)

	mailbox := requireMetricFamily(t, families, "wukongim_channelv2_reactor_mailbox_depth")
	require.Len(t, mailbox.GetMetric(), 1)
	requireMetricLabels(t, mailbox.GetMetric()[0], map[string]string{
		"node_id":    "8",
		"node_name":  "node-8",
		"reactor_id": "2",
		"priority":   "normal",
	})
	require.Equal(t, float64(9), mailbox.GetMetric()[0].GetGauge().GetValue())

	workerQueue := requireMetricFamily(t, families, "wukongim_channelv2_worker_queue_depth")
	require.Len(t, workerQueue.GetMetric(), 1)
	requireMetricLabels(t, workerQueue.GetMetric()[0], map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"pool":      "store_append",
	})
	require.Equal(t, float64(4), workerQueue.GetMetric()[0].GetGauge().GetValue())

	requireMetricFamily(t, families, "wukongim_channelv2_append_batch_records")
	requireMetricFamily(t, families, "wukongim_channelv2_append_batch_bytes")
	requireMetricFamily(t, families, "wukongim_channelv2_append_batch_wait_duration_seconds")

	appendLatency := requireMetricFamily(t, families, "wukongim_channelv2_append_duration_seconds")
	require.Len(t, appendLatency.GetMetric(), 1)
	requireMetricLabels(t, appendLatency.GetMetric()[0], map[string]string{
		"node_id":     "8",
		"node_name":   "node-8",
		"commit_mode": "local",
	})

	appendStage := requireMetricFamily(t, families, "wukongim_channelv2_append_stage_duration_seconds")
	require.Len(t, appendStage.GetMetric(), 1)
	requireMetricLabels(t, appendStage.GetMetric()[0], map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"stage":     "meta_apply",
		"result":    "ok",
	})

	appendWaitStage := requireMetricFamily(t, families, "wukongim_channelv2_append_wait_stage_duration_seconds")
	require.Len(t, appendWaitStage.GetMetric(), 1)
	requireMetricLabels(t, appendWaitStage.GetMetric()[0], map[string]string{
		"node_id":     "8",
		"node_name":   "node-8",
		"stage":       "store_append_wait",
		"commit_mode": "quorum",
		"result":      "ok",
	})

	replicationStage := requireMetricFamily(t, families, "wukongim_channelv2_replication_stage_duration_seconds")
	require.Len(t, replicationStage.GetMetric(), 1)
	requireMetricLabels(t, replicationStage.GetMetric()[0], map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"stage":     "follower_pull_rpc",
		"result":    "ok",
	})

	workerDuration := requireMetricFamily(t, families, "wukongim_channelv2_worker_task_duration_seconds")
	require.Len(t, workerDuration.GetMetric(), 2)
	findMetricByLabels(t, workerDuration, map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"kind":      "store_append",
		"result":    "ok",
	})
	findMetricByLabels(t, workerDuration, map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"kind":      "rpc_pull",
		"result":    "ok",
	})

	rpcPullTotal := requireMetricFamily(t, families, "wukongim_channelv2_rpc_pull_total")
	require.Len(t, rpcPullTotal.GetMetric(), 1)
	requireMetricLabels(t, rpcPullTotal.GetMetric()[0], map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"result":    "ok",
	})
	require.Equal(t, float64(1), rpcPullTotal.GetMetric()[0].GetCounter().GetValue())

	pullHintTotal := requireMetricFamily(t, families, "wukongim_channelv2_pull_hint_total")
	require.Len(t, pullHintTotal.GetMetric(), 1)
	requireMetricLabels(t, pullHintTotal.GetMetric()[0], map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"reason":    "append",
		"result":    "err",
		"error":     "stale_meta",
	})
	require.Equal(t, float64(1), pullHintTotal.GetMetric()[0].GetCounter().GetValue())

	pullHintReceiveTotal := requireMetricFamily(t, families, "wukongim_channelv2_pull_hint_receive_total")
	require.Len(t, pullHintReceiveTotal.GetMetric(), 1)
	requireMetricLabels(t, pullHintReceiveTotal.GetMetric()[0], map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"reason":    "append",
		"stage":     "meta_resolve",
		"result":    "err",
		"error":     "channel_not_found",
	})
	require.Equal(t, float64(1), pullHintReceiveTotal.GetMetric()[0].GetCounter().GetValue())

	pendingMetaCurrent := requireMetricFamily(t, families, "wukongim_channelv2_pending_meta_current")
	require.Len(t, pendingMetaCurrent.GetMetric(), 1)
	requireMetricLabels(t, pendingMetaCurrent.GetMetric()[0], map[string]string{
		"node_id":    "8",
		"node_name":  "node-8",
		"reactor_id": "2",
	})
	require.Equal(t, float64(3), pendingMetaCurrent.GetMetric()[0].GetGauge().GetValue())

	pendingMetaTotal := requireMetricFamily(t, families, "wukongim_channelv2_pending_meta_total")
	require.Len(t, pendingMetaTotal.GetMetric(), 1)
	requireMetricLabels(t, pendingMetaTotal.GetMetric()[0], map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"event":     "released",
		"error":     "timeout",
	})
	require.Equal(t, float64(1), pendingMetaTotal.GetMetric()[0].GetCounter().GetValue())

	needMetaPullTotal := requireMetricFamily(t, families, "wukongim_channelv2_need_meta_pull_total")
	require.Len(t, needMetaPullTotal.GetMetric(), 1)
	requireMetricLabels(t, needMetaPullTotal.GetMetric()[0], map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"result":    "retry",
		"error":     "other",
	})
	require.Equal(t, float64(1), needMetaPullTotal.GetMetric()[0].GetCounter().GetValue())

	activeRuntimes := requireMetricFamily(t, families, "wukongim_channelv2_active_runtimes")
	require.Len(t, activeRuntimes.GetMetric(), 1)
	requireMetricLabels(t, activeRuntimes.GetMetric()[0], map[string]string{
		"node_id":    "8",
		"node_name":  "node-8",
		"reactor_id": "2",
		"role":       "leader",
	})
	require.Equal(t, float64(17), activeRuntimes.GetMetric()[0].GetGauge().GetValue())

	activationRejected := requireMetricFamily(t, families, "wukongim_channelv2_activation_rejected_total")
	require.Len(t, activationRejected.GetMetric(), 1)
	requireMetricLabels(t, activationRejected.GetMetric()[0], map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"reason":    "max_channels",
	})
	require.Equal(t, float64(1), activationRejected.GetMetric()[0].GetCounter().GetValue())

	followerParked := requireMetricFamily(t, families, "wukongim_channelv2_follower_parked")
	require.Len(t, followerParked.GetMetric(), 1)
	requireMetricLabels(t, followerParked.GetMetric()[0], map[string]string{
		"node_id":    "8",
		"node_name":  "node-8",
		"reactor_id": "2",
	})
	require.Equal(t, float64(11), followerParked.GetMetric()[0].GetGauge().GetValue())

	recoveryProbe := requireMetricFamily(t, families, "wukongim_channelv2_recovery_probe_total")
	require.Len(t, recoveryProbe.GetMetric(), 1)
	requireMetricLabels(t, recoveryProbe.GetMetric()[0], map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"result":    "ok",
	})
	require.Equal(t, float64(1), recoveryProbe.GetMetric()[0].GetCounter().GetValue())

	pullTotal := requireMetricFamily(t, families, "wukongim_channelv2_pull_total")
	require.Len(t, pullTotal.GetMetric(), 1)
	requireMetricLabels(t, pullTotal.GetMetric()[0], map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"result":    "ok",
		"empty":     "true",
	})
	require.Equal(t, float64(1), pullTotal.GetMetric()[0].GetCounter().GetValue())

	metaCache := requireMetricFamily(t, families, "wukongim_channelv2_meta_cache_total")
	require.Len(t, metaCache.GetMetric(), 2)
	findMetricByLabels(t, metaCache, map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"result":    "hit",
	})
	findMetricByLabels(t, metaCache, map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"result":    "invalidate",
	})
}

func TestSlotAndTransportMetricsTrackProposalsLeaderChangesAndRPCs(t *testing.T) {
	reg := New(9, "node-9")

	reg.Slot.ObserveProposal(3, 12*time.Millisecond)
	reg.Slot.ObserveLeaderChange(3)
	reg.Transport.ObserveRPC("controller_list_nodes", "ok", 7*time.Millisecond)
	reg.Transport.ObserveRPC("controller_list_nodes", "err", 9*time.Millisecond)
	reg.Transport.ObserveSentBytes("rpc_request", 64)
	reg.Transport.ObserveReceivedBytes("rpc_response", 72)
	reg.Transport.SetPoolConnections(
		map[string]int{"2": 1, "3": 2},
		map[string]int{"2": 3, "3": 2},
	)

	families, err := reg.Gather()
	require.NoError(t, err)

	proposals := requireMetricFamily(t, families, "wukongim_slot_proposals_total")
	require.Len(t, proposals.GetMetric(), 1)
	requireMetricLabels(t, proposals.GetMetric()[0], map[string]string{
		"node_id":   "9",
		"node_name": "node-9",
		"slot_id":   "3",
	})
	require.Equal(t, float64(1), proposals.GetMetric()[0].GetCounter().GetValue())

	leaderChanges := requireMetricFamily(t, families, "wukongim_slot_leader_elections_total")
	require.Len(t, leaderChanges.GetMetric(), 1)
	requireMetricLabels(t, leaderChanges.GetMetric()[0], map[string]string{
		"node_id":   "9",
		"node_name": "node-9",
	})
	require.Equal(t, float64(1), leaderChanges.GetMetric()[0].GetCounter().GetValue())

	rpcTotal := requireMetricFamily(t, families, "wukongim_transport_rpc_total")
	require.Len(t, rpcTotal.GetMetric(), 2)
	for _, metric := range rpcTotal.GetMetric() {
		requireMetricLabels(t, metric, map[string]string{
			"node_id":   "9",
			"node_name": "node-9",
			"service":   "controller_list_nodes",
		})
	}

	sentBytes := requireMetricFamily(t, families, "wukongim_transport_sent_bytes_total")
	require.Len(t, sentBytes.GetMetric(), 1)
	requireMetricLabels(t, sentBytes.GetMetric()[0], map[string]string{
		"node_id":   "9",
		"node_name": "node-9",
		"msg_type":  "rpc_request",
	})
	require.Equal(t, float64(64), sentBytes.GetMetric()[0].GetCounter().GetValue())

	receivedBytes := requireMetricFamily(t, families, "wukongim_transport_received_bytes_total")
	require.Len(t, receivedBytes.GetMetric(), 1)
	requireMetricLabels(t, receivedBytes.GetMetric()[0], map[string]string{
		"node_id":   "9",
		"node_name": "node-9",
		"msg_type":  "rpc_response",
	})
	require.Equal(t, float64(72), receivedBytes.GetMetric()[0].GetCounter().GetValue())

	poolActive := requireMetricFamily(t, families, "wukongim_transport_connections_pool_active")
	require.Len(t, poolActive.GetMetric(), 2)

	poolIdle := requireMetricFamily(t, families, "wukongim_transport_connections_pool_idle")
	require.Len(t, poolIdle.GetMetric(), 2)
}

func TestTransportMetricsTrackRPCClientDialAndEnqueue(t *testing.T) {
	reg := New(15, "node-15")

	reg.Transport.ObserveRPCClient("3", "channel_append", "ok", 7*time.Millisecond)
	reg.Transport.ObserveRPCClient("3", "channel_append", "timeout", 11*time.Millisecond)
	reg.Transport.SetRPCInflight("3", "channel_append", 2)
	reg.Transport.ObserveEnqueue("3", "rpc", "queue_full")
	reg.Transport.ObserveDial("3", "dial_error", 5*time.Millisecond)

	families, err := reg.Gather()
	require.NoError(t, err)

	rpcTotal := requireMetricFamily(t, families, "wukongim_transport_rpc_client_total")
	require.Len(t, rpcTotal.GetMetric(), 2)

	rpcDuration := requireMetricFamily(t, families, "wukongim_transport_rpc_client_duration_seconds")
	require.Len(t, rpcDuration.GetMetric(), 1)
	requireMetricLabels(t, rpcDuration.GetMetric()[0], map[string]string{
		"node_id":     "15",
		"node_name":   "node-15",
		"target_node": "3",
		"service":     "channel_append",
	})

	rpcInflight := requireMetricFamily(t, families, "wukongim_transport_rpc_inflight")
	require.Len(t, rpcInflight.GetMetric(), 1)
	requireMetricLabels(t, rpcInflight.GetMetric()[0], map[string]string{
		"node_id":     "15",
		"node_name":   "node-15",
		"target_node": "3",
		"service":     "channel_append",
	})
	require.Equal(t, float64(2), rpcInflight.GetMetric()[0].GetGauge().GetValue())

	enqueueTotal := requireMetricFamily(t, families, "wukongim_transport_enqueue_total")
	require.Len(t, enqueueTotal.GetMetric(), 1)
	requireMetricLabels(t, enqueueTotal.GetMetric()[0], map[string]string{
		"node_id":     "15",
		"node_name":   "node-15",
		"target_node": "3",
		"kind":        "rpc",
		"result":      "queue_full",
	})
	require.Equal(t, float64(1), enqueueTotal.GetMetric()[0].GetCounter().GetValue())

	dialTotal := requireMetricFamily(t, families, "wukongim_transport_dial_total")
	require.Len(t, dialTotal.GetMetric(), 1)
	requireMetricLabels(t, dialTotal.GetMetric()[0], map[string]string{
		"node_id":     "15",
		"node_name":   "node-15",
		"target_node": "3",
		"result":      "dial_error",
	})
	require.Equal(t, float64(1), dialTotal.GetMetric()[0].GetCounter().GetValue())

	dialDuration := requireMetricFamily(t, families, "wukongim_transport_dial_duration_seconds")
	require.Len(t, dialDuration.GetMetric(), 1)
	requireMetricLabels(t, dialDuration.GetMetric()[0], map[string]string{
		"node_id":     "15",
		"node_name":   "node-15",
		"target_node": "3",
	})
}

func TestTransportMetricsCachesStableLabelHandles(t *testing.T) {
	reg := New(16, "node-16")

	reg.Transport.ObserveRPC("controller_list_nodes", "ok", 7*time.Millisecond)
	reg.Transport.ObserveRPCClient("3", "channel_append", "timeout", 11*time.Millisecond)
	reg.Transport.SetRPCInflight("3", "channel_append", 2)
	reg.Transport.ObserveEnqueue("3", "rpc", "queue_full")
	reg.Transport.ObserveDial("3", "dial_error", 5*time.Millisecond)
	reg.Transport.ObserveSentBytes("rpc_request", 64)
	reg.Transport.ObserveReceivedBytes("rpc_response", 72)

	requireSyncMapEntry(t, &reg.Transport.rpcDurationHandles, "controller_list_nodes")
	requireSyncMapEntry(t, &reg.Transport.rpcTotalHandles, transportRPCResultKey{service: "controller_list_nodes", result: "ok"})
	requireSyncMapEntry(t, &reg.Transport.rpcClientDurationHandles, transportRPCClientServiceKey{targetNode: "3", service: "channel_append"})
	requireSyncMapEntry(t, &reg.Transport.rpcClientTotalHandles, transportRPCClientResultKey{targetNode: "3", service: "channel_append", result: "timeout"})
	requireSyncMapEntry(t, &reg.Transport.rpcInflightHandles, transportRPCClientServiceKey{targetNode: "3", service: "channel_append"})
	requireSyncMapEntry(t, &reg.Transport.enqueueTotalHandles, transportEnqueueResultKey{targetNode: "3", kind: "rpc", result: "queue_full"})
	requireSyncMapEntry(t, &reg.Transport.dialDurationHandles, "3")
	requireSyncMapEntry(t, &reg.Transport.dialTotalHandles, transportDialResultKey{targetNode: "3", result: "dial_error"})
	requireSyncMapEntry(t, &reg.Transport.sentBytesHandles, "rpc_request")
	requireSyncMapEntry(t, &reg.Transport.receivedBytesHandles, "rpc_response")
}

func requireSyncMapEntry(t *testing.T, m *sync.Map, key any) {
	t.Helper()
	if _, ok := m.Load(key); !ok {
		t.Fatalf("metric handle cache missing key %#v", key)
	}
}

func TestControllerMetricsTrackDecisionStateAndTaskCounts(t *testing.T) {
	reg := New(11, "node-11")

	reg.Controller.ObserveDecision("repair", 18*time.Millisecond)
	reg.Controller.ObserveTaskCompleted("repair", "ok")
	reg.Controller.ObserveMigrationCompleted("ok")
	reg.Controller.SetControllerRaftStepQueue(7, 1024)
	reg.Controller.ObserveControllerRaftStepEnqueue("ok", 3*time.Millisecond)
	reg.Controller.SetNodeCounts(2, 1, 1)
	reg.Controller.SetTaskActive(map[string]int{
		"repair":    2,
		"rebalance": 1,
	})
	reg.Controller.SetMigrationsActive(2)

	families, err := reg.Gather()
	require.NoError(t, err)

	decisions := requireMetricFamily(t, families, "wukongim_controller_decisions_total")
	require.Len(t, decisions.GetMetric(), 4)
	foundRepair := false
	for _, metric := range decisions.GetMetric() {
		labels := metric.GetLabel()
		for _, label := range labels {
			if label.GetName() == "type" && label.GetValue() == "repair" {
				requireMetricLabels(t, metric, map[string]string{
					"node_id":   "11",
					"node_name": "node-11",
					"type":      "repair",
				})
				require.Equal(t, float64(1), metric.GetCounter().GetValue())
				foundRepair = true
			}
		}
	}
	require.True(t, foundRepair, "repair decision counter should be present")

	nodesAlive := requireMetricFamily(t, families, "wukongim_controller_nodes_alive")
	require.Len(t, nodesAlive.GetMetric(), 1)
	require.Equal(t, float64(2), nodesAlive.GetMetric()[0].GetGauge().GetValue())

	tasksActive := requireMetricFamily(t, families, "wukongim_controller_tasks_active")
	require.Len(t, tasksActive.GetMetric(), 4)

	migrationsActive := requireMetricFamily(t, families, "wukongim_controller_hashslot_migrations_active")
	require.Len(t, migrationsActive.GetMetric(), 1)
	require.Equal(t, float64(2), migrationsActive.GetMetric()[0].GetGauge().GetValue())

	migrationsTotal := requireMetricFamily(t, families, "wukongim_controller_hashslot_migrations_total")
	require.Len(t, migrationsTotal.GetMetric(), 3)
	foundOK := false
	for _, metric := range migrationsTotal.GetMetric() {
		for _, label := range metric.GetLabel() {
			if label.GetName() == "result" && label.GetValue() == "ok" {
				require.Equal(t, float64(1), metric.GetCounter().GetValue())
				foundOK = true
			}
		}
	}
	require.True(t, foundOK, "ok migration counter should be present")

	stepDepth := requireMetricFamily(t, families, "wukongim_controller_raft_step_queue_depth")
	require.Len(t, stepDepth.GetMetric(), 1)
	require.Equal(t, float64(7), stepDepth.GetMetric()[0].GetGauge().GetValue())

	stepCapacity := requireMetricFamily(t, families, "wukongim_controller_raft_step_queue_capacity")
	require.Len(t, stepCapacity.GetMetric(), 1)
	require.Equal(t, float64(1024), stepCapacity.GetMetric()[0].GetGauge().GetValue())

	stepEnqueue := requireMetricFamily(t, families, "wukongim_controller_raft_step_enqueue_duration_seconds")
	require.Len(t, stepEnqueue.GetMetric(), 1)
	requireMetricLabels(t, stepEnqueue.GetMetric()[0], map[string]string{"result": "ok"})
}

func TestControllerMetricsIncludeLeaderTransferAndLeaderSkew(t *testing.T) {
	reg := New(1, "node-1")
	reg.Controller.ObserveDecision("leader_transfer", time.Millisecond)
	reg.Controller.ObserveTaskCompleted("leader_transfer", "safety_check")
	reg.Controller.SetTaskActive(map[string]int{"leader_transfer": 1})
	reg.Controller.SetSlotLeaderSkew(2)

	families, err := reg.Gather()
	require.NoError(t, err)

	decisions := requireMetricFamily(t, families, "wukongim_controller_decisions_total")
	leaderTransferDecision := findMetricByLabels(t, decisions, map[string]string{"type": "leader_transfer"})
	requireMetricLabels(t, leaderTransferDecision, map[string]string{"type": "leader_transfer"})
	require.Equal(t, float64(1), leaderTransferDecision.GetCounter().GetValue())

	completed := requireMetricFamily(t, families, "wukongim_controller_tasks_completed_total")
	completedSafety := findMetricByLabels(t, completed, map[string]string{"type": "leader_transfer", "result": "safety_check"})
	requireMetricLabels(t, completedSafety, map[string]string{"type": "leader_transfer", "result": "safety_check"})
	require.Equal(t, float64(1), completedSafety.GetCounter().GetValue())

	active := requireMetricFamily(t, families, "wukongim_controller_tasks_active")
	activeLeaderTransfer := findMetricByLabels(t, active, map[string]string{"type": "leader_transfer"})
	require.Equal(t, float64(1), activeLeaderTransfer.GetGauge().GetValue())

	skew := requireMetricFamily(t, families, "wukongim_controller_slot_leader_skew")
	require.Equal(t, float64(2), skew.GetMetric()[0].GetGauge().GetValue())
}

func TestStorageMetricsTrackDiskUsageByStore(t *testing.T) {
	reg := New(13, "node-13")

	reg.Storage.SetDiskUsage(map[string]int64{
		"meta":            12,
		"channel_log":     34,
		"controller_meta": 56,
	})

	families, err := reg.Gather()
	require.NoError(t, err)

	usage := requireMetricFamily(t, families, "wukongim_storage_disk_usage_bytes")
	require.Len(t, usage.GetMetric(), 5)

	want := map[string]float64{
		"meta":            12,
		"raft":            0,
		"channel_log":     34,
		"controller_meta": 56,
		"controller_raft": 0,
	}
	for _, metric := range usage.GetMetric() {
		labels := map[string]string{}
		for _, label := range metric.GetLabel() {
			labels[label.GetName()] = label.GetValue()
		}
		store := labels["store"]
		expected, ok := want[store]
		require.True(t, ok, "unexpected store label %q", store)
		require.Equal(t, expected, metric.GetGauge().GetValue(), "store %s", store)
	}
}

func TestStorageMetricsTrackCommitCoordinator(t *testing.T) {
	reg := New(14, "node-14")

	reg.Storage.SetCommitQueueDepth("message", 7)
	reg.Storage.ObserveCommitBatch("message", "ok", StorageCommitBatchObservation{
		Requests:        2,
		Records:         3,
		Bytes:           30,
		CollectDuration: 1 * time.Millisecond,
		BuildDuration:   2 * time.Millisecond,
		CommitDuration:  3 * time.Millisecond,
		PublishDuration: 4 * time.Millisecond,
		TotalDuration:   10 * time.Millisecond,
	})

	families, err := reg.Gather()
	require.NoError(t, err)

	queueDepth := requireMetricFamily(t, families, "wukongim_storage_commit_queue_depth")
	require.Len(t, queueDepth.GetMetric(), 1)
	requireMetricLabels(t, queueDepth.GetMetric()[0], map[string]string{
		"node_id":   "14",
		"node_name": "node-14",
		"store":     "message",
	})
	require.Equal(t, float64(7), queueDepth.GetMetric()[0].GetGauge().GetValue())

	requests := requireMetricFamily(t, families, "wukongim_storage_commit_batch_requests")
	require.Len(t, requests.GetMetric(), 1)
	requireMetricLabels(t, requests.GetMetric()[0], map[string]string{"store": "message"})

	records := requireMetricFamily(t, families, "wukongim_storage_commit_batch_records")
	require.Len(t, records.GetMetric(), 1)
	requireMetricLabels(t, records.GetMetric()[0], map[string]string{"store": "message"})

	bytes := requireMetricFamily(t, families, "wukongim_storage_commit_batch_bytes")
	require.Len(t, bytes.GetMetric(), 1)
	requireMetricLabels(t, bytes.GetMetric()[0], map[string]string{"store": "message"})

	duration := requireMetricFamily(t, families, "wukongim_storage_commit_batch_duration_seconds")
	require.Len(t, duration.GetMetric(), 5)
	commitDuration := findMetricByLabels(t, duration, map[string]string{"store": "message", "stage": "commit", "result": "ok"})
	require.NotNil(t, commitDuration.GetHistogram())
}

func TestRegistryExposesMessageMetrics(t *testing.T) {
	reg := New(1, "n1")
	reg.Message.ObserveMetaRefresh("cache_hit", 3*time.Millisecond)
	reg.Message.ObserveAppend("local", "ok", 5*time.Millisecond)
	reg.Message.ObserveAppendError("channelplane", "timeout")
	reg.Message.SetCommittedDispatchQueueDepth("0", 7)
	reg.Message.ObserveCommittedDispatchEnqueue("0", "ok")
	reg.Message.ObserveCommittedDispatchEnqueue("0", "overflow")
	reg.Message.ObserveCommittedDispatchOverflow("0")
	reg.Message.SetCommittedReplayLag("person", 3)
	reg.Message.ObserveCommittedReplayPass("ok", 9*time.Millisecond)

	families, err := reg.Gather()
	require.NoError(t, err)
	requireMetricFamily(t, families, "wukongim_message_meta_refresh_total")
	requireMetricFamily(t, families, "wukongim_message_meta_refresh_duration_seconds")
	requireMetricFamily(t, families, "wukongim_message_append_total")
	requireMetricFamily(t, families, "wukongim_message_append_duration_seconds")
	requireMetricFamily(t, families, "wukongim_message_append_errors_total")
	requireMetricFamily(t, families, "wukongim_message_committed_dispatch_queue_depth")
	requireMetricFamily(t, families, "wukongim_message_committed_dispatch_enqueue_total")
	requireMetricFamily(t, families, "wukongim_message_committed_dispatch_overflow_total")
	requireMetricFamily(t, families, "wukongim_message_committed_replay_lag_messages")
	requireMetricFamily(t, families, "wukongim_message_committed_replay_pass_duration_seconds")
}

func TestRegistryExposesDeliveryMetrics(t *testing.T) {
	reg := New(1, "n1")
	reg.Delivery.ObserveResolve("person", "ok", 2*time.Millisecond, 1, 2)
	reg.Delivery.ObservePushRPC("2", "ok", 4*time.Millisecond, 3)
	reg.Delivery.ObserveEventQueue("ok")
	reg.Delivery.ObserveEventQueue("overflow")
	reg.Delivery.ObserveError("retryable")
	reg.Delivery.ObserveFanoutTask("2", "retryable", 3*time.Millisecond)
	reg.Delivery.ObserveRetry("enqueue", "ok")
	reg.Delivery.SetRetryQueueDepth(4)
	reg.Delivery.SetActorInflightRoutes(9)
	reg.Delivery.SetAckBindings(5)
	reg.Delivery.ObserveRouteExpired("group")

	families, err := reg.Gather()
	require.NoError(t, err)
	requireMetricFamily(t, families, "wukongim_delivery_resolve_duration_seconds")
	requireMetricFamily(t, families, "wukongim_delivery_resolve_pages_total")
	requireMetricFamily(t, families, "wukongim_delivery_resolve_routes_total")
	requireMetricFamily(t, families, "wukongim_delivery_push_rpc_total")
	requireMetricFamily(t, families, "wukongim_delivery_push_rpc_duration_seconds")
	requireMetricFamily(t, families, "wukongim_delivery_push_rpc_routes_total")
	requireMetricFamily(t, families, "wukongim_delivery_event_queue_total")
	requireMetricFamily(t, families, "wukongim_delivery_errors_total")
	requireMetricFamily(t, families, "wukongim_delivery_fanout_task_total")
	requireMetricFamily(t, families, "wukongim_delivery_fanout_task_duration_seconds")
	requireMetricFamily(t, families, "wukongim_delivery_retry_total")
	requireMetricFamily(t, families, "wukongim_delivery_retry_queue_depth")
	requireMetricFamily(t, families, "wukongim_delivery_actor_inflight_routes")
	requireMetricFamily(t, families, "wukongim_delivery_ack_bindings")
	requireMetricFamily(t, families, "wukongim_delivery_route_expired_total")
}

func TestRegistryIncludesDiagnosticsMetrics(t *testing.T) {
	reg := New(1, "node-1")
	reg.Diagnostics.RecordEvent("message.send_durable", "ok")
	reg.Diagnostics.RecordDropped("sampled_out")
	reg.Diagnostics.SetBufferUsageRatio(0.5)

	families, err := reg.Gather()
	require.NoError(t, err)

	requireMetricFamily(t, families, "wukongim_diagnostics_events_recorded_total")
	requireMetricFamily(t, families, "wukongim_diagnostics_events_dropped_total")
	requireMetricFamily(t, families, "wukongim_diagnostics_buffer_usage_ratio")
}

func requireMetricFamily(t *testing.T, families []*dto.MetricFamily, name string) *dto.MetricFamily {
	t.Helper()
	for _, family := range families {
		if family.GetName() == name {
			return family
		}
	}
	t.Fatalf("metric family %q not found", name)
	return nil
}

func requireMetricLabels(t *testing.T, metric *dto.Metric, want map[string]string) {
	t.Helper()
	got := make(map[string]string, len(metric.GetLabel()))
	for _, label := range metric.GetLabel() {
		got[label.GetName()] = label.GetValue()
	}
	for key, value := range want {
		require.Equal(t, value, got[key], "label %s", key)
	}
}

func findMetricByLabels(t *testing.T, family *dto.MetricFamily, want map[string]string) *dto.Metric {
	t.Helper()
	for _, metric := range family.GetMetric() {
		got := make(map[string]string, len(metric.GetLabel()))
		for _, label := range metric.GetLabel() {
			got[label.GetName()] = label.GetValue()
		}
		matched := true
		for key, value := range want {
			if got[key] != value {
				matched = false
				break
			}
		}
		if matched {
			return metric
		}
	}
	t.Fatalf("metric family %q with labels %v not found", family.GetName(), want)
	return nil
}
