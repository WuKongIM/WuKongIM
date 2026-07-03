package app

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	accessmanager "github.com/WuKongIM/WuKongIM/internal/access/manager"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

type managerClusterControlReader interface {
	// ListNodes returns the bounded manager node inventory snapshot.
	ListNodes(context.Context) (managementusecase.NodeList, error)
	// ListSlots returns the bounded manager slot inventory snapshot.
	ListSlots(context.Context, managementusecase.ListSlotsOptions) ([]managementusecase.Slot, error)
}

type clusterMonitorMetricDefinition struct {
	key       string
	category  string
	stage     string
	tone      string
	unit      string
	query     func(rateWindow string) string
	transform func(float64) float64
	nodeStats bool
}

type managerClusterControlSnapshot struct {
	nodesAlive    int
	totalSlots    int
	slotsReady    int
	leaderMissing int
	quorumLost    int
	ok            bool
}

func (p *managerPrometheusMonitorProvider) clusterCards(ctx context.Context, defs []clusterMonitorMetricDefinition, rateWindow string, start, end time.Time, step time.Duration, nodeID uint64) ([]accessmanager.RealtimeMonitorCard, int, error) {
	cards := make([]accessmanager.RealtimeMonitorCard, 0, len(defs))
	var firstErr error
	var available int
	for _, def := range defs {
		promQL := prometheusFilterNodeID(def.query(rateWindow), nodeID)
		series, stats, err := p.clusterCardSeries(ctx, def, promQL, start, end, step)
		card := accessmanager.RealtimeMonitorCard{
			Key:       def.key,
			Category:  def.category,
			Stage:     def.stage,
			Tone:      def.tone,
			Unit:      def.unit,
			Source:    accessmanager.RealtimeMonitorSourcePrometheus,
			Series:    series,
			Available: len(series) > 0 && err == nil,
		}
		if err != nil {
			card.Error = err.Error()
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", def.key, err)
			}
		} else if len(series) == 0 {
			card.Error = "prometheus returned no data"
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %s", def.key, card.Error)
			}
		} else {
			available++
			card.Value = clusterMonitorLatestValue(series)
			card.Stats = stats
		}
		cards = append(cards, card)
	}
	return cards, available, firstErr
}

func (p *managerPrometheusMonitorProvider) clusterCardSeries(ctx context.Context, def clusterMonitorMetricDefinition, promQL string, start, end time.Time, step time.Duration) ([]accessmanager.RealtimeMonitorPoint, []accessmanager.RealtimeMonitorStat, error) {
	if def.nodeStats {
		results, err := managerMonitorQueryRangeResults(ctx, p.client, p.options.BaseURL, promQL, start, end, step)
		if err != nil {
			return nil, nil, err
		}
		series, stats, err := clusterMonitorNodeSeries(results, def.unit, def.transform)
		if err != nil {
			return nil, nil, err
		}
		if len(stats) == 0 {
			stats = clusterMonitorStats(series, def.unit, step)
		}
		return series, stats, nil
	}
	rawSeries, err := managerMonitorQueryRange(ctx, p.client, p.options.BaseURL, promQL, start, end, step)
	if err != nil {
		return nil, nil, err
	}
	series := clusterMonitorTransformSeries(rawSeries, def.transform)
	return series, clusterMonitorStats(series, def.unit, step), nil
}

func (p *managerPrometheusMonitorProvider) controlSnapshot(ctx context.Context, nodeID uint64) (managerClusterControlSnapshot, accessmanager.RealtimeMonitorSource) {
	if p.options.Control == nil {
		return managerClusterControlSnapshot{}, accessmanager.RealtimeMonitorSource{
			Enabled: false,
			Error:   "control snapshot reader is not configured",
		}
	}
	started := time.Now()
	nodes, err := p.options.Control.ListNodes(ctx)
	if err != nil {
		return managerClusterControlSnapshot{}, accessmanager.RealtimeMonitorSource{
			Enabled: false,
			QueryMS: time.Since(started).Milliseconds(),
			Error:   fmt.Sprintf("read control snapshot nodes: %v", err),
		}
	}
	slots, err := p.options.Control.ListSlots(ctx, managementusecase.ListSlotsOptions{NodeID: nodeID})
	if err != nil {
		return managerClusterControlSnapshot{}, accessmanager.RealtimeMonitorSource{
			Enabled: false,
			QueryMS: time.Since(started).Milliseconds(),
			Error:   fmt.Sprintf("read control snapshot slots: %v", err),
		}
	}
	snapshot := managerClusterControlSnapshot{totalSlots: len(slots), ok: true}
	for _, node := range nodes.Items {
		if nodeID != 0 && node.NodeID != nodeID {
			continue
		}
		if strings.EqualFold(node.Status, "alive") {
			snapshot.nodesAlive++
		}
	}
	for _, slot := range slots {
		leaderMissing := slot.Runtime.PreferredLeaderID == 0
		quorumLost := slot.State.Quorum == "lost"
		if !slot.Runtime.HasQuorum && len(slot.Runtime.CurrentVoters) > 0 {
			quorumLost = true
		}
		if leaderMissing {
			snapshot.leaderMissing++
		}
		if quorumLost {
			snapshot.quorumLost++
		}
		if !leaderMissing && !quorumLost && (slot.Runtime.HasQuorum || slot.State.Quorum == "ready") {
			snapshot.slotsReady++
		}
	}
	return snapshot, accessmanager.RealtimeMonitorSource{
		Enabled: true,
		QueryMS: time.Since(started).Milliseconds(),
	}
}

func clusterMonitorStatus(total, available int, firstErr error, controlSource accessmanager.RealtimeMonitorSource) (string, string) {
	if available == 0 {
		if firstErr != nil {
			return accessmanager.RealtimeMonitorStatusPrometheusUnavailable, firstErr.Error()
		}
		return accessmanager.RealtimeMonitorStatusPrometheusUnavailable, "prometheus returned no cluster monitor series"
	}
	if available < total {
		if firstErr != nil {
			return accessmanager.RealtimeMonitorStatusPartial, firstErr.Error()
		}
		return accessmanager.RealtimeMonitorStatusPartial, ""
	}
	if !controlSource.Enabled {
		return accessmanager.RealtimeMonitorStatusPartial, ""
	}
	return accessmanager.RealtimeMonitorStatusReady, ""
}

func managerClusterMonitorMetricDefinitions() []clusterMonitorMetricDefinition {
	return []clusterMonitorMetricDefinition{
		clusterMetric(accessmanager.RealtimeMonitorCategoryControl, "controllerApplyGap", accessmanager.RealtimeMonitorStageControlPlane, accessmanager.RealtimeMonitorToneWarning, "entries", "max(wukongim_controller_apply_gap)"),
		clusterMetric(accessmanager.RealtimeMonitorCategoryControl, "controllerRaftStepQueueUsage", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "%", prometheusZeroFallback("(max(wukongim_controller_raft_step_queue_depth) / clamp_min(max(wukongim_controller_raft_step_queue_capacity), 1)) * 100")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryControl, "controllerRaftStepEnqueueLatencyP99", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "ms", prometheusZeroFallback("histogram_quantile(0.99, sum(rate(wukongim_controller_raft_step_enqueue_duration_seconds_bucket[%s])) by (le)) * 1000")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryControl, "controllerRaftStepEnqueueErrorRate", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneCritical, "%", prometheusZeroFallback("(sum(rate(wukongim_controller_raft_step_enqueue_duration_seconds_count{result!=\"ok\"}[%s])) / clamp_min(sum(rate(wukongim_controller_raft_step_enqueue_duration_seconds_count[%s])), 1)) * 100")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryControl, "controllerStateRevision", accessmanager.RealtimeMonitorStageControlPlane, accessmanager.RealtimeMonitorToneNormal, "", prometheusZeroFallback("max(wukongim_controller_state_revision)")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryControl, "controllerActiveTasks", accessmanager.RealtimeMonitorStageControlPlane, accessmanager.RealtimeMonitorToneWarning, "", prometheusZeroFallback("sum(max by (type) (wukongim_controller_tasks_active))")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryControl, "controllerFailedTasks", accessmanager.RealtimeMonitorStageIncidentClosure, accessmanager.RealtimeMonitorToneCritical, "", prometheusZeroFallback("sum(max by (type) (wukongim_controller_tasks_failed))")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryControl, "controllerNodesUnhealthy", accessmanager.RealtimeMonitorStageIncidentClosure, accessmanager.RealtimeMonitorToneCritical, "", prometheusZeroFallback("max(wukongim_controller_nodes_suspect) + max(wukongim_controller_nodes_dead)")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryControl, "controllerSlotLeaderSkew", accessmanager.RealtimeMonitorStageControlPlane, accessmanager.RealtimeMonitorToneWarning, "", prometheusZeroFallback("max(wukongim_controller_slot_leader_skew)")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryControl, "controllerLeaderPresent", accessmanager.RealtimeMonitorStageControlPlane, accessmanager.RealtimeMonitorToneNormal, "", prometheusZeroFallback("min(wukongim_controller_leader_present)")),
		clusterMetric(accessmanager.RealtimeMonitorCategorySlot, "slotLeaderStability", accessmanager.RealtimeMonitorStageSlotReplication, accessmanager.RealtimeMonitorToneNormal, "%", "(1 - clamp_max(sum(rate(wukongim_slot_leader_elections_total[%s])) / 10, 1)) * 100"),
		clusterMetric(accessmanager.RealtimeMonitorCategorySlot, "slotProposeRate", accessmanager.RealtimeMonitorStageSlotReplication, accessmanager.RealtimeMonitorToneNormal, "cmd/s", prometheusZeroFallback("sum(rate(wukongim_slot_proposals_total[%s]))")),
		clusterMetric(accessmanager.RealtimeMonitorCategorySlot, "slotApplyGap", accessmanager.RealtimeMonitorStageSlotReplication, accessmanager.RealtimeMonitorToneWarning, "entries", prometheusZeroFallback("max(wukongim_slot_apply_gap)")),
		clusterMetric(accessmanager.RealtimeMonitorCategorySlot, "slotLatencyP99", accessmanager.RealtimeMonitorStageSlotReplication, accessmanager.RealtimeMonitorToneWarning, "ms", prometheusZeroFallback("histogram_quantile(0.99, sum(rate(wukongim_slot_apply_duration_seconds_bucket[%s])) by (le)) * 1000")),
		clusterMetric(accessmanager.RealtimeMonitorCategorySlot, "slotProposalAdmissionRejectRate", accessmanager.RealtimeMonitorStageSlotReplication, accessmanager.RealtimeMonitorToneCritical, "%", prometheusZeroFallback("(sum(rate(wukongim_slot_proposal_admission_total{result!=\"ok\"}[%s])) / clamp_min(sum(rate(wukongim_slot_proposal_admission_total[%s])), 1)) * 100")),
		clusterMetric(accessmanager.RealtimeMonitorCategorySlot, "slotLeaderChangeRate", accessmanager.RealtimeMonitorStageSlotReplication, accessmanager.RealtimeMonitorToneWarning, "events/s", prometheusZeroFallback("sum(rate(wukongim_slot_leader_changes_total[%s]))")),
		clusterMetric(accessmanager.RealtimeMonitorCategorySlot, "slotReplicaLagMax", accessmanager.RealtimeMonitorStageSlotReplication, accessmanager.RealtimeMonitorToneWarning, "s", prometheusZeroFallback("max(wukongim_slot_replica_lag_seconds)")),
		clusterMetric(accessmanager.RealtimeMonitorCategorySlot, "slotSchedulerQueueUsage", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "%", prometheusZeroFallback("(sum(wukongim_runtime_pool_queue_depth{component=\"slot\",pool=\"scheduler\"}) / clamp_min(sum(wukongim_runtime_pool_queue_capacity{component=\"slot\",pool=\"scheduler\"}), 1)) * 100")),
		clusterMetric(accessmanager.RealtimeMonitorCategorySlot, "slotSchedulerInflightUsage", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "%", prometheusZeroFallback("(sum(wukongim_runtime_pool_inflight{component=\"slot\",pool=\"scheduler\"}) / clamp_min(sum(wukongim_runtime_pool_workers{component=\"slot\",pool=\"scheduler\"}), 1)) * 100")),
		clusterMetric(accessmanager.RealtimeMonitorCategorySlot, "slotSchedulerTaskLatencyP99", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "ms", "histogram_quantile(0.99, sum(rate(wukongim_runtime_pool_task_duration_seconds_bucket{component=\"slot\",pool=\"scheduler\"}[%s])) by (le)) * 1000"),
		clusterMetric(accessmanager.RealtimeMonitorCategoryChannel, "channelAppendLatencyP99", accessmanager.RealtimeMonitorStageChannelReplication, accessmanager.RealtimeMonitorToneWarning, "ms", "histogram_quantile(0.99, sum(rate(wukongim_channelv2_append_duration_seconds_bucket[%s])) by (le)) * 1000"),
		clusterMetric(accessmanager.RealtimeMonitorCategoryChannel, "activeChannels", accessmanager.RealtimeMonitorStageChannelReplication, accessmanager.RealtimeMonitorToneNormal, "", prometheusZeroFallback("sum(wukongim_channelv2_active_runtimes)")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryChannel, "channelAppendBatchRecordsP95", accessmanager.RealtimeMonitorStageChannelReplication, accessmanager.RealtimeMonitorToneNormal, "records", "histogram_quantile(0.95, sum(rate(wukongim_channelv2_append_batch_records_bucket[%s])) by (le))"),
		clusterMetric(accessmanager.RealtimeMonitorCategoryChannel, "channelAppendBatchBytesP95", accessmanager.RealtimeMonitorStageChannelReplication, accessmanager.RealtimeMonitorToneNormal, "B", "histogram_quantile(0.95, sum(rate(wukongim_channelv2_append_batch_bytes_bucket[%s])) by (le))"),
		clusterMetric(accessmanager.RealtimeMonitorCategoryChannel, "channelAppendErrorRate", accessmanager.RealtimeMonitorStageChannelReplication, accessmanager.RealtimeMonitorToneCritical, "%", prometheusZeroFallback("(sum(rate(wukongim_channelv2_append_stage_duration_seconds_count{result!=\"ok\"}[%s])) / clamp_min(sum(rate(wukongim_channelv2_append_stage_duration_seconds_count[%s])), 1)) * 100")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryChannel, "channelWriterAdmissionUsage", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "%", prometheusZeroFallback("(sum(wukongim_channelappend_writer_admission_depth) / clamp_min(sum(wukongim_channelappend_writer_admission_capacity), 1)) * 100")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryChannel, "channelRuntimeFollowersParked", accessmanager.RealtimeMonitorStageChannelReplication, accessmanager.RealtimeMonitorToneWarning, "", prometheusZeroFallback("sum(wukongim_channelv2_follower_parked)")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryChannel, "channelActivationRejectRate", accessmanager.RealtimeMonitorStageChannelReplication, accessmanager.RealtimeMonitorToneCritical, "events/s", prometheusZeroFallback("sum(rate(wukongim_channelv2_activation_rejected_total[%s]))")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryChannel, "channelReactorMailboxDepth", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "", prometheusZeroFallback("max(wukongim_channelv2_reactor_mailbox_depth)")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryChannel, "channelWorkerQueueDepth", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "", prometheusZeroFallback("sum(wukongim_channelv2_worker_queue_depth)")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryChannel, "channelPullHintErrorRate", accessmanager.RealtimeMonitorStageChannelReplication, accessmanager.RealtimeMonitorToneCritical, "%", prometheusZeroFallback("(sum(rate(wukongim_channelv2_pull_hint_total{result!=\"ok\"}[%s])) / clamp_min(sum(rate(wukongim_channelv2_pull_hint_total[%s])), 1)) * 100")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryChannel, "channelReplicationLatencyP99", accessmanager.RealtimeMonitorStageChannelReplication, accessmanager.RealtimeMonitorToneWarning, "ms", "histogram_quantile(0.99, sum(rate(wukongim_channelv2_replication_stage_duration_seconds_bucket[%s])) by (le)) * 1000"),
		clusterMetric(accessmanager.RealtimeMonitorCategoryDatabase, "storageWriteP99", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "ms", "histogram_quantile(0.99, sum(rate(wukongim_storage_commit_request_duration_seconds_bucket[%s])) by (le)) * 1000"),
		clusterMetric(accessmanager.RealtimeMonitorCategoryDatabase, "storageCommitErrorRate", accessmanager.RealtimeMonitorStageIncidentClosure, accessmanager.RealtimeMonitorToneCritical, "%", prometheusZeroFallback("(sum(rate(wukongim_storage_commit_request_duration_seconds_count{result!=\"ok\"}[%s])) / clamp_min(sum(rate(wukongim_storage_commit_request_duration_seconds_count[%s])), 1)) * 100")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryDatabase, "storageCommitQueueUsage", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "%", prometheusZeroFallback("(sum(wukongim_runtime_pool_queue_depth{component=\"db\",pool=\"message_commit\",queue=\"commit\"}) / clamp_min(sum(wukongim_runtime_pool_queue_capacity{component=\"db\",pool=\"message_commit\",queue=\"commit\"}), 1)) * 100")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryDatabase, "storagePhysicalCommitP99", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "ms", "histogram_quantile(0.99, sum(rate(wukongim_storage_commit_batch_duration_seconds_bucket{stage=\"commit\"}[%s])) by (le)) * 1000"),
		clusterMetric(accessmanager.RealtimeMonitorCategoryDatabase, "storageCommitBatchRecordsP95", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneNormal, "records", "histogram_quantile(0.95, sum(rate(wukongim_storage_commit_batch_records_bucket[%s])) by (le))"),
		clusterMetric(accessmanager.RealtimeMonitorCategoryDatabase, "storageCommitBatchBytesP95", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneNormal, "B", "histogram_quantile(0.95, sum(rate(wukongim_storage_commit_batch_bytes_bucket[%s])) by (le))"),
		clusterMetric(accessmanager.RealtimeMonitorCategoryDatabase, "storagePebbleDiskUsage", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "B", prometheusZeroFallback("sum(wukongim_storage_pebble_disk_usage_bytes)")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryDatabase, "storagePebbleReadAmplification", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "", prometheusZeroFallback("max(wukongim_storage_pebble_read_amplification)")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryDatabase, "storagePebbleCompactionDebt", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "B", prometheusZeroFallback("sum(wukongim_storage_pebble_compaction_estimated_debt_bytes)")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryInternal, "internalTraffic", accessmanager.RealtimeMonitorStageInternalNetwork, accessmanager.RealtimeMonitorToneNormal, "B/s", "sum(rate(wukongim_transport_sent_bytes_total[%s])) + sum(rate(wukongim_transport_received_bytes_total[%s]))"),
		clusterMetric(accessmanager.RealtimeMonitorCategoryInternal, "rpcSuccessRate", accessmanager.RealtimeMonitorStageInternalNetwork, accessmanager.RealtimeMonitorToneNormal, "%", "(sum(rate(wukongim_transport_rpc_total{result=\"ok\"}[%s])) / clamp_min(sum(rate(wukongim_transport_rpc_total[%s])), 1)) * 100"),
		clusterMetric(accessmanager.RealtimeMonitorCategoryInternal, "rpcLatencyP95", accessmanager.RealtimeMonitorStageInternalNetwork, accessmanager.RealtimeMonitorToneWarning, "ms", "histogram_quantile(0.95, sum(rate(wukongim_transport_rpc_duration_seconds_bucket[%s])) by (le)) * 1000"),
		clusterMetric(accessmanager.RealtimeMonitorCategoryInternal, "internalTxTraffic", accessmanager.RealtimeMonitorStageInternalNetwork, accessmanager.RealtimeMonitorToneNormal, "B/s", prometheusZeroFallback("sum(rate(wukongim_transport_sent_bytes_total[%s]))")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryInternal, "internalRxTraffic", accessmanager.RealtimeMonitorStageInternalNetwork, accessmanager.RealtimeMonitorToneNormal, "B/s", prometheusZeroFallback("sum(rate(wukongim_transport_received_bytes_total[%s]))")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryInternal, "rpcRate", accessmanager.RealtimeMonitorStageInternalNetwork, accessmanager.RealtimeMonitorToneNormal, "calls/s", prometheusZeroFallback("sum(rate(wukongim_transport_rpc_total[%s]))")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryInternal, "rpcErrorRate", accessmanager.RealtimeMonitorStageInternalNetwork, accessmanager.RealtimeMonitorToneCritical, "%", "(sum(rate(wukongim_transport_rpc_total{result!=\"ok\"}[%s])) / clamp_min(sum(rate(wukongim_transport_rpc_total[%s])), 1)) * 100"),
		clusterMetric(accessmanager.RealtimeMonitorCategoryInternal, "rpcInflight", accessmanager.RealtimeMonitorStageInternalNetwork, accessmanager.RealtimeMonitorToneWarning, "", prometheusZeroFallback("sum(wukongim_transport_rpc_inflight)")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryInternal, "rpcLatencyP99", accessmanager.RealtimeMonitorStageInternalNetwork, accessmanager.RealtimeMonitorToneWarning, "ms", "histogram_quantile(0.99, sum(rate(wukongim_transport_rpc_duration_seconds_bucket[%s])) by (le)) * 1000"),
		clusterMetric(accessmanager.RealtimeMonitorCategoryInternal, "dialSuccessRate", accessmanager.RealtimeMonitorStageInternalNetwork, accessmanager.RealtimeMonitorToneNormal, "%", "(sum(rate(wukongim_transport_dial_total{result=\"ok\"}[%s])) / clamp_min(sum(rate(wukongim_transport_dial_total[%s])), 1)) * 100"),
		clusterMetric(accessmanager.RealtimeMonitorCategoryInternal, "dialLatencyP95", accessmanager.RealtimeMonitorStageInternalNetwork, accessmanager.RealtimeMonitorToneWarning, "ms", "histogram_quantile(0.95, sum(rate(wukongim_transport_dial_duration_seconds_bucket[%s])) by (le)) * 1000"),
		clusterMetric(accessmanager.RealtimeMonitorCategoryInternal, "transportV2QueueUsage", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "%", prometheusZeroFallback("max((wukongim_runtime_pool_queue_depth{component=\"transportv2\"} / clamp_min(wukongim_runtime_pool_queue_capacity{component=\"transportv2\"}, 1)) * 100)")),
		clusterMetric(accessmanager.RealtimeMonitorCategoryInternal, "transportV2AdmissionErrorRate", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneCritical, "%", "(sum(rate(wukongim_runtime_pool_admission_total{component=\"transportv2\",result!=\"ok\"}[%s])) / clamp_min(sum(rate(wukongim_runtime_pool_admission_total{component=\"transportv2\"}[%s])), 1)) * 100"),
		clusterMetric(accessmanager.RealtimeMonitorCategoryNode, "workqueuePressure", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "%", "max(wukongim_runtime_pool_queue_depth / clamp_min(wukongim_runtime_pool_queue_capacity, 1)) * 100"),
		clusterNodeMetric(accessmanager.RealtimeMonitorCategoryNode, "nodeCpuPercent", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "%", prometheusZeroFallback("wukongim_node_cpu_percent")),
		clusterNodeMetric(accessmanager.RealtimeMonitorCategoryNode, "nodeMemoryRSS", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "B", prometheusZeroFallback("wukongim_node_memory_rss_bytes")),
		clusterNodeMetric(accessmanager.RealtimeMonitorCategoryNode, "nodeGoroutines", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "", prometheusZeroFallback("wukongim_node_goroutines")),
		clusterNodeMetric(accessmanager.RealtimeMonitorCategoryNode, "nodeGCPauseRate", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "ms/s", prometheusZeroFallback("rate(go_gc_duration_seconds_sum[%s]) * 1000")),
		clusterNodeMetric(accessmanager.RealtimeMonitorCategoryNode, "nodeGCRate", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "events/s", prometheusZeroFallback("rate(go_gc_duration_seconds_count[%s])")),
		clusterNodeMetric(accessmanager.RealtimeMonitorCategoryNode, "nodeGCCPUFraction", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "%", prometheusZeroFallback("go_memstats_gc_cpu_fraction * 100")),
		clusterNodeMetric(accessmanager.RealtimeMonitorCategoryNode, "nodeGCHeapGoalUsage", accessmanager.RealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "%", prometheusZeroFallback("(go_memstats_heap_alloc_bytes / clamp_min(go_memstats_next_gc_bytes, 1)) * 100")),
	}
}

func clusterMetricWithTransform(category, key, stage, tone, unit, pattern string, transform func(float64) float64) clusterMonitorMetricDefinition {
	def := clusterMetric(category, key, stage, tone, unit, pattern)
	def.transform = transform
	return def
}

func clusterNodeMetric(category, key, stage, tone, unit, pattern string) clusterMonitorMetricDefinition {
	def := clusterMetric(category, key, stage, tone, unit, pattern)
	def.nodeStats = true
	return def
}

func clusterMetric(category, key, stage, tone, unit, pattern string) clusterMonitorMetricDefinition {
	return clusterMonitorMetricDefinition{
		key:      key,
		category: category,
		stage:    stage,
		tone:     tone,
		unit:     unit,
		query: func(rateWindow string) string {
			switch strings.Count(pattern, "%s") {
			case 2:
				return fmt.Sprintf(pattern, rateWindow, rateWindow)
			case 1:
				return fmt.Sprintf(pattern, rateWindow)
			default:
				return pattern
			}
		},
	}
}

func clusterMonitorNodeSeries(results []prometheusMatrixElement, unit string, transform func(float64) float64) ([]accessmanager.RealtimeMonitorPoint, []accessmanager.RealtimeMonitorStat, error) {
	series := make([]accessmanager.RealtimeMonitorPoint, 0)
	sortKeys := make(map[string]string)
	stats := make([]clusterMonitorNodeStat, 0, len(results))
	for index, result := range results {
		points, err := parsePrometheusMatrixValues(result.Values)
		if err != nil {
			return nil, nil, err
		}
		points = clusterMonitorTransformSeries(points, transform)
		if len(points) == 0 {
			continue
		}
		label, seriesKey, sortKey, ok := clusterMonitorNodeIdentity(result.Metric, index)
		for _, point := range points {
			if ok {
				point.Label = label
				point.SeriesKey = seriesKey
				sortKeys[seriesKey] = sortKey
			}
			series = append(series, point)
		}
		if ok {
			value := points[len(points)-1].Value
			stats = append(stats, clusterMonitorNodeStat{
				sortKey: sortKey,
				stat:    accessmanager.RealtimeMonitorStat{Key: "node", Label: label, Value: value, Unit: unit},
			})
		}
	}
	sort.Slice(series, func(i, j int) bool {
		if series[i].Timestamp != series[j].Timestamp {
			return series[i].Timestamp < series[j].Timestamp
		}
		left := sortKeys[series[i].SeriesKey]
		right := sortKeys[series[j].SeriesKey]
		if left == right {
			return series[i].Label < series[j].Label
		}
		return left < right
	})
	sort.Slice(stats, func(i, j int) bool { return stats[i].sortKey < stats[j].sortKey })
	outStats := make([]accessmanager.RealtimeMonitorStat, 0, len(stats))
	for _, stat := range stats {
		outStats = append(outStats, stat.stat)
	}
	return series, outStats, nil
}

func clusterMonitorLatestValue(series []accessmanager.RealtimeMonitorPoint) float64 {
	if len(series) == 0 {
		return 0
	}
	latestTimestamp := series[0].Timestamp
	for _, point := range series[1:] {
		if point.Timestamp > latestTimestamp {
			latestTimestamp = point.Timestamp
		}
	}
	var value float64
	var found bool
	for _, point := range series {
		if point.Timestamp != latestTimestamp {
			continue
		}
		if !found || point.Value > value {
			value = point.Value
			found = true
		}
	}
	return value
}

type clusterMonitorNodeStat struct {
	sortKey string
	stat    accessmanager.RealtimeMonitorStat
}

func clusterMonitorNodeIdentity(metric map[string]string, index int) (string, string, string, bool) {
	if len(metric) == 0 {
		return "", "", "", false
	}
	nodeName := strings.TrimSpace(metric["node_name"])
	nodeID := strings.TrimSpace(metric["node_id"])
	if nodeName != "" {
		seriesKey := clusterMonitorNodeSeriesKey(nodeID, nodeName, index)
		return nodeName, seriesKey, clusterMonitorNodeSortKey(nodeID, nodeName, index), true
	}
	if nodeID != "" {
		label := "node-" + nodeID
		seriesKey := clusterMonitorNodeSeriesKey(nodeID, label, index)
		return label, seriesKey, clusterMonitorNodeSortKey(nodeID, label, index), true
	}
	return "", "", "", false
}

func clusterMonitorNodeSeriesKey(nodeID, fallback string, index int) string {
	if strings.TrimSpace(nodeID) != "" {
		return "node-" + strings.TrimSpace(nodeID)
	}
	key := strings.TrimSpace(fallback)
	if key == "" {
		key = fmt.Sprintf("node-%d", index+1)
	}
	return key
}

func clusterMonitorNodeSortKey(nodeID, fallback string, index int) string {
	if parsed, err := strconv.ParseUint(strings.TrimSpace(nodeID), 10, 64); err == nil {
		return fmt.Sprintf("%020d", parsed)
	}
	return fmt.Sprintf("z-%020d-%s", index, fallback)
}

func clusterMonitorTransformSeries(series []accessmanager.RealtimeMonitorPoint, transform func(float64) float64) []accessmanager.RealtimeMonitorPoint {
	if transform == nil || len(series) == 0 {
		return series
	}
	out := make([]accessmanager.RealtimeMonitorPoint, 0, len(series))
	for _, point := range series {
		out = append(out, accessmanager.RealtimeMonitorPoint{
			Timestamp: point.Timestamp,
			Value:     transform(point.Value),
			Label:     point.Label,
			SeriesKey: point.SeriesKey,
		})
	}
	return out
}

func clusterMonitorStats(series []accessmanager.RealtimeMonitorPoint, unit string, step time.Duration) []accessmanager.RealtimeMonitorStat {
	base := monitorCardStats(series, step)
	out := make([]accessmanager.RealtimeMonitorStat, 0, len(base))
	for _, stat := range base {
		out = append(out, accessmanager.RealtimeMonitorStat{Key: stat.Key, Value: stat.Value, Unit: unit})
	}
	return out
}

func clusterMonitorSnapshot(cards []accessmanager.RealtimeMonitorCard, control managerClusterControlSnapshot) []accessmanager.RealtimeMonitorSnapshotEntry {
	byKey := make(map[string]accessmanager.RealtimeMonitorCard, len(cards))
	for _, card := range cards {
		if card.Available {
			byKey[card.Key] = card
		}
	}
	out := make([]accessmanager.RealtimeMonitorSnapshotEntry, 0, 7)
	if control.ok {
		out = append(out,
			accessmanager.RealtimeMonitorSnapshotEntry{
				Key:       "nodesAlive",
				MetricKey: "nodesAlive",
				Value:     float64(control.nodesAlive),
				Tone:      accessmanager.RealtimeMonitorToneNormal,
				Source:    accessmanager.RealtimeMonitorSourceControlSnapshot,
			},
			accessmanager.RealtimeMonitorSnapshotEntry{
				Key:       "slotsReady",
				MetricKey: "slotsReady",
				Value:     clusterMonitorSlotsReadyPercent(control),
				Unit:      "%",
				Tone:      clusterMonitorSlotsTone(control),
				Source:    accessmanager.RealtimeMonitorSourceControlSnapshot,
			},
		)
	}
	out = appendClusterCardSnapshot(out, byKey, "controllerApplyGap", "controllerApplyGap", accessmanager.RealtimeMonitorToneWarning, func(value float64) float64 { return value })
	out = appendClusterCardSnapshot(out, byKey, "rpcErrorRate", "rpcSuccessRate", accessmanager.RealtimeMonitorToneWarning, func(value float64) float64 {
		if value > 100 {
			return 0
		}
		return 100 - value
	})
	out = appendClusterCardSnapshot(out, byKey, "queuePressure", "workqueuePressure", accessmanager.RealtimeMonitorToneWarning, func(value float64) float64 { return value })
	out = appendClusterCardSnapshot(out, byKey, "storageWriteP99", "storageWriteP99", accessmanager.RealtimeMonitorToneWarning, func(value float64) float64 { return value })
	return out
}

func appendClusterCardSnapshot(out []accessmanager.RealtimeMonitorSnapshotEntry, byKey map[string]accessmanager.RealtimeMonitorCard, key, metricKey, tone string, value func(float64) float64) []accessmanager.RealtimeMonitorSnapshotEntry {
	card, ok := byKey[metricKey]
	if !ok {
		return out
	}
	return appendClusterCardSnapshotEntry(out, card, key, metricKey, tone, card.Unit, value)
}

func appendClusterCardSnapshotEntry(out []accessmanager.RealtimeMonitorSnapshotEntry, card accessmanager.RealtimeMonitorCard, key, metricKey, tone, unit string, value func(float64) float64) []accessmanager.RealtimeMonitorSnapshotEntry {
	return append(out, accessmanager.RealtimeMonitorSnapshotEntry{
		Key:       key,
		MetricKey: metricKey,
		Value:     value(card.Value),
		Unit:      unit,
		Tone:      tone,
		Source:    accessmanager.RealtimeMonitorSourcePrometheus,
	})
}

func clusterMonitorSlotsReadyPercent(control managerClusterControlSnapshot) float64 {
	if control.totalSlots <= 0 {
		return 0
	}
	return (float64(control.slotsReady) / float64(control.totalSlots)) * 100
}

func clusterMonitorSlotsTone(control managerClusterControlSnapshot) string {
	if control.totalSlots <= 0 || control.leaderMissing > 0 || control.quorumLost > 0 || control.slotsReady < control.totalSlots {
		return accessmanager.RealtimeMonitorToneWarning
	}
	return accessmanager.RealtimeMonitorToneNormal
}
