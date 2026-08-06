//go:build e2e

package conversation_directory_multi_node

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const hydrationRemoteBatchMetric = "wukongim_conversation_hydration_remote_batch_calls"

const (
	directoryPerformanceEnv         = "WK_E2E_CONVERSATION_DIRECTORY_PERF"
	directoryPerformanceChannels    = 200
	directoryPerformanceConcurrency = 32
	directoryPerformanceRounds      = 10
)

type directoryChannel struct {
	ID          string
	Leader      uint64
	ClientMsgNo string
	MessageSeq  uint64
}

type channelRuntimeMetaPage struct {
	Items []channelRuntimeMetaItem `json:"items"`
}

type channelRuntimeMetaItem struct {
	ChannelID   string `json:"channel_id"`
	ChannelType int64  `json:"channel_type"`
	Leader      uint64 `json:"leader"`
	Status      string `json:"status"`
}

type directorySyncedMessage struct {
	ClientMsgNo string `json:"client_msg_no"`
}

type directoryMessagePage struct {
	Messages []directorySyncedMessage `json:"messages"`
}

type directoryPerformanceEvidence struct {
	Schema                 string  `json:"schema"`
	Channels               int     `json:"channels"`
	PageSize               int     `json:"page_size"`
	Concurrency            int     `json:"concurrency"`
	Rounds                 int     `json:"rounds"`
	Requests               int     `json:"requests"`
	ElapsedMS              float64 `json:"elapsed_ms"`
	RequestsPerSecond      float64 `json:"requests_per_second"`
	P50MS                  float64 `json:"p50_ms"`
	P95MS                  float64 `json:"p95_ms"`
	P99MS                  float64 `json:"p99_ms"`
	MaxMS                  float64 `json:"max_ms"`
	MembershipMutationRows float64 `json:"membership_mutation_rows"`
	HydrationBatches       float64 `json:"hydration_batches"`
	HydrationItems         float64 `json:"hydration_items"`
	RemoteBatchCalls       float64 `json:"remote_batch_calls"`
	LocalReads             float64 `json:"local_reads"`
	MailboxAdmissionFull   float64 `json:"mailbox_admission_full"`
	CPUObserved            bool    `json:"cpu_observed"`
	CPUSeconds             float64 `json:"cpu_seconds"`
	AllocatedBytes         float64 `json:"allocated_bytes"`
	AggregateHeapBytes     float64 `json:"aggregate_heap_bytes"`
}

type directoryPerformanceSnapshot struct {
	membershipMutationRows float64
	hydrationBatches       suite.MetricHistogramSnapshot
	hydrationItems         suite.MetricHistogramSnapshot
	remoteBatchCalls       suite.MetricHistogramSnapshot
	localReads             suite.MetricHistogramSnapshot
	mailboxAdmissionFull   float64
	cpuObserved            bool
	cpuSeconds             float64
	allocatedBytes         float64
	aggregateHeapBytes     float64
}

func TestThreeNodeConversationDirectoryBatchesHydrationByChannelLeader(t *testing.T) {
	cluster := startStableThreeNodeCluster(t)
	origin := cluster.MustNode(1)

	const (
		uid       = "directory-multi-user"
		senderUID = "directory-multi-sender"
	)
	channelsByLeader := createDirectoryChannelsByLeader(t, cluster, uid, senderUID, 2)
	require.Equal(t, []uint64{1, 2, 3}, sortedDirectoryLeaderIDs(channelsByLeader), cluster.DumpDiagnostics())
	requireDirectoryPageEventually(t, cluster, origin, uid, 6, func(page suite.ConversationListPage) error {
		if len(page.Conversations) != 6 || len(page.Unresolved) != 0 || !page.Done {
			return fmt.Errorf("page = %+v, want six resolved conversations and done", page)
		}
		return nil
	})

	beforeSamples := fetchMetricSamples(t, *origin)
	before := suite.HistogramSnapshot(beforeSamples, hydrationRemoteBatchMetric, map[string]string{"result": "ok"})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	page, err := suite.PostConversationListPage(ctx, origin.APIAddr(), suite.ConversationListRequest{UID: uid, Limit: 6})
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.True(t, page.Done)
	require.NotEmpty(t, page.NextCursor)
	require.Positive(t, page.Coverage)
	require.Empty(t, page.Deletes)
	require.Empty(t, page.Unresolved)
	require.Len(t, page.Conversations, 6)
	missingMessages := make([]string, 0)
	for _, channels := range channelsByLeader {
		for _, channel := range channels {
			item, ok := suite.FindConversation(page, channel.ID)
			require.True(t, ok, "conversation %s missing from %#v\n%s", channel.ID, page.Conversations, cluster.DumpDiagnostics())
			if item.LastMessage == nil {
				missingMessages = append(missingMessages, fmt.Sprintf("channel=%s leader=%d expected_seq=%d row=%+v", channel.ID, channel.Leader, channel.MessageSeq, item))
				continue
			}
			require.Equal(t, channel.ClientMsgNo, item.LastMessage.ClientMsgNo)
		}
	}
	require.Empty(t, missingMessages, "hydrated messages missing:\n%s\n%s", missingMessages, cluster.DumpDiagnostics())

	afterSamples := fetchMetricSamples(t, *origin)
	after := suite.HistogramSnapshot(afterSamples, hydrationRemoteBatchMetric, map[string]string{"result": "ok"})
	require.Equal(t, float64(1), after.Count-before.Count, "one directory request must record one hydration batch")
	require.Equal(t, float64(2), after.Sum-before.Sum, "four remote channels must be grouped into two Leader RPCs")
}

func TestThreeNodeConversationDirectoryIsolatesUnavailableLeaderAndRetries(t *testing.T) {
	cluster := startStableThreeNodeCluster(t)
	origin := cluster.MustNode(1)

	const (
		senderUID     = "directory-retry-sender"
		stoppedLeader = uint64(2)
	)
	uid := uidOwnedBySlotLeaderOtherThan(t, cluster, 1, stoppedLeader)
	channelsByLeader := createDirectoryChannelsByLeader(t, cluster, uid, senderUID, 1)
	requireDirectoryPageEventually(t, cluster, origin, uid, 3, func(page suite.ConversationListPage) error {
		if len(page.Conversations) != 3 || len(page.Unresolved) != 0 || !page.Done {
			return fmt.Errorf("baseline page = %+v, want three resolved conversations and done", page)
		}
		return nil
	})

	affected := channelsByLeader[stoppedLeader]
	require.Len(t, affected, 1)
	require.NoError(t, cluster.MustNode(stoppedLeader).Stop(), cluster.DumpDiagnostics())

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	unavailablePage, err := suite.PostConversationListPage(ctx, origin.APIAddr(), suite.ConversationListRequest{UID: uid, Limit: 3})
	cancel()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.True(t, unavailablePage.Done)
	require.NotEmpty(t, unavailablePage.NextCursor, "cursor must cover unresolved memberships")
	require.Positive(t, unavailablePage.Coverage)
	require.Empty(t, unavailablePage.Deletes)
	require.Len(t, unavailablePage.Conversations, 2, cluster.DumpDiagnostics())
	require.Len(t, unavailablePage.Unresolved, 1, cluster.DumpDiagnostics())
	_, ok := suite.FindConversationKey(unavailablePage.Unresolved, affected[0].ID, int64(frame.ChannelTypeGroup))
	require.True(t, ok, "unavailable Leader channel missing from unresolved: %+v", unavailablePage)
	for leaderID, channels := range channelsByLeader {
		if leaderID == stoppedLeader {
			continue
		}
		item, found := suite.FindConversation(unavailablePage, channels[0].ID)
		require.True(t, found, "healthy Leader %d channel missing: %+v", leaderID, unavailablePage)
		require.NotNil(t, item.LastMessage)
		require.Equal(t, channels[0].ClientMsgNo, item.LastMessage.ClientMsgNo)
	}

	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	afterCursor, err := suite.PostConversationListPage(ctx, origin.APIAddr(), suite.ConversationListRequest{
		UID: uid, Cursor: unavailablePage.NextCursor, Limit: 3, CompletedCoverage: unavailablePage.Coverage,
	})
	cancel()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.True(t, afterCursor.Done)
	require.Empty(t, afterCursor.Conversations)
	require.Empty(t, afterCursor.Unresolved)
	require.Empty(t, afterCursor.Deletes)

	require.NoError(t, cluster.StartStoppedNode(stoppedLeader), cluster.DumpDiagnostics())
	readyCtx, readyCancel := context.WithTimeout(context.Background(), 40*time.Second)
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())
	readyCancel()

	retryPage := requireConversationRetryEventually(t, cluster, origin, suite.ConversationRetryRequest{
		UID: uid, Channels: unavailablePage.Unresolved,
	})
	require.True(t, retryPage.Done)
	require.Empty(t, retryPage.Deletes)
	require.Empty(t, retryPage.Unresolved)
	require.Len(t, retryPage.Conversations, 1)
	recovered, ok := suite.FindConversation(retryPage, affected[0].ID)
	require.True(t, ok)
	require.NotNil(t, recovered.LastMessage)
	require.Equal(t, affected[0].ClientMsgNo, recovered.LastMessage.ClientMsgNo)
}

func TestFourNodeConversationDirectoryRoutesUIDMembershipReadsFromNonReplicaIngress(t *testing.T) {
	cluster := startStableFourNodeReplicaThreeCluster(t)
	origin := cluster.MustNode(1)

	uid := uidOwnedOutsideNode(t, cluster, 1)
	senderUID := "directory-non-replica-sender"
	channelID, sourceLeader, commandLeader := createDirectoryChannelLedOutsideNode(t, cluster, origin, senderUID, uid, 1)
	require.NotEqual(t, uint64(1), sourceLeader)
	require.NotEqual(t, uint64(1), commandLeader)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ordinaryMsgNo := "non-replica-ordinary"
	sendDirectoryMessage(t, ctx, cluster, *origin, senderUID, channelID, ordinaryMsgNo)

	page := requireDirectoryPageEventually(t, cluster, origin, uid, 10, func(page suite.ConversationListPage) error {
		item, ok := suite.FindConversation(page, channelID)
		if !ok || item.LastMessage == nil || item.LastMessage.ClientMsgNo != ordinaryMsgNo {
			return fmt.Errorf("directory page = %+v, want %s through non-replica ingress", page, ordinaryMsgNo)
		}
		return nil
	})

	retry := requireConversationRetryEventually(t, cluster, origin, suite.ConversationRetryRequest{
		UID: uid,
		Channels: []suite.ConversationListKey{{
			ChannelID: channelID, ChannelType: int64(frame.ChannelTypeGroup),
		}},
	})
	require.Len(t, retry.Conversations, 1)
	require.Empty(t, retry.Unresolved)
	require.Empty(t, retry.Deletes)
	require.NotEmpty(t, page.NextCursor)

	requireOrdinaryMessagePullEventually(t, cluster, origin, uid, channelID, ordinaryMsgNo)

	_, err := suite.PostJSON(ctx, "http://"+origin.APIAddr()+"/message/cmd/bind", map[string]any{
		"uid": uid, "channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
	}, nil)
	require.NoError(t, err, cluster.DumpDiagnostics())
	cmdMsgNo := "non-replica-cmd"
	cmdResp, err := suite.PostMessageSendEventually(ctx, origin.APIAddr(), map[string]any{
		"from_uid": senderUID, "channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
		"client_msg_no": cmdMsgNo, "sync_once": 1,
		"payload": base64.StdEncoding.EncodeToString([]byte(cmdMsgNo)),
	})
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, uint8(frame.ReasonSuccess), cmdResp.Reason)
	require.NotZero(t, cmdResp.MessageSeq, "persistent sync_once send must append to the command log")
	requireCMDMessageSyncEventually(t, cluster, origin, uid, cmdMsgNo)
}

func TestThreeNodeConversationDirectoryPerformanceAcceptance(t *testing.T) {
	if os.Getenv(directoryPerformanceEnv) != "1" {
		t.Skip("set WK_E2E_CONVERSATION_DIRECTORY_PERF=1 to run the bounded conversation-directory performance gate")
	}
	cluster := startStableThreeNodeCluster(t)
	origin := cluster.MustNode(1)
	users := []string{"directory-perf-user-0", "directory-perf-user-1", "directory-perf-user-2", "directory-perf-user-3"}
	prepareDirectoryPerformanceChannels(t, cluster, origin, users)
	requireDirectoryPageEventually(t, cluster, origin, users[0], directoryPerformanceChannels, func(page suite.ConversationListPage) error {
		return validateDirectoryPerformancePage(page, directoryPerformanceChannels, directoryPerformanceChannels)
	})

	for _, pageSize := range []int{25, 100, directoryPerformanceChannels} {
		evidence := runDirectoryPerformancePhase(t, cluster, origin, users, pageSize)
		encoded, err := json.Marshal(evidence)
		require.NoError(t, err)
		t.Logf("WK-CONVERSATION-DIRECTORY-PERF %s", encoded)
	}
}

func prepareDirectoryPerformanceChannels(t *testing.T, cluster *suite.StartedCluster, origin *suite.StartedNode, users []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	const workers = 8
	senderUID := "directory-perf-sender"
	subscribers := append([]string{senderUID}, users...)
	prefix := fmt.Sprintf("directory-perf-%d", time.Now().UnixNano())
	jobs := make(chan int)
	errs := make(chan error, directoryPerformanceChannels)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				channelID := fmt.Sprintf("%s-%03d", prefix, index)
				if err := suite.PostChannel(ctx, origin.APIAddr(), map[string]any{
					"channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
					"reset": 1, "subscribers": subscribers,
				}); err != nil {
					errs <- fmt.Errorf("create channel %s: %w", channelID, err)
					continue
				}
				clientMsgNo := fmt.Sprintf("directory-perf-message-%03d", index)
				response, err := suite.PostMessageSendEventually(ctx, origin.APIAddr(), map[string]any{
					"from_uid": senderUID, "channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
					"client_msg_no": clientMsgNo,
					"payload":       base64.StdEncoding.EncodeToString([]byte(clientMsgNo)),
				})
				if err != nil {
					errs <- fmt.Errorf("send channel %s: %w", channelID, err)
					continue
				}
				if response.Reason != uint8(frame.ReasonSuccess) || response.MessageSeq == 0 {
					errs <- fmt.Errorf("send channel %s response = %+v", channelID, response)
				}
			}
		}()
	}
	for index := 0; index < directoryPerformanceChannels; index++ {
		jobs <- index
	}
	close(jobs)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err, cluster.DumpDiagnostics())
	}
}

func runDirectoryPerformancePhase(t *testing.T, cluster *suite.StartedCluster, origin *suite.StartedNode, users []string, pageSize int) directoryPerformanceEvidence {
	t.Helper()
	before := captureDirectoryPerformanceSnapshot(t, cluster)
	requestCount := directoryPerformanceConcurrency * directoryPerformanceRounds
	latencies := make(chan time.Duration, requestCount)
	errs := make(chan error, requestCount)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for worker := 0; worker < directoryPerformanceConcurrency; worker++ {
		uid := users[worker%len(users)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for round := 0; round < directoryPerformanceRounds; round++ {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				requestStart := time.Now()
				page, err := suite.PostConversationListPage(ctx, origin.APIAddr(), suite.ConversationListRequest{UID: uid, Limit: pageSize})
				latency := time.Since(requestStart)
				cancel()
				if err == nil {
					err = validateDirectoryPerformancePage(page, pageSize, directoryPerformanceChannels)
				}
				if err != nil {
					errs <- fmt.Errorf("uid=%s round=%d: %w", uid, round, err)
					continue
				}
				latencies <- latency
			}
		}()
	}
	measuredStart := time.Now()
	close(start)
	wg.Wait()
	elapsed := time.Since(measuredStart)
	close(errs)
	close(latencies)
	for err := range errs {
		require.NoError(t, err, cluster.DumpDiagnostics())
	}
	measuredLatencies := make([]time.Duration, 0, requestCount)
	for latency := range latencies {
		measuredLatencies = append(measuredLatencies, latency)
	}
	require.Len(t, measuredLatencies, requestCount)
	after := captureDirectoryPerformanceSnapshot(t, cluster)

	evidence := directoryPerformanceEvidence{
		Schema:                 "wukongim/conversation-directory-performance/v1",
		Channels:               directoryPerformanceChannels,
		PageSize:               pageSize,
		Concurrency:            directoryPerformanceConcurrency,
		Rounds:                 directoryPerformanceRounds,
		Requests:               requestCount,
		ElapsedMS:              float64(elapsed) / float64(time.Millisecond),
		RequestsPerSecond:      float64(requestCount) / elapsed.Seconds(),
		P50MS:                  directoryDurationMS(directoryPercentile(measuredLatencies, 0.50)),
		P95MS:                  directoryDurationMS(directoryPercentile(measuredLatencies, 0.95)),
		P99MS:                  directoryDurationMS(directoryPercentile(measuredLatencies, 0.99)),
		MaxMS:                  directoryDurationMS(directoryPercentile(measuredLatencies, 1)),
		MembershipMutationRows: after.membershipMutationRows - before.membershipMutationRows,
		HydrationBatches:       after.hydrationBatches.Count - before.hydrationBatches.Count,
		HydrationItems:         after.hydrationItems.Sum - before.hydrationItems.Sum,
		RemoteBatchCalls:       after.remoteBatchCalls.Sum - before.remoteBatchCalls.Sum,
		LocalReads:             after.localReads.Sum - before.localReads.Sum,
		MailboxAdmissionFull:   after.mailboxAdmissionFull - before.mailboxAdmissionFull,
		CPUObserved:            before.cpuObserved && after.cpuObserved,
		CPUSeconds:             after.cpuSeconds - before.cpuSeconds,
		AllocatedBytes:         after.allocatedBytes - before.allocatedBytes,
		AggregateHeapBytes:     after.aggregateHeapBytes,
	}
	require.Zero(t, evidence.MembershipMutationRows, "conversation sync must not write memberships")
	require.Equal(t, float64(requestCount), evidence.HydrationBatches)
	require.Equal(t, float64(requestCount*pageSize), evidence.HydrationItems)
	require.Equal(t, float64(requestCount*pageSize), evidence.LocalReads)
	require.Positive(t, evidence.RemoteBatchCalls)
	require.LessOrEqual(t, evidence.RemoteBatchCalls, float64(requestCount*2), "three-node sync may issue at most two remote Leader calls per page")
	require.Zero(t, evidence.MailboxAdmissionFull, "conversation sync saturated a Channel reactor mailbox")
	return evidence
}

func captureDirectoryPerformanceSnapshot(t *testing.T, cluster *suite.StartedCluster) directoryPerformanceSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snapshot := directoryPerformanceSnapshot{cpuObserved: true}
	for _, node := range cluster.Nodes {
		samples, err := suite.FetchMetricSamples(ctx, node.APIAddr())
		require.NoError(t, err, node.DumpDiagnostics())
		snapshot.membershipMutationRows += suite.SumMetricSamples(samples, "wukongim_conversation_membership_mutation_rows_total", map[string]string{"directory": "ordinary"})
		snapshot.hydrationBatches = addDirectoryHistogram(snapshot.hydrationBatches, suite.HistogramSnapshot(samples, "wukongim_conversation_hydration_batch_duration_seconds", map[string]string{"result": "ok"}))
		snapshot.hydrationItems = addDirectoryHistogram(snapshot.hydrationItems, suite.HistogramSnapshot(samples, "wukongim_conversation_hydration_batch_items", map[string]string{"result": "ok"}))
		snapshot.remoteBatchCalls = addDirectoryHistogram(snapshot.remoteBatchCalls, suite.HistogramSnapshot(samples, hydrationRemoteBatchMetric, map[string]string{"result": "ok"}))
		snapshot.localReads = addDirectoryHistogram(snapshot.localReads, suite.HistogramSnapshot(samples, "wukongim_conversation_hydration_local_reads", map[string]string{"result": "ok"}))
		snapshot.mailboxAdmissionFull += suite.SumMetricSamples(samples, "wukongim_runtime_pool_admission_total", map[string]string{
			"component": "channel", "queue": "mailbox", "result": "full",
		})
		nodeCPUObserved := false
		for _, sample := range samples {
			if sample.Name == "process_cpu_seconds_total" {
				nodeCPUObserved = true
				snapshot.cpuSeconds += sample.Value
			}
		}
		snapshot.cpuObserved = snapshot.cpuObserved && nodeCPUObserved
		snapshot.allocatedBytes += suite.SumMetricSamples(samples, "go_memstats_alloc_bytes_total", nil)
		snapshot.aggregateHeapBytes += suite.SumMetricSamples(samples, "go_memstats_heap_alloc_bytes", nil)
	}
	return snapshot
}

func validateDirectoryPerformancePage(page suite.ConversationListPage, pageSize, total int) error {
	if len(page.Conversations) != pageSize || len(page.Unresolved) != 0 || len(page.Deletes) != 0 {
		return fmt.Errorf("page shape conversations=%d unresolved=%d deletes=%d, want %d/0/0", len(page.Conversations), len(page.Unresolved), len(page.Deletes), pageSize)
	}
	if page.Done != (pageSize == total) {
		return fmt.Errorf("done = %v, want %v for page size %d", page.Done, pageSize == total, pageSize)
	}
	for _, item := range page.Conversations {
		if item.LastMessage == nil {
			return fmt.Errorf("channel %s has no hydrated last message", item.ChannelID)
		}
	}
	return nil
}

func addDirectoryHistogram(left, right suite.MetricHistogramSnapshot) suite.MetricHistogramSnapshot {
	return suite.MetricHistogramSnapshot{Count: left.Count + right.Count, Sum: left.Sum + right.Sum}
}

func directoryPercentile(values []time.Duration, quantile float64) time.Duration {
	ordered := append([]time.Duration(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	if len(ordered) == 0 {
		return 0
	}
	index := int(float64(len(ordered)-1) * quantile)
	return ordered[index]
}

func directoryDurationMS(value time.Duration) float64 {
	return float64(value) / float64(time.Millisecond)
}

func requireDirectoryPageEventually(t *testing.T, cluster *suite.StartedCluster, node *suite.StartedNode, uid string, limit int, check func(suite.ConversationListPage) error) suite.ConversationListPage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var lastPage suite.ConversationListPage
	var lastErr error
	for {
		page, err := suite.PostConversationListPage(ctx, node.APIAddr(), suite.ConversationListRequest{UID: uid, Limit: limit})
		if err == nil {
			lastPage = page
			if checkErr := check(page); checkErr == nil {
				return page
			} else {
				lastErr = checkErr
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("directory page for uid %s did not converge: lastPage=%+v lastErr=%v\n%s", uid, lastPage, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func requireConversationRetryEventually(t *testing.T, cluster *suite.StartedCluster, node *suite.StartedNode, req suite.ConversationRetryRequest) suite.ConversationListPage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var lastPage suite.ConversationListPage
	var lastErr error
	for {
		requestCtx, requestCancel := context.WithTimeout(ctx, 2*time.Second)
		page, err := suite.PostConversationRetry(requestCtx, node.APIAddr(), req)
		requestCancel()
		if err == nil {
			lastPage = page
			if len(page.Conversations) == len(req.Channels) && len(page.Unresolved) == 0 {
				return page
			}
			lastErr = fmt.Errorf("retry page = %+v, want %d resolved conversations", page, len(req.Channels))
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("conversation retry did not converge: lastPage=%+v lastErr=%v\n%s", lastPage, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func startStableThreeNodeCluster(t *testing.T) *suite.StartedCluster {
	t.Helper()
	cluster := suite.New(t).StartThreeNodeCluster(suite.WithManagerHTTP())
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	_, err := cluster.WaitSlotLeadersStable(ctx, 2*time.Second)
	require.NoError(t, err, cluster.DumpDiagnostics())
	return cluster
}

func startStableFourNodeReplicaThreeCluster(t *testing.T) *suite.StartedCluster {
	t.Helper()
	config := map[string]string{
		"WK_CLUSTER_SLOT_REPLICA_N":    "3",
		"WK_CLUSTER_CHANNEL_REPLICA_N": "1",
	}
	cluster := suite.New(t).StartStaticCluster(4,
		suite.WithManagerHTTP(),
		suite.WithNodeConfigOverrides(1, config),
		suite.WithNodeConfigOverrides(2, config),
		suite.WithNodeConfigOverrides(3, config),
		suite.WithNodeConfigOverrides(4, config),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	_, err := cluster.WaitSlotLeadersStable(ctx, 2*time.Second)
	require.NoError(t, err, cluster.DumpDiagnostics())
	return cluster
}

func uidOwnedOutsideNode(t *testing.T, cluster *suite.StartedCluster, ingressNodeID uint64) string {
	t.Helper()
	slots := cluster.ManagerClient(t, ingressNodeID).MustSlots(t)
	for _, slot := range slots {
		if containsNodeID(slot.Assignment.DesiredPeers, ingressNodeID) || slot.HashSlots == nil {
			continue
		}
		for candidate := 0; candidate < 100_000; candidate++ {
			uid := fmt.Sprintf("directory-remote-owner-%d", candidate)
			hashSlot := routing.HashSlotForKey(uid, 16)
			for _, owned := range slot.HashSlots.Items {
				if hashSlot == owned {
					return uid
				}
			}
		}
	}
	t.Fatalf("no UID-owned Slot excludes ingress node %d\n%s", ingressNodeID, cluster.DumpDiagnostics())
	return ""
}

func uidOwnedBySlotLeaderOtherThan(t *testing.T, cluster *suite.StartedCluster, managerNodeID, excludedLeader uint64) string {
	t.Helper()
	slots := cluster.ManagerClient(t, managerNodeID).MustSlots(t)
	for _, slot := range slots {
		if slot.Runtime.LeaderID == 0 || slot.Runtime.LeaderID == excludedLeader || slot.HashSlots == nil {
			continue
		}
		for candidate := 0; candidate < 100_000; candidate++ {
			uid := fmt.Sprintf("directory-healthy-owner-%d", candidate)
			hashSlot := routing.HashSlotForKey(uid, 16)
			for _, owned := range slot.HashSlots.Items {
				if hashSlot == owned {
					return uid
				}
			}
		}
	}
	t.Fatalf("no UID Slot leader differs from excluded node %d\n%s", excludedLeader, cluster.DumpDiagnostics())
	return ""
}

func containsNodeID(values []uint64, want uint64) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func requireOrdinaryMessagePullEventually(t *testing.T, cluster *suite.StartedCluster, node *suite.StartedNode, uid, channelID, clientMsgNo string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last directoryMessagePage
	var lastErr error
	for {
		requestCtx, requestCancel := context.WithTimeout(ctx, 2*time.Second)
		var page directoryMessagePage
		_, err := suite.PostJSON(requestCtx, "http://"+node.APIAddr()+"/channel/messagesync", map[string]any{
			"login_uid": uid, "channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
			"start_message_seq": 1, "limit": 10, "pull_mode": 1,
		}, &page)
		requestCancel()
		if err == nil {
			last = page
			for _, message := range page.Messages {
				if message.ClientMsgNo == clientMsgNo {
					return
				}
			}
			lastErr = fmt.Errorf("message %s missing", clientMsgNo)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("ordinary pull through non-replica ingress did not converge: last=%+v err=%v\n%s", last, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func requireCMDMessageSyncEventually(t *testing.T, cluster *suite.StartedCluster, node *suite.StartedNode, uid, clientMsgNo string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last []directorySyncedMessage
	var lastErr error
	for {
		requestCtx, requestCancel := context.WithTimeout(ctx, 2*time.Second)
		var messages []directorySyncedMessage
		_, err := suite.PostJSON(requestCtx, "http://"+node.APIAddr()+"/message/sync", map[string]any{
			"uid": uid, "message_seq": 0, "limit": 10,
		}, &messages)
		requestCancel()
		if err == nil {
			last = messages
			for _, message := range messages {
				if message.ClientMsgNo == clientMsgNo {
					return
				}
			}
			lastErr = fmt.Errorf("CMD message %s missing", clientMsgNo)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("CMD sync through non-replica ingress did not converge: last=%+v err=%v\n%s", last, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func createDirectoryChannelLedOutsideNode(t *testing.T, cluster *suite.StartedCluster, origin *suite.StartedNode, senderUID, uid string, excludedNodeID uint64) (string, uint64, uint64) {
	t.Helper()
	prefix := fmt.Sprintf("directory-non-replica-%d", time.Now().UnixNano())
	for candidate := 0; candidate < 40; candidate++ {
		channelID := fmt.Sprintf("%s-%02d", prefix, candidate)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := suite.PostChannel(ctx, origin.APIAddr(), map[string]any{
			"channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
			"reset": 1, "subscribers": []string{senderUID},
		})
		if err != nil {
			cancel()
			require.NoError(t, err, cluster.DumpDiagnostics())
		}
		sendDirectoryMessage(t, ctx, cluster, *origin, senderUID, channelID, fmt.Sprintf("ordinary-route-probe-%02d", candidate))
		cancel()
		sourceMeta := requireChannelRuntimeMetaEventually(t, cluster, origin, channelID)
		if sourceMeta.Leader == excludedNodeID {
			continue
		}
		commandChannelID := runtimechannelid.ToCommandChannel(channelID)
		cmdCtx, cmdCancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmdResp, err := suite.PostMessageSendEventually(cmdCtx, origin.APIAddr(), map[string]any{
			"from_uid": senderUID, "channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
			"client_msg_no": fmt.Sprintf("command-route-probe-%02d", candidate), "sync_once": 1,
			"payload": base64.StdEncoding.EncodeToString([]byte("command-route-probe")),
		})
		cmdCancel()
		require.NoError(t, err, cluster.DumpDiagnostics())
		require.Equal(t, uint8(frame.ReasonSuccess), cmdResp.Reason, cluster.DumpDiagnostics())
		commandMeta := requireChannelRuntimeMetaEventually(t, cluster, origin, commandChannelID)
		if commandMeta.Leader == excludedNodeID {
			continue
		}

		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		_, err = suite.PostJSON(ctx, "http://"+origin.APIAddr()+"/channel/subscriber_add", map[string]any{
			"channel_id": channelID, "channel_type": frame.ChannelTypeGroup, "subscribers": []string{uid},
		}, nil)
		cancel()
		require.NoError(t, err, cluster.DumpDiagnostics())
		return channelID, sourceMeta.Leader, commandMeta.Leader
	}
	t.Fatalf("no ordinary/CMD channel pair found outside node %d\n%s", excludedNodeID, cluster.DumpDiagnostics())
	return "", 0, 0
}

func createDirectoryChannelsByLeader(t *testing.T, cluster *suite.StartedCluster, uid, senderUID string, perLeader int) map[uint64][]directoryChannel {
	t.Helper()
	origin := cluster.MustNode(1)
	channels := make(map[uint64][]directoryChannel, 3)
	prefix := fmt.Sprintf("directory-multi-%d", time.Now().UnixNano())
	for candidate := 0; candidate < 60 && directoryChannelCount(channels) < 3*perLeader; candidate++ {
		channelID := fmt.Sprintf("%s-%02d", prefix, candidate)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		require.NoError(t, suite.PostChannel(ctx, origin.APIAddr(), map[string]any{
			"channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
			"reset": 1, "subscribers": []string{senderUID},
		}), cluster.DumpDiagnostics())
		probe := sendDirectoryMessage(t, ctx, cluster, *origin, senderUID, channelID, fmt.Sprintf("leader-probe-%02d", candidate))
		cancel()

		meta := requireChannelRuntimeMetaEventually(t, cluster, origin, channelID)
		if meta.Leader == 0 || len(channels[meta.Leader]) >= perLeader {
			continue
		}

		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		_, err := suite.PostJSON(ctx, "http://"+origin.APIAddr()+"/channel/subscriber_add", map[string]any{
			"channel_id": channelID, "channel_type": frame.ChannelTypeGroup, "subscribers": []string{uid},
		}, nil)
		require.NoError(t, err, cluster.DumpDiagnostics())
		clientMsgNo := fmt.Sprintf("visible-%d-%d", meta.Leader, len(channels[meta.Leader])+1)
		visible := sendDirectoryMessage(t, ctx, cluster, *origin, senderUID, channelID, clientMsgNo)
		require.Greater(t, visible.MessageSeq, probe.MessageSeq, "accepted channel %s did not append after membership add", channelID)
		cancel()
		channels[meta.Leader] = append(channels[meta.Leader], directoryChannel{
			ID: channelID, Leader: meta.Leader, ClientMsgNo: clientMsgNo, MessageSeq: visible.MessageSeq,
		})
	}
	require.Len(t, channels, 3, "did not discover channels on every Leader\n%s", cluster.DumpDiagnostics())
	for leaderID, items := range channels {
		require.Len(t, items, perLeader, "Leader %d channel count", leaderID)
	}
	return channels
}

func sendDirectoryMessage(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, node suite.StartedNode, fromUID, channelID, clientMsgNo string) suite.MessageSendResponse {
	t.Helper()
	resp, err := suite.PostMessageSendEventually(ctx, node.APIAddr(), map[string]any{
		"from_uid": fromUID, "channel_id": channelID, "channel_type": frame.ChannelTypeGroup,
		"client_msg_no": clientMsgNo,
		"payload":       base64.StdEncoding.EncodeToString([]byte(clientMsgNo)),
	})
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, uint8(frame.ReasonSuccess), resp.Reason, cluster.DumpDiagnostics())
	require.NotZero(t, resp.MessageSeq)
	return resp
}

func requireChannelRuntimeMetaEventually(t *testing.T, cluster *suite.StartedCluster, node *suite.StartedNode, channelID string) channelRuntimeMetaItem {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var last channelRuntimeMetaItem
	var lastErr error
	for {
		query := url.Values{
			"exact": []string{"1"}, "channel_id": []string{channelID},
			"channel_type": []string{fmt.Sprint(frame.ChannelTypeGroup)},
		}
		var page channelRuntimeMetaPage
		requestCtx, requestCancel := context.WithTimeout(ctx, 2*time.Second)
		_, err := suite.GetJSON(requestCtx, "http://"+node.ManagerAddr()+"/manager/channel-runtime-meta?"+query.Encode(), &page)
		requestCancel()
		if err == nil {
			for _, item := range page.Items {
				if item.ChannelID == channelID && item.ChannelType == int64(frame.ChannelTypeGroup) {
					last = item
					if item.Leader != 0 && item.Status == "active" {
						return item
					}
					lastErr = fmt.Errorf("runtime meta = %+v, want active Leader", item)
				}
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("channel runtime meta for %s did not converge: last=%+v lastErr=%v\n%s", channelID, last, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func fetchMetricSamples(t *testing.T, node suite.StartedNode) []suite.MetricSample {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	samples, err := suite.FetchMetricSamples(ctx, node.APIAddr())
	require.NoError(t, err, node.DumpDiagnostics())
	return samples
}

func directoryChannelCount(channels map[uint64][]directoryChannel) int {
	total := 0
	for _, items := range channels {
		total += len(items)
	}
	return total
}

func sortedDirectoryLeaderIDs(channels map[uint64][]directoryChannel) []uint64 {
	ids := make([]uint64, 0, len(channels))
	for id := range channels {
		ids = append(ids, id)
	}
	if len(ids) == 3 {
		if ids[0] > ids[1] {
			ids[0], ids[1] = ids[1], ids[0]
		}
		if ids[1] > ids[2] {
			ids[1], ids[2] = ids[2], ids[1]
		}
		if ids[0] > ids[1] {
			ids[0], ids[1] = ids[1], ids[0]
		}
	}
	return ids
}
