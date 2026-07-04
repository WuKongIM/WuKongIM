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
	WukongIMBottleneckGateway        = "gateway_dispatch"
	WukongIMBottleneckControllerRaft = "controller_raft_step"
	WukongIMBottleneckChannelRuntime = "channel_append"
	WukongIMBottleneckStorageCommit  = "storage_commit"
	WukongIMBottleneckMixed          = "mixed_backpressure"
	WukongIMBottleneckUnobserved     = "no_observed_queue_pressure"
)

const (
	wukongIMGatewayQueuePressureRatio = 0.25
	wukongIMLatencyPressureSeconds    = 0.02
)

const (
	channelRuntimePromotedMetricPrefix = "wukongim_channel_"
	channelRuntimeLegacyMetricPrefix   = "wukongim_channelv2_"
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

// WukongIMAttribution summarizes gateway vs Channel runtime pressure from two snapshots.
type WukongIMAttribution struct {
	// Classification is the coarse bottleneck class inferred from observed pressure.
	Classification string
	// Reasons explains the evidence that produced the classification.
	Reasons []string

	GatewayQueueDepth                                    float64
	GatewayQueueCapacity                                 float64
	GatewayQueueRatio                                    float64
	GatewayDispatchWaitP99Seconds                        float64
	GatewayBatchRecordsP50                               float64
	GatewaySendackSystemErrorCount                       float64
	GatewaySendackSingleErrorCount                       float64
	GatewaySendackSingleMissingRequestContextCount       float64
	GatewaySendackBatchPrecheckCount                     float64
	GatewaySendackBatchMissingRequestContextCount        float64
	GatewaySendackBatchResultErrorCount                  float64
	GatewaySendackBatchResultTimeoutCount                float64
	GatewaySendackBatchResultCanceledCount               float64
	GatewaySendackBatchResultOtherCount                  float64
	GatewaySendackBatchFallbackErrorCount                float64
	MessageAppendErrorCount                              float64
	MessageAppendBackpressuredErrCount                   float64
	MessageAppendRouteNotReadyErrCount                   float64
	MessageAppendStaleRouteErrCount                      float64
	MessageAppendNotLeaderErrCount                       float64
	MessageAppendChannelNotFoundErrCount                 float64
	MessageAppendShortResultErrCount                     float64
	MessageAppendInvalidConfigErrCount                   float64
	MessageAppendClosedErrCount                          float64
	MessageAppendTooManyChannelsErrCount                 float64
	MessageAppendNotStartedErrCount                      float64
	MessageAppendCanceledErrCount                        float64
	MessageAppendTimeoutErrCount                         float64
	MessageAppendAppendFailedErrCount                    float64
	MessageAppendRemoteErrCount                          float64
	MessageAppendOtherErrCount                           float64
	ControllerRaftStepQueueDepth                         float64
	ControllerRaftStepQueueCapacity                      float64
	ControllerRaftStepQueueRatio                         float64
	ControllerRaftStepEnqueueOKP99Seconds                float64
	ControllerRaftStepEnqueueErrP99Seconds               float64
	ControllerRaftStepEnqueueErrCount                    float64
	ChannelRuntimeReactorMailboxDepthMax                 float64
	ChannelRuntimeWorkerQueueDepthMax                    float64
	ChannelRuntimeWorkerQueueDepthByPool                 map[string]float64
	ChannelRuntimeWorkerInflightByPool                   map[string]float64
	ChannelRuntimeWorkerInflightPeakByPool               map[string]float64
	ChannelRuntimeAppendP99Seconds                       float64
	ChannelRuntimeMetaResolveP99Seconds                  float64
	ChannelRuntimeMetaSlotReadP99Seconds                 float64
	ChannelRuntimeMetaCreateBuildP99Seconds              float64
	ChannelRuntimeMetaCreateProposeP99Seconds            float64
	ChannelRuntimeMetaCreateProposeLocalP99Seconds       float64
	ChannelRuntimeMetaCreateProposeForwardP99Seconds     float64
	ChannelRuntimeMetaCreateSlotProposeSubmitP99Seconds  float64
	ChannelRuntimeMetaCreateSlotProposeWaitP99Seconds    float64
	ChannelRuntimeMetaCreateSlotControlWaitP99Seconds    float64
	ChannelRuntimeMetaCreateSlotRaftCommitWaitP99Seconds float64
	ChannelRuntimeMetaCreateSlotFSMApplyP99Seconds       float64
	ChannelRuntimeMetaCreateSlotFSMCommitP99Seconds      float64
	ChannelRuntimeMetaCreateSlotMarkAppliedP99Seconds    float64
	ChannelRuntimeMetaCreateWriteP99Seconds              float64
	ChannelRuntimeMetaFinalReadP99Seconds                float64
	ChannelRuntimeMetaApplyP99Seconds                    float64
	ChannelRuntimeFacadeAppendP99Seconds                 float64
	ChannelRuntimeFacadeAppendReserveWaitP99Seconds      float64
	ChannelRuntimeFacadeAppendSubmitP99Seconds           float64
	ChannelRuntimeFacadeAppendWaitP99Seconds             float64
	ChannelRuntimeAppendBatchWaitP99Seconds              float64
	ChannelRuntimeAppendStoreWaitP99Seconds              float64
	ChannelRuntimeAppendPostStoreCommitWaitP99Seconds    float64
	ChannelRuntimeAppendQuorumFollowerPullWaitP99Seconds float64
	ChannelRuntimeAppendQuorumAckOffsetWaitP99Seconds    float64
	ChannelRuntimeAppendQuorumHWAdvanceWaitP99Seconds    float64
	ChannelRuntimeAppendQuorumFinalCompleteP99Seconds    float64
	ChannelRuntimeReplicationPullHintToSubmitP99Seconds  float64
	ChannelRuntimeReplicationPullRPCP99Seconds           float64
	ChannelRuntimeNeedMetaPullRPCP99Seconds              float64
	ChannelRuntimeReplicationStoreApplyP99Seconds        float64
	ChannelRuntimeReplicationApplyToAckReturnP99Seconds  float64
	ChannelRuntimePendingMetaCurrentMax                  float64
	ChannelRuntimePendingMetaCreatedCount                float64
	ChannelRuntimePendingMetaConvertedCount              float64
	ChannelRuntimePendingMetaReleasedCount               float64
	ChannelRuntimePendingMetaTimeoutReleaseCount         float64
	ChannelRuntimePendingMetaNotReadyReleaseCount        float64
	ChannelRuntimePullHintSubmittedCount                 float64
	ChannelRuntimePullHintOKCount                        float64
	ChannelRuntimePullHintErrCount                       float64
	ChannelRuntimePullHintStaleMetaErrCount              float64
	ChannelRuntimePullHintChannelNotFoundErrCount        float64
	ChannelRuntimePullHintNotReadyErrCount               float64
	ChannelRuntimePullHintNotLeaderErrCount              float64
	ChannelRuntimePullHintInvalidConfigErrCount          float64
	ChannelRuntimePullHintClosedErrCount                 float64
	ChannelRuntimePullHintCanceledErrCount               float64
	ChannelRuntimePullHintTimeoutErrCount                float64
	ChannelRuntimePullHintRemoteErrCount                 float64
	ChannelRuntimePullHintOtherErrCount                  float64
	ChannelRuntimePullHintReceiveOKCount                 float64
	ChannelRuntimePullHintReceiveErrCount                float64
	ChannelRuntimePullHintReceiveStateCheckErrCount      float64
	ChannelRuntimePullHintReceiveMetaResolveErrCount     float64
	ChannelRuntimePullHintReceiveMetaHintOKCount         float64
	ChannelRuntimePullHintReceiveMetaValidateErrCount    float64
	ChannelRuntimePullHintReceiveMetaApplyErrCount       float64
	ChannelRuntimePullHintReceiveSubmitErrCount          float64
	ChannelRuntimePullHintReceiveAwaitErrCount           float64
	ChannelRuntimePullHintReceiveStaleMetaErrCount       float64
	ChannelRuntimePullHintReceiveChannelNotFoundErrCount float64
	ChannelRuntimePullHintReceiveNotReadyErrCount        float64
	ChannelRuntimePullHintReceiveCanceledErrCount        float64
	ChannelRuntimePullHintReceiveTimeoutErrCount         float64
	ChannelRuntimePullHintReceiveOtherErrCount           float64
	ChannelRuntimeNeedMetaPullSubmittedCount             float64
	ChannelRuntimeNeedMetaPullOKCount                    float64
	ChannelRuntimeNeedMetaPullRetryCount                 float64
	ChannelRuntimeNeedMetaPullErrCount                   float64
	ChannelRuntimeNeedMetaPullStaleMetaErrCount          float64
	ChannelRuntimeNeedMetaPullChannelNotFoundErrCount    float64
	ChannelRuntimeNeedMetaPullNotReadyErrCount           float64
	ChannelRuntimeNeedMetaPullNotLeaderErrCount          float64
	ChannelRuntimeNeedMetaPullNotReplicaErrCount         float64
	ChannelRuntimeNeedMetaPullBackpressuredErrCount      float64
	ChannelRuntimeNeedMetaPullInvalidConfigErrCount      float64
	ChannelRuntimeNeedMetaPullClosedErrCount             float64
	ChannelRuntimeNeedMetaPullCanceledErrCount           float64
	ChannelRuntimeNeedMetaPullTimeoutErrCount            float64
	ChannelRuntimeNeedMetaPullRemoteErrCount             float64
	ChannelRuntimeNeedMetaPullOtherErrCount              float64
	ChannelRuntimeWorkerTaskP99Seconds                   float64
	ChannelRuntimeWorkerTaskP99SecondsByKind             map[string]float64
	ChannelRuntimeAppendBatchRecordsP50                  float64
	StorageCommitQueueDepthMax                           float64
	StorageCommitBatchRequestsP50                        float64
	StorageCommitBatchRecordsP50                         float64
	StorageCommitP99Seconds                              float64
	StorageCommitTotalP99Seconds                         float64
	StorageCommitRequestP99Seconds                       float64
	StorageCommitRequestOKP99Seconds                     float64
	StorageCommitRequestTimeoutCount                     float64
	StorageCommitRequestCanceledCount                    float64
	StorageCommitRequestClosedCount                      float64
	StorageCommitRequestErrCount                         float64
	StorageCommitRequestOver1sCount                      float64
	StorageCommitRequestOver5sCount                      float64
	StorageCommitRequestOver10sCount                     float64
	StorageCommitRequestP99SecondsByLane                 map[string]float64
	StorageCommitRequestOver1sCountByLane                map[string]float64
	StorageCommitRequestOver5sCountByLane                map[string]float64
	StorageCommitRequestOver10sCountByLane               map[string]float64
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

// AnalyzeWukongIMPrometheus classifies gateway vs Channel runtime pressure between two snapshots.
func AnalyzeWukongIMPrometheus(before, after PrometheusSnapshot) WukongIMAttribution {
	report := WukongIMAttribution{
		Classification:                           WukongIMBottleneckUnobserved,
		ChannelRuntimeWorkerQueueDepthByPool:     map[string]float64{},
		ChannelRuntimeWorkerInflightByPool:       map[string]float64{},
		ChannelRuntimeWorkerInflightPeakByPool:   map[string]float64{},
		ChannelRuntimeWorkerTaskP99SecondsByKind: map[string]float64{},
		StorageCommitRequestP99SecondsByLane:     map[string]float64{},
		StorageCommitRequestOver1sCountByLane:    map[string]float64{},
		StorageCommitRequestOver5sCountByLane:    map[string]float64{},
		StorageCommitRequestOver10sCountByLane:   map[string]float64{},
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

	report.ChannelRuntimeReactorMailboxDepthMax, _ = after.maxGauge("wukongim_channelv2_reactor_mailbox_depth")
	report.ChannelRuntimeWorkerQueueDepthMax, _ = after.maxGauge("wukongim_channelv2_worker_queue_depth")
	report.ChannelRuntimeWorkerQueueDepthByPool = after.maxGaugeByLabel("wukongim_channelv2_worker_queue_depth", "pool")
	report.ChannelRuntimeWorkerInflightByPool = after.maxGaugeByLabel("wukongim_channelv2_worker_inflight", "pool")
	report.ChannelRuntimeWorkerInflightPeakByPool = after.maxGaugeByLabel("wukongim_channelv2_worker_inflight_peak", "pool")
	report.ChannelRuntimeAppendP99Seconds, _ = histogramQuantileDelta(0.99, before, after, "wukongim_channelv2_append_duration_seconds")
	report.ChannelRuntimeMetaResolveP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_resolve"})
	report.ChannelRuntimeMetaSlotReadP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_slot_read"})
	report.ChannelRuntimeMetaCreateBuildP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_build"})
	report.ChannelRuntimeMetaCreateProposeP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_propose"})
	report.ChannelRuntimeMetaCreateProposeLocalP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_propose_local"})
	report.ChannelRuntimeMetaCreateProposeForwardP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_propose_forward"})
	report.ChannelRuntimeMetaCreateSlotProposeSubmitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_slot_propose_submit"})
	report.ChannelRuntimeMetaCreateSlotProposeWaitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_slot_propose_wait"})
	report.ChannelRuntimeMetaCreateSlotControlWaitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_slot_control_wait"})
	report.ChannelRuntimeMetaCreateSlotRaftCommitWaitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_slot_raft_commit_wait"})
	report.ChannelRuntimeMetaCreateSlotFSMApplyP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_slot_fsm_apply"})
	report.ChannelRuntimeMetaCreateSlotFSMCommitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_slot_fsm_commit"})
	report.ChannelRuntimeMetaCreateSlotMarkAppliedP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_slot_mark_applied"})
	report.ChannelRuntimeMetaCreateWriteP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_create_write"})
	report.ChannelRuntimeMetaFinalReadP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_final_read"})
	report.ChannelRuntimeMetaApplyP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "meta_apply"})
	report.ChannelRuntimeFacadeAppendP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "runtime_append"})
	report.ChannelRuntimeFacadeAppendReserveWaitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "runtime_append_reserve_wait"})
	report.ChannelRuntimeFacadeAppendSubmitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "runtime_append_submit"})
	report.ChannelRuntimeFacadeAppendWaitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_stage_duration_seconds", map[string]string{"stage": "runtime_append_wait"})
	report.ChannelRuntimeAppendBatchWaitP99Seconds, _ = histogramQuantileDelta(0.99, before, after, "wukongim_channelv2_append_batch_wait_duration_seconds")
	report.ChannelRuntimeAppendStoreWaitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_wait_stage_duration_seconds", map[string]string{"stage": "store_append_wait"})
	report.ChannelRuntimeAppendPostStoreCommitWaitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_wait_stage_duration_seconds", map[string]string{"stage": "post_store_commit_wait"})
	report.ChannelRuntimeAppendQuorumFollowerPullWaitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_wait_stage_duration_seconds", map[string]string{"stage": "quorum_follower_pull_wait"})
	report.ChannelRuntimeAppendQuorumAckOffsetWaitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_wait_stage_duration_seconds", map[string]string{"stage": "quorum_ack_offset_wait"})
	report.ChannelRuntimeAppendQuorumHWAdvanceWaitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_wait_stage_duration_seconds", map[string]string{"stage": "quorum_hw_advance_wait"})
	report.ChannelRuntimeAppendQuorumFinalCompleteP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_append_wait_stage_duration_seconds", map[string]string{"stage": "quorum_final_complete_wait"})
	report.ChannelRuntimeReplicationPullHintToSubmitP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_replication_stage_duration_seconds", map[string]string{"stage": "follower_pull_hint_to_submit"})
	report.ChannelRuntimeReplicationPullRPCP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_replication_stage_duration_seconds", map[string]string{"stage": "follower_pull_rpc"})
	report.ChannelRuntimeNeedMetaPullRPCP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_replication_stage_duration_seconds", map[string]string{"stage": "follower_need_meta_pull_rpc"})
	report.ChannelRuntimeReplicationStoreApplyP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_replication_stage_duration_seconds", map[string]string{"stage": "follower_store_apply"})
	report.ChannelRuntimeReplicationApplyToAckReturnP99Seconds, _ = histogramQuantileDeltaMatching(0.99, before, after, "wukongim_channelv2_replication_stage_duration_seconds", map[string]string{"stage": "follower_apply_to_ack_return"})
	report.ChannelRuntimePendingMetaCurrentMax, _ = after.maxGauge("wukongim_channelv2_pending_meta_current")
	report.ChannelRuntimePendingMetaCreatedCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pending_meta_total", map[string]string{"event": "created"})
	report.ChannelRuntimePendingMetaConvertedCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pending_meta_total", map[string]string{"event": "converted"})
	report.ChannelRuntimePendingMetaReleasedCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pending_meta_total", map[string]string{"event": "released"})
	report.ChannelRuntimePendingMetaTimeoutReleaseCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pending_meta_total", map[string]string{"event": "released", "error": "timeout"})
	report.ChannelRuntimePendingMetaNotReadyReleaseCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pending_meta_total", map[string]string{"event": "released", "error": "not_ready"})
	report.ChannelRuntimePullHintSubmittedCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "submitted"})
	report.ChannelRuntimePullHintOKCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "ok"})
	report.ChannelRuntimePullHintErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err"})
	report.ChannelRuntimePullHintStaleMetaErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err", "error": "stale_meta"})
	report.ChannelRuntimePullHintChannelNotFoundErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err", "error": "channel_not_found"})
	report.ChannelRuntimePullHintNotReadyErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err", "error": "not_ready"})
	report.ChannelRuntimePullHintNotLeaderErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err", "error": "not_leader"})
	report.ChannelRuntimePullHintInvalidConfigErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err", "error": "invalid_config"})
	report.ChannelRuntimePullHintClosedErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err", "error": "closed"})
	report.ChannelRuntimePullHintCanceledErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err", "error": "canceled"})
	report.ChannelRuntimePullHintTimeoutErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err", "error": "timeout"})
	report.ChannelRuntimePullHintRemoteErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err", "error": "remote_error"})
	report.ChannelRuntimePullHintOtherErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_total", map[string]string{"result": "err", "error": "other"})
	report.ChannelRuntimePullHintReceiveOKCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"result": "ok"})
	report.ChannelRuntimePullHintReceiveErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"result": "err"})
	report.ChannelRuntimePullHintReceiveStateCheckErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"stage": "state_check", "result": "err"})
	report.ChannelRuntimePullHintReceiveMetaResolveErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"stage": "meta_resolve", "result": "err"})
	report.ChannelRuntimePullHintReceiveMetaHintOKCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"stage": "meta_hint", "result": "ok"})
	report.ChannelRuntimePullHintReceiveMetaValidateErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"stage": "meta_validate", "result": "err"})
	report.ChannelRuntimePullHintReceiveMetaApplyErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"stage": "meta_apply", "result": "err"})
	report.ChannelRuntimePullHintReceiveSubmitErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"stage": "submit", "result": "err"})
	report.ChannelRuntimePullHintReceiveAwaitErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"stage": "await", "result": "err"})
	report.ChannelRuntimePullHintReceiveStaleMetaErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"result": "err", "error": "stale_meta"})
	report.ChannelRuntimePullHintReceiveChannelNotFoundErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"result": "err", "error": "channel_not_found"})
	report.ChannelRuntimePullHintReceiveNotReadyErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"result": "err", "error": "not_ready"})
	report.ChannelRuntimePullHintReceiveCanceledErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"result": "err", "error": "canceled"})
	report.ChannelRuntimePullHintReceiveTimeoutErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"result": "err", "error": "timeout"})
	report.ChannelRuntimePullHintReceiveOtherErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_pull_hint_receive_total", map[string]string{"result": "err", "error": "other"})
	report.ChannelRuntimeNeedMetaPullSubmittedCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "submitted"})
	report.ChannelRuntimeNeedMetaPullOKCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "ok"})
	report.ChannelRuntimeNeedMetaPullRetryCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "retry"})
	report.ChannelRuntimeNeedMetaPullErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err"})
	report.ChannelRuntimeNeedMetaPullStaleMetaErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "stale_meta"})
	report.ChannelRuntimeNeedMetaPullChannelNotFoundErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "channel_not_found"})
	report.ChannelRuntimeNeedMetaPullNotReadyErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "not_ready"})
	report.ChannelRuntimeNeedMetaPullNotLeaderErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "not_leader"})
	report.ChannelRuntimeNeedMetaPullNotReplicaErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "not_replica"})
	report.ChannelRuntimeNeedMetaPullBackpressuredErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "backpressured"})
	report.ChannelRuntimeNeedMetaPullInvalidConfigErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "invalid_config"})
	report.ChannelRuntimeNeedMetaPullClosedErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "closed"})
	report.ChannelRuntimeNeedMetaPullCanceledErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "canceled"})
	report.ChannelRuntimeNeedMetaPullTimeoutErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "timeout"})
	report.ChannelRuntimeNeedMetaPullRemoteErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "remote_error"})
	report.ChannelRuntimeNeedMetaPullOtherErrCount, _ = counterDeltaMatching(before, after, "wukongim_channelv2_need_meta_pull_total", map[string]string{"result": "err", "error": "other"})
	report.ChannelRuntimeWorkerTaskP99Seconds, _ = histogramQuantileDelta(0.99, before, after, "wukongim_channelv2_worker_task_duration_seconds")
	report.ChannelRuntimeWorkerTaskP99SecondsByKind = histogramQuantileDeltaByLabel(0.99, before, after, "wukongim_channelv2_worker_task_duration_seconds", "kind")
	report.ChannelRuntimeAppendBatchRecordsP50, _ = histogramQuantileDelta(0.50, before, after, "wukongim_channelv2_append_batch_records")
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
	if report.GatewayQueueRatio >= wukongIMGatewayQueuePressureRatio {
		gatewayPressure = true
		report.Reasons = append(report.Reasons, "gateway async SEND queue ratio is high")
	}
	if report.GatewayDispatchWaitP99Seconds >= wukongIMLatencyPressureSeconds {
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
	if report.ControllerRaftStepQueueRatio >= wukongIMGatewayQueuePressureRatio {
		controllerPressure = true
		report.Reasons = append(report.Reasons, "Controller Raft Step queue ratio is high")
	}
	if report.ControllerRaftStepEnqueueOKP99Seconds >= wukongIMLatencyPressureSeconds {
		controllerPressure = true
		report.Reasons = append(report.Reasons, "Controller Raft Step enqueue ok p99 is high")
	}
	if report.ControllerRaftStepEnqueueErrCount > 0 {
		controllerPressure = true
		report.Reasons = append(report.Reasons, "Controller Raft Step enqueue errors were observed")
	}

	channelPressure := false
	if report.ChannelRuntimeReactorMailboxDepthMax > 0 {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime reactor mailbox has queued events")
	}
	if report.ChannelRuntimeWorkerQueueDepthMax > 0 {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime worker pool has queued tasks")
	}
	if report.ChannelRuntimeAppendP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime append p99 is high")
	}
	if report.ChannelRuntimeMetaResolveP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime meta resolve p99 is high")
	}
	if report.ChannelRuntimeMetaSlotReadP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime meta slot read p99 is high")
	}
	if report.ChannelRuntimeMetaCreateBuildP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime meta create/build p99 is high")
	}
	if report.ChannelRuntimeMetaCreateProposeP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime meta create/propose p99 is high")
	}
	if report.ChannelRuntimeMetaCreateProposeLocalP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime meta create/propose local p99 is high")
	}
	if report.ChannelRuntimeMetaCreateProposeForwardP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime meta create/propose forward p99 is high")
	}
	if report.ChannelRuntimeMetaCreateSlotProposeSubmitP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime meta create Slot propose submit p99 is high")
	}
	if report.ChannelRuntimeMetaCreateSlotProposeWaitP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime meta create Slot propose wait p99 is high")
	}
	if report.ChannelRuntimeMetaCreateSlotControlWaitP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime meta create Slot control wait p99 is high")
	}
	if report.ChannelRuntimeMetaCreateSlotRaftCommitWaitP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime meta create Slot Raft commit wait p99 is high")
	}
	if report.ChannelRuntimeMetaCreateSlotFSMApplyP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime meta create Slot FSM apply p99 is high")
	}
	if report.ChannelRuntimeMetaCreateSlotFSMCommitP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime meta create Slot FSM commit p99 is high")
	}
	if report.ChannelRuntimeMetaCreateSlotMarkAppliedP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime meta create Slot mark-applied p99 is high")
	}
	if report.ChannelRuntimeMetaCreateWriteP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime meta create/write p99 is high")
	}
	if report.ChannelRuntimeMetaFinalReadP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime meta final read p99 is high")
	}
	if report.ChannelRuntimeMetaApplyP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime meta apply p99 is high")
	}
	if report.ChannelRuntimeFacadeAppendP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime append p99 is high")
	}
	if report.ChannelRuntimeFacadeAppendReserveWaitP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime append reserve wait p99 is high")
	}
	if report.ChannelRuntimeFacadeAppendSubmitP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime append submit p99 is high")
	}
	if report.ChannelRuntimeFacadeAppendWaitP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime append future wait p99 is high")
	}
	if report.ChannelRuntimeAppendBatchWaitP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime append batch wait p99 is high")
	}
	if report.ChannelRuntimeAppendStoreWaitP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime append store wait p99 is high")
	}
	if report.ChannelRuntimeAppendPostStoreCommitWaitP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime append post-store commit wait p99 is high")
	}
	if report.ChannelRuntimeAppendQuorumFollowerPullWaitP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime quorum follower pull wait p99 is high")
	}
	if report.ChannelRuntimeAppendQuorumAckOffsetWaitP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime quorum ack offset wait p99 is high")
	}
	if report.ChannelRuntimeAppendQuorumHWAdvanceWaitP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime quorum HW advance wait p99 is high")
	}
	if report.ChannelRuntimeAppendQuorumFinalCompleteP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime quorum final completion wait p99 is high")
	}
	if report.ChannelRuntimeReplicationPullHintToSubmitP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime follower PullHint-to-submit p99 is high")
	}
	if report.ChannelRuntimeReplicationPullRPCP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime follower pull RPC p99 is high")
	}
	if report.ChannelRuntimeNeedMetaPullRPCP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime follower NeedMeta pull RPC p99 is high")
	}
	if report.ChannelRuntimeReplicationStoreApplyP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime follower store apply p99 is high")
	}
	if report.ChannelRuntimeReplicationApplyToAckReturnP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime follower apply-to-AckOffset-return p99 is high")
	}
	if report.ChannelRuntimePendingMetaCurrentMax > 0 {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime PendingMeta shells remain active")
	}
	if report.ChannelRuntimePendingMetaReleasedCount > 0 {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime PendingMeta shells were released before conversion")
	}
	if report.ChannelRuntimeNeedMetaPullRetryCount > 0 {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime NeedMeta pull retries were observed")
	}
	if report.ChannelRuntimeNeedMetaPullErrCount > 0 {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime NeedMeta pull errors were observed")
	}
	if report.ChannelRuntimeNeedMetaPullTimeoutErrCount > 0 {
		report.Reasons = append(report.Reasons, "Channel runtime NeedMeta pull timeout errors were observed")
	}
	if report.ChannelRuntimeNeedMetaPullNotReadyErrCount > 0 {
		report.Reasons = append(report.Reasons, "Channel runtime NeedMeta pull not_ready errors were observed")
	}
	if report.ChannelRuntimeNeedMetaPullNotReplicaErrCount > 0 {
		report.Reasons = append(report.Reasons, "Channel runtime NeedMeta pull not_replica errors were observed")
	}
	if report.ChannelRuntimeNeedMetaPullBackpressuredErrCount > 0 {
		report.Reasons = append(report.Reasons, "Channel runtime NeedMeta pull backpressured errors were observed")
	}
	if report.ChannelRuntimePullHintErrCount > 0 {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime PullHint errors were observed")
	}
	if report.ChannelRuntimePullHintStaleMetaErrCount > 0 {
		report.Reasons = append(report.Reasons, "Channel runtime PullHint stale_meta errors were observed")
	}
	if report.ChannelRuntimePullHintChannelNotFoundErrCount > 0 {
		report.Reasons = append(report.Reasons, "Channel runtime PullHint channel_not_found errors were observed")
	}
	if report.ChannelRuntimePullHintNotReadyErrCount > 0 {
		report.Reasons = append(report.Reasons, "Channel runtime PullHint not_ready errors were observed")
	}
	if report.ChannelRuntimePullHintNotLeaderErrCount > 0 {
		report.Reasons = append(report.Reasons, "Channel runtime PullHint not_leader errors were observed")
	}
	if report.ChannelRuntimePullHintInvalidConfigErrCount > 0 {
		report.Reasons = append(report.Reasons, "Channel runtime PullHint invalid_config errors were observed")
	}
	if report.ChannelRuntimePullHintClosedErrCount > 0 {
		report.Reasons = append(report.Reasons, "Channel runtime PullHint closed errors were observed")
	}
	if report.ChannelRuntimePullHintCanceledErrCount > 0 {
		report.Reasons = append(report.Reasons, "Channel runtime PullHint canceled errors were observed")
	}
	if report.ChannelRuntimePullHintTimeoutErrCount > 0 {
		report.Reasons = append(report.Reasons, "Channel runtime PullHint timeout errors were observed")
	}
	if report.ChannelRuntimePullHintRemoteErrCount > 0 {
		report.Reasons = append(report.Reasons, "Channel runtime PullHint remote errors were observed")
	}
	if report.ChannelRuntimePullHintOtherErrCount > 0 {
		report.Reasons = append(report.Reasons, "Channel runtime PullHint other errors were observed")
	}
	if report.ChannelRuntimePullHintReceiveErrCount > 0 {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime PullHint receive errors were observed")
	}
	if report.ChannelRuntimePullHintReceiveMetaResolveErrCount > 0 {
		report.Reasons = append(report.Reasons, "Channel runtime PullHint receive meta_resolve errors were observed")
	}
	if report.ChannelRuntimePullHintReceiveMetaValidateErrCount > 0 {
		report.Reasons = append(report.Reasons, "Channel runtime PullHint receive meta_validate errors were observed")
	}
	if report.ChannelRuntimePullHintReceiveSubmitErrCount > 0 {
		report.Reasons = append(report.Reasons, "Channel runtime PullHint receive submit errors were observed")
	}
	if report.ChannelRuntimePullHintReceiveAwaitErrCount > 0 {
		report.Reasons = append(report.Reasons, "Channel runtime PullHint receive await errors were observed")
	}
	if report.ChannelRuntimePullHintReceiveChannelNotFoundErrCount > 0 {
		report.Reasons = append(report.Reasons, "Channel runtime PullHint receive channel_not_found errors were observed")
	}
	if report.ChannelRuntimeWorkerTaskP99Seconds >= wukongIMLatencyPressureSeconds {
		channelPressure = true
		report.Reasons = append(report.Reasons, "Channel runtime worker task p99 is high")
	}

	storagePressure := false
	if report.StorageCommitQueueDepthMax > 0 {
		storagePressure = true
		report.Reasons = append(report.Reasons, "storage commit queue has pending requests")
	}
	if report.StorageCommitP99Seconds >= wukongIMLatencyPressureSeconds {
		storagePressure = true
		report.Reasons = append(report.Reasons, "storage physical commit p99 is high")
	}
	if report.StorageCommitTotalP99Seconds >= wukongIMLatencyPressureSeconds {
		storagePressure = true
		report.Reasons = append(report.Reasons, "storage grouped commit total p99 is high")
	}
	if report.StorageCommitRequestP99Seconds >= wukongIMLatencyPressureSeconds {
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
		report.Classification = WukongIMBottleneckMixed
	case controllerPressure && (channelPressure || storagePressure):
		report.Classification = WukongIMBottleneckMixed
	case gatewayPressure:
		report.Classification = WukongIMBottleneckGateway
	case controllerPressure:
		report.Classification = WukongIMBottleneckControllerRaft
	case storagePressure:
		report.Classification = WukongIMBottleneckStorageCommit
	case channelPressure:
		report.Classification = WukongIMBottleneckChannelRuntime
	}
	return report
}

func (s PrometheusSnapshot) maxGauge(name string) (float64, bool) {
	found := false
	var maxValue float64
	for _, sample := range s.Samples {
		if !metricFamilyMatches(sample.Name, name) {
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
		if !metricFamilyMatches(sample.Name, name) {
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
	for _, sample := range s.Samples {
		if !metricFamilyMatches(sample.Name, family+"_bucket") {
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
	for _, candidate := range metricFamilyAliases(family) {
		found := false
		var total float64
		for _, sample := range s.Samples {
			if sample.Name != candidate {
				continue
			}
			if !prometheusLabelsMatch(sample.Labels, labels) {
				continue
			}
			total += sample.Value
			found = true
		}
		if found {
			return total, true
		}
	}
	return 0, false
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
	for _, candidate := range metricFamilyAliases(family) {
		out := map[float64]float64{}
		bucketName := candidate + "_bucket"
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
		if len(out) > 0 {
			return out
		}
	}
	return map[float64]float64{}
}

func metricFamilyMatches(sampleName string, requestedName string) bool {
	for _, candidate := range metricFamilyAliases(requestedName) {
		if sampleName == candidate {
			return true
		}
	}
	return false
}

func metricFamilyAliases(family string) []string {
	if strings.HasPrefix(family, channelRuntimeLegacyMetricPrefix) {
		return []string{
			channelRuntimePromotedMetricPrefix + strings.TrimPrefix(family, channelRuntimeLegacyMetricPrefix),
			family,
		}
	}
	return []string{family}
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
