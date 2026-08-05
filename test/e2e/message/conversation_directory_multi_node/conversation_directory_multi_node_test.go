//go:build e2e

package conversation_directory_multi_node

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const hydrationRemoteBatchMetric = "wukongim_conversation_hydration_remote_batch_calls"

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
		uid           = "directory-retry-user"
		senderUID     = "directory-retry-sender"
		stoppedLeader = uint64(2)
	)
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var lastPage suite.ConversationListPage
	var lastErr error
	for {
		page, err := suite.PostConversationRetry(ctx, node.APIAddr(), req)
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var last channelRuntimeMetaItem
	var lastErr error
	for {
		query := url.Values{"channel_id": []string{channelID}, "limit": []string{"10"}}
		var page channelRuntimeMetaPage
		_, err := suite.GetJSON(ctx, "http://"+node.ManagerAddr()+"/manager/channel-runtime-meta?"+query.Encode(), &page)
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
