//go:build e2e

package channel_failover

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

// channelPromiseActivatedEvent is the Controller leader's one-way activation log.
const channelPromiseActivatedEvent = "cluster.channel_promise.activated"

// The distributed log sequence is message_seq. After the cluster activates
// authority promises, a Channel leader change must not consume a sequence:
// the client-visible history stays exactly 1..N across a leader kill and the
// old leader's restart. Before activation the in-log barrier left a hidden gap.
// Set WK_E2E_CHANNEL_SEQ_REPORT to an absolute path to keep JSON evidence.
func TestChannelLeaderChangeKeepsMessageSeqContiguous(t *testing.T) {
	opts := fastRecoveryOptions()
	if dir := os.Getenv("WK_E2E_CHANNEL_SEQ_LOG_ROOT"); dir != "" {
		// Optional: retain node app logs after the test for failure analysis.
		opts = append(opts, suite.WithNodeLogRootDir(dir))
	}
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(opts...)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitHTTPReady(ctx), cluster.DumpDiagnostics())
	activatedBy := requireChannelPromiseActivatedEventually(t, cluster, 30*time.Second)

	channelsByLeader := createChannelsLedByEveryNode(t, cluster)
	killedNode := sortedLeaderIDs(channelsByLeader)[0]
	affected := channelsByLeader[killedNode]
	survivor := firstSurvivingNode(cluster, killedNode)
	sent := []sentMessage{affected.Pre, sendGroupMessage(t, cluster.MustNode(killedNode), affected.ChannelID, affected.ChannelType, "before-kill")}

	require.NoError(t, cluster.MustNode(killedNode).Stop(), cluster.DumpDiagnostics())
	requireNodeUnschedulableEventually(t, cluster, survivor, killedNode)
	sent = append(sent, sendGroupMessageWithin(t, survivor, affected.ChannelID, affected.ChannelType, "after-failover", 30*time.Second))
	afterFailover := requireContiguousHistory(t, survivor, affected, sent)

	require.NoError(t, cluster.StartStoppedNode(killedNode), cluster.DumpDiagnostics())
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitHTTPReady(ctx), cluster.DumpDiagnostics())
	requireNodeSchedulableEventually(t, cluster, survivor, killedNode)
	sent = append(sent, sendGroupMessageWithin(t, cluster.MustNode(killedNode), affected.ChannelID, affected.ChannelType, "after-restart", 20*time.Second))
	afterRestart := requireContiguousHistory(t, survivor, affected, sent)

	if path := os.Getenv("WK_E2E_CHANNEL_SEQ_REPORT"); path != "" {
		report := map[string]any{
			"scenario":          "channel_leader_change_message_seq_contiguity",
			"activated_by_node": activatedBy,
			"channel_id":        affected.ChannelID,
			"killed_leader":     killedNode,
			"after_failover":    afterFailover,
			"after_restart":     afterRestart,
		}
		encoded, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, encoded, 0o644))
	}
}

// requireChannelPromiseActivatedEventually waits for the activation event in
// any node's app log and returns the node that committed it.
func requireChannelPromiseActivatedEventually(t *testing.T, cluster *suite.StartedCluster, timeout time.Duration) uint64 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		for _, nodeID := range clusterNodeIDs(cluster) {
			if appLogHasEvent(t, cluster.MustNode(nodeID), channelPromiseActivatedEvent) {
				return nodeID
			}
		}
		if time.Now().After(deadline) {
			for _, nodeID := range clusterNodeIDs(cluster) {
				t.Logf("node %d channel promise log: %v", nodeID, appLogLines(cluster.MustNode(nodeID), "channel_promise", 5))
			}
			t.Fatalf("no node logged %s within %s\n%s", channelPromiseActivatedEvent, timeout, cluster.DumpDiagnostics())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func appLogHasEvent(t *testing.T, node *suite.StartedNode, event string) bool {
	t.Helper()
	return len(appLogLines(node, `"event": "`+event+`"`, 1)) > 0
}

// appLogLines returns the last limit app-log lines containing substring.
func appLogLines(node *suite.StartedNode, substring string, limit int) []string {
	file, err := os.Open(filepath.Join(node.Spec.LogDir, "app.log"))
	if err != nil {
		return nil
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(io.LimitReader(file, 64<<20))
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), substring) {
			lines = append(lines, scanner.Text())
			if len(lines) > limit {
				lines = lines[1:]
			}
		}
	}
	return lines
}

// requireContiguousHistory reads the complete client-visible history and
// requires message_seq to be exactly 1..N with every acknowledged message at
// its acknowledged sequence. It returns the observed sequences.
func requireContiguousHistory(t *testing.T, node *suite.StartedNode, channel failoverChannel, sent []sentMessage) []uint64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var page struct {
		More     int `json:"more"`
		Messages []struct {
			Seq    uint64 `json:"message_seq"`
			Client string `json:"client_msg_no"`
		} `json:"messages"`
	}
	_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+"/channel/messagesync", map[string]any{
		"login_uid": "channel-ha-member-a", "channel_id": channel.ChannelID, "channel_type": channel.ChannelType,
		"start_message_seq": 0, "end_message_seq": 0, "pull_mode": 0, "limit": 100,
	}, &page)
	require.NoError(t, err, node.DumpDiagnostics())
	require.Zero(t, page.More, "history must fit one page")
	bySeq := make(map[string]uint64, len(page.Messages))
	seqs := make([]uint64, 0, len(page.Messages))
	for _, message := range page.Messages {
		bySeq[message.Client] = message.Seq
		seqs = append(seqs, message.Seq)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for index, seq := range seqs {
		require.Equal(t, uint64(index+1), seq, "message_seq must be contiguous across leader changes: %v", seqs)
	}
	for _, message := range sent {
		require.Equal(t, message.Seq, bySeq[message.ClientMsgNo], "acknowledged %s", message.ClientMsgNo)
	}
	return seqs
}
