//go:build e2e

package medium_recipient_hotpath

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	benchmetrics "github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
)

func TestPermissionSoakStageLatencyUsesMeasuredHistogramDelta(t *testing.T) {
	before := benchmetrics.PrometheusSnapshot{}
	after := benchmetrics.PrometheusSnapshot{}
	addHistogram := func(family string, labels map[string]string) {
		for _, bucket := range []struct {
			le     string
			before float64
			after  float64
		}{
			{le: "0.5", before: 10, after: 10},
			{le: "1", before: 110, after: 210},
			{le: "+Inf", before: 110, after: 210},
		} {
			bucketLabels := make(map[string]string, len(labels)+1)
			for key, value := range labels {
				bucketLabels[key] = value
			}
			bucketLabels["le"] = bucket.le
			before.Samples = append(before.Samples, benchmetrics.PrometheusSample{
				Name: family + "_bucket", Labels: bucketLabels, Value: bucket.before,
			})
			after.Samples = append(after.Samples, benchmetrics.PrometheusSample{
				Name: family + "_bucket", Labels: bucketLabels, Value: bucket.after,
			})
		}
	}

	addHistogram("wukongim_gateway_async_send_dispatch_wait_duration_seconds", map[string]string{"protocol": "wkproto"})
	addHistogram("wukongim_gateway_async_send_batch_records", nil)
	addHistogram("wukongim_gateway_frame_handle_duration_seconds", map[string]string{"frame_type": "SEND"})
	for _, path := range []string{"local", "remote", "batch"} {
		addHistogram("wukongim_channelappend_router_duration_seconds", map[string]string{
			"path": path, "result": "ok",
		})
	}
	addHistogram("wukongim_channelappend_router_item_duration_seconds", map[string]string{
		"path": "batch", "result": "ok",
	})
	for _, stage := range []string{"permission", "pre_append", "submitter"} {
		addHistogram("wukongim_message_send_batch_stage_item_duration_seconds", map[string]string{
			"stage": stage, "result": "ok",
		})
	}
	for _, stage := range []string{
		"store_append_wait",
		"post_store_commit_wait",
		"quorum_follower_pull_wait",
		"quorum_ack_offset_wait",
		"quorum_hw_advance_wait",
		"quorum_final_complete_wait",
	} {
		addHistogram("wukongim_channelv2_append_wait_stage_duration_seconds", map[string]string{
			"stage": stage, "commit_mode": "quorum", "result": "ok",
		})
	}
	for _, lane := range []string{"leader_append", "follower_apply"} {
		addHistogram("wukongim_storage_commit_request_duration_seconds", map[string]string{
			"store": "message", "lane": lane, "result": "ok",
		})
	}
	addHistogram("wukongim_storage_commit_batch_duration_seconds", map[string]string{
		"store": "message", "stage": "commit", "result": "ok",
	})
	for _, stage := range []string{"mailbox_wait", "ack_apply", "handler"} {
		addHistogram("wukongim_channelv2_leader_pull_stage_duration_seconds", map[string]string{
			"stage": stage,
		})
	}

	got := permissionSoakStageLatencyFromSnapshots(before, after)
	if gotBatchP99 := permissionSoakGatewayBatchRecordsP99FromSnapshots(before, after); gotBatchP99 != 0.995 {
		t.Fatalf("gateway batch records P99 = %.3f, want 0.995 from measured delta", gotBatchP99)
	}
	for name, value := range map[string]float64{
		"gateway_dispatch_wait":      got.GatewayDispatchWaitP99MS,
		"gateway_send_handle":        got.GatewaySendHandleP99MS,
		"router_local":               got.ChannelAppendRouterLocalP99MS,
		"router_remote":              got.ChannelAppendRouterRemoteP99MS,
		"router_batch":               got.ChannelAppendRouterBatchP99MS,
		"router_batch_item":          got.ChannelAppendRouterBatchItemP99MS,
		"message_permission":         got.MessagePermissionP99MS,
		"message_pre_append":         got.MessagePreAppendP99MS,
		"message_submitter":          got.MessageSubmitterP99MS,
		"store_append_wait":          got.ChannelStoreAppendWaitP99MS,
		"post_store_commit_wait":     got.ChannelPostStoreCommitWaitP99MS,
		"quorum_follower_pull_wait":  got.ChannelQuorumFollowerPullWaitP99MS,
		"quorum_ack_offset_wait":     got.ChannelQuorumAckOffsetWaitP99MS,
		"quorum_hw_advance_wait":     got.ChannelQuorumHWAdvanceWaitP99MS,
		"quorum_final_complete_wait": got.ChannelQuorumFinalCompleteWaitP99MS,
		"leader_commit_request":      got.StorageLeaderCommitRequestP99MS,
		"follower_commit_request":    got.StorageFollowerCommitRequestP99MS,
		"physical_commit":            got.StoragePhysicalCommitP99MS,
		"leader_pull_mailbox_wait":   got.ChannelLeaderPullMailboxWaitP99MS,
		"leader_pull_ack_apply":      got.ChannelLeaderPullAckApplyP99MS,
		"leader_pull_handler":        got.ChannelLeaderPullHandlerP99MS,
	} {
		if value != 995 {
			t.Fatalf("%s P99 = %.3fms, want 995ms from measured delta", name, value)
		}
	}

	tail := permissionSoakStageTailLatencyFromSnapshots(before, after)
	for name, value := range map[string]float64{
		"gateway_dispatch_wait":      tail.GatewayDispatchWaitP99MS,
		"gateway_send_handle":        tail.GatewaySendHandleP99MS,
		"router_local":               tail.ChannelAppendRouterLocalP99MS,
		"router_remote":              tail.ChannelAppendRouterRemoteP99MS,
		"router_batch":               tail.ChannelAppendRouterBatchP99MS,
		"router_batch_item":          tail.ChannelAppendRouterBatchItemP99MS,
		"message_permission":         tail.MessagePermissionP99MS,
		"message_pre_append":         tail.MessagePreAppendP99MS,
		"message_submitter":          tail.MessageSubmitterP99MS,
		"store_append_wait":          tail.ChannelStoreAppendWaitP99MS,
		"post_store_commit_wait":     tail.ChannelPostStoreCommitWaitP99MS,
		"quorum_follower_pull_wait":  tail.ChannelQuorumFollowerPullWaitP99MS,
		"quorum_ack_offset_wait":     tail.ChannelQuorumAckOffsetWaitP99MS,
		"quorum_hw_advance_wait":     tail.ChannelQuorumHWAdvanceWaitP99MS,
		"quorum_final_complete_wait": tail.ChannelQuorumFinalCompleteWaitP99MS,
		"leader_commit_request":      tail.StorageLeaderCommitRequestP99MS,
		"follower_commit_request":    tail.StorageFollowerCommitRequestP99MS,
		"physical_commit":            tail.StoragePhysicalCommitP99MS,
		"leader_pull_mailbox_wait":   tail.ChannelLeaderPullMailboxWaitP99MS,
		"leader_pull_ack_apply":      tail.ChannelLeaderPullAckApplyP99MS,
		"leader_pull_handler":        tail.ChannelLeaderPullHandlerP99MS,
	} {
		if value != 999.5 {
			t.Fatalf("%s P99.9 = %.3fms, want 999.5ms from measured delta", name, value)
		}
	}
}

func TestPermissionSoakStageCaptureIncludesGatewayBatchRecords(t *testing.T) {
	if !isPermissionSoakStageLatencyBucket("wukongim_gateway_async_send_batch_records_bucket") {
		t.Fatal("gateway batch records histogram must be retained in measured-window evidence")
	}
	if !isPermissionSoakStageLatencyBucket("wukongim_gateway_frame_handle_duration_seconds_bucket") {
		t.Fatal("gateway SEND handle histogram must be retained in measured-window evidence")
	}
	if !isPermissionSoakStageLatencyBucket("wukongim_channelappend_router_duration_seconds_bucket") {
		t.Fatal("channel append router histogram must be retained in measured-window evidence")
	}
	if !isPermissionSoakStageLatencyBucket("wukongim_channelappend_router_item_duration_seconds_bucket") {
		t.Fatal("channel append router item histogram must be retained in measured-window evidence")
	}
	if !isPermissionSoakStageLatencyBucket("wukongim_message_send_batch_stage_item_duration_seconds_bucket") {
		t.Fatal("message SendBatch stage histogram must be retained in measured-window evidence")
	}
}

func TestPermissionSoakReceiverContinuesAfterBoundedReadTimeout(t *testing.T) {
	tracker := newPermissionSoakTracker()
	tracker.begin("message-1", 1)
	receiver := &scriptedPermissionSoakReceiver{
		reads: []scriptedPermissionSoakRead{
			{err: context.DeadlineExceeded},
			{packet: &frame.RecvPacket{ClientMsgNo: "message-1", MessageID: 7, MessageSeq: 9}},
		},
	}
	progress := &permissionSoakReceiverProgress{}

	err := runPermissionSoakReceiver(
		receiver,
		1,
		"receiver-1",
		time.Now().Add(time.Second),
		tracker,
		progress,
	)
	if err != nil {
		t.Fatalf("runPermissionSoakReceiver(): %v", err)
	}
	if got := progress.received.Load(); got != 1 {
		t.Fatalf("received = %d, want 1", got)
	}
	if got := progress.readTimeouts.Load(); got != 1 {
		t.Fatalf("read timeouts = %d, want 1", got)
	}
	if receiver.acks != 1 {
		t.Fatalf("recv acks = %d, want 1", receiver.acks)
	}
}

func TestPermissionSoakReceiverStopsAfterOverallDeadline(t *testing.T) {
	receiver := &scriptedPermissionSoakReceiver{
		reads: []scriptedPermissionSoakRead{{err: context.DeadlineExceeded}},
	}
	progress := &permissionSoakReceiverProgress{}

	err := runPermissionSoakReceiver(
		receiver,
		1,
		"receiver-2",
		time.Now().Add(-time.Second),
		newPermissionSoakTracker(),
		progress,
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	if !strings.Contains(err.Error(), "receiver-2") || !strings.Contains(err.Error(), "received=0/1") {
		t.Fatalf("error = %v, want bounded recipient progress", err)
	}
}

func TestPermissionSoakHeartbeatKeepsConnectedSessionsActive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time, 2)
	client := &recordingPermissionSoakHeartbeatClient{}
	done := make(chan error, 1)
	go func() {
		done <- runPermissionSoakHeartbeat(ctx, client, ticks)
	}()

	ticks <- time.Now()
	ticks <- time.Now()
	for client.pings.Load() != 2 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runPermissionSoakHeartbeat(): %v", err)
	}
}

type scriptedPermissionSoakRead struct {
	packet *frame.RecvPacket
	err    error
}

type scriptedPermissionSoakReceiver struct {
	reads []scriptedPermissionSoakRead
	acks  int
}

type recordingPermissionSoakHeartbeatClient struct {
	pings atomic.Uint64
}

func (c *recordingPermissionSoakHeartbeatClient) SendFrame(value frame.Frame) error {
	if _, ok := value.(*frame.PingPacket); !ok {
		return errors.New("heartbeat frame is not PING")
	}
	c.pings.Add(1)
	return nil
}

func (r *scriptedPermissionSoakReceiver) ReadRecv() (*frame.RecvPacket, error) {
	if len(r.reads) == 0 {
		return nil, errors.New("unexpected receiver read")
	}
	read := r.reads[0]
	r.reads = r.reads[1:]
	return read.packet, read.err
}

func (r *scriptedPermissionSoakReceiver) RecvAck(int64, uint64) error {
	r.acks++
	return nil
}

func TestPermissionSoakConfigFromEnv(t *testing.T) {
	t.Setenv("WK_E2E_MEDIUM_RECIPIENT_PERMISSION_SOAK", "1")
	t.Setenv("WK_E2E_MEDIUM_RECIPIENT_SOAK_DURATION", "")
	t.Setenv("WK_E2E_MEDIUM_RECIPIENT_QPS", "")
	t.Setenv("WK_E2E_MEDIUM_RECIPIENT_GROUP_CHANNELS", "")

	config, err := permissionSoakConfigFromEnv()
	if err != nil {
		t.Fatalf("permissionSoakConfigFromEnv(): %v", err)
	}
	if !config.enabled || config.duration != 30*time.Minute || config.offeredQPS != 4_500 || config.groupChannels != 5_000 {
		t.Fatalf("default soak config = %+v, want enabled 30m/4500 QPS/5000 channels", config)
	}

	t.Run("bounded smoke override", func(t *testing.T) {
		t.Setenv("WK_E2E_MEDIUM_RECIPIENT_SOAK_DURATION", "10s")
		t.Setenv("WK_E2E_MEDIUM_RECIPIENT_QPS", "5000")
		t.Setenv("WK_E2E_MEDIUM_RECIPIENT_GROUP_CHANNELS", "100")
		config, err := permissionSoakConfigFromEnv()
		if err != nil {
			t.Fatalf("permissionSoakConfigFromEnv(): %v", err)
		}
		if config.duration != 10*time.Second || config.offeredQPS != 5_000 || config.groupChannels != 100 {
			t.Fatalf("smoke config = %+v, want 10s/5000 QPS/100 channels", config)
		}
	})

	for _, test := range []struct {
		name     string
		duration string
		channels string
		want     string
	}{
		{name: "duration below bound", duration: "9s", channels: "100", want: "duration"},
		{name: "duration above bound", duration: "31m", channels: "100", want: "duration"},
		{name: "channels must cover senders", duration: "10s", channels: "20", want: "GROUP_CHANNELS"},
		{name: "channels align to senders", duration: "10s", channels: "51", want: "multiple"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("WK_E2E_MEDIUM_RECIPIENT_SOAK_DURATION", test.duration)
			t.Setenv("WK_E2E_MEDIUM_RECIPIENT_GROUP_CHANNELS", test.channels)
			_, err := permissionSoakConfigFromEnv()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestPermissionSoakDiagnosticDelayCapturesSustainedPressureWindow(t *testing.T) {
	tests := []struct {
		duration time.Duration
		want     time.Duration
	}{
		{duration: 10 * time.Minute, want: 7 * time.Minute},
		{duration: 30 * time.Minute, want: 7 * time.Minute},
		{duration: 5 * time.Minute, want: 4 * time.Minute},
		{duration: 2 * time.Minute, want: 90 * time.Second},
		{duration: time.Minute, want: 5 * time.Second},
	}
	for _, test := range tests {
		if got := permissionSoakDiagnosticDelay(test.duration); got != test.want {
			t.Fatalf("diagnostic delay for %s = %s, want %s", test.duration, got, test.want)
		}
	}
}

func TestPollPermissionSoakResultSurfacesBufferedReaderFailure(t *testing.T) {
	results := make(chan error, 1)
	want := errors.New("sender reader failed")
	results <- want

	got, ready := pollPermissionSoakResult(results)
	if !ready {
		t.Fatal("pollPermissionSoakResult() did not surface a buffered failure")
	}
	if !errors.Is(got, want) {
		t.Fatalf("pollPermissionSoakResult() error = %v, want %v", got, want)
	}
	if _, ready := pollPermissionSoakResult(results); ready {
		t.Fatal("pollPermissionSoakResult() reported an empty channel as ready")
	}
}

func TestPermissionSoakFailureDiagnosticsRetainScheduledAndLiveSnapshots(t *testing.T) {
	scheduled := make(chan string, 1)
	scheduled <- "scheduled snapshot"
	liveCalls := 0

	gotScheduled, gotLive := permissionSoakFailureDiagnostics(scheduled, func() string {
		liveCalls++
		return "live snapshot"
	})
	if gotScheduled != "scheduled snapshot" {
		t.Fatalf("scheduled diagnostics = %q, want scheduled snapshot", gotScheduled)
	}
	if gotLive != "live snapshot" {
		t.Fatalf("live diagnostics = %q, want live snapshot", gotLive)
	}
	if liveCalls != 1 {
		t.Fatalf("live diagnostics calls = %d, want 1", liveCalls)
	}
}

func TestPermissionSoakDiagnosticArtifactPersistsFullSnapshot(t *testing.T) {
	dir := t.TempDir()
	want := strings.Repeat("goroutine stack\n", 8_192)
	if err := writePermissionSoakDiagnosticArtifact(dir, "scheduled-goroutines.txt", want); err != nil {
		t.Fatalf("write diagnostic artifact: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "scheduled-goroutines.txt"))
	if err != nil {
		t.Fatalf("read diagnostic artifact: %v", err)
	}
	if string(got) != want {
		t.Fatalf("diagnostic artifact bytes = %d, want %d", len(got), len(want))
	}
}

func TestPermissionSoakAcceptanceError(t *testing.T) {
	config := permissionSoakConfig{
		enabled:       true,
		duration:      10 * time.Second,
		offeredQPS:    mediumOfferedQPS,
		groupChannels: 100,
	}
	passing := permissionSoakEvidence{
		Schema:                         mediumPermissionSoakEvidenceSchema,
		ConfiguredDurationMS:           milliseconds(config.duration),
		SendLoopDurationMS:             milliseconds(config.duration),
		MeasuredDurationMS:             milliseconds(config.duration + time.Second),
		Messages:                       45_000,
		GroupChannels:                  100,
		ActiveGroupChannels:            100,
		Senders:                        mediumSenderConnections,
		Recipients:                     mediumSenderConnections,
		OfferedQPS:                     mediumOfferedQPS,
		IngressPerSecond:               mediumOfferedQPS * mediumCIMinIngressFraction,
		CompletionPerSecond:            4_000,
		SendackP99MS:                   900,
		RecvP99MS:                      1_500,
		TransportRPCMetricNodes:        mediumReplicaCount,
		MaxTransportRPCQueueRatio:      0.99,
		MaxTransportRPCBusyRatio:       0.99,
		PermissionSlotRPCCalls:         1,
		MaxPermissionSlotRPCQueueRatio: 0.25,
		PermissionBatchStarted:         1,
		MaxPermissionBatchActive:       15,
		MaxHeapBytes:                   256 << 20,
		MaxAggregateHeapBytes:          768 << 20,
		AllocatedBytes:                 45_000 * 350_000,
		GCCountDelta:                   100,
		PluginReceiveAccepted:          45_000,
		PluginReceiveInvokeOK:          45_000,
		MetricSamples:                  1,
		Drained:                        true,
		ProcessContinuous:              true,
	}
	if err := permissionSoakAcceptanceError(passing, config); err != nil {
		t.Fatalf("passing soak evidence rejected: %v", err)
	}

	tests := []struct {
		name string
		edit func(*permissionSoakEvidence)
		want string
	}{
		{name: "messages", edit: func(e *permissionSoakEvidence) { e.Messages-- }, want: "messages"},
		{name: "duration", edit: func(e *permissionSoakEvidence) { e.SendLoopDurationMS *= 0.9 }, want: "send loop duration"},
		{name: "active channels", edit: func(e *permissionSoakEvidence) { e.ActiveGroupChannels-- }, want: "active group channels"},
		{name: "ingress", edit: func(e *permissionSoakEvidence) { e.IngressPerSecond-- }, want: "ingress"},
		{name: "sendack", edit: func(e *permissionSoakEvidence) { e.SendackP99MS = 1_001 }, want: "SENDACK P99"},
		{name: "recv", edit: func(e *permissionSoakEvidence) { e.RecvP99MS = 2_001 }, want: "RECV P99"},
		{name: "Channel RPC full", edit: func(e *permissionSoakEvidence) { e.ChannelRPCAdmissionFull = 1 }, want: "Channel RPC admission full"},
		{name: "Channel store apply full", edit: func(e *permissionSoakEvidence) { e.ChannelStoreApplyFull = 1 }, want: "Channel store-apply admission full"},
		{name: "transport metrics", edit: func(e *permissionSoakEvidence) { e.TransportRPCMetricNodes-- }, want: "transport RPC metric nodes"},
		{name: "transport queue", edit: func(e *permissionSoakEvidence) { e.MaxTransportRPCQueueRatio = 1 }, want: "transport RPC queue"},
		{name: "transport busy", edit: func(e *permissionSoakEvidence) { e.MaxTransportRPCBusyRatio = 1 }, want: "transport RPC busy"},
		{name: "transport rejected", edit: func(e *permissionSoakEvidence) { e.TransportRPCRejected = 1 }, want: "transport RPC rejected"},
		{name: "permission Slot RPC missing", edit: func(e *permissionSoakEvidence) { e.PermissionSlotRPCCalls = 0 }, want: "permission Slot RPC calls"},
		{name: "permission Slot RPC error", edit: func(e *permissionSoakEvidence) { e.PermissionSlotRPCErrors = 1 }, want: "permission Slot RPC errors"},
		{name: "permission Slot RPC queue saturated", edit: func(e *permissionSoakEvidence) { e.MaxPermissionSlotRPCQueueRatio = 1 }, want: "permission Slot RPC queue ratio"},
		{name: "permission Slot RPC admission error", edit: func(e *permissionSoakEvidence) { e.PermissionSlotRPCAdmissionErrors = 1 }, want: "permission Slot RPC admission errors"},
		{name: "permission workers", edit: func(e *permissionSoakEvidence) { e.PermissionBatchStarted = 0 }, want: "permission batch started"},
		{name: "permission panic", edit: func(e *permissionSoakEvidence) { e.PermissionBatchPanics = 1 }, want: "permission batch panics"},
		{name: "membership mutation", edit: func(e *permissionSoakEvidence) { e.MembershipMutationRows = 1 }, want: "membership mutation"},
		{name: "plugin full", edit: func(e *permissionSoakEvidence) { e.PluginReceiveFull = 1 }, want: "plugin receive enqueue"},
		{name: "plugin invoke", edit: func(e *permissionSoakEvidence) { e.PluginReceiveInvokeOK-- }, want: "plugin receive invoke"},
		{name: "heap", edit: func(e *permissionSoakEvidence) { e.MaxHeapBytes = mediumMaxHeapBytes + 1 }, want: "max heap"},
		{name: "aggregate heap", edit: func(e *permissionSoakEvidence) { e.MaxAggregateHeapBytes = 3*mediumMaxHeapBytes + 1 }, want: "aggregate heap"},
		{name: "pending", edit: func(e *permissionSoakEvidence) { e.PendingMessages = 1 }, want: "pending messages"},
		{name: "samples", edit: func(e *permissionSoakEvidence) { e.MetricSamples = 0 }, want: "metric samples"},
		{name: "drain", edit: func(e *permissionSoakEvidence) { e.Drained = false }, want: "did not drain"},
		{name: "continuity", edit: func(e *permissionSoakEvidence) { e.ProcessContinuous = false }, want: "continuity"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := passing
			test.edit(&evidence)
			err := permissionSoakAcceptanceError(evidence, config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestHotPathAcceptanceError(t *testing.T) {
	passing := hotPathEvidence{
		Schema:                   mediumEvidenceSchema,
		PhysicalHashSlots:        mediumPhysicalHashSlots,
		LogicalSlots:             mediumLogicalSlots,
		Replicas:                 mediumReplicaCount,
		SlotTickIntervalMS:       milliseconds(mediumSlotTickInterval),
		SlotHeartbeatTick:        mediumSlotHeartbeatTick,
		SlotElectionTick:         mediumSlotElectionTick,
		Messages:                 mediumMessageCount * mediumMeasuredRounds,
		RecipientRows:            mediumRecipientRows * mediumMeasuredRounds,
		OnlineRoutes:             expectedMeasuredOnlineRoutes(mediumMeasuredRounds),
		Connections:              expectedConnectionCount(),
		GroupChannels:            mediumGroupChannelCount,
		ActiveGroupChannels:      expectedActiveGroupChannels(mediumGroupChannelCount, mediumMeasuredRounds),
		OfferedQPS:               mediumOfferedQPS,
		ClusterConvergenceMS:     2_500,
		ClusterStableWindowMS:    milliseconds(mediumConvergenceStableWindow),
		SlotLeaders:              []uint64{1, 2, 3, 1, 2, 3, 1, 2, 3, 1},
		MeasuredDurationMS:       float64(mediumMessageCount*mediumMeasuredRounds) / mediumOfferedQPS * 1000,
		IngressPerSecond:         mediumOfferedQPS,
		SendackP99MS:             1_000,
		RecvP99MS:                2_000,
		MaxGatewayQueueRatio:     0.99,
		MaxRecipientQueueRatio:   0.99,
		MaxRecipientWorkerRatio:  0.99,
		ChannelRPCMetricNodes:    mediumReplicaCount,
		MinChannelRPCWorkers:     mediumChannelRPCWorkers,
		MaxChannelRPCWorkers:     mediumChannelRPCWorkers,
		ChannelRPCBatchMaxItems:  mediumChannelRPCBatchMaxItems,
		ChannelRPCPullBatches:    1,
		ChannelRPCPullBatchItems: 2,
		ChannelRPCHintBatches:    1,
		ChannelRPCHintBatchItems: 2,
		PluginReceiveAccepted:    float64(pluginReceiveBatchCount() * mediumMeasuredRounds),
		PluginReceiveInvokeOK:    float64(pluginReceiveBatchCount() * mediumMeasuredRounds),
		AllocatedBytes:           float64(mediumMessageCount*mediumMeasuredRounds) * 350_000,
		GCCountDelta:             100,
		MaxHeapBytes:             256 << 20,
		MaxAggregateHeapBytes:    768 << 20,
		MetricSamples:            1,
		Drained:                  true,
		ProcessContinuous:        true,
	}
	if err := hotPathAcceptanceError(passing, mediumOfferedQPS, mediumMeasuredRounds); err != nil {
		t.Fatalf("passing evidence rejected: %v", err)
	}

	t.Run("bounded rounds use their own acceptance totals", func(t *testing.T) {
		const rounds = 20
		evidence := passing
		evidence.Messages = mediumMessageCount * rounds
		evidence.RecipientRows = mediumRecipientRows * rounds
		evidence.OnlineRoutes = expectedMeasuredOnlineRoutes(rounds)
		evidence.ActiveGroupChannels = expectedActiveGroupChannels(evidence.GroupChannels, rounds)
		evidence.MeasuredDurationMS = float64(evidence.Messages) / mediumOfferedQPS * 1000
		evidence.PluginReceiveAccepted = float64(pluginReceiveBatchCount() * rounds)
		evidence.PluginReceiveInvokeOK = evidence.PluginReceiveAccepted
		evidence.AllocatedBytes = float64(evidence.Messages) * 350_000
		evidence.GCCountDelta = 25
		if err := hotPathAcceptanceError(evidence, mediumOfferedQPS, rounds); err != nil {
			t.Fatalf("bounded-round evidence rejected: %v", err)
		}
	})

	tests := []struct {
		name string
		edit func(*hotPathEvidence)
		want string
	}{
		{name: "schema", edit: func(e *hotPathEvidence) { e.Schema = "other" }, want: "acceptance schema"},
		{name: "physical hash slots", edit: func(e *hotPathEvidence) { e.PhysicalHashSlots-- }, want: "physical hash slots"},
		{name: "logical slots", edit: func(e *hotPathEvidence) { e.LogicalSlots-- }, want: "logical slots"},
		{name: "replicas", edit: func(e *hotPathEvidence) { e.Replicas-- }, want: "acceptance replicas"},
		{name: "slot tick interval", edit: func(e *hotPathEvidence) { e.SlotTickIntervalMS-- }, want: "Slot tick interval"},
		{name: "slot heartbeat tick", edit: func(e *hotPathEvidence) { e.SlotHeartbeatTick-- }, want: "Slot heartbeat tick"},
		{name: "slot election tick", edit: func(e *hotPathEvidence) { e.SlotElectionTick-- }, want: "Slot election tick"},
		{name: "messages", edit: func(e *hotPathEvidence) { e.Messages-- }, want: "acceptance messages"},
		{name: "recipient rows", edit: func(e *hotPathEvidence) { e.RecipientRows-- }, want: "recipient rows"},
		{name: "online routes", edit: func(e *hotPathEvidence) { e.OnlineRoutes-- }, want: "online routes"},
		{name: "connections", edit: func(e *hotPathEvidence) { e.Connections-- }, want: "acceptance connections"},
		{name: "group channels", edit: func(e *hotPathEvidence) { e.GroupChannels = 0 }, want: "acceptance group channels"},
		{name: "active group channels", edit: func(e *hotPathEvidence) { e.ActiveGroupChannels-- }, want: "acceptance active group channels"},
		{name: "offered load", edit: func(e *hotPathEvidence) { e.OfferedQPS-- }, want: "offered QPS"},
		{name: "cluster convergence missing", edit: func(e *hotPathEvidence) { e.ClusterConvergenceMS = 0 }, want: "cluster convergence"},
		{name: "cluster stability short", edit: func(e *hotPathEvidence) { e.ClusterStableWindowMS-- }, want: "cluster stable window"},
		{name: "actual slot leader missing", edit: func(e *hotPathEvidence) { e.SlotLeaders[0] = 0 }, want: "actual Slot leaders"},
		{name: "actual slot leader count", edit: func(e *hotPathEvidence) { e.SlotLeaders = e.SlotLeaders[:9] }, want: "actual Slot leaders"},
		{name: "actual slot leader skew missing", edit: func(e *hotPathEvidence) {
			e.SlotLeaders[2] = 2
		}, want: "actual Slot leaders"},
		{name: "ingress", edit: func(e *hotPathEvidence) {
			e.IngressPerSecond = mediumOfferedQPS - 0.001
		}, want: "acceptance ingress"},
		{name: "sendack", edit: func(e *hotPathEvidence) { e.SendackP99MS++ }, want: "SENDACK P99"},
		{name: "recv", edit: func(e *hotPathEvidence) { e.RecvP99MS++ }, want: "RECV P99"},
		{name: "gateway queue", edit: func(e *hotPathEvidence) { e.MaxGatewayQueueRatio = 1 }, want: "gateway queue"},
		{name: "recipient queue", edit: func(e *hotPathEvidence) { e.MaxRecipientQueueRatio = 1 }, want: "recipient queue"},
		{name: "recipient worker", edit: func(e *hotPathEvidence) { e.MaxRecipientWorkerRatio = 1 }, want: "recipient worker"},
		{name: "Channel RPC metrics missing", edit: func(e *hotPathEvidence) { e.ChannelRPCMetricNodes-- }, want: "Channel RPC metric nodes"},
		{name: "Channel RPC worker drift", edit: func(e *hotPathEvidence) { e.MinChannelRPCWorkers-- }, want: "Channel RPC workers"},
		{name: "Channel RPC batch drift", edit: func(e *hotPathEvidence) { e.ChannelRPCBatchMaxItems-- }, want: "Channel RPC batch max items"},
		{name: "Channel RPC admission full", edit: func(e *hotPathEvidence) { e.ChannelRPCAdmissionFull = 1 }, want: "Channel RPC full admissions"},
		{name: "Channel RPC Pull batch missing", edit: func(e *hotPathEvidence) { e.ChannelRPCPullBatches = 0 }, want: "Channel RPC Pull batch evidence"},
		{name: "Channel RPC PullHint batch missing", edit: func(e *hotPathEvidence) { e.ChannelRPCHintBatches = 0 }, want: "Channel RPC PullHint batch evidence"},
		{name: "Channel RPC queue", edit: func(e *hotPathEvidence) { e.MaxChannelRPCQueueRatio = 1 }, want: "Channel RPC queue"},
		{name: "Channel RPC worker", edit: func(e *hotPathEvidence) { e.MaxChannelRPCWorkerRatio = 1 }, want: "Channel RPC worker"},
		{name: "membership mutation", edit: func(e *hotPathEvidence) { e.MembershipMutationRows = 1 }, want: "membership mutation rows"},
		{name: "plugin accepted", edit: func(e *hotPathEvidence) { e.PluginReceiveAccepted-- }, want: "plugin receive accepted"},
		{name: "plugin full", edit: func(e *hotPathEvidence) { e.PluginReceiveFull = 1 }, want: "enqueue non-accepted"},
		{name: "plugin invoke", edit: func(e *hotPathEvidence) { e.PluginReceiveInvokeOK-- }, want: "plugin receive invoke"},
		{name: "recipient process", edit: func(e *hotPathEvidence) { e.RecipientProcessError = 1 }, want: "recipient worker process errors"},
		{name: "measured duration missing", edit: func(e *hotPathEvidence) { e.MeasuredDurationMS = 0 }, want: "measured duration"},
		{name: "allocated missing", edit: func(e *hotPathEvidence) { e.AllocatedBytes = 0 }, want: "allocated bytes"},
		{name: "allocated regression", edit: func(e *hotPathEvidence) {
			e.AllocatedBytes = maxAcceptedAllocatedBytes(*e) + 1
		}, want: "allocated bytes/message"},
		{name: "gc missing", edit: func(e *hotPathEvidence) { e.GCCountDelta = 0 }, want: "GC count delta"},
		{name: "gc regression", edit: func(e *hotPathEvidence) { e.GCCountDelta = float64(e.Messages)*mediumMaxGCPerMessage + 1 }, want: "GC/message"},
		{name: "heap missing", edit: func(e *hotPathEvidence) { e.MaxHeapBytes = 0 }, want: "max heap bytes"},
		{name: "heap regression", edit: func(e *hotPathEvidence) { e.MaxHeapBytes = mediumMaxHeapBytes + 1 }, want: "max heap bytes"},
		{name: "samples", edit: func(e *hotPathEvidence) { e.MetricSamples = 0 }, want: "no public metric"},
		{name: "sample errors", edit: func(e *hotPathEvidence) { e.MetricSampleErrors = 1 }, want: "sample errors"},
		{name: "drain", edit: func(e *hotPathEvidence) { e.Drained = false }, want: "did not drain"},
		{name: "continuity", edit: func(e *hotPathEvidence) { e.ProcessContinuous = false }, want: "continuity"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence := passing
			evidence.SlotLeaders = append([]uint64(nil), passing.SlotLeaders...)
			test.edit(&evidence)
			err := hotPathAcceptanceError(evidence, mediumOfferedQPS, mediumMeasuredRounds)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}

	t.Run("CI scaled pacing tolerance", func(t *testing.T) {
		evidence := passing
		evidence.OfferedQPS = mediumCIAcceptanceQPS
		evidence.MeasuredDurationMS = float64(evidence.Messages) / mediumCIAcceptanceQPS * 1000
		evidence.IngressPerSecond = float64(mediumCIAcceptanceQPS) * mediumCIMinIngressFraction
		if err := hotPathAcceptanceError(evidence, mediumCIAcceptanceQPS, mediumMeasuredRounds); err != nil {
			t.Fatalf("CI-scaled evidence rejected: %v", err)
		}
		evidence.IngressPerSecond = float64(mediumCIAcceptanceQPS)*mediumCIMinIngressFraction - 0.001
		if err := hotPathAcceptanceError(evidence, mediumCIAcceptanceQPS, mediumMeasuredRounds); err == nil || !strings.Contains(err.Error(), "acceptance ingress") {
			t.Fatalf("below-tolerance ingress error = %v, want acceptance ingress", err)
		}
	})

	t.Run("overdrive proves target margin", func(t *testing.T) {
		evidence := passing
		evidence.OfferedQPS = mediumOfferedQPS + 500
		evidence.IngressPerSecond = mediumOfferedQPS + 250
		if err := hotPathAcceptanceError(evidence, mediumOfferedQPS, mediumMeasuredRounds); err != nil {
			t.Fatalf("overdrive evidence rejected: %v", err)
		}
	})

	t.Run("allocation allowance uses paced duration", func(t *testing.T) {
		evidence := passing
		evidence.OfferedQPS = mediumCIAcceptanceQPS
		evidence.IngressPerSecond = mediumCIAcceptanceQPS
		evidence.MeasuredDurationMS = float64(evidence.Messages) / mediumCIAcceptanceQPS * 2000
		wantPerMessage := float64(440_000)
		if got := maxAcceptedAllocatedBytes(evidence) / float64(evidence.Messages); got != wantPerMessage {
			t.Fatalf("CI allocation allowance = %.0f bytes/message, want %.0f", got, wantPerMessage)
		}
		evidence.AllocatedBytes = maxAcceptedAllocatedBytes(evidence)
		if err := hotPathAcceptanceError(evidence, mediumCIAcceptanceQPS, mediumMeasuredRounds); err != nil {
			t.Fatalf("bounded CI allocation rejected: %v", err)
		}
		evidence.AllocatedBytes++
		if err := hotPathAcceptanceError(evidence, mediumCIAcceptanceQPS, mediumMeasuredRounds); err == nil || !strings.Contains(err.Error(), "allocated bytes/message") {
			t.Fatalf("slow-drain allocation error = %v, want allocated bytes/message", err)
		}
	})

}

func TestPrimePersonMessagesUseMeasuredSenders(t *testing.T) {
	personUIDs := make([]string, mediumSenderConnections*2)
	for index := range personUIDs {
		personUIDs[index] = "person"
	}
	messages := buildPrimeMessages(nil, personUIDs)
	for index, message := range messages {
		want := mediumSenderUID(index % mediumSenderConnections)
		if got := primeSenderUID(message); got != want {
			t.Fatalf("prime sender at person message %d = %q, want measured sender %q", index, got, want)
		}
	}
}

func TestPressureSamplerObservesCompleteClusterAggregateHeap(t *testing.T) {
	sampler := &pressureSampler{}
	sampler.observeAggregateHeap([]hotPathMetricValues{
		{heapBytes: 100},
		{heapBytes: 200},
		{heapBytes: 300},
	})
	if got := sampler.state.maxAggregateHeapBytes; got != 600 {
		t.Fatalf("aggregate heap = %.0f, want 600", got)
	}
	sampler.observeAggregateHeap([]hotPathMetricValues{
		{heapBytes: 50},
		{heapBytes: 50},
		{heapBytes: 50},
	})
	if got := sampler.state.maxAggregateHeapBytes; got != 600 {
		t.Fatalf("aggregate heap peak regressed to %.0f, want 600", got)
	}
}

func TestPressureSamplerObservesPermissionSoakMetrics(t *testing.T) {
	transportLabels := map[string]string{"module": "transport", "task": "rpc_executor", "kind": "pool"}
	permissionLabels := map[string]string{"module": "message", "task": "permission_batch", "kind": "burst"}
	slotPermissionLabels := map[string]string{"module": "slot", "task": "permission_batch", "kind": "burst"}
	values := metricValues([]suite.MetricSample{
		{Name: "wukongim_goroutine_pool_busy_tasks", Labels: transportLabels, Value: 8},
		{Name: "wukongim_goroutine_pool_capacity", Labels: transportLabels, Value: 16},
		{Name: "wukongim_goroutine_pool_queue_depth", Labels: transportLabels, Value: 4},
		{Name: "wukongim_goroutine_pool_queue_capacity", Labels: transportLabels, Value: 32},
		{Name: "wukongim_goroutines_active", Labels: permissionLabels, Value: 7},
		{Name: "wukongim_goroutines_active", Labels: slotPermissionLabels, Value: 2},
		{Name: "wukongim_runtime_pool_queue_depth", Labels: map[string]string{"component": "transport", "pool": "service", "queue": "slot channel metadata", "priority": "none"}, Value: 3},
		{Name: "wukongim_runtime_pool_queue_depth", Labels: map[string]string{"component": "transport", "pool": "service", "queue": "slot subscriber metadata", "priority": "none"}, Value: 5},
		{Name: "wukongim_runtime_pool_queue_capacity", Labels: map[string]string{"component": "transport", "pool": "service", "queue": "slot channel metadata", "priority": "none"}, Value: 16},
		{Name: "wukongim_runtime_pool_queue_capacity", Labels: map[string]string{"component": "transport", "pool": "service", "queue": "slot subscriber metadata", "priority": "none"}, Value: 16},
		{Name: "wukongim_runtime_pool_inflight", Labels: map[string]string{"component": "transport", "pool": "slot channel metadata"}, Value: 3},
		{Name: "wukongim_runtime_pool_inflight", Labels: map[string]string{"component": "transport", "pool": "slot subscriber metadata"}, Value: 4},
		{Name: "wukongim_channelappend_router_group_inflight", Value: 144},
		{Name: "wukongim_channelappend_router_group_capacity", Value: 192},
	})
	if !values.transportRPCMetricsPresent {
		t.Fatal("transport RPC pool metrics not detected")
	}
	if values.transportRPCBusy != 8 || values.transportRPCCapacity != 16 || values.transportRPCQueueDepth != 4 || values.transportRPCQueueCapacity != 32 {
		t.Fatalf("transport RPC values = %+v", values)
	}
	if values.permissionBatchActive != 9 {
		t.Fatalf("permission batch active = %.0f, want 9", values.permissionBatchActive)
	}
	if values.permissionSlotRPCInflight != 7 {
		t.Fatalf("permission Slot RPC inflight = %.0f, want 7", values.permissionSlotRPCInflight)
	}
	if values.permissionSlotRPCQueueDepth != 8 || values.permissionSlotRPCQueueCapacity != 32 {
		t.Fatalf("permission Slot RPC queue = %.0f/%.0f, want 8/32", values.permissionSlotRPCQueueDepth, values.permissionSlotRPCQueueCapacity)
	}
	if values.routerGroupInflight != 144 || values.routerGroupCapacity != 192 {
		t.Fatalf("router group pressure = %.0f/%.0f, want 144/192", values.routerGroupInflight, values.routerGroupCapacity)
	}

	sampler := &pressureSampler{}
	sampler.observeValues(values)
	if sampler.state.maxTransportRPCBusyRatio != 0.5 || sampler.state.maxTransportRPCQueueRatio != 0.125 {
		t.Fatalf("transport RPC pressure = %+v, want busy 0.5 queue 0.125", sampler.state)
	}
	if sampler.state.maxPermissionBatchActive != 9 {
		t.Fatalf("permission batch peak = %.0f, want 9", sampler.state.maxPermissionBatchActive)
	}
	if sampler.state.maxPermissionSlotRPCInflight != 7 {
		t.Fatalf("permission Slot RPC inflight peak = %.0f, want 7", sampler.state.maxPermissionSlotRPCInflight)
	}
	if sampler.state.maxPermissionSlotRPCQueueRatio != 0.25 {
		t.Fatalf("permission Slot RPC queue ratio = %.2f, want 0.25", sampler.state.maxPermissionSlotRPCQueueRatio)
	}
	if sampler.state.maxRouterGroupInflight != 144 || sampler.state.maxRouterGroupCapacity != 192 || sampler.state.maxRouterGroupRatio != 0.75 {
		t.Fatalf("router group pressure peak = %.0f/%.0f ratio %.2f, want 144/192 ratio 0.75", sampler.state.maxRouterGroupInflight, sampler.state.maxRouterGroupCapacity, sampler.state.maxRouterGroupRatio)
	}
}

func TestPressureSamplerObservesChannelReactorAndStoreWorkerPressure(t *testing.T) {
	values := metricValues([]suite.MetricSample{
		{Name: "wukongim_runtime_pool_queue_depth", Labels: map[string]string{"component": "channel", "pool": "reactor_0", "queue": "mailbox", "priority": "normal"}, Value: 12},
		{Name: "wukongim_runtime_pool_queue_capacity", Labels: map[string]string{"component": "channel", "pool": "reactor_0", "queue": "mailbox", "priority": "normal"}, Value: 16},
		{Name: "wukongim_runtime_pool_queue_depth", Labels: map[string]string{"component": "channel", "pool": "reactor_1", "queue": "mailbox", "priority": "high"}, Value: 2},
		{Name: "wukongim_runtime_pool_queue_capacity", Labels: map[string]string{"component": "channel", "pool": "reactor_1", "queue": "mailbox", "priority": "high"}, Value: 16},
		{Name: "wukongim_runtime_pool_queue_depth", Labels: map[string]string{"component": "channel", "pool": "channelv2-store-append", "queue": "worker", "priority": "none"}, Value: 24},
		{Name: "wukongim_runtime_pool_queue_capacity", Labels: map[string]string{"component": "channel", "pool": "channelv2-store-append", "queue": "worker", "priority": "none"}, Value: 32},
		{Name: "wukongim_runtime_pool_inflight", Labels: map[string]string{"component": "channel", "pool": "channelv2-store-append"}, Value: 8},
		{Name: "wukongim_runtime_pool_workers", Labels: map[string]string{"component": "channel", "pool": "channelv2-store-append"}, Value: 16},
		{Name: "wukongim_runtime_pool_queue_depth", Labels: map[string]string{"component": "channel", "pool": "channelv2-store-apply", "queue": "worker", "priority": "none"}, Value: 12},
		{Name: "wukongim_runtime_pool_queue_capacity", Labels: map[string]string{"component": "channel", "pool": "channelv2-store-apply", "queue": "worker", "priority": "none"}, Value: 48},
		{Name: "wukongim_runtime_pool_inflight", Labels: map[string]string{"component": "channel", "pool": "channelv2-store-apply"}, Value: 18},
		{Name: "wukongim_runtime_pool_workers", Labels: map[string]string{"component": "channel", "pool": "channelv2-store-apply"}, Value: 24},
	})

	if values.channelReactorMailboxRatio != 0.75 {
		t.Fatalf("reactor mailbox ratio = %.2f, want 0.75", values.channelReactorMailboxRatio)
	}
	if values.channelStoreAppendQueueRatio != 0.75 || values.channelStoreAppendWorkerRatio != 0.5 {
		t.Fatalf("store append pressure = queue %.2f workers %.2f, want 0.75/0.5", values.channelStoreAppendQueueRatio, values.channelStoreAppendWorkerRatio)
	}
	if values.channelStoreApplyQueueRatio != 0.25 || values.channelStoreApplyWorkerRatio != 0.75 {
		t.Fatalf("store apply pressure = queue %.2f workers %.2f, want 0.25/0.75", values.channelStoreApplyQueueRatio, values.channelStoreApplyWorkerRatio)
	}

	sampler := &pressureSampler{}
	sampler.observeValues(values)
	if sampler.state.maxChannelReactorMailboxRatio != 0.75 ||
		sampler.state.maxChannelStoreAppendQueueRatio != 0.75 ||
		sampler.state.maxChannelStoreAppendWorkerRatio != 0.5 ||
		sampler.state.maxChannelStoreApplyQueueRatio != 0.25 ||
		sampler.state.maxChannelStoreApplyWorkerRatio != 0.75 {
		t.Fatalf("channel pressure snapshot = %+v", sampler.state)
	}
}

func TestHotPathCountersObservePermissionSlotRPCServerMetrics(t *testing.T) {
	var counters hotPathCounters
	for _, sample := range []suite.MetricSample{
		{Name: "wukongim_transport_rpc_total", Labels: map[string]string{"service": "slot channel metadata", "result": "ok"}, Value: 20},
		{Name: "wukongim_transport_rpc_total", Labels: map[string]string{"service": "slot channel metadata", "result": "error"}, Value: 2},
		{Name: "wukongim_transport_rpc_total", Labels: map[string]string{"service": "slot subscriber metadata", "result": "ok"}, Value: 40},
		{Name: "wukongim_transport_rpc_total", Labels: map[string]string{"service": "slot permission metadata batch", "result": "ok"}, Value: 10},
		{Name: "wukongim_runtime_pool_admission_total", Labels: map[string]string{"component": "transport", "pool": "service", "queue": "slot channel metadata", "priority": "none", "result": "busy"}, Value: 3},
		{Name: "wukongim_runtime_pool_admission_total", Labels: map[string]string{"component": "transport", "pool": "service", "queue": "slot subscriber metadata", "priority": "none", "result": "busy"}, Value: 2},
		{Name: "wukongim_runtime_pool_admission_total", Labels: map[string]string{"component": "transport", "pool": "service", "queue": "slot permission metadata batch", "priority": "none", "result": "busy"}, Value: 1},
		{Name: "wukongim_runtime_pool_admission_total", Labels: map[string]string{"component": "channel", "pool": "channelv2-store-checkpoint", "queue": "worker", "priority": "none", "result": "full"}, Value: 23},
		{Name: "wukongim_channelv2_worker_admission_total", Labels: map[string]string{"pool": "channelv2-rpc", "kind": "rpc_pull", "result": "full"}, Value: 5},
		{Name: "wukongim_channelv2_worker_admission_total", Labels: map[string]string{"pool": "channelv2-rpc", "kind": "rpc_pull_hint", "result": "full"}, Value: 7},
		{Name: "wukongim_channelv2_worker_admission_total", Labels: map[string]string{"pool": "channelv2-rpc", "kind": "rpc_pull", "result": "paced"}, Value: 11},
		{Name: "wukongim_channelv2_worker_admission_total", Labels: map[string]string{"pool": "channelv2-rpc", "kind": "rpc_pull_hint", "result": "paced"}, Value: 13},
		{Name: "wukongim_channelv2_worker_admission_total", Labels: map[string]string{"pool": "channelv2-store-apply", "kind": "store_apply", "result": "full"}, Value: 100},
		{Name: "wukongim_channelv2_worker_admission_total", Labels: map[string]string{"pool": "channelv2-store-apply", "kind": "rpc_pull", "result": "paced"}, Value: 17},
		{Name: "wukongim_channelv2_worker_task_duration_seconds_count", Labels: map[string]string{"kind": "store_apply", "result": "ok"}, Value: 41},
		{Name: "wukongim_channelv2_worker_task_duration_seconds_count", Labels: map[string]string{"kind": "store_checkpoint", "result": "ok"}, Value: 37},
		{Name: "wukongim_channelv2_pull_total", Labels: map[string]string{"result": "ok", "empty": "true"}, Value: 43},
		{Name: "wukongim_channelv2_pull_total", Labels: map[string]string{"result": "ok", "empty": "false"}, Value: 47},
		{Name: "wukongim_channelv2_pull_total", Labels: map[string]string{"result": "err", "empty": "false"}, Value: 3},
		{Name: "wukongim_channelv2_pull_hint_total", Labels: map[string]string{"reason": "append", "result": "paced"}, Value: 19},
		{Name: "wukongim_channelv2_pull_hint_total", Labels: map[string]string{"reason": "resume", "result": "paced"}, Value: 23},
		{Name: "wukongim_transport_rpc_total", Labels: map[string]string{"service": "other", "result": "error"}, Value: 100},
		{Name: "wukongim_storage_commit_batch_duration_seconds_count", Labels: map[string]string{"store": "message", "stage": "commit", "result": "ok"}, Value: 12},
		{Name: "wukongim_storage_commit_batch_duration_seconds_sum", Labels: map[string]string{"store": "message", "stage": "commit", "result": "ok"}, Value: 1.5},
		{Name: "wukongim_storage_commit_batch_requests_sum", Labels: map[string]string{"store": "message"}, Value: 24},
		{Name: "wukongim_storage_commit_batch_records_sum", Labels: map[string]string{"store": "message"}, Value: 96},
		{Name: "wukongim_storage_commit_batch_bytes_sum", Labels: map[string]string{"store": "message"}, Value: 4096},
		{Name: "wukongim_storage_commit_request_duration_seconds_count", Labels: map[string]string{"store": "message", "lane": "leader_append", "result": "ok"}, Value: 10},
		{Name: "wukongim_storage_commit_request_duration_seconds_sum", Labels: map[string]string{"store": "message", "lane": "leader_append", "result": "ok"}, Value: 2},
		{Name: "wukongim_storage_commit_request_duration_seconds_count", Labels: map[string]string{"store": "message", "lane": "follower_apply", "result": "ok"}, Value: 14},
		{Name: "wukongim_storage_commit_request_duration_seconds_sum", Labels: map[string]string{"store": "message", "lane": "follower_apply", "result": "ok"}, Value: 3},
		{Name: "wukongim_storage_pebble_wal_bytes_in", Labels: map[string]string{"store": "channel_log"}, Value: 1000},
		{Name: "wukongim_storage_pebble_wal_bytes_written", Labels: map[string]string{"store": "channel_log"}, Value: 1100},
		{Name: "wukongim_storage_pebble_flush_bytes_written", Labels: map[string]string{"store": "channel_log"}, Value: 900},
		{Name: "wukongim_storage_pebble_compaction_bytes_read", Labels: map[string]string{"store": "channel_log"}, Value: 1700},
		{Name: "wukongim_storage_pebble_compaction_bytes_written", Labels: map[string]string{"store": "channel_log"}, Value: 1500},
		{Name: "wukongim_storage_pebble_sstable_size_bytes", Labels: map[string]string{"store": "channel_log"}, Value: 800},
		{Name: "wukongim_storage_message_idempotency_negative_filter_skips", Labels: map[string]string{"store": "channel_log"}, Value: 1200},
		{Name: "wukongim_storage_message_idempotency_point_reads", Labels: map[string]string{"store": "channel_log"}, Value: 18},
		{Name: "wukongim_delivery_recipient_worker_process_total", Labels: map[string]string{"result": "ok"}, Value: 80},
		{Name: "wukongim_delivery_recipient_worker_process_total", Labels: map[string]string{"result": "error"}, Value: 2},
		{Name: "wukongim_delivery_recipient_worker_process_recipients_sum", Labels: map[string]string{"result": "ok"}, Value: 96},
	} {
		observeHotPathCounterSample(&counters, sample)
	}
	if counters.permissionSlotRPCCalls != 72 || counters.permissionSlotRPCErrors != 2 {
		t.Fatalf("permission Slot RPC counters = calls %.0f errors %.0f, want 72/2", counters.permissionSlotRPCCalls, counters.permissionSlotRPCErrors)
	}
	if counters.permissionSlotRPCAdmissionErrors != 6 {
		t.Fatalf("permission Slot RPC admission errors = %.0f, want 6", counters.permissionSlotRPCAdmissionErrors)
	}
	if counters.channelStoreApplyTasks != 41 || counters.channelStoreApplyAdmissionFull != 100 || counters.channelStoreApplyPullPaced != 17 || counters.channelStoreCheckpointTasks != 37 || counters.channelStoreCheckpointAdmissionFull != 23 {
		t.Fatalf(
			"channel store task counters = apply %.0f apply_full %.0f apply_pull_paced %.0f checkpoint %.0f checkpoint_full %.0f, want 41/100/17/37/23",
			counters.channelStoreApplyTasks,
			counters.channelStoreApplyAdmissionFull,
			counters.channelStoreApplyPullPaced,
			counters.channelStoreCheckpointTasks,
			counters.channelStoreCheckpointAdmissionFull,
		)
	}
	if counters.channelRPCPullAdmissionFull != 5 || counters.channelRPCHintAdmissionFull != 7 {
		t.Fatalf("typed Channel RPC full admissions = pull %.0f hint %.0f, want 5/7", counters.channelRPCPullAdmissionFull, counters.channelRPCHintAdmissionFull)
	}
	if counters.channelRPCPullPaced != 11 || counters.channelRPCHintPaced != 13 {
		t.Fatalf("typed Channel RPC paced admissions = pull %.0f hint %.0f, want 11/13", counters.channelRPCPullPaced, counters.channelRPCHintPaced)
	}
	if counters.channelPullOKEmpty != 43 || counters.channelPullOKRecords != 47 || counters.channelPullError != 3 {
		t.Fatalf("Channel Pull results = empty %.0f records %.0f error %.0f, want 43/47/3", counters.channelPullOKEmpty, counters.channelPullOKRecords, counters.channelPullError)
	}
	if counters.channelAppendHintPaced != 19 || counters.channelResumeHintPaced != 23 {
		t.Fatalf("Channel PullHint paced by reason = append %.0f resume %.0f, want 19/23", counters.channelAppendHintPaced, counters.channelResumeHintPaced)
	}
	if counters.messagePhysicalCommits != 12 || counters.messageCommitBatchRequests != 24 || counters.messageCommitBatchRecords != 96 || counters.messageCommitBatchBytes != 4096 || counters.messageCommitSeconds != 1.5 {
		t.Fatalf("message physical commit counters = %+v", counters.messageCommitSummary())
	}
	if counters.messageLeaderCommitRequests != 10 || counters.messageFollowerCommitRequests != 14 || counters.messageLeaderCommitSeconds != 2 || counters.messageFollowerCommitSeconds != 3 {
		t.Fatalf("message request counters = %+v", counters.messageCommitSummary())
	}
	if counters.messageWALBytesIn != 1000 || counters.messageWALBytesWritten != 1100 || counters.messageFlushBytesWritten != 900 || counters.messageCompactionBytesRead != 1700 || counters.messageCompactionBytesWritten != 1500 || counters.messageSSTableSizeBytes != 800 {
		t.Fatalf("message storage counters = %+v", counters.messageCommitSummary())
	}
	if counters.messageIdempotencyNegativeSkips != 1200 || counters.messageIdempotencyPointReads != 18 {
		t.Fatalf("message idempotency counters = skips %.0f point_reads %.0f, want 1200/18", counters.messageIdempotencyNegativeSkips, counters.messageIdempotencyPointReads)
	}
	if counters.recipientProcessOK != 80 || counters.recipientProcessRecipientsOK != 96 || counters.recipientProcessError != 2 {
		t.Fatalf("recipient process counters = %+v", counters.recipientProcessSummary())
	}
}

func TestPermissionSoakChannelIDsIncludeLocalAndRemoteSlotLeaders(t *testing.T) {
	hashSlotLeaders := make([]uint64, mediumPhysicalHashSlots)
	for hashSlot := range hashSlotLeaders {
		hashSlotLeaders[hashSlot] = uint64(hashSlot%mediumReplicaCount + 1)
	}
	channels, err := permissionSoakChannelIDs(mediumSenderConnections*4, hashSlotLeaders)
	if err != nil {
		t.Fatalf("build permission soak channels: %v", err)
	}
	if len(channels) != mediumSenderConnections*4 {
		t.Fatalf("channels = %d, want %d", len(channels), mediumSenderConnections*4)
	}
	localChannels := 0
	remoteChannels := 0
	for index, channelID := range channels {
		ingressNodeID := uint64(index%mediumSenderConnections%mediumReplicaCount + 1)
		hashSlot := permissionSoakChannelHashSlot(channelID)
		if leaderID := hashSlotLeaders[hashSlot]; leaderID == ingressNodeID {
			localChannels++
		} else {
			remoteChannels++
		}
	}
	if localChannels == 0 || remoteChannels == 0 {
		t.Fatalf("permission soak route mix = local %d remote %d, want both positive", localChannels, remoteChannels)
	}
}

func TestPermissionSoakLatencyAndInflightTrackingStayBounded(t *testing.T) {
	histogram := newBoundedLatencyHistogram()
	for _, latency := range []time.Duration{time.Millisecond, 2 * time.Millisecond, 100 * time.Millisecond, 20 * time.Second} {
		histogram.observe(latency)
	}
	if got := histogram.percentile(0.50); got != 2*time.Millisecond {
		t.Fatalf("P50 = %s, want 2ms", got)
	}
	if got := histogram.percentile(0.99); got != mediumPermissionSoakMaxLatency {
		t.Fatalf("P99 = %s, want bounded %s", got, mediumPermissionSoakMaxLatency)
	}
	if got := histogram.maximum(); got != 20*time.Second {
		t.Fatalf("max = %s, want exact 20s", got)
	}

	tracker := newPermissionSoakTracker()
	tracker.begin("message-1", 2)
	if got := tracker.pending.Load(); got != 1 {
		t.Fatalf("pending after begin = %d, want 1", got)
	}
	if _, err := tracker.observeSendack("message-1"); err != nil {
		t.Fatalf("observe sendack: %v", err)
	}
	if _, err := tracker.observeRecv("message-1"); err != nil {
		t.Fatalf("observe first recv: %v", err)
	}
	if _, err := tracker.observeRecv("message-1"); err != nil {
		t.Fatalf("observe second recv: %v", err)
	}
	if got := tracker.pending.Load(); got != 0 {
		t.Fatalf("pending after all observations = %d, want 0", got)
	}
	if _, err := tracker.observeRecv("message-1"); err == nil || !strings.Contains(err.Error(), "no send start") {
		t.Fatalf("late observation error = %v, want no send start", err)
	}
}

func TestScaleGroupChannelCounts(t *testing.T) {
	tests := []struct {
		total int
		want  []int
	}{
		{total: mediumGroupChannelCount, want: []int{1, 1, 1, 1}},
		{total: mediumCloudGroupChannelCount, want: []int{3_321, 1_186, 237, 256}},
	}
	for _, test := range tests {
		got := scaleGroupChannelCounts(test.total)
		if len(got) != len(test.want) {
			t.Fatalf("total %d counts = %v, want %v", test.total, got, test.want)
		}
		sum := 0
		for index := range got {
			sum += got[index]
			if got[index] != test.want[index] {
				t.Fatalf("total %d counts = %v, want %v", test.total, got, test.want)
			}
		}
		if sum != test.total {
			t.Fatalf("total %d counts sum = %d, want %d", test.total, sum, test.total)
		}
	}
}

func TestBoundedPositiveEnvInt(t *testing.T) {
	const name = "WK_E2E_MEDIUM_RECIPIENT_TEST_VALUE"
	t.Setenv(name, "")
	if got := boundedPositiveEnvInt(t, name, 80, 1, 200); got != 80 {
		t.Fatalf("fallback = %d, want 80", got)
	}
	t.Setenv(name, "120")
	if got := boundedPositiveEnvInt(t, name, 80, 1, 200); got != 120 {
		t.Fatalf("parsed = %d, want 120", got)
	}
}
