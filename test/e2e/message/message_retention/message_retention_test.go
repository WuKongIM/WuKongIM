//go:build e2e

package message_retention

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	channel "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestThreeNodeManagerRetentionForwardsAndSurvivesLeaderRestart(t *testing.T) {
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(
		suite.WithManagerHTTP(),
		retentionGCConfig(1),
		retentionGCConfig(2),
		retentionGCConfig(3),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	const (
		channelID   = "e2e-message-retention-group"
		channelType = frame.ChannelTypeGroup
	)
	messages := appendGroupMessages(t, cluster.MustNode(1), channelID, channelType, 3)
	require.Len(t, messages, 3)
	seqs := sentMessageSeqs(messages)
	for _, node := range cluster.Nodes {
		requireManagerMessageSeqsEventually(t, cluster, node, channelID, channelType, []uint64{seqs[2], seqs[1], seqs[0]})
	}

	meta := requireRuntimeMetaEventually(t, cluster.MustNode(1), channelID, channelType)
	require.NotZero(t, meta.Leader, cluster.DumpDiagnostics())
	origin := firstNonLeaderNode(t, cluster, meta.Leader)

	retention := advanceMessageRetentionEventually(t, cluster, origin, channelID, channelType, seqs[1])
	require.Equal(t, "advanced", retention.Status, cluster.DumpDiagnostics())
	require.Equal(t, seqs[1], retention.AdvancedThroughSeq, cluster.DumpDiagnostics())
	require.Equal(t, seqs[1]+1, retention.MinAvailableSeq, cluster.DumpDiagnostics())

	for _, node := range cluster.Nodes {
		requireManagerMessageSeqsEventually(t, cluster, node, channelID, channelType, []uint64{seqs[2]})
	}

	time.Sleep(1500 * time.Millisecond)
	restartNodeWithInspection(t, cluster, meta.Leader, func(node suite.StartedNode) {
		requirePhysicalRetentionApplied(t, node, channelID, channelType, messages[:2], messages[2])
	})
	requireManagerMessageSeqsEventually(t, cluster, *cluster.MustNode(meta.Leader), channelID, channelType, []uint64{seqs[2]})

	afterRestart := appendGroupMessages(t, cluster.MustNode(1), channelID, channelType, 1)[0]
	require.Greater(t, afterRestart.Seq, seqs[2], cluster.DumpDiagnostics())
	for _, node := range cluster.Nodes {
		requireManagerMessageSeqsEventually(t, cluster, node, channelID, channelType, []uint64{afterRestart.Seq, seqs[2]})
	}
}

func retentionGCConfig(nodeID uint64) suite.Option {
	return suite.WithNodeConfigOverrides(nodeID, map[string]string{
		"WK_CHANNEL_MESSAGE_RETENTION_PHYSICAL_GC_ENABLE": "true",
		"WK_CHANNEL_MESSAGE_RETENTION_SCAN_INTERVAL":      "100ms",
		"WK_CHANNEL_MESSAGE_RETENTION_CHANNEL_BATCH_SIZE": "16",
		"WK_CHANNEL_MESSAGE_RETENTION_MAX_TRIM_MESSAGES":  "10",
	})
}

type sentMessage struct {
	ID  uint64
	Seq uint64
}

func appendGroupMessages(t *testing.T, node *suite.StartedNode, channelID string, channelType uint8, count int) []sentMessage {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, suite.PostChannel(ctx, node.APIAddr(), map[string]any{
		"channel_id":   channelID,
		"channel_type": channelType,
		"subscribers":  []string{"retention-sender", "retention-member-a", "retention-member-b"},
	}), node.DumpDiagnostics())

	messages := make([]sentMessage, 0, count)
	for i := 1; i <= count; i++ {
		clientMsgNo := fmt.Sprintf("retention-%d-%d", time.Now().UnixNano(), i)
		payload := fmt.Sprintf("retention payload %d", i)
		resp, err := suite.PostMessageSend(ctx, node.APIAddr(), map[string]any{
			"from_uid":      "retention-sender",
			"channel_id":    channelID,
			"channel_type":  channelType,
			"client_msg_no": clientMsgNo,
			"payload":       base64.StdEncoding.EncodeToString([]byte(payload)),
		})
		require.NoError(t, err, node.DumpDiagnostics())
		require.Equal(t, uint8(frame.ReasonSuccess), resp.Reason, node.DumpDiagnostics())
		require.NotZero(t, resp.MessageID)
		require.NotZero(t, resp.MessageSeq)
		messages = append(messages, sentMessage{ID: uint64(resp.MessageID), Seq: resp.MessageSeq})
	}
	return messages
}

func sentMessageSeqs(messages []sentMessage) []uint64 {
	seqs := make([]uint64, 0, len(messages))
	for _, msg := range messages {
		seqs = append(seqs, msg.Seq)
	}
	return seqs
}

type managerMessagePage struct {
	Items []managerMessageItem `json:"items"`
}

type managerMessageItem struct {
	MessageSeq  uint64 `json:"message_seq"`
	ClientMsgNo string `json:"client_msg_no"`
}

func managerMessageSeqs(ctx context.Context, node suite.StartedNode, channelID string, channelType uint8) ([]uint64, error) {
	var page managerMessagePage
	query := url.Values{}
	query.Set("channel_id", channelID)
	query.Set("channel_type", fmt.Sprintf("%d", channelType))
	query.Set("limit", "10")
	_, err := suite.GetJSON(ctx, "http://"+node.ManagerAddr()+"/manager/messages?"+query.Encode(), &page)
	if err != nil {
		return nil, err
	}
	seqs := make([]uint64, 0, len(page.Items))
	for _, item := range page.Items {
		seqs = append(seqs, item.MessageSeq)
	}
	return seqs, nil
}

func requireManagerMessageSeqsEventually(t *testing.T, cluster *suite.StartedCluster, node suite.StartedNode, channelID string, channelType uint8, want []uint64) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var lastSeqs []uint64
	var lastErr error
	for {
		seqs, err := managerMessageSeqs(ctx, node, channelID, channelType)
		if err == nil {
			lastSeqs = seqs
			if equalSeqs(seqs, want) {
				return
			}
			lastErr = fmt.Errorf("seqs = %v, want %v", seqs, want)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("manager messages on node %d timed out: lastSeqs=%v lastErr=%v\n%s", node.Spec.ID, lastSeqs, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func equalSeqs(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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

func runtimeMeta(ctx context.Context, node *suite.StartedNode, channelID string, channelType uint8) (channelRuntimeMetaItem, error) {
	var page channelRuntimeMetaPage
	query := url.Values{}
	query.Set("channel_id", channelID)
	query.Set("limit", "10")
	_, err := suite.GetJSON(ctx, "http://"+node.ManagerAddr()+"/manager/channel-runtime-meta?"+query.Encode(), &page)
	if err != nil {
		return channelRuntimeMetaItem{}, err
	}
	for _, item := range page.Items {
		if item.ChannelID == channelID && item.ChannelType == int64(channelType) {
			return item, nil
		}
	}
	return channelRuntimeMetaItem{}, fmt.Errorf("runtime meta for %s/%d not found in %+v", channelID, channelType, page.Items)
}

func requireRuntimeMetaEventually(t *testing.T, node *suite.StartedNode, channelID string, channelType uint8) channelRuntimeMetaItem {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var last channelRuntimeMetaItem
	var lastErr error
	for {
		meta, err := runtimeMeta(ctx, node, channelID, channelType)
		if err == nil {
			last = meta
			if meta.Leader != 0 && meta.Status == "active" {
				return meta
			}
			lastErr = fmt.Errorf("runtime meta = %+v, want active leader", meta)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("runtime meta timed out: last=%+v lastErr=%v\n%s", last, lastErr, node.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

type retentionResponse struct {
	Status             string `json:"status"`
	AdvancedThroughSeq uint64 `json:"advanced_through_seq"`
	MinAvailableSeq    uint64 `json:"min_available_seq"`
	BlockedReason      string `json:"blocked_reason"`
}

func advanceMessageRetention(t *testing.T, node *suite.StartedNode, channelID string, channelType uint8, throughSeq uint64) retentionResponse {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out retentionResponse
	_, err := suite.PostJSON(ctx, "http://"+node.ManagerAddr()+"/manager/messages/retention", map[string]any{
		"channel_id":   channelID,
		"channel_type": channelType,
		"through_seq":  throughSeq,
	}, &out)
	require.NoError(t, err, node.DumpDiagnostics())
	return out
}

func advanceMessageRetentionEventually(t *testing.T, cluster *suite.StartedCluster, node *suite.StartedNode, channelID string, channelType uint8, throughSeq uint64) retentionResponse {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var last retentionResponse
	for {
		last = advanceMessageRetention(t, node, channelID, channelType, throughSeq)
		if last.Status == "advanced" {
			return last
		}
		select {
		case <-ctx.Done():
			t.Fatalf("advance retention timed out: last=%+v\n%s", last, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func firstNonLeaderNode(t *testing.T, cluster *suite.StartedCluster, leaderID uint64) *suite.StartedNode {
	t.Helper()
	for i := range cluster.Nodes {
		if cluster.Nodes[i].Spec.ID != leaderID {
			return &cluster.Nodes[i]
		}
	}
	t.Fatalf("no non-leader node found for leader %d", leaderID)
	return nil
}

func restartNodeWithInspection(t *testing.T, cluster *suite.StartedCluster, nodeID uint64, inspect func(suite.StartedNode)) {
	t.Helper()

	node := cluster.MustNode(nodeID)
	require.NotNil(t, node.Process)
	binaryPath := node.Process.BinaryPath
	require.NoError(t, node.Stop())
	if inspect != nil {
		inspect(*node)
	}

	process := &suite.NodeProcess{
		Spec:       node.Spec,
		BinaryPath: binaryPath,
	}
	require.NoError(t, process.Start())
	node.Process = process

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
}

func requirePhysicalRetentionApplied(t *testing.T, node suite.StartedNode, channelID string, channelType uint8, retained []sentMessage, suffix sentMessage) {
	t.Helper()

	factory := channelstore.NewMessageDBFactory(filepath.Join(node.Spec.DataDir, "messages"))
	defer func() { require.NoError(t, factory.Close()) }()
	id := channel.ChannelID{ID: channelID, Type: channelType}
	store, err := factory.ChannelStore(channel.ChannelKeyForID(id), id)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close()) }()
	lookup, ok := store.(channelstore.MessageLookup)
	require.True(t, ok)

	ctx := context.Background()
	for _, msg := range retained {
		read, err := store.ReadCommitted(ctx, channelstore.ReadCommittedRequest{FromSeq: msg.Seq, MaxSeq: msg.Seq, Limit: 1, MaxBytes: 1024})
		require.NoError(t, err)
		require.Empty(t, read.Messages, "retained seq %d should be physically deleted on node %d", msg.Seq, node.Spec.ID)
		_, found, err := lookup.LookupMessageByID(ctx, msg.ID)
		require.NoError(t, err)
		require.False(t, found, "retained message id %d should be removed from index on node %d", msg.ID, node.Spec.ID)
	}

	read, err := store.ReadCommitted(ctx, channelstore.ReadCommittedRequest{FromSeq: suffix.Seq, MaxSeq: suffix.Seq, Limit: 1, MaxBytes: 1024})
	require.NoError(t, err)
	require.Len(t, read.Messages, 1, "suffix seq %d should remain on node %d", suffix.Seq, node.Spec.ID)
	require.Equal(t, suffix.ID, read.Messages[0].MessageID)
	foundSuffix, found, err := lookup.LookupMessageByID(ctx, suffix.ID)
	require.NoError(t, err)
	require.True(t, found, "suffix message id %d should remain indexed on node %d", suffix.ID, node.Spec.ID)
	require.Equal(t, suffix.Seq, foundSuffix.MessageSeq)
}
