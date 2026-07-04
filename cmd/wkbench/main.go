package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/capacity"
	"github.com/WuKongIM/WuKongIM/internal/bench/config"
	"github.com/WuKongIM/WuKongIM/internal/bench/coordinator"
	"github.com/WuKongIM/WuKongIM/internal/bench/devsim"
	benchmetrics "github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/internal/bench/planner"
	"github.com/WuKongIM/WuKongIM/internal/bench/worker"
	"github.com/spf13/pflag"
)

const (
	exitConfig    = 1
	exitPreflight = 2
	exitHardLimit = 3
	exitWorker    = 4
	exitTarget    = 5
	exitInternal  = 6
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	return runWithStderr(args, os.Stderr)
}

func runWithStderr(args []string, stderr io.Writer) int {
	return executeRoot(args, stderr)
}

func runMetricsClassify(args []string, stderr io.Writer) int {
	fs := pflag.NewFlagSet("metrics classify", pflag.ContinueOnError)
	fs.SetOutput(stderr)
	var cfg metricsClassifyConfig
	fs.StringVar(&cfg.beforePath, "before", "", "Prometheus text snapshot before the measured window")
	fs.StringVar(&cfg.afterPath, "after", "", "Prometheus text snapshot after the measured window")
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	if err := validateMetricsClassifyConfig(cfg); err != nil {
		fmt.Fprintln(stderr, err)
		return exitConfig
	}
	return runMetricsClassifyConfig(cfg, stderr)
}

func validateMetricsClassifyConfig(cfg metricsClassifyConfig) error {
	if strings.TrimSpace(cfg.beforePath) == "" || strings.TrimSpace(cfg.afterPath) == "" {
		return fmt.Errorf("--before and --after are required")
	}
	return nil
}

func runMetricsClassifyConfig(cfg metricsClassifyConfig, stderr io.Writer) int {
	before, err := readPrometheusSnapshot(cfg.beforePath)
	if err != nil {
		fmt.Fprintf(stderr, "read before snapshot failed: %v\n", err)
		return exitConfig
	}
	after, err := readPrometheusSnapshot(cfg.afterPath)
	if err != nil {
		fmt.Fprintf(stderr, "read after snapshot failed: %v\n", err)
		return exitConfig
	}
	printWukongIMAttribution(stderr, benchmetrics.AnalyzeWukongIMPrometheus(before, after))
	return 0
}

func readPrometheusSnapshot(path string) (benchmetrics.PrometheusSnapshot, error) {
	file, err := os.Open(path)
	if err != nil {
		return benchmetrics.PrometheusSnapshot{}, err
	}
	defer file.Close()
	return benchmetrics.ParsePrometheusText(file)
}

func printWukongIMAttribution(w io.Writer, report benchmetrics.WukongIMAttribution) {
	fmt.Fprintln(w, "# wukongim metrics attribution")
	fmt.Fprintf(w, "classification: %s\n", report.Classification)
	fmt.Fprintf(w, "gateway_queue_depth: %.0f\n", report.GatewayQueueDepth)
	fmt.Fprintf(w, "gateway_queue_capacity: %.0f\n", report.GatewayQueueCapacity)
	fmt.Fprintf(w, "gateway_queue_ratio: %.3f\n", report.GatewayQueueRatio)
	fmt.Fprintf(w, "gateway_dispatch_wait_p99_seconds: %.6f\n", report.GatewayDispatchWaitP99Seconds)
	fmt.Fprintf(w, "gateway_batch_records_p50: %.3f\n", report.GatewayBatchRecordsP50)
	fmt.Fprintf(w, "gateway_sendack_system_error_count: %.0f\n", report.GatewaySendackSystemErrorCount)
	fmt.Fprintf(w, "gateway_sendack_single_error_count: %.0f\n", report.GatewaySendackSingleErrorCount)
	fmt.Fprintf(w, "gateway_sendack_single_missing_request_context_count: %.0f\n", report.GatewaySendackSingleMissingRequestContextCount)
	fmt.Fprintf(w, "gateway_sendack_batch_precheck_count: %.0f\n", report.GatewaySendackBatchPrecheckCount)
	fmt.Fprintf(w, "gateway_sendack_batch_missing_request_context_count: %.0f\n", report.GatewaySendackBatchMissingRequestContextCount)
	fmt.Fprintf(w, "gateway_sendack_batch_result_error_count: %.0f\n", report.GatewaySendackBatchResultErrorCount)
	fmt.Fprintf(w, "gateway_sendack_batch_result_timeout_count: %.0f\n", report.GatewaySendackBatchResultTimeoutCount)
	fmt.Fprintf(w, "gateway_sendack_batch_result_canceled_count: %.0f\n", report.GatewaySendackBatchResultCanceledCount)
	fmt.Fprintf(w, "gateway_sendack_batch_result_other_count: %.0f\n", report.GatewaySendackBatchResultOtherCount)
	fmt.Fprintf(w, "gateway_sendack_batch_fallback_error_count: %.0f\n", report.GatewaySendackBatchFallbackErrorCount)
	fmt.Fprintf(w, "message_append_error_count: %.0f\n", report.MessageAppendErrorCount)
	fmt.Fprintf(w, "message_append_backpressured_err_count: %.0f\n", report.MessageAppendBackpressuredErrCount)
	fmt.Fprintf(w, "message_append_route_not_ready_err_count: %.0f\n", report.MessageAppendRouteNotReadyErrCount)
	fmt.Fprintf(w, "message_append_stale_route_err_count: %.0f\n", report.MessageAppendStaleRouteErrCount)
	fmt.Fprintf(w, "message_append_not_leader_err_count: %.0f\n", report.MessageAppendNotLeaderErrCount)
	fmt.Fprintf(w, "message_append_channel_not_found_err_count: %.0f\n", report.MessageAppendChannelNotFoundErrCount)
	fmt.Fprintf(w, "message_append_short_result_err_count: %.0f\n", report.MessageAppendShortResultErrCount)
	fmt.Fprintf(w, "message_append_invalid_config_err_count: %.0f\n", report.MessageAppendInvalidConfigErrCount)
	fmt.Fprintf(w, "message_append_closed_err_count: %.0f\n", report.MessageAppendClosedErrCount)
	fmt.Fprintf(w, "message_append_too_many_channels_err_count: %.0f\n", report.MessageAppendTooManyChannelsErrCount)
	fmt.Fprintf(w, "message_append_not_started_err_count: %.0f\n", report.MessageAppendNotStartedErrCount)
	fmt.Fprintf(w, "message_append_canceled_err_count: %.0f\n", report.MessageAppendCanceledErrCount)
	fmt.Fprintf(w, "message_append_timeout_err_count: %.0f\n", report.MessageAppendTimeoutErrCount)
	fmt.Fprintf(w, "message_append_append_failed_err_count: %.0f\n", report.MessageAppendAppendFailedErrCount)
	fmt.Fprintf(w, "message_append_remote_err_count: %.0f\n", report.MessageAppendRemoteErrCount)
	fmt.Fprintf(w, "message_append_other_err_count: %.0f\n", report.MessageAppendOtherErrCount)
	fmt.Fprintf(w, "controller_raft_step_queue_depth: %.0f\n", report.ControllerRaftStepQueueDepth)
	fmt.Fprintf(w, "controller_raft_step_queue_capacity: %.0f\n", report.ControllerRaftStepQueueCapacity)
	fmt.Fprintf(w, "controller_raft_step_queue_ratio: %.3f\n", report.ControllerRaftStepQueueRatio)
	fmt.Fprintf(w, "controller_raft_step_enqueue_ok_p99_seconds: %.6f\n", report.ControllerRaftStepEnqueueOKP99Seconds)
	fmt.Fprintf(w, "controller_raft_step_enqueue_err_p99_seconds: %.6f\n", report.ControllerRaftStepEnqueueErrP99Seconds)
	fmt.Fprintf(w, "controller_raft_step_enqueue_err_count: %.0f\n", report.ControllerRaftStepEnqueueErrCount)
	// Channel runtime report keys use the promoted channel prefix; Prometheus input parsing still accepts legacy channelv2 metric names.
	fmt.Fprintf(w, "channel_reactor_mailbox_depth_max: %.0f\n", report.ChannelRuntimeReactorMailboxDepthMax)
	fmt.Fprintf(w, "channel_worker_queue_depth_max: %.0f\n", report.ChannelRuntimeWorkerQueueDepthMax)
	printLabeledFloatMap(w, "channel_worker_queue_depth", "pool", report.ChannelRuntimeWorkerQueueDepthByPool, "%.0f")
	printLabeledFloatMap(w, "channel_worker_inflight", "pool", report.ChannelRuntimeWorkerInflightByPool, "%.0f")
	printLabeledFloatMap(w, "channel_worker_inflight_peak", "pool", report.ChannelRuntimeWorkerInflightPeakByPool, "%.0f")
	fmt.Fprintf(w, "channel_append_p99_seconds: %.6f\n", report.ChannelRuntimeAppendP99Seconds)
	fmt.Fprintf(w, "channel_meta_resolve_p99_seconds: %.6f\n", report.ChannelRuntimeMetaResolveP99Seconds)
	fmt.Fprintf(w, "channel_meta_slot_read_p99_seconds: %.6f\n", report.ChannelRuntimeMetaSlotReadP99Seconds)
	fmt.Fprintf(w, "channel_meta_create_build_p99_seconds: %.6f\n", report.ChannelRuntimeMetaCreateBuildP99Seconds)
	fmt.Fprintf(w, "channel_meta_create_propose_p99_seconds: %.6f\n", report.ChannelRuntimeMetaCreateProposeP99Seconds)
	fmt.Fprintf(w, "channel_meta_create_propose_local_p99_seconds: %.6f\n", report.ChannelRuntimeMetaCreateProposeLocalP99Seconds)
	fmt.Fprintf(w, "channel_meta_create_propose_forward_p99_seconds: %.6f\n", report.ChannelRuntimeMetaCreateProposeForwardP99Seconds)
	fmt.Fprintf(w, "channel_meta_create_slot_propose_submit_p99_seconds: %.6f\n", report.ChannelRuntimeMetaCreateSlotProposeSubmitP99Seconds)
	fmt.Fprintf(w, "channel_meta_create_slot_propose_wait_p99_seconds: %.6f\n", report.ChannelRuntimeMetaCreateSlotProposeWaitP99Seconds)
	fmt.Fprintf(w, "channel_meta_create_slot_control_wait_p99_seconds: %.6f\n", report.ChannelRuntimeMetaCreateSlotControlWaitP99Seconds)
	fmt.Fprintf(w, "channel_meta_create_slot_raft_commit_wait_p99_seconds: %.6f\n", report.ChannelRuntimeMetaCreateSlotRaftCommitWaitP99Seconds)
	fmt.Fprintf(w, "channel_meta_create_slot_fsm_apply_p99_seconds: %.6f\n", report.ChannelRuntimeMetaCreateSlotFSMApplyP99Seconds)
	fmt.Fprintf(w, "channel_meta_create_slot_fsm_commit_p99_seconds: %.6f\n", report.ChannelRuntimeMetaCreateSlotFSMCommitP99Seconds)
	fmt.Fprintf(w, "channel_meta_create_slot_mark_applied_p99_seconds: %.6f\n", report.ChannelRuntimeMetaCreateSlotMarkAppliedP99Seconds)
	fmt.Fprintf(w, "channel_meta_create_write_p99_seconds: %.6f\n", report.ChannelRuntimeMetaCreateWriteP99Seconds)
	fmt.Fprintf(w, "channel_meta_final_read_p99_seconds: %.6f\n", report.ChannelRuntimeMetaFinalReadP99Seconds)
	fmt.Fprintf(w, "channel_meta_apply_p99_seconds: %.6f\n", report.ChannelRuntimeMetaApplyP99Seconds)
	fmt.Fprintf(w, "channel_runtime_append_p99_seconds: %.6f\n", report.ChannelRuntimeFacadeAppendP99Seconds)
	fmt.Fprintf(w, "channel_runtime_append_reserve_wait_p99_seconds: %.6f\n", report.ChannelRuntimeFacadeAppendReserveWaitP99Seconds)
	fmt.Fprintf(w, "channel_runtime_append_submit_p99_seconds: %.6f\n", report.ChannelRuntimeFacadeAppendSubmitP99Seconds)
	fmt.Fprintf(w, "channel_runtime_append_wait_p99_seconds: %.6f\n", report.ChannelRuntimeFacadeAppendWaitP99Seconds)
	fmt.Fprintf(w, "channel_append_batch_wait_p99_seconds: %.6f\n", report.ChannelRuntimeAppendBatchWaitP99Seconds)
	fmt.Fprintf(w, "channel_append_batch_records_p50: %.3f\n", report.ChannelRuntimeAppendBatchRecordsP50)
	fmt.Fprintf(w, "channel_append_store_wait_p99_seconds: %.6f\n", report.ChannelRuntimeAppendStoreWaitP99Seconds)
	fmt.Fprintf(w, "channel_append_post_store_commit_wait_p99_seconds: %.6f\n", report.ChannelRuntimeAppendPostStoreCommitWaitP99Seconds)
	fmt.Fprintf(w, "channel_append_quorum_follower_pull_wait_p99_seconds: %.6f\n", report.ChannelRuntimeAppendQuorumFollowerPullWaitP99Seconds)
	fmt.Fprintf(w, "channel_append_quorum_ack_offset_wait_p99_seconds: %.6f\n", report.ChannelRuntimeAppendQuorumAckOffsetWaitP99Seconds)
	fmt.Fprintf(w, "channel_append_quorum_hw_advance_wait_p99_seconds: %.6f\n", report.ChannelRuntimeAppendQuorumHWAdvanceWaitP99Seconds)
	fmt.Fprintf(w, "channel_append_quorum_final_complete_p99_seconds: %.6f\n", report.ChannelRuntimeAppendQuorumFinalCompleteP99Seconds)
	fmt.Fprintf(w, "channel_replication_follower_pull_hint_to_submit_p99_seconds: %.6f\n", report.ChannelRuntimeReplicationPullHintToSubmitP99Seconds)
	fmt.Fprintf(w, "channel_replication_follower_pull_rpc_p99_seconds: %.6f\n", report.ChannelRuntimeReplicationPullRPCP99Seconds)
	fmt.Fprintf(w, "channel_need_meta_pull_rpc_p99_seconds: %.6f\n", report.ChannelRuntimeNeedMetaPullRPCP99Seconds)
	fmt.Fprintf(w, "channel_replication_follower_store_apply_p99_seconds: %.6f\n", report.ChannelRuntimeReplicationStoreApplyP99Seconds)
	fmt.Fprintf(w, "channel_replication_follower_apply_to_ack_return_p99_seconds: %.6f\n", report.ChannelRuntimeReplicationApplyToAckReturnP99Seconds)
	fmt.Fprintf(w, "channel_pending_meta_current_max: %.0f\n", report.ChannelRuntimePendingMetaCurrentMax)
	fmt.Fprintf(w, "channel_pending_meta_created_count: %.0f\n", report.ChannelRuntimePendingMetaCreatedCount)
	fmt.Fprintf(w, "channel_pending_meta_converted_count: %.0f\n", report.ChannelRuntimePendingMetaConvertedCount)
	fmt.Fprintf(w, "channel_pending_meta_released_count: %.0f\n", report.ChannelRuntimePendingMetaReleasedCount)
	fmt.Fprintf(w, "channel_pending_meta_timeout_release_count: %.0f\n", report.ChannelRuntimePendingMetaTimeoutReleaseCount)
	fmt.Fprintf(w, "channel_pending_meta_not_ready_release_count: %.0f\n", report.ChannelRuntimePendingMetaNotReadyReleaseCount)
	fmt.Fprintf(w, "channel_pull_hint_submitted_count: %.0f\n", report.ChannelRuntimePullHintSubmittedCount)
	fmt.Fprintf(w, "channel_pull_hint_ok_count: %.0f\n", report.ChannelRuntimePullHintOKCount)
	fmt.Fprintf(w, "channel_pull_hint_err_count: %.0f\n", report.ChannelRuntimePullHintErrCount)
	fmt.Fprintf(w, "channel_pull_hint_stale_meta_err_count: %.0f\n", report.ChannelRuntimePullHintStaleMetaErrCount)
	fmt.Fprintf(w, "channel_pull_hint_channel_not_found_err_count: %.0f\n", report.ChannelRuntimePullHintChannelNotFoundErrCount)
	fmt.Fprintf(w, "channel_pull_hint_not_ready_err_count: %.0f\n", report.ChannelRuntimePullHintNotReadyErrCount)
	fmt.Fprintf(w, "channel_pull_hint_not_leader_err_count: %.0f\n", report.ChannelRuntimePullHintNotLeaderErrCount)
	fmt.Fprintf(w, "channel_pull_hint_invalid_config_err_count: %.0f\n", report.ChannelRuntimePullHintInvalidConfigErrCount)
	fmt.Fprintf(w, "channel_pull_hint_closed_err_count: %.0f\n", report.ChannelRuntimePullHintClosedErrCount)
	fmt.Fprintf(w, "channel_pull_hint_canceled_err_count: %.0f\n", report.ChannelRuntimePullHintCanceledErrCount)
	fmt.Fprintf(w, "channel_pull_hint_timeout_err_count: %.0f\n", report.ChannelRuntimePullHintTimeoutErrCount)
	fmt.Fprintf(w, "channel_pull_hint_remote_err_count: %.0f\n", report.ChannelRuntimePullHintRemoteErrCount)
	fmt.Fprintf(w, "channel_pull_hint_other_err_count: %.0f\n", report.ChannelRuntimePullHintOtherErrCount)
	fmt.Fprintf(w, "channel_pull_hint_receive_ok_count: %.0f\n", report.ChannelRuntimePullHintReceiveOKCount)
	fmt.Fprintf(w, "channel_pull_hint_receive_err_count: %.0f\n", report.ChannelRuntimePullHintReceiveErrCount)
	fmt.Fprintf(w, "channel_pull_hint_receive_state_check_err_count: %.0f\n", report.ChannelRuntimePullHintReceiveStateCheckErrCount)
	fmt.Fprintf(w, "channel_pull_hint_receive_meta_resolve_err_count: %.0f\n", report.ChannelRuntimePullHintReceiveMetaResolveErrCount)
	fmt.Fprintf(w, "channel_pull_hint_receive_meta_hint_ok_count: %.0f\n", report.ChannelRuntimePullHintReceiveMetaHintOKCount)
	fmt.Fprintf(w, "channel_pull_hint_receive_meta_validate_err_count: %.0f\n", report.ChannelRuntimePullHintReceiveMetaValidateErrCount)
	fmt.Fprintf(w, "channel_pull_hint_receive_meta_apply_err_count: %.0f\n", report.ChannelRuntimePullHintReceiveMetaApplyErrCount)
	fmt.Fprintf(w, "channel_pull_hint_receive_submit_err_count: %.0f\n", report.ChannelRuntimePullHintReceiveSubmitErrCount)
	fmt.Fprintf(w, "channel_pull_hint_receive_await_err_count: %.0f\n", report.ChannelRuntimePullHintReceiveAwaitErrCount)
	fmt.Fprintf(w, "channel_pull_hint_receive_stale_meta_err_count: %.0f\n", report.ChannelRuntimePullHintReceiveStaleMetaErrCount)
	fmt.Fprintf(w, "channel_pull_hint_receive_channel_not_found_err_count: %.0f\n", report.ChannelRuntimePullHintReceiveChannelNotFoundErrCount)
	fmt.Fprintf(w, "channel_pull_hint_receive_not_ready_err_count: %.0f\n", report.ChannelRuntimePullHintReceiveNotReadyErrCount)
	fmt.Fprintf(w, "channel_pull_hint_receive_canceled_err_count: %.0f\n", report.ChannelRuntimePullHintReceiveCanceledErrCount)
	fmt.Fprintf(w, "channel_pull_hint_receive_timeout_err_count: %.0f\n", report.ChannelRuntimePullHintReceiveTimeoutErrCount)
	fmt.Fprintf(w, "channel_pull_hint_receive_other_err_count: %.0f\n", report.ChannelRuntimePullHintReceiveOtherErrCount)
	fmt.Fprintf(w, "channel_need_meta_pull_submitted_count: %.0f\n", report.ChannelRuntimeNeedMetaPullSubmittedCount)
	fmt.Fprintf(w, "channel_need_meta_pull_ok_count: %.0f\n", report.ChannelRuntimeNeedMetaPullOKCount)
	fmt.Fprintf(w, "channel_need_meta_pull_retry_count: %.0f\n", report.ChannelRuntimeNeedMetaPullRetryCount)
	fmt.Fprintf(w, "channel_need_meta_pull_err_count: %.0f\n", report.ChannelRuntimeNeedMetaPullErrCount)
	fmt.Fprintf(w, "channel_need_meta_pull_stale_meta_err_count: %.0f\n", report.ChannelRuntimeNeedMetaPullStaleMetaErrCount)
	fmt.Fprintf(w, "channel_need_meta_pull_channel_not_found_err_count: %.0f\n", report.ChannelRuntimeNeedMetaPullChannelNotFoundErrCount)
	fmt.Fprintf(w, "channel_need_meta_pull_not_ready_err_count: %.0f\n", report.ChannelRuntimeNeedMetaPullNotReadyErrCount)
	fmt.Fprintf(w, "channel_need_meta_pull_not_leader_err_count: %.0f\n", report.ChannelRuntimeNeedMetaPullNotLeaderErrCount)
	fmt.Fprintf(w, "channel_need_meta_pull_not_replica_err_count: %.0f\n", report.ChannelRuntimeNeedMetaPullNotReplicaErrCount)
	fmt.Fprintf(w, "channel_need_meta_pull_backpressured_err_count: %.0f\n", report.ChannelRuntimeNeedMetaPullBackpressuredErrCount)
	fmt.Fprintf(w, "channel_need_meta_pull_invalid_config_err_count: %.0f\n", report.ChannelRuntimeNeedMetaPullInvalidConfigErrCount)
	fmt.Fprintf(w, "channel_need_meta_pull_closed_err_count: %.0f\n", report.ChannelRuntimeNeedMetaPullClosedErrCount)
	fmt.Fprintf(w, "channel_need_meta_pull_canceled_err_count: %.0f\n", report.ChannelRuntimeNeedMetaPullCanceledErrCount)
	fmt.Fprintf(w, "channel_need_meta_pull_timeout_err_count: %.0f\n", report.ChannelRuntimeNeedMetaPullTimeoutErrCount)
	fmt.Fprintf(w, "channel_need_meta_pull_remote_err_count: %.0f\n", report.ChannelRuntimeNeedMetaPullRemoteErrCount)
	fmt.Fprintf(w, "channel_need_meta_pull_other_err_count: %.0f\n", report.ChannelRuntimeNeedMetaPullOtherErrCount)
	fmt.Fprintf(w, "channel_worker_task_p99_seconds: %.6f\n", report.ChannelRuntimeWorkerTaskP99Seconds)
	printLabeledFloatMap(w, "channel_worker_task_p99_seconds", "kind", report.ChannelRuntimeWorkerTaskP99SecondsByKind, "%.6f")
	fmt.Fprintf(w, "storage_commit_queue_depth_max: %.0f\n", report.StorageCommitQueueDepthMax)
	fmt.Fprintf(w, "storage_commit_batch_requests_p50: %.3f\n", report.StorageCommitBatchRequestsP50)
	fmt.Fprintf(w, "storage_commit_batch_records_p50: %.3f\n", report.StorageCommitBatchRecordsP50)
	fmt.Fprintf(w, "storage_commit_p99_seconds: %.6f\n", report.StorageCommitP99Seconds)
	fmt.Fprintf(w, "storage_commit_total_p99_seconds: %.6f\n", report.StorageCommitTotalP99Seconds)
	fmt.Fprintf(w, "storage_commit_request_p99_seconds: %.6f\n", report.StorageCommitRequestP99Seconds)
	printLabeledFloatMap(w, "storage_commit_request_p99_seconds", "lane", report.StorageCommitRequestP99SecondsByLane, "%.6f")
	fmt.Fprintf(w, "storage_commit_request_ok_p99_seconds: %.6f\n", report.StorageCommitRequestOKP99Seconds)
	fmt.Fprintf(w, "storage_commit_request_timeout_count: %.0f\n", report.StorageCommitRequestTimeoutCount)
	fmt.Fprintf(w, "storage_commit_request_canceled_count: %.0f\n", report.StorageCommitRequestCanceledCount)
	fmt.Fprintf(w, "storage_commit_request_closed_count: %.0f\n", report.StorageCommitRequestClosedCount)
	fmt.Fprintf(w, "storage_commit_request_err_count: %.0f\n", report.StorageCommitRequestErrCount)
	fmt.Fprintf(w, "storage_commit_request_over_1s_count: %.0f\n", report.StorageCommitRequestOver1sCount)
	printLabeledFloatMap(w, "storage_commit_request_over_1s_count", "lane", report.StorageCommitRequestOver1sCountByLane, "%.0f")
	fmt.Fprintf(w, "storage_commit_request_over_5s_count: %.0f\n", report.StorageCommitRequestOver5sCount)
	printLabeledFloatMap(w, "storage_commit_request_over_5s_count", "lane", report.StorageCommitRequestOver5sCountByLane, "%.0f")
	fmt.Fprintf(w, "storage_commit_request_over_10s_count: %.0f\n", report.StorageCommitRequestOver10sCount)
	printLabeledFloatMap(w, "storage_commit_request_over_10s_count", "lane", report.StorageCommitRequestOver10sCountByLane, "%.0f")
	if len(report.Reasons) == 0 {
		return
	}
	fmt.Fprintln(w, "reasons:")
	for _, reason := range report.Reasons {
		fmt.Fprintf(w, "- %s\n", reason)
	}
}

func printLabeledFloatMap(w io.Writer, name string, label string, values map[string]float64, format string) {
	if len(values) == 0 {
		return
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(w, "%s{%s=%q}: "+format+"\n", name, label, key, values[key])
	}
}

type activateChannelsRunner interface {
	Run(context.Context) (capacity.ActivateChannelsResult, error)
}

var discoverActivateChannelsTarget = func(ctx context.Context, cfg capacity.ActivateChannelsConfig) (capacity.DiscoveredTarget, error) {
	return capacity.DiscoverTarget(ctx, activateChannelsDiscoveryConfig(cfg))
}

var newActivateChannelsRunner = func(cfg capacity.ActivateChannelsConfig, discovered capacity.DiscoveredTarget) activateChannelsRunner {
	return capacity.NewActivateChannelsRunner(cfg, discovered)
}

func runCapacitySendConfig(cfg capacity.Config, stderr io.Writer) int {
	discovered, err := capacity.DiscoverTarget(context.Background(), cfg)
	if err != nil {
		fmt.Fprintf(stderr, "capacity preflight failed: %v\n", err)
		return exitPreflight
	}
	result, err := capacity.NewRunner(cfg, discovered).Run(context.Background())
	if writeErr := capacity.WriteResult(result.ReportDir, result); writeErr != nil {
		fmt.Fprintf(stderr, "capacity report write failed: %v\n", writeErr)
		return exitInternal
	}
	fmt.Fprint(stderr, capacity.ConsoleSummary(result))
	if err != nil {
		fmt.Fprintf(stderr, "capacity run failed: %v\n", err)
		return exitWorker
	}
	return result.ExitCode()
}

func runCapacityHotChannelConfig(cfg capacity.HotChannelConfig, stderr io.Writer) int {
	discovered, err := capacity.DiscoverTarget(context.Background(), cfg.Config)
	if err != nil {
		fmt.Fprintf(stderr, "capacity preflight failed: %v\n", err)
		return exitPreflight
	}
	result, err := capacity.NewHotChannelRunner(cfg, discovered).Run(context.Background())
	if writeErr := capacity.WriteResult(result.ReportDir, result); writeErr != nil {
		fmt.Fprintf(stderr, "capacity report write failed: %v\n", writeErr)
		return exitInternal
	}
	fmt.Fprint(stderr, capacity.ConsoleSummary(result))
	if err != nil {
		fmt.Fprintf(stderr, "capacity run failed: %v\n", err)
		return exitWorker
	}
	return result.ExitCode()
}

func runCapacityActivateChannelsConfig(cfg capacity.ActivateChannelsConfig, stderr io.Writer) int {
	discovered, err := discoverActivateChannelsTarget(context.Background(), cfg)
	if err != nil {
		fmt.Fprintf(stderr, "activate-channels preflight failed: %v\n", err)
		return exitPreflight
	}
	result, err := newActivateChannelsRunner(cfg, discovered).Run(context.Background())
	if writeErr := capacity.WriteActivateChannelsResult(result.ReportDir, result); writeErr != nil {
		fmt.Fprintf(stderr, "activate-channels report write failed: %v\n", writeErr)
		return exitInternal
	}
	fmt.Fprint(stderr, capacity.ActivateChannelsConsoleSummary(result))
	if err != nil {
		fmt.Fprintf(stderr, "activate-channels run failed: %v\n", err)
		return result.ExitCode()
	}
	return result.ExitCode()
}

func parseCapacitySendConfig(args []string, stderr io.Writer) (capacity.Config, int) {
	cfg := capacity.DefaultConfig()
	fs := pflag.NewFlagSet("capacity send", pflag.ContinueOnError)
	fs.SetOutput(stderr)
	var apiCSV string
	var gatewayCSV string
	bindCapacitySendFlags(fs, &cfg, &apiCSV, &gatewayCSV)
	if err := fs.Parse(args); err != nil {
		return capacity.Config{}, exitConfig
	}
	if err := finalizeCapacitySendConfig(&cfg, apiCSV, gatewayCSV); err != nil {
		fmt.Fprintln(stderr, err)
		return capacity.Config{}, exitConfig
	}
	return cfg, 0
}

func parseCapacityHotChannelConfig(args []string, stderr io.Writer) (capacity.HotChannelConfig, int) {
	cfg := capacity.DefaultHotChannelConfig()
	fs := pflag.NewFlagSet("capacity hot-channel", pflag.ContinueOnError)
	fs.SetOutput(stderr)
	var apiCSV string
	var gatewayCSV string
	bindCapacityHotChannelFlags(fs, &cfg, &apiCSV, &gatewayCSV)
	if err := fs.Parse(args); err != nil {
		return capacity.HotChannelConfig{}, exitConfig
	}
	if err := finalizeCapacityHotChannelConfig(&cfg, apiCSV, gatewayCSV); err != nil {
		fmt.Fprintln(stderr, err)
		return capacity.HotChannelConfig{}, exitConfig
	}
	return cfg, 0
}

func parseCapacityActivateChannelsConfig(args []string, stderr io.Writer) (capacity.ActivateChannelsConfig, int) {
	cfg := capacity.DefaultActivateChannelsConfig()
	fs := pflag.NewFlagSet("capacity activate-channels", pflag.ContinueOnError)
	fs.SetOutput(stderr)
	var apiCSV string
	var gatewayCSV string
	bindCapacityActivateChannelsFlags(fs, &cfg, &apiCSV, &gatewayCSV)
	if err := fs.Parse(args); err != nil {
		return capacity.ActivateChannelsConfig{}, exitConfig
	}
	if err := finalizeCapacityActivateChannelsConfig(&cfg, apiCSV, gatewayCSV); err != nil {
		fmt.Fprintln(stderr, err)
		return capacity.ActivateChannelsConfig{}, exitConfig
	}
	return cfg, 0
}

func finalizeCapacitySendConfig(cfg *capacity.Config, apiCSV string, gatewayCSV string) error {
	cfg.APIAddrs = splitCSV(apiCSV)
	cfg.GatewayTCPAddrs = splitCSV(gatewayCSV)
	if len(cfg.APIAddrs) == 0 {
		return fmt.Errorf("--api is required")
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}
	return nil
}

func finalizeCapacityHotChannelConfig(cfg *capacity.HotChannelConfig, apiCSV string, gatewayCSV string) error {
	cfg.APIAddrs = splitCSV(apiCSV)
	cfg.GatewayTCPAddrs = splitCSV(gatewayCSV)
	if len(cfg.APIAddrs) == 0 {
		return fmt.Errorf("--api is required")
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}
	return nil
}

func finalizeCapacityActivateChannelsConfig(cfg *capacity.ActivateChannelsConfig, apiCSV string, gatewayCSV string) error {
	cfg.APIAddrs = splitCSV(apiCSV)
	cfg.GatewayTCPAddrs = splitCSV(gatewayCSV)
	if len(cfg.APIAddrs) == 0 {
		return fmt.Errorf("--api is required")
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}
	return nil
}

func activateChannelsDiscoveryConfig(cfg capacity.ActivateChannelsConfig) capacity.Config {
	base := capacity.DefaultConfig()
	base.APIAddrs = append([]string(nil), cfg.APIAddrs...)
	base.GatewayTCPAddrs = append([]string(nil), cfg.GatewayTCPAddrs...)
	base.BenchToken = cfg.BenchToken
	base.GroupMembers = cfg.GroupMembers
	base.ReportDir = cfg.ReportDir
	return base
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

type devSimCLIConfig struct {
	configPath   string
	statusListen string
}

func runDevSimConfig(cliCfg devSimCLIConfig, stderr io.Writer) int {
	cfg, err := devsim.LoadConfig(cliCfg.configPath, envMap(os.Environ()))
	if err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return exitConfig
	}
	if strings.TrimSpace(cliCfg.statusListen) != "" {
		cfg.Status.Listen = strings.TrimSpace(cliCfg.statusListen)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := devsim.Run(ctx, cfg); err != nil {
		fmt.Fprintf(stderr, "dev-sim failed: %v\n", err)
		return exitTarget
	}
	return 0
}

func parseDevSimConfig(args []string, stderr io.Writer) (devSimCLIConfig, int) {
	fs := pflag.NewFlagSet("dev-sim", pflag.ContinueOnError)
	fs.SetOutput(stderr)
	var cfg devSimCLIConfig
	bindDevSimFlags(fs, &cfg)
	if err := fs.Parse(args); err != nil {
		return devSimCLIConfig{}, exitConfig
	}
	if err := finalizeDevSimConfig(cfg); err != nil {
		fmt.Fprintln(stderr, err)
		return devSimCLIConfig{}, exitConfig
	}
	return cfg, 0
}

func finalizeDevSimConfig(cfg devSimCLIConfig) error {
	if strings.TrimSpace(cfg.configPath) == "" {
		return fmt.Errorf("--config is required")
	}
	return nil
}

func envMap(items []string) map[string]string {
	env := make(map[string]string, len(items))
	for _, item := range items {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		env[key] = value
	}
	return env
}

func runBenchConfigCommand(runCfg runBenchConfig, stderr io.Writer) int {
	targetCfg, scenario, workers, phasePollTimeout, code := loadValidateRunInputsFromConfig(runCfg, stderr)
	if code != 0 {
		return code
	}
	coord := coordinator.New(coordinator.CoordinatorConfig{Workers: workers.Workers, Target: targetCfg, PollTimeout: phasePollTimeout})
	result, err := coord.Run(context.Background(), scenario)
	if err != nil {
		switch result.Status {
		case coordinator.StatusConfigFailed:
			fmt.Fprintf(stderr, "config validation failed: %v\n", err)
			return exitConfig
		case coordinator.StatusPreflightFailed:
			fmt.Fprintf(stderr, "preflight failed: %v\n", err)
			return exitPreflight
		case coordinator.StatusHardLimitFailed:
			fmt.Fprintf(stderr, "hard limit failed: %v\n", err)
			return exitHardLimit
		case coordinator.StatusWorkerFailed:
			fmt.Fprintf(stderr, "worker run failed: %v\n", err)
			return exitWorker
		case coordinator.StatusTargetUnavailable:
			fmt.Fprintf(stderr, "target unavailable: %v\n", err)
			return exitTarget
		default:
			fmt.Fprintf(stderr, "run failed: %v\n", err)
			return exitInternal
		}
	}
	fmt.Fprintln(stderr, "wkbench workload orchestration completed")
	return result.Status.ExitCode()
}

type benchConfigPaths struct {
	target   string
	scenario string
	workers  string
}

type runBenchConfig struct {
	paths            benchConfigPaths
	phasePollTimeout time.Duration
}

func runValidateConfig(paths benchConfigPaths, stderr io.Writer) int {
	targetCfg, scenario, workers, code := loadValidateInputsFromConfig(paths, stderr)
	if code != 0 {
		return code
	}
	if _, err := planner.Build(scenario, workers.Workers); err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return exitConfig
	}
	_ = targetCfg
	return 0
}

func runDoctorConfig(paths benchConfigPaths, stderr io.Writer) int {
	targetCfg, workers, scenario, hasScenario, code := loadDoctorInputsFromConfig(paths, stderr)
	if code != 0 {
		return code
	}
	if hasScenario {
		if _, err := planner.Build(scenario, workers.Workers); err != nil {
			fmt.Fprintf(stderr, "config validation failed: %v\n", err)
			return exitConfig
		}
	}
	if err := coordinator.NewPreflight(coordinator.PreflightConfig{}).Check(context.Background(), targetCfg, workers); err != nil {
		fmt.Fprintf(stderr, "preflight failed: %v\n", err)
		return exitPreflight
	}
	return 0
}

func loadValidateRunInputs(args []string, stderr io.Writer) (config.Target, config.Scenario, config.WorkerSet, time.Duration, int) {
	runCfg, code := parseRunBenchConfig(args, stderr)
	if code != 0 {
		return config.Target{}, config.Scenario{}, config.WorkerSet{}, 0, code
	}
	return loadValidateRunInputsFromConfig(runCfg, stderr)
}

func loadValidateRunInputsFromConfig(runCfg runBenchConfig, stderr io.Writer) (config.Target, config.Scenario, config.WorkerSet, time.Duration, int) {
	targetCfg, workers, code := loadTargetAndWorkers(runCfg.paths, stderr)
	if code != 0 {
		return config.Target{}, config.Scenario{}, config.WorkerSet{}, 0, code
	}
	scenario, err := config.LoadScenario(runCfg.paths.scenario)
	if err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return config.Target{}, config.Scenario{}, config.WorkerSet{}, 0, exitConfig
	}
	if err := config.ValidateTargetScenario(targetCfg, scenario); err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return config.Target{}, config.Scenario{}, config.WorkerSet{}, 0, exitConfig
	}
	return targetCfg, scenario, workers, runCfg.phasePollTimeout, 0
}

func loadValidateInputs(name string, args []string, stderr io.Writer) (config.Target, config.Scenario, config.WorkerSet, int) {
	paths, code := parseBenchConfigPaths(name, args, stderr, true)
	if code != 0 {
		return config.Target{}, config.Scenario{}, config.WorkerSet{}, code
	}
	return loadValidateInputsFromConfig(paths, stderr)
}

func loadValidateInputsFromConfig(paths benchConfigPaths, stderr io.Writer) (config.Target, config.Scenario, config.WorkerSet, int) {
	targetCfg, workers, code := loadTargetAndWorkers(paths, stderr)
	if code != 0 {
		return config.Target{}, config.Scenario{}, config.WorkerSet{}, code
	}
	scenario, err := config.LoadScenario(paths.scenario)
	if err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return config.Target{}, config.Scenario{}, config.WorkerSet{}, exitConfig
	}
	if err := config.ValidateTargetScenario(targetCfg, scenario); err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return config.Target{}, config.Scenario{}, config.WorkerSet{}, exitConfig
	}
	return targetCfg, scenario, workers, 0
}

func loadDoctorInputs(name string, args []string, stderr io.Writer) (config.Target, config.WorkerSet, config.Scenario, bool, int) {
	paths, code := parseBenchConfigPaths(name, args, stderr, false)
	if code != 0 {
		return config.Target{}, config.WorkerSet{}, config.Scenario{}, false, code
	}
	return loadDoctorInputsFromConfig(paths, stderr)
}

func loadDoctorInputsFromConfig(paths benchConfigPaths, stderr io.Writer) (config.Target, config.WorkerSet, config.Scenario, bool, int) {
	targetCfg, workers, code := loadTargetAndWorkers(paths, stderr)
	if code != 0 {
		return config.Target{}, config.WorkerSet{}, config.Scenario{}, false, code
	}
	if paths.scenario == "" {
		return targetCfg, workers, config.Scenario{}, false, 0
	}
	scenario, err := config.LoadScenario(paths.scenario)
	if err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return config.Target{}, config.WorkerSet{}, config.Scenario{}, false, exitConfig
	}
	if err := config.ValidateTargetScenario(targetCfg, scenario); err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return config.Target{}, config.WorkerSet{}, config.Scenario{}, false, exitConfig
	}
	return targetCfg, workers, scenario, true, 0
}

func loadTargetAndWorkers(paths benchConfigPaths, stderr io.Writer) (config.Target, config.WorkerSet, int) {
	targetCfg, err := config.LoadTarget(paths.target)
	if err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return config.Target{}, config.WorkerSet{}, exitConfig
	}
	workers, err := config.LoadWorkerSet(paths.workers)
	if err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return config.Target{}, config.WorkerSet{}, exitConfig
	}
	if err := config.ValidateStaticConfig(targetCfg, workers); err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return config.Target{}, config.WorkerSet{}, exitConfig
	}
	return targetCfg, workers, 0
}

func parseRunBenchConfig(args []string, stderr io.Writer) (runBenchConfig, int) {
	fs := pflag.NewFlagSet("run", pflag.ContinueOnError)
	fs.SetOutput(stderr)
	var cfg runBenchConfig
	bindBenchConfigPathFlags(fs, &cfg.paths)
	fs.DurationVar(&cfg.phasePollTimeout, "phase-poll-timeout", 0, "base worker phase poll timeout; connect/warmup/run/cooldown add their configured schedule duration; 0 uses the wkbench default")
	if err := fs.Parse(args); err != nil {
		return runBenchConfig{}, exitConfig
	}
	if err := validateRunBenchConfig(cfg); err != nil {
		fmt.Fprintln(stderr, err)
		return runBenchConfig{}, exitConfig
	}
	return cfg, 0
}

func parseBenchConfigPaths(name string, args []string, stderr io.Writer, scenarioRequired bool) (benchConfigPaths, int) {
	fs := pflag.NewFlagSet(name, pflag.ContinueOnError)
	fs.SetOutput(stderr)
	var paths benchConfigPaths
	bindBenchConfigPathFlags(fs, &paths)
	if err := fs.Parse(args); err != nil {
		return benchConfigPaths{}, exitConfig
	}
	if err := validateBenchConfigPaths(paths, scenarioRequired); err != nil {
		fmt.Fprintln(stderr, err)
		return benchConfigPaths{}, exitConfig
	}
	return paths, 0
}

func validateRunBenchConfig(cfg runBenchConfig) error {
	return validateBenchConfigPaths(cfg.paths, true)
}

func validateBenchConfigPaths(paths benchConfigPaths, scenarioRequired bool) error {
	if paths.target == "" {
		return fmt.Errorf("--target is required")
	}
	if scenarioRequired && paths.scenario == "" {
		return fmt.Errorf("--scenario is required")
	}
	if paths.workers == "" {
		return fmt.Errorf("--workers is required")
	}
	return nil
}

type workerCLIConfig struct {
	listen string
	server worker.Config
}

func runWorkerConfig(cfg workerCLIConfig, stderr io.Writer) int {
	if err := http.ListenAndServe(cfg.listen, worker.NewServer(cfg.server)); err != nil {
		fmt.Fprintf(stderr, "worker server failed: %v\n", err)
		return exitConfig
	}
	return 0
}

func parseWorkerConfig(args []string, stderr io.Writer) (workerCLIConfig, int) {
	fs := pflag.NewFlagSet("worker", pflag.ContinueOnError)
	fs.SetOutput(stderr)
	cfg := defaultWorkerCLIConfig()
	bindWorkerFlags(fs, &cfg)
	if err := fs.Parse(args); err != nil {
		return workerCLIConfig{}, exitConfig
	}
	if err := finalizeWorkerConfig(&cfg); err != nil {
		fmt.Fprintln(stderr, err)
		return workerCLIConfig{}, exitConfig
	}
	return cfg, 0
}

func finalizeWorkerConfig(cfg *workerCLIConfig) error {
	if cfg.server.InsecureControl {
		cfg.server.ControlToken = ""
		return nil
	}
	if cfg.server.ControlToken == "" {
		return fmt.Errorf("--control-token is required unless --insecure-control=true")
	}
	return nil
}
