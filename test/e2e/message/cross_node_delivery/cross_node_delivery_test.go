//go:build e2e

package cross_node_delivery

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/internal/bench/planner"
	"github.com/WuKongIM/WuKongIM/internal/bench/worker"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestThreeNodeClusterCrossNodeUsersExchangeMessages(t *testing.T) {
	s := suite.New(t)
	overrides := replicaOverrides(3)
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithNodeConfigOverrides(1, overrides),
		suite.WithNodeConfigOverrides(2, overrides),
		suite.WithNodeConfigOverrides(3, overrides),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	convergence, err := cluster.WaitSlotLeadersStable(ctx, 2*time.Second)
	require.NoError(t, err, cluster.DumpDiagnostics())
	t.Logf(
		"actual Slot leaders stable for %s after %s: %v",
		convergence.StableDuration,
		convergence.WaitDuration,
		convergence.Leaders,
	)

	userA := newConnectedClient(t, cluster.MustNode(1), "e2e-cross-a")
	defer func() { _ = userA.Close() }()
	userB := newConnectedClient(t, cluster.MustNode(2), "e2e-cross-b")
	defer func() { _ = userB.Close() }()

	sendAndRequireRecv(t, cluster, cluster.MustNode(2), userA, userB, "e2e-cross-a", "e2e-cross-b", 1, "e2e-cross-a-to-b-1", []byte("hello b from a"))
	sendAndRequireRecv(t, cluster, cluster.MustNode(1), userB, userA, "e2e-cross-b", "e2e-cross-a", 1, "e2e-cross-b-to-a-1", []byte("hello a from b"))

	channelID := channelid.EncodePersonChannel("e2e-cross-a", "e2e-cross-b")
	requireChannelReplicaCountEventually(t, cluster, channelID, frame.ChannelTypePerson, 3)
}

func TestThreeNodeReplicaTwoClusterConvergesSlotTopology(t *testing.T) {
	s := suite.New(t)
	overrides := replicaTwoOverrides()
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithNodeConfigOverrides(1, overrides),
		suite.WithNodeConfigOverrides(2, overrides),
		suite.WithNodeConfigOverrides(3, overrides),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	slots := cluster.ManagerClient(t, 1).MustSlots(t)
	require.Len(t, slots, 12, cluster.DumpDiagnostics())
	hashSlots := make(map[uint16]struct{}, 256)
	for _, slot := range slots {
		require.Len(t, slot.Assignment.DesiredPeers, 2, "slot=%d\n%s", slot.SlotID, cluster.DumpDiagnostics())
		require.Len(t, slot.Runtime.CurrentVoters, 2, "slot=%d\n%s", slot.SlotID, cluster.DumpDiagnostics())
		require.True(t, slot.Runtime.HasQuorum, "slot=%d\n%s", slot.SlotID, cluster.DumpDiagnostics())
		require.NotNil(t, slot.HashSlots, "slot=%d\n%s", slot.SlotID, cluster.DumpDiagnostics())
		for _, hashSlot := range slot.HashSlots.Items {
			hashSlots[hashSlot] = struct{}{}
		}
	}
	require.Len(t, hashSlots, 256, cluster.DumpDiagnostics())
}

func TestThreeNodeReplicaThreeClusterConvergesSlotTopology(t *testing.T) {
	s := suite.New(t)
	overrides := replicaOverrides(3)
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithNodeConfigOverrides(1, overrides),
		suite.WithNodeConfigOverrides(2, overrides),
		suite.WithNodeConfigOverrides(3, overrides),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	slots := cluster.ManagerClient(t, 1).MustSlots(t)
	require.Len(t, slots, 12, cluster.DumpDiagnostics())
	hashSlots := make(map[uint16]struct{}, 256)
	for _, slot := range slots {
		require.Len(t, slot.Assignment.DesiredPeers, 3, "slot=%d\n%s", slot.SlotID, cluster.DumpDiagnostics())
		require.Len(t, slot.Runtime.CurrentVoters, 3, "slot=%d\n%s", slot.SlotID, cluster.DumpDiagnostics())
		require.True(t, slot.Runtime.HasQuorum, "slot=%d\n%s", slot.SlotID, cluster.DumpDiagnostics())
		require.NotNil(t, slot.HashSlots, "slot=%d\n%s", slot.SlotID, cluster.DumpDiagnostics())
		for _, hashSlot := range slot.HashSlots.Items {
			hashSlots[hashSlot] = struct{}{}
		}
	}
	require.Len(t, hashSlots, 256, cluster.DumpDiagnostics())
}

func TestThreeNodeReplicaTwoClusterCrossNodeMessagesUseTwoReplicaChannel(t *testing.T) {
	s := suite.New(t)
	overrides := replicaTwoOverrides()
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithNodeConfigOverrides(1, overrides),
		suite.WithNodeConfigOverrides(2, overrides),
		suite.WithNodeConfigOverrides(3, overrides),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	_, err := cluster.WaitSlotLeadersStable(ctx, 2*time.Second)
	require.NoError(t, err, cluster.DumpDiagnostics())

	const userAUID = "e2e-replica-two-a"
	const userBUID = "e2e-replica-two-b"
	userA := newConnectedClient(t, cluster.MustNode(1), userAUID)
	defer func() { _ = userA.Close() }()
	userB := newConnectedClient(t, cluster.MustNode(2), userBUID)
	defer func() { _ = userB.Close() }()

	sendAndRequireRecv(t, cluster, cluster.MustNode(2), userA, userB, userAUID, userBUID, 1, "e2e-replica-two-a-to-b", []byte("hello b from replica-two a"))
	sendAndRequireRecv(t, cluster, cluster.MustNode(1), userB, userA, userBUID, userAUID, 1, "e2e-replica-two-b-to-a", []byte("hello a from replica-two b"))

	channelID := channelid.EncodePersonChannel(userAUID, userBUID)
	requireChannelReplicaCountEventually(t, cluster, channelID, frame.ChannelTypePerson, 2)
}

func TestThreeNodeReplicaLoadComparisonAtTwoThousandQPS(t *testing.T) {
	if os.Getenv("WK_E2E_REPLICA_LOAD_COMPARISON") != "1" {
		t.Skip("set WK_E2E_REPLICA_LOAD_COMPARISON=1 to compare the bounded 2/2 and 3/3 2,000 QPS profiles")
	}

	testCases := []struct {
		round        int
		replicaCount int
	}{
		{round: 1, replicaCount: 2},
		{round: 1, replicaCount: 3},
		{round: 2, replicaCount: 3},
		{round: 2, replicaCount: 2},
		{round: 3, replicaCount: 2},
		{round: 3, replicaCount: 3},
	}
	results := make(map[int][]replicaLoadResult, 2)
	for _, testCase := range testCases {
		testCase := testCase
		t.Run(fmt.Sprintf("round_%d_replicas_%d", testCase.round, testCase.replicaCount), func(t *testing.T) {
			result := runReplicaLoad(t, testCase.replicaCount, testCase.round)
			results[testCase.replicaCount] = append(results[testCase.replicaCount], result)
			t.Logf(
				"round=%d replicas=%d local load: success=%d achieved=%.1f msg/s recv=%d send_errors=%d recv_errors=%d sendack_p50=%s sendack_p99=%s rss_before=%.1fMiB rss_after=%.1fMiB elapsed=%s",
				testCase.round,
				testCase.replicaCount,
				result.SendSuccess,
				float64(result.SendSuccess)/result.Duration.Seconds(),
				result.RecvSuccess,
				result.SendErrors,
				result.RecvErrors,
				result.SendACKP50,
				result.SendACKP99,
				bytesToMiB(result.RSSBeforeRun),
				bytesToMiB(result.RSSAfterCooldown),
				result.RunElapsed,
			)
		})
	}
	if len(results[2]) != 3 || len(results[3]) != 3 {
		return
	}

	replicaTwo := summarizeReplicaLoads(results[2])
	replicaThree := summarizeReplicaLoads(results[3])
	for _, summary := range []replicaLoadSummary{replicaTwo, replicaThree} {
		t.Logf(
			"replicas=%d summary: p50_samples=%v p50_median=%s p99_samples=%v p99_mean=%s p99_median=%s rss_after_samples_mib=%v rss_after_mean=%.1fMiB rss_delta_mean=%.1fMiB",
			summary.ReplicaCount,
			summary.P50Samples,
			summary.P50Median,
			summary.P99Samples,
			summary.P99Mean,
			summary.P99Median,
			summary.RSSAfterSamplesMiB,
			summary.RSSAfterMeanMiB,
			summary.RSSDeltaMeanMiB,
		)
	}
	t.Logf(
		"same-host A/B median: replica-two p99=%s, replica-three p99=%s, delta=%s, ratio=%.2fx",
		replicaTwo.P99Median,
		replicaThree.P99Median,
		replicaThree.P99Median-replicaTwo.P99Median,
		float64(replicaThree.P99Median)/float64(replicaTwo.P99Median),
	)
}

type replicaLoadResult struct {
	ReplicaCount     int
	Duration         time.Duration
	RunElapsed       time.Duration
	SendSuccess      uint64
	RecvSuccess      uint64
	SendErrors       uint64
	RecvErrors       uint64
	SendACKP50       time.Duration
	SendACKP99       time.Duration
	RSSBeforeRun     uint64
	RSSAfterCooldown uint64
}

type replicaLoadSummary struct {
	ReplicaCount       int
	P50Samples         []time.Duration
	P99Samples         []time.Duration
	P50Median          time.Duration
	P99Mean            time.Duration
	P99Median          time.Duration
	RSSAfterSamplesMiB []float64
	RSSAfterMeanMiB    float64
	RSSDeltaMeanMiB    float64
}

func runReplicaLoad(t *testing.T, replicaCount, round int) replicaLoadResult {
	t.Helper()

	s := suite.New(t)
	overrides := replicaOverrides(replicaCount)
	overrides["WK_BENCH_API_ENABLE"] = "true"
	overrides["WK_BENCH_API_MAX_BATCH_SIZE"] = "10000"
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		suite.WithNodeConfigOverrides(1, overrides),
		suite.WithNodeConfigOverrides(2, overrides),
		suite.WithNodeConfigOverrides(3, overrides),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	_, err := cluster.WaitSlotLeadersStable(ctx, 2*time.Second)
	require.NoError(t, err, cluster.DumpDiagnostics())

	scenario := replicaLoadScenario(replicaCount, round)
	workerConfig := model.Worker{ID: fmt.Sprintf("round-%d-replica-%d-worker", round, replicaCount), Weight: 1}
	plan, err := planner.Build(scenario, []model.Worker{workerConfig})
	require.NoError(t, err)
	assignment := worker.Assignment{
		RunID: scenario.Run.ID, WorkerID: workerConfig.ID,
		Target: model.Target{
			API: model.TargetAPIConfig{Addrs: []string{
				"http://" + cluster.MustNode(1).APIAddr(),
				"http://" + cluster.MustNode(2).APIAddr(),
				"http://" + cluster.MustNode(3).APIAddr(),
			}},
			BenchAPI: model.BenchAPIConfig{Enabled: true, Addrs: []string{
				"http://" + cluster.MustNode(1).APIAddr(),
				"http://" + cluster.MustNode(2).APIAddr(),
				"http://" + cluster.MustNode(3).APIAddr(),
			}},
			Gateway: model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: []string{
				cluster.MustNode(1).GatewayAddr(),
				cluster.MustNode(2).GatewayAddr(),
				cluster.MustNode(3).GatewayAddr(),
			}}},
		},
		Scenario: scenario,
		Plan:     plan.Workers[workerConfig.ID],
	}

	runner := worker.NewDefaultWorkloadRunner(nil)
	runner.(worker.AssignmentStarter).BeginAssignment(assignment)
	defer func() { require.NoError(t, runner.(worker.AssignmentStopper).EndAssignment(assignment)) }()
	require.NoError(t, runner.Prepare(ctx, assignment))
	require.NoError(t, runner.Connect(ctx, assignment))
	require.NoError(t, runner.Warmup(ctx, assignment))
	rssBeforeRun := clusterRSSBytes(t, cluster)
	runStarted := time.Now()
	require.NoError(t, runner.Run(ctx, assignment))
	runElapsed := time.Since(runStarted)
	require.NoError(t, runner.Cooldown(ctx, assignment))
	rssAfterCooldown := clusterRSSBytes(t, cluster)

	snapshot := runner.(worker.MetricsReporter).MetricsSnapshot()
	success := counterSum(snapshot, "person_send_success_total", "run")
	recvSuccess := counterSum(snapshot, "person_recv_success_total", "run")
	sendErrors := counterSum(snapshot, "person_send_error_total", "run")
	recvErrors := counterSum(snapshot, "recv_verify_error_total", "run")
	p50 := maximumP50(snapshot, "person_send_latency_seconds", "run")
	p99 := maximumP99(snapshot, "person_send_latency_seconds", "run")
	minimumSuccess := uint64(1900 * scenario.Run.Duration.Seconds())
	require.GreaterOrEqual(t, success, minimumSuccess, "elapsed=%s p99=%s metrics=%+v\n%s", runElapsed, p99, snapshot, cluster.DumpDiagnostics())
	require.GreaterOrEqual(t, recvSuccess, minimumSuccess, "metrics=%+v\n%s", snapshot, cluster.DumpDiagnostics())
	require.Zero(t, sendErrors, "metrics=%+v\n%s", snapshot, cluster.DumpDiagnostics())
	require.Zero(t, recvErrors, "metrics=%+v\n%s", snapshot, cluster.DumpDiagnostics())
	require.Positive(t, p50, "metrics=%+v\n%s", snapshot, cluster.DumpDiagnostics())
	require.Positive(t, p99, "metrics=%+v\n%s", snapshot, cluster.DumpDiagnostics())
	require.LessOrEqual(t, p99, 400*time.Millisecond, "metrics=%+v\n%s", snapshot, cluster.DumpDiagnostics())
	return replicaLoadResult{
		ReplicaCount:     replicaCount,
		Duration:         scenario.Run.Duration,
		RunElapsed:       runElapsed,
		SendSuccess:      success,
		RecvSuccess:      recvSuccess,
		SendErrors:       sendErrors,
		RecvErrors:       recvErrors,
		SendACKP50:       p50,
		SendACKP99:       p99,
		RSSBeforeRun:     rssBeforeRun,
		RSSAfterCooldown: rssAfterCooldown,
	}
}

func replicaLoadScenario(replicaCount, round int) model.Scenario {
	prefix := fmt.Sprintf("round-%d-replica-%d", round, replicaCount)
	return model.Scenario{
		Version: "wkbench/v1",
		Run: model.RunConfig{
			ID: prefix + "-2000qps", Duration: 10 * time.Second, Warmup: 5 * time.Second, Cooldown: 10 * time.Second, RandomSeed: 20260826, FailFast: true,
		},
		Prepare: model.PrepareConfig{
			Concurrency: 128, RateLimit: model.Rate{PerSecond: 2000}, Retry: model.RetryConfig{MaxAttempts: 3, Backoff: 100 * time.Millisecond},
		},
		Identity: model.IdentityConfig{
			TotalUsers: 1000, UIDPrefix: prefix + "-u", DevicePrefix: prefix + "-d", ClientMsgPrefix: prefix + "-msg",
			Token: model.TokenConfig{Mode: "bench_api"},
		},
		Online: model.OnlineConfig{
			TotalUsers: 1000, ConnectRate: model.Rate{PerSecond: 1000}, GatewayBalance: "round_robin",
		},
		Channels: model.ChannelsConfig{Profiles: []model.ChannelProfile{{
			Name: "person-load", ChannelType: model.ChannelTypePerson, Count: 500,
			Participants: model.ParticipantsConfig{Pick: "round_robin"},
			Online:       model.ChannelOnlineConfig{SenderRatio: 1, RecipientRatio: 1},
			Shard:        model.ShardConfig{Mode: "hash"},
			Prepare:      model.ChannelPrepareConfig{SubscribersBatchSize: 1000},
		}}},
		Messages: model.MessagesConfig{
			Payload: model.PayloadConfig{SizeBytes: 256, Mode: "deterministic"},
			Traffic: []model.TrafficConfig{{
				Name: "person-load-send", ChannelRef: "person-load", RatePerChannel: model.Rate{PerSecond: 4},
				Concurrency: 512, AckTimeout: 5 * time.Second, RecvTimeout: 5 * time.Second,
				SenderPick: "round_robin", RecvAck: true,
				Verify: model.VerifyConfig{Recv: model.RecvVerifyConfig{Mode: "full"}},
			}},
		},
	}
}

func counterSum(snapshot metrics.SnapshotData, metricName, phase string) uint64 {
	var total uint64
	prefix := metricName + "{"
	phaseLabel := "phase=" + phase
	for key, value := range snapshot.Counters {
		if strings.HasPrefix(key, prefix) && strings.Contains(key, phaseLabel) {
			total += value
		}
	}
	return total
}

func maximumP99(snapshot metrics.SnapshotData, metricName, phase string) time.Duration {
	var maximum float64
	prefix := metricName + "{"
	phaseLabel := "phase=" + phase
	for key, value := range snapshot.Histograms {
		if strings.HasPrefix(key, prefix) && strings.Contains(key, phaseLabel) && value.P99Seconds > maximum {
			maximum = value.P99Seconds
		}
	}
	return time.Duration(maximum * float64(time.Second))
}

func maximumP50(snapshot metrics.SnapshotData, metricName, phase string) time.Duration {
	var maximum float64
	prefix := metricName + "{"
	phaseLabel := "phase=" + phase
	for key, value := range snapshot.Histograms {
		if strings.HasPrefix(key, prefix) && strings.Contains(key, phaseLabel) && value.P50Seconds > maximum {
			maximum = value.P50Seconds
		}
	}
	return time.Duration(maximum * float64(time.Second))
}

func clusterRSSBytes(t *testing.T, cluster *suite.StartedCluster) uint64 {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var total uint64
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		value, err := suite.FetchMetricValue(ctx, cluster.MustNode(nodeID).APIAddr(), "wukongim_node_memory_rss_bytes", nil)
		require.NoError(t, err, cluster.DumpDiagnostics())
		require.Positive(t, value, cluster.DumpDiagnostics())
		total += uint64(value)
	}
	return total
}

func summarizeReplicaLoads(results []replicaLoadResult) replicaLoadSummary {
	p50Samples := make([]time.Duration, 0, len(results))
	p99Samples := make([]time.Duration, 0, len(results))
	rssAfterSamplesMiB := make([]float64, 0, len(results))
	var p99Total time.Duration
	var rssAfterTotalMiB float64
	var rssDeltaTotalMiB float64
	for _, result := range results {
		p50Samples = append(p50Samples, result.SendACKP50)
		p99Samples = append(p99Samples, result.SendACKP99)
		p99Total += result.SendACKP99
		rssAfterMiB := bytesToMiB(result.RSSAfterCooldown)
		rssAfterSamplesMiB = append(rssAfterSamplesMiB, rssAfterMiB)
		rssAfterTotalMiB += rssAfterMiB
		rssDeltaTotalMiB += rssAfterMiB - bytesToMiB(result.RSSBeforeRun)
	}
	slices.Sort(p50Samples)
	slices.Sort(p99Samples)
	return replicaLoadSummary{
		ReplicaCount:       results[0].ReplicaCount,
		P50Samples:         p50Samples,
		P99Samples:         p99Samples,
		P50Median:          p50Samples[len(p50Samples)/2],
		P99Mean:            p99Total / time.Duration(len(results)),
		P99Median:          p99Samples[len(p99Samples)/2],
		RSSAfterSamplesMiB: rssAfterSamplesMiB,
		RSSAfterMeanMiB:    rssAfterTotalMiB / float64(len(results)),
		RSSDeltaMeanMiB:    rssDeltaTotalMiB / float64(len(results)),
	}
}

func bytesToMiB(value uint64) float64 {
	return float64(value) / (1 << 20)
}

func replicaTwoOverrides() map[string]string {
	return replicaOverrides(2)
}

func replicaOverrides(replicaCount int) map[string]string {
	overrides := deliveryTopOverrides()
	overrides["WK_CLUSTER_INITIAL_SLOT_COUNT"] = "12"
	overrides["WK_CLUSTER_HASH_SLOT_COUNT"] = "256"
	overrides["WK_CLUSTER_SLOT_REPLICA_N"] = fmt.Sprintf("%d", replicaCount)
	overrides["WK_CLUSTER_CHANNEL_REPLICA_N"] = fmt.Sprintf("%d", replicaCount)
	return overrides
}

type channelRuntimeMetaItem = suite.ChannelRuntimeMeta

func requireChannelReplicaCountEventually(t *testing.T, cluster *suite.StartedCluster, channelID string, channelType uint8, replicaCount int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var last channelRuntimeMetaItem
	var lastErr error
	for {
		for nodeID := uint64(1); nodeID <= 3; nodeID++ {
			meta, err := channelRuntimeMeta(ctx, cluster.MustNode(nodeID), channelID, channelType)
			if err != nil {
				lastErr = err
				continue
			}
			last = meta
			if meta.Leader != 0 && meta.Status == "active" && len(meta.Replicas) == replicaCount && len(meta.ISR) == replicaCount {
				return
			}
			lastErr = fmt.Errorf("runtime meta replicas=%v isr=%v leader=%d status=%q, want %d active replicas", meta.Replicas, meta.ISR, meta.Leader, meta.Status, replicaCount)
		}

		select {
		case <-ctx.Done():
			t.Fatalf("channel runtime meta did not converge: last=%+v lastErr=%v\n%s", last, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func channelRuntimeMeta(ctx context.Context, node *suite.StartedNode, channelID string, channelType uint8) (channelRuntimeMetaItem, error) {
	return suite.GetChannelRuntimeMeta(ctx, node, channelID, channelType)
}

func deliveryTopOverrides() map[string]string {
	return map[string]string{
		"WK_DELIVERY_ENABLE":      "true",
		"WK_TOP_API_ENABLE":       "true",
		"WK_TOP_COLLECT_INTERVAL": "100ms",
		"WK_TOP_HISTORY_WINDOW":   "2s",
	}
}

func newConnectedClient(t *testing.T, node *suite.StartedNode, uid string) *suite.WKProtoClient {
	t.Helper()

	client, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	require.NoError(t, client.Connect(node.GatewayAddr(), uid, uid+"-device"), node.DumpDiagnostics())
	return client
}

func sendAndRequireRecv(
	t *testing.T,
	cluster *suite.StartedCluster,
	recipientOwner *suite.StartedNode,
	sender, recipient *suite.WKProtoClient,
	senderUID, recipientUID string,
	clientSeq uint64,
	clientMsgNo string,
	payload []byte,
) {
	t.Helper()

	require.NoError(t, sender.SendFrame(&frame.SendPacket{
		ChannelID:   recipientUID,
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		Payload:     payload,
	}), cluster.DumpDiagnostics())

	sendack, err := sender.ReadSendAck()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode, cluster.DumpDiagnostics())
	require.Equal(t, clientSeq, sendack.ClientSeq)
	require.Equal(t, clientMsgNo, sendack.ClientMsgNo)
	require.NotZero(t, sendack.MessageID)
	require.NotZero(t, sendack.MessageSeq)

	recv, err := recipient.ReadRecv()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, senderUID, recv.FromUID)
	require.Equal(t, senderUID, recv.ChannelID)
	require.Equal(t, frame.ChannelTypePerson, recv.ChannelType)
	require.Equal(t, payload, recv.Payload)
	require.Equal(t, sendack.MessageID, recv.MessageID)
	require.Equal(t, sendack.MessageSeq, recv.MessageSeq)
	fmt.Printf("recipient received message: %+v\n", recv)

	suite.RequireTopDeliveryAckBindingsAtLeastEventually(t, *recipientOwner, 1)
	require.NoError(t, recipient.RecvAck(recv.MessageID, recv.MessageSeq), cluster.DumpDiagnostics())
	suite.RequireTopDeliveryAckBindingsEventually(t, *recipientOwner, 0)
}
