//go:build e2e

package medium_recipient_hotpath

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
)

type permissionSoakConfig struct {
	enabled       bool
	duration      time.Duration
	offeredQPS    int
	groupChannels int
}

// permissionSoakEvidence is the bounded, machine-readable result of the
// long-running public-protocol permission pressure gate.
type permissionSoakEvidence struct {
	Schema                           string  `json:"schema"`
	ConfiguredDurationMS             float64 `json:"configured_duration_ms"`
	SendLoopDurationMS               float64 `json:"send_loop_duration_ms"`
	MeasuredDurationMS               float64 `json:"measured_duration_ms"`
	Messages                         int     `json:"messages"`
	GroupChannels                    int     `json:"group_channels"`
	ActiveGroupChannels              int     `json:"active_group_channels"`
	Senders                          int     `json:"senders"`
	Recipients                       int     `json:"recipients"`
	OfferedQPS                       int     `json:"offered_qps"`
	IngressPerSecond                 float64 `json:"ingress_per_second"`
	CompletionPerSecond              float64 `json:"completion_per_second"`
	SendackP50MS                     float64 `json:"sendack_p50_ms"`
	SendackP99MS                     float64 `json:"sendack_p99_ms"`
	SendackMaxMS                     float64 `json:"sendack_max_ms"`
	RecvP99MS                        float64 `json:"recv_p99_ms"`
	RecvMaxMS                        float64 `json:"recv_max_ms"`
	TransportRPCMetricNodes          int     `json:"transport_rpc_metric_nodes"`
	MaxTransportRPCQueueRatio        float64 `json:"max_transport_rpc_queue_ratio"`
	MaxTransportRPCBusyRatio         float64 `json:"max_transport_rpc_busy_ratio"`
	TransportRPCRejected             float64 `json:"transport_rpc_rejected"`
	PermissionSlotRPCCalls           float64 `json:"permission_slot_rpc_calls"`
	PermissionSlotRPCErrors          float64 `json:"permission_slot_rpc_errors"`
	PermissionSlotRPCAdmissionErrors float64 `json:"permission_slot_rpc_admission_errors"`
	MaxPermissionSlotRPCQueueRatio   float64 `json:"max_permission_slot_rpc_queue_ratio"`
	MaxPermissionSlotRPCInflight     float64 `json:"max_permission_slot_rpc_inflight"`
	PermissionBatchStarted           float64 `json:"permission_batch_started"`
	PermissionBatchPanics            float64 `json:"permission_batch_panics"`
	MaxPermissionBatchActive         float64 `json:"max_permission_batch_active"`
	MembershipMutationRows           float64 `json:"membership_mutation_rows"`
	PluginReceiveAccepted            float64 `json:"plugin_receive_enqueue_accepted"`
	PluginReceiveFull                float64 `json:"plugin_receive_enqueue_full"`
	PluginReceiveClosed              float64 `json:"plugin_receive_enqueue_closed"`
	PluginReceiveInvokeOK            float64 `json:"plugin_receive_invoke_ok"`
	PluginReceiveInvokeError         float64 `json:"plugin_receive_invoke_error"`
	MaxHeapBytes                     float64 `json:"max_heap_bytes"`
	MaxAggregateHeapBytes            float64 `json:"max_aggregate_heap_bytes"`
	AllocatedBytes                   float64 `json:"allocated_bytes"`
	GCCountDelta                     float64 `json:"gc_count_delta"`
	MetricSamples                    int     `json:"metric_samples"`
	MetricSampleErrors               int     `json:"metric_sample_errors"`
	PendingMessages                  int64   `json:"pending_messages"`
	Drained                          bool    `json:"drained"`
	ProcessContinuous                bool    `json:"process_continuous"`
}

// permissionSoakFailureEvidence preserves bounded public-metric evidence when
// the long-running gate fails before it can emit its complete acceptance row.
type permissionSoakFailureEvidence struct {
	Schema                           string  `json:"schema"`
	Phase                            string  `json:"phase"`
	Error                            string  `json:"error"`
	CompletedSendCalls               int     `json:"completed_send_calls"`
	ElapsedMS                        float64 `json:"elapsed_ms"`
	IngressPerSecond                 float64 `json:"ingress_per_second"`
	SendackP99MS                     float64 `json:"sendack_p99_ms"`
	RecvP99MS                        float64 `json:"recv_p99_ms"`
	PendingMessages                  int64   `json:"pending_messages"`
	TransportRPCRejected             float64 `json:"transport_rpc_rejected"`
	PermissionSlotRPCCalls           float64 `json:"permission_slot_rpc_calls"`
	PermissionSlotRPCErrors          float64 `json:"permission_slot_rpc_errors"`
	PermissionSlotRPCAdmissionErrors float64 `json:"permission_slot_rpc_admission_errors"`
	MaxTransportRPCQueueRatio        float64 `json:"max_transport_rpc_queue_ratio"`
	MaxTransportRPCBusyRatio         float64 `json:"max_transport_rpc_busy_ratio"`
	MaxPermissionSlotRPCQueueRatio   float64 `json:"max_permission_slot_rpc_queue_ratio"`
	MaxPermissionSlotRPCInflight     float64 `json:"max_permission_slot_rpc_inflight"`
	PermissionBatchStarted           float64 `json:"permission_batch_started"`
	PermissionBatchPanics            float64 `json:"permission_batch_panics"`
	MaxPermissionBatchActive         float64 `json:"max_permission_batch_active"`
	MembershipMutationRows           float64 `json:"membership_mutation_rows"`
	MaxHeapBytes                     float64 `json:"max_heap_bytes"`
	MaxAggregateHeapBytes            float64 `json:"max_aggregate_heap_bytes"`
	AllocatedBytes                   float64 `json:"allocated_bytes"`
	GCCountDelta                     float64 `json:"gc_count_delta"`
	MetricSamples                    int     `json:"metric_samples"`
	MetricSampleErrors               int     `json:"metric_sample_errors"`
	ProcessContinuous                bool    `json:"process_continuous"`
	CounterCaptureError              string  `json:"counter_capture_error,omitempty"`
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
	channels := preparePermissionSoakChannels(t, setupCtx, cluster.MustNode(1), config.groupChannels, convergence.HashSlotLeaders)
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
	senderResults := startPermissionSoakSenderReaders(senders, senderCounts, tracker)
	receiverResults := startPermissionSoakReceiverReaders(recipients, receiverCounts, tracker)
	sampler := newPressureSampler(cluster, mediumMetricSampleInterval)
	sampler.start()
	defer sampler.stop()
	counterStart := mustCaptureHotPathCounters(t, cluster)
	payload := bytes.Repeat([]byte("s"), mediumPayloadBytes)

	measuredStart := time.Now()
	for index := 0; index < messageCount; index++ {
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
				index,
				time.Since(measuredStart),
				err,
			)
			t.Fatalf("permission soak send %s: %v\n%s", clientMsgNo, err, cluster.DumpDiagnostics())
		}
	}
	sendLoopDuration := time.Since(measuredStart)

	for range senders {
		if err := <-senderResults; err != nil {
			t.Fatalf("permission soak sender read: %v\n%s", err, cluster.DumpDiagnostics())
		}
	}
	for range recipients {
		if err := <-receiverResults; err != nil {
			t.Fatalf("permission soak receiver read: %v\n%s", err, cluster.DumpDiagnostics())
		}
	}
	measuredDuration := time.Since(measuredStart)

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
		t.Fatal(err)
	}
}

func logPermissionSoakFailureEvidence(
	t *testing.T,
	cluster *suite.StartedCluster,
	tracker *permissionSoakTracker,
	sampler *pressureSampler,
	counterStart hotPathCounters,
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
		Phase:                            "send",
		Error:                            failure.Error(),
		CompletedSendCalls:               completedSendCalls,
		ElapsedMS:                        milliseconds(elapsed),
		IngressPerSecond:                 ingressPerSecond,
		SendackP99MS:                     milliseconds(tracker.sendacks.percentile(0.99)),
		RecvP99MS:                        milliseconds(tracker.recvs.percentile(0.99)),
		PendingMessages:                  tracker.pending.Load(),
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
	node *suite.StartedNode,
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
	client := benchtarget.NewClient(benchtarget.Config{APIAddrs: []string{"http://" + node.APIAddr()}})
	if err := client.UpsertChannels(ctx, benchmodel.BatchChannelsRequest{
		RunID: "wkrc-permission-soak", BatchID: "channels", Upsert: true, Channels: channelItems,
	}); err != nil {
		t.Fatalf("prepare permission soak channels: %v\n%s", err, node.DumpDiagnostics())
	}
	for start := 0; start < len(subscriberItems); start += 200 {
		end := min(start+200, len(subscriberItems))
		if err := client.AddSubscribers(ctx, benchmodel.BatchSubscribersRequest{
			RunID: "wkrc-permission-soak", BatchID: fmt.Sprintf("subscribers-%04d", start/200), Items: subscriberItems[start:end],
		}); err != nil {
			t.Fatalf("prepare permission soak subscribers [%d,%d): %v\n%s", start, end, err, node.DumpDiagnostics())
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
	tracker *permissionSoakTracker,
) <-chan error {
	results := make(chan error, len(clients))
	for index, client := range clients {
		client := client
		count := counts[index]
		go func() {
			for range count {
				recv, err := client.ReadRecv()
				if err != nil {
					results <- err
					return
				}
				if _, err := tracker.observeRecv(recv.ClientMsgNo); err != nil {
					results <- err
					return
				}
				if err := client.RecvAck(recv.MessageID, recv.MessageSeq); err != nil {
					results <- err
					return
				}
			}
			results <- nil
		}()
	}
	return results
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
