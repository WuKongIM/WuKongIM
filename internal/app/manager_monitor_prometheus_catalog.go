package app

import (
	"fmt"
	"strings"

	accessmanager "github.com/WuKongIM/WuKongIM/internal/access/manager"
)

func managerAdditionalMonitorMetricDefinitions() []monitorMetricDefinition {
	definitions := []monitorMetricDefinition{
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryGateway,
			"gatewayDeliveryRate",
			accessmanager.RealtimeMonitorStageSendEntry,
			accessmanager.RealtimeMonitorToneNormal,
			"msg/s",
			[]string{"protocol"},
			8,
			monitorSummaryLatestSum,
			prometheusZeroFallback("sum by (protocol) (rate(wukongim_gateway_messages_delivered_total[%s]))"),
		),
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryGateway,
			"gatewayTransportWriteLatencyP99",
			accessmanager.RealtimeMonitorStageSendEntry,
			accessmanager.RealtimeMonitorToneWarning,
			"ms",
			[]string{"frame_type"},
			8,
			monitorSummaryLatestMax,
			"histogram_quantile(0.99, sum by (le, frame_type) (rate(wukongim_gateway_transport_write_duration_seconds_bucket{result=\"ok\"}[%s]))) * 1000",
		),
	}
	definitions = append(definitions, managerInternalMonitorMetricDefinitions()...)
	definitions = append(definitions, managerMessageMonitorMetricDefinitions()...)
	definitions = append(definitions, managerConversationMonitorMetricDefinitions()...)
	definitions = append(definitions, managerChannelMonitorMetricDefinitions()...)
	definitions = append(definitions, managerDatabaseMonitorMetricDefinitions()...)
	definitions = append(definitions, managerControlMonitorMetricDefinitions()...)
	definitions = append(definitions, managerSlotMonitorMetricDefinitions()...)
	definitions = append(definitions, managerNodeMonitorMetricDefinitions()...)
	definitions = append(definitions, managerGoroutineMonitorMetricDefinitions()...)
	return definitions
}

func managerInternalMonitorMetricDefinitions() []monitorMetricDefinition {
	return []monitorMetricDefinition{
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryInternal,
			"rpcClientErrorRate",
			accessmanager.RealtimeMonitorStageIncidentClosure,
			accessmanager.RealtimeMonitorToneCritical,
			"%",
			[]string{"target_node", "service"},
			8,
			monitorSummaryLatestMax,
			"("+prometheusZeroWhenPresentBy(
				"sum by (target_node, service) (rate(wukongim_transport_rpc_client_total{result!=\"ok\"}[%s]))",
				"sum by (target_node, service) (rate(wukongim_transport_rpc_client_total[%s]))",
				"target_node", "service",
			)+" / clamp_min(sum by (target_node, service) (rate(wukongim_transport_rpc_client_total[%s])), 1)) * 100",
		),
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryInternal,
			"rpcClientLatencyP99",
			accessmanager.RealtimeMonitorStageInternalNetwork,
			accessmanager.RealtimeMonitorToneWarning,
			"ms",
			[]string{"target_node", "service"},
			8,
			monitorSummaryLatestMax,
			"histogram_quantile(0.99, sum by (le, target_node, service) (rate(wukongim_transport_rpc_client_duration_seconds_bucket[%s]))) * 1000",
		),
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryInternal,
			"transportEnqueueErrorRate",
			accessmanager.RealtimeMonitorStageRuntimePressure,
			accessmanager.RealtimeMonitorToneCritical,
			"events/s",
			[]string{"target_node", "kind", "result"},
			8,
			monitorSummaryLatestSum,
			prometheusZeroFallback("sum by (target_node, kind, result) (rate(wukongim_transport_enqueue_total{result!=\"ok\"}[%s]))"),
		),
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryInternal,
			"transportConnectionPool",
			accessmanager.RealtimeMonitorStageInternalNetwork,
			accessmanager.RealtimeMonitorToneWarning,
			"connections",
			[]string{"peer_node", "state"},
			8,
			monitorSummaryLatestSum,
			prometheusAnySeries(
				`sum by (peer_node, state) (label_replace(wukongim_transport_connections_pool_active, "state", "active", "__name__", ".*"))`,
				`sum by (peer_node, state) (label_replace(wukongim_transport_connections_pool_idle, "state", "idle", "__name__", ".*"))`,
			),
		),
		monitorCatalogMetric(
			accessmanager.RealtimeMonitorCategoryInternal,
			"transportWriteBatchFrames",
			accessmanager.RealtimeMonitorStageInternalNetwork,
			accessmanager.RealtimeMonitorToneNormal,
			"frames/batch",
			"sum(rate(wukongim_transport_write_frames_total[%s])) / clamp_min(sum(rate(wukongim_transport_write_batches_total[%s])), 1)",
		),
	}
}

func managerMessageMonitorMetricDefinitions() []monitorMetricDefinition {
	return []monitorMetricDefinition{
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryMessage,
			"messageAppendErrorBreakdown",
			accessmanager.RealtimeMonitorStageErrorClosure,
			accessmanager.RealtimeMonitorToneCritical,
			"events/s",
			[]string{"path", "class"},
			8,
			monitorSummaryLatestSum,
			prometheusZeroFallback("sum by (path, class) (rate(wukongim_message_append_errors_total[%s]))"),
		),
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryMessage,
			"messageSendBatchStageLatencyP99",
			accessmanager.RealtimeMonitorStageAppendCommit,
			accessmanager.RealtimeMonitorToneWarning,
			"ms",
			[]string{"stage"},
			8,
			monitorSummaryLatestMax,
			"histogram_quantile(0.99, sum by (le, stage) (rate(wukongim_message_send_batch_stage_item_duration_seconds_bucket{result=\"ok\"}[%s]))) * 1000",
		),
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryMessage,
			"messageEventRate",
			accessmanager.RealtimeMonitorStageAppendCommit,
			accessmanager.RealtimeMonitorToneNormal,
			"events/s",
			[]string{"operation", "path", "event_type"},
			8,
			monitorSummaryLatestSum,
			prometheusAnySeries(
				`sum by (operation, path, event_type) (label_replace(rate(wukongim_message_event_append_total[%s]), "operation", "append", "__name__", ".*"))`,
				`sum by (operation, path) (label_replace(rate(wukongim_message_event_propose_total[%s]), "operation", "propose", "__name__", ".*"))`,
			),
		),
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryMessage,
			"messageEventErrorRate",
			accessmanager.RealtimeMonitorStageErrorClosure,
			accessmanager.RealtimeMonitorToneCritical,
			"events/s",
			[]string{"operation", "path", "event_type", "result"},
			8,
			monitorSummaryLatestSum,
			prometheusAnySeries(
				`sum by (operation, path, event_type, result) (label_replace(rate(wukongim_message_event_append_total{result!="ok"}[%s]), "operation", "append", "__name__", ".*"))`,
				`sum by (operation, path, result) (label_replace(rate(wukongim_message_event_propose_total{result!="ok"}[%s]), "operation", "propose", "__name__", ".*"))`,
			),
		),
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryMessage,
			"messageEventStageLatencyP99",
			accessmanager.RealtimeMonitorStageAppendCommit,
			accessmanager.RealtimeMonitorToneWarning,
			"ms",
			[]string{"operation", "path", "stage"},
			8,
			monitorSummaryLatestMax,
			prometheusAnySeries(
				`label_replace(histogram_quantile(0.99, sum by (le, path, stage) (rate(wukongim_message_event_append_stage_duration_seconds_bucket{result="ok"}[%s]))) * 1000, "operation", "append", "__name__", ".*")`,
				`label_replace(histogram_quantile(0.99, sum by (le, path, stage) (rate(wukongim_message_event_propose_stage_duration_seconds_bucket{result="ok"}[%s]))) * 1000, "operation", "propose", "__name__", ".*")`,
			),
		),
		monitorCatalogMetric(
			accessmanager.RealtimeMonitorCategoryMessage,
			"messageEventStreamCacheUsage",
			accessmanager.RealtimeMonitorStageRuntimePressure,
			accessmanager.RealtimeMonitorToneWarning,
			"%",
			prometheusZeroFallback("(sum(wukongim_message_event_stream_cache_sessions) / clamp_min(sum(wukongim_message_event_stream_cache_max_sessions), 1)) * 100"),
		),
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryMessage,
			"messageCommittedReplayLag",
			accessmanager.RealtimeMonitorStageOfflineRetry,
			accessmanager.RealtimeMonitorToneWarning,
			"messages",
			[]string{"channel_type"},
			8,
			monitorSummaryLatestSum,
			prometheusZeroFallback("sum by (channel_type) (wukongim_message_committed_replay_lag_messages)"),
		),
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryMessage,
			"messageCommittedReplayLatencyP99",
			accessmanager.RealtimeMonitorStageOfflineRetry,
			accessmanager.RealtimeMonitorToneWarning,
			"ms",
			[]string{"result"},
			8,
			monitorSummaryLatestMax,
			"histogram_quantile(0.99, sum by (le, result) (rate(wukongim_message_committed_replay_pass_duration_seconds_bucket[%s]))) * 1000",
		),
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryMessage,
			"deliveryErrorRate",
			accessmanager.RealtimeMonitorStageErrorClosure,
			accessmanager.RealtimeMonitorToneCritical,
			"events/s",
			[]string{"class"},
			8,
			monitorSummaryLatestSum,
			prometheusZeroFallback("sum by (class) (rate(wukongim_delivery_errors_total[%s]))"),
		),
		monitorCatalogMetric(
			accessmanager.RealtimeMonitorCategoryMessage,
			"deliveryRecipientWorkerUsage",
			accessmanager.RealtimeMonitorStageRuntimePressure,
			accessmanager.RealtimeMonitorToneWarning,
			"%",
			prometheusZeroFallback("(sum(wukongim_delivery_recipient_worker_inflight) / clamp_min(sum(wukongim_delivery_recipient_worker_capacity), 1)) * 100"),
		),
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryMessage,
			"deliveryRecipientAdmissionWaitP99",
			accessmanager.RealtimeMonitorStageRuntimePressure,
			accessmanager.RealtimeMonitorToneWarning,
			"ms",
			[]string{"result"},
			8,
			monitorSummaryLatestMax,
			"histogram_quantile(0.99, sum by (le, result) (rate(wukongim_delivery_recipient_worker_admission_wait_seconds_bucket[%s]))) * 1000",
		),
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryMessage,
			"deliveryAckFailureRate",
			accessmanager.RealtimeMonitorStageErrorClosure,
			accessmanager.RealtimeMonitorToneCritical,
			"events/s",
			[]string{"failure", "phase", "outcome"},
			8,
			monitorSummaryLatestSum,
			prometheusAnySeries(
				`sum by (failure, phase, outcome) (label_replace(rate(wukongim_delivery_ack_batch_rejected_total[%s]), "failure", "rejected", "__name__", ".*"))`,
				`sum by (failure, phase, outcome) (label_replace(rate(wukongim_delivery_ack_batch_rollback_total[%s]), "failure", "rollback", "__name__", ".*"))`,
			),
		),
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryMessage,
			"presenceEndpointLookupErrorRate",
			accessmanager.RealtimeMonitorStageErrorClosure,
			accessmanager.RealtimeMonitorToneCritical,
			"events/s",
			[]string{"path", "outcome", "stale_retry"},
			8,
			monitorSummaryLatestSum,
			prometheusZeroFallback("sum by (path, outcome, stale_retry) (rate(wukongim_presence_endpoint_lookup_total{outcome!=\"ok\"}[%s]))"),
		),
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryMessage,
			"presenceEndpointLookupLatencyP99",
			accessmanager.RealtimeMonitorStageOnlineDelivery,
			accessmanager.RealtimeMonitorToneWarning,
			"ms",
			[]string{"path"},
			8,
			monitorSummaryLatestMax,
			"histogram_quantile(0.99, sum by (le, path) (rate(wukongim_presence_endpoint_lookup_duration_seconds_bucket{outcome=\"ok\"}[%s]))) * 1000",
		),
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryMessage,
			"presenceMaintenanceErrorRate",
			accessmanager.RealtimeMonitorStageErrorClosure,
			accessmanager.RealtimeMonitorToneCritical,
			"events/s",
			[]string{"operation", "result", "budget_reached"},
			8,
			monitorSummaryLatestSum,
			prometheusAnySeries(
				`sum by (operation, result) (label_replace(rate(wukongim_presence_expiry_total{result!="ok"}[%s]), "operation", "expiry", "__name__", ".*"))`,
				`sum by (operation, result, budget_reached) (label_replace(rate(wukongim_presence_touch_flush_total{result!="ok"}[%s]), "operation", "touch_flush", "__name__", ".*"))`,
				`sum by (operation, result, budget_reached) (label_replace(rate(wukongim_presence_touch_flush_total{budget_reached="true"}[%s]), "operation", "touch_budget", "__name__", ".*"))`,
			),
		),
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryMessage,
			"presenceMaintenanceLatencyP99",
			accessmanager.RealtimeMonitorStageOnlineDelivery,
			accessmanager.RealtimeMonitorToneWarning,
			"ms",
			[]string{"operation", "result"},
			8,
			monitorSummaryLatestMax,
			prometheusAnySeries(
				`label_replace(histogram_quantile(0.99, sum by (le, result) (rate(wukongim_presence_expiry_duration_seconds_bucket[%s]))) * 1000, "operation", "expiry", "__name__", ".*")`,
				`label_replace(histogram_quantile(0.99, sum by (le, result) (rate(wukongim_presence_touch_flush_duration_seconds_bucket[%s]))) * 1000, "operation", "touch_flush", "__name__", ".*")`,
			),
		),
	}
}

func managerConversationMonitorMetricDefinitions() []monitorMetricDefinition {
	return []monitorMetricDefinition{
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryConversation,
			"conversationHydrationErrorRate",
			accessmanager.RealtimeMonitorStageIncidentClosure,
			accessmanager.RealtimeMonitorToneCritical,
			"events/s",
			[]string{"result"}, 8, monitorSummaryLatestSum,
			prometheusZeroFallback("sum by (result) (rate(wukongim_conversation_hydration_batch_total{result!=\"ok\"}[%s]))"),
		),
		monitorCatalogLabeledMetric(
			accessmanager.RealtimeMonitorCategoryConversation,
			"conversationHydrationBatchItemsP95",
			accessmanager.RealtimeMonitorStageConversationSync,
			accessmanager.RealtimeMonitorToneNormal,
			"items",
			[]string{"result"}, 8, monitorSummaryLatestMax,
			"histogram_quantile(0.95, sum by (le, result) (rate(wukongim_conversation_hydration_batch_items_bucket[%s])))",
		),
	}
}

func managerChannelMonitorMetricDefinitions() []monitorMetricDefinition {
	category := accessmanager.RealtimeMonitorCategoryChannel
	replication := accessmanager.RealtimeMonitorStageChannelReplication
	pressure := accessmanager.RealtimeMonitorStageRuntimePressure
	incident := accessmanager.RealtimeMonitorStageIncidentClosure
	return []monitorMetricDefinition{
		monitorCatalogMetric(category, "channelCapacityUsage", pressure, accessmanager.RealtimeMonitorToneWarning, "%", "(((sum(wukongim_channel_active_channels) / clamp_min(sum(wukongim_channel_max_channels), 1)) * 100) and on() (sum(wukongim_channel_max_channels) > 0)) or on() (sum(wukongim_channel_max_channels) * 0)"),
		monitorCatalogMetric(category, "channelExecutionQueueDepth", pressure, accessmanager.RealtimeMonitorToneWarning, "items", prometheusZeroFallback("sum(wukongim_channel_execution_queue_depth)")),
		monitorCatalogMetric(category, "channelExecutionWorkerBusy", pressure, accessmanager.RealtimeMonitorToneWarning, "%", prometheusZeroFallback("max(wukongim_channel_execution_worker_busy_ratio) * 100")),
		monitorCatalogLabeledMetric(category, "channelExecutionEnqueueErrorRate", incident, accessmanager.RealtimeMonitorToneCritical, "events/s", []string{"result"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (result) (rate(wukongim_channel_execution_enqueue_total{result!=\"ok\"}[%s]))")),
		monitorCatalogMetric(category, "channelExecutionMailboxWaitP99", pressure, accessmanager.RealtimeMonitorToneWarning, "ms", "histogram_quantile(0.99, sum(rate(wukongim_channel_execution_mailbox_wait_duration_seconds_bucket[%s])) by (le)) * 1000"),
		monitorCatalogLabeledMetric(category, "channelISRAnomalies", incident, accessmanager.RealtimeMonitorToneCritical, "channels", []string{"reason"}, 8, monitorSummaryLatestSum, channelRuntimePrometheusZeroFallback("sum by (reason) (wukongim_channelv2_isr_anomaly_channels)")),
		monitorCatalogLabeledMetric(category, "channelWorkerQueueUsage", pressure, accessmanager.RealtimeMonitorToneWarning, "%", []string{"pool"}, 8, monitorSummaryLatestMax, channelRuntimePrometheusZeroFallback("(sum by (pool) (wukongim_channelv2_worker_queue_depth) / clamp_min(sum by (pool) (wukongim_channelv2_worker_queue_capacity), 1)) * 100")),
		monitorCatalogLabeledMetric(category, "channelWorkerAdmissionErrorRate", incident, accessmanager.RealtimeMonitorToneCritical, "events/s", []string{"pool", "result"}, 8, monitorSummaryLatestSum, channelRuntimePrometheusZeroFallback("sum by (pool, result) (rate(wukongim_channelv2_worker_admission_total{result!=\"ok\"}[%s]))")),
		monitorCatalogLabeledMetric(category, "channelPullErrorRate", incident, accessmanager.RealtimeMonitorToneCritical, "events/s", []string{"result", "empty"}, 8, monitorSummaryLatestSum, channelRuntimePrometheusZeroFallback("sum by (result, empty) (rate(wukongim_channelv2_pull_total{result!=\"ok\"}[%s]))")),
		monitorCatalogLabeledMetric(category, "channelPullLatencyP99", replication, accessmanager.RealtimeMonitorToneWarning, "ms", []string{"stage", "result"}, 8, monitorSummaryLatestMax, channelRuntimePrometheusFallback("histogram_quantile(0.99, sum by (le, stage, result) (rate(wukongim_channelv2_pull_batch_duration_seconds_bucket[%s]))) * 1000")),
		monitorCatalogMetric(category, "channelPendingMeta", replication, accessmanager.RealtimeMonitorToneWarning, "channels", channelRuntimePrometheusZeroFallback("sum(wukongim_channelv2_pending_meta_current)")),
		monitorCatalogClusterLabeledMetric(category, "channelMetaCreateQueueDepth", pressure, accessmanager.RealtimeMonitorToneWarning, "channels", []string{"slot_id"}, 8, monitorSummaryLatestSum, channelRuntimePrometheusFallback("sum by (slot_id) (wukongim_channelv2_meta_create_queue_depth)")),
		monitorCatalogClusterLabeledMetric(category, "channelMetaCreateErrorRate", incident, accessmanager.RealtimeMonitorToneCritical, "events/s", []string{"slot_id", "result"}, 8, monitorSummaryLatestSum, channelRuntimePrometheusFallback("sum by (slot_id, result) (rate(wukongim_channelv2_meta_created_total{result=\"error\"}[%s]))")),
		monitorCatalogMetric(category, "channelAppendBatchWaitP99", replication, accessmanager.RealtimeMonitorToneWarning, "ms", channelRuntimePrometheusFallback("histogram_quantile(0.99, sum(rate(wukongim_channelv2_append_batch_wait_duration_seconds_bucket[%s])) by (le)) * 1000")),
		monitorCatalogMetric(category, "channelRouterGroupUsage", pressure, accessmanager.RealtimeMonitorToneWarning, "%", prometheusZeroFallback("(sum(wukongim_channelappend_router_group_inflight) / clamp_min(sum(wukongim_channelappend_router_group_capacity), 1)) * 100")),
		monitorCatalogLabeledMetric(category, "channelRouterErrorRate", incident, accessmanager.RealtimeMonitorToneCritical, "events/s", []string{"path", "result"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (path, result) (rate(wukongim_channelappend_router_total{result!=\"ok\"}[%s]))")),
		monitorCatalogLabeledMetric(category, "channelRouterLatencyP99", replication, accessmanager.RealtimeMonitorToneWarning, "ms", []string{"path", "result"}, 8, monitorSummaryLatestMax, "histogram_quantile(0.99, sum by (le, path, result) (rate(wukongim_channelappend_router_item_duration_seconds_bucket[%s]))) * 1000"),
		monitorCatalogMetric(category, "channelPostCommitHandoffUsage", pressure, accessmanager.RealtimeMonitorToneWarning, "%", prometheusZeroFallback("(sum(wukongim_channelappend_post_commit_handoff_depth) / clamp_min(sum(wukongim_channelappend_post_commit_handoff_capacity), 1)) * 100")),
		monitorCatalogMetric(category, "channelPostCommitRetryDepth", incident, accessmanager.RealtimeMonitorToneCritical, "writers", prometheusZeroFallback("sum(wukongim_channelappend_post_commit_retry_queue_depth)")),
		monitorCatalogLabeledMetric(category, "channelEffectPoolUsage", pressure, accessmanager.RealtimeMonitorToneWarning, "%", []string{"stage"}, 8, monitorSummaryLatestMax, prometheusZeroFallback("(sum by (stage) (wukongim_channelappend_effect_pool_inflight) / clamp_min(sum by (stage) (wukongim_channelappend_effect_pool_capacity), 1)) * 100")),
		monitorCatalogLabeledMetric(category, "channelEffectErrorRate", incident, accessmanager.RealtimeMonitorToneCritical, "events/s", []string{"stage", "result"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (stage, result) (rate(wukongim_channelappend_effect_total{result!=\"ok\"}[%s]))")),
	}
}

func managerDatabaseMonitorMetricDefinitions() []monitorMetricDefinition {
	category := accessmanager.RealtimeMonitorCategoryDatabase
	pressure := accessmanager.RealtimeMonitorStageRuntimePressure
	return []monitorMetricDefinition{
		monitorCatalogLabeledMetric(category, "storageMemtableUsage", pressure, accessmanager.RealtimeMonitorToneWarning, "B", []string{"store"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (store) (wukongim_storage_pebble_memtable_size_bytes)")),
		monitorCatalogLabeledMetric(category, "storageWALPhysicalSize", pressure, accessmanager.RealtimeMonitorToneWarning, "B", []string{"store"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (store) (wukongim_storage_pebble_wal_physical_size_bytes)")),
		monitorCatalogLabeledMetric(category, "storageSSTSize", pressure, accessmanager.RealtimeMonitorToneNormal, "B", []string{"store"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (store) (wukongim_storage_pebble_sstable_size_bytes)")),
		monitorCatalogLabeledMetric(category, "storageWALAmplification", pressure, accessmanager.RealtimeMonitorToneWarning, "x", []string{"store"}, 8, monitorSummaryLatestMax, prometheusZeroFallback("sum by (store) (rate(wukongim_storage_pebble_wal_bytes_written[%s])) / clamp_min(sum by (store) (rate(wukongim_storage_pebble_wal_bytes_in[%s])), 1)")),
		monitorCatalogLabeledMetric(category, "storageFlushThroughput", accessmanager.RealtimeMonitorStageAppendCommit, accessmanager.RealtimeMonitorToneNormal, "B/s", []string{"store"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (store) (clamp_min(rate(wukongim_storage_pebble_flush_bytes_written[%s]), 0))")),
		monitorCatalogLabeledMetric(category, "storageCompactionReadThroughput", pressure, accessmanager.RealtimeMonitorToneNormal, "B/s", []string{"store"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (store) (clamp_min(rate(wukongim_storage_pebble_compaction_bytes_read[%s]), 0))")),
		monitorCatalogLabeledMetric(category, "storageCompactionWriteThroughput", pressure, accessmanager.RealtimeMonitorToneNormal, "B/s", []string{"store"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (store) (clamp_min(rate(wukongim_storage_pebble_compaction_bytes_written[%s]), 0))")),
		monitorCatalogLabeledMetric(category, "storageBackgroundJobs", pressure, accessmanager.RealtimeMonitorToneWarning, "jobs", []string{"store", "job"}, 8, monitorSummaryLatestSum, prometheusAnySeries(
			`sum by (store, job) (label_replace(wukongim_storage_pebble_flushes_in_progress, "job", "flush", "__name__", ".*"))`,
			`sum by (store, job) (label_replace(wukongim_storage_pebble_compactions_in_progress, "job", "compaction", "__name__", ".*"))`,
		)),
		monitorCatalogLabeledMetric(category, "storageCompactionInflightBytes", pressure, accessmanager.RealtimeMonitorToneWarning, "B", []string{"store"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (store) (wukongim_storage_pebble_compaction_in_progress_bytes)")),
		monitorCatalogLabeledMetric(category, "channelStoreOwnership", pressure, accessmanager.RealtimeMonitorToneWarning, "items", []string{"state"}, 8, monitorSummaryLatestSum, prometheusAnySeries(
			`sum by (state) (label_replace(wukongim_storage_channel_entries_active, "state", "active_entries", "__name__", ".*"))`,
			`sum by (state) (label_replace(wukongim_storage_channel_leases_outstanding, "state", "outstanding_leases", "__name__", ".*"))`,
			`sum by (state) (label_replace(wukongim_storage_channel_background_pins, "state", "background_pins", "__name__", ".*"))`,
		)),
	}
}

func managerControlMonitorMetricDefinitions() []monitorMetricDefinition {
	category := accessmanager.RealtimeMonitorCategoryControl
	control := accessmanager.RealtimeMonitorStageControlPlane
	incident := accessmanager.RealtimeMonitorStageIncidentClosure
	return []monitorMetricDefinition{
		monitorCatalogLabeledMetric(category, "controllerDecisionRate", control, accessmanager.RealtimeMonitorToneNormal, "events/s", []string{"type"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (type) (rate(wukongim_controller_decisions_total[%s]))")),
		monitorCatalogMetric(category, "controllerDecisionLatencyP99", control, accessmanager.RealtimeMonitorToneWarning, "ms", "histogram_quantile(0.99, sum(rate(wukongim_controller_decision_duration_seconds_bucket[%s])) by (le)) * 1000"),
		monitorCatalogLabeledMetric(category, "controllerOldestTaskAge", incident, accessmanager.RealtimeMonitorToneCritical, "s", []string{"kind", "status", "step", "source"}, 8, monitorSummaryLatestMax, prometheusZeroFallback("max by (kind, status, step, source) (wukongim_controller_task_oldest_age_seconds)")),
		monitorCatalogLabeledMetric(category, "controllerTaskFailureRate", incident, accessmanager.RealtimeMonitorToneCritical, "events/s", []string{"type", "result"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (type, result) (rate(wukongim_controller_tasks_completed_total{result!=\"ok\"}[%s]))")),
		monitorCatalogMetric(category, "controllerMigrationsActive", control, accessmanager.RealtimeMonitorToneWarning, "migrations", prometheusZeroFallback("sum(wukongim_controller_hashslot_migrations_active)")),
		monitorCatalogLabeledMetric(category, "controllerMigrationFailureRate", incident, accessmanager.RealtimeMonitorToneCritical, "events/s", []string{"result"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (result) (rate(wukongim_controller_hashslot_migrations_total{result!=\"ok\"}[%s]))")),
		monitorCatalogLabeledMetric(category, "controllerRaftMembership", control, accessmanager.RealtimeMonitorToneWarning, "nodes", []string{"role"}, 8, monitorSummaryLatestSum, prometheusAnySeries(
			`sum by (role) (label_replace(wukongim_controller_raft_voters, "role", "voter", "__name__", ".*"))`,
			`sum by (role) (label_replace(wukongim_controller_raft_learners, "role", "learner", "__name__", ".*"))`,
		)),
		monitorCatalogLabeledMetric(category, "controllerVoterPromotionRate", control, accessmanager.RealtimeMonitorToneNormal, "events/s", []string{"result"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (result) (rate(wukongim_controller_voter_promotion_attempts_total[%s]))")),
		monitorCatalogLabeledMetric(category, "controllerVoterPromotionBlockers", incident, accessmanager.RealtimeMonitorToneCritical, "events/s", []string{"reason"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (reason) (rate(wukongim_controller_voter_promotion_blockers_total[%s]))")),
		monitorCatalogLabeledMetric(category, "controllerVoterPromotionLatencyP99", control, accessmanager.RealtimeMonitorToneWarning, "ms", []string{"phase"}, 8, monitorSummaryLatestMax, "histogram_quantile(0.99, sum by (le, phase) (rate(wukongim_controller_voter_promotion_phase_seconds_bucket[%s]))) * 1000"),
		monitorCatalogLabeledMetric(category, "nodeLifecycleState", control, accessmanager.RealtimeMonitorToneWarning, "nodes", []string{"join_state", "status"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (join_state, status) (wukongim_node_lifecycle_nodes)")),
		monitorCatalogLabeledMetric(category, "nodeHealthFreshness", control, accessmanager.RealtimeMonitorToneWarning, "nodes", []string{"freshness", "status"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (freshness, status) (wukongim_node_health_freshness_nodes)")),
		monitorCatalogLabeledMetric(category, "nodeHealthReportAge", incident, accessmanager.RealtimeMonitorToneCritical, "s", []string{"freshness", "status"}, 8, monitorSummaryLatestMax, prometheusZeroFallback("max by (freshness, status) (wukongim_node_health_report_age_seconds)")),
		monitorCatalogLabeledMetric(category, "nodeLifecycleFailureRate", incident, accessmanager.RealtimeMonitorToneCritical, "events/s", []string{"operation", "result"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (operation, result) (rate(wukongim_node_lifecycle_attempts_total{result!=\"ok\",result!=\"noop\"}[%s]))")),
		monitorCatalogLabeledMetric(category, "nodeLifecycleBlockers", incident, accessmanager.RealtimeMonitorToneCritical, "events/s", []string{"reason"}, 8, monitorSummaryLatestSum, "sum by (reason) (rate(wukongim_node_scale_in_blockers_total[%s]))"),
	}
}

func managerSlotMonitorMetricDefinitions() []monitorMetricDefinition {
	category := accessmanager.RealtimeMonitorCategorySlot
	replication := accessmanager.RealtimeMonitorStageSlotReplication
	incident := accessmanager.RealtimeMonitorStageIncidentClosure
	return []monitorMetricDefinition{
		monitorCatalogLabeledMetric(category, "slotPreferredLeaderReconcileRate", replication, accessmanager.RealtimeMonitorToneNormal, "events/s", []string{"decision"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (decision) (rate(wukongim_slot_preferred_leader_reconcile_total[%s]))")),
		monitorCatalogLabeledMetric(category, "slotPreferredLeaderWaitP99", replication, accessmanager.RealtimeMonitorToneWarning, "ms", []string{"decision"}, 8, monitorSummaryLatestMax, "histogram_quantile(0.99, sum by (le, decision) (rate(wukongim_slot_preferred_leader_strict_wait_duration_seconds_bucket[%s]))) * 1000"),
		monitorCatalogLabeledMetric(category, "slotReplicaMoveLatencyP99", replication, accessmanager.RealtimeMonitorToneWarning, "s", []string{"result"}, 8, monitorSummaryLatestMax, "histogram_quantile(0.99, sum by (le, result) (rate(wukongim_slot_replica_move_duration_seconds_bucket[%s])))"),
		monitorCatalogLabeledMetric(category, "slotReplicaMoveFailureRate", incident, accessmanager.RealtimeMonitorToneCritical, "events/s", []string{"reason"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (reason) (rate(wukongim_slot_replica_move_failures_total[%s]))")),
		monitorCatalogLabeledMetric(category, "slotReplicaMovePhaseFailureRate", incident, accessmanager.RealtimeMonitorToneCritical, "events/s", []string{"step", "result"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (step, result) (rate(wukongim_slot_replica_move_phase_observed_total{result!=\"ok\"}[%s]))")),
		monitorCatalogLabeledMetric(category, "slotReplicaMovePhaseLatencyP99", replication, accessmanager.RealtimeMonitorToneWarning, "s", []string{"step", "result"}, 8, monitorSummaryLatestMax, "histogram_quantile(0.99, sum by (le, step, result) (rate(wukongim_slot_replica_move_phase_duration_seconds_bucket[%s])))"),
	}
}

func managerNodeMonitorMetricDefinitions() []monitorMetricDefinition {
	category := accessmanager.RealtimeMonitorCategoryNode
	pressure := accessmanager.RealtimeMonitorStageRuntimePressure
	incident := accessmanager.RealtimeMonitorStageIncidentClosure
	return []monitorMetricDefinition{
		monitorCatalogLabeledMetric(category, "nodeThreads", pressure, accessmanager.RealtimeMonitorToneWarning, "threads", []string{"node_id", "node_name"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("max by (node_id, node_name) (wukongim_node_threads)")),
		monitorCatalogLabeledMetric(category, "nodeAntsPoolUsage", pressure, accessmanager.RealtimeMonitorToneWarning, "%", []string{"component", "pool"}, 8, monitorSummaryLatestMax, prometheusZeroFallback("max by (component, pool) (wukongim_ants_pool_utilization) * 100")),
		monitorCatalogLabeledMetric(category, "nodeAntsPoolWaiting", pressure, accessmanager.RealtimeMonitorToneWarning, "tasks", []string{"component", "pool"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (component, pool) (wukongim_ants_pool_waiting)")),
		monitorCatalogLabeledMetric(category, "runtimePoolWaitP99", pressure, accessmanager.RealtimeMonitorToneWarning, "ms", []string{"component", "pool", "queue", "priority", "result"}, 8, monitorSummaryLatestMax, "histogram_quantile(0.99, sum by (le, component, pool, queue, priority, result) (rate(wukongim_runtime_pool_wait_duration_seconds_bucket[%s]))) * 1000"),
		monitorCatalogLabeledMetric(category, "runtimePoolTaskP99", pressure, accessmanager.RealtimeMonitorToneWarning, "ms", []string{"component", "pool", "task", "result"}, 8, monitorSummaryLatestMax, "histogram_quantile(0.99, sum by (le, component, pool, task, result) (rate(wukongim_runtime_pool_task_duration_seconds_bucket[%s]))) * 1000"),
		monitorCatalogLabeledMetric(category, "runtimePoolAdmissionErrorRate", incident, accessmanager.RealtimeMonitorToneCritical, "events/s", []string{"component", "pool", "queue", "priority", "result"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (component, pool, queue, priority, result) (rate(wukongim_runtime_pool_admission_total{result!=\"ok\"}[%s]))")),
		monitorCatalogLabeledMetric(category, "runtimePoolInflightUsage", pressure, accessmanager.RealtimeMonitorToneWarning, "%", []string{"component", "pool"}, 8, monitorSummaryLatestMax, prometheusZeroFallback("(sum by (component, pool) (wukongim_runtime_pool_inflight) / clamp_min(sum by (component, pool) (wukongim_runtime_pool_workers), 1)) * 100")),
		monitorCatalogLabeledMetric(category, "runtimePoolQueueBytesUsage", pressure, accessmanager.RealtimeMonitorToneWarning, "%", []string{"component", "pool", "queue", "priority"}, 8, monitorSummaryLatestMax, prometheusZeroFallback("(sum by (component, pool, queue, priority) (wukongim_runtime_pool_queue_bytes) / clamp_min(sum by (component, pool, queue, priority) (wukongim_runtime_pool_queue_bytes_capacity), 1)) * 100")),
		monitorCatalogMetric(category, "diagnosticsBufferUsage", pressure, accessmanager.RealtimeMonitorToneWarning, "%", prometheusZeroFallback("max(wukongim_diagnostics_buffer_usage_ratio) * 100")),
		monitorCatalogLabeledMetric(category, "diagnosticsDroppedRate", incident, accessmanager.RealtimeMonitorToneCritical, "events/s", []string{"reason"}, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (reason) (rate(wukongim_diagnostics_events_dropped_total[%s]))")),
	}
}

func managerGoroutineMonitorMetricDefinitions() []monitorMetricDefinition {
	category := accessmanager.RealtimeMonitorCategoryGoroutines
	pressure := accessmanager.RealtimeMonitorStageRuntimePressure
	incident := accessmanager.RealtimeMonitorStageIncidentClosure
	labels := []string{"module", "task", "kind"}
	return []monitorMetricDefinition{
		monitorCatalogLabeledMetric(category, "goroutineStartRate", pressure, accessmanager.RealtimeMonitorToneNormal, "starts/s", labels, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (module, task, kind) (rate(wukongim_goroutines_started_total[%s]))")),
		monitorCatalogLabeledMetric(category, "goroutinePanicRate", incident, accessmanager.RealtimeMonitorToneCritical, "panics/s", labels, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (module, task, kind) (rate(wukongim_goroutines_panics_total[%s]))")),
		monitorCatalogLabeledMetric(category, "goroutinePoolBusy", pressure, accessmanager.RealtimeMonitorToneWarning, "%", labels, 8, monitorSummaryLatestMax, prometheusZeroFallback("(sum by (module, task, kind) (wukongim_goroutine_pool_busy_tasks) / clamp_min(sum by (module, task, kind) (wukongim_goroutine_pool_capacity), 1)) * 100")),
		monitorCatalogLabeledMetric(category, "goroutinePoolQueueDepth", pressure, accessmanager.RealtimeMonitorToneWarning, "tasks", labels, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (module, task, kind) (wukongim_goroutine_pool_queue_depth)")),
		monitorCatalogLabeledMetric(category, "goroutinePoolRejectionRate", incident, accessmanager.RealtimeMonitorToneCritical, "events/s", labels, 8, monitorSummaryLatestSum, prometheusZeroFallback("sum by (module, task, kind) (rate(wukongim_goroutine_pool_rejected_total[%s]))")),
	}
}

func monitorCatalogMetric(category, key, stage, tone, unit, pattern string) monitorMetricDefinition {
	return monitorCatalogLabeledMetric(category, key, stage, tone, unit, nil, 0, monitorSummaryLatestMax, pattern)
}

func monitorCatalogLabeledMetric(category, key, stage, tone, unit string, labelKeys []string, maxSeries int, summary monitorSeriesSummary, pattern string) monitorMetricDefinition {
	pattern = prometheusRequireSeries(pattern)
	if len(labelKeys) > 0 {
		pattern = strings.ReplaceAll(pattern, ") or vector(0)", ") or on() vector(0)")
	}
	return monitorMetricDefinition{
		key:             key,
		category:        category,
		stage:           stage,
		tone:            tone,
		unit:            unit,
		seriesLabelKeys: labelKeys,
		maxSeries:       maxSeries,
		summary:         summary,
		query: func(rateWindow string) string {
			count := strings.Count(pattern, "%s")
			if count == 0 {
				return pattern
			}
			args := make([]any, count)
			for index := range args {
				args[index] = rateWindow
			}
			return fmt.Sprintf(pattern, args...)
		},
	}
}

func monitorCatalogClusterLabeledMetric(category, key, stage, tone, unit string, labelKeys []string, maxSeries int, summary monitorSeriesSummary, pattern string) monitorMetricDefinition {
	definition := monitorCatalogLabeledMetric(category, key, stage, tone, unit, labelKeys, maxSeries, summary, pattern)
	definition.clusterScoped = true
	return definition
}

func prometheusRequireSeries(pattern string) string {
	for _, suffix := range []string{") or vector(0))", ") or on() vector(0))"} {
		if strings.HasPrefix(pattern, "((") && strings.HasSuffix(pattern, suffix) {
			return strings.TrimSuffix(strings.TrimPrefix(pattern, "(("), suffix)
		}
	}
	return pattern
}
