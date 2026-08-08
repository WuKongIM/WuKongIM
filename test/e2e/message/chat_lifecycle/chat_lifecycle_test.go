//go:build e2e

package chat_lifecycle

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	benchlifecycle "github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const (
	benchToken       = "chat-lifecycle-e2e-bench-token"
	operationTimeout = 20 * time.Second
	idleEvictAfter   = 5 * time.Minute
	idlePollDeadline = 6*time.Minute + 15*time.Second
)

func TestPersonChannelNaturalReheat(t *testing.T) {
	cluster := startLifecycleCluster(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute+30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	_, err := cluster.WaitSlotLeadersStable(ctx, 2*time.Second)
	require.NoError(t, err, cluster.DumpDiagnostics())

	api := lifecycleTarget(cluster)
	baseline := requireCreatedTotal(t, ctx, cluster)

	const (
		leftUID  = "e2e-lifecycle-left"
		rightUID = "e2e-lifecycle-right"
	)
	canonicalChannel := channelid.EncodePersonChannel(leftUID, rightUID)
	probeRequest := model.ChannelRuntimeProbeRequest{Channels: []model.ChannelRuntimeChannelIdentity{{
		ChannelID: canonicalChannel, ChannelType: uint8(frame.ChannelTypePerson),
	}}}

	left := connectAndFullSync(t, ctx, cluster, api, 1, leftUID)
	right := connectAndFullSync(t, ctx, cluster, api, 2, rightUID)
	first := sendPersonMessage(t, cluster, left, right, rightUID, 1, "e2e-lifecycle-first")

	initial := requireLoadedOnAllNodes(t, ctx, cluster, api, probeRequest)
	require.Equal(t, baseline+1, requireCreatedTotalEventually(t, ctx, cluster, baseline+1))
	require.NoError(t, left.Close())
	require.NoError(t, right.Close())

	quietStarted := time.Now()
	requireMissingAfterNaturalIdle(t, ctx, cluster, api, probeRequest, quietStarted)

	left = connectAndFullSyncContaining(t, ctx, cluster, api, 1, leftUID, "e2e-lifecycle-first")
	defer func() { _ = left.Close() }()
	right = connectAndFullSyncContaining(t, ctx, cluster, api, 2, rightUID, "e2e-lifecycle-first")
	defer func() { _ = right.Close() }()
	second := sendPersonMessage(t, cluster, left, right, rightUID, 2, "e2e-lifecycle-second")
	require.Greater(t, second, first, cluster.DumpDiagnostics())

	reheated := requireLoadedOnAllNodes(t, ctx, cluster, api, probeRequest)
	requireMonotonicRuntimeEvidence(t, initial, reheated)
	require.Equal(t, baseline+1, requireCreatedTotalEventually(t, ctx, cluster, baseline+1),
		"reheat must reuse replicated Channel metadata")
}

func TestPersonChannelCrossIngressBurstPreservesReceiveSequence(t *testing.T) {
	cluster := startLifecycleCluster(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	_, err := cluster.WaitSlotLeadersStable(ctx, 2*time.Second)
	require.NoError(t, err, cluster.DumpDiagnostics())

	api := lifecycleTarget(cluster)
	const (
		leftUID        = "e2e-sequence-left"
		rightUID       = "e2e-sequence-right"
		messagesPerWay = 100
		rounds         = 6
	)
	left := connectAndFullSync(t, ctx, cluster, api, 1, leftUID)
	defer func() { _ = left.Close() }()
	right := connectAndFullSync(t, ctx, cluster, api, 2, rightUID)
	defer func() { _ = right.Close() }()

	var leftLast, rightLast uint64
	for round := 0; round < rounds; round++ {
		start := round*messagesPerWay + 1
		var sends sync.WaitGroup
		sendErrs := make(chan error, 2)
		sends.Add(2)
		go func() {
			defer sends.Done()
			sendErrs <- sendPersonBurst(left, rightUID, "left", start, messagesPerWay)
		}()
		go func() {
			defer sends.Done()
			sendErrs <- sendPersonBurst(right, leftUID, "right", start, messagesPerWay)
		}()
		sends.Wait()
		close(sendErrs)
		for sendErr := range sendErrs {
			require.NoError(t, sendErr, cluster.DumpDiagnostics())
		}

		requireSuccessfulSendacks(t, cluster, left, messagesPerWay)
		requireSuccessfulSendacks(t, cluster, right, messagesPerWay)
		leftLast = requireMonotonicPersonReceives(t, cluster, left, rightUID, messagesPerWay, leftLast)
		rightLast = requireMonotonicPersonReceives(t, cluster, right, leftUID, messagesPerWay, rightLast)
	}
}

func TestGroupChannelCrossIngressBurstPreservesReceiveSequence(t *testing.T) {
	cluster := startLifecycleCluster(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	_, err := cluster.WaitSlotLeadersStable(ctx, 2*time.Second)
	require.NoError(t, err, cluster.DumpDiagnostics())

	const (
		channelID      = "e2e-sequence-group"
		leftUID        = "e2e-sequence-group-left"
		rightUID       = "e2e-sequence-group-right"
		recipientUID   = "e2e-sequence-group-recipient"
		messagesPerWay = 100
		rounds         = 6
	)
	require.NoError(t, suite.PostChannel(ctx, cluster.MustNode(1).APIAddr(), map[string]any{
		"channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
		"reset": 1, "subscribers": []string{leftUID, rightUID, recipientUID},
	}), cluster.DumpDiagnostics())

	api := lifecycleTarget(cluster)
	left := connectAndFullSync(t, ctx, cluster, api, 1, leftUID)
	defer func() { _ = left.Close() }()
	right := connectAndFullSync(t, ctx, cluster, api, 2, rightUID)
	defer func() { _ = right.Close() }()
	recipient := connectAndFullSync(t, ctx, cluster, api, 3, recipientUID)
	defer func() { _ = recipient.Close() }()

	var previous uint64
	for round := 0; round < rounds; round++ {
		start := round*messagesPerWay + 1
		var sends sync.WaitGroup
		sendErrs := make(chan error, 2)
		sends.Add(2)
		go func() {
			defer sends.Done()
			sendErrs <- sendGroupBurst(left, channelID, "left", start, messagesPerWay)
		}()
		go func() {
			defer sends.Done()
			sendErrs <- sendGroupBurst(right, channelID, "right", start, messagesPerWay)
		}()
		sends.Wait()
		close(sendErrs)
		for sendErr := range sendErrs {
			require.NoError(t, sendErr, cluster.DumpDiagnostics())
		}

		requireSuccessfulSendacks(t, cluster, left, messagesPerWay)
		requireSuccessfulSendacks(t, cluster, right, messagesPerWay)
		previous = requireMonotonicGroupReceives(t, cluster, recipient, channelID, messagesPerWay*2, previous)
	}
}

func sendPersonBurst(
	client *suite.WKProtoClient,
	peerUID, prefix string,
	start, count int,
) error {
	for offset := 0; offset < count; offset++ {
		ordinal := start + offset
		if err := client.SendFrame(&frame.SendPacket{
			ChannelID: peerUID, ChannelType: frame.ChannelTypePerson,
			ClientSeq: uint64(ordinal), ClientMsgNo: fmt.Sprintf("e2e-sequence-%s-%06d", prefix, ordinal),
			Payload: []byte("cross-ingress sequence evidence"),
		}); err != nil {
			return err
		}
	}
	return nil
}

func sendGroupBurst(client *suite.WKProtoClient, channelID, prefix string, start, count int) error {
	for offset := 0; offset < count; offset++ {
		ordinal := start + offset
		if err := client.SendFrame(&frame.SendPacket{
			ChannelID: channelID, ChannelType: frame.ChannelTypeGroup,
			ClientSeq: uint64(ordinal), ClientMsgNo: fmt.Sprintf("e2e-sequence-group-%s-%06d", prefix, ordinal),
			Payload: []byte("cross-ingress group sequence evidence"),
		}); err != nil {
			return err
		}
	}
	return nil
}

func requireSuccessfulSendacks(t *testing.T, cluster *suite.StartedCluster, client *suite.WKProtoClient, count int) {
	t.Helper()
	for range count {
		ack, err := client.ReadSendAck()
		require.NoError(t, err, cluster.DumpDiagnostics())
		require.Equal(t, frame.ReasonSuccess, ack.ReasonCode, cluster.DumpDiagnostics())
		require.NotZero(t, ack.MessageSeq, cluster.DumpDiagnostics())
	}
}

func requireMonotonicPersonReceives(
	t *testing.T,
	cluster *suite.StartedCluster,
	client *suite.WKProtoClient,
	peerUID string,
	count int,
	previous uint64,
) uint64 {
	t.Helper()
	for range count {
		recv, err := client.ReadRecv()
		require.NoError(t, err, cluster.DumpDiagnostics())
		require.Equal(t, peerUID, recv.FromUID, cluster.DumpDiagnostics())
		require.Greater(t, recv.MessageSeq, previous,
			"person-channel receive sequence regressed from %d to %d\n%s", previous, recv.MessageSeq, cluster.DumpDiagnostics())
		require.NoError(t, client.RecvAck(recv.MessageID, recv.MessageSeq), cluster.DumpDiagnostics())
		previous = recv.MessageSeq
	}
	return previous
}

func requireMonotonicGroupReceives(
	t *testing.T,
	cluster *suite.StartedCluster,
	client *suite.WKProtoClient,
	channelID string,
	count int,
	previous uint64,
) uint64 {
	t.Helper()
	for range count {
		recv, err := client.ReadRecv()
		require.NoError(t, err, cluster.DumpDiagnostics())
		require.Equal(t, channelID, recv.ChannelID, cluster.DumpDiagnostics())
		require.Greater(t, recv.MessageSeq, previous,
			"group-channel receive sequence regressed from %d to %d\n%s", previous, recv.MessageSeq, cluster.DumpDiagnostics())
		require.NoError(t, client.RecvAck(recv.MessageID, recv.MessageSeq), cluster.DumpDiagnostics())
		previous = recv.MessageSeq
	}
	return previous
}

func startLifecycleCluster(t *testing.T) *suite.StartedCluster {
	t.Helper()
	overrides := map[string]string{
		"WK_CLUSTER_INITIAL_SLOT_COUNT": "12",
		"WK_CLUSTER_HASH_SLOT_COUNT":    "256",
		"WK_CLUSTER_SLOT_REPLICA_N":     "3",
		"WK_CLUSTER_CHANNEL_REPLICA_N":  "3",
		"WK_CLUSTER_MAX_CHANNELS":       "50000",
		"WK_GATEWAY_SEND_TIMEOUT":       "20s",
		"WK_BENCH_API_ENABLE":           "true",
		"WK_BENCH_API_TOKEN":            benchToken,
		"WK_BENCH_API_MAX_BATCH_SIZE":   "1200",
		"WK_METRICS_ENABLE":             "true",
		"WK_DEBUG_API_ENABLE":           "true",
	}
	options := []suite.Option{
		suite.WithManagerHTTP(),
		suite.WithNodeConfigOverrides(1, overrides),
		suite.WithNodeConfigOverrides(2, overrides),
		suite.WithNodeConfigOverrides(3, overrides),
	}
	if artifactRoot := os.Getenv("WK_E2E_CHAT_LIFECYCLE_ARTIFACT_ROOT"); artifactRoot != "" {
		options = append(options, suite.WithWorkspaceRootDir(artifactRoot))
	}
	return suite.New(t).StartThreeNodeCluster(options...)
}

func lifecycleTarget(cluster *suite.StartedCluster) *target.Client {
	addrs := make([]string, 0, len(cluster.Nodes))
	for i := range cluster.Nodes {
		addrs = append(addrs, "http://"+cluster.Nodes[i].APIAddr())
	}
	return target.NewClient(target.Config{APIAddrs: addrs, Token: benchToken})
}

func connectAndFullSync(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	api *target.Client,
	nodeID uint64,
	uid string,
) *suite.WKProtoClient {
	t.Helper()
	client, err := suite.NewWKProtoClientWithTimeout(operationTimeout)
	require.NoError(t, err)
	require.NoError(t, client.Connect(cluster.MustNode(nodeID).GatewayAddr(), uid, uid+"-device"), cluster.DumpDiagnostics())
	rows, err := api.ConversationSync(ctx, benchlifecycle.NewConversationSyncRequest(uid))
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.NoError(t, benchlifecycle.ValidateConversationSync(rows), cluster.DumpDiagnostics())
	return client
}

func connectAndFullSyncContaining(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	api *target.Client,
	nodeID uint64,
	uid string,
	clientMsgNo string,
) *suite.WKProtoClient {
	t.Helper()
	client, err := suite.NewWKProtoClientWithTimeout(operationTimeout)
	require.NoError(t, err)
	require.NoError(t, client.Connect(cluster.MustNode(nodeID).GatewayAddr(), uid, uid+"-device-reheat"), cluster.DumpDiagnostics())

	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastRows []target.ConversationSyncConversation
	for {
		rows, syncErr := api.ConversationSync(ctx, benchlifecycle.NewConversationSyncRequest(uid))
		if syncErr == nil {
			lastRows = rows
			syncErr = benchlifecycle.ValidateConversationSync(rows)
		}
		if syncErr == nil && syncContains(rows, clientMsgNo) {
			return client
		}
		select {
		case <-ctx.Done():
			t.Fatalf("full login sync canceled: rows=%d err=%v\n%s", len(lastRows), ctx.Err(), cluster.DumpDiagnostics())
		case <-deadline.C:
			t.Fatalf("full login sync did not reconstruct prior message: rows=%d\n%s", len(lastRows), cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func syncContains(rows []target.ConversationSyncConversation, clientMsgNo string) bool {
	for _, row := range rows {
		if row.LastMessage != nil && row.LastMessage.ClientMsgNo == clientMsgNo {
			return true
		}
	}
	return false
}

func sendPersonMessage(
	t *testing.T,
	cluster *suite.StartedCluster,
	sender, recipient *suite.WKProtoClient,
	peerUID string,
	clientSeq uint64,
	clientMsgNo string,
) uint64 {
	t.Helper()
	require.NoError(t, sender.SendFrame(&frame.SendPacket{
		ChannelID: peerUID, ChannelType: frame.ChannelTypePerson,
		ClientSeq: clientSeq, ClientMsgNo: clientMsgNo, Payload: []byte("chat lifecycle evidence"),
	}), cluster.DumpDiagnostics())
	ack, err := sender.ReadSendAck()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, frame.ReasonSuccess, ack.ReasonCode, cluster.DumpDiagnostics())
	recv, err := recipient.ReadRecv()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, ack.MessageID, recv.MessageID)
	require.Equal(t, ack.MessageSeq, recv.MessageSeq)
	require.NoError(t, recipient.RecvAck(recv.MessageID, recv.MessageSeq), cluster.DumpDiagnostics())
	return ack.MessageSeq
}

func requireLoadedOnAllNodes(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	api *target.Client,
	req model.ChannelRuntimeProbeRequest,
) []model.ChannelRuntimeProbeResult {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last []model.ChannelRuntimeProbeResult
	var lastErr error
	for {
		last, lastErr = api.ProbeChannelRuntimeAll(ctx, req)
		if lastErr == nil && loadedEvidenceValid(last) {
			return last
		}
		select {
		case <-ctx.Done():
			t.Fatalf("runtime load probe canceled: %v; evidence=%s\n%s", ctx.Err(), summarizeProbe(last), cluster.DumpDiagnostics())
		case <-deadline.C:
			t.Fatalf("runtime did not load on all replicas: err=%v evidence=%s\n%s", lastErr, summarizeProbe(last), cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func loadedEvidenceValid(results []model.ChannelRuntimeProbeResult) bool {
	if len(results) != 3 {
		return false
	}
	leaders, followers := 0, 0
	var leo, hw uint64
	for index, result := range results {
		if result.Checked != 1 || len(result.Channels) != 1 || len(result.Missing) != 0 {
			return false
		}
		row := result.Channels[0]
		if row.Status != "active" || row.HW == 0 || row.LEO < row.HW {
			return false
		}
		if index == 0 {
			leo, hw = row.LEO, row.HW
		} else if row.LEO != leo || row.HW != hw {
			return false
		}
		switch row.Role {
		case "leader":
			leaders++
		case "follower":
			followers++
		default:
			return false
		}
	}
	return leaders == 1 && followers == 2
}

func requireMissingAfterNaturalIdle(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	api *target.Client,
	req model.ChannelRuntimeProbeRequest,
	quietStarted time.Time,
) {
	t.Helper()
	deadline := time.NewTimer(idlePollDeadline)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var last []model.ChannelRuntimeProbeResult
	var lastErr error
	for {
		last, lastErr = api.ProbeChannelRuntimeAll(ctx, req)
		if lastErr == nil && time.Since(quietStarted) >= idleEvictAfter && missingEvidenceValid(last) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("natural idle probe canceled: %v; evidence=%s\n%s", ctx.Err(), summarizeProbe(last), cluster.DumpDiagnostics())
		case <-deadline.C:
			t.Fatalf("runtime remained loaded after natural idle: err=%v evidence=%s\n%s", lastErr, summarizeProbe(last), cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func missingEvidenceValid(results []model.ChannelRuntimeProbeResult) bool {
	if len(results) != 3 {
		return false
	}
	for _, result := range results {
		if result.Checked != 1 || result.LoadedLeader != 0 || result.LoadedFollower != 0 ||
			len(result.Channels) != 1 || result.Channels[0].Role != "missing" || result.Channels[0].Status != "missing" {
			return false
		}
	}
	return true
}

func requireMonotonicRuntimeEvidence(t *testing.T, before, after []model.ChannelRuntimeProbeResult) {
	t.Helper()
	if len(before) != 3 || len(after) != 3 {
		t.Fatalf("invalid lifecycle evidence cardinality: before=%d after=%d", len(before), len(after))
	}
	maxBefore := uint64(0)
	for _, result := range before {
		maxBefore = max(maxBefore, result.Channels[0].HW)
	}
	for _, result := range after {
		if result.Channels[0].HW <= maxBefore || result.Channels[0].LEO < result.Channels[0].HW {
			t.Fatalf("reheated runtime did not advance monotonically: before=%s after=%s", summarizeProbe(before), summarizeProbe(after))
		}
	}
}

func requireCreatedTotal(t *testing.T, ctx context.Context, cluster *suite.StartedCluster) uint64 {
	t.Helper()
	var total uint64
	for i := range cluster.Nodes {
		nodeClient := target.NewClient(target.Config{APIAddrs: []string{"http://" + cluster.Nodes[i].APIAddr()}, Token: benchToken})
		snapshot, err := nodeClient.Metrics(ctx)
		require.NoError(t, err, cluster.DumpDiagnostics())
		require.NoError(t, snapshot.ValidateRequired(), cluster.DumpDiagnostics())
		for _, slot := range snapshot.MetaCreatedBySlot {
			total += slot.Created
		}
	}
	return total
}

func requireCreatedTotalEventually(
	t *testing.T,
	ctx context.Context,
	cluster *suite.StartedCluster,
	want uint64,
) uint64 {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var got uint64
	var lastErr error
	for {
		got, lastErr = createdTotal(ctx, cluster)
		if lastErr == nil && got == want {
			return got
		}
		if lastErr == nil && got > want {
			t.Fatalf("authoritative metadata create total = %d, want %d", got, want)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("metadata metric canceled: %v", ctx.Err())
		case <-deadline.C:
			t.Fatalf("authoritative metadata create total = %d, want %d: %v\n%s", got, want, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func createdTotal(ctx context.Context, cluster *suite.StartedCluster) (uint64, error) {
	var total uint64
	for i := range cluster.Nodes {
		nodeClient := target.NewClient(target.Config{APIAddrs: []string{"http://" + cluster.Nodes[i].APIAddr()}, Token: benchToken})
		snapshot, err := nodeClient.Metrics(ctx)
		if err != nil {
			return 0, err
		}
		if err := snapshot.ValidateRequired(); err != nil {
			return 0, err
		}
		for _, slot := range snapshot.MetaCreatedBySlot {
			total += slot.Created
		}
	}
	return total, nil
}

func summarizeProbe(results []model.ChannelRuntimeProbeResult) string {
	if len(results) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(results))
	for _, result := range results {
		if len(result.Channels) != 1 {
			parts = append(parts, fmt.Sprintf("node=%d rows=%d", result.NodeID, len(result.Channels)))
			continue
		}
		row := result.Channels[0]
		parts = append(parts, fmt.Sprintf("node=%d role=%s status=%s leo=%d hw=%d", result.NodeID, row.Role, row.Status, row.LEO, row.HW))
	}
	return fmt.Sprintf("%v", parts)
}
