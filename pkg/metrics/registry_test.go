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
	reg.ChannelV2.SetWorkerInflight("store_append", 3)
	reg.ChannelV2.SetWorkerInflightPeak("store_append", 7)
	reg.ChannelV2.ObserveAppendBatch(16, 1024, 3*time.Millisecond)
	reg.ChannelV2.ObserveAppendLatency("local", 7*time.Millisecond)
	reg.ChannelV2.ObserveAppendStage("meta_apply", "ok", 5*time.Millisecond)
	reg.ChannelV2.ObserveAppendWaitStage("store_append_wait", "quorum", "ok", 17*time.Millisecond)
	reg.ChannelV2.ObserveReplicationStage("follower_pull_rpc", "ok", 19*time.Millisecond)
	reg.ChannelV2.ObserveWorkerResult("store_append", "ok", 11*time.Millisecond)
	reg.ChannelV2.ObserveWorkerResult("rpc_pull", "ok", 13*time.Millisecond)
	reg.ChannelV2.ObserveWorkerBatch("rpc_pull", "ok", 3)
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

	workerInflight := requireMetricFamily(t, families, "wukongim_channelv2_worker_inflight")
	require.Len(t, workerInflight.GetMetric(), 1)
	requireMetricLabels(t, workerInflight.GetMetric()[0], map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"pool":      "store_append",
	})
	require.Equal(t, float64(3), workerInflight.GetMetric()[0].GetGauge().GetValue())

	workerInflightPeak := requireMetricFamily(t, families, "wukongim_channelv2_worker_inflight_peak")
	require.Len(t, workerInflightPeak.GetMetric(), 1)
	requireMetricLabels(t, workerInflightPeak.GetMetric()[0], map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"pool":      "store_append",
	})
	require.Equal(t, float64(7), workerInflightPeak.GetMetric()[0].GetGauge().GetValue())

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

	workerBatch := requireMetricFamily(t, families, "wukongim_channelv2_worker_batch_items")
	require.Len(t, workerBatch.GetMetric(), 1)
	workerBatchPull := findMetricByLabels(t, workerBatch, map[string]string{
		"node_id":   "8",
		"node_name": "node-8",
		"kind":      "rpc_pull",
		"result":    "ok",
	})
	require.Equal(t, uint64(1), workerBatchPull.GetHistogram().GetSampleCount())
	require.Equal(t, float64(3), workerBatchPull.GetHistogram().GetSampleSum())

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
	reg.Storage.ObserveCommitRequest("message", "append", "ok", StorageCommitRequestObservation{
		Records:  2,
		Bytes:    32,
		Duration: 11 * time.Millisecond,
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

	requestDuration := requireMetricFamily(t, families, "wukongim_storage_commit_request_duration_seconds")
	require.Len(t, requestDuration.GetMetric(), 1)
	request := findMetricByLabels(t, requestDuration, map[string]string{"store": "message", "lane": "append", "result": "ok"})
	require.NotNil(t, request.GetHistogram())
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

func TestRegistryExposesChannelWriteMetrics(t *testing.T) {
	reg := New(12, "node-12")
	reg.ChannelWrite.ObserveRouter("local", "ok", 8, 3*time.Millisecond)
	reg.ChannelWrite.ObserveRouter("remote", "backpressured", 4, 5*time.Millisecond)
	reg.ChannelWrite.ObserveLocalAdmission("accepted", 8)
	reg.ChannelWrite.ObserveLocalAdmission("backpressured", 4)
	reg.ChannelWrite.SetWriterPressure(3, 1024, 9, 1024, 7, 2, 5)
	reg.ChannelWrite.ObserveEffectPool("append", "submitted", 8, 16, false)
	reg.ChannelWrite.ObserveEffectPool("append", "full", 16, 16, true)
	reg.ChannelWrite.ObserveEffect("append", "ok", 8, 4*time.Millisecond)
	reg.ChannelWrite.ObserveEffect("post_commit", "route_not_ready", 1, 6*time.Millisecond)

	families, err := reg.Gather()
	require.NoError(t, err)

	router := requireMetricFamily(t, families, "wukongim_channelwrite_router_total")
	require.Equal(t, float64(1), findMetricByLabels(t, router, map[string]string{
		"node_id":   "12",
		"node_name": "node-12",
		"path":      "remote",
		"result":    "backpressured",
	}).GetCounter().GetValue())

	admission := requireMetricFamily(t, families, "wukongim_channelwrite_local_admission_total")
	require.Equal(t, float64(1), findMetricByLabels(t, admission, map[string]string{
		"node_id":   "12",
		"node_name": "node-12",
		"result":    "accepted",
	}).GetCounter().GetValue())

	state := requireMetricFamily(t, families, "wukongim_channelwrite_writer_state_items")
	require.Equal(t, float64(5), findMetricByLabels(t, state, map[string]string{
		"node_id":   "12",
		"node_name": "node-12",
		"kind":      "post_commit_backlog",
	}).GetGauge().GetValue())

	effect := requireMetricFamily(t, families, "wukongim_channelwrite_effect_total")
	require.Equal(t, float64(1), findMetricByLabels(t, effect, map[string]string{
		"node_id":   "12",
		"node_name": "node-12",
		"stage":     "post_commit",
		"result":    "route_not_ready",
	}).GetCounter().GetValue())

	poolSubmit := requireMetricFamily(t, families, "wukongim_channelwrite_effect_pool_submit_total")
	require.Equal(t, float64(1), findMetricByLabels(t, poolSubmit, map[string]string{
		"node_id":   "12",
		"node_name": "node-12",
		"stage":     "append",
		"result":    "full",
	}).GetCounter().GetValue())

	poolInflight := requireMetricFamily(t, families, "wukongim_channelwrite_effect_pool_inflight")
	require.Equal(t, float64(16), findMetricByLabels(t, poolInflight, map[string]string{
		"node_id":   "12",
		"node_name": "node-12",
		"stage":     "append",
	}).GetGauge().GetValue())

	poolCapacity := requireMetricFamily(t, families, "wukongim_channelwrite_effect_pool_capacity")
	require.Equal(t, float64(16), findMetricByLabels(t, poolCapacity, map[string]string{
		"node_id":   "12",
		"node_name": "node-12",
		"stage":     "append",
	}).GetGauge().GetValue())

	poolSaturated := requireMetricFamily(t, families, "wukongim_channelwrite_effect_pool_saturated")
	require.Equal(t, float64(1), findMetricByLabels(t, poolSaturated, map[string]string{
		"node_id":   "12",
		"node_name": "node-12",
		"stage":     "append",
	}).GetGauge().GetValue())

	for _, family := range []*dto.MetricFamily{router, admission, state, effect, poolSubmit, poolInflight, poolCapacity, poolSaturated} {
		for _, metric := range family.GetMetric() {
			requireNoMetricLabel(t, metric, "uid")
			requireNoMetricLabel(t, metric, "channel_id")
			requireNoMetricLabel(t, metric, "channelID")
		}
	}
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
	reg.Delivery.SetRecipientWorkerQueue(3, 8)
	reg.Delivery.ObserveRecipientWorkerAdmission("accepted", 2*time.Millisecond)
	reg.Delivery.ObserveRecipientWorkerProcess("ok", 4, 5*time.Millisecond)

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
	queueDepth := requireMetricFamily(t, families, "wukongim_delivery_recipient_worker_queue_depth")
	require.Equal(t, float64(3), findMetricByLabels(t, queueDepth, map[string]string{
		"node_id":   "1",
		"node_name": "n1",
	}).GetGauge().GetValue())
	queueCapacity := requireMetricFamily(t, families, "wukongim_delivery_recipient_worker_queue_capacity")
	require.Equal(t, float64(8), findMetricByLabels(t, queueCapacity, map[string]string{
		"node_id":   "1",
		"node_name": "n1",
	}).GetGauge().GetValue())
	admission := requireMetricFamily(t, families, "wukongim_delivery_recipient_worker_admission_total")
	require.Equal(t, float64(1), findMetricByLabels(t, admission, map[string]string{
		"node_id":   "1",
		"node_name": "n1",
		"result":    "accepted",
	}).GetCounter().GetValue())
	process := requireMetricFamily(t, families, "wukongim_delivery_recipient_worker_process_recipients")
	require.Equal(t, float64(4), findMetricByLabels(t, process, map[string]string{
		"node_id":   "1",
		"node_name": "n1",
		"result":    "ok",
	}).GetHistogram().GetSampleSum())
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

func TestRuntimePressureMetricsTrackPoolsQueuesAndDurations(t *testing.T) {
	reg := New(9, "node-9")

	reg.RuntimePressure.SetPoolWorkers("channelv2", "store_append", 4)
	reg.RuntimePressure.SetPoolInflight("channelv2", "store_append", 2)
	reg.RuntimePressure.SetQueue("channelv2", "store_append", "worker", "none", RuntimePressureQueueObservation{
		Depth:         7,
		Capacity:      128,
		Bytes:         4096,
		BytesCapacity: 1 << 20,
	})
	reg.RuntimePressure.ObserveAdmission("channelv2", "store_append", "worker", "none", "ok")
	reg.RuntimePressure.ObserveAdmission("channelv2", "store_append", "worker", "none", "raw uid leaked")
	reg.RuntimePressure.ObserveQueueWait("channelv2", "store_append", "worker", "none", "ok", 2*time.Millisecond)
	reg.RuntimePressure.ObserveTaskDuration("channelv2", "store_append", "store_append", "ok", 3*time.Millisecond)
	reg.RuntimePressure.ObserveTaskDuration("channelv2", "store_append", "store_append", "ok", -time.Second)
	reg.RuntimePressure.SetPoolWorkers("", "", -4)
	reg.RuntimePressure.SetPoolInflight("", "", -2)
	reg.RuntimePressure.SetQueue("", "", "", "", RuntimePressureQueueObservation{
		Depth:         -7,
		Capacity:      -128,
		Bytes:         -4096,
		BytesCapacity: -1 << 20,
	})
	reg.RuntimePressure.ObserveQueueWait("", "", "", "", "ok", -time.Second)
	reg.RuntimePressure.ObserveTaskDuration("", "", "", "ok", -time.Second)

	families, err := reg.Gather()
	require.NoError(t, err)

	workers := requireMetricFamily(t, families, "wukongim_runtime_pool_workers")
	require.Equal(t, float64(4), findMetricByLabels(t, workers, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "channelv2", "pool": "store_append",
	}).GetGauge().GetValue())
	require.Equal(t, float64(0), findMetricByLabels(t, workers, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "unknown", "pool": "unknown",
	}).GetGauge().GetValue())

	inflight := requireMetricFamily(t, families, "wukongim_runtime_pool_inflight")
	require.Equal(t, float64(2), findMetricByLabels(t, inflight, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "channelv2", "pool": "store_append",
	}).GetGauge().GetValue())
	require.Equal(t, float64(0), findMetricByLabels(t, inflight, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "unknown", "pool": "unknown",
	}).GetGauge().GetValue())

	depth := requireMetricFamily(t, families, "wukongim_runtime_pool_queue_depth")
	require.Equal(t, float64(7), findMetricByLabels(t, depth, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "channelv2", "pool": "store_append", "queue": "worker", "priority": "none",
	}).GetGauge().GetValue())
	require.Equal(t, float64(0), findMetricByLabels(t, depth, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "unknown", "pool": "unknown", "queue": "unknown", "priority": "none",
	}).GetGauge().GetValue())

	capacity := requireMetricFamily(t, families, "wukongim_runtime_pool_queue_capacity")
	require.Equal(t, float64(128), findMetricByLabels(t, capacity, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "channelv2", "pool": "store_append", "queue": "worker", "priority": "none",
	}).GetGauge().GetValue())
	require.Equal(t, float64(0), findMetricByLabels(t, capacity, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "unknown", "pool": "unknown", "queue": "unknown", "priority": "none",
	}).GetGauge().GetValue())

	bytes := requireMetricFamily(t, families, "wukongim_runtime_pool_queue_bytes")
	require.Equal(t, float64(4096), findMetricByLabels(t, bytes, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "channelv2", "pool": "store_append", "queue": "worker", "priority": "none",
	}).GetGauge().GetValue())
	require.Equal(t, float64(0), findMetricByLabels(t, bytes, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "unknown", "pool": "unknown", "queue": "unknown", "priority": "none",
	}).GetGauge().GetValue())

	byteCapacity := requireMetricFamily(t, families, "wukongim_runtime_pool_queue_bytes_capacity")
	require.Equal(t, float64(1<<20), findMetricByLabels(t, byteCapacity, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "channelv2", "pool": "store_append", "queue": "worker", "priority": "none",
	}).GetGauge().GetValue())
	require.Equal(t, float64(0), findMetricByLabels(t, byteCapacity, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "unknown", "pool": "unknown", "queue": "unknown", "priority": "none",
	}).GetGauge().GetValue())

	admissions := requireMetricFamily(t, families, "wukongim_runtime_pool_admission_total")
	require.Equal(t, float64(1), findMetricByLabels(t, admissions, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "channelv2", "pool": "store_append", "queue": "worker", "priority": "none", "result": "ok",
	}).GetCounter().GetValue())
	require.Equal(t, float64(1), findMetricByLabels(t, admissions, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "channelv2", "pool": "store_append", "queue": "worker", "priority": "none", "result": "other",
	}).GetCounter().GetValue())

	waitDuration := requireMetricFamily(t, families, "wukongim_runtime_pool_wait_duration_seconds")
	waitOK := findMetricByLabels(t, waitDuration, map[string]string{
		"node_id":   "9",
		"node_name": "node-9",
		"component": "channelv2",
		"pool":      "store_append",
		"queue":     "worker",
		"priority":  "none",
		"result":    "ok",
	})
	require.Greater(t, waitOK.GetHistogram().GetSampleCount(), uint64(0))
	waitUnknown := findMetricByLabels(t, waitDuration, map[string]string{
		"node_id":   "9",
		"node_name": "node-9",
		"component": "unknown",
		"pool":      "unknown",
		"queue":     "unknown",
		"priority":  "none",
		"result":    "ok",
	})
	require.Equal(t, uint64(1), waitUnknown.GetHistogram().GetSampleCount())
	require.Equal(t, float64(0), waitUnknown.GetHistogram().GetSampleSum())

	taskDuration := requireMetricFamily(t, families, "wukongim_runtime_pool_task_duration_seconds")
	taskOK := findMetricByLabels(t, taskDuration, map[string]string{
		"node_id":   "9",
		"node_name": "node-9",
		"component": "channelv2",
		"pool":      "store_append",
		"task":      "store_append",
		"result":    "ok",
	})
	require.Greater(t, taskOK.GetHistogram().GetSampleCount(), uint64(0))
	taskUnknown := findMetricByLabels(t, taskDuration, map[string]string{
		"node_id":   "9",
		"node_name": "node-9",
		"component": "unknown",
		"pool":      "unknown",
		"task":      "unknown",
		"result":    "ok",
	})
	require.Equal(t, uint64(1), taskUnknown.GetHistogram().GetSampleCount())
	require.Equal(t, float64(0), taskUnknown.GetHistogram().GetSampleSum())
}

func TestNormalizeRuntimePressureResult(t *testing.T) {
	tests := []struct {
		result string
		want   string
	}{
		{result: "ok", want: "ok"},
		{result: "full", want: "full"},
		{result: "busy", want: "busy"},
		{result: "closed", want: "closed"},
		{result: "canceled", want: "canceled"},
		{result: "timeout", want: "timeout"},
		{result: "too_large", want: "too_large"},
		{result: "invalid", want: "invalid"},
		{result: "dropped", want: "dropped"},
		{result: "coalesced", want: "coalesced"},
		{result: "stopped", want: "stopped"},
		{result: "dirty", want: "dirty"},
		{result: "requeued", want: "requeued"},
		{result: "err", want: "err"},
		{result: "", want: "other"},
		{result: "raw uid leaked", want: "other"},
	}

	for _, tt := range tests {
		require.Equal(t, tt.want, NormalizeRuntimePressureResult(tt.result))
	}
}

func TestRuntimePressureMetricsNilSafe(t *testing.T) {
	var m *RuntimePressureMetrics
	m.SetPoolWorkers("gateway", "async_auth", 1)
	m.SetPoolInflight("gateway", "async_auth", 1)
	m.SetQueue("gateway", "async_auth", "auth", "none", RuntimePressureQueueObservation{Depth: 1})
	m.SetQueueDepth("gateway", "async_auth", "auth", "none", 1)
	m.SetQueueCapacity("gateway", "async_auth", "auth", "none", 1)
	m.SetQueueBytes("gateway", "async_auth", "auth", "none", 1)
	m.SetQueueBytesCapacity("gateway", "async_auth", "auth", "none", 1)
	m.ObserveAdmission("gateway", "async_auth", "auth", "none", "ok")
	m.ObserveQueueWait("gateway", "async_auth", "auth", "none", "ok", time.Millisecond)
	m.ObserveTaskDuration("gateway", "async_auth", "auth", "ok", time.Millisecond)
}

func TestConversationMetricsTrackListShapeAndLatency(t *testing.T) {
	reg := New(11, "node-11")

	reg.Conversation.ObserveList("ok", true, 12*time.Millisecond, 2, 1, 2, 0, 0)

	families, err := reg.Gather()
	require.NoError(t, err)

	total := requireMetricFamily(t, families, "wukongim_conversation_list_total")
	require.Equal(t, float64(1), findMetricByLabels(t, total, map[string]string{
		"node_id":   "11",
		"node_name": "node-11",
		"result":    "ok",
		"more":      "true",
	}).GetCounter().GetValue())

	duration := requireMetricFamily(t, families, "wukongim_conversation_list_duration_seconds")
	require.Equal(t, uint64(1), findMetricByLabels(t, duration, map[string]string{
		"node_id":   "11",
		"node_name": "node-11",
		"result":    "ok",
		"more":      "true",
	}).GetHistogram().GetSampleCount())

	returned := requireMetricFamily(t, families, "wukongim_conversation_list_returned_items")
	require.Equal(t, float64(2), findMetricByLabels(t, returned, map[string]string{
		"node_id":   "11",
		"node_name": "node-11",
		"result":    "ok",
		"more":      "true",
	}).GetHistogram().GetSampleSum())

	sparseItems := requireMetricFamily(t, families, "wukongim_conversation_list_sparse_items")
	require.Equal(t, float64(1), findMetricByLabels(t, sparseItems, map[string]string{
		"node_id":   "11",
		"node_name": "node-11",
		"result":    "ok",
		"more":      "true",
	}).GetHistogram().GetSampleSum())

	lastMessageLoads := requireMetricFamily(t, families, "wukongim_conversation_list_last_message_loads")
	require.Equal(t, float64(2), findMetricByLabels(t, lastMessageLoads, map[string]string{
		"node_id":   "11",
		"node_name": "node-11",
		"result":    "ok",
		"more":      "true",
	}).GetHistogram().GetSampleSum())

	lastMessageErrors := requireMetricFamily(t, families, "wukongim_conversation_list_last_message_errors")
	require.Equal(t, float64(0), findMetricByLabels(t, lastMessageErrors, map[string]string{
		"node_id":   "11",
		"node_name": "node-11",
		"result":    "ok",
		"more":      "true",
	}).GetHistogram().GetSampleSum())

	activeIndexStaleSkips := requireMetricFamily(t, families, "wukongim_conversation_list_active_index_stale_skips")
	require.Equal(t, float64(0), findMetricByLabels(t, activeIndexStaleSkips, map[string]string{
		"node_id":   "11",
		"node_name": "node-11",
		"result":    "ok",
		"more":      "true",
	}).GetHistogram().GetSampleSum())

	requireNoMetricFamily(t, families, "wukongim_conversation_list_scanned_memberships")
	requireNoMetricFamily(t, families, "wukongim_conversation_list_last_message_hits")
}

func TestConversationMetricsTrackAuthorityCountersAndLowCardinalityLabels(t *testing.T) {
	reg := New(11, "node-11")

	reg.Conversation.ObserveAuthorityAdmit("timeout")
	reg.Conversation.ObserveAuthorityCachePressure("admit", "cache_pressure")
	reg.Conversation.ObserveAuthorityList("route_not_ready")
	reg.Conversation.ObserveAuthorityHandoff("drained")

	families, err := reg.Gather()
	require.NoError(t, err)

	admit := requireMetricFamily(t, families, "wukongim_conversation_authority_admit_total")
	require.Equal(t, float64(1), findMetricByLabels(t, admit, map[string]string{
		"node_id":   "11",
		"node_name": "node-11",
		"result":    "timeout",
	}).GetCounter().GetValue())

	pressure := requireMetricFamily(t, families, "wukongim_conversation_authority_cache_pressure_total")
	require.Equal(t, float64(1), findMetricByLabels(t, pressure, map[string]string{
		"node_id":   "11",
		"node_name": "node-11",
		"phase":     "admit",
		"result":    "cache_pressure",
	}).GetCounter().GetValue())

	list := requireMetricFamily(t, families, "wukongim_conversation_authority_list_total")
	require.Equal(t, float64(1), findMetricByLabels(t, list, map[string]string{
		"node_id":   "11",
		"node_name": "node-11",
		"result":    "route_not_ready",
	}).GetCounter().GetValue())

	handoff := requireMetricFamily(t, families, "wukongim_conversation_authority_handoff_total")
	require.Equal(t, float64(1), findMetricByLabels(t, handoff, map[string]string{
		"node_id":   "11",
		"node_name": "node-11",
		"result":    "drained",
	}).GetCounter().GetValue())

	for _, family := range []*dto.MetricFamily{admit, pressure, list, handoff} {
		for _, metric := range family.GetMetric() {
			requireNoMetricLabel(t, metric, "uid")
			requireNoMetricLabel(t, metric, "channel_id")
			requireNoMetricLabel(t, metric, "channelID")
		}
	}
	requireNoMetricFamily(t, families, "wukongim_conversation_authority_uid")
	requireNoMetricFamily(t, families, "wukongim_conversation_authority_channel_id")
}

func TestConversationMetricsTrackActiveCacheAndFlush(t *testing.T) {
	reg := New(11, "node-11")

	reg.Conversation.SetActiveCache(12, 5, 2*time.Second)
	reg.Conversation.ObserveActiveFlush("ok", 4, 3, 7*time.Millisecond)

	families, err := reg.Gather()
	require.NoError(t, err)

	rows := requireMetricFamily(t, families, "wukongim_conversation_active_cache_rows")
	require.Equal(t, float64(12), findMetricByLabels(t, rows, map[string]string{
		"node_id":   "11",
		"node_name": "node-11",
	}).GetGauge().GetValue())

	dirty := requireMetricFamily(t, families, "wukongim_conversation_active_cache_dirty_rows")
	require.Equal(t, float64(5), findMetricByLabels(t, dirty, map[string]string{
		"node_id":   "11",
		"node_name": "node-11",
	}).GetGauge().GetValue())

	age := requireMetricFamily(t, families, "wukongim_conversation_active_cache_oldest_dirty_age_seconds")
	require.Equal(t, float64(2), findMetricByLabels(t, age, map[string]string{
		"node_id":   "11",
		"node_name": "node-11",
	}).GetGauge().GetValue())

	total := requireMetricFamily(t, families, "wukongim_conversation_active_flush_total")
	require.Equal(t, float64(1), findMetricByLabels(t, total, map[string]string{
		"node_id":   "11",
		"node_name": "node-11",
		"result":    "ok",
	}).GetCounter().GetValue())

	flushRows := requireMetricFamily(t, families, "wukongim_conversation_active_flush_rows")
	require.Equal(t, float64(4), findMetricByLabels(t, flushRows, map[string]string{
		"node_id":   "11",
		"node_name": "node-11",
		"result":    "ok",
		"kind":      "selected",
	}).GetHistogram().GetSampleSum())
	require.Equal(t, float64(3), findMetricByLabels(t, flushRows, map[string]string{
		"node_id":   "11",
		"node_name": "node-11",
		"result":    "ok",
		"kind":      "flushed",
	}).GetHistogram().GetSampleSum())

	duration := requireMetricFamily(t, families, "wukongim_conversation_active_flush_duration_seconds")
	require.Equal(t, uint64(1), findMetricByLabels(t, duration, map[string]string{
		"node_id":   "11",
		"node_name": "node-11",
		"result":    "ok",
	}).GetHistogram().GetSampleCount())
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

func requireNoMetricFamily(t *testing.T, families []*dto.MetricFamily, name string) {
	t.Helper()
	for _, family := range families {
		if family.GetName() == name {
			t.Fatalf("metric family %q found, want absent", name)
		}
	}
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

func requireNoMetricLabel(t *testing.T, metric *dto.Metric, name string) {
	t.Helper()
	for _, label := range metric.GetLabel() {
		require.NotEqual(t, name, label.GetName())
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
