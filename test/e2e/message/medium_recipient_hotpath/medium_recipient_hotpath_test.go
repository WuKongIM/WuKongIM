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

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
)

const (
	mediumEvidenceSchema    = "wukongim/local-medium-rc-hifi-evidence/v2"
	mediumPhysicalHashSlots = 256
	mediumLogicalSlots      = 10
	mediumReplicaCount      = 3
	mediumMessageCount      = 250
	mediumRecipientRows     = 19_650
	mediumOnlineRoutes      = 2_545
	mediumPayloadBytes      = 256
	mediumMeasuredRounds    = 80
	mediumOfferedQPS        = 4_500
	mediumCIAcceptanceQPS   = 500
	mediumMinOfferedQPS     = 500
	mediumRecipientPlanSize = 512
	mediumSenderConnections = 25
	mediumGroupSenders      = 5
	mediumSenderUIDPrefix   = "wkrc-hifi-sender"

	mediumMinIngressFraction                   = 0.995
	mediumMaxAllocatedBytesPerMessage          = 360_000
	mediumMaxBackgroundAllocatedBytesPerSecond = 40_000_000
	mediumMaxGCPerMessage                      = 0.0075
	mediumMaxHeapBytes                         = 512 << 20
	mediumMetricSampleInterval                 = 250 * time.Millisecond
	mediumCIMetricSampleInterval               = 500 * time.Millisecond
)

var mediumGroupProfiles = []struct {
	messages   int
	recipients int
	online     int
}{
	{messages: 60, recipients: 20, online: 5},
	{messages: 42, recipients: 100, online: 15},
	{messages: 18, recipients: 500, online: 55},
	{messages: 5, recipients: 1_000, online: 100},
}

type hotPathMessage struct {
	clientSeq   uint64
	clientMsgNo string
	channelID   string
	channelType uint8
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
	Messages                 int      `json:"messages"`
	RecipientRows            int      `json:"recipient_rows"`
	OnlineRoutes             int      `json:"online_routes"`
	Connections              int      `json:"connections"`
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
	MaxAdvancePoolUtil       float64  `json:"max_advance_pool_utilization"`
	MaxAdvancePoolWaiting    float64  `json:"max_advance_pool_waiting"`
	MaxAppendPoolUtil        float64  `json:"max_append_pool_utilization"`
	MaxPostCommitPoolUtil    float64  `json:"max_post_commit_pool_utilization"`
	MaxPostCommitBacklog     float64  `json:"max_post_commit_backlog"`
	MaxPostCommitHandoffRate float64  `json:"max_post_commit_handoff_ratio"`
	MaxHeapBytes             float64  `json:"max_heap_bytes"`
	AllocatedBytes           float64  `json:"allocated_bytes"`
	GCCountDelta             float64  `json:"gc_count_delta"`
	PluginReceiveAccepted    float64  `json:"plugin_receive_enqueue_accepted"`
	PluginReceiveFull        float64  `json:"plugin_receive_enqueue_full"`
	PluginReceiveClosed      float64  `json:"plugin_receive_enqueue_closed"`
	PluginReceiveInvokeOK    float64  `json:"plugin_receive_invoke_ok"`
	PluginReceiveInvokeError float64  `json:"plugin_receive_invoke_error"`
	MetricSamples            int      `json:"metric_samples"`
	MetricSampleErrors       int      `json:"metric_sample_errors"`
	Drained                  bool     `json:"drained"`
	ProcessContinuous        bool     `json:"process_continuous"`
}

func TestCloudMediumScaledRecipientHotPath(t *testing.T) {
	if os.Getenv("WK_E2E_MEDIUM_RECIPIENT_HOTPATH") != "1" {
		t.Skip("set WK_E2E_MEDIUM_RECIPIENT_HOTPATH=1 to run the bounded higher-fidelity gate")
	}
	measuredRounds := boundedPositiveEnvInt(t, "WK_E2E_MEDIUM_RECIPIENT_ROUNDS", mediumMeasuredRounds, 1, 200)
	offeredQPS := boundedPositiveEnvInt(t, "WK_E2E_MEDIUM_RECIPIENT_QPS", mediumOfferedQPS, mediumMinOfferedQPS, 20_000)
	expectedAcceptanceQPS := mediumOfferedQPS
	metricSampleInterval := mediumMetricSampleInterval
	if os.Getenv("WK_E2E_MEDIUM_RECIPIENT_CI_SCALE") == "1" {
		expectedAcceptanceQPS = mediumCIAcceptanceQPS
		metricSampleInterval = mediumCIMetricSampleInterval
	}
	if os.Getenv("WK_E2E_MEDIUM_RECIPIENT_ENFORCE_ACCEPTANCE") == "1" && offeredQPS != expectedAcceptanceQPS {
		t.Fatalf("acceptance offered QPS = %d, want %d", offeredQPS, expectedAcceptanceQPS)
	}

	cluster := startMediumCluster(t)
	setupCtx, setupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
	groupChannels, groupRecipients, groupOnline := prepareGroupChannels(t, setupCtx, cluster.MustNode(1))
	personRecipients := make([]string, 125)
	for i := range personRecipients {
		personRecipients[i] = fmt.Sprintf("wkrc-person-%03d", i)
	}

	recipients := connectRecipients(t, cluster, groupOnline, personRecipients)
	defer closeRecipients(recipients)

	primeMessages := buildMessages(groupChannels, personRecipients)
	if len(primeMessages) != mediumMessageCount || groupRecipients+len(personRecipients)*2 != mediumRecipientRows {
		t.Fatalf("fixture shape messages=%d recipient_rows=%d, want %d/%d", len(primeMessages), groupRecipients+len(personRecipients)*2, mediumMessageCount, mediumRecipientRows)
	}
	expectedOnline := 0
	for _, recipient := range recipients {
		expectedOnline += recipient.expected
	}
	if expectedOnline != mediumOnlineRoutes {
		t.Fatalf("fixture online routes = %d, want %d", expectedOnline, mediumOnlineRoutes)
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
	coldPrimeDuration := primeHotPathChannels(t, cluster, primeSender, recipients, primeMessages, payload)
	_ = primeSender.Close()

	senders := connectSenders(t, cluster)
	defer closeClients(senders)
	messages := repeatMessages(primeMessages, measuredRounds)
	measuredRecipients := multiplyRecipientExpectations(recipients, measuredRounds)

	starts := &sync.Map{}
	receiverResults := startReceivers(measuredRecipients, starts)
	sampler := newPressureSampler(cluster, metricSampleInterval)
	sampler.start()
	profileDone := startHotPathCPUProfiles(cluster, os.Getenv("WK_E2E_MEDIUM_RECIPIENT_PROFILE_DIR"))

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
		Messages:                 len(messages),
		RecipientRows:            mediumRecipientRows * measuredRounds,
		OnlineRoutes:             measuredOnlineRoutes,
		Connections:              len(recipients) + len(senders),
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
		MaxAdvancePoolUtil:       pressure.maxAdvancePoolUtil,
		MaxAdvancePoolWaiting:    pressure.maxAdvancePoolWaiting,
		MaxAppendPoolUtil:        pressure.maxAppendPoolUtil,
		MaxPostCommitPoolUtil:    pressure.maxPostCommitPoolUtil,
		MaxPostCommitBacklog:     pressure.maxPostCommitBacklog,
		MaxPostCommitHandoffRate: pressure.maxPostCommitHandoffRatio,
		MaxHeapBytes:             pressure.maxHeapBytes,
		AllocatedBytes:           counterDelta.allocatedBytes,
		GCCountDelta:             counterDelta.gcCount,
		PluginReceiveAccepted:    counterDelta.pluginReceiveAccepted,
		PluginReceiveFull:        counterDelta.pluginReceiveFull,
		PluginReceiveClosed:      counterDelta.pluginReceiveClosed,
		PluginReceiveInvokeOK:    counterDelta.pluginReceiveInvokeOK,
		PluginReceiveInvokeError: counterDelta.pluginReceiveInvokeError,
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
		requireHotPathAcceptance(t, evidence, expectedAcceptanceQPS)
	}
}

func requireHotPathAcceptance(t *testing.T, evidence hotPathEvidence, expectedOfferedQPS int) {
	t.Helper()
	if err := hotPathAcceptanceError(evidence, expectedOfferedQPS); err != nil {
		t.Fatal(err)
	}
}

func hotPathAcceptanceError(evidence hotPathEvidence, expectedOfferedQPS int) error {
	switch {
	case evidence.Schema != mediumEvidenceSchema:
		return fmt.Errorf("acceptance schema = %q, want %q", evidence.Schema, mediumEvidenceSchema)
	case evidence.PhysicalHashSlots != mediumPhysicalHashSlots:
		return fmt.Errorf("acceptance physical hash slots = %d, want %d", evidence.PhysicalHashSlots, mediumPhysicalHashSlots)
	case evidence.LogicalSlots != mediumLogicalSlots:
		return fmt.Errorf("acceptance logical slots = %d, want %d", evidence.LogicalSlots, mediumLogicalSlots)
	case evidence.Replicas != mediumReplicaCount:
		return fmt.Errorf("acceptance replicas = %d, want %d", evidence.Replicas, mediumReplicaCount)
	case evidence.Messages != mediumMessageCount*mediumMeasuredRounds:
		return fmt.Errorf("acceptance messages = %d, want %d", evidence.Messages, mediumMessageCount*mediumMeasuredRounds)
	case evidence.RecipientRows != mediumRecipientRows*mediumMeasuredRounds:
		return fmt.Errorf("acceptance recipient rows = %d, want %d", evidence.RecipientRows, mediumRecipientRows*mediumMeasuredRounds)
	case evidence.OnlineRoutes != expectedMeasuredOnlineRoutes():
		return fmt.Errorf("acceptance online routes = %d, want %d", evidence.OnlineRoutes, expectedMeasuredOnlineRoutes())
	case evidence.Connections != expectedConnectionCount():
		return fmt.Errorf("acceptance connections = %d, want %d", evidence.Connections, expectedConnectionCount())
	case evidence.OfferedQPS != expectedOfferedQPS:
		return fmt.Errorf("acceptance offered QPS = %d, want %d", evidence.OfferedQPS, expectedOfferedQPS)
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
			"acceptance actual Slot leaders = %v, want %d leaders in nodes [1,%d]",
			evidence.SlotLeaders,
			mediumLogicalSlots,
			mediumReplicaCount,
		)
	case evidence.IngressPerSecond < float64(expectedOfferedQPS)*mediumMinIngressFraction:
		return fmt.Errorf(
			"acceptance ingress = %.3f/s, want at least %.3f/s (%.1f%% of offered load)",
			evidence.IngressPerSecond,
			float64(expectedOfferedQPS)*mediumMinIngressFraction,
			mediumMinIngressFraction*100,
		)
	case evidence.SendackP99MS > 1_000:
		return fmt.Errorf("acceptance SENDACK P99 = %.3fms, want at most 1000ms", evidence.SendackP99MS)
	case evidence.RecvP99MS > 2_000:
		return fmt.Errorf("acceptance RECV P99 = %.3fms, want at most 2000ms", evidence.RecvP99MS)
	case evidence.MaxGatewayQueueRatio >= 1:
		return fmt.Errorf("acceptance gateway queue ratio = %.6f, want below 1", evidence.MaxGatewayQueueRatio)
	case evidence.MaxRecipientQueueRatio >= 1:
		return fmt.Errorf("acceptance recipient queue ratio = %.6f, want below 1", evidence.MaxRecipientQueueRatio)
	case evidence.MaxRecipientWorkerRatio >= 1:
		return fmt.Errorf("acceptance recipient worker ratio = %.6f, want below 1", evidence.MaxRecipientWorkerRatio)
	case evidence.PluginReceiveAccepted != float64(pluginReceiveBatchCount()*mediumMeasuredRounds):
		return fmt.Errorf(
			"acceptance plugin receive accepted = %.0f, want %d",
			evidence.PluginReceiveAccepted,
			pluginReceiveBatchCount()*mediumMeasuredRounds,
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
	case evidence.MeasuredDurationMS <= 0:
		return fmt.Errorf("acceptance measured duration = %.3fms, want a positive duration", evidence.MeasuredDurationMS)
	case evidence.AllocatedBytes <= 0:
		return fmt.Errorf("acceptance allocated bytes = %.0f, want a positive measured delta", evidence.AllocatedBytes)
	case evidence.AllocatedBytes > maxAcceptedAllocatedBytes(evidence):
		return fmt.Errorf(
			"acceptance allocated bytes/message = %.0f, want at most %.0f after %.3fs paced background allowance",
			evidence.AllocatedBytes/float64(evidence.Messages),
			maxAcceptedAllocatedBytes(evidence)/float64(evidence.Messages),
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
	case evidence.MaxHeapBytes <= 0 || evidence.MaxHeapBytes > mediumMaxHeapBytes:
		return fmt.Errorf(
			"acceptance max heap bytes = %.0f, want in (0,%d]",
			evidence.MaxHeapBytes,
			mediumMaxHeapBytes,
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
	return float64(evidence.Messages)*mediumMaxAllocatedBytesPerMessage +
		acceptedBackgroundDurationSeconds(evidence)*mediumMaxBackgroundAllocatedBytesPerSecond
}

func acceptedBackgroundDurationSeconds(evidence hotPathEvidence) float64 {
	return float64(evidence.Messages) / float64(evidence.OfferedQPS)
}

func expectedMeasuredOnlineRoutes() int {
	groupMessages := 0
	for _, profile := range mediumGroupProfiles {
		groupMessages += profile.messages
	}
	return mediumOnlineRoutes*mediumMeasuredRounds +
		groupMessages*(mediumGroupSenders-1)*mediumMeasuredRounds
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

func startHotPathCPUProfiles(cluster *suite.StartedCluster, outputDir string) <-chan error {
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
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+node.APIAddr()+"/debug/pprof/profile?seconds=2", nil)
				if err != nil {
					errs <- err
					return
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					errs <- fmt.Errorf("node %d profile request: %w", node.Spec.ID, err)
					return
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					errs <- fmt.Errorf("node %d profile status = %d", node.Spec.ID, resp.StatusCode)
					return
				}
				data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
				if err != nil {
					errs <- fmt.Errorf("node %d read profile: %w", node.Spec.ID, err)
					return
				}
				path := filepath.Join(outputDir, fmt.Sprintf("node-%d-cpu.pb.gz", node.Spec.ID))
				if err := os.WriteFile(path, data, 0o600); err != nil {
					errs <- fmt.Errorf("node %d write profile: %w", node.Spec.ID, err)
					return
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
	cluster *suite.StartedCluster,
	sender *suite.WKProtoClient,
	recipients []hotPathRecipient,
	messages []hotPathMessage,
	payload []byte,
) time.Duration {
	t.Helper()
	startedAt := time.Now()
	starts := &sync.Map{}
	receiverResults := startReceivers(recipients, starts)
	for index, message := range messages {
		clientMsgNo := fmt.Sprintf("wkrc-hifi-prime-%03d", index+1)
		starts.Store(clientMsgNo, time.Now())
		if err := sender.SendFrame(&frame.SendPacket{
			ChannelID:   message.channelID,
			ChannelType: message.channelType,
			ClientSeq:   uint64(2_000_000 + index),
			ClientMsgNo: clientMsgNo,
			Payload:     payload,
		}); err != nil {
			t.Fatalf("prime send %s: %v\n%s", clientMsgNo, err, cluster.DumpDiagnostics())
		}
	}
	for index := range messages {
		sendack, err := sender.ReadSendAck()
		if err != nil {
			t.Fatalf(
				"read prime SENDACK index=%d/%d after %s metrics=%s goroutines=%s: %v\n%s",
				index,
				len(messages),
				time.Since(startedAt),
				hotPathRuntimeDiagnostics(cluster),
				hotPathGoroutineDiagnostics(cluster),
				err,
				cluster.DumpDiagnostics(),
			)
		}
		if sendack.ReasonCode != frame.ReasonSuccess {
			t.Fatalf("prime SENDACK %s reason=%v\n%s", sendack.ClientMsgNo, sendack.ReasonCode, cluster.DumpDiagnostics())
		}
	}
	for range recipients {
		result := <-receiverResults
		if result.err != nil {
			t.Fatalf("prime receive and RECVACK: %v\n%s", result.err, cluster.DumpDiagnostics())
		}
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

func startMediumCluster(t *testing.T) *suite.StartedCluster {
	t.Helper()
	overrides := map[string]string{
		"WK_CLUSTER_INITIAL_SLOT_COUNT":                              "10",
		"WK_CLUSTER_HASH_SLOT_COUNT":                                 "256",
		"WK_CLUSTER_SLOT_REPLICA_N":                                  "3",
		"WK_CLUSTER_CHANNEL_REPLICA_N":                               "3",
		"WK_CLUSTER_CHANNEL_REACTOR_COUNT":                           "4",
		"WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS":                    "8",
		"WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS":                     "8",
		"WK_CLUSTER_CHANNEL_RPC_WORKERS":                             "50",
		"WK_GATEWAY_GNET_MULTICORE":                                  "true",
		"WK_GATEWAY_GNET_NUM_EVENT_LOOP":                             "4",
		"WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS":                      "128",
		"WK_GATEWAY_RUNTIME_ASYNC_SEND_QUEUE_CAPACITY":               "131072",
		"WK_DEBUG_API_ENABLE":                                        "true",
		"WK_DELIVERY_ENABLE":                                         "true",
		"WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY":                   "320",
		"WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS":                   "750000",
		"WK_PLUGIN_ENABLE":                                           "true",
		"WK_CHANNEL_APPEND_ADVANCE_POOL_SIZE":                        "500",
		"WK_CHANNEL_APPEND_EFFECT_POOL_SIZE":                         "2000",
		"WK_CHANNEL_APPEND_RECIPIENT_AUTHORITY_DISPATCH_CONCURRENCY": "100",
	}
	s := suite.New(t)
	return s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithNodeConfigOverrides(1, overrides),
		suite.WithNodeConfigOverrides(2, overrides),
		suite.WithNodeConfigOverrides(3, overrides),
	)
}

func validMediumSlotLeaders(leaders []uint64) bool {
	if len(leaders) != mediumLogicalSlots {
		return false
	}
	for _, leaderID := range leaders {
		if leaderID == 0 || leaderID > uint64(mediumReplicaCount) {
			return false
		}
	}
	return true
}

func prepareGroupChannels(t *testing.T, ctx context.Context, node *suite.StartedNode) ([]string, int, []string) {
	t.Helper()
	onlineUIDs := make([]string, 100)
	for i := range onlineUIDs {
		onlineUIDs[i] = fmt.Sprintf("wkrc-group-online-%03d", i)
	}
	channels := make([]string, len(mediumGroupProfiles))
	recipientRows := 0
	for profileIndex, profile := range mediumGroupProfiles {
		channelID := fmt.Sprintf("wkrc-hifi-group-%d", profileIndex)
		channels[profileIndex] = channelID
		subscribers := make([]string, profile.recipients)
		copy(subscribers, onlineUIDs[:profile.online])
		for i := profile.online; i < len(subscribers); i++ {
			subscribers[i] = fmt.Sprintf("wkrc-group-%d-offline-%04d", profileIndex, i)
		}
		for senderIndex := 0; senderIndex < mediumGroupSenders; senderIndex++ {
			subscribers[len(subscribers)-1-senderIndex] = mediumSenderUID(senderIndex)
		}
		if err := suite.PostChannel(ctx, node.APIAddr(), map[string]any{
			"channel_id":   channelID,
			"channel_type": frame.ChannelTypeGroup,
			"subscribers":  subscribers,
		}); err != nil {
			t.Fatalf("prepare group channel %s: %v\n%s", channelID, err, node.DumpDiagnostics())
		}
		recipientRows += profile.messages * profile.recipients
	}
	return channels, recipientRows, onlineUIDs
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

func buildMessages(groupChannels, personUIDs []string) []hotPathMessage {
	messages := make([]hotPathMessage, 0, mediumMessageCount)
	nextSeq := uint64(1)
	for _, uid := range personUIDs {
		messages = append(messages, hotPathMessage{
			clientSeq:   nextSeq,
			clientMsgNo: fmt.Sprintf("wkrc-hifi-%03d", nextSeq),
			channelID:   uid,
			channelType: frame.ChannelTypePerson,
		})
		nextSeq++
	}
	for profileIndex, profile := range mediumGroupProfiles {
		for range profile.messages {
			messages = append(messages, hotPathMessage{
				clientSeq:   nextSeq,
				clientMsgNo: fmt.Sprintf("wkrc-hifi-%03d", nextSeq),
				channelID:   groupChannels[profileIndex],
				channelType: frame.ChannelTypeGroup,
			})
			nextSeq++
		}
	}
	return messages
}

func repeatMessages(base []hotPathMessage, rounds int) []hotPathMessage {
	messages := make([]hotPathMessage, 0, len(base)*rounds)
	nextSeq := uint64(1)
	for round := range rounds {
		for _, message := range base {
			message.clientSeq = nextSeq
			message.clientMsgNo = fmt.Sprintf("wkrc-hifi-round-%02d-%04d", round+1, nextSeq)
			messages = append(messages, message)
			nextSeq++
		}
	}
	return messages
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
	maxGatewayQueueRatio      float64
	maxRecipientQueueRatio    float64
	maxRecipientWorkerRatio   float64
	maxAdvancePoolUtil        float64
	maxAdvancePoolWaiting     float64
	maxAppendPoolUtil         float64
	maxPostCommitPoolUtil     float64
	maxPostCommitBacklog      float64
	maxPostCommitHandoffRatio float64
	maxHeapBytes              float64
	samples                   int
	sampleErrors              int
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
	for _, node := range s.cluster.Nodes {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		samples, err := suite.FetchMetricSamples(ctx, node.APIAddr())
		cancel()
		s.mu.Lock()
		if err != nil {
			s.state.sampleErrors++
			s.mu.Unlock()
			continue
		}
		s.state.samples++
		s.observeSamples(samples)
		s.mu.Unlock()
	}
}

func (s *pressureSampler) observeSamples(samples []suite.MetricSample) {
	values := metricValues(samples)
	s.state.maxGatewayQueueRatio = maxFloat(s.state.maxGatewayQueueRatio, ratio(values.gatewayQueueDepth, values.gatewayQueueCapacity))
	s.state.maxRecipientQueueRatio = maxFloat(s.state.maxRecipientQueueRatio, ratio(values.recipientQueueDepth, values.recipientQueueCapacity))
	s.state.maxRecipientWorkerRatio = maxFloat(s.state.maxRecipientWorkerRatio, ratio(values.recipientInflight, values.recipientCapacity))
	s.state.maxAdvancePoolUtil = maxFloat(s.state.maxAdvancePoolUtil, values.advanceUtil)
	s.state.maxAdvancePoolWaiting = maxFloat(s.state.maxAdvancePoolWaiting, values.advanceWaiting)
	s.state.maxAppendPoolUtil = maxFloat(s.state.maxAppendPoolUtil, values.appendUtil)
	s.state.maxPostCommitPoolUtil = maxFloat(s.state.maxPostCommitPoolUtil, values.postCommitUtil)
	s.state.maxPostCommitBacklog = maxFloat(s.state.maxPostCommitBacklog, values.postCommitBacklog)
	s.state.maxPostCommitHandoffRatio = maxFloat(s.state.maxPostCommitHandoffRatio, ratio(values.handoffDepth, values.handoffCapacity))
	s.state.maxHeapBytes = maxFloat(s.state.maxHeapBytes, values.heapBytes)
}

type hotPathMetricValues struct {
	gatewayQueueDepth      float64
	gatewayQueueCapacity   float64
	recipientQueueDepth    float64
	recipientQueueCapacity float64
	recipientInflight      float64
	recipientCapacity      float64
	advanceUtil            float64
	advanceWaiting         float64
	appendUtil             float64
	postCommitUtil         float64
	postCommitBacklog      float64
	handoffDepth           float64
	handoffCapacity        float64
	heapBytes              float64
}

func metricValues(samples []suite.MetricSample) hotPathMetricValues {
	var values hotPathMetricValues
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
		case "go_memstats_heap_alloc_bytes":
			values.heapBytes = sample.Value
		}
	}
	return values
}

type hotPathCounters struct {
	allocatedBytes           float64
	gcCount                  float64
	pluginReceiveAccepted    float64
	pluginReceiveFull        float64
	pluginReceiveClosed      float64
	pluginReceiveInvokeOK    float64
	pluginReceiveInvokeError float64
}

func (c hotPathCounters) subtract(start hotPathCounters) hotPathCounters {
	return hotPathCounters{
		allocatedBytes:           c.allocatedBytes - start.allocatedBytes,
		gcCount:                  c.gcCount - start.gcCount,
		pluginReceiveAccepted:    c.pluginReceiveAccepted - start.pluginReceiveAccepted,
		pluginReceiveFull:        c.pluginReceiveFull - start.pluginReceiveFull,
		pluginReceiveClosed:      c.pluginReceiveClosed - start.pluginReceiveClosed,
		pluginReceiveInvokeOK:    c.pluginReceiveInvokeOK - start.pluginReceiveInvokeOK,
		pluginReceiveInvokeError: c.pluginReceiveInvokeError - start.pluginReceiveInvokeError,
	}
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
			switch sample.Name {
			case "go_memstats_alloc_bytes_total":
				counters.allocatedBytes += sample.Value
			case "go_gc_duration_seconds_count":
				counters.gcCount += sample.Value
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
			}
		}
	}
	return counters, nil
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
			if strings.HasPrefix(sample.Name, "wukongim_runtime_pool_") && sample.Labels["component"] != "gateway" {
				continue
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
		"wukongim_conversation_authority_",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func hotPathGoroutineDiagnostics(cluster *suite.StartedCluster) string {
	const maxProfileBytes = 2 << 20
	const maxOutputBytes = 96 << 10
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
			if !isHotPathGoroutineBlock(block) {
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

func isHotPathGoroutineBlock(block string) bool {
	for _, match := range []string{
		"internal/usecase/message",
		"internal/access/gateway",
		"internal/runtime/channelappend",
		"internal/infra/cluster",
		"pkg/slot/proxy",
		"pkg/gateway/core",
	} {
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
