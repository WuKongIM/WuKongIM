package metrics

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

const (
	WukongIMV2BottleneckGateway        = "gateway_dispatch"
	WukongIMV2BottleneckControllerRaft = "controller_raft_step"
	WukongIMV2BottleneckChannelV2      = "channelv2_append"
	WukongIMV2BottleneckStorageCommit  = "storage_commit"
	WukongIMV2BottleneckMixed          = "mixed_backpressure"
	WukongIMV2BottleneckUnobserved     = "no_observed_queue_pressure"
)

const (
	wukongIMV2GatewayQueuePressureRatio = 0.25
	wukongIMV2LatencyPressureSeconds    = 0.02
)

// PrometheusSample is one parsed Prometheus text exposition sample.
type PrometheusSample struct {
	// Name is the metric family or sample name.
	Name string
	// Labels contains the parsed low-cardinality labels for this sample.
	Labels map[string]string
	// Value is the sample value.
	Value float64
}

// PrometheusSnapshot is a parsed point-in-time Prometheus text exposition.
type PrometheusSnapshot struct {
	// Samples contains all parsed metric samples in input order.
	Samples []PrometheusSample
}

// WukongIMV2Attribution summarizes gateway vs ChannelV2 pressure from two snapshots.
type WukongIMV2Attribution struct {
	// Classification is the coarse bottleneck class inferred from observed pressure.
	Classification string
	// Reasons explains the evidence that produced the classification.
	Reasons []string

	GatewayQueueDepth                               float64
	GatewayQueueCapacity                            float64
	GatewayQueueRatio                               float64
	GatewayDispatchWaitP99Seconds                   float64
	GatewayBatchRecordsP50                          float64
	GatewaySendackSystemErrorCount                  float64
	GatewaySendackSingleErrorCount                  float64
	GatewaySendackSingleMissingRequestContextCount  float64
	GatewaySendackBatchPrecheckCount                float64
	GatewaySendackBatchMissingRequestContextCount   float64
	GatewaySendackBatchResultErrorCount             float64
	GatewaySendackBatchResultTimeoutCount           float64
	GatewaySendackBatchResultCanceledCount          float64
	GatewaySendackBatchResultOtherCount             float64
	GatewaySendackBatchFallbackErrorCount           float64
	MessageAppendErrorCount                         float64
	MessageAppendBackpressuredErrCount              float64
	MessageAppendRouteNotReadyErrCount              float64
	MessageAppendStaleRouteErrCount                 float64
	MessageAppendNotLeaderErrCount                  float64
	MessageAppendChannelNotFoundErrCount            float64
	MessageAppendShortResultErrCount                float64
	MessageAppendInvalidConfigErrCount              float64
	MessageAppendClosedErrCount                     float64
	MessageAppendTooManyChannelsErrCount            float64
	MessageAppendNotStartedErrCount                 float64
	MessageAppendCanceledErrCount                   float64
	MessageAppendTimeoutErrCount                    float64
	MessageAppendAppendFailedErrCount               float64
	MessageAppendRemoteErrCount                     float64
	MessageAppendOtherErrCount                      float64
	ControllerRaftStepQueueDepth                    float64
	ControllerRaftStepQueueCapacity                 float64
	ControllerRaftStepQueueRatio                    float64
	ControllerRaftStepEnqueueOKP99Seconds           float64
	ControllerRaftStepEnqueueErrP99Seconds          float64
	ControllerRaftStepEnqueueErrCount               float64
	ChannelV2ReactorMailboxDepthMax                 float64
	ChannelV2WorkerQueueDepthMax                    float64
	ChannelV2WorkerQueueDepthByPool                 map[string]float64
	ChannelV2WorkerInflightByPool                   map[string]float64
	ChannelV2WorkerInflightPeakByPool               map[string]float64
	ChannelV2AppendP99Seconds                       float64
	ChannelV2MetaResolveP99Seconds                  float64
	ChannelV2MetaSlotReadP99Seconds                 float64
	ChannelV2MetaCreateBuildP99Seconds              float64
	ChannelV2MetaCreateProposeP99Seconds            float64
	ChannelV2MetaCreateProposeLocalP99Seconds       float64
	ChannelV2MetaCreateProposeForwardP99Seconds     float64
	ChannelV2MetaCreateSlotProposeSubmitP99Seconds  float64
	ChannelV2MetaCreateSlotProposeWaitP99Seconds    float64
	ChannelV2MetaCreateSlotControlWaitP99Seconds    float64
	ChannelV2MetaCreateSlotRaftCommitWaitP99Seconds float64
	ChannelV2MetaCreateSlotFSMApplyP99Seconds       float64
	ChannelV2MetaCreateSlotFSMCommitP99Seconds      float64
	ChannelV2MetaCreateSlotMarkAppliedP99Seconds    float64
	ChannelV2MetaCreateWriteP99Seconds              float64
	ChannelV2MetaFinalReadP99Seconds                float64
	ChannelV2MetaApplyP99Seconds                    float64
	ChannelV2RuntimeAppendP99Seconds                float64
	ChannelV2RuntimeAppendReserveWaitP99Seconds     float64
	ChannelV2RuntimeAppendSubmitP99Seconds          float64
	ChannelV2RuntimeAppendWaitP99Seconds            float64
	ChannelV2AppendBatchWaitP99Seconds              float64
	ChannelV2AppendStoreWaitP99Seconds              float64
	ChannelV2AppendPostStoreCommitWaitP99Seconds    float64
	ChannelV2AppendQuorumFollowerPullWaitP99Seconds float64
	ChannelV2AppendQuorumAckOffsetWaitP99Seconds    float64
	ChannelV2AppendQuorumHWAdvanceWaitP99Seconds    float64
	ChannelV2AppendQuorumFinalCompleteP99Seconds    float64
	ChannelV2ReplicationPullHintToSubmitP99Seconds  float64
	ChannelV2ReplicationPullRPCP99Seconds           float64
	ChannelV2NeedMetaPullRPCP99Seconds              float64
	ChannelV2ReplicationStoreApplyP99Seconds        float64
	ChannelV2ReplicationApplyToAckReturnP99Seconds  float64
	ChannelV2PendingMetaCurrentMax                  float64
	ChannelV2PendingMetaCreatedCount                float64
	ChannelV2PendingMetaConvertedCount              float64
	ChannelV2PendingMetaReleasedCount               float64
	ChannelV2PendingMetaTimeoutReleaseCount         float64
	ChannelV2PendingMetaNotReadyReleaseCount        float64
	ChannelV2PullHintSubmittedCount                 float64
	ChannelV2PullHintOKCount                        float64
	ChannelV2PullHintErrCount                       float64
	ChannelV2PullHintStaleMetaErrCount              float64
	ChannelV2PullHintChannelNotFoundErrCount        float64
	ChannelV2PullHintNotReadyErrCount               float64
	ChannelV2PullHintNotLeaderErrCount              float64
	ChannelV2PullHintInvalidConfigErrCount          float64
	ChannelV2PullHintClosedErrCount                 float64
	ChannelV2PullHintCanceledErrCount               float64
	ChannelV2PullHintTimeoutErrCount                float64
	ChannelV2PullHintRemoteErrCount                 float64
	ChannelV2PullHintOtherErrCount                  float64
	ChannelV2PullHintReceiveOKCount                 float64
	ChannelV2PullHintReceiveErrCount                float64
	ChannelV2PullHintReceiveStateCheckErrCount      float64
	ChannelV2PullHintReceiveMetaResolveErrCount     float64
	ChannelV2PullHintReceiveMetaHintOKCount         float64
	ChannelV2PullHintReceiveMetaValidateErrCount    float64
	ChannelV2PullHintReceiveMetaApplyErrCount       float64
	ChannelV2PullHintReceiveSubmitErrCount          float64
	ChannelV2PullHintReceiveAwaitErrCount           float64
	ChannelV2PullHintReceiveStaleMetaErrCount       float64
	ChannelV2PullHintReceiveChannelNotFoundErrCount float64
	ChannelV2PullHintReceiveNotReadyErrCount        float64
	ChannelV2PullHintReceiveCanceledErrCount        float64
	ChannelV2PullHintReceiveTimeoutErrCount         float64
	ChannelV2PullHintReceiveOtherErrCount           float64
	ChannelV2NeedMetaPullSubmittedCount             float64
	ChannelV2NeedMetaPullOKCount                    float64
	ChannelV2NeedMetaPullRetryCount                 float64
	ChannelV2NeedMetaPullErrCount                   float64
	ChannelV2NeedMetaPullStaleMetaErrCount          float64
	ChannelV2NeedMetaPullChannelNotFoundErrCount    float64
	ChannelV2NeedMetaPullNotReadyErrCount           float64
	ChannelV2NeedMetaPullNotLeaderErrCount          float64
	ChannelV2NeedMetaPullNotReplicaErrCount         float64
	ChannelV2NeedMetaPullBackpressuredErrCount      float64
	ChannelV2NeedMetaPullInvalidConfigErrCount      float64
	ChannelV2NeedMetaPullClosedErrCount             float64
	ChannelV2NeedMetaPullCanceledErrCount           float64
	ChannelV2NeedMetaPullTimeoutErrCount            float64
	ChannelV2NeedMetaPullRemoteErrCount             float64
	ChannelV2NeedMetaPullOtherErrCount              float64
	ChannelV2WorkerTaskP99Seconds                   float64
	ChannelV2WorkerTaskP99SecondsByKind             map[string]float64
	ChannelV2AppendBatchRecordsP50                  float64
	StorageCommitQueueDepthMax                      float64
	StorageCommitBatchRequestsP50                   float64
	StorageCommitBatchRecordsP50                    float64
	StorageCommitP99Seconds                         float64
	StorageCommitTotalP99Seconds                    float64
	StorageCommitRequestP99Seconds                  float64
	StorageCommitRequestOKP99Seconds                float64
	StorageCommitRequestTimeoutCount                float64
	StorageCommitRequestCanceledCount               float64
	StorageCommitRequestClosedCount                 float64
	StorageCommitRequestErrCount                    float64
	StorageCommitRequestOver1sCount                 float64
	StorageCommitRequestOver5sCount                 float64
	StorageCommitRequestOver10sCount                float64
	StorageCommitRequestP99SecondsByLane            map[string]float64
	StorageCommitRequestOver1sCountByLane           map[string]float64
	StorageCommitRequestOver5sCountByLane           map[string]float64
	StorageCommitRequestOver10sCountByLane          map[string]float64
}

// ParsePrometheusText parses the simple Prometheus text exposition emitted by WuKongIM.
func ParsePrometheusText(r io.Reader) (PrometheusSnapshot, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var out PrometheusSnapshot
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sample, err := parsePrometheusSample(line)
		if err != nil {
			return PrometheusSnapshot{}, fmt.Errorf("line %d: %w", lineNo, err)
		}
		out.Samples = append(out.Samples, sample)
	}
	if err := scanner.Err(); err != nil {
		return PrometheusSnapshot{}, err
	}
	return out, nil
}

func parsePrometheusSample(line string) (PrometheusSample, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return PrometheusSample{}, fmt.Errorf("expected metric and value")
	}
	value, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return PrometheusSample{}, fmt.Errorf("parse value: %w", err)
	}
	name, labels, err := parsePrometheusMetric(fields[0])
	if err != nil {
		return PrometheusSample{}, err
	}
	return PrometheusSample{Name: name, Labels: labels, Value: value}, nil
}

func parsePrometheusMetric(raw string) (string, map[string]string, error) {
	open := strings.IndexByte(raw, '{')
	if open < 0 {
		name := strings.TrimSpace(raw)
		if name == "" {
			return "", nil, fmt.Errorf("missing metric name")
		}
		return name, nil, nil
	}
	close := strings.LastIndexByte(raw, '}')
	if close < open {
		return "", nil, fmt.Errorf("unterminated label set")
	}
	name := strings.TrimSpace(raw[:open])
	if name == "" {
		return "", nil, fmt.Errorf("missing metric name")
	}
	labels, err := parsePrometheusLabels(raw[open+1 : close])
	if err != nil {
		return "", nil, err
	}
	return name, labels, nil
}

func parsePrometheusLabels(raw string) (map[string]string, error) {
	labels := map[string]string{}
	for _, part := range splitPrometheusLabels(raw) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("invalid label %q", part)
		}
		value = strings.TrimSpace(value)
		if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
			return nil, fmt.Errorf("invalid label value %q", value)
		}
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return nil, fmt.Errorf("parse label value: %w", err)
		}
		labels[strings.TrimSpace(key)] = unquoted
	}
	return labels, nil
}

func splitPrometheusLabels(raw string) []string {
	var parts []string
	start := 0
	inQuote := false
	escaped := false
	for i, r := range raw {
		if escaped {
			escaped = false
			continue
		}
		switch r {
		case '\\':
			escaped = inQuote
		case '"':
			inQuote = !inQuote
		case ',':
			if !inQuote {
				parts = append(parts, raw[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, raw[start:])
	return parts
}

// AnalyzeWukongIMV2Prometheus classifies gateway vs ChannelV2 pressure between two snapshots.
func AnalyzeWukongIMV2Prometheus(before, after PrometheusSnapshot) WukongIMV2Attribution {
	report := WukongIMV2Attribution{
		Classification:                         WukongIMV2BottleneckUnobserved,
		ChannelV2WorkerQueueDepthByPool:        map[string]float64{},
		ChannelV2WorkerInflightByPool:          map[string]float64{},
		ChannelV2WorkerInflightPeakByPool:      map[string]float64{},
		ChannelV2WorkerTaskP99SecondsByKind:    map[string]float64{},
		StorageCommitRequestP99SecondsByLane:   map[string]float64{},
		StorageCommitRequestOver1sCountByLane:  map[string]float64{},
		StorageCommitRequestOver5sCountByLane:  map[string]float64{},
		StorageCommitRequestOver10sCountByLane: map[string]float64{},
	}
	report.GatewayQueueDepth, _ = after.maxGauge("wukongim_gateway_async_send_queue_depth")
	report.GatewayQueueCapacity, _ = after.maxGauge("wukongim_gateway_async_send_queue_capacity")
	if report.GatewayQueueCapacity > 0 {
		report.GatewayQueueRatio = report.GatewayQueueDepth / report.GatewayQueueCapacity
	}
	report.GatewayDispatchWaitP99Seconds, _ = histogramQuantileDelta(0.99, before, after, "wukongim_gateway_async_send_dispatch_wait_duration_seconds")
	report.GatewayBatchRecordsP50, _ = histogramQuantileDelta(0.50, before, after, "wukongim_gateway_async_send_batch_records")
	report.GatewaySendackSystemErrorCount, _ = counterDeltaMatching(before, after, "wukongim_gateway_sendacks_total", map[string]string{"reason": "system_error"})
	report.GatewaySendackSingleErrorCount, _ = counterDeltaMatching(before, after, "wukongim_gateway_sendacks_total", map[string]string{"source": "single_error"})
	report.GatewaySendackSingleMissingRequestContextCount, _ = counterDeltaMatching(before, after, "wukongim_gateway_sendacks_total", map[string]string{"source": "single_missing_request_context"})
	report.GatewaySendackBatchPrecheckCount, _ = counterDeltaMatching(before, after, "wukongim_gateway_sendacks_total", map[string]string{"source": "batch_precheck"})
	report.GatewaySendackBatchMissingRequestContextCount, _ = counterDeltaMatching(before, after, "wukongim_gateway_sendacks_total", map[string]string{"source": "batch_missing_request_context"})
	report.GatewaySendackBatchResultErrorCount, _ = counterDeltaMatching(before, after, "wukongim_gateway_sendacks_total", map[string]string{"source": "batch_result_error"})
	report.GatewaySendackBatchResultTimeoutCount, _ = counterDeltaMatching(before, after, "wukongim_gateway_sendacks_total", map[string]string{"source": "batch_result_error", "class": "timeout"})
	report.GatewaySendackBatchResultCanceledCount, _ = counterDeltaMatching(before, after, "wukongim_gateway_sendacks_total", map[string]string{"source": "batch_result_error", "class": "canceled"})
	report.GatewaySendackBatchResultOtherCount, _ = counterDeltaMatching(before, after, "wukongim_gateway_sendacks_total", map[string]string{"source": "batch_result_error", "class": "other"})
	report.GatewaySendackBatchFallbackErrorCount, _ = counterDeltaMatching(before, after, "wukongim_gateway_sendacks_total", map[string]string{"source": "batch_fallback_error"})
	report.MessageAppendErrorCount, _ = counterDeltaMatching(before, after, "wukongim_message_append_errors_total", nil)
	report.MessageAppendBackpressuredErrCount, _ = counterDeltaMatching(before, after, "wukongim_message_append_errors_total", map[string]string{"class": "backpressured"})
	report.MessageAppendRouteNotReadyErrCount, _ = counterDeltaMatching(before, after, "wukongim_message_append_errors_total", map[string]string{"class": "route_not_ready"})
	report.MessageAppendStaleRouteErrCount, _ = counterDeltaMatching(before, after, "wukongim_message_append_errors_total", map[string]string{"class": "stale_route"})
	report.MessageAppendNotLeaderErrCount, _ = counterDeltaMatching(before, after, "wukongim_message_append_errors_total", map[string]string{"class": "not_leader"})
	report.MessageAppendChannelNotFoundErrCount, _ = counterDeltaMatching(before, after, "wukongim_message_append_errors_total", map[string]string{"class": "channel_not_found"})
	report.MessageAppendShortResultErrCount, _ = counterDeltaMatching(before, after, "wukongim_message_append_errors_total", map[string]string{"class": "short_result"})
	report.MessageAppendInvalidConfigErrCount, _ = counterDeltaMatching(before, after, "wukongim_message_append_errors_total", map[string]string{"class": "invalid_config"})
	report.MessageAppendClosedErrCount, _ = counterDeltaMatching(before, after, "wukongim_message_append_errors_total", map[string]string{"class": "closed"})
	report.MessageAppendTooManyChannelsErrCount, _ = counterDeltaMatching(before, after, "wukongim_message_append_errors_total", map[string]string{"class": "too_many_channels"})
	report.MessageAppendNotStartedErrCount, _ = counterDeltaMatching(before, after, "wukongim_message_append_errors_total", map[string]string{"class": "not_started"})
	report.MessageAppendCanceledErrCount, _ = counterDeltaMatching(before, after, "wukongim_message_append_errors_total", map[string]string{"class": "canceled"})
	report.MessageAppendTimeoutErrCount, _ = counterDeltaMatching(before, after, "wukongim_message_append_errors_total", map[string]string{"class": "timeout"})
	report.MessageAppendAppendFailedErrCount, _ = counterDeltaMatching(before, after, "wukongim_message_append_errors_total", map[string]string{"class": "append_failed"})
	report.MessageAppendRemoteErrCount, _ = counterDeltaMatching(before, after, "wukongim_message_append_errors_total", map[string]string{"class": "remote_error"})
	report.MessageAppendOtherErrCount, _ = counterDeltaMatching(before, after, "wukongim_message_append_errors_total", map[string]string{"class": "other"})

	report.ControllerRaftStepQueueDepth, _ = after.maxGauge("wukongim_controller_raft_step_queue_depth")
	report.ControllerRaftStepQueueCapacity, _ = after.maxGauge("wukongim_controller_raft_step_queue_capacity")
	if report.ControllerRaftStepQueueCapacity > 0 {
		report.ControllerRaftStepQueueRatio = report.ControllerRaftStepQueueDepth / report.ControllerRaftStepQueueCapacity
	}
	report.ControllerRaftStepEnqueueOKP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_controller_raft_step_enqueue_duration_seconds", map[string]string{"result": "ok"})
	report.ControllerRaftStepEnqueueErrP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_controller_raft_step_enqueue_duration_seconds", map[string]string{"result": "err"})
	report.ControllerRaftStepEnqueueErrCount, _ = histogramCountDeltaMatching(before, after, "wukongim_controller_raft_step_enqueue_duration_seconds", map[string]string{"result": "err"})

	report.ChannelV2ReactorMailboxDepthMax, _ = after.maxGauge("wukongim_channelv2_reactor_mailbox_depth")
	report.ChannelV2WorkerQueueDepthMax, _ = after.maxGauge("wukongim_channelv2_worker_queue_depth")
	report.ChannelV2WorkerQueueDepthByPool = after.maxGaugeByLabel("wukongim_channelv2_worker_queue_depth", "pool")
	report.ChannelV2WorkerInflightByPool = after.maxGaugeByLabel("wukongim_channelv2_worker_inflight", "pool")
	report.ChannelV2WorkerInflightPeakByPool = after.maxGaugeByLabel("wukongim_channelv2_worker_inflight_peak", "pool")
	report.ChannelV2AppendP99Seconds, _ = histogramQuantileDelta(0.99, before, after, "wukongim_channelv2_append_duration_seconds")
	report.ChannelV2MetaResolveP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_resolve"})
	report.ChannelV2MetaSlotReadP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_slot_read"})
	report.ChannelV2MetaCreateBuildP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_build"})
	report.ChannelV2MetaCreateProposeP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_propose"})
	report.ChannelV2MetaCreateProposeLocalP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_propose_local"})
	report.ChannelV2MetaCreateProposeForwardP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_propose_forward"})
	report.ChannelV2MetaCreateSlotProposeSubmitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_slot_propose_submit"})
	report.ChannelV2MetaCreateSlotProposeWaitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_slot_propose_wait"})
	report.ChannelV2MetaCreateSlotControlWaitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_slot_control_wait"})
	report.ChannelV2MetaCreateSlotRaftCommitWaitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_slot_raft_commit_wait"})
	report.ChannelV2MetaCreateSlotFSMApplyP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_slot_fsm_apply"})
	report.ChannelV2MetaCreateSlotFSMCommitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_slot_fsm_commit"})
	report.ChannelV2MetaCreateSlotMarkAppliedP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_slot_mark_applied"})
	report.ChannelV2MetaCreateWriteP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_write"})
	report.ChannelV2MetaFinalReadP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_final_read"})
	report.ChannelV2MetaApplyP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_apply"})
	report.ChannelV2RuntimeAppendP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "runtime_append"})
	report.ChannelV2RuntimeAppendReserveWaitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "runtime_append_reserve_wait"})
	report.ChannelV2RuntimeAppendSubmitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "runtime_append_submit"})
	report.ChannelV2RuntimeAppendWaitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "runtime_append_wait"})
	report.ChannelV2AppendBatchWaitP99Seconds, _ = histogramQuantileDelta(0.99, before, after, "wukongim_channelv2_append_batch_wait_duration_seconds")
	report.ChannelV2AppendStoreWaitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_wait_stage_duration_seconds", map[string]string{"stage": "store_append_wait"})
	report.ChannelV2AppendPostStoreCommitWaitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_wait_stage_duration_seconds", map[string]string{"stage": "post_store_commit_wait"})
	report.ChannelV2AppendQuorumFollowerPullWaitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_wait_stage_duration_seconds", map[string]string{"stage": "quorum_follower_pull_wait"})
	report.ChannelV2AppendQuorumAckOffsetWaitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_wait_stage_duration_seconds", map[string]string{"stage": "quorum_ack_offset_wait"})
	report.ChannelV2AppendQuorumHWAdvanceWaitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_wait_stage_duration_seconds", map[string]string{"stage": "quorum_hw_advance_wait"})
	report.ChannelV2AppendQuorumFinalCompleteP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_wait_stage_duration_seconds", map[string]string{"stage": "quorum_final_complete_wait"})
	report.ChannelV2ReplicationPullHintToSubmitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_replication_stage_duration_seconds", map[string]string{"stage": "follower_pull_hint_to_submit"})
	report.ChannelV2ReplicationPullRPCP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_replication_stage_duration_seconds", map[string]string{"stage": "follower_pull_rpc"})
	report.ChannelV2NeedMetaPullRPCP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_replication_stage_duration_seconds", map[string]string{"stage": "follower_need_meta_pull_rpc"})
	report.ChannelV2ReplicationStoreApplyP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_replication_stage_duration_seconds", map[string]string{"stage": "follower_store_apply"})
	report.ChannelV2ReplicationApplyToAckReturnP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_replication_stage_duration_seconds", map[string]string{"stage": "follower_apply_to_ack_return"})
	report.ChannelV2PendingMetaCurrentMax, _ = after.maxGauge("wukongim_channelv2_pending_meta_current")
	report.ChannelV2PendingMetaCreatedCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pending_meta_total", map[string]string{"event": "created"})
	report.ChannelV2PendingMetaConvertedCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pending_meta_total", map[string]string{"event": "converted"})
	report.ChannelV2PendingMetaReleasedCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pending_meta_total", map[string]string{"event": "released"})
	report.ChannelV2PendingMetaTimeoutReleaseCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pending_meta_total", map[string]string{"event": "released", "error": "timeout"})
	report.ChannelV2PendingMetaNotReadyReleaseCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pending_meta_total", map[string]string{"event": "released", "error": "not_ready"})
	report.ChannelV2PullHintSubmittedCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "submitted"})
	report.ChannelV2PullHintOKCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "ok"})
	report.ChannelV2PullHintErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err"})
	report.ChannelV2PullHintStaleMetaErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err", "error": "stale_meta"})
	report.ChannelV2PullHintChannelNotFoundErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err", "error": "channel_not_found"})
	report.ChannelV2PullHintNotReadyErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err", "error": "not_ready"})
	report.ChannelV2PullHintNotLeaderErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err", "error": "not_leader"})
	report.ChannelV2PullHintInvalidConfigErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err", "error": "invalid_config"})
	report.ChannelV2PullHintClosedErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err", "error": "closed"})
	report.ChannelV2PullHintCanceledErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err", "error": "canceled"})
	report.ChannelV2PullHintTimeoutErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err", "error": "timeout"})
	report.ChannelV2PullHintRemoteErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err", "error": "remote_error"})
	report.ChannelV2PullHintOtherErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err", "error": "other"})
	report.ChannelV2PullHintReceiveOKCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"result": "ok"})
	report.ChannelV2PullHintReceiveErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"result": "err"})
	report.ChannelV2PullHintReceiveStateCheckErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"stage": "state_check", "result": "err"})
	report.ChannelV2PullHintReceiveMetaResolveErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"stage": "meta_resolve", "result": "err"})
	report.ChannelV2PullHintReceiveMetaHintOKCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"stage": "meta_hint", "result": "ok"})
	report.ChannelV2PullHintReceiveMetaValidateErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"stage": "meta_validate", "result": "err"})
	report.ChannelV2PullHintReceiveMetaApplyErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"stage": "meta_apply", "result": "err"})
	report.ChannelV2PullHintReceiveSubmitErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"stage": "submit", "result": "err"})
	report.ChannelV2PullHintReceiveAwaitErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"stage": "await", "result": "err"})
	report.ChannelV2PullHintReceiveStaleMetaErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"result": "err", "error": "stale_meta"})
	report.ChannelV2PullHintReceiveChannelNotFoundErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"result": "err", "error": "channel_not_found"})
	report.ChannelV2PullHintReceiveNotReadyErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"result": "err", "error": "not_ready"})
	report.ChannelV2PullHintReceiveCanceledErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"result": "err", "error": "canceled"})
	report.ChannelV2PullHintReceiveTimeoutErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"result": "err", "error": "timeout"})
	report.ChannelV2PullHintReceiveOtherErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"result": "err", "error": "other"})
	report.ChannelV2NeedMetaPullSubmittedCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "submitted"})
	report.ChannelV2NeedMetaPullOKCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "ok"})
	report.ChannelV2NeedMetaPullRetryCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "retry"})
	report.ChannelV2NeedMetaPullErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err"})
	report.ChannelV2NeedMetaPullStaleMetaErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "stale_meta"})
	report.ChannelV2NeedMetaPullChannelNotFoundErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "channel_not_found"})
	report.ChannelV2NeedMetaPullNotReadyErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "not_ready"})
	report.ChannelV2NeedMetaPullNotLeaderErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "not_leader"})
	report.ChannelV2NeedMetaPullNotReplicaErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "not_replica"})
	report.ChannelV2NeedMetaPullBackpressuredErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "backpressured"})
	report.ChannelV2NeedMetaPullInvalidConfigErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "invalid_config"})
	report.ChannelV2NeedMetaPullClosedErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "closed"})
	report.ChannelV2NeedMetaPullCanceledErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "canceled"})
	report.ChannelV2NeedMetaPullTimeoutErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "timeout"})
	report.ChannelV2NeedMetaPullRemoteErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "remote_error"})
	report.ChannelV2NeedMetaPullOtherErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "other"})
	report.ChannelV2WorkerTaskP99Seconds, _ = histogramQuantileDelta(0.99, before, after, "wukongim_channelv2_worker_task_duration_seconds")
	report.ChannelV2WorkerTaskP99SecondsByKind = histogramQuantileDeltaByLabel(0.99, before, after, "wukongim_channelv2_worker_task_duration_seconds", "kind")
	report.ChannelV2AppendBatchRecordsP50, _ = histogramQuantileDelta(0.50, before, after, "wukongim_channelv2_append_batch_records")
	report.StorageCommitQueueDepthMax, _ = after.maxGauge("wukongim_storage_commit_queue_depth")
	report.StorageCommitBatchRequestsP50, _ = histogramQuantileDelta(0.50, before, after, "wukongim_storage_commit_batch_requests")
	report.StorageCommitBatchRecordsP50, _ = histogramQuantileDelta(0.50, before, after, "wukongim_storage_commit_batch_records")
	report.StorageCommitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_storage_commit_batch_duration_seconds", map[string]string{"stage": "commit"})
	report.StorageCommitTotalP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_storage_commit_batch_duration_seconds", map[string]string{"stage": "total"})
	report.StorageCommitRequestP99Seconds, _ = histogramQuantileDelta(0.99, before, after, "wukongim_storage_commit_request_duration_seconds")
	report.StorageCommitRequestOKP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_storage_commit_request_duration_seconds", map[string]string{"result": "ok"})
	report.StorageCommitRequestTimeoutCount, _ = histogramCountDeltaMatching(before, after, "wukongim_storage_commit_request_duration_seconds", map[string]string{"result": "timeout"})
	report.StorageCommitRequestCanceledCount, _ = histogramCountDeltaMatching(before, after, "wukongim_storage_commit_request_duration_seconds", map[string]string{"result": "canceled"})
	report.StorageCommitRequestClosedCount, _ = histogramCountDeltaMatching(before, after, "wukongim_storage_commit_request_duration_seconds", map[string]string{"result": "closed"})
	report.StorageCommitRequestErrCount, _ = histogramCountDeltaMatching(before, after, "wukongim_storage_commit_request_duration_seconds", map[string]string{"result": "err"})
	report.StorageCommitRequestOver1sCount, _ = histogramCountAboveDeltaMatching(before, after, "wukongim_storage_commit_request_duration_seconds", nil, 1)
	report.StorageCommitRequestOver5sCount, _ = histogramCountAboveDeltaMatching(before, after, "wukongim_storage_commit_request_duration_seconds", nil, 5)
	report.StorageCommitRequestOver10sCount, _ = histogramCountAboveDeltaMatching(before, after, "wukongim_storage_commit_request_duration_seconds", nil, 10)
	for _, lane := range histogramLabelValues(before, after, "wukongim_storage_commit_request_duration_seconds", "lane") {
		labels := map[string]string{"lane": lane}
		report.StorageCommitRequestP99SecondsByLane[lane], _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_storage_commit_request_duration_seconds", labels)
		report.StorageCommitRequestOver1sCountByLane[lane], _ = histogramCountAboveDeltaMatching(before, after, "wukongim_storage_commit_request_duration_seconds", labels, 1)
		report.StorageCommitRequestOver5sCountByLane[lane], _ = histogramCountAboveDeltaMatching(before, after, "wukongim_storage_commit_request_duration_seconds", labels, 5)
		report.StorageCommitRequestOver10sCountByLane[lane], _ = histogramCountAboveDeltaMatching(before, after, "wukongim_storage_commit_request_duration_seconds", labels, 10)
	}

	gatewayPressure := false
	if report.GatewayQueueRatio >= wukongIMV2GatewayQueuePressureRatio {
		gatewayPressure = true
		report.Reasons = append(report.Reasons, "gateway async SEND queue ratio is high")
	}
	if report.GatewayDispatchWaitP99Seconds >= wukongIMV2LatencyPressureSeconds {
		gatewayPressure = true
		report.Reasons = append(report.Reasons, "gateway async SEND dispatch wait p99 is high")
	}
	if report.GatewaySendackSystemErrorCount > 0 {
		report.Reasons = append(report.Reasons, "gateway system-error sendacks were observed")
	}
	if report.MessageAppendErrorCount > 0 {
		report.Reasons = append(report.Reasons, "message append errors were observed")
	}
	if report.MessageAppendBackpressuredErrCount > 0 {
		report.Reasons = append(report.Reasons, "message append backpressured errors were observed")
	}
	if report.MessageAppendRouteNotReadyErrCount > 0 {
		report.Reasons = append(report.Reasons, "message append route_not_ready errors were observed")
	}
	if report.MessageAppendStaleRouteErrCount > 0 {
		report.Reasons = append(report.Reasons, "message append stale_route errors were observed")
	}
	if report.MessageAppendNotLeaderErrCount > 0 {
		report.Reasons = append(report.Reasons, "message append not_leader errors were observed")
	}
	if report.MessageAppendChannelNotFoundErrCount > 0 {
		report.Reasons = append(report.Reasons, "message append channel_not_found errors were observed")
	}
	if report.MessageAppendShortResultErrCount > 0 {
		report.Reasons = append(report.Reasons, "message append short_result errors were observed")
	}
	if report.MessageAppendInvalidConfigErrCount > 0 {
		report.Reasons = append(report.Reasons, "message append invalid_config errors were observed")
	}
	if report.MessageAppendClosedErrCount > 0 {
		report.Reasons = append(report.Reasons, "message append closed errors were observed")
	}
	if report.MessageAppendTooManyChannelsErrCount > 0 {
		report.Reasons = append(report.Reasons, "message append too_many_channels errors were observed")
	}
	if report.MessageAppendNotStartedErrCount > 0 {
		report.Reasons = append(report.Reasons, "message append not_started errors were observed")
	}
	if report.MessageAppendCanceledErrCount > 0 {
		report.Reasons = append(report.Reasons, "message append canceled errors were observed")
	}
	if report.MessageAppendTimeoutErrCount > 0 {
		report.Reasons = append(report.Reasons, "message append timeout errors were observed")
	}
	if report.MessageAppendAppendFailedErrCount > 0 {
		report.Reasons = append(report.Reasons, "message append append_failed errors were observed")
	}
	if report.MessageAppendRemoteErrCount > 0 {
		report.Reasons = append(report.Reasons, "message append remote errors were observed")
	}
	if report.MessageAppendOtherErrCount > 0 {
		report.Reasons = append(report.Reasons, "message append other errors were observed")
	}

	controllerPressure := false
	if report.ControllerRaftStepQueueRatio >= wukongIMV2GatewayQueuePressureRatio {
		controllerPressure = true
		report.Reasons = append(report.Reasons, "ControllerV2 Raft Step queue ratio is high")
	}
	if report.ControllerRaftStepEnqueueOKP99Seconds >= wukongIMV2LatencyPressureSeconds {
		controllerPressure = true
		report.Reasons = append(report.Reasons, "ControllerV2 Raft Step enqueue ok p99 is high")
	}
	if report.ControllerRaftStepEnqueueErrCount > 0 {
		controllerPressure = true
		report.Reasons = append(report.Reasons, "ControllerV2 Raft Step enqueue errors were observed")
	}

	channelPressure := false
	if report.ChannelV2ReactorMailboxDepthMax > 0 {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 reactor mailbox has queued events")
	}
	if report.ChannelV2WorkerQueueDepthMax > 0 {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 worker pool has queued tasks")
	}
	if report.ChannelV2AppendP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 append p99 is high")
	}
	if report.ChannelV2MetaResolveP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 meta resolve p99 is high")
	}
	if report.ChannelV2MetaSlotReadP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 meta slot read p99 is high")
	}
	if report.ChannelV2MetaCreateBuildP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 meta create/build p99 is high")
	}
	if report.ChannelV2MetaCreateProposeP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 meta create/propose p99 is high")
	}
	if report.ChannelV2MetaCreateProposeLocalP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 meta create/propose local p99 is high")
	}
	if report.ChannelV2MetaCreateProposeForwardP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 meta create/propose forward p99 is high")
	}
	if report.ChannelV2MetaCreateSlotProposeSubmitP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 meta create Slot propose submit p99 is high")
	}
	if report.ChannelV2MetaCreateSlotProposeWaitP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 meta create Slot propose wait p99 is high")
	}
	if report.ChannelV2MetaCreateSlotControlWaitP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 meta create Slot control wait p99 is high")
	}
	if report.ChannelV2MetaCreateSlotRaftCommitWaitP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 meta create Slot Raft commit wait p99 is high")
	}
	if report.ChannelV2MetaCreateSlotFSMApplyP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 meta create Slot FSM apply p99 is high")
	}
	if report.ChannelV2MetaCreateSlotFSMCommitP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 meta create Slot FSM commit p99 is high")
	}
	if report.ChannelV2MetaCreateSlotMarkAppliedP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 meta create Slot mark-applied p99 is high")
	}
	if report.ChannelV2MetaCreateWriteP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 meta create/write p99 is high")
	}
	if report.ChannelV2MetaFinalReadP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 meta final read p99 is high")
	}
	if report.ChannelV2MetaApplyP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 meta apply p99 is high")
	}
	if report.ChannelV2RuntimeAppendP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 runtime append p99 is high")
	}
	if report.ChannelV2RuntimeAppendReserveWaitP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 runtime append reserve wait p99 is high")
	}
	if report.ChannelV2RuntimeAppendSubmitP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 runtime append submit p99 is high")
	}
	if report.ChannelV2RuntimeAppendWaitP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 runtime append future wait p99 is high")
	}
	if report.ChannelV2AppendBatchWaitP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 append batch wait p99 is high")
	}
	if report.ChannelV2AppendStoreWaitP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 append store wait p99 is high")
	}
	if report.ChannelV2AppendPostStoreCommitWaitP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 append post-store commit wait p99 is high")
	}
	if report.ChannelV2AppendQuorumFollowerPullWaitP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 quorum follower pull wait p99 is high")
	}
	if report.ChannelV2AppendQuorumAckOffsetWaitP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 quorum ack offset wait p99 is high")
	}
	if report.ChannelV2AppendQuorumHWAdvanceWaitP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 quorum HW advance wait p99 is high")
	}
	if report.ChannelV2AppendQuorumFinalCompleteP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 quorum final completion wait p99 is high")
	}
	if report.ChannelV2ReplicationPullHintToSubmitP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 follower PullHint-to-submit p99 is high")
	}
	if report.ChannelV2ReplicationPullRPCP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 follower pull RPC p99 is high")
	}
	if report.ChannelV2NeedMetaPullRPCP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 follower NeedMeta pull RPC p99 is high")
	}
	if report.ChannelV2ReplicationStoreApplyP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 follower store apply p99 is high")
	}
	if report.ChannelV2ReplicationApplyToAckReturnP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 follower apply-to-AckOffset-return p99 is high")
	}
	if report.ChannelV2PendingMetaCurrentMax > 0 {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 PendingMeta shells remain active")
	}
	if report.ChannelV2PendingMetaReleasedCount > 0 {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 PendingMeta shells were released before conversion")
	}
	if report.ChannelV2NeedMetaPullRetryCount > 0 {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 NeedMeta pull retries were observed")
	}
	if report.ChannelV2NeedMetaPullErrCount > 0 {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 NeedMeta pull errors were observed")
	}
	if report.ChannelV2NeedMetaPullTimeoutErrCount > 0 {
		report.Reasons = append(report.Reasons, "ChannelV2 NeedMeta pull timeout errors were observed")
	}
	if report.ChannelV2NeedMetaPullNotReadyErrCount > 0 {
		report.Reasons = append(report.Reasons, "ChannelV2 NeedMeta pull not_ready errors were observed")
	}
	if report.ChannelV2NeedMetaPullNotReplicaErrCount > 0 {
		report.Reasons = append(report.Reasons, "ChannelV2 NeedMeta pull not_replica errors were observed")
	}
	if report.ChannelV2NeedMetaPullBackpressuredErrCount > 0 {
		report.Reasons = append(report.Reasons, "ChannelV2 NeedMeta pull backpressured errors were observed")
	}
	if report.ChannelV2PullHintErrCount > 0 {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 PullHint errors were observed")
	}
	if report.ChannelV2PullHintStaleMetaErrCount > 0 {
		report.Reasons = append(report.Reasons, "ChannelV2 PullHint stale_meta errors were observed")
	}
	if report.ChannelV2PullHintChannelNotFoundErrCount > 0 {
		report.Reasons = append(report.Reasons, "ChannelV2 PullHint channel_not_found errors were observed")
	}
	if report.ChannelV2PullHintNotReadyErrCount > 0 {
		report.Reasons = append(report.Reasons, "ChannelV2 PullHint not_ready errors were observed")
	}
	if report.ChannelV2PullHintNotLeaderErrCount > 0 {
		report.Reasons = append(report.Reasons, "ChannelV2 PullHint not_leader errors were observed")
	}
	if report.ChannelV2PullHintInvalidConfigErrCount > 0 {
		report.Reasons = append(report.Reasons, "ChannelV2 PullHint invalid_config errors were observed")
	}
	if report.ChannelV2PullHintClosedErrCount > 0 {
		report.Reasons = append(report.Reasons, "ChannelV2 PullHint closed errors were observed")
	}
	if report.ChannelV2PullHintCanceledErrCount > 0 {
		report.Reasons = append(report.Reasons, "ChannelV2 PullHint canceled errors were observed")
	}
	if report.ChannelV2PullHintTimeoutErrCount > 0 {
		report.Reasons = append(report.Reasons, "ChannelV2 PullHint timeout errors were observed")
	}
	if report.ChannelV2PullHintRemoteErrCount > 0 {
		report.Reasons = append(report.Reasons, "ChannelV2 PullHint remote errors were observed")
	}
	if report.ChannelV2PullHintOtherErrCount > 0 {
		report.Reasons = append(report.Reasons, "ChannelV2 PullHint other errors were observed")
	}
	if report.ChannelV2PullHintReceiveErrCount > 0 {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 PullHint receive errors were observed")
	}
	if report.ChannelV2PullHintReceiveMetaResolveErrCount > 0 {
		report.Reasons = append(report.Reasons, "ChannelV2 PullHint receive meta_resolve errors were observed")
	}
	if report.ChannelV2PullHintReceiveMetaValidateErrCount > 0 {
		report.Reasons = append(report.Reasons, "ChannelV2 PullHint receive meta_validate errors were observed")
	}
	if report.ChannelV2PullHintReceiveSubmitErrCount > 0 {
		report.Reasons = append(report.Reasons, "ChannelV2 PullHint receive submit errors were observed")
	}
	if report.ChannelV2PullHintReceiveAwaitErrCount > 0 {
		report.Reasons = append(report.Reasons, "ChannelV2 PullHint receive await errors were observed")
	}
	if report.ChannelV2PullHintReceiveChannelNotFoundErrCount > 0 {
		report.Reasons = append(report.Reasons, "ChannelV2 PullHint receive channel_not_found errors were observed")
	}
	if report.ChannelV2WorkerTaskP99Seconds >= wukongIMV2LatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "ChannelV2 worker task p99 is high")
	}

	storagePressure := false
	if report.StorageCommitQueueDepthMax > 0 {
		storagePressure = true
		report.Reasons = append(report.Reasons, "storage commit queue has pending requests")
	}
	if report.StorageCommitP99Seconds >= wukongIMV2LatencyPressureSeconds {
		storagePressure = true
		report.Reasons = append(report.Reasons, "storage physical commit p99 is high")
	}
	if report.StorageCommitTotalP99Seconds >= wukongIMV2LatencyPressureSeconds {
		storagePressure = true
		report.Reasons = append(report.Reasons, "storage grouped commit total p99 is high")
	}
	if report.StorageCommitRequestP99Seconds >= wukongIMV2LatencyPressureSeconds {
		storagePressure = true
		report.Reasons = append(report.Reasons, "storage logical commit request p99 is high")
	}
	if report.StorageCommitRequestTimeoutCount > 0 {
		storagePressure = true
		report.Reasons = append(report.Reasons, "storage logical commit request timeouts were observed")
	}
	if report.StorageCommitRequestCanceledCount > 0 {
		storagePressure = true
		report.Reasons = append(report.Reasons, "storage logical commit request cancellations were observed")
	}
	if report.StorageCommitRequestClosedCount > 0 {
		storagePressure = true
		report.Reasons = append(report.Reasons, "storage logical commit request closed errors were observed")
	}
	if report.StorageCommitRequestErrCount > 0 {
		storagePressure = true
		report.Reasons = append(report.Reasons, "storage logical commit request errors were observed")
	}
	if report.StorageCommitRequestOver1sCount > 0 {
		storagePressure = true
		report.Reasons = append(report.Reasons, "storage logical commit requests exceeded 1s")
	}
	if report.StorageCommitRequestOver5sCount > 0 {
		storagePressure = true
		report.Reasons = append(report.Reasons, "storage logical commit requests exceeded 5s")
	}
	if report.StorageCommitRequestOver10sCount > 0 {
		storagePressure = true
		report.Reasons = append(report.Reasons, "storage logical commit requests exceeded 10s")
	}

	switch {
	case gatewayPressure && (controllerPressure || channelPressure || storagePressure):
		report.Classification = WukongIMV2BottleneckMixed
	case controllerPressure && (channelPressure || storagePressure):
		report.Classification = WukongIMV2BottleneckMixed
	case gatewayPressure:
		report.Classification = WukongIMV2BottleneckGateway
	case controllerPressure:
		report.Classification = WukongIMV2BottleneckControllerRaft
	case storagePressure:
		report.Classification = WukongIMV2BottleneckStorageCommit
	case channelPressure:
		report.Classification = WukongIMV2BottleneckChannelV2
	}
	return report
}

func (s PrometheusSnapshot) maxGauge(name string) (float64, bool) {
	found := false
	var maxValue float64
	for _, sample := range s.Samples {
		if sample.Name != name {
			continue
		}
		if !found || sample.Value > maxValue {
			maxValue = sample.Value
			found = true
		}
	}
	return maxValue, found
}

func (s PrometheusSnapshot) maxGaugeByLabel(name string, label string) map[string]float64 {
	out := map[string]float64{}
	for _, sample := range s.Samples {
		if sample.Name != name {
			continue
		}
		value := sample.Labels[label]
		if value == "" {
			continue
		}
		current, ok := out[value]
		if !ok || sample.Value > current {
			out[value] = sample.Value
		}
	}
	return out
}

type histogramBucket struct {
	le    float64
	count float64
}

func histogramQuantileDelta(q float64, before, after PrometheusSnapshot, family string) (float64, bool) {
	return histogramQuantileDeltaMatching(q, before, after, family, nil)
}

func histogramQuantileDeltaByLabel(q float64, before, after PrometheusSnapshot, family string, label string) map[string]float64 {
	out := map[string]float64{}
	for _, value := range histogramLabelValues(before, after, family, label) {
		quantile, ok := histogramQuantileDeltaMatching(q, before, after, family, map[string]string{label: value})
		if ok {
			out[value] = quantile
		}
	}
	return out
}

func histogramLabelValues(before, after PrometheusSnapshot, family string, label string) []string {
	values := map[string]struct{}{}
	before.collectHistogramLabelValues(family, label, values)
	after.collectHistogramLabelValues(family, label, values)
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func (s PrometheusSnapshot) collectHistogramLabelValues(family string, label string, values map[string]struct{}) {
	bucketName := family + "_bucket"
	for _, sample := range s.Samples {
		if sample.Name != bucketName {
			continue
		}
		value := sample.Labels[label]
		if value == "" {
			continue
		}
		values[value] = struct{}{}
	}
}

func histogramCountDeltaMatching(before, after PrometheusSnapshot, family string, labels map[string]string) (float64, bool) {
	beforeBuckets := before.histogramBucketsMatching(family, labels)
	afterBuckets := after.histogramBucketsMatching(family, labels)
	afterCount, ok := afterBuckets[math.Inf(1)]
	if !ok {
		return 0, false
	}
	delta := afterCount - beforeBuckets[math.Inf(1)]
	if delta < 0 {
		delta = 0
	}
	return delta, true
}

func histogramCountAboveDeltaMatching(before, after PrometheusSnapshot, family string, labels map[string]string, threshold float64) (float64, bool) {
	beforeBuckets := before.histogramBucketsMatching(family, labels)
	afterBuckets := after.histogramBucketsMatching(family, labels)
	afterTotal, ok := afterBuckets[math.Inf(1)]
	if !ok {
		return 0, false
	}
	totalDelta := afterTotal - beforeBuckets[math.Inf(1)]
	if totalDelta < 0 {
		totalDelta = 0
	}
	beforeLE, beforeOK := histogramCumulativeAtOrBelow(beforeBuckets, threshold)
	afterLE, afterOK := histogramCumulativeAtOrBelow(afterBuckets, threshold)
	if !beforeOK && !afterOK {
		return totalDelta, true
	}
	leDelta := afterLE - beforeLE
	if leDelta < 0 {
		leDelta = 0
	}
	above := totalDelta - leDelta
	if above < 0 {
		above = 0
	}
	return above, true
}

func histogramCumulativeAtOrBelow(buckets map[float64]float64, threshold float64) (float64, bool) {
	found := false
	bestLE := 0.0
	value := 0.0
	for le, count := range buckets {
		if math.IsInf(le, 1) || le > threshold {
			continue
		}
		if !found || le > bestLE {
			found = true
			bestLE = le
			value = count
		}
	}
	return value, found
}

func counterDeltaMatching(before, after PrometheusSnapshot, family string, labels map[string]string) (float64, bool) {
	beforeValue, _ := before.counterValueMatching(family, labels)
	afterValue, ok := after.counterValueMatching(family, labels)
	if !ok {
		return 0, false
	}
	delta := afterValue - beforeValue
	if delta < 0 {
		delta = 0
	}
	return delta, true
}

func (s PrometheusSnapshot) counterValueMatching(family string, labels map[string]string) (float64, bool) {
	found := false
	var total float64
	for _, sample := range s.Samples {
		if sample.Name != family {
			continue
		}
		if !prometheusLabelsMatch(sample.Labels, labels) {
			continue
		}
		total += sample.Value
		found = true
	}
	return total, found
}

func histogramQuantileDeltaMatching(q float64, before, after PrometheusSnapshot, family string, labels map[string]string) (float64, bool) {
	beforeBuckets := before.histogramBucketsMatching(family, labels)
	afterBuckets := after.histogramBucketsMatching(family, labels)
	if len(afterBuckets) == 0 {
		return 0, false
	}
	buckets := make([]histogramBucket, 0, len(afterBuckets))
	for le, afterCount := range afterBuckets {
		delta := afterCount - beforeBuckets[le]
		if delta < 0 {
			delta = 0
		}
		buckets = append(buckets, histogramBucket{le: le, count: delta})
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].le < buckets[j].le })
	return histogramQuantile(q, buckets)
}

func (s PrometheusSnapshot) histogramBucketsMatching(family string, labels map[string]string) map[float64]float64 {
	out := map[float64]float64{}
	bucketName := family + "_bucket"
	for _, sample := range s.Samples {
		if sample.Name != bucketName {
			continue
		}
		if !prometheusLabelsMatch(sample.Labels, labels) {
			continue
		}
		rawLE := sample.Labels["le"]
		le, err := parsePrometheusLE(rawLE)
		if err != nil {
			continue
		}
		out[le] += sample.Value
	}
	return out
}

func prometheusLabelsMatch(got map[string]string, want map[string]string) bool {
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}

func parsePrometheusLE(raw string) (float64, error) {
	if raw == "+Inf" || raw == "Inf" {
		return math.Inf(1), nil
	}
	return strconv.ParseFloat(raw, 64)
}

func histogramQuantile(q float64, buckets []histogramBucket) (float64, bool) {
	if len(buckets) == 0 {
		return 0, false
	}
	total := buckets[len(buckets)-1].count
	if total <= 0 {
		return 0, false
	}
	rank := q * total
	prevCount := 0.0
	prevBound := 0.0
	for _, bucket := range buckets {
		if bucket.count < rank {
			prevCount = bucket.count
			if !math.IsInf(bucket.le, 1) {
				prevBound = bucket.le
			}
			continue
		}
		if math.IsInf(bucket.le, 1) {
			return prevBound, true
		}
		inBucket := bucket.count - prevCount
		if inBucket <= 0 {
			return bucket.le, true
		}
		fraction := (rank - prevCount) / inBucket
		return prevBound + (bucket.le-prevBound)*fraction, true
	}
	return buckets[len(buckets)-1].le, true
}
