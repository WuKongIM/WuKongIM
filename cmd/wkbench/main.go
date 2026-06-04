package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
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
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: wkbench <run|worker|validate|doctor|dev-sim|capacity|metrics|report>")
		return exitConfig
	}
	switch args[0] {
	case "worker":
		return runWorker(args[1:], stderr)
	case "validate":
		return runValidate(args[1:], stderr)
	case "doctor":
		return runDoctor(args[1:], stderr)
	case "run":
		return runBench(args[1:], stderr)
	case "dev-sim":
		return runDevSim(args[1:], stderr)
	case "capacity":
		return runCapacity(args[1:], stderr)
	case "metrics":
		return runMetrics(args[1:], stderr)
	case "report":
		fmt.Fprintf(stderr, "%s is not implemented yet\n", args[0])
		return exitInternal
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return exitConfig
	}
}

func runMetrics(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: wkbench metrics <classify>")
		return exitConfig
	}
	switch args[0] {
	case "classify":
		return runMetricsClassify(args[1:], stderr)
	default:
		fmt.Fprintln(stderr, "usage: wkbench metrics <classify>")
		return exitConfig
	}
}

func runMetricsClassify(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("metrics classify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var beforePath string
	var afterPath string
	fs.StringVar(&beforePath, "before", "", "Prometheus text snapshot before the measured window")
	fs.StringVar(&afterPath, "after", "", "Prometheus text snapshot after the measured window")
	if err := fs.Parse(args); err != nil {
		return exitConfig
	}
	if strings.TrimSpace(beforePath) == "" || strings.TrimSpace(afterPath) == "" {
		fmt.Fprintln(stderr, "--before and --after are required")
		return exitConfig
	}
	before, err := readPrometheusSnapshot(beforePath)
	if err != nil {
		fmt.Fprintf(stderr, "read before snapshot failed: %v\n", err)
		return exitConfig
	}
	after, err := readPrometheusSnapshot(afterPath)
	if err != nil {
		fmt.Fprintf(stderr, "read after snapshot failed: %v\n", err)
		return exitConfig
	}
	printWukongIMV2Attribution(stderr, benchmetrics.AnalyzeWukongIMV2Prometheus(before, after))
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

func printWukongIMV2Attribution(w io.Writer, report benchmetrics.WukongIMV2Attribution) {
	fmt.Fprintln(w, "# wukongimv2 metrics attribution")
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
	fmt.Fprintf(w, "channelv2_reactor_mailbox_depth_max: %.0f\n", report.ChannelV2ReactorMailboxDepthMax)
	fmt.Fprintf(w, "channelv2_worker_queue_depth_max: %.0f\n", report.ChannelV2WorkerQueueDepthMax)
	fmt.Fprintf(w, "channelv2_append_p99_seconds: %.6f\n", report.ChannelV2AppendP99Seconds)
	fmt.Fprintf(w, "channelv2_meta_resolve_p99_seconds: %.6f\n", report.ChannelV2MetaResolveP99Seconds)
	fmt.Fprintf(w, "channelv2_meta_slot_read_p99_seconds: %.6f\n", report.ChannelV2MetaSlotReadP99Seconds)
	fmt.Fprintf(w, "channelv2_meta_create_build_p99_seconds: %.6f\n", report.ChannelV2MetaCreateBuildP99Seconds)
	fmt.Fprintf(w, "channelv2_meta_create_propose_p99_seconds: %.6f\n", report.ChannelV2MetaCreateProposeP99Seconds)
	fmt.Fprintf(w, "channelv2_meta_create_propose_local_p99_seconds: %.6f\n", report.ChannelV2MetaCreateProposeLocalP99Seconds)
	fmt.Fprintf(w, "channelv2_meta_create_propose_forward_p99_seconds: %.6f\n", report.ChannelV2MetaCreateProposeForwardP99Seconds)
	fmt.Fprintf(w, "channelv2_meta_create_slot_propose_submit_p99_seconds: %.6f\n", report.ChannelV2MetaCreateSlotProposeSubmitP99Seconds)
	fmt.Fprintf(w, "channelv2_meta_create_slot_propose_wait_p99_seconds: %.6f\n", report.ChannelV2MetaCreateSlotProposeWaitP99Seconds)
	fmt.Fprintf(w, "channelv2_meta_create_slot_control_wait_p99_seconds: %.6f\n", report.ChannelV2MetaCreateSlotControlWaitP99Seconds)
	fmt.Fprintf(w, "channelv2_meta_create_slot_raft_commit_wait_p99_seconds: %.6f\n", report.ChannelV2MetaCreateSlotRaftCommitWaitP99Seconds)
	fmt.Fprintf(w, "channelv2_meta_create_slot_fsm_apply_p99_seconds: %.6f\n", report.ChannelV2MetaCreateSlotFSMApplyP99Seconds)
	fmt.Fprintf(w, "channelv2_meta_create_slot_fsm_commit_p99_seconds: %.6f\n", report.ChannelV2MetaCreateSlotFSMCommitP99Seconds)
	fmt.Fprintf(w, "channelv2_meta_create_slot_mark_applied_p99_seconds: %.6f\n", report.ChannelV2MetaCreateSlotMarkAppliedP99Seconds)
	fmt.Fprintf(w, "channelv2_meta_create_write_p99_seconds: %.6f\n", report.ChannelV2MetaCreateWriteP99Seconds)
	fmt.Fprintf(w, "channelv2_meta_final_read_p99_seconds: %.6f\n", report.ChannelV2MetaFinalReadP99Seconds)
	fmt.Fprintf(w, "channelv2_meta_apply_p99_seconds: %.6f\n", report.ChannelV2MetaApplyP99Seconds)
	fmt.Fprintf(w, "channelv2_runtime_append_p99_seconds: %.6f\n", report.ChannelV2RuntimeAppendP99Seconds)
	fmt.Fprintf(w, "channelv2_runtime_append_reserve_wait_p99_seconds: %.6f\n", report.ChannelV2RuntimeAppendReserveWaitP99Seconds)
	fmt.Fprintf(w, "channelv2_runtime_append_submit_p99_seconds: %.6f\n", report.ChannelV2RuntimeAppendSubmitP99Seconds)
	fmt.Fprintf(w, "channelv2_runtime_append_wait_p99_seconds: %.6f\n", report.ChannelV2RuntimeAppendWaitP99Seconds)
	fmt.Fprintf(w, "channelv2_append_batch_wait_p99_seconds: %.6f\n", report.ChannelV2AppendBatchWaitP99Seconds)
	fmt.Fprintf(w, "channelv2_append_batch_records_p50: %.3f\n", report.ChannelV2AppendBatchRecordsP50)
	fmt.Fprintf(w, "channelv2_append_store_wait_p99_seconds: %.6f\n", report.ChannelV2AppendStoreWaitP99Seconds)
	fmt.Fprintf(w, "channelv2_append_post_store_commit_wait_p99_seconds: %.6f\n", report.ChannelV2AppendPostStoreCommitWaitP99Seconds)
	fmt.Fprintf(w, "channelv2_append_quorum_follower_pull_wait_p99_seconds: %.6f\n", report.ChannelV2AppendQuorumFollowerPullWaitP99Seconds)
	fmt.Fprintf(w, "channelv2_append_quorum_ack_offset_wait_p99_seconds: %.6f\n", report.ChannelV2AppendQuorumAckOffsetWaitP99Seconds)
	fmt.Fprintf(w, "channelv2_append_quorum_hw_advance_wait_p99_seconds: %.6f\n", report.ChannelV2AppendQuorumHWAdvanceWaitP99Seconds)
	fmt.Fprintf(w, "channelv2_append_quorum_final_complete_p99_seconds: %.6f\n", report.ChannelV2AppendQuorumFinalCompleteP99Seconds)
	fmt.Fprintf(w, "channelv2_replication_follower_pull_hint_to_submit_p99_seconds: %.6f\n", report.ChannelV2ReplicationPullHintToSubmitP99Seconds)
	fmt.Fprintf(w, "channelv2_replication_follower_pull_rpc_p99_seconds: %.6f\n", report.ChannelV2ReplicationPullRPCP99Seconds)
	fmt.Fprintf(w, "channelv2_need_meta_pull_rpc_p99_seconds: %.6f\n", report.ChannelV2NeedMetaPullRPCP99Seconds)
	fmt.Fprintf(w, "channelv2_replication_follower_store_apply_p99_seconds: %.6f\n", report.ChannelV2ReplicationStoreApplyP99Seconds)
	fmt.Fprintf(w, "channelv2_replication_follower_apply_to_ack_return_p99_seconds: %.6f\n", report.ChannelV2ReplicationApplyToAckReturnP99Seconds)
	fmt.Fprintf(w, "channelv2_pending_meta_current_max: %.0f\n", report.ChannelV2PendingMetaCurrentMax)
	fmt.Fprintf(w, "channelv2_pending_meta_created_count: %.0f\n", report.ChannelV2PendingMetaCreatedCount)
	fmt.Fprintf(w, "channelv2_pending_meta_converted_count: %.0f\n", report.ChannelV2PendingMetaConvertedCount)
	fmt.Fprintf(w, "channelv2_pending_meta_released_count: %.0f\n", report.ChannelV2PendingMetaReleasedCount)
	fmt.Fprintf(w, "channelv2_pending_meta_timeout_release_count: %.0f\n", report.ChannelV2PendingMetaTimeoutReleaseCount)
	fmt.Fprintf(w, "channelv2_pending_meta_not_ready_release_count: %.0f\n", report.ChannelV2PendingMetaNotReadyReleaseCount)
	fmt.Fprintf(w, "channelv2_pull_hint_submitted_count: %.0f\n", report.ChannelV2PullHintSubmittedCount)
	fmt.Fprintf(w, "channelv2_pull_hint_ok_count: %.0f\n", report.ChannelV2PullHintOKCount)
	fmt.Fprintf(w, "channelv2_pull_hint_err_count: %.0f\n", report.ChannelV2PullHintErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_stale_meta_err_count: %.0f\n", report.ChannelV2PullHintStaleMetaErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_channel_not_found_err_count: %.0f\n", report.ChannelV2PullHintChannelNotFoundErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_not_ready_err_count: %.0f\n", report.ChannelV2PullHintNotReadyErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_not_leader_err_count: %.0f\n", report.ChannelV2PullHintNotLeaderErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_invalid_config_err_count: %.0f\n", report.ChannelV2PullHintInvalidConfigErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_closed_err_count: %.0f\n", report.ChannelV2PullHintClosedErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_canceled_err_count: %.0f\n", report.ChannelV2PullHintCanceledErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_timeout_err_count: %.0f\n", report.ChannelV2PullHintTimeoutErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_remote_err_count: %.0f\n", report.ChannelV2PullHintRemoteErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_other_err_count: %.0f\n", report.ChannelV2PullHintOtherErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_receive_ok_count: %.0f\n", report.ChannelV2PullHintReceiveOKCount)
	fmt.Fprintf(w, "channelv2_pull_hint_receive_err_count: %.0f\n", report.ChannelV2PullHintReceiveErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_receive_state_check_err_count: %.0f\n", report.ChannelV2PullHintReceiveStateCheckErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_receive_meta_resolve_err_count: %.0f\n", report.ChannelV2PullHintReceiveMetaResolveErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_receive_meta_hint_ok_count: %.0f\n", report.ChannelV2PullHintReceiveMetaHintOKCount)
	fmt.Fprintf(w, "channelv2_pull_hint_receive_meta_validate_err_count: %.0f\n", report.ChannelV2PullHintReceiveMetaValidateErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_receive_meta_apply_err_count: %.0f\n", report.ChannelV2PullHintReceiveMetaApplyErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_receive_submit_err_count: %.0f\n", report.ChannelV2PullHintReceiveSubmitErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_receive_await_err_count: %.0f\n", report.ChannelV2PullHintReceiveAwaitErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_receive_stale_meta_err_count: %.0f\n", report.ChannelV2PullHintReceiveStaleMetaErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_receive_channel_not_found_err_count: %.0f\n", report.ChannelV2PullHintReceiveChannelNotFoundErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_receive_not_ready_err_count: %.0f\n", report.ChannelV2PullHintReceiveNotReadyErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_receive_canceled_err_count: %.0f\n", report.ChannelV2PullHintReceiveCanceledErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_receive_timeout_err_count: %.0f\n", report.ChannelV2PullHintReceiveTimeoutErrCount)
	fmt.Fprintf(w, "channelv2_pull_hint_receive_other_err_count: %.0f\n", report.ChannelV2PullHintReceiveOtherErrCount)
	fmt.Fprintf(w, "channelv2_need_meta_pull_submitted_count: %.0f\n", report.ChannelV2NeedMetaPullSubmittedCount)
	fmt.Fprintf(w, "channelv2_need_meta_pull_ok_count: %.0f\n", report.ChannelV2NeedMetaPullOKCount)
	fmt.Fprintf(w, "channelv2_need_meta_pull_retry_count: %.0f\n", report.ChannelV2NeedMetaPullRetryCount)
	fmt.Fprintf(w, "channelv2_need_meta_pull_err_count: %.0f\n", report.ChannelV2NeedMetaPullErrCount)
	fmt.Fprintf(w, "channelv2_need_meta_pull_stale_meta_err_count: %.0f\n", report.ChannelV2NeedMetaPullStaleMetaErrCount)
	fmt.Fprintf(w, "channelv2_need_meta_pull_channel_not_found_err_count: %.0f\n", report.ChannelV2NeedMetaPullChannelNotFoundErrCount)
	fmt.Fprintf(w, "channelv2_need_meta_pull_not_ready_err_count: %.0f\n", report.ChannelV2NeedMetaPullNotReadyErrCount)
	fmt.Fprintf(w, "channelv2_need_meta_pull_not_leader_err_count: %.0f\n", report.ChannelV2NeedMetaPullNotLeaderErrCount)
	fmt.Fprintf(w, "channelv2_need_meta_pull_not_replica_err_count: %.0f\n", report.ChannelV2NeedMetaPullNotReplicaErrCount)
	fmt.Fprintf(w, "channelv2_need_meta_pull_backpressured_err_count: %.0f\n", report.ChannelV2NeedMetaPullBackpressuredErrCount)
	fmt.Fprintf(w, "channelv2_need_meta_pull_invalid_config_err_count: %.0f\n", report.ChannelV2NeedMetaPullInvalidConfigErrCount)
	fmt.Fprintf(w, "channelv2_need_meta_pull_closed_err_count: %.0f\n", report.ChannelV2NeedMetaPullClosedErrCount)
	fmt.Fprintf(w, "channelv2_need_meta_pull_canceled_err_count: %.0f\n", report.ChannelV2NeedMetaPullCanceledErrCount)
	fmt.Fprintf(w, "channelv2_need_meta_pull_timeout_err_count: %.0f\n", report.ChannelV2NeedMetaPullTimeoutErrCount)
	fmt.Fprintf(w, "channelv2_need_meta_pull_remote_err_count: %.0f\n", report.ChannelV2NeedMetaPullRemoteErrCount)
	fmt.Fprintf(w, "channelv2_need_meta_pull_other_err_count: %.0f\n", report.ChannelV2NeedMetaPullOtherErrCount)
	fmt.Fprintf(w, "channelv2_worker_task_p99_seconds: %.6f\n", report.ChannelV2WorkerTaskP99Seconds)
	fmt.Fprintf(w, "storage_commit_queue_depth_max: %.0f\n", report.StorageCommitQueueDepthMax)
	fmt.Fprintf(w, "storage_commit_batch_requests_p50: %.3f\n", report.StorageCommitBatchRequestsP50)
	fmt.Fprintf(w, "storage_commit_batch_records_p50: %.3f\n", report.StorageCommitBatchRecordsP50)
	fmt.Fprintf(w, "storage_commit_p99_seconds: %.6f\n", report.StorageCommitP99Seconds)
	fmt.Fprintf(w, "storage_commit_total_p99_seconds: %.6f\n", report.StorageCommitTotalP99Seconds)
	if len(report.Reasons) == 0 {
		return
	}
	fmt.Fprintln(w, "reasons:")
	for _, reason := range report.Reasons {
		fmt.Fprintf(w, "- %s\n", reason)
	}
}

func runCapacity(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: wkbench capacity <send|hot-channel|activate-channels>")
		return exitConfig
	}
	switch args[0] {
	case "send":
		return runCapacitySend(args[1:], stderr)
	case "hot-channel":
		return runCapacityHotChannel(args[1:], stderr)
	case "activate-channels":
		return runCapacityActivateChannels(args[1:], stderr)
	default:
		fmt.Fprintln(stderr, "usage: wkbench capacity <send|hot-channel|activate-channels>")
		return exitConfig
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

func runCapacitySend(args []string, stderr io.Writer) int {
	cfg, code := parseCapacitySendConfig(args, stderr)
	if code != 0 {
		return code
	}
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

func runCapacityHotChannel(args []string, stderr io.Writer) int {
	cfg, code := parseCapacityHotChannelConfig(args, stderr)
	if code != 0 {
		return code
	}
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

func runCapacityActivateChannels(args []string, stderr io.Writer) int {
	cfg, code := parseCapacityActivateChannelsConfig(args, stderr)
	if code != 0 {
		return code
	}
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
	fs := flag.NewFlagSet("capacity send", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var apiCSV string
	var gatewayCSV string
	fs.StringVar(&apiCSV, "api", "", "comma-separated target HTTP API base addresses")
	fs.StringVar(&gatewayCSV, "gateway", "", "optional comma-separated WKProto TCP gateway addresses")
	fs.StringVar(&cfg.BenchToken, "bench-token", cfg.BenchToken, "optional bearer token for bench API routes")
	fs.StringVar(&cfg.Profile, "profile", cfg.Profile, "traffic profile: person, group, or mixed")
	fs.Float64Var(&cfg.StartQPS, "start-qps", cfg.StartQPS, "first offered ingress QPS")
	fs.Float64Var(&cfg.MaxQPS, "max-qps", cfg.MaxQPS, "maximum offered ingress QPS")
	fs.Float64Var(&cfg.StepFactor, "step-factor", cfg.StepFactor, "ramp multiplier after passing attempts")
	fs.DurationVar(&cfg.Duration, "duration", cfg.Duration, "measured run duration per attempt")
	fs.DurationVar(&cfg.Warmup, "warmup", cfg.Warmup, "warmup duration per attempt")
	fs.DurationVar(&cfg.Cooldown, "cooldown", cfg.Cooldown, "cooldown duration per attempt")
	fs.DurationVar(&cfg.StableP99, "stable-p99", cfg.StableP99, "maximum stable sendack p99 latency")
	fs.Float64Var(&cfg.MinActualRatio, "min-actual-ratio", cfg.MinActualRatio, "minimum actual/offered QPS ratio")
	fs.Float64Var(&cfg.MaxSendackErrorRate, "max-sendack-error-rate", cfg.MaxSendackErrorRate, "maximum allowed sendack error rate")
	fs.Float64Var(&cfg.MaxConnectErrorRate, "max-connect-error-rate", cfg.MaxConnectErrorRate, "maximum allowed connect error rate")
	fs.BoolVar(&cfg.BinarySearch, "binary-search", cfg.BinarySearch, "enable binary search after first failed ramp attempt")
	fs.Float64Var(&cfg.BinarySearchMinDeltaRatio, "binary-search-min-delta-ratio", cfg.BinarySearchMinDeltaRatio, "binary search stop ratio")
	fs.IntVar(&cfg.GroupMembers, "group-members", cfg.GroupMembers, "members per generated group channel")
	fs.StringVar(&cfg.ReportDir, "report-dir", cfg.ReportDir, "capacity report output directory")
	if err := fs.Parse(args); err != nil {
		return capacity.Config{}, exitConfig
	}
	cfg.APIAddrs = splitCSV(apiCSV)
	cfg.GatewayTCPAddrs = splitCSV(gatewayCSV)
	if len(cfg.APIAddrs) == 0 {
		fmt.Fprintln(stderr, "--api is required")
		return capacity.Config{}, exitConfig
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return capacity.Config{}, exitConfig
	}
	return cfg, 0
}

func parseCapacityHotChannelConfig(args []string, stderr io.Writer) (capacity.HotChannelConfig, int) {
	cfg := capacity.DefaultHotChannelConfig()
	fs := flag.NewFlagSet("capacity hot-channel", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var apiCSV string
	var gatewayCSV string
	fs.StringVar(&apiCSV, "api", "", "comma-separated target HTTP API base addresses")
	fs.StringVar(&gatewayCSV, "gateway", "", "optional comma-separated WKProto TCP gateway addresses")
	fs.StringVar(&cfg.BenchToken, "bench-token", cfg.BenchToken, "optional bearer token for bench API routes")
	fs.IntVar(&cfg.Senders, "senders", cfg.Senders, "number of online senders fanning into the one hot group channel")
	fs.Float64Var(&cfg.StartQPS, "start-qps", cfg.StartQPS, "first offered ingress QPS")
	fs.Float64Var(&cfg.MaxQPS, "max-qps", cfg.MaxQPS, "maximum offered ingress QPS")
	fs.Float64Var(&cfg.StepFactor, "step-factor", cfg.StepFactor, "ramp multiplier after passing attempts")
	fs.DurationVar(&cfg.Duration, "duration", cfg.Duration, "measured run duration per attempt")
	fs.DurationVar(&cfg.Warmup, "warmup", cfg.Warmup, "warmup duration per attempt")
	fs.DurationVar(&cfg.Cooldown, "cooldown", cfg.Cooldown, "cooldown duration per attempt")
	fs.DurationVar(&cfg.StableP99, "stable-p99", cfg.StableP99, "maximum stable sendack p99 latency")
	fs.Float64Var(&cfg.MinActualRatio, "min-actual-ratio", cfg.MinActualRatio, "minimum actual/offered QPS ratio")
	fs.Float64Var(&cfg.MaxSendackErrorRate, "max-sendack-error-rate", cfg.MaxSendackErrorRate, "maximum allowed sendack error rate")
	fs.Float64Var(&cfg.MaxConnectErrorRate, "max-connect-error-rate", cfg.MaxConnectErrorRate, "maximum allowed connect error rate")
	fs.BoolVar(&cfg.BinarySearch, "binary-search", cfg.BinarySearch, "enable binary search after first failed ramp attempt")
	fs.Float64Var(&cfg.BinarySearchMinDeltaRatio, "binary-search-min-delta-ratio", cfg.BinarySearchMinDeltaRatio, "binary search stop ratio")
	fs.StringVar(&cfg.ReportDir, "report-dir", cfg.ReportDir, "capacity report output directory")
	if err := fs.Parse(args); err != nil {
		return capacity.HotChannelConfig{}, exitConfig
	}
	cfg.APIAddrs = splitCSV(apiCSV)
	cfg.GatewayTCPAddrs = splitCSV(gatewayCSV)
	if len(cfg.APIAddrs) == 0 {
		fmt.Fprintln(stderr, "--api is required")
		return capacity.HotChannelConfig{}, exitConfig
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return capacity.HotChannelConfig{}, exitConfig
	}
	return cfg, 0
}

func parseCapacityActivateChannelsConfig(args []string, stderr io.Writer) (capacity.ActivateChannelsConfig, int) {
	cfg := capacity.DefaultActivateChannelsConfig()
	fs := flag.NewFlagSet("capacity activate-channels", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var apiCSV string
	var gatewayCSV string
	fs.StringVar(&apiCSV, "api", "", "comma-separated target HTTP API base addresses")
	fs.StringVar(&gatewayCSV, "gateway", "", "optional comma-separated WKProto TCP gateway addresses")
	fs.StringVar(&cfg.BenchToken, "bench-token", cfg.BenchToken, "optional bearer token for bench API routes")
	fs.StringVar(&cfg.RunID, "run-id", cfg.RunID, "stable benchmark run identifier")
	fs.IntVar(&cfg.Channels, "channels", cfg.Channels, "number of group channels to activate")
	fs.IntVar(&cfg.Users, "users", cfg.Users, "number of online users prepared for activation")
	fs.IntVar(&cfg.GroupMembers, "group-members", cfg.GroupMembers, "members per generated group channel")
	fs.Float64Var(&cfg.PrepareRatePerSecond, "prepare-rate", cfg.PrepareRatePerSecond, "bench API preparation rate limit per second; 0 means unlimited")
	fs.Float64Var(&cfg.ConnectRatePerSecond, "connect-rate", cfg.ConnectRatePerSecond, "gateway connect attempt rate limit per second; 0 means unlimited")
	fs.IntVar(&cfg.ActivationConcurrency, "activation-concurrency", cfg.ActivationConcurrency, "maximum in-flight SEND operations during activation")
	fs.DurationVar(&cfg.ActivationWindow, "activation-window", cfg.ActivationWindow, "active window used to schedule one SEND per channel")
	fs.DurationVar(&cfg.Hold, "hold", cfg.Hold, "post-activation observation duration")
	fs.DurationVar(&cfg.HoldProbeInterval, "hold-probe-interval", cfg.HoldProbeInterval, "interval between hold runtime snapshots")
	fs.IntVar(&cfg.ProbeBatchSize, "probe-batch-size", cfg.ProbeBatchSize, "generated channels checked per runtime probe batch")
	fs.DurationVar(&cfg.StableP99, "stable-p99", cfg.StableP99, "maximum stable sendack p99 latency")
	fs.Float64Var(&cfg.MaxSendackErrorRate, "max-sendack-error-rate", cfg.MaxSendackErrorRate, "maximum allowed sendack error rate")
	fs.Float64Var(&cfg.MaxConnectErrorRate, "max-connect-error-rate", cfg.MaxConnectErrorRate, "maximum allowed connect error rate")
	fs.BoolVar(&cfg.EvictAfter, "evict-after", cfg.EvictAfter, "evict generated channel runtime state after probing")
	fs.StringVar(&cfg.ReportDir, "report-dir", cfg.ReportDir, "activation report output directory")
	if err := fs.Parse(args); err != nil {
		return capacity.ActivateChannelsConfig{}, exitConfig
	}
	cfg.APIAddrs = splitCSV(apiCSV)
	cfg.GatewayTCPAddrs = splitCSV(gatewayCSV)
	if len(cfg.APIAddrs) == 0 {
		fmt.Fprintln(stderr, "--api is required")
		return capacity.ActivateChannelsConfig{}, exitConfig
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(stderr, "config validation failed: %v\n", err)
		return capacity.ActivateChannelsConfig{}, exitConfig
	}
	return cfg, 0
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

func runDevSim(args []string, stderr io.Writer) int {
	cliCfg, code := parseDevSimConfig(args, stderr)
	if code != 0 {
		return code
	}
	if cliCfg.configPath == "" {
		return 0
	}
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
	fs := flag.NewFlagSet("dev-sim", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: wkbench dev-sim --config <path> [--status-listen addr]")
		fs.PrintDefaults()
	}
	var cfg devSimCLIConfig
	fs.StringVar(&cfg.configPath, "config", "", "dev-sim YAML file")
	fs.StringVar(&cfg.statusListen, "status-listen", "", "override status.listen")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return devSimCLIConfig{}, 0
		}
		return devSimCLIConfig{}, exitConfig
	}
	if strings.TrimSpace(cfg.configPath) == "" {
		fmt.Fprintln(stderr, "--config is required")
		return devSimCLIConfig{}, exitConfig
	}
	return cfg, 0
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

func runBench(args []string, stderr io.Writer) int {
	targetCfg, scenario, workers, phasePollTimeout, code := loadValidateRunInputs(args, stderr)
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

func runValidate(args []string, stderr io.Writer) int {
	targetCfg, scenario, workers, code := loadValidateInputs("validate", args, stderr)
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

func runDoctor(args []string, stderr io.Writer) int {
	targetCfg, workers, scenario, hasScenario, code := loadDoctorInputs("doctor", args, stderr)
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
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cfg runBenchConfig
	fs.StringVar(&cfg.paths.target, "target", "", "target YAML file")
	fs.StringVar(&cfg.paths.scenario, "scenario", "", "scenario YAML file")
	fs.StringVar(&cfg.paths.workers, "workers", "", "workers YAML file")
	fs.DurationVar(&cfg.phasePollTimeout, "phase-poll-timeout", 0, "base worker phase poll timeout; connect/warmup/run/cooldown add their configured schedule duration; 0 uses the wkbench default")
	if err := fs.Parse(args); err != nil {
		return runBenchConfig{}, exitConfig
	}
	if cfg.paths.target == "" {
		fmt.Fprintln(stderr, "--target is required")
		return runBenchConfig{}, exitConfig
	}
	if cfg.paths.scenario == "" {
		fmt.Fprintln(stderr, "--scenario is required")
		return runBenchConfig{}, exitConfig
	}
	if cfg.paths.workers == "" {
		fmt.Fprintln(stderr, "--workers is required")
		return runBenchConfig{}, exitConfig
	}
	return cfg, 0
}

func parseBenchConfigPaths(name string, args []string, stderr io.Writer, scenarioRequired bool) (benchConfigPaths, int) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var paths benchConfigPaths
	fs.StringVar(&paths.target, "target", "", "target YAML file")
	fs.StringVar(&paths.scenario, "scenario", "", "scenario YAML file")
	fs.StringVar(&paths.workers, "workers", "", "workers YAML file")
	if err := fs.Parse(args); err != nil {
		return benchConfigPaths{}, exitConfig
	}
	if paths.target == "" {
		fmt.Fprintln(stderr, "--target is required")
		return benchConfigPaths{}, exitConfig
	}
	if scenarioRequired && paths.scenario == "" {
		fmt.Fprintln(stderr, "--scenario is required")
		return benchConfigPaths{}, exitConfig
	}
	if paths.workers == "" {
		fmt.Fprintln(stderr, "--workers is required")
		return benchConfigPaths{}, exitConfig
	}
	return paths, 0
}

type workerCLIConfig struct {
	listen string
	server worker.Config
}

func runWorker(args []string, stderr io.Writer) int {
	cfg, code := parseWorkerConfig(args, stderr)
	if code != 0 {
		return code
	}
	if err := http.ListenAndServe(cfg.listen, worker.NewServer(cfg.server)); err != nil {
		fmt.Fprintf(stderr, "worker server failed: %v\n", err)
		return exitConfig
	}
	return 0
}

func parseWorkerConfig(args []string, stderr io.Writer) (workerCLIConfig, int) {
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfg := workerCLIConfig{listen: "127.0.0.1:19090"}
	fs.StringVar(&cfg.listen, "listen", cfg.listen, "worker control listen address")
	fs.StringVar(&cfg.server.WorkDir, "work-dir", "", "directory for worker control state")
	fs.StringVar(&cfg.server.ControlToken, "control-token", os.Getenv("WK_BENCH_WORKER_TOKEN"), "bearer token for worker control API")
	fs.BoolVar(&cfg.server.InsecureControl, "insecure-control", false, "allow unauthenticated worker control API")
	if err := fs.Parse(args); err != nil {
		return workerCLIConfig{}, exitConfig
	}
	if cfg.server.InsecureControl {
		cfg.server.ControlToken = ""
		return cfg, 0
	}
	if cfg.server.ControlToken == "" {
		fmt.Fprintln(stderr, "--control-token is required unless --insecure-control=true")
		return workerCLIConfig{}, exitConfig
	}
	return cfg, 0
}
