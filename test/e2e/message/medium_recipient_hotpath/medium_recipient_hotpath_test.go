//go:build e2e

package medium_recipient_hotpath

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	benchtarget "github.com/WuKongIM/WuKongIM/internal/bench/target"
	benchmodel "github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/pelletier/go-toml/v2"
)

const (
	mediumEvidenceSchema                       = "wukongim/local-medium-rc-hifi-evidence/v5"
	mediumPhysicalHashSlots                    = 256
	mediumLogicalSlots                         = 10
	mediumReplicaCount                         = 3
	mediumSlotTickInterval                     = 50 * time.Millisecond
	mediumSlotHeartbeatTick                    = 2
	mediumSlotElectionTick                     = 20
	mediumMessageCount                         = 250
	mediumRecipientRows                        = 19_650
	mediumOnlineRoutes                         = 2_545
	mediumPayloadBytes                         = 256
	mediumPrimeConcurrency                     = 16
	mediumMeasuredRounds                       = 80
	mediumOfferedQPS                           = 4_500
	mediumCIAcceptanceQPS                      = 500
	mediumMinOfferedQPS                        = 500
	mediumRecipientPlanSize                    = 512
	mediumSenderConnections                    = 25
	mediumGroupSenders                         = 5
	mediumGroupChannelCount                    = 4
	mediumCloudGroupChannelCount               = 5_000
	mediumChannelRPCWorkers                    = 96
	mediumChannelRPCBatchMaxItems              = 8
	mediumCommitCoordinatorShards              = 1
	mediumSenderUIDPrefix                      = "wkrc-hifi-sender"
	mediumCIMinIngressFraction                 = 0.995
	mediumMaxAllocatedBytesPerMessage          = 360_000
	mediumMaxBackgroundAllocatedBytesPerSecond = 40_000_000
	mediumMaxGCPerMessage                      = 0.0075
	mediumMaxHeapBytes                         = 512 << 20
	// The process-level gate shares one host with all three nodes. A 250ms
	// full-registry scrape measurably inflated SENDACK and RECV tail latency;
	// one second remains bounded while avoiding observer-induced saturation.
	mediumMetricSampleInterval   = time.Second
	mediumCIMetricSampleInterval = time.Second
	mediumMetricFetchTimeout     = 750 * time.Millisecond
	mediumMetricFetchAttempts    = 2
)

var mediumGroupProfiles = []struct {
	messages      int
	recipients    int
	online        int
	cloudChannels int
}{
	{messages: 60, recipients: 20, online: 5, cloudChannels: 3_321},
	{messages: 42, recipients: 100, online: 15, cloudChannels: 1_186},
	{messages: 18, recipients: 500, online: 55, cloudChannels: 237},
	{messages: 5, recipients: 1_000, online: 100, cloudChannels: 256},
}

type hotPathMessage struct {
	clientSeq    uint64
	clientMsgNo  string
	channelID    string
	channelType  uint8
	groupProfile int
	groupOrdinal int
	primeSender  int
}

type hotPathRecipient struct {
	uid      string
	expected int
	client   *suite.WKProtoClient
}

type receiverResult struct {
	latencies []time.Duration
	err       error
}

type senderResult struct {
	sendackLatencies []time.Duration
	recvLatencies    []time.Duration
	err              error
}

// hotPathEvidence is the machine-readable, revision-neutral result emitted by
// the opt-in process-level gate.
type hotPathEvidence struct {
	Schema                   string   `json:"schema"`
	PhysicalHashSlots        int      `json:"physical_hash_slots"`
	LogicalSlots             int      `json:"logical_slots"`
	Replicas                 int      `json:"replicas"`
	SlotTickIntervalMS       float64  `json:"slot_tick_interval_ms"`
	SlotHeartbeatTick        int      `json:"slot_heartbeat_tick"`
	SlotElectionTick         int      `json:"slot_election_tick"`
	Messages                 int      `json:"messages"`
	RecipientRows            int      `json:"recipient_rows"`
	OnlineRoutes             int      `json:"online_routes"`
	Connections              int      `json:"connections"`
	GroupChannels            int      `json:"group_channels"`
	ActiveGroupChannels      int      `json:"active_group_channels"`
	OfferedQPS               int      `json:"offered_qps"`
	ClusterConvergenceMS     float64  `json:"cluster_convergence_ms"`
	ClusterStableWindowMS    float64  `json:"cluster_stable_window_ms"`
	SlotLeaders              []uint64 `json:"slot_leaders"`
	ColdPrimeDurationMS      float64  `json:"cold_prime_duration_ms"`
	SendLoopDurationMS       float64  `json:"send_loop_duration_ms"`
	MeasuredDurationMS       float64  `json:"measured_duration_ms"`
	CompletionDrainMS        float64  `json:"completion_drain_ms"`
	IngressPerSecond         float64  `json:"ingress_per_second"`
	CompletionPerSecond      float64  `json:"completion_per_second"`
	SendackP50MS             float64  `json:"sendack_p50_ms"`
	SendackP99MS             float64  `json:"sendack_p99_ms"`
	SendackMaxMS             float64  `json:"sendack_max_ms"`
	RecvP99MS                float64  `json:"recv_p99_ms"`
	RecvMaxMS                float64  `json:"recv_max_ms"`
	MaxGatewayQueueRatio     float64  `json:"max_gateway_queue_ratio"`
	MaxRecipientQueueRatio   float64  `json:"max_recipient_queue_ratio"`
	MaxRecipientWorkerRatio  float64  `json:"max_recipient_worker_ratio"`
	ChannelRPCMetricNodes    int      `json:"channel_rpc_metric_nodes"`
	MinChannelRPCWorkers     float64  `json:"min_channel_rpc_workers"`
	MaxChannelRPCWorkers     float64  `json:"max_channel_rpc_workers"`
	ChannelRPCBatchMaxItems  int      `json:"channel_rpc_batch_max_items"`
	ChannelRPCAdmissionFull  float64  `json:"channel_rpc_admission_full"`
	ChannelRPCPullBatches    float64  `json:"channel_rpc_pull_batches"`
	ChannelRPCPullBatchItems float64  `json:"channel_rpc_pull_batch_items"`
	ChannelRPCHintBatches    float64  `json:"channel_rpc_hint_batches"`
	ChannelRPCHintBatchItems float64  `json:"channel_rpc_hint_batch_items"`
	MembershipMutationRows   float64  `json:"membership_mutation_rows"`
	MaxChannelRPCQueueRatio  float64  `json:"max_channel_rpc_queue_ratio"`
	MaxChannelRPCWorkerRatio float64  `json:"max_channel_rpc_worker_ratio"`
	MaxAdvancePoolUtil       float64  `json:"max_advance_pool_utilization"`
	MaxAdvancePoolWaiting    float64  `json:"max_advance_pool_waiting"`
	MaxAppendPoolUtil        float64  `json:"max_append_pool_utilization"`
	MaxPostCommitPoolUtil    float64  `json:"max_post_commit_pool_utilization"`
	MaxPostCommitBacklog     float64  `json:"max_post_commit_backlog"`
	MaxPostCommitHandoffRate float64  `json:"max_post_commit_handoff_ratio"`
	MaxHeapBytes             float64  `json:"max_heap_bytes"`
	MaxAggregateHeapBytes    float64  `json:"max_aggregate_heap_bytes"`
	AllocatedBytes           float64  `json:"allocated_bytes"`
	GCCountDelta             float64  `json:"gc_count_delta"`
	PluginReceiveAccepted    float64  `json:"plugin_receive_enqueue_accepted"`
	PluginReceiveFull        float64  `json:"plugin_receive_enqueue_full"`
	PluginReceiveClosed      float64  `json:"plugin_receive_enqueue_closed"`
	PluginReceiveInvokeOK    float64  `json:"plugin_receive_invoke_ok"`
	PluginReceiveInvokeError float64  `json:"plugin_receive_invoke_error"`
	RecipientProcessError    float64  `json:"recipient_worker_process_error"`
	MetricSamples            int      `json:"metric_samples"`
	MetricSampleErrors       int      `json:"metric_sample_errors"`
	Drained                  bool     `json:"drained"`
	ProcessContinuous        bool     `json:"process_continuous"`
}

type hotPathAcceptanceLimits struct {
	maxSendackP99MS         float64
	maxHeapBytes            int64
	maxAllocatedBytesPerMsg float64
	// skipAllocatedBytesCeiling is reserved for intervals whose concurrent
	// background work is intentionally variable; heap bounds remain enforced.
	skipAllocatedBytesCeiling bool
}

func TestCloudMediumScaledRecipientHotPath(t *testing.T) {
	if os.Getenv("WK_E2E_MEDIUM_RECIPIENT_HOTPATH") != "1" {
		t.Skip("set WK_E2E_MEDIUM_RECIPIENT_HOTPATH=1 to run the bounded higher-fidelity gate")
	}
	measuredRounds := boundedPositiveEnvInt(t, "WK_E2E_MEDIUM_RECIPIENT_ROUNDS", mediumMeasuredRounds, 1, 200)
	offeredQPS := boundedPositiveEnvInt(t, "WK_E2E_MEDIUM_RECIPIENT_QPS", mediumOfferedQPS, mediumMinOfferedQPS, 20_000)
	rpcBatchMaxItems := boundedPositiveEnvInt(t, "WK_E2E_MEDIUM_RECIPIENT_RPC_BATCH_MAX_ITEMS", mediumChannelRPCBatchMaxItems, 1, 64)
	groupChannelCount := boundedPositiveEnvInt(
		t,
		"WK_E2E_MEDIUM_RECIPIENT_GROUP_CHANNELS",
		mediumGroupChannelCount,
		len(mediumGroupProfiles),
		mediumCloudGroupChannelCount,
	)
	expectedAcceptanceQPS := mediumOfferedQPS
	metricSampleInterval := mediumMetricSampleInterval
	if os.Getenv("WK_E2E_MEDIUM_RECIPIENT_CI_SCALE") == "1" {
		expectedAcceptanceQPS = mediumCIAcceptanceQPS
		metricSampleInterval = mediumCIMetricSampleInterval
	}
	if os.Getenv("WK_E2E_MEDIUM_RECIPIENT_ENFORCE_ACCEPTANCE") == "1" && offeredQPS < expectedAcceptanceQPS {
		t.Fatalf("acceptance offered QPS = %d, want at least %d", offeredQPS, expectedAcceptanceQPS)
	}

	cluster := startMediumCluster(t, rpcBatchMaxItems)
	verifyMediumRenderedRuntime(t, cluster, rpcBatchMaxItems)
	setupTimeout := 2 * time.Minute
	if groupChannelCount > 500 {
		setupTimeout = 5 * time.Minute
	}
	setupCtx, setupCancel := context.WithTimeout(context.Background(), setupTimeout)
	defer setupCancel()
	if err := cluster.WaitClusterReady(setupCtx); err != nil {
		t.Fatalf("wait for Cloud Medium-shaped cluster: %v\n%s", err, cluster.DumpDiagnostics())
	}
	setupConvergence, err := waitForMediumSlotConvergence(setupCtx, cluster)
	if err != nil {
		t.Fatalf("wait for stable actual Slot leaders before setup: %v\n%s", err, cluster.DumpDiagnostics())
	}
	t.Logf(
		"WKRC-HIFI-SLOT-CONVERGENCE phase=setup wait=%s stable=%s leaders=%v",
		setupConvergence.WaitDuration,
		setupConvergence.StableDuration,
		setupConvergence.Leaders,
	)
	var sampler *pressureSampler
	groupChannels, groupRecipients, groupOnline := prepareGroupChannels(t, setupCtx, cluster.MustNode(1), groupChannelCount)
	personRecipients := make([]string, 125)
	for i := range personRecipients {
		personRecipients[i] = fmt.Sprintf("wkrc-person-%03d", i)
	}

	baseMessages := buildMessages(groupChannels, personRecipients)
	if len(baseMessages) != mediumMessageCount || groupRecipients+len(personRecipients)*2 != mediumRecipientRows {
		t.Fatalf("fixture shape messages=%d recipient_rows=%d, want %d/%d", len(baseMessages), groupRecipients+len(personRecipients)*2, mediumMessageCount, mediumRecipientRows)
	}

	payload := bytes.Repeat([]byte("w"), mediumPayloadBytes)
	primeSender := mustConnect(t, cluster.MustNode(1), mediumSenderUID(0))
	convergence, err := waitForMediumSlotConvergence(setupCtx, cluster)
	if err != nil {
		t.Fatalf("wait for stable actual Slot leaders before cold prime: %v\n%s", err, cluster.DumpDiagnostics())
	}
	t.Logf(
		"WKRC-HIFI-SLOT-CONVERGENCE phase=cold-prime wait=%s stable=%s leaders=%v",
		convergence.WaitDuration,
		convergence.StableDuration,
		convergence.Leaders,
	)
	proveWarmupSend(t, cluster, primeSender)
	_ = primeSender.Close()
	primeMessages := buildPrimeMessages(groupChannels, personRecipients)
	coldPrimeDuration := primeHotPathChannels(t, setupCtx, cluster, primeMessages, payload)
	recipients := connectRecipients(t, cluster, groupOnline, personRecipients)
	defer closeRecipients(recipients)
	if err := waitForRecipientPresence(setupCtx, cluster, len(recipients)); err != nil {
		t.Fatalf("wait for recipient presence convergence: %v\n%s", err, cluster.DumpDiagnostics())
	}
	expectedOnline := 0
	for _, recipient := range recipients {
		expectedOnline += recipient.expected
	}
	if expectedOnline != mediumOnlineRoutes {
		t.Fatalf("fixture online routes = %d, want %d", expectedOnline, mediumOnlineRoutes)
	}

	senders := connectSenders(t, cluster)
	defer closeClients(senders)
	messages := repeatMessages(baseMessages, measuredRounds, groupChannels)
	measuredRecipients := multiplyRecipientExpectations(recipients, measuredRounds)

	starts := &sync.Map{}
	receiverResults := startReceivers(measuredRecipients, starts)
	if sampler == nil {
		sampler = newPressureSampler(cluster, metricSampleInterval)
		sampler.start()
		defer sampler.stop()
	}
	profileDone := startHotPathProfiles(cluster, os.Getenv("WK_E2E_MEDIUM_RECIPIENT_PROFILE_DIR"))

	sendackLatencies := make([]time.Duration, 0, len(messages))
	recvLatencies := make([]time.Duration, 0, mediumOnlineRoutes*mediumMeasuredRounds)
	sendCounts := make([]int, len(senders))
	senderRecvCounts := make([]int, len(senders))
	senderIndexes := make([]int, len(messages))
	extraSenderRoutes := 0
	for index, message := range messages {
		senderIndex := messageSenderIndex(index, message)
		senderIndexes[index] = senderIndex
		sendCounts[senderIndex]++
		if message.channelType == frame.ChannelTypeGroup {
			for receiverIndex := 0; receiverIndex < mediumGroupSenders; receiverIndex++ {
				if receiverIndex == senderIndex {
					continue
				}
				senderRecvCounts[receiverIndex]++
				extraSenderRoutes++
			}
		}
	}
	senderResults := startSenderReaders(senders, sendCounts, senderRecvCounts, starts)
	counterStart := mustCaptureHotPathCounters(t, cluster)
	measuredStart := time.Now()
	for index, message := range messages {
		paceMessage(measuredStart, index, offeredQPS)
		start := time.Now()
		starts.Store(message.clientMsgNo, start)
		if err := senders[senderIndexes[index]].SendFrame(&frame.SendPacket{
			ChannelID:   message.channelID,
			ChannelType: message.channelType,
			ClientSeq:   message.clientSeq,
			ClientMsgNo: message.clientMsgNo,
			Payload:     payload,
		}); err != nil {
			sampler.stop()
			t.Fatalf("send %s: %v\n%s", message.clientMsgNo, err, cluster.DumpDiagnostics())
		}
	}
	sendLoopDuration := time.Since(measuredStart)

	for range senders {
		result := <-senderResults
		if result.err != nil {
			sampler.stop()
			t.Fatalf("read sender frames send_loop=%s pressure=%+v metrics=%s goroutines=%s: %v\n%s", sendLoopDuration, sampler.snapshot(), hotPathRuntimeDiagnostics(cluster), hotPathGoroutineDiagnostics(cluster), result.err, cluster.DumpDiagnostics())
		}
		sendackLatencies = append(sendackLatencies, result.sendackLatencies...)
		recvLatencies = append(recvLatencies, result.recvLatencies...)
	}
	measuredDuration := time.Since(measuredStart)

	for range measuredRecipients {
		result := <-receiverResults
		if result.err != nil {
			sampler.stop()
			t.Fatalf("receive and RECVACK: %v\n%s", result.err, cluster.DumpDiagnostics())
		}
		recvLatencies = append(recvLatencies, result.latencies...)
	}
	measuredOnlineRoutes := mediumOnlineRoutes*measuredRounds + extraSenderRoutes
	if len(recvLatencies) != measuredOnlineRoutes {
		sampler.stop()
		t.Fatalf("received routes = %d, want %d", len(recvLatencies), measuredOnlineRoutes)
	}
	if err := <-profileDone; err != nil {
		sampler.stop()
		t.Fatalf("capture CPU profiles: %v\n%s", err, cluster.DumpDiagnostics())
	}

	drainCtx, drainCancel := context.WithTimeout(context.Background(), 20*time.Second)
	drainErr := waitForHotPathDrain(drainCtx, cluster)
	drainCancel()
	sampler.stop()
	if drainErr != nil {
		t.Fatalf("hot path did not drain: %v\n%s", drainErr, cluster.DumpDiagnostics())
	}
	expectedPluginBatches := float64(pluginReceiveBatchCount() * measuredRounds)
	pluginCtx, pluginCancel := context.WithTimeout(context.Background(), 10*time.Second)
	counterEnd, pluginErr := waitForPluginReceiveDrain(pluginCtx, cluster, counterStart, expectedPluginBatches)
	pluginCancel()
	if pluginErr != nil {
		t.Fatalf("plugin receive batch path did not drain: %v\n%s", pluginErr, cluster.DumpDiagnostics())
	}
	counterDelta := counterEnd.subtract(counterStart)

	processContinuous := true
	for _, node := range cluster.Nodes {
		processContinuous = processContinuous && node.Process.Running()
	}
	if !processContinuous {
		t.Fatalf("one or more node processes exited\n%s", cluster.DumpDiagnostics())
	}

	pressure := sampler.snapshot()
	evidence := hotPathEvidence{
		Schema:                   mediumEvidenceSchema,
		PhysicalHashSlots:        mediumPhysicalHashSlots,
		LogicalSlots:             mediumLogicalSlots,
		Replicas:                 mediumReplicaCount,
		SlotTickIntervalMS:       milliseconds(mediumSlotTickInterval),
		SlotHeartbeatTick:        mediumSlotHeartbeatTick,
		SlotElectionTick:         mediumSlotElectionTick,
		Messages:                 len(messages),
		RecipientRows:            mediumRecipientRows * measuredRounds,
		OnlineRoutes:             measuredOnlineRoutes,
		Connections:              len(recipients) + len(senders),
		GroupChannels:            groupChannelCount,
		ActiveGroupChannels:      countActiveGroupChannels(messages),
		OfferedQPS:               offeredQPS,
		ClusterConvergenceMS:     milliseconds(convergence.WaitDuration),
		ClusterStableWindowMS:    milliseconds(convergence.StableDuration),
		SlotLeaders:              append([]uint64(nil), convergence.Leaders...),
		ColdPrimeDurationMS:      milliseconds(coldPrimeDuration),
		SendLoopDurationMS:       milliseconds(sendLoopDuration),
		MeasuredDurationMS:       milliseconds(measuredDuration),
		CompletionDrainMS:        milliseconds(measuredDuration - sendLoopDuration),
		IngressPerSecond:         float64(len(messages)) / sendLoopDuration.Seconds(),
		CompletionPerSecond:      float64(len(messages)) / measuredDuration.Seconds(),
		SendackP50MS:             milliseconds(percentile(sendackLatencies, 0.50)),
		SendackP99MS:             milliseconds(percentile(sendackLatencies, 0.99)),
		SendackMaxMS:             milliseconds(percentile(sendackLatencies, 1)),
		RecvP99MS:                milliseconds(percentile(recvLatencies, 0.99)),
		RecvMaxMS:                milliseconds(percentile(recvLatencies, 1)),
		MaxGatewayQueueRatio:     pressure.maxGatewayQueueRatio,
		MaxRecipientQueueRatio:   pressure.maxRecipientQueueRatio,
		MaxRecipientWorkerRatio:  pressure.maxRecipientWorkerRatio,
		ChannelRPCMetricNodes:    pressure.maxChannelRPCMetricNodes,
		MinChannelRPCWorkers:     pressure.minChannelRPCWorkers,
		MaxChannelRPCWorkers:     pressure.maxChannelRPCWorkers,
		ChannelRPCBatchMaxItems:  rpcBatchMaxItems,
		ChannelRPCAdmissionFull:  counterDelta.channelRPCAdmissionFull,
		ChannelRPCPullBatches:    counterDelta.channelRPCPullBatches,
		ChannelRPCPullBatchItems: counterDelta.channelRPCPullBatchItems,
		ChannelRPCHintBatches:    counterDelta.channelRPCHintBatches,
		ChannelRPCHintBatchItems: counterDelta.channelRPCHintBatchItems,
		MembershipMutationRows:   counterDelta.membershipMutationRows,
		MaxChannelRPCQueueRatio:  pressure.maxChannelRPCQueueRatio,
		MaxChannelRPCWorkerRatio: pressure.maxChannelRPCWorkerRatio,
		MaxAdvancePoolUtil:       pressure.maxAdvancePoolUtil,
		MaxAdvancePoolWaiting:    pressure.maxAdvancePoolWaiting,
		MaxAppendPoolUtil:        pressure.maxAppendPoolUtil,
		MaxPostCommitPoolUtil:    pressure.maxPostCommitPoolUtil,
		MaxPostCommitBacklog:     pressure.maxPostCommitBacklog,
		MaxPostCommitHandoffRate: pressure.maxPostCommitHandoffRatio,
		MaxHeapBytes:             pressure.maxHeapBytes,
		MaxAggregateHeapBytes:    pressure.maxAggregateHeapBytes,
		AllocatedBytes:           counterDelta.allocatedBytes,
		GCCountDelta:             counterDelta.gcCount,
		PluginReceiveAccepted:    counterDelta.pluginReceiveAccepted,
		PluginReceiveFull:        counterDelta.pluginReceiveFull,
		PluginReceiveClosed:      counterDelta.pluginReceiveClosed,
		PluginReceiveInvokeOK:    counterDelta.pluginReceiveInvokeOK,
		PluginReceiveInvokeError: counterDelta.pluginReceiveInvokeError,
		RecipientProcessError:    counterDelta.recipientProcessError,
		MetricSamples:            pressure.samples,
		MetricSampleErrors:       pressure.sampleErrors,
		Drained:                  true,
		ProcessContinuous:        true,
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatalf("marshal evidence: %v", err)
	}
	t.Logf("WKRC-HIFI-EVIDENCE %s", encoded)
	if os.Getenv("WK_E2E_MEDIUM_RECIPIENT_ENFORCE_ACCEPTANCE") == "1" {
		requireHotPathAcceptance(
			t, evidence, expectedAcceptanceQPS, measuredRounds,
			defaultHotPathAcceptanceLimits(),
		)
	}
}

func requireHotPathAcceptance(
	t *testing.T,
	evidence hotPathEvidence,
	expectedOfferedQPS int,
	expectedRounds int,
	limits hotPathAcceptanceLimits,
) {
	t.Helper()
	if err := hotPathAcceptanceErrorWithLimits(
		evidence, expectedOfferedQPS, expectedRounds, limits,
	); err != nil {
		t.Fatal(err)
	}
}

func hotPathAcceptanceError(evidence hotPathEvidence, expectedOfferedQPS int, expectedRounds int) error {
	return hotPathAcceptanceErrorWithLimits(
		evidence, expectedOfferedQPS, expectedRounds,
		defaultHotPathAcceptanceLimits(),
	)
}

func defaultHotPathAcceptanceLimits() hotPathAcceptanceLimits {
	return hotPathAcceptanceLimits{
		maxSendackP99MS:         milliseconds(time.Second),
		maxHeapBytes:            mediumMaxHeapBytes,
		maxAllocatedBytesPerMsg: mediumMaxAllocatedBytesPerMessage,
	}
}

func hotPathAcceptanceErrorWithLimits(
	evidence hotPathEvidence,
	expectedOfferedQPS int,
	expectedRounds int,
	limits hotPathAcceptanceLimits,
) error {
	expectedMessages := mediumMessageCount * expectedRounds
	expectedRecipientRows := mediumRecipientRows * expectedRounds
	switch {
	case evidence.Schema != mediumEvidenceSchema:
		return fmt.Errorf("acceptance schema = %q, want %q", evidence.Schema, mediumEvidenceSchema)
	case evidence.PhysicalHashSlots != mediumPhysicalHashSlots:
		return fmt.Errorf("acceptance physical hash slots = %d, want %d", evidence.PhysicalHashSlots, mediumPhysicalHashSlots)
	case evidence.LogicalSlots != mediumLogicalSlots:
		return fmt.Errorf("acceptance logical slots = %d, want %d", evidence.LogicalSlots, mediumLogicalSlots)
	case evidence.Replicas != mediumReplicaCount:
		return fmt.Errorf("acceptance replicas = %d, want %d", evidence.Replicas, mediumReplicaCount)
	case evidence.SlotTickIntervalMS != milliseconds(mediumSlotTickInterval):
		return fmt.Errorf(
			"acceptance Slot tick interval = %.3fms, want %.3fms",
			evidence.SlotTickIntervalMS,
			milliseconds(mediumSlotTickInterval),
		)
	case evidence.SlotHeartbeatTick != mediumSlotHeartbeatTick:
		return fmt.Errorf("acceptance Slot heartbeat tick = %d, want %d", evidence.SlotHeartbeatTick, mediumSlotHeartbeatTick)
	case evidence.SlotElectionTick != mediumSlotElectionTick:
		return fmt.Errorf("acceptance Slot election tick = %d, want %d", evidence.SlotElectionTick, mediumSlotElectionTick)
	case expectedRounds <= 0:
		return fmt.Errorf("acceptance expected rounds = %d, want positive", expectedRounds)
	case evidence.Messages != expectedMessages:
		return fmt.Errorf("acceptance messages = %d, want %d", evidence.Messages, expectedMessages)
	case evidence.RecipientRows != expectedRecipientRows:
		return fmt.Errorf("acceptance recipient rows = %d, want %d", evidence.RecipientRows, expectedRecipientRows)
	case evidence.OnlineRoutes != expectedMeasuredOnlineRoutes(expectedRounds):
		return fmt.Errorf("acceptance online routes = %d, want %d", evidence.OnlineRoutes, expectedMeasuredOnlineRoutes(expectedRounds))
	case evidence.Connections != expectedConnectionCount():
		return fmt.Errorf("acceptance connections = %d, want %d", evidence.Connections, expectedConnectionCount())
	case evidence.GroupChannels < len(mediumGroupProfiles) || evidence.GroupChannels > mediumCloudGroupChannelCount:
		return fmt.Errorf(
			"acceptance group channels = %d, want in [%d,%d]",
			evidence.GroupChannels,
			len(mediumGroupProfiles),
			mediumCloudGroupChannelCount,
		)
	case evidence.ActiveGroupChannels != expectedActiveGroupChannels(evidence.GroupChannels, expectedRounds):
		return fmt.Errorf(
			"acceptance active group channels = %d, want %d",
			evidence.ActiveGroupChannels,
			expectedActiveGroupChannels(evidence.GroupChannels, expectedRounds),
		)
	case evidence.OfferedQPS < expectedOfferedQPS:
		return fmt.Errorf("acceptance offered QPS = %d, want at least %d", evidence.OfferedQPS, expectedOfferedQPS)
	case evidence.ClusterConvergenceMS <= 0:
		return fmt.Errorf("acceptance cluster convergence = %.3fms, want a positive duration", evidence.ClusterConvergenceMS)
	case evidence.ClusterStableWindowMS < milliseconds(mediumConvergenceStableWindow):
		return fmt.Errorf(
			"acceptance cluster stable window = %.3fms, want at least %.3fms",
			evidence.ClusterStableWindowMS,
			milliseconds(mediumConvergenceStableWindow),
		)
	case !validMediumSlotLeaders(evidence.SlotLeaders):
		return fmt.Errorf(
			"acceptance actual Slot leaders = %v, want %d leaders in nodes [1,%d] distributed 3/3/4",
			evidence.SlotLeaders,
			mediumLogicalSlots,
			mediumReplicaCount,
		)
	case evidence.IngressPerSecond < minimumAcceptedIngress(expectedOfferedQPS):
		return fmt.Errorf(
			"acceptance ingress = %.3f/s, want at least %.3f/s",
			evidence.IngressPerSecond,
			minimumAcceptedIngress(expectedOfferedQPS),
		)
	case evidence.SendackP99MS > limits.maxSendackP99MS:
		return fmt.Errorf(
			"acceptance SENDACK P99 = %.3fms, want at most %.3fms",
			evidence.SendackP99MS, limits.maxSendackP99MS,
		)
	case evidence.RecvP99MS > 2_000:
		return fmt.Errorf("acceptance RECV P99 = %.3fms, want at most 2000ms", evidence.RecvP99MS)
	case evidence.MaxGatewayQueueRatio >= 1:
		return fmt.Errorf("acceptance gateway queue ratio = %.6f, want below 1", evidence.MaxGatewayQueueRatio)
	case evidence.MaxRecipientQueueRatio >= 1:
		return fmt.Errorf("acceptance recipient queue ratio = %.6f, want below 1", evidence.MaxRecipientQueueRatio)
	case evidence.MaxRecipientWorkerRatio >= 1:
		return fmt.Errorf("acceptance recipient worker ratio = %.6f, want below 1", evidence.MaxRecipientWorkerRatio)
	case evidence.ChannelRPCMetricNodes != mediumReplicaCount:
		return fmt.Errorf("acceptance Channel RPC metric nodes = %d, want %d", evidence.ChannelRPCMetricNodes, mediumReplicaCount)
	case evidence.MinChannelRPCWorkers != mediumChannelRPCWorkers || evidence.MaxChannelRPCWorkers != mediumChannelRPCWorkers:
		return fmt.Errorf(
			"acceptance Channel RPC workers = min %.0f max %.0f, want %d/%d",
			evidence.MinChannelRPCWorkers,
			evidence.MaxChannelRPCWorkers,
			mediumChannelRPCWorkers,
			mediumChannelRPCWorkers,
		)
	case evidence.ChannelRPCBatchMaxItems != mediumChannelRPCBatchMaxItems:
		return fmt.Errorf(
			"acceptance Channel RPC batch max items = %d, want %d",
			evidence.ChannelRPCBatchMaxItems,
			mediumChannelRPCBatchMaxItems,
		)
	case evidence.ChannelRPCAdmissionFull != 0:
		return fmt.Errorf("acceptance Channel RPC full admissions = %.0f, want 0", evidence.ChannelRPCAdmissionFull)
	case evidence.ChannelRPCPullBatches <= 0 || evidence.ChannelRPCPullBatchItems <= 0:
		return fmt.Errorf(
			"acceptance Channel RPC Pull batch evidence = batches %.0f items %.0f, want positive",
			evidence.ChannelRPCPullBatches,
			evidence.ChannelRPCPullBatchItems,
		)
	case evidence.ChannelRPCHintBatches <= 0 || evidence.ChannelRPCHintBatchItems <= 0:
		return fmt.Errorf(
			"acceptance Channel RPC PullHint batch evidence = batches %.0f items %.0f, want positive",
			evidence.ChannelRPCHintBatches,
			evidence.ChannelRPCHintBatchItems,
		)
	case evidence.MaxChannelRPCQueueRatio >= 1:
		return fmt.Errorf("acceptance Channel RPC queue ratio = %.6f, want below 1", evidence.MaxChannelRPCQueueRatio)
	case evidence.MaxChannelRPCWorkerRatio >= 1:
		return fmt.Errorf("acceptance Channel RPC worker ratio = %.6f, want below 1", evidence.MaxChannelRPCWorkerRatio)
	case evidence.MembershipMutationRows != 0:
		return fmt.Errorf("acceptance membership mutation rows = %.0f, want 0 during measured SEND", evidence.MembershipMutationRows)
	case evidence.PluginReceiveAccepted != float64(pluginReceiveBatchCount()*expectedRounds):
		return fmt.Errorf(
			"acceptance plugin receive accepted = %.0f, want %d",
			evidence.PluginReceiveAccepted,
			pluginReceiveBatchCount()*expectedRounds,
		)
	case evidence.PluginReceiveFull != 0 || evidence.PluginReceiveClosed != 0:
		return fmt.Errorf(
			"acceptance plugin receive enqueue non-accepted = full %.0f closed %.0f, want 0/0",
			evidence.PluginReceiveFull,
			evidence.PluginReceiveClosed,
		)
	case evidence.PluginReceiveInvokeOK != evidence.PluginReceiveAccepted || evidence.PluginReceiveInvokeError != 0:
		return fmt.Errorf(
			"acceptance plugin receive invoke = ok %.0f error %.0f, want %.0f/0",
			evidence.PluginReceiveInvokeOK,
			evidence.PluginReceiveInvokeError,
			evidence.PluginReceiveAccepted,
		)
	case evidence.RecipientProcessError != 0:
		return fmt.Errorf("acceptance recipient worker process errors = %.0f, want 0", evidence.RecipientProcessError)
	case evidence.MeasuredDurationMS <= 0:
		return fmt.Errorf("acceptance measured duration = %.3fms, want a positive duration", evidence.MeasuredDurationMS)
	case evidence.AllocatedBytes <= 0:
		return fmt.Errorf("acceptance allocated bytes = %.0f, want a positive measured delta", evidence.AllocatedBytes)
	case !limits.skipAllocatedBytesCeiling && evidence.AllocatedBytes > maxAcceptedAllocatedBytesWithLimit(
		evidence, limits.maxAllocatedBytesPerMsg,
	):
		return fmt.Errorf(
			"acceptance allocated bytes/message = %.0f, want at most %.0f after %.3fs paced background allowance",
			evidence.AllocatedBytes/float64(evidence.Messages),
			maxAcceptedAllocatedBytesWithLimit(
				evidence, limits.maxAllocatedBytesPerMsg,
			)/float64(evidence.Messages),
			acceptedBackgroundDurationSeconds(evidence),
		)
	case evidence.GCCountDelta <= 0:
		return fmt.Errorf("acceptance GC count delta = %.0f, want a positive measured delta", evidence.GCCountDelta)
	case evidence.GCCountDelta/float64(evidence.Messages) > mediumMaxGCPerMessage:
		return fmt.Errorf(
			"acceptance GC/message = %.6f, want at most %.6f",
			evidence.GCCountDelta/float64(evidence.Messages),
			mediumMaxGCPerMessage,
		)
	case evidence.MaxHeapBytes <= 0 || evidence.MaxHeapBytes > float64(limits.maxHeapBytes):
		return fmt.Errorf(
			"acceptance max heap bytes = %.0f, want in (0,%d]",
			evidence.MaxHeapBytes,
			limits.maxHeapBytes,
		)
	case evidence.MetricSamples == 0:
		return errors.New("acceptance collected no public metric samples")
	case evidence.MetricSampleErrors != 0:
		return fmt.Errorf("acceptance metric sample errors = %d, want 0", evidence.MetricSampleErrors)
	case !evidence.Drained:
		return errors.New("acceptance hot path did not drain")
	case !evidence.ProcessContinuous:
		return errors.New("acceptance process continuity failed")
	}
	return nil
}

func maxAcceptedAllocatedBytes(evidence hotPathEvidence) float64 {
	return maxAcceptedAllocatedBytesWithLimit(
		evidence, mediumMaxAllocatedBytesPerMessage,
	)
}

func maxAcceptedAllocatedBytesWithLimit(
	evidence hotPathEvidence,
	perMessage float64,
) float64 {
	return float64(evidence.Messages)*perMessage +
		acceptedBackgroundDurationSeconds(evidence)*mediumMaxBackgroundAllocatedBytesPerSecond
}

func acceptedBackgroundDurationSeconds(evidence hotPathEvidence) float64 {
	return float64(evidence.Messages) / float64(evidence.OfferedQPS)
}

func expectedMeasuredOnlineRoutes(rounds int) int {
	groupMessages := 0
	for _, profile := range mediumGroupProfiles {
		groupMessages += profile.messages
	}
	return mediumOnlineRoutes*rounds +
		groupMessages*(mediumGroupSenders-1)*rounds
}

func expectedConnectionCount() int {
	return 100 + 125 + mediumSenderConnections
}

func boundedPositiveEnvInt(t *testing.T, name string, fallback, minimum, maximum int) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		t.Fatalf("%s=%q must be an integer in [%d,%d]", name, raw, minimum, maximum)
	}
	return value
}

func startHotPathProfiles(cluster *suite.StartedCluster, outputDir string) <-chan error {
	done := make(chan error, 1)
	if strings.TrimSpace(outputDir) == "" {
		done <- nil
		return done
	}
	go func() {
		if err := os.MkdirAll(outputDir, 0o755); err != nil {
			done <- err
			return
		}
		errs := make(chan error, len(cluster.Nodes))
		for _, node := range cluster.Nodes {
			node := node
			go func() {
				for _, profile := range []struct {
					name string
					path string
				}{
					{name: "cpu", path: "/debug/pprof/profile?seconds=2"},
					{name: "heap", path: "/debug/pprof/heap"},
				} {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+node.APIAddr()+profile.path, nil)
					if err != nil {
						cancel()
						errs <- err
						return
					}
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						cancel()
						errs <- fmt.Errorf("node %d %s profile request: %w", node.Spec.ID, profile.name, err)
						return
					}
					data, readErr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
					_ = resp.Body.Close()
					cancel()
					if resp.StatusCode != http.StatusOK {
						errs <- fmt.Errorf("node %d %s profile status = %d", node.Spec.ID, profile.name, resp.StatusCode)
						return
					}
					if readErr != nil {
						errs <- fmt.Errorf("node %d read %s profile: %w", node.Spec.ID, profile.name, readErr)
						return
					}
					path := filepath.Join(outputDir, fmt.Sprintf("node-%d-%s.pb.gz", node.Spec.ID, profile.name))
					if err := os.WriteFile(path, data, 0o600); err != nil {
						errs <- fmt.Errorf("node %d write %s profile: %w", node.Spec.ID, profile.name, err)
						return
					}
				}
				errs <- nil
			}()
		}
		for range cluster.Nodes {
			if err := <-errs; err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	return done
}

func paceMessage(start time.Time, index, perSecond int) {
	if index <= 0 || perSecond <= 0 {
		return
	}
	target := start.Add(time.Duration(int64(index) * int64(time.Second) / int64(perSecond)))
	if delay := time.Until(target); delay > 0 {
		time.Sleep(delay)
	}
}

func primeHotPathChannels(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	messages []hotPathMessage,
	payload []byte,
) time.Duration {
	t.Helper()
	startedAt := time.Now()
	primeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan hotPathMessage)
	errs := make(chan error, mediumPrimeConcurrency)
	var workers sync.WaitGroup
	for range mediumPrimeConcurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for message := range jobs {
				resp, err := suite.PostMessageSendEventually(primeCtx, cluster.MustNode(1).APIAddr(), map[string]any{
					"from_uid":      primeSenderUID(message),
					"channel_id":    message.channelID,
					"channel_type":  message.channelType,
					"client_msg_no": message.clientMsgNo,
					"payload":       payload,
				})
				if err != nil {
					select {
					case errs <- fmt.Errorf("prime send %s: %w", message.clientMsgNo, err):
					default:
					}
					cancel()
					return
				}
				if resp.Reason != uint8(frame.ReasonSuccess) || resp.MessageID == 0 || resp.MessageSeq == 0 {
					select {
					case errs <- fmt.Errorf(
						"prime send %s returned reason=%d message_id=%d message_seq=%d",
						message.clientMsgNo,
						resp.Reason,
						resp.MessageID,
						resp.MessageSeq,
					):
					default:
					}
					cancel()
					return
				}
			}
		}()
	}
enqueue:
	for _, message := range messages {
		select {
		case jobs <- message:
		case <-primeCtx.Done():
			break enqueue
		}
	}
	close(jobs)
	workers.Wait()
	select {
	case err := <-errs:
		t.Fatalf(
			"bounded HTTP cold prime after %s metrics=%s goroutines=%s: %v\n%s",
			time.Since(startedAt),
			hotPathRuntimeDiagnostics(cluster),
			hotPathGoroutineDiagnostics(cluster),
			err,
			cluster.DumpDiagnostics(),
		)
	default:
	}
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer drainCancel()
	if err := waitForHotPathDrain(drainCtx, cluster); err != nil {
		t.Fatalf("prime hot path did not drain: %v\n%s", err, cluster.DumpDiagnostics())
	}
	duration := time.Since(startedAt)
	t.Logf("WKRC-HIFI-PRIME duration=%s messages=%d", duration, len(messages))
	return duration
}

func primeSenderUID(message hotPathMessage) string {
	return mediumSenderUID(message.primeSender)
}

func proveWarmupSend(t *testing.T, cluster *suite.StartedCluster, sender *suite.WKProtoClient) {
	t.Helper()
	start := time.Now()
	if err := sender.SendFrame(&frame.SendPacket{
		ChannelID:   "wkrc-hifi-warmup-offline",
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   1_000_000,
		ClientMsgNo: "wkrc-hifi-warmup",
		Payload:     []byte("warmup"),
	}); err != nil {
		t.Fatalf("submit warmup SEND: %v\n%s", err, cluster.DumpDiagnostics())
	}
	sendack, err := sender.ReadSendAck()
	if err != nil {
		t.Fatalf("read warmup SENDACK after %s metrics=%s: %v\n%s", time.Since(start), hotPathRuntimeDiagnostics(cluster), err, cluster.DumpDiagnostics())
	}
	if sendack.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("warmup SENDACK reason=%v metrics=%s\n%s", sendack.ReasonCode, hotPathRuntimeDiagnostics(cluster), cluster.DumpDiagnostics())
	}
	t.Logf("WKRC-HIFI-WARMUP duration=%s", time.Since(start))
}

func startMediumCluster(t *testing.T, rpcBatchMaxItems int) *suite.StartedCluster {
	t.Helper()
	overrides := map[string]string{
		"WK_CLUSTER_INITIAL_SLOT_COUNT":                              "10",
		"WK_CLUSTER_HASH_SLOT_COUNT":                                 "256",
		"WK_CLUSTER_SLOT_REPLICA_N":                                  "3",
		"WK_CLUSTER_SLOT_TICK_INTERVAL":                              mediumSlotTickInterval.String(),
		"WK_CLUSTER_SLOT_HEARTBEAT_TICK":                             strconv.Itoa(mediumSlotHeartbeatTick),
		"WK_CLUSTER_SLOT_ELECTION_TICK":                              strconv.Itoa(mediumSlotElectionTick),
		"WK_CLUSTER_CHANNEL_REPLICA_N":                               "3",
		"WK_CLUSTER_CHANNEL_REACTOR_COUNT":                           "4",
		"WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS":                    "8",
		"WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS":                     "8",
		"WK_CLUSTER_CHANNEL_RPC_WORKERS":                             strconv.Itoa(mediumChannelRPCWorkers),
		"WK_CLUSTER_CHANNEL_RPC_BATCH_MAX_ITEMS":                     strconv.Itoa(rpcBatchMaxItems),
		"WK_CLUSTER_COMMIT_COORDINATOR_SHARDS":                       strconv.Itoa(mediumCommitCoordinatorShards),
		"WK_GATEWAY_GNET_MULTICORE":                                  "true",
		"WK_GATEWAY_GNET_NUM_EVENT_LOOP":                             "4",
		"WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS":                      "128",
		"WK_GATEWAY_RUNTIME_ASYNC_SEND_QUEUE_CAPACITY":               "131072",
		"WK_BENCH_API_ENABLE":                                        "true",
		"WK_DEBUG_API_ENABLE":                                        "true",
		"WK_DELIVERY_ENABLE":                                         "true",
		"WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY":                   "320",
		"WK_PLUGIN_ENABLE":                                           "true",
		"WK_CHANNEL_APPEND_ADVANCE_POOL_SIZE":                        "500",
		"WK_CHANNEL_APPEND_EFFECT_POOL_SIZE":                         "2000",
		"WK_CHANNEL_APPEND_RECIPIENT_AUTHORITY_DISPATCH_CONCURRENCY": "100",
	}
	options := []suite.Option{suite.WithManagerHTTP()}
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		options = append(
			options,
			suite.WithNodeConfigOverrides(nodeID, overrides),
		)
	}
	return suite.New(t).StartThreeNodeCluster(options...)
}

func verifyMediumRenderedRuntime(t *testing.T, cluster *suite.StartedCluster, rpcBatchMaxItems int) {
	t.Helper()
	type clusterRuntime struct {
		Cluster struct {
			SlotTickInterval        string `toml:"slot_tick_interval"`
			SlotHeartbeatTick       int    `toml:"slot_heartbeat_tick"`
			SlotElectionTick        int    `toml:"slot_election_tick"`
			ChannelRPCBatchMaxItems int    `toml:"channel_rpc_batch_max_items"`
			CommitCoordinatorShards int    `toml:"commit_coordinator_shards"`
		} `toml:"cluster"`
	}
	for _, node := range cluster.Nodes {
		data, err := os.ReadFile(node.Spec.ConfigPath)
		if err != nil {
			t.Fatalf("read node %d rendered config: %v", node.Spec.ID, err)
		}
		var runtime clusterRuntime
		if err := toml.Unmarshal(data, &runtime); err != nil {
			t.Fatalf("decode node %d rendered config: %v", node.Spec.ID, err)
		}
		if runtime.Cluster.SlotTickInterval != mediumSlotTickInterval.String() ||
			runtime.Cluster.SlotHeartbeatTick != mediumSlotHeartbeatTick ||
			runtime.Cluster.SlotElectionTick != mediumSlotElectionTick ||
			runtime.Cluster.ChannelRPCBatchMaxItems != rpcBatchMaxItems ||
			runtime.Cluster.CommitCoordinatorShards != mediumCommitCoordinatorShards {
			t.Fatalf(
				"node %d runtime = Slot timing %s/%d/%d Channel RPC batch %d commit shards %d, want %s/%d/%d/%d/%d",
				node.Spec.ID,
				runtime.Cluster.SlotTickInterval,
				runtime.Cluster.SlotHeartbeatTick,
				runtime.Cluster.SlotElectionTick,
				runtime.Cluster.ChannelRPCBatchMaxItems,
				runtime.Cluster.CommitCoordinatorShards,
				mediumSlotTickInterval,
				mediumSlotHeartbeatTick,
				mediumSlotElectionTick,
				rpcBatchMaxItems,
				mediumCommitCoordinatorShards,
			)
		}
	}
}

func validMediumSlotLeaders(leaders []uint64) bool {
	if len(leaders) != mediumLogicalSlots {
		return false
	}
	counts := make([]int, mediumReplicaCount)
	for _, leaderID := range leaders {
		if leaderID == 0 || leaderID > uint64(mediumReplicaCount) {
			return false
		}
		counts[leaderID-1]++
	}
	sort.Ints(counts)
	return counts[0] == 3 && counts[1] == 3 && counts[2] == 4
}

func minimumAcceptedIngress(expectedOfferedQPS int) float64 {
	if expectedOfferedQPS >= mediumOfferedQPS {
		return mediumOfferedQPS
	}
	return float64(expectedOfferedQPS) * mediumCIMinIngressFraction
}

func prepareGroupChannels(
	t *testing.T,
	ctx context.Context,
	node *suite.StartedNode,
	totalChannels int,
) ([][]string, int, []string) {
	t.Helper()
	onlineUIDs := make([]string, 100)
	for i := range onlineUIDs {
		onlineUIDs[i] = fmt.Sprintf("wkrc-group-online-%03d", i)
	}
	profileChannelCounts := scaleGroupChannelCounts(totalChannels)
	channels := make([][]string, len(mediumGroupProfiles))
	channelItems := make([]benchmodel.ChannelItem, 0, totalChannels)
	subscriberItems := make([][]benchmodel.SubscriberItem, len(mediumGroupProfiles))
	recipientRows := 0
	for profileIndex, profile := range mediumGroupProfiles {
		subscribers := make([]string, profile.recipients)
		copy(subscribers, onlineUIDs[:profile.online])
		for i := profile.online; i < len(subscribers); i++ {
			subscribers[i] = fmt.Sprintf("wkrc-group-%d-offline-%04d", profileIndex, i)
		}
		for senderIndex := 0; senderIndex < mediumGroupSenders; senderIndex++ {
			subscribers[len(subscribers)-1-senderIndex] = mediumSenderUID(senderIndex)
		}
		channels[profileIndex] = make([]string, profileChannelCounts[profileIndex])
		for channelIndex := range channels[profileIndex] {
			channelID := fmt.Sprintf("wkrc-hifi-group-%d-%04d", profileIndex, channelIndex)
			channels[profileIndex][channelIndex] = channelID
			channelItems = append(channelItems, benchmodel.ChannelItem{
				ChannelID:   channelID,
				ChannelType: frame.ChannelTypeGroup,
			})
			subscriberItems[profileIndex] = append(subscriberItems[profileIndex], benchmodel.SubscriberItem{
				ChannelID:   channelID,
				ChannelType: frame.ChannelTypeGroup,
				Subscribers: subscribers,
			})
		}
		recipientRows += profile.messages * profile.recipients
	}
	client := benchtarget.NewClient(benchtarget.Config{APIAddrs: []string{"http://" + node.APIAddr()}})
	if err := client.UpsertChannels(ctx, benchmodel.BatchChannelsRequest{
		RunID:    "wkrc-hifi",
		BatchID:  "group-channels",
		Upsert:   true,
		Channels: channelItems,
	}); err != nil {
		t.Fatalf("batch prepare %d group channels: %v\n%s", totalChannels, err, node.DumpDiagnostics())
	}
	for profileIndex, items := range subscriberItems {
		batchSize := groupSubscriberPrepareBatchSize(mediumGroupProfiles[profileIndex].recipients)
		for start := 0; start < len(items); start += batchSize {
			end := min(start+batchSize, len(items))
			if err := client.AddSubscribers(ctx, benchmodel.BatchSubscribersRequest{
				RunID:   "wkrc-hifi",
				BatchID: fmt.Sprintf("group-subscribers-%d-%04d", profileIndex, start/batchSize),
				Items:   items[start:end],
			}); err != nil {
				t.Fatalf(
					"batch prepare group subscribers profile=%d range=[%d,%d): %v\n%s",
					profileIndex,
					start,
					end,
					err,
					node.DumpDiagnostics(),
				)
			}
		}
	}
	return channels, recipientRows, onlineUIDs
}

func groupSubscriberPrepareBatchSize(recipients int) int {
	switch {
	case recipients >= 1_000:
		return 50
	case recipients >= 500:
		return 100
	default:
		return 200
	}
}

func scaleGroupChannelCounts(totalChannels int) []int {
	counts := make([]int, len(mediumGroupProfiles))
	if totalChannels < len(counts) {
		return counts
	}
	assigned := 0
	for index, profile := range mediumGroupProfiles {
		count := totalChannels * profile.cloudChannels / mediumCloudGroupChannelCount
		if count < 1 {
			count = 1
		}
		counts[index] = count
		assigned += count
	}
	for assigned < totalChannels {
		for index := range counts {
			if assigned >= totalChannels {
				break
			}
			counts[index]++
			assigned++
		}
	}
	for assigned > totalChannels {
		for index := len(counts) - 1; index >= 0 && assigned > totalChannels; index-- {
			if counts[index] <= 1 {
				continue
			}
			counts[index]--
			assigned--
		}
	}
	return counts
}

func expectedActiveGroupChannels(totalChannels, rounds int) int {
	counts := scaleGroupChannelCounts(totalChannels)
	active := 0
	for index, count := range counts {
		active += min(count, mediumGroupProfiles[index].messages*rounds)
	}
	return active
}

func connectRecipients(t *testing.T, cluster *suite.StartedCluster, groupOnline, personUIDs []string) []hotPathRecipient {
	t.Helper()
	recipients := make([]hotPathRecipient, 0, len(groupOnline)+len(personUIDs))
	for index, uid := range groupOnline {
		expected := 0
		for _, profile := range mediumGroupProfiles {
			if index < profile.online {
				expected += profile.messages
			}
		}
		recipients = append(recipients, hotPathRecipient{
			uid:      uid,
			expected: expected,
			client:   mustConnect(t, cluster.MustNode(uint64(index%3+1)), uid),
		})
	}
	for index, uid := range personUIDs {
		recipients = append(recipients, hotPathRecipient{
			uid:      uid,
			expected: 1,
			client:   mustConnect(t, cluster.MustNode(uint64(index%3+1)), uid),
		})
	}
	return recipients
}

func waitForRecipientPresence(ctx context.Context, cluster *suite.StartedCluster, expected int) error {
	apiAddrs := make([]string, 0, len(cluster.Nodes))
	for _, node := range cluster.Nodes {
		apiAddrs = append(apiAddrs, "http://"+node.APIAddr())
	}
	client := benchtarget.NewClient(benchtarget.Config{APIAddrs: apiAddrs})
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var last []benchmodel.PresenceSnapshot
	for {
		snapshots, err := client.PresenceSnapshots(ctx)
		if err == nil {
			last = snapshots
			ownerActive := 0
			ownerPending := 0
			authorityActive := 0
			for _, snapshot := range snapshots {
				ownerActive += snapshot.OwnerRoutesActive
				ownerPending += snapshot.OwnerRoutesPending
				authorityActive += snapshot.AuthorityRoutesActive
			}
			if len(snapshots) == len(cluster.Nodes) &&
				ownerActive == expected &&
				ownerPending == 0 &&
				authorityActive == expected {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("presence convergence: %w; last=%+v", ctx.Err(), last)
		case <-ticker.C:
		}
	}
}

func mustConnect(t *testing.T, node *suite.StartedNode, uid string) *suite.WKProtoClient {
	t.Helper()
	return mustConnectDevice(t, node, uid, uid+"-device")
}

func mustConnectDevice(t *testing.T, node *suite.StartedNode, uid, deviceID string) *suite.WKProtoClient {
	t.Helper()
	client, err := suite.NewWKProtoClient()
	if err != nil {
		t.Fatalf("new WKProto client %s: %v", uid, err)
	}
	if err := client.Connect(node.GatewayAddr(), uid, deviceID); err != nil {
		_ = client.Close()
		t.Fatalf("connect WKProto client %s: %v\n%s", uid, err, node.DumpDiagnostics())
	}
	return client
}

func connectSenders(t *testing.T, cluster *suite.StartedCluster) []*suite.WKProtoClient {
	t.Helper()
	senders := make([]*suite.WKProtoClient, mediumSenderConnections)
	for index := range senders {
		senders[index] = mustConnectDevice(
			t,
			cluster.MustNode(uint64(index%3+1)),
			mediumSenderUID(index),
			fmt.Sprintf("%s-device", mediumSenderUID(index)),
		)
	}
	return senders
}

func mediumSenderUID(index int) string {
	return fmt.Sprintf("%s-%02d", mediumSenderUIDPrefix, index+1)
}

func pluginReceiveBatchCount() int {
	total := 0
	for _, profile := range mediumGroupProfiles {
		plansPerMessage := (profile.recipients + mediumRecipientPlanSize - 1) / mediumRecipientPlanSize
		total += profile.messages * plansPerMessage
	}
	return total
}

func closeClients(clients []*suite.WKProtoClient) {
	for _, client := range clients {
		_ = client.Close()
	}
}

func closeRecipients(recipients []hotPathRecipient) {
	for _, recipient := range recipients {
		_ = recipient.client.Close()
	}
}

func buildMessages(groupChannels [][]string, personUIDs []string) []hotPathMessage {
	messages := make([]hotPathMessage, 0, mediumMessageCount)
	nextSeq := uint64(1)
	for _, uid := range personUIDs {
		messages = append(messages, hotPathMessage{
			clientSeq:    nextSeq,
			clientMsgNo:  fmt.Sprintf("wkrc-hifi-%03d", nextSeq),
			channelID:    uid,
			channelType:  frame.ChannelTypePerson,
			groupProfile: -1,
		})
		nextSeq++
	}
	for profileIndex, profile := range mediumGroupProfiles {
		for ordinal := range profile.messages {
			messages = append(messages, hotPathMessage{
				clientSeq:    nextSeq,
				clientMsgNo:  fmt.Sprintf("wkrc-hifi-%03d", nextSeq),
				channelID:    groupChannels[profileIndex][ordinal%len(groupChannels[profileIndex])],
				channelType:  frame.ChannelTypeGroup,
				groupProfile: profileIndex,
				groupOrdinal: ordinal,
			})
			nextSeq++
		}
	}
	return messages
}

func buildPrimeMessages(groupChannels [][]string, personUIDs []string) []hotPathMessage {
	buckets := make([][]hotPathMessage, 1+len(groupChannels))
	buckets[0] = make([]hotPathMessage, 0, len(personUIDs))
	for personIndex, uid := range personUIDs {
		buckets[0] = append(buckets[0], hotPathMessage{
			channelID:    uid,
			channelType:  frame.ChannelTypePerson,
			groupProfile: -1,
			primeSender:  personIndex % mediumSenderConnections,
		})
	}
	for profileIndex, channels := range groupChannels {
		bucket := make([]hotPathMessage, 0, len(channels))
		for channelIndex, channelID := range channels {
			bucket = append(bucket, hotPathMessage{
				channelID:    channelID,
				channelType:  frame.ChannelTypeGroup,
				groupProfile: profileIndex,
				groupOrdinal: channelIndex,
				primeSender:  channelIndex % mediumGroupSenders,
			})
		}
		buckets[profileIndex+1] = bucket
	}
	messages := make([]hotPathMessage, 0, len(personUIDs)+countGroupChannels(groupChannels))
	for ordinal := 0; len(messages) < cap(messages); ordinal++ {
		for _, bucket := range buckets {
			if ordinal < len(bucket) {
				messages = append(messages, bucket[ordinal])
			}
		}
	}
	for index := range messages {
		nextSeq := uint64(index + 1)
		messages[index].clientSeq = nextSeq
		messages[index].clientMsgNo = fmt.Sprintf("wkrc-hifi-prime-%04d", nextSeq)
	}
	return messages
}

func countGroupChannels(groupChannels [][]string) int {
	total := 0
	for _, channels := range groupChannels {
		total += len(channels)
	}
	return total
}

func repeatMessages(base []hotPathMessage, rounds int, groupChannels [][]string) []hotPathMessage {
	messages := make([]hotPathMessage, 0, len(base)*rounds)
	nextSeq := uint64(1)
	for round := range rounds {
		for _, message := range base {
			message.clientSeq = nextSeq
			message.clientMsgNo = fmt.Sprintf("wkrc-hifi-round-%02d-%04d", round+1, nextSeq)
			if message.groupProfile >= 0 {
				profileChannels := groupChannels[message.groupProfile]
				profileMessages := mediumGroupProfiles[message.groupProfile].messages
				channelIndex := (round*profileMessages + message.groupOrdinal) % len(profileChannels)
				message.channelID = profileChannels[channelIndex]
			}
			messages = append(messages, message)
			nextSeq++
		}
	}
	return messages
}

func countActiveGroupChannels(messages []hotPathMessage) int {
	active := make(map[string]struct{})
	for _, message := range messages {
		if message.channelType == frame.ChannelTypeGroup {
			active[message.channelID] = struct{}{}
		}
	}
	return len(active)
}

func messageSenderIndex(index int, message hotPathMessage) int {
	if message.channelType == frame.ChannelTypeGroup {
		return index % mediumGroupSenders
	}
	return index % mediumSenderConnections
}

func multiplyRecipientExpectations(base []hotPathRecipient, multiplier int) []hotPathRecipient {
	recipients := make([]hotPathRecipient, len(base))
	copy(recipients, base)
	for index := range recipients {
		recipients[index].expected *= multiplier
	}
	return recipients
}

func startReceivers(recipients []hotPathRecipient, starts *sync.Map) <-chan receiverResult {
	results := make(chan receiverResult, len(recipients))
	for _, recipient := range recipients {
		recipient := recipient
		go func() {
			latencies := make([]time.Duration, 0, recipient.expected)
			for range recipient.expected {
				recv, err := recipient.client.ReadRecv()
				if err != nil {
					results <- receiverResult{err: fmt.Errorf("%s read RECV: %w", recipient.uid, err)}
					return
				}
				startValue, ok := starts.Load(recv.ClientMsgNo)
				if !ok {
					results <- receiverResult{err: fmt.Errorf("%s RECV %s has no send start", recipient.uid, recv.ClientMsgNo)}
					return
				}
				latencies = append(latencies, time.Since(startValue.(time.Time)))
				if err := recipient.client.RecvAck(recv.MessageID, recv.MessageSeq); err != nil {
					results <- receiverResult{err: fmt.Errorf("%s RECVACK: %w", recipient.uid, err)}
					return
				}
			}
			results <- receiverResult{latencies: latencies}
		}()
	}
	return results
}

func startSenderReaders(
	clients []*suite.WKProtoClient,
	sendackCounts []int,
	recvCounts []int,
	starts *sync.Map,
) <-chan senderResult {
	results := make(chan senderResult, len(clients))
	for index, client := range clients {
		client := client
		sendackCount := sendackCounts[index]
		recvCount := recvCounts[index]
		go func() {
			sendackLatencies := make([]time.Duration, 0, sendackCount)
			recvLatencies := make([]time.Duration, 0, recvCount)
			for range sendackCount + recvCount {
				next, err := client.ReadFrame()
				if err != nil {
					results <- senderResult{err: err}
					return
				}
				switch packet := next.(type) {
				case *frame.SendackPacket:
					if packet.ReasonCode != frame.ReasonSuccess {
						results <- senderResult{err: fmt.Errorf("SENDACK %s reason=%v", packet.ClientMsgNo, packet.ReasonCode)}
						return
					}
					startValue, ok := starts.Load(packet.ClientMsgNo)
					if !ok {
						results <- senderResult{err: fmt.Errorf("SENDACK %s has no send start", packet.ClientMsgNo)}
						return
					}
					sendackLatencies = append(sendackLatencies, time.Since(startValue.(time.Time)))
				case *frame.RecvPacket:
					startValue, ok := starts.Load(packet.ClientMsgNo)
					if !ok {
						results <- senderResult{err: fmt.Errorf("sender RECV %s has no send start", packet.ClientMsgNo)}
						return
					}
					recvLatencies = append(recvLatencies, time.Since(startValue.(time.Time)))
					if err := client.RecvAck(packet.MessageID, packet.MessageSeq); err != nil {
						results <- senderResult{err: fmt.Errorf("sender RECVACK: %w", err)}
						return
					}
				default:
					results <- senderResult{err: fmt.Errorf("unexpected sender frame %T", next)}
					return
				}
			}
			if len(sendackLatencies) != sendackCount || len(recvLatencies) != recvCount {
				results <- senderResult{err: fmt.Errorf(
					"sender frames sendack=%d/%d recv=%d/%d",
					len(sendackLatencies), sendackCount, len(recvLatencies), recvCount,
				)}
				return
			}
			results <- senderResult{
				sendackLatencies: sendackLatencies,
				recvLatencies:    recvLatencies,
			}
		}()
	}
	return results
}

func percentile(values []time.Duration, quantile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := int(math.Ceil(quantile*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func milliseconds(value time.Duration) float64 {
	return float64(value) / float64(time.Millisecond)
}

type pressureSnapshot struct {
	maxGatewayQueueRatio             float64
	maxRecipientQueueRatio           float64
	maxRecipientWorkerRatio          float64
	maxChannelReactorMailboxRatio    float64
	maxChannelStoreAppendQueueRatio  float64
	maxChannelStoreAppendWorkerRatio float64
	maxChannelStoreApplyQueueRatio   float64
	maxChannelStoreApplyWorkerRatio  float64
	maxRouterGroupInflight           float64
	maxRouterGroupCapacity           float64
	maxRouterGroupRatio              float64
	maxChannelRPCMetricNodes         int
	maxTransportRPCMetricNodes       int
	minChannelRPCWorkers             float64
	maxChannelRPCWorkers             float64
	maxChannelRPCQueueRatio          float64
	maxChannelRPCWorkerRatio         float64
	maxTransportRPCQueueRatio        float64
	maxTransportRPCBusyRatio         float64
	maxPermissionBatchActive         float64
	maxPermissionSlotRPCQueueRatio   float64
	maxPermissionSlotRPCInflight     float64
	maxAdvancePoolUtil               float64
	maxAdvancePoolWaiting            float64
	maxAppendPoolUtil                float64
	maxPostCommitPoolUtil            float64
	maxPostCommitBacklog             float64
	maxPostCommitHandoffRatio        float64
	maxMessageCommitQueueDepth       float64
	maxMessageMemTableBytes          float64
	maxMessageMemTableCount          float64
	maxMessageReadAmplification      float64
	maxMessageCompactionDebtBytes    float64
	maxMessageCompactions            float64
	maxMessageFlushes                float64
	maxHeapBytes                     float64
	maxAggregateHeapBytes            float64
	samples                          int
	sampleErrors                     int
}

type pressureSampler struct {
	cluster  *suite.StartedCluster
	interval time.Duration
	stopC    chan struct{}
	doneC    chan struct{}
	mu       sync.Mutex
	state    pressureSnapshot
}

func newPressureSampler(cluster *suite.StartedCluster, interval time.Duration) *pressureSampler {
	return &pressureSampler{
		cluster:  cluster,
		interval: interval,
		stopC:    make(chan struct{}),
		doneC:    make(chan struct{}),
	}
}

func (s *pressureSampler) start() {
	if os.Getenv("WK_E2E_MEDIUM_RECIPIENT_DISABLE_SAMPLER") == "1" {
		close(s.doneC)
		return
	}
	s.sample()
	go func() {
		defer close(s.doneC)
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.sample()
			case <-s.stopC:
				return
			}
		}
	}()
}

func (s *pressureSampler) stop() {
	select {
	case <-s.stopC:
	default:
		close(s.stopC)
	}
	<-s.doneC
}

func (s *pressureSampler) snapshot() pressureSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *pressureSampler) sample() {
	channelRPCMetricNodes := 0
	transportRPCMetricNodes := 0
	heapValues := make([]hotPathMetricValues, 0, len(s.cluster.Nodes))
	for _, node := range s.cluster.Nodes {
		samples, err := fetchPressureMetricSamples(node.APIAddr())
		s.mu.Lock()
		if err != nil {
			s.state.sampleErrors++
			s.mu.Unlock()
			continue
		}
		values := metricValues(samples)
		s.state.samples++
		s.observeValues(values)
		heapValues = append(heapValues, values)
		if values.channelRPCQueuePresent && values.channelRPCWorkersPresent {
			channelRPCMetricNodes++
		}
		if values.transportRPCMetricsPresent {
			transportRPCMetricNodes++
		}
		s.mu.Unlock()
	}
	s.mu.Lock()
	if len(heapValues) == len(s.cluster.Nodes) {
		s.observeAggregateHeap(heapValues)
	}
	if channelRPCMetricNodes > s.state.maxChannelRPCMetricNodes {
		s.state.maxChannelRPCMetricNodes = channelRPCMetricNodes
	}
	if transportRPCMetricNodes > s.state.maxTransportRPCMetricNodes {
		s.state.maxTransportRPCMetricNodes = transportRPCMetricNodes
	}
	s.mu.Unlock()
}

func fetchPressureMetricSamples(
	apiAddr string,
) ([]suite.MetricSample, error) {
	var lastErr error
	for attempt := 0; attempt < mediumMetricFetchAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(
			context.Background(), mediumMetricFetchTimeout,
		)
		samples, err := suite.FetchMetricSamples(ctx, apiAddr)
		cancel()
		if err == nil {
			return samples, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (s *pressureSampler) observeValues(values hotPathMetricValues) {
	s.state.maxGatewayQueueRatio = maxFloat(s.state.maxGatewayQueueRatio, ratio(values.gatewayQueueDepth, values.gatewayQueueCapacity))
	s.state.maxRecipientQueueRatio = maxFloat(s.state.maxRecipientQueueRatio, ratio(values.recipientQueueDepth, values.recipientQueueCapacity))
	s.state.maxRecipientWorkerRatio = maxFloat(s.state.maxRecipientWorkerRatio, ratio(values.recipientInflight, values.recipientCapacity))
	s.state.maxChannelReactorMailboxRatio = maxFloat(s.state.maxChannelReactorMailboxRatio, values.channelReactorMailboxRatio)
	s.state.maxChannelStoreAppendQueueRatio = maxFloat(s.state.maxChannelStoreAppendQueueRatio, values.channelStoreAppendQueueRatio)
	s.state.maxChannelStoreAppendWorkerRatio = maxFloat(s.state.maxChannelStoreAppendWorkerRatio, values.channelStoreAppendWorkerRatio)
	s.state.maxChannelStoreApplyQueueRatio = maxFloat(s.state.maxChannelStoreApplyQueueRatio, values.channelStoreApplyQueueRatio)
	s.state.maxChannelStoreApplyWorkerRatio = maxFloat(s.state.maxChannelStoreApplyWorkerRatio, values.channelStoreApplyWorkerRatio)
	s.state.maxRouterGroupInflight = maxFloat(s.state.maxRouterGroupInflight, values.routerGroupInflight)
	s.state.maxRouterGroupCapacity = maxFloat(s.state.maxRouterGroupCapacity, values.routerGroupCapacity)
	s.state.maxRouterGroupRatio = maxFloat(s.state.maxRouterGroupRatio, ratio(values.routerGroupInflight, values.routerGroupCapacity))
	if values.channelRPCWorkersPresent {
		if s.state.minChannelRPCWorkers == 0 || values.channelRPCWorkers < s.state.minChannelRPCWorkers {
			s.state.minChannelRPCWorkers = values.channelRPCWorkers
		}
		s.state.maxChannelRPCWorkers = maxFloat(s.state.maxChannelRPCWorkers, values.channelRPCWorkers)
	}
	s.state.maxChannelRPCQueueRatio = maxFloat(s.state.maxChannelRPCQueueRatio, ratio(values.channelRPCQueueDepth, values.channelRPCQueueCapacity))
	s.state.maxChannelRPCWorkerRatio = maxFloat(s.state.maxChannelRPCWorkerRatio, ratio(values.channelRPCInflight, values.channelRPCWorkers))
	s.state.maxTransportRPCQueueRatio = maxFloat(s.state.maxTransportRPCQueueRatio, ratio(values.transportRPCQueueDepth, values.transportRPCQueueCapacity))
	s.state.maxTransportRPCBusyRatio = maxFloat(s.state.maxTransportRPCBusyRatio, ratio(values.transportRPCBusy, values.transportRPCCapacity))
	s.state.maxPermissionBatchActive = maxFloat(s.state.maxPermissionBatchActive, values.permissionBatchActive)
	s.state.maxPermissionSlotRPCQueueRatio = maxFloat(s.state.maxPermissionSlotRPCQueueRatio, ratio(values.permissionSlotRPCQueueDepth, values.permissionSlotRPCQueueCapacity))
	s.state.maxPermissionSlotRPCInflight = maxFloat(s.state.maxPermissionSlotRPCInflight, values.permissionSlotRPCInflight)
	s.state.maxAdvancePoolUtil = maxFloat(s.state.maxAdvancePoolUtil, values.advanceUtil)
	s.state.maxAdvancePoolWaiting = maxFloat(s.state.maxAdvancePoolWaiting, values.advanceWaiting)
	s.state.maxAppendPoolUtil = maxFloat(s.state.maxAppendPoolUtil, values.appendUtil)
	s.state.maxPostCommitPoolUtil = maxFloat(s.state.maxPostCommitPoolUtil, values.postCommitUtil)
	s.state.maxPostCommitBacklog = maxFloat(s.state.maxPostCommitBacklog, values.postCommitBacklog)
	s.state.maxPostCommitHandoffRatio = maxFloat(s.state.maxPostCommitHandoffRatio, ratio(values.handoffDepth, values.handoffCapacity))
	s.state.maxMessageCommitQueueDepth = maxFloat(s.state.maxMessageCommitQueueDepth, values.messageCommitQueueDepth)
	s.state.maxMessageMemTableBytes = maxFloat(s.state.maxMessageMemTableBytes, values.messageMemTableBytes)
	s.state.maxMessageMemTableCount = maxFloat(s.state.maxMessageMemTableCount, values.messageMemTableCount)
	s.state.maxMessageReadAmplification = maxFloat(s.state.maxMessageReadAmplification, values.messageReadAmplification)
	s.state.maxMessageCompactionDebtBytes = maxFloat(s.state.maxMessageCompactionDebtBytes, values.messageCompactionDebtBytes)
	s.state.maxMessageCompactions = maxFloat(s.state.maxMessageCompactions, values.messageCompactions)
	s.state.maxMessageFlushes = maxFloat(s.state.maxMessageFlushes, values.messageFlushes)
	s.state.maxHeapBytes = maxFloat(s.state.maxHeapBytes, values.heapBytes)
}

// observeAggregateHeap records one complete cluster scrape and must be called
// while the sampler mutex is held.
func (s *pressureSampler) observeAggregateHeap(values []hotPathMetricValues) {
	var total float64
	for _, value := range values {
		total += value.heapBytes
	}
	s.state.maxAggregateHeapBytes = maxFloat(
		s.state.maxAggregateHeapBytes, total,
	)
}

type hotPathMetricValues struct {
	gatewayQueueDepth              float64
	gatewayQueueCapacity           float64
	recipientQueueDepth            float64
	recipientQueueCapacity         float64
	recipientInflight              float64
	recipientCapacity              float64
	channelReactorMailboxRatio     float64
	channelStoreAppendQueueRatio   float64
	channelStoreAppendWorkerRatio  float64
	channelStoreApplyQueueRatio    float64
	channelStoreApplyWorkerRatio   float64
	routerGroupInflight            float64
	routerGroupCapacity            float64
	channelRPCQueueDepth           float64
	channelRPCQueueCapacity        float64
	channelRPCInflight             float64
	channelRPCWorkers              float64
	channelRPCQueuePresent         bool
	channelRPCWorkersPresent       bool
	transportRPCQueueDepth         float64
	transportRPCQueueCapacity      float64
	transportRPCBusy               float64
	transportRPCCapacity           float64
	transportRPCMetricsPresent     bool
	permissionBatchActive          float64
	permissionSlotRPCQueueDepth    float64
	permissionSlotRPCQueueCapacity float64
	permissionSlotRPCInflight      float64
	advanceUtil                    float64
	advanceWaiting                 float64
	appendUtil                     float64
	postCommitUtil                 float64
	postCommitBacklog              float64
	handoffDepth                   float64
	handoffCapacity                float64
	messageCommitQueueDepth        float64
	messageMemTableBytes           float64
	messageMemTableCount           float64
	messageReadAmplification       float64
	messageCompactionDebtBytes     float64
	messageCompactions             float64
	messageFlushes                 float64
	heapBytes                      float64
}

func metricValues(samples []suite.MetricSample) hotPathMetricValues {
	var values hotPathMetricValues
	mailboxDepths := make(map[string]float64)
	mailboxCapacities := make(map[string]float64)
	var storeAppendQueueDepth float64
	var storeAppendQueueCapacity float64
	var storeAppendInflight float64
	var storeAppendWorkers float64
	var storeApplyQueueDepth float64
	var storeApplyQueueCapacity float64
	var storeApplyInflight float64
	var storeApplyWorkers float64
	for _, sample := range samples {
		switch sample.Name {
		case "wukongim_gateway_async_send_queue_depth":
			values.gatewayQueueDepth = sample.Value
		case "wukongim_gateway_async_send_queue_capacity":
			values.gatewayQueueCapacity = sample.Value
		case "wukongim_delivery_recipient_worker_queue_depth":
			values.recipientQueueDepth = sample.Value
		case "wukongim_delivery_recipient_worker_queue_capacity":
			values.recipientQueueCapacity = sample.Value
		case "wukongim_delivery_recipient_worker_inflight":
			values.recipientInflight = sample.Value
		case "wukongim_delivery_recipient_worker_capacity":
			values.recipientCapacity = sample.Value
		case "wukongim_runtime_pool_queue_depth":
			if isChannelReactorMailboxQueueSample(sample) {
				mailboxDepths[channelRuntimeQueueSampleKey(sample)] = sample.Value
			}
			if isChannelWorkerQueueSample(sample, "channelv2-store-append") {
				storeAppendQueueDepth = sample.Value
			}
			if isChannelWorkerQueueSample(sample, "channelv2-store-apply") {
				storeApplyQueueDepth = sample.Value
			}
			if isChannelRPCQueueSample(sample) {
				values.channelRPCQueueDepth = sample.Value
			}
			if isPermissionSlotRPCQueueSample(sample) {
				values.permissionSlotRPCQueueDepth += sample.Value
			}
		case "wukongim_runtime_pool_queue_capacity":
			if isChannelReactorMailboxQueueSample(sample) {
				mailboxCapacities[channelRuntimeQueueSampleKey(sample)] = sample.Value
			}
			if isChannelWorkerQueueSample(sample, "channelv2-store-append") {
				storeAppendQueueCapacity = sample.Value
			}
			if isChannelWorkerQueueSample(sample, "channelv2-store-apply") {
				storeApplyQueueCapacity = sample.Value
			}
			if isChannelRPCQueueSample(sample) {
				values.channelRPCQueueCapacity = sample.Value
				values.channelRPCQueuePresent = sample.Value > 0
			}
			if isPermissionSlotRPCQueueSample(sample) {
				values.permissionSlotRPCQueueCapacity += sample.Value
			}
		case "wukongim_runtime_pool_inflight":
			if isChannelWorkerPoolSample(sample, "channelv2-store-append") {
				storeAppendInflight = sample.Value
			}
			if isChannelWorkerPoolSample(sample, "channelv2-store-apply") {
				storeApplyInflight = sample.Value
			}
			if isChannelRPCPoolSample(sample) {
				values.channelRPCInflight = sample.Value
			}
			if isPermissionSlotRPCPoolSample(sample) {
				values.permissionSlotRPCInflight += sample.Value
			}
		case "wukongim_runtime_pool_workers":
			if isChannelWorkerPoolSample(sample, "channelv2-store-append") {
				storeAppendWorkers = sample.Value
			}
			if isChannelWorkerPoolSample(sample, "channelv2-store-apply") {
				storeApplyWorkers = sample.Value
			}
			if isChannelRPCPoolSample(sample) {
				values.channelRPCWorkers = sample.Value
				values.channelRPCWorkersPresent = sample.Value > 0
			}
		case "wukongim_goroutine_pool_busy_tasks":
			if isTransportRPCPoolSample(sample) {
				values.transportRPCBusy = sample.Value
			}
		case "wukongim_goroutine_pool_capacity":
			if isTransportRPCPoolSample(sample) {
				values.transportRPCCapacity = sample.Value
				values.transportRPCMetricsPresent = sample.Value > 0
			}
		case "wukongim_goroutine_pool_queue_depth":
			if isTransportRPCPoolSample(sample) {
				values.transportRPCQueueDepth = sample.Value
			}
		case "wukongim_goroutine_pool_queue_capacity":
			if isTransportRPCPoolSample(sample) {
				values.transportRPCQueueCapacity = sample.Value
			}
		case "wukongim_goroutines_active":
			if isPermissionBatchTaskSample(sample) {
				values.permissionBatchActive += sample.Value
			}
		case "wukongim_ants_pool_utilization":
			if sample.Labels["component"] != "channelappend" {
				continue
			}
			switch sample.Labels["pool"] {
			case "advance":
				values.advanceUtil = sample.Value
			case "append_effect":
				values.appendUtil = sample.Value
			case "post_commit":
				values.postCommitUtil = sample.Value
			}
		case "wukongim_ants_pool_waiting":
			if sample.Labels["component"] == "channelappend" && sample.Labels["pool"] == "advance" {
				values.advanceWaiting = sample.Value
			}
		case "wukongim_channelappend_writer_state_items":
			if sample.Labels["kind"] == "post_commit_backlog" {
				values.postCommitBacklog = sample.Value
			}
		case "wukongim_channelappend_post_commit_handoff_depth":
			values.handoffDepth = sample.Value
		case "wukongim_channelappend_post_commit_handoff_capacity":
			values.handoffCapacity = sample.Value
		case "wukongim_channelappend_router_group_inflight":
			values.routerGroupInflight = sample.Value
		case "wukongim_channelappend_router_group_capacity":
			values.routerGroupCapacity = sample.Value
		case "wukongim_storage_commit_queue_depth":
			if sample.Labels["store"] == "message" {
				values.messageCommitQueueDepth = sample.Value
			}
		case "wukongim_storage_pebble_memtable_size_bytes":
			if sample.Labels["store"] == "channel_log" {
				values.messageMemTableBytes = sample.Value
			}
		case "wukongim_storage_pebble_memtable_count":
			if sample.Labels["store"] == "channel_log" {
				values.messageMemTableCount = sample.Value
			}
		case "wukongim_storage_pebble_read_amplification":
			if sample.Labels["store"] == "channel_log" {
				values.messageReadAmplification = sample.Value
			}
		case "wukongim_storage_pebble_compaction_estimated_debt_bytes":
			if sample.Labels["store"] == "channel_log" {
				values.messageCompactionDebtBytes = sample.Value
			}
		case "wukongim_storage_pebble_compactions_in_progress":
			if sample.Labels["store"] == "channel_log" {
				values.messageCompactions = sample.Value
			}
		case "wukongim_storage_pebble_flushes_in_progress":
			if sample.Labels["store"] == "channel_log" {
				values.messageFlushes = sample.Value
			}
		case "go_memstats_heap_alloc_bytes":
			values.heapBytes = sample.Value
		}
	}
	for key, depth := range mailboxDepths {
		values.channelReactorMailboxRatio = maxFloat(
			values.channelReactorMailboxRatio,
			ratio(depth, mailboxCapacities[key]),
		)
	}
	values.channelStoreAppendQueueRatio = ratio(storeAppendQueueDepth, storeAppendQueueCapacity)
	values.channelStoreAppendWorkerRatio = ratio(storeAppendInflight, storeAppendWorkers)
	values.channelStoreApplyQueueRatio = ratio(storeApplyQueueDepth, storeApplyQueueCapacity)
	values.channelStoreApplyWorkerRatio = ratio(storeApplyInflight, storeApplyWorkers)
	return values
}

func isChannelReactorMailboxQueueSample(sample suite.MetricSample) bool {
	return sample.Labels["component"] == "channel" &&
		strings.HasPrefix(sample.Labels["pool"], "reactor_") &&
		sample.Labels["queue"] == "mailbox"
}

func channelRuntimeQueueSampleKey(sample suite.MetricSample) string {
	return sample.Labels["pool"] + "\x00" + sample.Labels["priority"]
}

func isChannelWorkerPoolSample(sample suite.MetricSample, pool string) bool {
	return sample.Labels["component"] == "channel" && sample.Labels["pool"] == pool
}

func isChannelWorkerQueueSample(sample suite.MetricSample, pool string) bool {
	return isChannelWorkerPoolSample(sample, pool) &&
		sample.Labels["queue"] == "worker" &&
		sample.Labels["priority"] == "none"
}

func isChannelRPCPoolSample(sample suite.MetricSample) bool {
	return sample.Labels["component"] == "channel" && sample.Labels["pool"] == "channelv2-rpc"
}

func isChannelRPCQueueSample(sample suite.MetricSample) bool {
	return isChannelRPCPoolSample(sample) &&
		sample.Labels["queue"] == "worker" &&
		sample.Labels["priority"] == "none"
}

func isTransportRPCPoolSample(sample suite.MetricSample) bool {
	return sample.Labels["module"] == "transport" &&
		sample.Labels["task"] == "rpc_executor" &&
		sample.Labels["kind"] == "pool"
}

func isPermissionBatchTaskSample(sample suite.MetricSample) bool {
	module := sample.Labels["module"]
	return (module == "message" || module == "slot") &&
		sample.Labels["task"] == "permission_batch" &&
		sample.Labels["kind"] == "burst"
}

func isPermissionSlotRPCCounterSample(sample suite.MetricSample) bool {
	return isPermissionSlotRPCServiceLabel(sample.Labels["service"])
}

func isPermissionSlotRPCPoolSample(sample suite.MetricSample) bool {
	return sample.Labels["component"] == "transport" &&
		isPermissionSlotRPCServiceLabel(sample.Labels["pool"])
}

func isPermissionSlotRPCQueueSample(sample suite.MetricSample) bool {
	return sample.Labels["component"] == "transport" &&
		sample.Labels["pool"] == "service" &&
		isPermissionSlotRPCServiceLabel(sample.Labels["queue"]) &&
		sample.Labels["priority"] == "none"
}

func isPermissionSlotRPCServiceLabel(label string) bool {
	return label == "slot channel metadata" ||
		label == "slot subscriber metadata" ||
		label == "slot permission metadata batch"
}

type hotPathCounters struct {
	allocatedBytes                      float64
	gcCount                             float64
	channelRPCAdmissionFull             float64
	channelRPCPullAdmissionFull         float64
	channelRPCHintAdmissionFull         float64
	channelRPCPullPaced                 float64
	channelRPCHintPaced                 float64
	channelStoreApplyTasks              float64
	channelStoreApplyAdmissionFull      float64
	channelStoreApplyPullPaced          float64
	channelStoreCheckpointTasks         float64
	channelStoreCheckpointAdmissionFull float64
	channelRPCPullBatches               float64
	channelRPCPullBatchItems            float64
	channelRPCHintBatches               float64
	channelRPCHintBatchItems            float64
	channelPullOKEmpty                  float64
	channelPullOKRecords                float64
	channelPullError                    float64
	channelAppendHintPaced              float64
	channelResumeHintPaced              float64
	membershipMutationRows              float64
	pluginReceiveAccepted               float64
	pluginReceiveFull                   float64
	pluginReceiveClosed                 float64
	pluginReceiveInvokeOK               float64
	pluginReceiveInvokeError            float64
	recipientProcessOK                  float64
	recipientProcessRecipientsOK        float64
	recipientProcessError               float64
	transportRPCRejected                float64
	permissionBatchStarted              float64
	permissionBatchPanics               float64
	permissionSlotRPCCalls              float64
	permissionSlotRPCErrors             float64
	permissionSlotRPCAdmissionErrors    float64
	messagePhysicalCommits              float64
	messageCommitBatchRequests          float64
	messageCommitBatchRecords           float64
	messageCommitBatchBytes             float64
	messageCommitSeconds                float64
	messageLeaderCommitRequests         float64
	messageFollowerCommitRequests       float64
	messageLeaderCommitSeconds          float64
	messageFollowerCommitSeconds        float64
	messageWALBytesIn                   float64
	messageWALBytesWritten              float64
	messageFlushBytesWritten            float64
	messageCompactionBytesRead          float64
	messageCompactionBytesWritten       float64
	messageSSTableSizeBytes             float64
	messageIdempotencyNegativeSkips     float64
	messageIdempotencyPointReads        float64
}

func (c hotPathCounters) subtract(start hotPathCounters) hotPathCounters {
	return hotPathCounters{
		allocatedBytes:                      c.allocatedBytes - start.allocatedBytes,
		gcCount:                             c.gcCount - start.gcCount,
		channelRPCAdmissionFull:             c.channelRPCAdmissionFull - start.channelRPCAdmissionFull,
		channelRPCPullAdmissionFull:         c.channelRPCPullAdmissionFull - start.channelRPCPullAdmissionFull,
		channelRPCHintAdmissionFull:         c.channelRPCHintAdmissionFull - start.channelRPCHintAdmissionFull,
		channelRPCPullPaced:                 c.channelRPCPullPaced - start.channelRPCPullPaced,
		channelRPCHintPaced:                 c.channelRPCHintPaced - start.channelRPCHintPaced,
		channelStoreApplyTasks:              c.channelStoreApplyTasks - start.channelStoreApplyTasks,
		channelStoreApplyAdmissionFull:      c.channelStoreApplyAdmissionFull - start.channelStoreApplyAdmissionFull,
		channelStoreApplyPullPaced:          c.channelStoreApplyPullPaced - start.channelStoreApplyPullPaced,
		channelStoreCheckpointTasks:         c.channelStoreCheckpointTasks - start.channelStoreCheckpointTasks,
		channelStoreCheckpointAdmissionFull: c.channelStoreCheckpointAdmissionFull - start.channelStoreCheckpointAdmissionFull,
		channelRPCPullBatches:               c.channelRPCPullBatches - start.channelRPCPullBatches,
		channelRPCPullBatchItems:            c.channelRPCPullBatchItems - start.channelRPCPullBatchItems,
		channelRPCHintBatches:               c.channelRPCHintBatches - start.channelRPCHintBatches,
		channelRPCHintBatchItems:            c.channelRPCHintBatchItems - start.channelRPCHintBatchItems,
		channelPullOKEmpty:                  c.channelPullOKEmpty - start.channelPullOKEmpty,
		channelPullOKRecords:                c.channelPullOKRecords - start.channelPullOKRecords,
		channelPullError:                    c.channelPullError - start.channelPullError,
		channelAppendHintPaced:              c.channelAppendHintPaced - start.channelAppendHintPaced,
		channelResumeHintPaced:              c.channelResumeHintPaced - start.channelResumeHintPaced,
		membershipMutationRows:              c.membershipMutationRows - start.membershipMutationRows,
		pluginReceiveAccepted:               c.pluginReceiveAccepted - start.pluginReceiveAccepted,
		pluginReceiveFull:                   c.pluginReceiveFull - start.pluginReceiveFull,
		pluginReceiveClosed:                 c.pluginReceiveClosed - start.pluginReceiveClosed,
		pluginReceiveInvokeOK:               c.pluginReceiveInvokeOK - start.pluginReceiveInvokeOK,
		pluginReceiveInvokeError:            c.pluginReceiveInvokeError - start.pluginReceiveInvokeError,
		recipientProcessOK:                  c.recipientProcessOK - start.recipientProcessOK,
		recipientProcessRecipientsOK:        c.recipientProcessRecipientsOK - start.recipientProcessRecipientsOK,
		recipientProcessError:               c.recipientProcessError - start.recipientProcessError,
		transportRPCRejected:                c.transportRPCRejected - start.transportRPCRejected,
		permissionBatchStarted:              c.permissionBatchStarted - start.permissionBatchStarted,
		permissionBatchPanics:               c.permissionBatchPanics - start.permissionBatchPanics,
		permissionSlotRPCCalls:              c.permissionSlotRPCCalls - start.permissionSlotRPCCalls,
		permissionSlotRPCErrors:             c.permissionSlotRPCErrors - start.permissionSlotRPCErrors,
		permissionSlotRPCAdmissionErrors:    c.permissionSlotRPCAdmissionErrors - start.permissionSlotRPCAdmissionErrors,
		messagePhysicalCommits:              c.messagePhysicalCommits - start.messagePhysicalCommits,
		messageCommitBatchRequests:          c.messageCommitBatchRequests - start.messageCommitBatchRequests,
		messageCommitBatchRecords:           c.messageCommitBatchRecords - start.messageCommitBatchRecords,
		messageCommitBatchBytes:             c.messageCommitBatchBytes - start.messageCommitBatchBytes,
		messageCommitSeconds:                c.messageCommitSeconds - start.messageCommitSeconds,
		messageLeaderCommitRequests:         c.messageLeaderCommitRequests - start.messageLeaderCommitRequests,
		messageFollowerCommitRequests:       c.messageFollowerCommitRequests - start.messageFollowerCommitRequests,
		messageLeaderCommitSeconds:          c.messageLeaderCommitSeconds - start.messageLeaderCommitSeconds,
		messageFollowerCommitSeconds:        c.messageFollowerCommitSeconds - start.messageFollowerCommitSeconds,
		messageWALBytesIn:                   c.messageWALBytesIn - start.messageWALBytesIn,
		messageWALBytesWritten:              c.messageWALBytesWritten - start.messageWALBytesWritten,
		messageFlushBytesWritten:            c.messageFlushBytesWritten - start.messageFlushBytesWritten,
		messageCompactionBytesRead:          c.messageCompactionBytesRead - start.messageCompactionBytesRead,
		messageCompactionBytesWritten:       c.messageCompactionBytesWritten - start.messageCompactionBytesWritten,
		messageSSTableSizeBytes:             c.messageSSTableSizeBytes - start.messageSSTableSizeBytes,
		messageIdempotencyNegativeSkips:     c.messageIdempotencyNegativeSkips - start.messageIdempotencyNegativeSkips,
		messageIdempotencyPointReads:        c.messageIdempotencyPointReads - start.messageIdempotencyPointReads,
	}
}

type messageCommitSummary struct {
	StoreApplyTasks     float64
	StoreApplyFull      float64
	StoreApplyPullPaced float64
	CheckpointTasks     float64
	CheckpointFull      float64
	PhysicalCommits     float64
	BatchRequests       float64
	BatchRecords        float64
	BatchBytes          float64
	CommitSeconds       float64
	LeaderRequests      float64
	FollowerRequests    float64
	LeaderSeconds       float64
	FollowerSeconds     float64
	WALBytesIn          float64
	WALBytesWritten     float64
	FlushBytes          float64
	CompactionRead      float64
	CompactionWrite     float64
	SSTableSizeDelta    float64
	IdempotencySkips    float64
	IdempotencyReads    float64
}

type channelRPCAdmissionSummary struct {
	Full      float64
	PullFull  float64
	HintFull  float64
	PullPaced float64
	HintPaced float64
}

func (c hotPathCounters) channelRPCAdmissionSummary() channelRPCAdmissionSummary {
	return channelRPCAdmissionSummary{
		Full:      c.channelRPCAdmissionFull,
		PullFull:  c.channelRPCPullAdmissionFull,
		HintFull:  c.channelRPCHintAdmissionFull,
		PullPaced: c.channelRPCPullPaced,
		HintPaced: c.channelRPCHintPaced,
	}
}

func (c hotPathCounters) messageCommitSummary() messageCommitSummary {
	return messageCommitSummary{
		StoreApplyTasks:     c.channelStoreApplyTasks,
		StoreApplyFull:      c.channelStoreApplyAdmissionFull,
		StoreApplyPullPaced: c.channelStoreApplyPullPaced,
		CheckpointTasks:     c.channelStoreCheckpointTasks,
		CheckpointFull:      c.channelStoreCheckpointAdmissionFull,
		PhysicalCommits:     c.messagePhysicalCommits,
		BatchRequests:       c.messageCommitBatchRequests,
		BatchRecords:        c.messageCommitBatchRecords,
		BatchBytes:          c.messageCommitBatchBytes,
		CommitSeconds:       c.messageCommitSeconds,
		LeaderRequests:      c.messageLeaderCommitRequests,
		FollowerRequests:    c.messageFollowerCommitRequests,
		LeaderSeconds:       c.messageLeaderCommitSeconds,
		FollowerSeconds:     c.messageFollowerCommitSeconds,
		WALBytesIn:          c.messageWALBytesIn,
		WALBytesWritten:     c.messageWALBytesWritten,
		FlushBytes:          c.messageFlushBytesWritten,
		CompactionRead:      c.messageCompactionBytesRead,
		CompactionWrite:     c.messageCompactionBytesWritten,
		SSTableSizeDelta:    c.messageSSTableSizeBytes,
		IdempotencySkips:    c.messageIdempotencyNegativeSkips,
		IdempotencyReads:    c.messageIdempotencyPointReads,
	}
}

type recipientProcessSummary struct {
	OK         float64
	Recipients float64
	Errors     float64
}

func (c hotPathCounters) recipientProcessSummary() recipientProcessSummary {
	return recipientProcessSummary{
		OK:         c.recipientProcessOK,
		Recipients: c.recipientProcessRecipientsOK,
		Errors:     c.recipientProcessError,
	}
}

func capturePermissionSoakCommitDelta(cluster *suite.StartedCluster, start hotPathCounters) (hotPathCounters, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	current, err := captureHotPathCounters(ctx, cluster)
	if err != nil {
		return hotPathCounters{}, err
	}
	return current.subtract(start), nil
}

func mustCaptureHotPathCounters(t *testing.T, cluster *suite.StartedCluster) hotPathCounters {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	counters, err := captureHotPathCounters(ctx, cluster)
	if err != nil {
		t.Fatalf("capture hot-path counters: %v", err)
	}
	return counters
}

func captureHotPathCounters(ctx context.Context, cluster *suite.StartedCluster) (hotPathCounters, error) {
	var counters hotPathCounters
	for _, node := range cluster.Nodes {
		samples, err := suite.FetchMetricSamples(ctx, node.APIAddr())
		if err != nil {
			return hotPathCounters{}, fmt.Errorf("node %d metrics: %w", node.Spec.ID, err)
		}
		for _, sample := range samples {
			observeHotPathCounterSample(&counters, sample)
			switch sample.Name {
			case "go_memstats_alloc_bytes_total":
				counters.allocatedBytes += sample.Value
			case "go_gc_duration_seconds_count":
				counters.gcCount += sample.Value
			case "wukongim_runtime_pool_admission_total":
				if isChannelRPCQueueSample(sample) && sample.Labels["result"] == "full" {
					counters.channelRPCAdmissionFull += sample.Value
				}
			case "wukongim_channelv2_worker_batch_items_count":
				switch sample.Labels["kind"] {
				case "rpc_pull":
					counters.channelRPCPullBatches += sample.Value
				case "rpc_pull_hint":
					counters.channelRPCHintBatches += sample.Value
				}
			case "wukongim_channelv2_worker_batch_items_sum":
				switch sample.Labels["kind"] {
				case "rpc_pull":
					counters.channelRPCPullBatchItems += sample.Value
				case "rpc_pull_hint":
					counters.channelRPCHintBatchItems += sample.Value
				}
			case "wukongim_conversation_membership_mutation_rows_total":
				if sample.Labels["directory"] == "ordinary" {
					counters.membershipMutationRows += sample.Value
				}
			case "wukongim_plugin_hook_enqueue_total":
				if sample.Labels["method"] != "receive" {
					continue
				}
				switch sample.Labels["result"] {
				case "accepted":
					counters.pluginReceiveAccepted += sample.Value
				case "full":
					counters.pluginReceiveFull += sample.Value
				case "closed":
					counters.pluginReceiveClosed += sample.Value
				}
			case "wukongim_plugin_hook_invoke_total":
				if sample.Labels["method"] != "receive" {
					continue
				}
				switch sample.Labels["result"] {
				case "ok":
					counters.pluginReceiveInvokeOK += sample.Value
				case "error", "timeout", "panic":
					counters.pluginReceiveInvokeError += sample.Value
				}
			case "wukongim_goroutine_pool_rejected_total":
				if isTransportRPCPoolSample(sample) {
					counters.transportRPCRejected += sample.Value
				}
			case "wukongim_goroutines_started_total":
				if isPermissionBatchTaskSample(sample) {
					counters.permissionBatchStarted += sample.Value
				}
			case "wukongim_goroutines_panics_total":
				if isPermissionBatchTaskSample(sample) {
					counters.permissionBatchPanics += sample.Value
				}
			}
		}
	}
	return counters, nil
}

func observeHotPathCounterSample(counters *hotPathCounters, sample suite.MetricSample) {
	switch sample.Name {
	case "wukongim_transport_rpc_total":
		if !isPermissionSlotRPCCounterSample(sample) {
			return
		}
		counters.permissionSlotRPCCalls += sample.Value
		if sample.Labels["result"] != "ok" {
			counters.permissionSlotRPCErrors += sample.Value
		}
	case "wukongim_runtime_pool_admission_total":
		if isPermissionSlotRPCQueueSample(sample) && sample.Labels["result"] != "ok" {
			counters.permissionSlotRPCAdmissionErrors += sample.Value
		}
		if isChannelWorkerQueueSample(sample, "channelv2-store-checkpoint") && sample.Labels["result"] == "full" {
			counters.channelStoreCheckpointAdmissionFull += sample.Value
		}
	case "wukongim_channelv2_worker_task_duration_seconds_count":
		if sample.Labels["result"] != "ok" {
			return
		}
		switch sample.Labels["kind"] {
		case "store_apply":
			counters.channelStoreApplyTasks += sample.Value
		case "store_checkpoint":
			counters.channelStoreCheckpointTasks += sample.Value
		}
	case "wukongim_channelv2_worker_admission_total":
		if sample.Labels["pool"] == "channelv2-store-apply" {
			switch {
			case sample.Labels["kind"] == "store_apply" && sample.Labels["result"] == "full":
				counters.channelStoreApplyAdmissionFull += sample.Value
			case sample.Labels["kind"] == "rpc_pull" && sample.Labels["result"] == "paced":
				counters.channelStoreApplyPullPaced += sample.Value
			}
			return
		}
		if sample.Labels["pool"] != "channelv2-rpc" {
			return
		}
		switch sample.Labels["result"] {
		case "full":
			switch sample.Labels["kind"] {
			case "rpc_pull":
				counters.channelRPCPullAdmissionFull += sample.Value
			case "rpc_pull_hint":
				counters.channelRPCHintAdmissionFull += sample.Value
			}
		case "paced":
			switch sample.Labels["kind"] {
			case "rpc_pull":
				counters.channelRPCPullPaced += sample.Value
			case "rpc_pull_hint":
				counters.channelRPCHintPaced += sample.Value
			}
		}
	case "wukongim_channelv2_pull_total":
		switch {
		case sample.Labels["result"] == "ok" && sample.Labels["empty"] == "true":
			counters.channelPullOKEmpty += sample.Value
		case sample.Labels["result"] == "ok" && sample.Labels["empty"] == "false":
			counters.channelPullOKRecords += sample.Value
		case sample.Labels["result"] == "err":
			counters.channelPullError += sample.Value
		}
	case "wukongim_channelv2_pull_hint_total":
		if sample.Labels["result"] != "paced" {
			return
		}
		switch sample.Labels["reason"] {
		case "append":
			counters.channelAppendHintPaced += sample.Value
		case "resume":
			counters.channelResumeHintPaced += sample.Value
		}
	case "wukongim_storage_commit_batch_duration_seconds_count":
		if isSuccessfulMessageCommitBatchSample(sample) && sample.Labels["stage"] == "commit" {
			counters.messagePhysicalCommits += sample.Value
		}
	case "wukongim_storage_commit_batch_duration_seconds_sum":
		if isSuccessfulMessageCommitBatchSample(sample) && sample.Labels["stage"] == "commit" {
			counters.messageCommitSeconds += sample.Value
		}
	case "wukongim_storage_commit_batch_requests_sum":
		if sample.Labels["store"] == "message" {
			counters.messageCommitBatchRequests += sample.Value
		}
	case "wukongim_storage_commit_batch_records_sum":
		if sample.Labels["store"] == "message" {
			counters.messageCommitBatchRecords += sample.Value
		}
	case "wukongim_storage_commit_batch_bytes_sum":
		if sample.Labels["store"] == "message" {
			counters.messageCommitBatchBytes += sample.Value
		}
	case "wukongim_storage_commit_request_duration_seconds_count":
		observeMessageCommitRequestCounter(counters, sample, false)
	case "wukongim_storage_commit_request_duration_seconds_sum":
		observeMessageCommitRequestCounter(counters, sample, true)
	case "wukongim_storage_pebble_wal_bytes_in":
		if sample.Labels["store"] == "channel_log" {
			counters.messageWALBytesIn += sample.Value
		}
	case "wukongim_storage_pebble_wal_bytes_written":
		if sample.Labels["store"] == "channel_log" {
			counters.messageWALBytesWritten += sample.Value
		}
	case "wukongim_storage_pebble_flush_bytes_written":
		if sample.Labels["store"] == "channel_log" {
			counters.messageFlushBytesWritten += sample.Value
		}
	case "wukongim_storage_pebble_compaction_bytes_read":
		if sample.Labels["store"] == "channel_log" {
			counters.messageCompactionBytesRead += sample.Value
		}
	case "wukongim_storage_pebble_compaction_bytes_written":
		if sample.Labels["store"] == "channel_log" {
			counters.messageCompactionBytesWritten += sample.Value
		}
	case "wukongim_storage_pebble_sstable_size_bytes":
		if sample.Labels["store"] == "channel_log" {
			counters.messageSSTableSizeBytes += sample.Value
		}
	case "wukongim_storage_message_idempotency_negative_filter_skips":
		if sample.Labels["store"] == "channel_log" {
			counters.messageIdempotencyNegativeSkips += sample.Value
		}
	case "wukongim_storage_message_idempotency_point_reads":
		if sample.Labels["store"] == "channel_log" {
			counters.messageIdempotencyPointReads += sample.Value
		}
	case "wukongim_delivery_recipient_worker_process_total":
		if sample.Labels["result"] == "ok" {
			counters.recipientProcessOK += sample.Value
		} else {
			counters.recipientProcessError += sample.Value
		}
	case "wukongim_delivery_recipient_worker_process_recipients_sum":
		if sample.Labels["result"] == "ok" {
			counters.recipientProcessRecipientsOK += sample.Value
		}
	}
}

func isSuccessfulMessageCommitBatchSample(sample suite.MetricSample) bool {
	return sample.Labels["store"] == "message" && sample.Labels["result"] == "ok"
}

func observeMessageCommitRequestCounter(counters *hotPathCounters, sample suite.MetricSample, duration bool) {
	if sample.Labels["store"] != "message" || sample.Labels["result"] != "ok" {
		return
	}
	switch sample.Labels["lane"] {
	case "leader_append":
		if duration {
			counters.messageLeaderCommitSeconds += sample.Value
		} else {
			counters.messageLeaderCommitRequests += sample.Value
		}
	case "follower_apply":
		if duration {
			counters.messageFollowerCommitSeconds += sample.Value
		} else {
			counters.messageFollowerCommitRequests += sample.Value
		}
	}
}

func permissionSlotRPCMetricDiagnostics(cluster *suite.StartedCluster) string {
	if cluster == nil {
		return "cluster unavailable"
	}
	lines := make([]string, 0, 32)
	for _, node := range cluster.Nodes {
		ctx, cancel := context.WithTimeout(context.Background(), mediumMetricFetchTimeout)
		samples, err := suite.FetchMetricSamples(ctx, node.APIAddr())
		cancel()
		if err != nil {
			lines = append(lines, fmt.Sprintf("node=%d error=%v", node.Spec.ID, err))
			continue
		}
		for _, sample := range samples {
			if sample.Name != "wukongim_transport_rpc_total" &&
				!strings.Contains(sample.Labels["service"], "slot") &&
				!strings.Contains(sample.Labels["pool"], "slot") &&
				!strings.Contains(sample.Labels["queue"], "slot") {
				continue
			}
			switch sample.Name {
			case "wukongim_transport_rpc_total",
				"wukongim_runtime_pool_queue_depth",
				"wukongim_runtime_pool_queue_capacity",
				"wukongim_runtime_pool_inflight",
				"wukongim_runtime_pool_admission_total":
				lines = append(lines, fmt.Sprintf("node=%d name=%s labels=%v value=%.0f", node.Spec.ID, sample.Name, sample.Labels, sample.Value))
			}
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "; ")
}

func waitForPluginReceiveDrain(
	ctx context.Context,
	cluster *suite.StartedCluster,
	start hotPathCounters,
	expectedBatches float64,
) (hotPathCounters, error) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var last hotPathCounters
	for {
		current, err := captureHotPathCounters(ctx, cluster)
		if err != nil {
			return hotPathCounters{}, err
		}
		last = current.subtract(start)
		enqueueTotal := last.pluginReceiveAccepted + last.pluginReceiveFull + last.pluginReceiveClosed
		invokeTotal := last.pluginReceiveInvokeOK + last.pluginReceiveInvokeError
		if enqueueTotal >= expectedBatches && invokeTotal >= last.pluginReceiveAccepted {
			return current, nil
		}
		select {
		case <-ctx.Done():
			return hotPathCounters{}, fmt.Errorf(
				"enqueue %.0f/%.0f accepted %.0f invoked %.0f: %w",
				enqueueTotal,
				expectedBatches,
				last.pluginReceiveAccepted,
				invokeTotal,
				ctx.Err(),
			)
		case <-ticker.C:
		}
	}
}

func waitForHotPathDrain(ctx context.Context, cluster *suite.StartedCluster) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var last []string
	for {
		last = last[:0]
		for _, node := range cluster.Nodes {
			samples, err := suite.FetchMetricSamples(ctx, node.APIAddr())
			if err != nil {
				last = append(last, fmt.Sprintf("node-%d metrics: %v", node.Spec.ID, err))
				continue
			}
			for _, sample := range samples {
				if isDrainGauge(sample) && sample.Value != 0 {
					last = append(last, fmt.Sprintf("node-%d %s%v=%v", node.Spec.ID, sample.Name, sample.Labels, sample.Value))
				}
			}
		}
		if len(last) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), fmt.Errorf("remaining gauges: %v", last))
		case <-ticker.C:
		}
	}
}

func isDrainGauge(sample suite.MetricSample) bool {
	switch sample.Name {
	case "wukongim_gateway_async_send_queue_depth",
		"wukongim_delivery_recipient_worker_queue_depth",
		"wukongim_delivery_recipient_worker_inflight",
		"wukongim_delivery_ack_bindings",
		"wukongim_channelappend_post_commit_handoff_depth":
		return true
	case "wukongim_channelappend_writer_state_items":
		switch sample.Labels["kind"] {
		case "pending_append", "append_inflight", "post_commit_backlog":
			return true
		}
	case "wukongim_ants_pool_waiting":
		return sample.Labels["component"] == "channelappend"
	}
	return false
}

func ratio(value, capacity float64) float64 {
	if capacity <= 0 {
		return 0
	}
	return value / capacity
}

func maxFloat(left, right float64) float64 {
	if right > left {
		return right
	}
	return left
}

func hotPathRuntimeDiagnostics(cluster *suite.StartedCluster) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out := make(map[string]map[string]float64, len(cluster.Nodes))
	for _, node := range cluster.Nodes {
		nodeMetrics := map[string]float64{}
		samples, err := suite.FetchMetricSamples(ctx, node.APIAddr())
		if err != nil {
			nodeMetrics["metrics_fetch_error"] = 1
			out[fmt.Sprintf("node-%d", node.Spec.ID)] = nodeMetrics
			continue
		}
		for _, sample := range samples {
			if !isHotPathDiagnosticMetric(sample.Name) {
				continue
			}
			if strings.HasPrefix(sample.Name, "wukongim_ants_pool_") && sample.Labels["component"] != "channelappend" {
				continue
			}
			if strings.HasPrefix(sample.Name, "wukongim_runtime_pool_") {
				component := sample.Labels["component"]
				if component != "gateway" && component != "channel" {
					continue
				}
			}
			nodeMetrics[diagnosticMetricKey(sample)] = sample.Value
		}
		out[fmt.Sprintf("node-%d", node.Spec.ID)] = nodeMetrics
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return fmt.Sprintf(`{"marshal_error":%q}`, err.Error())
	}
	return string(encoded)
}

func isHotPathDiagnosticMetric(name string) bool {
	for _, prefix := range []string{
		"wukongim_gateway_messages_received_total",
		"wukongim_gateway_sendacks_total",
		"wukongim_gateway_async_send_queue_",
		"wukongim_gateway_async_send_dispatch_wait_duration_seconds_count",
		"wukongim_gateway_async_send_dispatch_wait_duration_seconds_sum",
		"wukongim_gateway_async_send_batch_records_count",
		"wukongim_gateway_async_send_batch_records_sum",
		"wukongim_gateway_async_send_batch_bytes_count",
		"wukongim_gateway_async_send_batch_bytes_sum",
		"wukongim_gateway_async_send_batch_wait_duration_seconds_count",
		"wukongim_gateway_async_send_batch_wait_duration_seconds_sum",
		"wukongim_gateway_frame_handle_duration_seconds_count",
		"wukongim_gateway_frame_handle_duration_seconds_sum",
		"wukongim_runtime_pool_admission_total",
		"wukongim_runtime_pool_queue_wait_duration_seconds_count",
		"wukongim_runtime_pool_queue_wait_duration_seconds_sum",
		"wukongim_channelappend_router_total",
		"wukongim_channelappend_router_duration_seconds_count",
		"wukongim_channelappend_router_duration_seconds_sum",
		"wukongim_channelappend_effect_duration_seconds_count",
		"wukongim_channelappend_effect_duration_seconds_sum",
		"wukongim_channelappend_local_admission_total",
		"wukongim_channelappend_writer_admission_depth",
		"wukongim_channelappend_writer_pool_running",
		"wukongim_channelappend_writer_state_items",
		"wukongim_channelappend_post_commit_",
		"wukongim_ants_pool_",
		"wukongim_delivery_recipient_worker_queue_",
		"wukongim_delivery_recipient_worker_inflight",
		"wukongim_delivery_recipient_worker_capacity",
		"wukongim_delivery_recipient_worker_process_total",
		"wukongim_delivery_recipient_worker_process_duration_seconds_count",
		"wukongim_delivery_recipient_worker_process_duration_seconds_sum",
		"wukongim_channelv2_append_duration_seconds_count",
		"wukongim_channelv2_append_duration_seconds_sum",
		"wukongim_channelv2_append_stage_duration_seconds_count",
		"wukongim_channelv2_append_stage_duration_seconds_sum",
		"wukongim_channelv2_append_wait_stage_duration_seconds_count",
		"wukongim_channelv2_append_wait_stage_duration_seconds_sum",
		"wukongim_channelv2_worker_task_duration_seconds_count",
		"wukongim_channelv2_worker_task_duration_seconds_sum",
		"wukongim_channelv2_worker_admission_total",
		"wukongim_conversation_directory_",
		"wukongim_conversation_hydration_",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func hotPathGoroutineDiagnostics(cluster *suite.StartedCluster) string {
	return hotPathGoroutineDiagnosticsMatching(cluster, []string{
		"internal/usecase/message",
		"internal/access/gateway",
		"internal/runtime/channelappend",
		"internal/infra/cluster",
		"pkg/slot/proxy",
		"pkg/gateway/core",
	}, 96<<10)
}

func hotPathBottleneckGoroutineDiagnostics(cluster *suite.StartedCluster) string {
	return hotPathGoroutineDiagnosticsMatching(cluster, []string{
		"internal/runtime/channelappend.(*Future).Wait",
		"internal/runtime/channelappend.(*Router).submitGroup",
		"internal/runtime/channelappend.appendEffect.run",
		"internal/infra/cluster.(*ChannelAppender).AppendBatch",
		"internal/infra/cluster.(*ChannelAppendClient).ForwardSendBatch",
		"pkg/cluster/channels.(*Service).AppendBatch",
		"pkg/cluster/channels.(*TransportClient).ForwardAppendBatch",
		"pkg/channel/reactor",
		"pkg/channel/service",
		"pkg/channel/worker",
		"internal/runtime/delivery",
	}, 64<<10)
}

func hotPathGoroutineDiagnosticsMatching(cluster *suite.StartedCluster, matches []string, maxOutputBytes int) string {
	const maxProfileBytes = 2 << 20
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out strings.Builder
	for _, node := range cluster.Nodes {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+node.APIAddr()+"/debug/goroutines", nil)
		if err != nil {
			continue
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			fmt.Fprintf(&out, "node-%d fetch=%v\n", node.Spec.ID, err)
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxProfileBytes))
		_ = response.Body.Close()
		if readErr != nil {
			fmt.Fprintf(&out, "node-%d read=%v\n", node.Spec.ID, readErr)
			continue
		}
		for _, block := range strings.Split(string(body), "\n\n") {
			if !goroutineBlockMatches(block, matches) {
				continue
			}
			fmt.Fprintf(&out, "node-%d\n%s\n\n", node.Spec.ID, block)
			if out.Len() >= maxOutputBytes {
				return out.String()[:maxOutputBytes]
			}
		}
	}
	return out.String()
}

func goroutineBlockMatches(block string, matches []string) bool {
	for _, match := range matches {
		if strings.Contains(block, match) {
			return true
		}
	}
	return false
}

func diagnosticMetricKey(sample suite.MetricSample) string {
	if len(sample.Labels) == 0 {
		return sample.Name
	}
	keys := make([]string, 0, len(sample.Labels))
	for key := range sample.Labels {
		if key == "node_id" || key == "node_name" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out strings.Builder
	out.WriteString(sample.Name)
	out.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			out.WriteByte(',')
		}
		out.WriteString(key)
		out.WriteByte('=')
		out.WriteString(sample.Labels[key])
	}
	out.WriteByte('}')
	return out.String()
}
