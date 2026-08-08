//go:build e2e

package medium_recipient_hotpath

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	benchmetrics "github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	benchtarget "github.com/WuKongIM/WuKongIM/internal/bench/target"
	benchmodel "github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
)

const (
	mediumPermissionSoakEvidenceSchema = "wukongim/permission-soak-evidence/v1"
	mediumPermissionSoakFailureSchema  = "wukongim/permission-soak-failure/v1"
	mediumPermissionSoakDuration       = 30 * time.Minute
	mediumPermissionSoakMinDuration    = 10 * time.Second
	mediumPermissionSoakMaxDuration    = 30 * time.Minute
	mediumPermissionSoakGroupChannels  = mediumCloudGroupChannelCount
	mediumPermissionSoakMaxLatency     = 10 * time.Second
	mediumPermissionSoakDrainAllowance = 30 * time.Second
	mediumPermissionSoakHeartbeat      = 30 * time.Second
)

type permissionSoakConfig struct {
	enabled       bool
	duration      time.Duration
	offeredQPS    int
	groupChannels int
}

// permissionSoakStageLatencyEvidence attributes end-to-end SENDACK latency to
// bounded public histogram stages observed only during the measured window.
type permissionSoakStageLatencyEvidence struct {
	GatewayDispatchWaitP99MS            float64 `json:"gateway_dispatch_wait"`
	GatewaySendHandleP99MS              float64 `json:"gateway_send_handle"`
	ChannelAppendRouterLocalP99MS       float64 `json:"channel_append_router_local"`
	ChannelAppendRouterRemoteP99MS      float64 `json:"channel_append_router_remote"`
	ChannelAppendRouterBatchP99MS       float64 `json:"channel_append_router_batch"`
	ChannelAppendRouterBatchItemP99MS   float64 `json:"channel_append_router_batch_item"`
	MessagePermissionP99MS              float64 `json:"message_permission"`
	MessagePreAppendP99MS               float64 `json:"message_pre_append"`
	MessageSubmitterP99MS               float64 `json:"message_submitter"`
	ChannelStoreAppendWaitP99MS         float64 `json:"channel_store_append_wait"`
	ChannelPostStoreCommitWaitP99MS     float64 `json:"channel_post_store_commit_wait"`
	ChannelQuorumFollowerPullWaitP99MS  float64 `json:"channel_quorum_follower_pull_wait"`
	ChannelQuorumAckOffsetWaitP99MS     float64 `json:"channel_quorum_ack_offset_wait"`
	ChannelQuorumHWAdvanceWaitP99MS     float64 `json:"channel_quorum_hw_advance_wait"`
	ChannelQuorumFinalCompleteWaitP99MS float64 `json:"channel_quorum_final_complete_wait"`
	StorageLeaderCommitRequestP99MS     float64 `json:"storage_leader_commit_request"`
	StorageFollowerCommitRequestP99MS   float64 `json:"storage_follower_commit_request"`
	StoragePhysicalCommitP99MS          float64 `json:"storage_physical_commit"`
	ChannelLeaderPullMailboxWaitP99MS   float64 `json:"channel_leader_pull_mailbox_wait"`
	ChannelLeaderPullAckApplyP99MS      float64 `json:"channel_leader_pull_ack_apply"`
	ChannelLeaderPullHandlerP99MS       float64 `json:"channel_leader_pull_handler"`
}

// permissionSoakEvidence is the bounded, machine-readable result of the
// long-running public-protocol permission pressure gate.
type permissionSoakEvidence struct {
	Schema                           string                             `json:"schema"`
	ConfiguredDurationMS             float64                            `json:"configured_duration_ms"`
	SendLoopDurationMS               float64                            `json:"send_loop_duration_ms"`
	MeasuredDurationMS               float64                            `json:"measured_duration_ms"`
	Messages                         int                                `json:"messages"`
	GroupChannels                    int                                `json:"group_channels"`
	ActiveGroupChannels              int                                `json:"active_group_channels"`
	Senders                          int                                `json:"senders"`
	Recipients                       int                                `json:"recipients"`
	OfferedQPS                       int                                `json:"offered_qps"`
	IngressPerSecond                 float64                            `json:"ingress_per_second"`
	CompletionPerSecond              float64                            `json:"completion_per_second"`
	SendackP50MS                     float64                            `json:"sendack_p50_ms"`
	SendackP99MS                     float64                            `json:"sendack_p99_ms"`
	SendackMaxMS                     float64                            `json:"sendack_max_ms"`
	RecvP99MS                        float64                            `json:"recv_p99_ms"`
	RecvMaxMS                        float64                            `json:"recv_max_ms"`
	StageP99MS                       permissionSoakStageLatencyEvidence `json:"stage_p99_ms"`
	StageP999MS                      permissionSoakStageLatencyEvidence `json:"stage_p999_ms"`
	GatewayBatchRecordsP99           float64                            `json:"gateway_batch_records_p99"`
	StageLatencyCaptureError         string                             `json:"stage_latency_capture_error,omitempty"`
	MaxGatewayQueueRatio             float64                            `json:"max_gateway_queue_ratio"`
	MaxRecipientQueueRatio           float64                            `json:"max_recipient_queue_ratio"`
	MaxRecipientWorkerRatio          float64                            `json:"max_recipient_worker_ratio"`
	MaxChannelReactorMailboxRatio    float64                            `json:"max_channel_reactor_mailbox_ratio"`
	MaxChannelStoreAppendQueueRatio  float64                            `json:"max_channel_store_append_queue_ratio"`
	MaxChannelStoreAppendWorkerRatio float64                            `json:"max_channel_store_append_worker_ratio"`
	MaxChannelStoreApplyQueueRatio   float64                            `json:"max_channel_store_apply_queue_ratio"`
	MaxChannelStoreApplyWorkerRatio  float64                            `json:"max_channel_store_apply_worker_ratio"`
	MaxChannelRPCQueueRatio          float64                            `json:"max_channel_rpc_queue_ratio"`
	MaxChannelRPCWorkerRatio         float64                            `json:"max_channel_rpc_worker_ratio"`
	ChannelRPCAdmissionFull          float64                            `json:"channel_rpc_admission_full"`
	ChannelRPCPullAdmissionFull      float64                            `json:"channel_rpc_pull_admission_full"`
	ChannelRPCHintAdmissionFull      float64                            `json:"channel_rpc_pull_hint_admission_full"`
	ChannelRPCPullPaced              float64                            `json:"channel_rpc_pull_paced"`
	ChannelRPCHintPaced              float64                            `json:"channel_rpc_pull_hint_paced"`
	ChannelRPCPullBatches            float64                            `json:"channel_rpc_pull_batches"`
	ChannelRPCPullBatchItems         float64                            `json:"channel_rpc_pull_batch_items"`
	ChannelRPCHintBatches            float64                            `json:"channel_rpc_pull_hint_batches"`
	ChannelRPCHintBatchItems         float64                            `json:"channel_rpc_pull_hint_batch_items"`
	ChannelPullOKEmpty               float64                            `json:"channel_pull_ok_empty"`
	ChannelPullOKRecords             float64                            `json:"channel_pull_ok_records"`
	ChannelPullError                 float64                            `json:"channel_pull_error"`
	ChannelAppendHintPaced           float64                            `json:"channel_rpc_append_hint_paced"`
	ChannelResumeHintPaced           float64                            `json:"channel_rpc_resume_hint_paced"`
	ChannelStoreApplyTasks           float64                            `json:"channel_store_apply_tasks"`
	ChannelStoreApplyFull            float64                            `json:"channel_store_apply_admission_full"`
	ChannelStoreApplyPullPaced       float64                            `json:"channel_store_apply_pull_paced"`
	ChannelStoreCheckpointTasks      float64                            `json:"channel_store_checkpoint_tasks"`
	ChannelStoreCheckpointFull       float64                            `json:"channel_store_checkpoint_admission_full"`
	TransportRPCMetricNodes          int                                `json:"transport_rpc_metric_nodes"`
	MaxTransportRPCQueueRatio        float64                            `json:"max_transport_rpc_queue_ratio"`
	MaxTransportRPCBusyRatio         float64                            `json:"max_transport_rpc_busy_ratio"`
	TransportRPCRejected             float64                            `json:"transport_rpc_rejected"`
	PermissionSlotRPCCalls           float64                            `json:"permission_slot_rpc_calls"`
	PermissionSlotRPCErrors          float64                            `json:"permission_slot_rpc_errors"`
	PermissionSlotRPCAdmissionErrors float64                            `json:"permission_slot_rpc_admission_errors"`
	MaxPermissionSlotRPCQueueRatio   float64                            `json:"max_permission_slot_rpc_queue_ratio"`
	MaxPermissionSlotRPCInflight     float64                            `json:"max_permission_slot_rpc_inflight"`
	PermissionBatchStarted           float64                            `json:"permission_batch_started"`
	PermissionBatchPanics            float64                            `json:"permission_batch_panics"`
	MaxPermissionBatchActive         float64                            `json:"max_permission_batch_active"`
	MembershipMutationRows           float64                            `json:"membership_mutation_rows"`
	MaxAdvancePoolUtil               float64                            `json:"max_advance_pool_utilization"`
	MaxAdvancePoolWaiting            float64                            `json:"max_advance_pool_waiting"`
	MaxAppendPoolUtil                float64                            `json:"max_append_pool_utilization"`
	MaxPostCommitPoolUtil            float64                            `json:"max_post_commit_pool_utilization"`
	MaxPostCommitBacklog             float64                            `json:"max_post_commit_backlog"`
	MaxPostCommitHandoffRatio        float64                            `json:"max_post_commit_handoff_ratio"`
	MaxRouterGroupInflight           float64                            `json:"max_channel_append_router_group_inflight"`
	MaxRouterGroupCapacity           float64                            `json:"max_channel_append_router_group_capacity"`
	MaxRouterGroupRatio              float64                            `json:"max_channel_append_router_group_ratio"`
	MaxMessageCommitQueueDepth       float64                            `json:"max_message_commit_queue_depth"`
	MaxMessageMemTableBytes          float64                            `json:"max_message_memtable_bytes"`
	MaxMessageMemTableCount          float64                            `json:"max_message_memtable_count"`
	MaxMessageReadAmplification      float64                            `json:"max_message_read_amplification"`
	MaxMessageCompactionDebtBytes    float64                            `json:"max_message_compaction_debt_bytes"`
	MaxMessageCompactions            float64                            `json:"max_message_compactions_in_progress"`
	MaxMessageFlushes                float64                            `json:"max_message_flushes_in_progress"`
	MessageIdempotencyNegativeSkips  float64                            `json:"message_idempotency_negative_filter_skips"`
	MessageIdempotencyPointReads     float64                            `json:"message_idempotency_point_reads"`
	MessagePhysicalCommits           float64                            `json:"message_physical_commits"`
	MessageCommitBatchRequests       float64                            `json:"message_commit_batch_requests"`
	MessageCommitBatchRecords        float64                            `json:"message_commit_batch_records"`
	MessageCommitBatchBytes          float64                            `json:"message_commit_batch_bytes"`
	MessageCommitSeconds             float64                            `json:"message_commit_seconds"`
	MessageLeaderCommitRequests      float64                            `json:"message_leader_commit_requests"`
	MessageFollowerCommitRequests    float64                            `json:"message_follower_commit_requests"`
	MessageLeaderCommitSeconds       float64                            `json:"message_leader_commit_seconds"`
	MessageFollowerCommitSeconds     float64                            `json:"message_follower_commit_seconds"`
	MessageWALBytesIn                float64                            `json:"message_wal_bytes_in"`
	MessageWALBytesWritten           float64                            `json:"message_wal_bytes_written"`
	MessageFlushBytesWritten         float64                            `json:"message_flush_bytes_written"`
	MessageCompactionBytesRead       float64                            `json:"message_compaction_bytes_read"`
	MessageCompactionBytesWritten    float64                            `json:"message_compaction_bytes_written"`
	MessageSSTableSizeBytesDelta     float64                            `json:"message_sstable_size_bytes_delta"`
	RecipientProcessOK               float64                            `json:"recipient_worker_process_ok"`
	RecipientProcessRecipientsOK     float64                            `json:"recipient_worker_process_recipients_ok"`
	RecipientProcessError            float64                            `json:"recipient_worker_process_error"`
	ReceiverProgress                 []permissionSoakReceiverSnapshot   `json:"receiver_progress"`
	PluginReceiveAccepted            float64                            `json:"plugin_receive_enqueue_accepted"`
	PluginReceiveFull                float64                            `json:"plugin_receive_enqueue_full"`
	PluginReceiveClosed              float64                            `json:"plugin_receive_enqueue_closed"`
	PluginReceiveInvokeOK            float64                            `json:"plugin_receive_invoke_ok"`
	PluginReceiveInvokeError         float64                            `json:"plugin_receive_invoke_error"`
	MaxHeapBytes                     float64                            `json:"max_heap_bytes"`
	MaxAggregateHeapBytes            float64                            `json:"max_aggregate_heap_bytes"`
	AllocatedBytes                   float64                            `json:"allocated_bytes"`
	GCCountDelta                     float64                            `json:"gc_count_delta"`
	MetricSamples                    int                                `json:"metric_samples"`
	MetricSampleErrors               int                                `json:"metric_sample_errors"`
	PendingMessages                  int64                              `json:"pending_messages"`
	Drained                          bool                               `json:"drained"`
	ProcessContinuous                bool                               `json:"process_continuous"`
}

// permissionSoakFailureEvidence preserves bounded public-metric evidence when
// the long-running gate fails before it can emit its complete acceptance row.
type permissionSoakFailureEvidence struct {
	Schema                           string                             `json:"schema"`
	Phase                            string                             `json:"phase"`
	Error                            string                             `json:"error"`
	CompletedSendCalls               int                                `json:"completed_send_calls"`
	ElapsedMS                        float64                            `json:"elapsed_ms"`
	IngressPerSecond                 float64                            `json:"ingress_per_second"`
	SendackP99MS                     float64                            `json:"sendack_p99_ms"`
	RecvP99MS                        float64                            `json:"recv_p99_ms"`
	StageP99MS                       permissionSoakStageLatencyEvidence `json:"stage_p99_ms"`
	StageP999MS                      permissionSoakStageLatencyEvidence `json:"stage_p999_ms"`
	GatewayBatchRecordsP99           float64                            `json:"gateway_batch_records_p99"`
	StageLatencyCaptureError         string                             `json:"stage_latency_capture_error,omitempty"`
	PendingMessages                  int64                              `json:"pending_messages"`
	MaxGatewayQueueRatio             float64                            `json:"max_gateway_queue_ratio"`
	MaxRecipientQueueRatio           float64                            `json:"max_recipient_queue_ratio"`
	MaxRecipientWorkerRatio          float64                            `json:"max_recipient_worker_ratio"`
	MaxChannelReactorMailboxRatio    float64                            `json:"max_channel_reactor_mailbox_ratio"`
	MaxChannelStoreAppendQueueRatio  float64                            `json:"max_channel_store_append_queue_ratio"`
	MaxChannelStoreAppendWorkerRatio float64                            `json:"max_channel_store_append_worker_ratio"`
	MaxChannelStoreApplyQueueRatio   float64                            `json:"max_channel_store_apply_queue_ratio"`
	MaxChannelStoreApplyWorkerRatio  float64                            `json:"max_channel_store_apply_worker_ratio"`
	MaxChannelRPCQueueRatio          float64                            `json:"max_channel_rpc_queue_ratio"`
	MaxChannelRPCWorkerRatio         float64                            `json:"max_channel_rpc_worker_ratio"`
	ChannelRPCAdmissionFull          float64                            `json:"channel_rpc_admission_full"`
	ChannelRPCPullAdmissionFull      float64                            `json:"channel_rpc_pull_admission_full"`
	ChannelRPCHintAdmissionFull      float64                            `json:"channel_rpc_pull_hint_admission_full"`
	ChannelRPCPullPaced              float64                            `json:"channel_rpc_pull_paced"`
	ChannelRPCHintPaced              float64                            `json:"channel_rpc_pull_hint_paced"`
	ChannelRPCPullBatches            float64                            `json:"channel_rpc_pull_batches"`
	ChannelRPCPullBatchItems         float64                            `json:"channel_rpc_pull_batch_items"`
	ChannelRPCHintBatches            float64                            `json:"channel_rpc_pull_hint_batches"`
	ChannelRPCHintBatchItems         float64                            `json:"channel_rpc_pull_hint_batch_items"`
	ChannelPullOKEmpty               float64                            `json:"channel_pull_ok_empty"`
	ChannelPullOKRecords             float64                            `json:"channel_pull_ok_records"`
	ChannelPullError                 float64                            `json:"channel_pull_error"`
	ChannelAppendHintPaced           float64                            `json:"channel_rpc_append_hint_paced"`
	ChannelResumeHintPaced           float64                            `json:"channel_rpc_resume_hint_paced"`
	ChannelStoreApplyTasks           float64                            `json:"channel_store_apply_tasks"`
	ChannelStoreApplyFull            float64                            `json:"channel_store_apply_admission_full"`
	ChannelStoreApplyPullPaced       float64                            `json:"channel_store_apply_pull_paced"`
	ChannelStoreCheckpointTasks      float64                            `json:"channel_store_checkpoint_tasks"`
	ChannelStoreCheckpointFull       float64                            `json:"channel_store_checkpoint_admission_full"`
	TransportRPCRejected             float64                            `json:"transport_rpc_rejected"`
	PermissionSlotRPCCalls           float64                            `json:"permission_slot_rpc_calls"`
	PermissionSlotRPCErrors          float64                            `json:"permission_slot_rpc_errors"`
	PermissionSlotRPCAdmissionErrors float64                            `json:"permission_slot_rpc_admission_errors"`
	MaxTransportRPCQueueRatio        float64                            `json:"max_transport_rpc_queue_ratio"`
	MaxTransportRPCBusyRatio         float64                            `json:"max_transport_rpc_busy_ratio"`
	MaxPermissionSlotRPCQueueRatio   float64                            `json:"max_permission_slot_rpc_queue_ratio"`
	MaxPermissionSlotRPCInflight     float64                            `json:"max_permission_slot_rpc_inflight"`
	PermissionBatchStarted           float64                            `json:"permission_batch_started"`
	PermissionBatchPanics            float64                            `json:"permission_batch_panics"`
	MaxPermissionBatchActive         float64                            `json:"max_permission_batch_active"`
	MembershipMutationRows           float64                            `json:"membership_mutation_rows"`
	MaxAdvancePoolUtil               float64                            `json:"max_advance_pool_utilization"`
	MaxAdvancePoolWaiting            float64                            `json:"max_advance_pool_waiting"`
	MaxAppendPoolUtil                float64                            `json:"max_append_pool_utilization"`
	MaxPostCommitPoolUtil            float64                            `json:"max_post_commit_pool_utilization"`
	MaxPostCommitBacklog             float64                            `json:"max_post_commit_backlog"`
	MaxPostCommitHandoffRatio        float64                            `json:"max_post_commit_handoff_ratio"`
	MaxRouterGroupInflight           float64                            `json:"max_channel_append_router_group_inflight"`
	MaxRouterGroupCapacity           float64                            `json:"max_channel_append_router_group_capacity"`
	MaxRouterGroupRatio              float64                            `json:"max_channel_append_router_group_ratio"`
	MaxMessageCommitQueueDepth       float64                            `json:"max_message_commit_queue_depth"`
	MaxMessageMemTableBytes          float64                            `json:"max_message_memtable_bytes"`
	MaxMessageMemTableCount          float64                            `json:"max_message_memtable_count"`
	MaxMessageReadAmplification      float64                            `json:"max_message_read_amplification"`
	MaxMessageCompactionDebtBytes    float64                            `json:"max_message_compaction_debt_bytes"`
	MaxMessageCompactions            float64                            `json:"max_message_compactions_in_progress"`
	MaxMessageFlushes                float64                            `json:"max_message_flushes_in_progress"`
	MessageIdempotencyNegativeSkips  float64                            `json:"message_idempotency_negative_filter_skips"`
	MessageIdempotencyPointReads     float64                            `json:"message_idempotency_point_reads"`
	MessagePhysicalCommits           float64                            `json:"message_physical_commits"`
	MessageCommitBatchRequests       float64                            `json:"message_commit_batch_requests"`
	MessageCommitBatchRecords        float64                            `json:"message_commit_batch_records"`
	MessageCommitBatchBytes          float64                            `json:"message_commit_batch_bytes"`
	MessageCommitSeconds             float64                            `json:"message_commit_seconds"`
	MessageLeaderCommitRequests      float64                            `json:"message_leader_commit_requests"`
	MessageFollowerCommitRequests    float64                            `json:"message_follower_commit_requests"`
	MessageLeaderCommitSeconds       float64                            `json:"message_leader_commit_seconds"`
	MessageFollowerCommitSeconds     float64                            `json:"message_follower_commit_seconds"`
	MessageWALBytesIn                float64                            `json:"message_wal_bytes_in"`
	MessageWALBytesWritten           float64                            `json:"message_wal_bytes_written"`
	MessageFlushBytesWritten         float64                            `json:"message_flush_bytes_written"`
	MessageCompactionBytesRead       float64                            `json:"message_compaction_bytes_read"`
	MessageCompactionBytesWritten    float64                            `json:"message_compaction_bytes_written"`
	MessageSSTableSizeBytesDelta     float64                            `json:"message_sstable_size_bytes_delta"`
	RecipientProcessOK               float64                            `json:"recipient_worker_process_ok"`
	RecipientProcessRecipientsOK     float64                            `json:"recipient_worker_process_recipients_ok"`
	RecipientProcessError            float64                            `json:"recipient_worker_process_error"`
	ReceiverProgress                 []permissionSoakReceiverSnapshot   `json:"receiver_progress"`
	MaxHeapBytes                     float64                            `json:"max_heap_bytes"`
	MaxAggregateHeapBytes            float64                            `json:"max_aggregate_heap_bytes"`
	AllocatedBytes                   float64                            `json:"allocated_bytes"`
	GCCountDelta                     float64                            `json:"gc_count_delta"`
	MetricSamples                    int                                `json:"metric_samples"`
	MetricSampleErrors               int                                `json:"metric_sample_errors"`
	ProcessContinuous                bool                               `json:"process_continuous"`
	CounterCaptureError              string                             `json:"counter_capture_error,omitempty"`
}

type boundedLatencyHistogram struct {
	buckets     [10_001]atomic.Uint64
	count       atomic.Uint64
	maximumNano atomic.Int64
}

func newBoundedLatencyHistogram() *boundedLatencyHistogram {
	return &boundedLatencyHistogram{}
}

func (h *boundedLatencyHistogram) observe(latency time.Duration) {
	if latency < 0 {
		latency = 0
	}
	bucket := int((latency + time.Millisecond - 1) / time.Millisecond)
	if bucket >= len(h.buckets) {
		bucket = len(h.buckets) - 1
	}
	h.buckets[bucket].Add(1)
	h.count.Add(1)
	for {
		maximum := h.maximumNano.Load()
		if int64(latency) <= maximum || h.maximumNano.CompareAndSwap(maximum, int64(latency)) {
			break
		}
	}
}

func (h *boundedLatencyHistogram) percentile(quantile float64) time.Duration {
	count := h.count.Load()
	if count == 0 {
		return 0
	}
	target := uint64(math.Ceil(quantile * float64(count)))
	if target == 0 {
		target = 1
	}
	var observed uint64
	for bucket := range h.buckets {
		observed += h.buckets[bucket].Load()
		if observed >= target {
			return time.Duration(bucket) * time.Millisecond
		}
	}
	return mediumPermissionSoakMaxLatency
}

func (h *boundedLatencyHistogram) maximum() time.Duration {
	return time.Duration(h.maximumNano.Load())
}

type permissionSoakMessageStart struct {
	startedAt time.Time
	remaining atomic.Int32
}

type permissionSoakTracker struct {
	starts   sync.Map
	pending  atomic.Int64
	sendacks *boundedLatencyHistogram
	recvs    *boundedLatencyHistogram
}

type permissionSoakReceiverProgress struct {
	received     atomic.Uint64
	readTimeouts atomic.Uint64
}

type permissionSoakReceiverSnapshot struct {
	Index        int    `json:"index"`
	UID          string `json:"uid"`
	Expected     int    `json:"expected"`
	Received     uint64 `json:"received"`
	ReadTimeouts uint64 `json:"read_timeouts"`
}

func TestCloudMediumPermissionSoak(t *testing.T) {
	if os.Getenv("WK_E2E_MEDIUM_RECIPIENT_PERMISSION_SOAK") != "1" {
		t.Skip("set WK_E2E_MEDIUM_RECIPIENT_PERMISSION_SOAK=1 to run the bounded permission soak")
	}
	config, err := permissionSoakConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}

	cluster := startMediumCluster(t, mediumChannelRPCBatchMaxItems)
	verifyMediumRenderedRuntime(t, cluster, mediumChannelRPCBatchMaxItems)
	setupTimeout := 2 * time.Minute
	if config.groupChannels > 500 {
		setupTimeout = 5 * time.Minute
	}
	setupCtx, setupCancel := context.WithTimeout(context.Background(), setupTimeout)
	defer setupCancel()
	if err := cluster.WaitClusterReady(setupCtx); err != nil {
		t.Fatalf("wait for permission soak cluster: %v\n%s", err, cluster.DumpDiagnostics())
	}
	convergence, err := waitForMediumSlotConvergence(setupCtx, cluster)
	if err != nil {
		t.Fatalf("wait for stable actual Slot leaders before soak setup: %v\n%s", err, cluster.DumpDiagnostics())
	}
	t.Logf(
		"WKRC-PERMISSION-SOAK-CONVERGENCE wait=%s stable=%s leaders=%v",
		convergence.WaitDuration,
		convergence.StableDuration,
		convergence.Leaders,
	)
	channels := preparePermissionSoakChannels(t, setupCtx, cluster, config.groupChannels, convergence.HashSlotLeaders)
	primeMessages := make([]hotPathMessage, len(channels))
	for index, channelID := range channels {
		primeMessages[index] = hotPathMessage{
			clientSeq:    uint64(index + 1),
			clientMsgNo:  fmt.Sprintf("wkrc-permission-soak-prime-%05d", index+1),
			channelID:    channelID,
			channelType:  frame.ChannelTypeGroup,
			groupProfile: -1,
			primeSender:  index % mediumSenderConnections,
		}
	}
	primeHotPathChannels(t, setupCtx, cluster, primeMessages, []byte("prime"))

	senders := connectSenders(t, cluster)
	defer closeClients(senders)
	recipients := connectPermissionSoakRecipients(t, cluster)
	defer closeClients(recipients)
	if err := waitForRecipientPresence(setupCtx, cluster, len(recipients)+len(senders)); err != nil {
		t.Fatalf("wait for permission soak presence convergence: %v\n%s", err, cluster.DumpDiagnostics())
	}

	messageCount := int(config.duration.Seconds() * float64(config.offeredQPS))
	senderCounts := make([]int, len(senders))
	receiverCounts := make([]int, len(recipients))
	for index := 0; index < messageCount; index++ {
		clientIndex := index % mediumSenderConnections
		senderCounts[clientIndex]++
		receiverCounts[clientIndex]++
	}
	tracker := newPermissionSoakTracker()
	receiverProgress := newPermissionSoakReceiverProgress(len(recipients))
	receiverDeadline := time.Now().Add(config.duration + mediumPermissionSoakDrainAllowance)
	heartbeatClients := make([]*suite.WKProtoClient, 0, len(senders)+len(recipients))
	heartbeatClients = append(heartbeatClients, senders...)
	heartbeatClients = append(heartbeatClients, recipients...)
	heartbeatCtx, heartbeatCancel := context.WithCancel(context.Background())
	defer heartbeatCancel()
	heartbeatResults := startPermissionSoakHeartbeats(
		heartbeatCtx,
		heartbeatClients,
		mediumPermissionSoakHeartbeat,
	)
	senderResults := startPermissionSoakSenderReaders(senders, senderCounts, tracker)
	receiverResults := startPermissionSoakReceiverReaders(
		recipients,
		receiverCounts,
		receiverDeadline,
		tracker,
		receiverProgress,
	)
	sampler := newPressureSampler(cluster, mediumMetricSampleInterval)
	sampler.start()
	defer sampler.stop()
	counterStart := mustCaptureHotPathCounters(t, cluster)
	stageLatencyStart := mustCapturePermissionSoakStageLatencySnapshot(t, cluster)
	profileDir := os.Getenv("WK_E2E_MEDIUM_RECIPIENT_PROFILE_DIR")
	profileDone := startPermissionSoakProfiles(cluster, profileDir, config.duration)
	duringLoadDiagnostics := startPermissionSoakDuringLoadDiagnostics(cluster, profileDir, config.duration)
	payload := bytes.Repeat([]byte("s"), mediumPayloadBytes)

	measuredStart := time.Now()
	for index := 0; index < messageCount; index++ {
		if index < messageCount-len(senders) {
			for _, source := range []struct {
				phase   string
				results <-chan error
			}{
				{phase: "sender_read", results: senderResults},
				{phase: "receiver_read", results: receiverResults},
				{phase: "heartbeat", results: heartbeatResults},
			} {
				failure, ready := pollPermissionSoakResult(source.results)
				if !ready {
					continue
				}
				if failure == nil {
					failure = fmt.Errorf("%s completed before all measured messages were sent", source.phase)
				}
				logPermissionSoakFailureEvidence(
					t,
					cluster,
					tracker,
					sampler,
					counterStart,
					stageLatencyStart,
					receiverCounts,
					receiverProgress,
					source.phase,
					index,
					time.Since(measuredStart),
					failure,
				)
				logPermissionSoakFailureRuntimeDiagnostics(t, cluster, profileDir, duringLoadDiagnostics)
				t.Fatalf("permission soak %s: %v\n%s", source.phase, failure, cluster.DumpDiagnostics())
			}
		}
		paceMessage(measuredStart, index, config.offeredQPS)
		clientIndex := index % mediumSenderConnections
		clientMsgNo := fmt.Sprintf("wkrc-permission-soak-%09d", index+1)
		tracker.begin(clientMsgNo, 1)
		if err := senders[clientIndex].SendFrame(&frame.SendPacket{
			ChannelID:   channels[index%len(channels)],
			ChannelType: frame.ChannelTypeGroup,
			ClientSeq:   uint64(index + 1),
			ClientMsgNo: clientMsgNo,
			Payload:     payload,
		}); err != nil {
			logPermissionSoakFailureEvidence(
				t,
				cluster,
				tracker,
				sampler,
				counterStart,
				stageLatencyStart,
				receiverCounts,
				receiverProgress,
				"send",
				index,
				time.Since(measuredStart),
				err,
			)
			logPermissionSoakFailureRuntimeDiagnostics(t, cluster, profileDir, duringLoadDiagnostics)
			t.Fatalf("permission soak send %s: %v\n%s", clientMsgNo, err, cluster.DumpDiagnostics())
		}
		if index > 0 && index%(config.offeredQPS*60) == 0 {
			commitDelta, commitErr := capturePermissionSoakCommitDelta(cluster, counterStart)
			t.Logf(
				"WKRC-PERMISSION-SOAK-MINUTE elapsed=%s pending=%d sendacks=%d recvs=%d recipients=%v delivery=%+v pressure=%+v channel_rpc_admission=%+v commits=%+v commit_error=%v",
				time.Since(measuredStart).Round(time.Second),
				tracker.pending.Load(),
				tracker.sendacks.count.Load(),
				tracker.recvs.count.Load(),
				permissionSoakReceiverSnapshots(receiverCounts, receiverProgress),
				commitDelta.recipientProcessSummary(),
				sampler.snapshot(),
				commitDelta.channelRPCAdmissionSummary(),
				commitDelta.messageCommitSummary(),
				commitErr,
			)
		}
	}
	sendLoopDuration := time.Since(measuredStart)

	for range senders {
		if err := <-senderResults; err != nil {
			logPermissionSoakFailureEvidence(
				t, cluster, tracker, sampler, counterStart, stageLatencyStart, receiverCounts,
				receiverProgress, "sender_read", messageCount, sendLoopDuration, err,
			)
			logPermissionSoakFailureRuntimeDiagnostics(t, cluster, profileDir, duringLoadDiagnostics)
			t.Fatalf("permission soak sender read: %v\n%s", err, cluster.DumpDiagnostics())
		}
	}
	for range recipients {
		if err := <-receiverResults; err != nil {
			logPermissionSoakFailureEvidence(
				t, cluster, tracker, sampler, counterStart, stageLatencyStart, receiverCounts,
				receiverProgress, "receiver_read", messageCount, sendLoopDuration, err,
			)
			logPermissionSoakFailureRuntimeDiagnostics(t, cluster, profileDir, duringLoadDiagnostics)
			t.Fatalf("permission soak receiver read: %v\n%s", err, cluster.DumpDiagnostics())
		}
	}
	heartbeatCancel()
	for range heartbeatClients {
		if err := <-heartbeatResults; err != nil {
			t.Fatalf("permission soak heartbeat: %v\n%s", err, cluster.DumpDiagnostics())
		}
	}
	measuredDuration := time.Since(measuredStart)
	stageP99MS := permissionSoakStageLatencyEvidence{}
	stageP999MS := permissionSoakStageLatencyEvidence{}
	gatewayBatchRecordsP99 := float64(0)
	stageLatencyCaptureError := ""
	stageLatencyCtx, stageLatencyCancel := context.WithTimeout(context.Background(), 3*time.Second)
	stageLatencyEnd, stageLatencyErr := capturePermissionSoakStageLatencySnapshot(stageLatencyCtx, cluster)
	stageLatencyCancel()
	if stageLatencyErr != nil {
		stageLatencyCaptureError = stageLatencyErr.Error()
	} else {
		stageP99MS = permissionSoakStageLatencyFromSnapshots(stageLatencyStart, stageLatencyEnd)
		stageP999MS = permissionSoakStageTailLatencyFromSnapshots(stageLatencyStart, stageLatencyEnd)
		gatewayBatchRecordsP99 = permissionSoakGatewayBatchRecordsP99FromSnapshots(stageLatencyStart, stageLatencyEnd)
	}
	if err := <-profileDone; err != nil {
		t.Fatalf("capture permission soak profiles: %v\n%s", err, cluster.DumpDiagnostics())
	}
	duringLoadGoroutines := <-duringLoadDiagnostics

	drainCtx, drainCancel := context.WithTimeout(context.Background(), 20*time.Second)
	drainErr := waitForHotPathDrain(drainCtx, cluster)
	drainCancel()
	if drainErr != nil {
		t.Fatalf("permission soak hot path did not drain: %v\n%s", drainErr, cluster.DumpDiagnostics())
	}
	pluginCtx, pluginCancel := context.WithTimeout(context.Background(), 20*time.Second)
	counterEnd, pluginErr := waitForPluginReceiveDrain(
		pluginCtx,
		cluster,
		counterStart,
		0,
	)
	pluginCancel()
	if pluginErr != nil {
		t.Fatalf("permission soak plugin path did not drain: %v\n%s", pluginErr, cluster.DumpDiagnostics())
	}
	sampler.stop()
	counterDelta := counterEnd.subtract(counterStart)
	pressure := sampler.snapshot()

	processContinuous := true
	for _, node := range cluster.Nodes {
		processContinuous = processContinuous && node.Process.Running()
	}
	evidence := permissionSoakEvidence{
		Schema:                           mediumPermissionSoakEvidenceSchema,
		ConfiguredDurationMS:             milliseconds(config.duration),
		SendLoopDurationMS:               milliseconds(sendLoopDuration),
		MeasuredDurationMS:               milliseconds(measuredDuration),
		Messages:                         messageCount,
		GroupChannels:                    len(channels),
		ActiveGroupChannels:              min(len(channels), messageCount),
		Senders:                          len(senders),
		Recipients:                       len(recipients),
		OfferedQPS:                       config.offeredQPS,
		IngressPerSecond:                 float64(messageCount) / sendLoopDuration.Seconds(),
		CompletionPerSecond:              float64(messageCount) / measuredDuration.Seconds(),
		SendackP50MS:                     milliseconds(tracker.sendacks.percentile(0.50)),
		SendackP99MS:                     milliseconds(tracker.sendacks.percentile(0.99)),
		SendackMaxMS:                     milliseconds(tracker.sendacks.maximum()),
		RecvP99MS:                        milliseconds(tracker.recvs.percentile(0.99)),
		RecvMaxMS:                        milliseconds(tracker.recvs.maximum()),
		StageP99MS:                       stageP99MS,
		StageP999MS:                      stageP999MS,
		GatewayBatchRecordsP99:           gatewayBatchRecordsP99,
		StageLatencyCaptureError:         stageLatencyCaptureError,
		MaxGatewayQueueRatio:             pressure.maxGatewayQueueRatio,
		MaxRecipientQueueRatio:           pressure.maxRecipientQueueRatio,
		MaxRecipientWorkerRatio:          pressure.maxRecipientWorkerRatio,
		MaxChannelReactorMailboxRatio:    pressure.maxChannelReactorMailboxRatio,
		MaxChannelStoreAppendQueueRatio:  pressure.maxChannelStoreAppendQueueRatio,
		MaxChannelStoreAppendWorkerRatio: pressure.maxChannelStoreAppendWorkerRatio,
		MaxChannelStoreApplyQueueRatio:   pressure.maxChannelStoreApplyQueueRatio,
		MaxChannelStoreApplyWorkerRatio:  pressure.maxChannelStoreApplyWorkerRatio,
		MaxChannelRPCQueueRatio:          pressure.maxChannelRPCQueueRatio,
		MaxChannelRPCWorkerRatio:         pressure.maxChannelRPCWorkerRatio,
		ChannelRPCAdmissionFull:          counterDelta.channelRPCAdmissionFull,
		ChannelRPCPullAdmissionFull:      counterDelta.channelRPCPullAdmissionFull,
		ChannelRPCHintAdmissionFull:      counterDelta.channelRPCHintAdmissionFull,
		ChannelRPCPullPaced:              counterDelta.channelRPCPullPaced,
		ChannelRPCHintPaced:              counterDelta.channelRPCHintPaced,
		ChannelRPCPullBatches:            counterDelta.channelRPCPullBatches,
		ChannelRPCPullBatchItems:         counterDelta.channelRPCPullBatchItems,
		ChannelRPCHintBatches:            counterDelta.channelRPCHintBatches,
		ChannelRPCHintBatchItems:         counterDelta.channelRPCHintBatchItems,
		ChannelPullOKEmpty:               counterDelta.channelPullOKEmpty,
		ChannelPullOKRecords:             counterDelta.channelPullOKRecords,
		ChannelPullError:                 counterDelta.channelPullError,
		ChannelAppendHintPaced:           counterDelta.channelAppendHintPaced,
		ChannelResumeHintPaced:           counterDelta.channelResumeHintPaced,
		ChannelStoreApplyTasks:           counterDelta.channelStoreApplyTasks,
		ChannelStoreApplyFull:            counterDelta.channelStoreApplyAdmissionFull,
		ChannelStoreApplyPullPaced:       counterDelta.channelStoreApplyPullPaced,
		ChannelStoreCheckpointTasks:      counterDelta.channelStoreCheckpointTasks,
		ChannelStoreCheckpointFull:       counterDelta.channelStoreCheckpointAdmissionFull,
		TransportRPCMetricNodes:          pressure.maxTransportRPCMetricNodes,
		MaxTransportRPCQueueRatio:        pressure.maxTransportRPCQueueRatio,
		MaxTransportRPCBusyRatio:         pressure.maxTransportRPCBusyRatio,
		TransportRPCRejected:             counterDelta.transportRPCRejected,
		PermissionSlotRPCCalls:           counterDelta.permissionSlotRPCCalls,
		PermissionSlotRPCErrors:          counterDelta.permissionSlotRPCErrors,
		PermissionSlotRPCAdmissionErrors: counterDelta.permissionSlotRPCAdmissionErrors,
		MaxPermissionSlotRPCQueueRatio:   pressure.maxPermissionSlotRPCQueueRatio,
		MaxPermissionSlotRPCInflight:     pressure.maxPermissionSlotRPCInflight,
		PermissionBatchStarted:           counterDelta.permissionBatchStarted,
		PermissionBatchPanics:            counterDelta.permissionBatchPanics,
		MaxPermissionBatchActive:         pressure.maxPermissionBatchActive,
		MembershipMutationRows:           counterDelta.membershipMutationRows,
		MaxAdvancePoolUtil:               pressure.maxAdvancePoolUtil,
		MaxAdvancePoolWaiting:            pressure.maxAdvancePoolWaiting,
		MaxAppendPoolUtil:                pressure.maxAppendPoolUtil,
		MaxPostCommitPoolUtil:            pressure.maxPostCommitPoolUtil,
		MaxPostCommitBacklog:             pressure.maxPostCommitBacklog,
		MaxPostCommitHandoffRatio:        pressure.maxPostCommitHandoffRatio,
		MaxRouterGroupInflight:           pressure.maxRouterGroupInflight,
		MaxRouterGroupCapacity:           pressure.maxRouterGroupCapacity,
		MaxRouterGroupRatio:              pressure.maxRouterGroupRatio,
		MaxMessageCommitQueueDepth:       pressure.maxMessageCommitQueueDepth,
		MaxMessageMemTableBytes:          pressure.maxMessageMemTableBytes,
		MaxMessageMemTableCount:          pressure.maxMessageMemTableCount,
		MaxMessageReadAmplification:      pressure.maxMessageReadAmplification,
		MaxMessageCompactionDebtBytes:    pressure.maxMessageCompactionDebtBytes,
		MaxMessageCompactions:            pressure.maxMessageCompactions,
		MaxMessageFlushes:                pressure.maxMessageFlushes,
		MessageIdempotencyNegativeSkips:  counterDelta.messageIdempotencyNegativeSkips,
		MessageIdempotencyPointReads:     counterDelta.messageIdempotencyPointReads,
		MessagePhysicalCommits:           counterDelta.messagePhysicalCommits,
		MessageCommitBatchRequests:       counterDelta.messageCommitBatchRequests,
		MessageCommitBatchRecords:        counterDelta.messageCommitBatchRecords,
		MessageCommitBatchBytes:          counterDelta.messageCommitBatchBytes,
		MessageCommitSeconds:             counterDelta.messageCommitSeconds,
		MessageLeaderCommitRequests:      counterDelta.messageLeaderCommitRequests,
		MessageFollowerCommitRequests:    counterDelta.messageFollowerCommitRequests,
		MessageLeaderCommitSeconds:       counterDelta.messageLeaderCommitSeconds,
		MessageFollowerCommitSeconds:     counterDelta.messageFollowerCommitSeconds,
		MessageWALBytesIn:                counterDelta.messageWALBytesIn,
		MessageWALBytesWritten:           counterDelta.messageWALBytesWritten,
		MessageFlushBytesWritten:         counterDelta.messageFlushBytesWritten,
		MessageCompactionBytesRead:       counterDelta.messageCompactionBytesRead,
		MessageCompactionBytesWritten:    counterDelta.messageCompactionBytesWritten,
		MessageSSTableSizeBytesDelta:     counterDelta.messageSSTableSizeBytes,
		RecipientProcessOK:               counterDelta.recipientProcessOK,
		RecipientProcessRecipientsOK:     counterDelta.recipientProcessRecipientsOK,
		RecipientProcessError:            counterDelta.recipientProcessError,
		ReceiverProgress:                 permissionSoakReceiverSnapshots(receiverCounts, receiverProgress),
		PluginReceiveAccepted:            counterDelta.pluginReceiveAccepted,
		PluginReceiveFull:                counterDelta.pluginReceiveFull,
		PluginReceiveClosed:              counterDelta.pluginReceiveClosed,
		PluginReceiveInvokeOK:            counterDelta.pluginReceiveInvokeOK,
		PluginReceiveInvokeError:         counterDelta.pluginReceiveInvokeError,
		MaxHeapBytes:                     pressure.maxHeapBytes,
		MaxAggregateHeapBytes:            pressure.maxAggregateHeapBytes,
		AllocatedBytes:                   counterDelta.allocatedBytes,
		GCCountDelta:                     counterDelta.gcCount,
		MetricSamples:                    pressure.samples,
		MetricSampleErrors:               pressure.sampleErrors,
		PendingMessages:                  tracker.pending.Load(),
		Drained:                          drainErr == nil,
		ProcessContinuous:                processContinuous,
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatalf("marshal permission soak evidence: %v", err)
	}
	t.Logf("WKRC-PERMISSION-SOAK-EVIDENCE %s", encoded)
	if evidence.PermissionSlotRPCCalls == 0 {
		t.Logf("WKRC-PERMISSION-SOAK-SLOT-RPC-DIAGNOSTICS %s", permissionSlotRPCMetricDiagnostics(cluster))
	}
	if err := permissionSoakAcceptanceError(evidence, config); err != nil {
		t.Logf("WKRC-PERMISSION-SOAK-ACCEPTANCE-RUNTIME %s", hotPathRuntimeDiagnostics(cluster))
		if duringLoadGoroutines != "" {
			t.Logf("WKRC-PERMISSION-SOAK-DURING-LOAD-GOROUTINES\n%s", duringLoadGoroutines)
		}
		t.Fatal(err)
	}
}

func startPermissionSoakProfiles(cluster *suite.StartedCluster, profileDir string, duration time.Duration) <-chan error {
	done := make(chan error, 1)
	if strings.TrimSpace(profileDir) == "" {
		done <- nil
		return done
	}
	go func() {
		timer := time.NewTimer(permissionSoakDiagnosticDelay(duration))
		defer timer.Stop()
		<-timer.C
		done <- <-startHotPathProfiles(cluster, profileDir)
	}()
	return done
}

func startPermissionSoakDuringLoadDiagnostics(cluster *suite.StartedCluster, profileDir string, duration time.Duration) <-chan string {
	done := make(chan string, 1)
	if strings.TrimSpace(profileDir) == "" {
		done <- ""
		return done
	}
	go func() {
		timer := time.NewTimer(permissionSoakDiagnosticDelay(duration))
		defer timer.Stop()
		<-timer.C
		captured := hotPathBottleneckGoroutineDiagnostics(cluster)
		_ = writePermissionSoakDiagnosticArtifact(profileDir, "scheduled-goroutines.txt", captured)
		done <- captured
	}()
	return done
}

func permissionSoakDiagnosticDelay(duration time.Duration) time.Duration {
	if duration >= 8*time.Minute {
		return 7 * time.Minute
	}
	if duration >= 5*time.Minute {
		return 4 * time.Minute
	}
	if duration >= 2*time.Minute {
		return 90 * time.Second
	}
	return 5 * time.Second
}

func mustCapturePermissionSoakStageLatencySnapshot(
	t *testing.T,
	cluster *suite.StartedCluster,
) benchmetrics.PrometheusSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	snapshot, err := capturePermissionSoakStageLatencySnapshot(ctx, cluster)
	if err != nil {
		t.Fatalf("capture permission soak stage latency: %v", err)
	}
	return snapshot
}

func capturePermissionSoakStageLatencySnapshot(
	ctx context.Context,
	cluster *suite.StartedCluster,
) (benchmetrics.PrometheusSnapshot, error) {
	snapshot := benchmetrics.PrometheusSnapshot{}
	for _, node := range cluster.Nodes {
		samples, err := suite.FetchMetricSamples(ctx, node.APIAddr())
		if err != nil {
			return benchmetrics.PrometheusSnapshot{}, fmt.Errorf("node %d metrics: %w", node.Spec.ID, err)
		}
		for _, sample := range samples {
			if !isPermissionSoakStageLatencyBucket(sample.Name) {
				continue
			}
			snapshot.Samples = append(snapshot.Samples, benchmetrics.PrometheusSample{
				Name: sample.Name, Labels: sample.Labels, Value: sample.Value,
			})
		}
	}
	return snapshot, nil
}

func isPermissionSoakStageLatencyBucket(name string) bool {
	switch name {
	case "wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket",
		"wukongim_gateway_async_send_batch_records_bucket",
		"wukongim_gateway_frame_handle_duration_seconds_bucket",
		"wukongim_channelappend_router_duration_seconds_bucket",
		"wukongim_channelappend_router_item_duration_seconds_bucket",
		"wukongim_message_send_batch_stage_item_duration_seconds_bucket",
		"wukongim_channelv2_append_wait_stage_duration_seconds_bucket",
		"wukongim_channelv2_leader_pull_stage_duration_seconds_bucket",
		"wukongim_channel_append_wait_stage_duration_seconds_bucket",
		"wukongim_storage_commit_request_duration_seconds_bucket",
		"wukongim_storage_commit_batch_duration_seconds_bucket":
		return true
	default:
		return false
	}
}

func permissionSoakStageLatencyFromSnapshots(
	before benchmetrics.PrometheusSnapshot,
	after benchmetrics.PrometheusSnapshot,
) permissionSoakStageLatencyEvidence {
	report := benchmetrics.AnalyzeWukongIMPrometheus(before, after)
	return permissionSoakStageLatencyEvidence{
		GatewayDispatchWaitP99MS:            report.GatewayDispatchWaitP99Seconds * 1000,
		GatewaySendHandleP99MS:              report.GatewaySendHandleP99Seconds * 1000,
		ChannelAppendRouterLocalP99MS:       report.ChannelAppendRouterLocalP99Seconds * 1000,
		ChannelAppendRouterRemoteP99MS:      report.ChannelAppendRouterRemoteP99Seconds * 1000,
		ChannelAppendRouterBatchP99MS:       report.ChannelAppendRouterBatchP99Seconds * 1000,
		ChannelAppendRouterBatchItemP99MS:   report.ChannelAppendRouterBatchItemP99Seconds * 1000,
		MessagePermissionP99MS:              report.MessageSendBatchPermissionP99Seconds * 1000,
		MessagePreAppendP99MS:               report.MessageSendBatchPreAppendP99Seconds * 1000,
		MessageSubmitterP99MS:               report.MessageSendBatchSubmitterP99Seconds * 1000,
		ChannelStoreAppendWaitP99MS:         report.ChannelRuntimeAppendStoreWaitP99Seconds * 1000,
		ChannelPostStoreCommitWaitP99MS:     report.ChannelRuntimeAppendPostStoreCommitWaitP99Seconds * 1000,
		ChannelQuorumFollowerPullWaitP99MS:  report.ChannelRuntimeAppendQuorumFollowerPullWaitP99Seconds * 1000,
		ChannelQuorumAckOffsetWaitP99MS:     report.ChannelRuntimeAppendQuorumAckOffsetWaitP99Seconds * 1000,
		ChannelQuorumHWAdvanceWaitP99MS:     report.ChannelRuntimeAppendQuorumHWAdvanceWaitP99Seconds * 1000,
		ChannelQuorumFinalCompleteWaitP99MS: report.ChannelRuntimeAppendQuorumFinalCompleteP99Seconds * 1000,
		StorageLeaderCommitRequestP99MS:     report.StorageCommitRequestP99SecondsByLane["leader_append"] * 1000,
		StorageFollowerCommitRequestP99MS:   report.StorageCommitRequestP99SecondsByLane["follower_apply"] * 1000,
		StoragePhysicalCommitP99MS:          report.StorageCommitP99Seconds * 1000,
		ChannelLeaderPullMailboxWaitP99MS:   report.ChannelRuntimeLeaderPullMailboxWaitP99Seconds * 1000,
		ChannelLeaderPullAckApplyP99MS:      report.ChannelRuntimeLeaderPullAckApplyP99Seconds * 1000,
		ChannelLeaderPullHandlerP99MS:       report.ChannelRuntimeLeaderPullHandlerP99Seconds * 1000,
	}
}

func permissionSoakGatewayBatchRecordsP99FromSnapshots(
	before benchmetrics.PrometheusSnapshot,
	after benchmetrics.PrometheusSnapshot,
) float64 {
	report := benchmetrics.AnalyzeWukongIMPrometheus(before, after)
	return report.GatewayBatchRecordsP99
}

func permissionSoakStageTailLatencyFromSnapshots(
	before benchmetrics.PrometheusSnapshot,
	after benchmetrics.PrometheusSnapshot,
) permissionSoakStageLatencyEvidence {
	report := benchmetrics.AnalyzeWukongIMPrometheus(before, after)
	return permissionSoakStageLatencyEvidence{
		GatewayDispatchWaitP99MS:            report.GatewayDispatchWaitP999Seconds * 1000,
		GatewaySendHandleP99MS:              report.GatewaySendHandleP999Seconds * 1000,
		ChannelAppendRouterLocalP99MS:       report.ChannelAppendRouterLocalP999Seconds * 1000,
		ChannelAppendRouterRemoteP99MS:      report.ChannelAppendRouterRemoteP999Seconds * 1000,
		ChannelAppendRouterBatchP99MS:       report.ChannelAppendRouterBatchP999Seconds * 1000,
		ChannelAppendRouterBatchItemP99MS:   report.ChannelAppendRouterBatchItemP999Seconds * 1000,
		MessagePermissionP99MS:              report.MessageSendBatchPermissionP999Seconds * 1000,
		MessagePreAppendP99MS:               report.MessageSendBatchPreAppendP999Seconds * 1000,
		MessageSubmitterP99MS:               report.MessageSendBatchSubmitterP999Seconds * 1000,
		ChannelStoreAppendWaitP99MS:         report.ChannelRuntimeAppendStoreWaitP999Seconds * 1000,
		ChannelPostStoreCommitWaitP99MS:     report.ChannelRuntimeAppendPostStoreCommitWaitP999Seconds * 1000,
		ChannelQuorumFollowerPullWaitP99MS:  report.ChannelRuntimeAppendQuorumFollowerPullWaitP999Seconds * 1000,
		ChannelQuorumAckOffsetWaitP99MS:     report.ChannelRuntimeAppendQuorumAckOffsetWaitP999Seconds * 1000,
		ChannelQuorumHWAdvanceWaitP99MS:     report.ChannelRuntimeAppendQuorumHWAdvanceWaitP999Seconds * 1000,
		ChannelQuorumFinalCompleteWaitP99MS: report.ChannelRuntimeAppendQuorumFinalCompleteP999Seconds * 1000,
		StorageLeaderCommitRequestP99MS:     report.StorageCommitRequestP999SecondsByLane["leader_append"] * 1000,
		StorageFollowerCommitRequestP99MS:   report.StorageCommitRequestP999SecondsByLane["follower_apply"] * 1000,
		StoragePhysicalCommitP99MS:          report.StorageCommitP999Seconds * 1000,
		ChannelLeaderPullMailboxWaitP99MS:   report.ChannelRuntimeLeaderPullMailboxWaitP999Seconds * 1000,
		ChannelLeaderPullAckApplyP99MS:      report.ChannelRuntimeLeaderPullAckApplyP999Seconds * 1000,
		ChannelLeaderPullHandlerP99MS:       report.ChannelRuntimeLeaderPullHandlerP999Seconds * 1000,
	}
}

func pollPermissionSoakResult(results <-chan error) (error, bool) {
	select {
	case result := <-results:
		return result, true
	default:
		return nil, false
	}
}

func permissionSoakFailureDiagnostics(
	scheduled <-chan string,
	live func() string,
) (scheduledSnapshot string, liveSnapshot string) {
	select {
	case scheduledSnapshot = <-scheduled:
	default:
	}
	return scheduledSnapshot, live()
}

func logPermissionSoakFailureRuntimeDiagnostics(
	t *testing.T,
	cluster *suite.StartedCluster,
	profileDir string,
	duringLoadDiagnostics <-chan string,
) {
	t.Helper()
	t.Logf("WKRC-PERMISSION-SOAK-FAILURE-RUNTIME %s", hotPathRuntimeDiagnostics(cluster))
	scheduled, live := permissionSoakFailureDiagnostics(
		duringLoadDiagnostics,
		func() string { return hotPathBottleneckGoroutineDiagnostics(cluster) },
	)
	if scheduled != "" {
		t.Logf("WKRC-PERMISSION-SOAK-SCHEDULED-GOROUTINES\n%s", scheduled)
	}
	if live != "" {
		if err := writePermissionSoakDiagnosticArtifact(profileDir, "failure-goroutines.txt", live); err != nil {
			t.Logf("WKRC-PERMISSION-SOAK-FAILURE-GOROUTINES-WRITE error=%v", err)
		}
		t.Logf("WKRC-PERMISSION-SOAK-FAILURE-GOROUTINES\n%s", live)
	}
}

func writePermissionSoakDiagnosticArtifact(dir, name, content string) error {
	if strings.TrimSpace(dir) == "" || strings.TrimSpace(name) == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}

func logPermissionSoakFailureEvidence(
	t *testing.T,
	cluster *suite.StartedCluster,
	tracker *permissionSoakTracker,
	sampler *pressureSampler,
	counterStart hotPathCounters,
	stageLatencyStart benchmetrics.PrometheusSnapshot,
	receiverCounts []int,
	receiverProgress []*permissionSoakReceiverProgress,
	phase string,
	completedSendCalls int,
	elapsed time.Duration,
	failure error,
) {
	t.Helper()
	pressure := sampler.snapshot()
	counterDelta := hotPathCounters{}
	counterCaptureError := ""
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	counterEnd, err := captureHotPathCounters(ctx, cluster)
	cancel()
	if err != nil {
		counterCaptureError = err.Error()
	} else {
		counterDelta = counterEnd.subtract(counterStart)
	}
	stageP99MS := permissionSoakStageLatencyEvidence{}
	stageP999MS := permissionSoakStageLatencyEvidence{}
	gatewayBatchRecordsP99 := float64(0)
	stageLatencyCaptureError := ""
	stageLatencyCtx, stageLatencyCancel := context.WithTimeout(context.Background(), 3*time.Second)
	stageLatencyEnd, stageLatencyErr := capturePermissionSoakStageLatencySnapshot(stageLatencyCtx, cluster)
	stageLatencyCancel()
	if stageLatencyErr != nil {
		stageLatencyCaptureError = stageLatencyErr.Error()
	} else {
		stageP99MS = permissionSoakStageLatencyFromSnapshots(stageLatencyStart, stageLatencyEnd)
		stageP999MS = permissionSoakStageTailLatencyFromSnapshots(stageLatencyStart, stageLatencyEnd)
		gatewayBatchRecordsP99 = permissionSoakGatewayBatchRecordsP99FromSnapshots(stageLatencyStart, stageLatencyEnd)
	}
	processContinuous := true
	for _, node := range cluster.Nodes {
		processContinuous = processContinuous && node.Process.Running()
	}
	ingressPerSecond := float64(0)
	if elapsed > 0 {
		ingressPerSecond = float64(completedSendCalls) / elapsed.Seconds()
	}
	evidence := permissionSoakFailureEvidence{
		Schema:                           mediumPermissionSoakFailureSchema,
		Phase:                            phase,
		Error:                            failure.Error(),
		CompletedSendCalls:               completedSendCalls,
		ElapsedMS:                        milliseconds(elapsed),
		IngressPerSecond:                 ingressPerSecond,
		SendackP99MS:                     milliseconds(tracker.sendacks.percentile(0.99)),
		RecvP99MS:                        milliseconds(tracker.recvs.percentile(0.99)),
		StageP99MS:                       stageP99MS,
		StageP999MS:                      stageP999MS,
		GatewayBatchRecordsP99:           gatewayBatchRecordsP99,
		StageLatencyCaptureError:         stageLatencyCaptureError,
		PendingMessages:                  tracker.pending.Load(),
		MaxGatewayQueueRatio:             pressure.maxGatewayQueueRatio,
		MaxRecipientQueueRatio:           pressure.maxRecipientQueueRatio,
		MaxRecipientWorkerRatio:          pressure.maxRecipientWorkerRatio,
		MaxChannelReactorMailboxRatio:    pressure.maxChannelReactorMailboxRatio,
		MaxChannelStoreAppendQueueRatio:  pressure.maxChannelStoreAppendQueueRatio,
		MaxChannelStoreAppendWorkerRatio: pressure.maxChannelStoreAppendWorkerRatio,
		MaxChannelStoreApplyQueueRatio:   pressure.maxChannelStoreApplyQueueRatio,
		MaxChannelStoreApplyWorkerRatio:  pressure.maxChannelStoreApplyWorkerRatio,
		MaxChannelRPCQueueRatio:          pressure.maxChannelRPCQueueRatio,
		MaxChannelRPCWorkerRatio:         pressure.maxChannelRPCWorkerRatio,
		ChannelRPCAdmissionFull:          counterDelta.channelRPCAdmissionFull,
		ChannelRPCPullAdmissionFull:      counterDelta.channelRPCPullAdmissionFull,
		ChannelRPCHintAdmissionFull:      counterDelta.channelRPCHintAdmissionFull,
		ChannelRPCPullPaced:              counterDelta.channelRPCPullPaced,
		ChannelRPCHintPaced:              counterDelta.channelRPCHintPaced,
		ChannelRPCPullBatches:            counterDelta.channelRPCPullBatches,
		ChannelRPCPullBatchItems:         counterDelta.channelRPCPullBatchItems,
		ChannelRPCHintBatches:            counterDelta.channelRPCHintBatches,
		ChannelRPCHintBatchItems:         counterDelta.channelRPCHintBatchItems,
		ChannelPullOKEmpty:               counterDelta.channelPullOKEmpty,
		ChannelPullOKRecords:             counterDelta.channelPullOKRecords,
		ChannelPullError:                 counterDelta.channelPullError,
		ChannelAppendHintPaced:           counterDelta.channelAppendHintPaced,
		ChannelResumeHintPaced:           counterDelta.channelResumeHintPaced,
		ChannelStoreApplyTasks:           counterDelta.channelStoreApplyTasks,
		ChannelStoreApplyFull:            counterDelta.channelStoreApplyAdmissionFull,
		ChannelStoreApplyPullPaced:       counterDelta.channelStoreApplyPullPaced,
		ChannelStoreCheckpointTasks:      counterDelta.channelStoreCheckpointTasks,
		ChannelStoreCheckpointFull:       counterDelta.channelStoreCheckpointAdmissionFull,
		TransportRPCRejected:             counterDelta.transportRPCRejected,
		PermissionSlotRPCCalls:           counterDelta.permissionSlotRPCCalls,
		PermissionSlotRPCErrors:          counterDelta.permissionSlotRPCErrors,
		PermissionSlotRPCAdmissionErrors: counterDelta.permissionSlotRPCAdmissionErrors,
		MaxTransportRPCQueueRatio:        pressure.maxTransportRPCQueueRatio,
		MaxTransportRPCBusyRatio:         pressure.maxTransportRPCBusyRatio,
		MaxPermissionSlotRPCQueueRatio:   pressure.maxPermissionSlotRPCQueueRatio,
		MaxPermissionSlotRPCInflight:     pressure.maxPermissionSlotRPCInflight,
		PermissionBatchStarted:           counterDelta.permissionBatchStarted,
		PermissionBatchPanics:            counterDelta.permissionBatchPanics,
		MaxPermissionBatchActive:         pressure.maxPermissionBatchActive,
		MembershipMutationRows:           counterDelta.membershipMutationRows,
		MaxAdvancePoolUtil:               pressure.maxAdvancePoolUtil,
		MaxAdvancePoolWaiting:            pressure.maxAdvancePoolWaiting,
		MaxAppendPoolUtil:                pressure.maxAppendPoolUtil,
		MaxPostCommitPoolUtil:            pressure.maxPostCommitPoolUtil,
		MaxPostCommitBacklog:             pressure.maxPostCommitBacklog,
		MaxPostCommitHandoffRatio:        pressure.maxPostCommitHandoffRatio,
		MaxRouterGroupInflight:           pressure.maxRouterGroupInflight,
		MaxRouterGroupCapacity:           pressure.maxRouterGroupCapacity,
		MaxRouterGroupRatio:              pressure.maxRouterGroupRatio,
		MaxMessageCommitQueueDepth:       pressure.maxMessageCommitQueueDepth,
		MaxMessageMemTableBytes:          pressure.maxMessageMemTableBytes,
		MaxMessageMemTableCount:          pressure.maxMessageMemTableCount,
		MaxMessageReadAmplification:      pressure.maxMessageReadAmplification,
		MaxMessageCompactionDebtBytes:    pressure.maxMessageCompactionDebtBytes,
		MaxMessageCompactions:            pressure.maxMessageCompactions,
		MaxMessageFlushes:                pressure.maxMessageFlushes,
		MessageIdempotencyNegativeSkips:  counterDelta.messageIdempotencyNegativeSkips,
		MessageIdempotencyPointReads:     counterDelta.messageIdempotencyPointReads,
		MessagePhysicalCommits:           counterDelta.messagePhysicalCommits,
		MessageCommitBatchRequests:       counterDelta.messageCommitBatchRequests,
		MessageCommitBatchRecords:        counterDelta.messageCommitBatchRecords,
		MessageCommitBatchBytes:          counterDelta.messageCommitBatchBytes,
		MessageCommitSeconds:             counterDelta.messageCommitSeconds,
		MessageLeaderCommitRequests:      counterDelta.messageLeaderCommitRequests,
		MessageFollowerCommitRequests:    counterDelta.messageFollowerCommitRequests,
		MessageLeaderCommitSeconds:       counterDelta.messageLeaderCommitSeconds,
		MessageFollowerCommitSeconds:     counterDelta.messageFollowerCommitSeconds,
		MessageWALBytesIn:                counterDelta.messageWALBytesIn,
		MessageWALBytesWritten:           counterDelta.messageWALBytesWritten,
		MessageFlushBytesWritten:         counterDelta.messageFlushBytesWritten,
		MessageCompactionBytesRead:       counterDelta.messageCompactionBytesRead,
		MessageCompactionBytesWritten:    counterDelta.messageCompactionBytesWritten,
		MessageSSTableSizeBytesDelta:     counterDelta.messageSSTableSizeBytes,
		RecipientProcessOK:               counterDelta.recipientProcessOK,
		RecipientProcessRecipientsOK:     counterDelta.recipientProcessRecipientsOK,
		RecipientProcessError:            counterDelta.recipientProcessError,
		ReceiverProgress:                 permissionSoakReceiverSnapshots(receiverCounts, receiverProgress),
		MaxHeapBytes:                     pressure.maxHeapBytes,
		MaxAggregateHeapBytes:            pressure.maxAggregateHeapBytes,
		AllocatedBytes:                   counterDelta.allocatedBytes,
		GCCountDelta:                     counterDelta.gcCount,
		MetricSamples:                    pressure.samples,
		MetricSampleErrors:               pressure.sampleErrors,
		ProcessContinuous:                processContinuous,
		CounterCaptureError:              counterCaptureError,
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Logf("WKRC-PERMISSION-SOAK-FAILURE marshal error: %v", err)
		return
	}
	t.Logf("WKRC-PERMISSION-SOAK-FAILURE %s", encoded)
}

func preparePermissionSoakChannels(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	totalChannels int,
	hashSlotLeaders []uint64,
) []string {
	t.Helper()
	channels, err := permissionSoakChannelIDs(totalChannels, hashSlotLeaders)
	if err != nil {
		t.Fatalf("select permission soak channels: %v", err)
	}
	channelItems := make([]benchmodel.ChannelItem, totalChannels)
	subscriberItems := make([]benchmodel.SubscriberItem, totalChannels)
	for index, channelID := range channels {
		clientIndex := index % mediumSenderConnections
		channelItems[index] = benchmodel.ChannelItem{ChannelID: channelID, ChannelType: frame.ChannelTypeGroup}
		subscriberItems[index] = benchmodel.SubscriberItem{
			ChannelID:   channelID,
			ChannelType: frame.ChannelTypeGroup,
			Subscribers: []string{mediumSenderUID(clientIndex), mediumPermissionSoakReceiverUID(clientIndex)},
		}
	}
	apiAddrs := make([]string, 0, len(cluster.Nodes))
	for _, node := range cluster.Nodes {
		apiAddrs = append(apiAddrs, "http://"+node.APIAddr())
	}
	client := benchtarget.NewClient(benchtarget.Config{APIAddrs: apiAddrs})
	if err := client.UpsertChannels(ctx, benchmodel.BatchChannelsRequest{
		RunID: "wkrc-permission-soak", BatchID: "channels", Upsert: true, Channels: channelItems,
	}); err != nil {
		t.Fatalf("prepare permission soak channels: %v\n%s", err, cluster.DumpDiagnostics())
	}
	for start := 0; start < len(subscriberItems); start += 200 {
		end := min(start+200, len(subscriberItems))
		if err := client.AddSubscribers(ctx, benchmodel.BatchSubscribersRequest{
			RunID: "wkrc-permission-soak", BatchID: fmt.Sprintf("subscribers-%04d", start/200), Items: subscriberItems[start:end],
		}); err != nil {
			t.Fatalf("prepare permission soak subscribers [%d,%d): %v\n%s", start, end, err, cluster.DumpDiagnostics())
		}
	}
	return channels
}

func permissionSoakChannelIDs(totalChannels int, hashSlotLeaders []uint64) ([]string, error) {
	if totalChannels <= 0 {
		return nil, fmt.Errorf("permission soak channel count must be positive")
	}
	if len(hashSlotLeaders) != mediumPhysicalHashSlots {
		return nil, fmt.Errorf("permission soak hash-slot leaders = %d, want %d", len(hashSlotLeaders), mediumPhysicalHashSlots)
	}
	for hashSlot, leaderID := range hashSlotLeaders {
		if leaderID == 0 || leaderID > mediumReplicaCount {
			return nil, fmt.Errorf("permission soak hash slot %d leader = %d, want 1..%d", hashSlot, leaderID, mediumReplicaCount)
		}
	}

	channels := make([]string, 0, totalChannels)
	localChannels := 0
	remoteChannels := 0
	for index := 0; index < totalChannels; index++ {
		channelID := fmt.Sprintf("wkrc-permission-soak-group-%05d", index+1)
		ingressNodeID := uint64(index%mediumSenderConnections%mediumReplicaCount + 1)
		if hashSlotLeaders[permissionSoakChannelHashSlot(channelID)] == ingressNodeID {
			localChannels++
		} else {
			remoteChannels++
		}
		channels = append(channels, channelID)
	}
	if localChannels == 0 || remoteChannels == 0 {
		return nil, fmt.Errorf("permission soak route mix = local %d remote %d, want both positive", localChannels, remoteChannels)
	}
	return channels, nil
}

func permissionSoakChannelHashSlot(channelID string) uint16 {
	return uint16(crc32.ChecksumIEEE([]byte(channelID)) % mediumPhysicalHashSlots)
}

func connectPermissionSoakRecipients(t *testing.T, cluster *suite.StartedCluster) []*suite.WKProtoClient {
	t.Helper()
	recipients := make([]*suite.WKProtoClient, mediumSenderConnections)
	for index := range recipients {
		recipients[index] = mustConnect(
			t,
			cluster.MustNode(uint64(index%mediumReplicaCount+1)),
			mediumPermissionSoakReceiverUID(index),
		)
	}
	return recipients
}

func mediumPermissionSoakReceiverUID(index int) string {
	return fmt.Sprintf("wkrc-permission-soak-receiver-%02d", index+1)
}

func startPermissionSoakSenderReaders(
	clients []*suite.WKProtoClient,
	counts []int,
	tracker *permissionSoakTracker,
) <-chan error {
	results := make(chan error, len(clients))
	for index, client := range clients {
		client := client
		count := counts[index]
		go func() {
			for range count {
				sendack, err := client.ReadSendAck()
				if err != nil {
					results <- err
					return
				}
				if sendack.ReasonCode != frame.ReasonSuccess {
					results <- fmt.Errorf("SENDACK %s reason=%v", sendack.ClientMsgNo, sendack.ReasonCode)
					return
				}
				if _, err := tracker.observeSendack(sendack.ClientMsgNo); err != nil {
					results <- err
					return
				}
			}
			results <- nil
		}()
	}
	return results
}

func startPermissionSoakReceiverReaders(
	clients []*suite.WKProtoClient,
	counts []int,
	deadline time.Time,
	tracker *permissionSoakTracker,
	progress []*permissionSoakReceiverProgress,
) <-chan error {
	results := make(chan error, len(clients))
	for index, client := range clients {
		client := client
		count := counts[index]
		uid := mediumPermissionSoakReceiverUID(index)
		clientProgress := progress[index]
		go func() {
			results <- runPermissionSoakReceiver(client, count, uid, deadline, tracker, clientProgress)
		}()
	}
	return results
}

type permissionSoakReceiver interface {
	ReadRecv() (*frame.RecvPacket, error)
	RecvAck(messageID int64, messageSeq uint64) error
}

type permissionSoakHeartbeatClient interface {
	SendFrame(frame.Frame) error
}

func startPermissionSoakHeartbeats(
	ctx context.Context,
	clients []*suite.WKProtoClient,
	interval time.Duration,
) <-chan error {
	results := make(chan error, len(clients))
	for index, client := range clients {
		client := client
		clientIndex := index
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			err := runPermissionSoakHeartbeat(ctx, client, ticker.C)
			if err != nil {
				err = fmt.Errorf("client=%d: %w", clientIndex, err)
			}
			results <- err
		}()
	}
	return results
}

func runPermissionSoakHeartbeat(
	ctx context.Context,
	client permissionSoakHeartbeatClient,
	ticks <-chan time.Time,
) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-ticks:
			if !ok {
				return nil
			}
			if err := client.SendFrame(&frame.PingPacket{}); err != nil {
				return fmt.Errorf("send PING heartbeat: %w", err)
			}
		}
	}
}

func runPermissionSoakReceiver(
	client permissionSoakReceiver,
	count int,
	uid string,
	deadline time.Time,
	tracker *permissionSoakTracker,
	progress *permissionSoakReceiverProgress,
) error {
	for received := 0; received < count; {
		recv, err := client.ReadRecv()
		if errors.Is(err, context.DeadlineExceeded) && time.Now().Before(deadline) {
			progress.readTimeouts.Add(1)
			continue
		}
		if err != nil {
			return fmt.Errorf(
				"permission soak recipient %s received=%d/%d read_timeouts=%d: %w",
				uid,
				received,
				count,
				progress.readTimeouts.Load(),
				err,
			)
		}
		if _, err := tracker.observeRecv(recv.ClientMsgNo); err != nil {
			return fmt.Errorf("permission soak recipient %s observe RECV: %w", uid, err)
		}
		if err := client.RecvAck(recv.MessageID, recv.MessageSeq); err != nil {
			return fmt.Errorf("permission soak recipient %s ack RECV: %w", uid, err)
		}
		received++
		progress.received.Add(1)
	}
	return nil
}

func newPermissionSoakReceiverProgress(count int) []*permissionSoakReceiverProgress {
	progress := make([]*permissionSoakReceiverProgress, count)
	for index := range progress {
		progress[index] = &permissionSoakReceiverProgress{}
	}
	return progress
}

func permissionSoakReceiverSnapshots(
	counts []int,
	progress []*permissionSoakReceiverProgress,
) []permissionSoakReceiverSnapshot {
	snapshots := make([]permissionSoakReceiverSnapshot, 0, min(len(counts), len(progress)))
	for index := 0; index < len(counts) && index < len(progress); index++ {
		snapshots = append(snapshots, permissionSoakReceiverSnapshot{
			Index:        index,
			UID:          mediumPermissionSoakReceiverUID(index),
			Expected:     counts[index],
			Received:     progress[index].received.Load(),
			ReadTimeouts: progress[index].readTimeouts.Load(),
		})
	}
	return snapshots
}

func newPermissionSoakTracker() *permissionSoakTracker {
	return &permissionSoakTracker{
		sendacks: newBoundedLatencyHistogram(),
		recvs:    newBoundedLatencyHistogram(),
	}
}

func (t *permissionSoakTracker) begin(clientMsgNo string, expectedReceives int) {
	start := &permissionSoakMessageStart{startedAt: time.Now()}
	start.remaining.Store(int32(expectedReceives + 1))
	if _, loaded := t.starts.LoadOrStore(clientMsgNo, start); loaded {
		panic("duplicate permission soak client message number")
	}
	t.pending.Add(1)
}

func (t *permissionSoakTracker) observeSendack(clientMsgNo string) (time.Duration, error) {
	return t.observe(clientMsgNo, t.sendacks)
}

func (t *permissionSoakTracker) observeRecv(clientMsgNo string) (time.Duration, error) {
	return t.observe(clientMsgNo, t.recvs)
}

func (t *permissionSoakTracker) observe(clientMsgNo string, histogram *boundedLatencyHistogram) (time.Duration, error) {
	value, ok := t.starts.Load(clientMsgNo)
	if !ok {
		return 0, fmt.Errorf("permission soak message %s has no send start", clientMsgNo)
	}
	start := value.(*permissionSoakMessageStart)
	latency := time.Since(start.startedAt)
	histogram.observe(latency)
	remaining := start.remaining.Add(-1)
	if remaining < 0 {
		return 0, fmt.Errorf("permission soak message %s received too many terminal frames", clientMsgNo)
	}
	if remaining == 0 {
		t.starts.Delete(clientMsgNo)
		t.pending.Add(-1)
	}
	return latency, nil
}

func permissionSoakConfigFromEnv() (permissionSoakConfig, error) {
	config := permissionSoakConfig{
		enabled:       os.Getenv("WK_E2E_MEDIUM_RECIPIENT_PERMISSION_SOAK") == "1",
		duration:      mediumPermissionSoakDuration,
		offeredQPS:    mediumOfferedQPS,
		groupChannels: mediumPermissionSoakGroupChannels,
	}
	if raw := strings.TrimSpace(os.Getenv("WK_E2E_MEDIUM_RECIPIENT_SOAK_DURATION")); raw != "" {
		duration, err := time.ParseDuration(raw)
		if err != nil || duration < mediumPermissionSoakMinDuration || duration > mediumPermissionSoakMaxDuration {
			return permissionSoakConfig{}, fmt.Errorf(
				"WK_E2E_MEDIUM_RECIPIENT_SOAK_DURATION=%q must be a duration in [%s,%s]",
				raw,
				mediumPermissionSoakMinDuration,
				mediumPermissionSoakMaxDuration,
			)
		}
		config.duration = duration
	}
	var err error
	config.offeredQPS, err = permissionSoakEnvInt(
		"WK_E2E_MEDIUM_RECIPIENT_QPS",
		config.offeredQPS,
		mediumMinOfferedQPS,
		20_000,
	)
	if err != nil {
		return permissionSoakConfig{}, err
	}
	config.groupChannels, err = permissionSoakEnvInt(
		"WK_E2E_MEDIUM_RECIPIENT_GROUP_CHANNELS",
		config.groupChannels,
		mediumSenderConnections,
		mediumCloudGroupChannelCount,
	)
	if err != nil {
		return permissionSoakConfig{}, err
	}
	if config.groupChannels%mediumSenderConnections != 0 {
		return permissionSoakConfig{}, fmt.Errorf(
			"WK_E2E_MEDIUM_RECIPIENT_GROUP_CHANNELS=%d must be a multiple of %d senders",
			config.groupChannels,
			mediumSenderConnections,
		)
	}
	return config, nil
}

func permissionSoakEnvInt(name string, fallback, minimum, maximum int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s=%q must be an integer in [%d,%d]", name, raw, minimum, maximum)
	}
	return value, nil
}

func permissionSoakAcceptanceError(evidence permissionSoakEvidence, config permissionSoakConfig) error {
	expectedMessages := int(config.duration.Seconds() * float64(config.offeredQPS))
	minimumIngress := float64(config.offeredQPS) * mediumCIMinIngressFraction
	minimumSendLoopMS := milliseconds(config.duration) * mediumCIMinIngressFraction
	maxAllocatedBytes := float64(expectedMessages)*mediumMaxAllocatedBytesPerMessage +
		config.duration.Seconds()*mediumMaxBackgroundAllocatedBytesPerSecond
	switch {
	case evidence.Schema != mediumPermissionSoakEvidenceSchema:
		return fmt.Errorf("permission soak schema = %q, want %q", evidence.Schema, mediumPermissionSoakEvidenceSchema)
	case evidence.ConfiguredDurationMS != milliseconds(config.duration):
		return fmt.Errorf("permission soak configured duration = %.3fms, want %.3fms", evidence.ConfiguredDurationMS, milliseconds(config.duration))
	case evidence.SendLoopDurationMS < minimumSendLoopMS:
		return fmt.Errorf("permission soak send loop duration = %.3fms, want at least %.3fms", evidence.SendLoopDurationMS, minimumSendLoopMS)
	case evidence.MeasuredDurationMS < evidence.SendLoopDurationMS:
		return fmt.Errorf("permission soak measured duration = %.3fms, want at least send loop %.3fms", evidence.MeasuredDurationMS, evidence.SendLoopDurationMS)
	case evidence.Messages != expectedMessages:
		return fmt.Errorf("permission soak messages = %d, want %d", evidence.Messages, expectedMessages)
	case evidence.GroupChannels != config.groupChannels:
		return fmt.Errorf("permission soak group channels = %d, want %d", evidence.GroupChannels, config.groupChannels)
	case evidence.ActiveGroupChannels != min(config.groupChannels, expectedMessages):
		return fmt.Errorf("permission soak active group channels = %d, want %d", evidence.ActiveGroupChannels, min(config.groupChannels, expectedMessages))
	case evidence.Senders != mediumSenderConnections || evidence.Recipients != mediumSenderConnections:
		return fmt.Errorf("permission soak clients = senders %d recipients %d, want %d/%d", evidence.Senders, evidence.Recipients, mediumSenderConnections, mediumSenderConnections)
	case evidence.OfferedQPS != config.offeredQPS:
		return fmt.Errorf("permission soak offered QPS = %d, want %d", evidence.OfferedQPS, config.offeredQPS)
	case evidence.IngressPerSecond < minimumIngress:
		return fmt.Errorf("permission soak ingress = %.3f/s, want at least %.3f/s", evidence.IngressPerSecond, minimumIngress)
	case evidence.SendackP99MS > milliseconds(time.Second):
		return fmt.Errorf("permission soak SENDACK P99 = %.3fms, want at most 1000ms", evidence.SendackP99MS)
	case evidence.RecvP99MS > 2_000:
		return fmt.Errorf("permission soak RECV P99 = %.3fms, want at most 2000ms", evidence.RecvP99MS)
	case evidence.ChannelRPCAdmissionFull != 0:
		return fmt.Errorf("permission soak Channel RPC admission full = %.0f, want 0", evidence.ChannelRPCAdmissionFull)
	case evidence.ChannelStoreApplyFull != 0:
		return fmt.Errorf("permission soak Channel store-apply admission full = %.0f, want 0", evidence.ChannelStoreApplyFull)
	case evidence.TransportRPCMetricNodes != mediumReplicaCount:
		return fmt.Errorf("permission soak transport RPC metric nodes = %d, want %d", evidence.TransportRPCMetricNodes, mediumReplicaCount)
	case evidence.MaxTransportRPCQueueRatio >= 1:
		return fmt.Errorf("permission soak transport RPC queue ratio = %.6f, want below 1", evidence.MaxTransportRPCQueueRatio)
	case evidence.MaxTransportRPCBusyRatio >= 1:
		return fmt.Errorf("permission soak transport RPC busy ratio = %.6f, want below 1", evidence.MaxTransportRPCBusyRatio)
	case evidence.TransportRPCRejected != 0:
		return fmt.Errorf("permission soak transport RPC rejected = %.0f, want 0", evidence.TransportRPCRejected)
	case evidence.PermissionSlotRPCCalls <= 0:
		return fmt.Errorf("permission soak permission Slot RPC calls = %.0f, want positive", evidence.PermissionSlotRPCCalls)
	case evidence.PermissionSlotRPCErrors != 0:
		return fmt.Errorf("permission soak permission Slot RPC errors = %.0f, want 0", evidence.PermissionSlotRPCErrors)
	case evidence.MaxPermissionSlotRPCQueueRatio >= 1:
		return fmt.Errorf("permission soak permission Slot RPC queue ratio = %.6f, want below 1", evidence.MaxPermissionSlotRPCQueueRatio)
	case evidence.PermissionSlotRPCAdmissionErrors != 0:
		return fmt.Errorf("permission soak permission Slot RPC admission errors = %.0f, want 0", evidence.PermissionSlotRPCAdmissionErrors)
	case evidence.PermissionBatchStarted <= 0:
		return fmt.Errorf("permission soak permission batch started = %.0f, want positive", evidence.PermissionBatchStarted)
	case evidence.PermissionBatchPanics != 0:
		return fmt.Errorf("permission soak permission batch panics = %.0f, want 0", evidence.PermissionBatchPanics)
	case evidence.MembershipMutationRows != 0:
		return fmt.Errorf("permission soak membership mutation rows = %.0f, want 0", evidence.MembershipMutationRows)
	case evidence.PluginReceiveFull != 0 || evidence.PluginReceiveClosed != 0:
		return fmt.Errorf("permission soak plugin receive enqueue non-accepted = full %.0f closed %.0f, want 0/0", evidence.PluginReceiveFull, evidence.PluginReceiveClosed)
	case evidence.PluginReceiveInvokeOK != evidence.PluginReceiveAccepted || evidence.PluginReceiveInvokeError != 0:
		return fmt.Errorf("permission soak plugin receive invoke = ok %.0f error %.0f, want %.0f/0", evidence.PluginReceiveInvokeOK, evidence.PluginReceiveInvokeError, evidence.PluginReceiveAccepted)
	case evidence.AllocatedBytes <= 0 || evidence.AllocatedBytes > maxAllocatedBytes:
		return fmt.Errorf("permission soak allocated bytes = %.0f, want in (0,%.0f]", evidence.AllocatedBytes, maxAllocatedBytes)
	case evidence.GCCountDelta <= 0 || evidence.GCCountDelta/float64(expectedMessages) > mediumMaxGCPerMessage:
		return fmt.Errorf("permission soak GC/message = %.6f, want in (0,%.6f]", evidence.GCCountDelta/float64(expectedMessages), mediumMaxGCPerMessage)
	case evidence.MaxHeapBytes <= 0 || evidence.MaxHeapBytes > float64(mediumMaxHeapBytes):
		return fmt.Errorf("permission soak max heap bytes = %.0f, want in (0,%d]", evidence.MaxHeapBytes, mediumMaxHeapBytes)
	case evidence.MaxAggregateHeapBytes <= 0 || evidence.MaxAggregateHeapBytes > float64(mediumReplicaCount*mediumMaxHeapBytes):
		return fmt.Errorf("permission soak aggregate heap bytes = %.0f, want in (0,%d]", evidence.MaxAggregateHeapBytes, mediumReplicaCount*mediumMaxHeapBytes)
	case evidence.MetricSamples == 0:
		return fmt.Errorf("permission soak metric samples = %d, want positive", evidence.MetricSamples)
	case evidence.MetricSampleErrors != 0:
		return fmt.Errorf("permission soak metric sample errors = %d, want 0", evidence.MetricSampleErrors)
	case evidence.PendingMessages != 0:
		return fmt.Errorf("permission soak pending messages = %d, want 0", evidence.PendingMessages)
	case !evidence.Drained:
		return fmt.Errorf("permission soak did not drain")
	case !evidence.ProcessContinuous:
		return fmt.Errorf("permission soak process continuity failed")
	}
	return nil
}
