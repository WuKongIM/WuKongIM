//go:build e2e

package message_retention

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestThreeNodeManagerRetentionForwardsAndSurvivesLeaderRestart(t *testing.T) {
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(suite.WithManagerHTTP())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	const (
		channelID   = "e2ev2-message-retention-group"
		channelType = frame.ChannelTypeGroup
	)
	seqs := appendThreeGroupMessages(t, cluster.MustNode(1), channelID, channelType)
	require.Len(t, seqs, 3)
	for _, node := range cluster.Nodes {
		requireManagerMessageSeqsEventually(t, cluster, node, channelID, channelType, []uint64{seqs[2], seqs[1], seqs[0]})
	}

	meta := requireRuntimeMetaEventually(t, cluster.MustNode(1), channelID, channelType)
	require.NotZero(t, meta.Leader, cluster.DumpDiagnostics())
	origin := firstNonLeaderNode(t, cluster, meta.Leader)

	retention := advanceMessageRetention(t, origin, channelID, channelType, seqs[1])
	require.Equal(t, "advanced", retention.Status, cluster.DumpDiagnostics())
	require.Equal(t, seqs[1], retention.AdvancedThroughSeq, cluster.DumpDiagnostics())
	require.Equal(t, seqs[1]+1, retention.MinAvailableSeq, cluster.DumpDiagnostics())

	for _, node := range cluster.Nodes {
		requireManagerMessageSeqsEventually(t, cluster, node, channelID, channelType, []uint64{seqs[2]})
	}

	restartNode(t, cluster, meta.Leader)
	requireManagerMessageSeqsEventually(t, cluster, *cluster.MustNode(meta.Leader), channelID, channelType, []uint64{seqs[2]})
}

func appendThreeGroupMessages(t *testing.T, node *suite.StartedNode, channelID string, channelType uint8) []uint64 {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, suite.PostChannel(ctx, node.APIAddr(), map[string]any{
		"channel_id":   channelID,
		"channel_type": channelType,
		"subscribers":  []string{"retention-sender", "retention-member-a", "retention-member-b"},
	}), node.DumpDiagnostics())

	seqs := make([]uint64, 0, 3)
	for i := 1; i <= 3; i++ {
		payload := fmt.Sprintf("retention payload %d", i)
		resp, err := suite.PostMessageSend(ctx, node.APIAddr(), map[string]any{
			"from_uid":      "retention-sender",
			"channel_id":    channelID,
			"channel_type":  channelType,
			"client_msg_no": fmt.Sprintf("retention-%d", i),
			"payload":       base64.StdEncoding.EncodeToString([]byte(payload)),
		})
		require.NoError(t, err, node.DumpDiagnostics())
		require.Equal(t, uint8(frame.ReasonSuccess), resp.Reason, node.DumpDiagnostics())
		require.NotZero(t, resp.MessageID)
		require.NotZero(t, resp.MessageSeq)
		seqs = append(seqs, resp.MessageSeq)
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

func restartNode(t *testing.T, cluster *suite.StartedCluster, nodeID uint64) {
	t.Helper()

	node := cluster.MustNode(nodeID)
	require.NotNil(t, node.Process)
	binaryPath := node.Process.BinaryPath
	require.NoError(t, node.Stop())

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
