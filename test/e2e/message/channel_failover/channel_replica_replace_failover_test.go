//go:build e2e

package channel_failover

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const (
	// replicaReplaceVerifyFailpoint holds a replica replacement at VerifyMembership.
	replicaReplaceVerifyFailpoint = "wkChannelReplicaReplaceVerifyMembershipError"
	// replicaReplaceFailoverWriteBudget bounds write recovery after the leader stops.
	replicaReplaceFailoverWriteBudget = 30 * time.Second
)

// replicaReplaceFailoverReport is the repeatable artifact of the Issue #1077 gate.
type replicaReplaceFailoverReport struct {
	ChannelID            string   `json:"channel_id"`
	ChannelType          uint8    `json:"channel_type"`
	StoppedLeader        uint64   `json:"stopped_leader"`
	ReplacedSource       uint64   `json:"replaced_source"`
	Target               uint64   `json:"target"`
	TaskID               string   `json:"task_id"`
	HeldPhase            string   `json:"held_phase"`
	FailpointHits        int      `json:"failpoint_hits"`
	WriteRecoveryMS      int64    `json:"write_recovery_ms"`
	WriteRecoveryBudgetS float64  `json:"write_recovery_budget_s"`
	LeaderAfterFailover  uint64   `json:"leader_after_failover"`
	ReplicasAfter        []uint64 `json:"replicas_after"`
	ISRAfter             []uint64 `json:"isr_after"`
	RejoinedWriteSeq     uint64   `json:"rejoined_write_seq"`
	FinalHistory         []string `json:"final_history"`
}

// TestChannelLeaderFailoverDuringPromotedReplicaReplace covers Issue #1077: a
// replica replacement already past promotion must not block leader failover.
func TestChannelLeaderFailoverDuringPromotedReplicaReplace(t *testing.T) {
	if os.Getenv("WK_E2E_GOFAIL_CHANNEL_MIGRATION") != "1" {
		t.Skip("set WK_E2E_GOFAIL_CHANNEL_MIGRATION=1 with a gofail-enabled WK_E2E_BINARY")
	}

	failpoints := map[uint64]suite.GofailEndpoint{}
	opts := fastRecoveryOptionsForNodes(4, map[string]string{
		"WK_CLUSTER_CHANNEL_REPLICA_N": "3",
	})
	for nodeID := uint64(1); nodeID <= 4; nodeID++ {
		endpoint := suite.ReserveGofailEndpoint(t)
		failpoints[nodeID] = endpoint
		opts = append(opts, suite.WithNodeEnv(nodeID, endpoint.Env()))
		t.Logf("node %d gofail endpoint %s", nodeID, endpoint.Addr)
	}
	opts = append(opts, suite.WithDynamicJoinToken("join-secret"))
	if dir := os.Getenv("WK_E2E_REPLICA_REPLACE_LOG_ROOT"); dir != "" {
		// Optional: retain node app logs after the test for failure analysis.
		opts = append(opts, suite.WithNodeLogRootDir(dir))
	}

	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(opts...)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitHTTPReady(ctx), cluster.DumpDiagnostics())

	manager := cluster.ManagerClient(t, 1)
	node4 := cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{NodeID: 4, JoinToken: "join-secret"})
	manager.EventuallyNodeReadiness(t, node4.Spec.ID, true, 20*time.Second)

	candidate := createFollowerRepairCandidate(t, cluster)
	manager.MustActivateNode(t, node4.Spec.ID)
	requireNodeSchedulableEventually(t, cluster, cluster.MustNode(1), node4.Spec.ID)
	t.Logf("replica replace candidate: channel=%s/%d leader=%d slotLeader=%d replicas=%v source=%d target=%d",
		candidate.ChannelID, candidate.ChannelType, candidate.Leader, candidate.SlotLeader, candidate.Replicas, candidate.StoppedFollower, candidate.SpareNode)

	for nodeID, endpoint := range failpoints {
		listCtx, cancelList := context.WithTimeout(context.Background(), 10*time.Second)
		body, err := endpoint.WaitListed(listCtx, replicaReplaceVerifyFailpoint)
		cancelList()
		require.NoError(t, err, "node %d gofail body:\n%s\n%s", nodeID, body, cluster.DumpDiagnostics())
		enableCtx, cancelEnable := context.WithTimeout(context.Background(), 10*time.Second)
		err = endpoint.Enable(enableCtx, replicaReplaceVerifyFailpoint, `return("e2e-hold")`)
		cancelEnable()
		require.NoError(t, err, cluster.DumpDiagnostics())
	}
	defer disableReplicaReplaceFailpoints(failpoints, 0)

	startReplicaReplace(t, cluster, cluster.MustNode(1), candidate)
	held := requireReplicaReplaceHeldAtVerifyMembership(t, cluster, candidate, failpoints, 45*time.Second)

	survivor := firstSurvivingNode(cluster, candidate.Leader)
	require.NoError(t, cluster.MustNode(candidate.Leader).Stop(), cluster.DumpDiagnostics())
	stoppedAt := time.Now()
	// The dead leader now fails VerifyMembership on its own, exactly as in Issue #1077.
	disableReplicaReplaceFailpoints(failpoints, candidate.Leader)

	afterStop := sendGroupMessageWithin(t, survivor, candidate.ChannelID, candidate.ChannelType, "replica-replace-after-leader-stop", replicaReplaceFailoverWriteBudget)
	writeRecovery := time.Since(stoppedAt)
	require.Greater(t, afterStop.Seq, candidate.Pre.Seq, cluster.DumpDiagnostics())
	requireIdenticalHistoryEverywhereEventually(t, cluster, candidate, candidate.Leader, candidate.Pre, afterStop)
	metaCtx, cancelMeta := context.WithTimeout(context.Background(), 5*time.Second)
	meta, err := channelRuntimeMeta(metaCtx, survivor, candidate.ChannelID, candidate.ChannelType)
	cancelMeta()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.NotEqual(t, candidate.Leader, meta.Leader, "meta=%+v\n%s", meta, cluster.DumpDiagnostics())
	// The held task must be finished. A later follower repair of the dead
	// leader may legitimately re-add the replaced source as the only spare.
	require.NotEqual(t, held.TaskID, meta.ActiveTaskID, "held task still active: meta=%+v\n%s", meta, cluster.DumpDiagnostics())
	require.True(t, uint64InList(meta.Replicas, candidate.SpareNode), "target missing: meta=%+v", meta)

	// The former leader must rejoin without divergence and accept writes again.
	require.NoError(t, cluster.StartStoppedNode(candidate.Leader), cluster.DumpDiagnostics())
	restartCtx, cancelRestart := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelRestart()
	require.NoError(t, cluster.WaitHTTPReady(restartCtx), cluster.DumpDiagnostics())
	rejoined := cluster.MustNode(candidate.Leader)
	afterRejoin := sendGroupMessageWithin(t, rejoined, candidate.ChannelID, candidate.ChannelType, "replica-replace-after-rejoin", 20*time.Second)
	require.Greater(t, afterRejoin.Seq, afterStop.Seq, cluster.DumpDiagnostics())
	history := requireIdenticalHistoryEverywhereEventually(t, cluster, candidate, 0, candidate.Pre, afterStop, afterRejoin)

	writeReplicaReplaceFailoverReport(t, replicaReplaceFailoverReport{
		ChannelID:            candidate.ChannelID,
		ChannelType:          candidate.ChannelType,
		StoppedLeader:        candidate.Leader,
		ReplacedSource:       candidate.StoppedFollower,
		Target:               candidate.SpareNode,
		TaskID:               held.TaskID,
		HeldPhase:            held.Phase,
		FailpointHits:        held.hits,
		WriteRecoveryMS:      writeRecovery.Milliseconds(),
		WriteRecoveryBudgetS: replicaReplaceFailoverWriteBudget.Seconds(),
		LeaderAfterFailover:  meta.Leader,
		ReplicasAfter:        meta.Replicas,
		ISRAfter:             meta.ISR,
		RejoinedWriteSeq:     afterRejoin.Seq,
		FinalHistory:         history,
	})
}

// requireIdenticalHistoryEverywhereEventually reads committed history through
// the public routed /channel/messagesync from every running node except skip.
// Each node must return the same history containing every message exactly once.
// Manager message reads are node-local and cannot serve non-replica nodes.
func requireIdenticalHistoryEverywhereEventually(t *testing.T, cluster *suite.StartedCluster, candidate followerRepairCandidate, skip uint64, msgs ...sentMessage) []string {
	t.Helper()

	var reference []string
	var referenceNode uint64
	for i := range cluster.Nodes {
		node := &cluster.Nodes[i]
		if node.Spec.ID == skip {
			continue
		}
		history := requireHistoryContainsOnceEventually(t, cluster, node, candidate, msgs)
		if reference == nil {
			reference, referenceNode = history, node.Spec.ID
			continue
		}
		require.Equal(t, reference, history, "history on node %d differs from node %d\n%s", node.Spec.ID, referenceNode, cluster.DumpDiagnostics())
	}
	require.NotNil(t, reference, "no running node checked")
	return reference
}

type syncedMessage struct {
	MessageID   int64  `json:"message_id"`
	MessageSeq  uint64 `json:"message_seq"`
	ClientMsgNo string `json:"client_msg_no"`
}

func requireHistoryContainsOnceEventually(t *testing.T, cluster *suite.StartedCluster, node *suite.StartedNode, candidate followerRepairCandidate, msgs []sentMessage) []string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var lastErr error
	for {
		var page struct {
			Messages []syncedMessage `json:"messages"`
		}
		reqCtx, cancelReq := context.WithTimeout(ctx, 3*time.Second)
		_, err := suite.PostJSON(reqCtx, "http://"+node.APIAddr()+"/channel/messagesync", map[string]any{
			"login_uid":    "channel-ha-sender",
			"channel_id":   candidate.ChannelID,
			"channel_type": candidate.ChannelType,
			"limit":        100,
		}, &page)
		cancelReq()
		if err == nil {
			if err = historyContainsOnce(page.Messages, msgs); err == nil {
				history := make([]string, 0, len(page.Messages))
				for _, msg := range page.Messages {
					history = append(history, fmt.Sprintf("%d:%d:%s", msg.MessageSeq, msg.MessageID, msg.ClientMsgNo))
				}
				return history
			}
		}
		lastErr = err
		select {
		case <-ctx.Done():
			t.Fatalf("node %d history for %s/%d incomplete: %v\n%s", node.Spec.ID, candidate.ChannelID, candidate.ChannelType, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func historyContainsOnce(history []syncedMessage, msgs []sentMessage) error {
	for _, want := range msgs {
		found := 0
		for _, got := range history {
			if got.ClientMsgNo != want.ClientMsgNo {
				continue
			}
			found++
			if uint64(got.MessageID) != want.ID || got.MessageSeq != want.Seq {
				return fmt.Errorf("message %s = id %d seq %d, want id %d seq %d", want.ClientMsgNo, got.MessageID, got.MessageSeq, want.ID, want.Seq)
			}
		}
		if found != 1 {
			return fmt.Errorf("message %s found %d times in %d history rows, want 1", want.ClientMsgNo, found, len(history))
		}
	}
	return nil
}

type heldReplicaReplace struct {
	channelMigrationItem
	hits int
}

func startReplicaReplace(t *testing.T, cluster *suite.StartedCluster, node *suite.StartedNode, candidate followerRepairCandidate) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	body, err := suite.PostJSON(ctx, "http://"+node.ManagerAddr()+"/manager/channel-migrations/replica-replace", map[string]any{
		"channel_id":   candidate.ChannelID,
		"channel_type": candidate.ChannelType,
		"source_node":  candidate.StoppedFollower,
		"target_node":  candidate.SpareNode,
	}, nil)
	require.NoError(t, err, "body=%s\n%s", body, cluster.DumpDiagnostics())
}

// requireReplicaReplaceHeldAtVerifyMembership proves promotion committed and the failpoint fired.
func requireReplicaReplaceHeldAtVerifyMembership(t *testing.T, cluster *suite.StartedCluster, candidate followerRepairCandidate, failpoints map[uint64]suite.GofailEndpoint, timeout time.Duration) heldReplicaReplace {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var (
		last    []channelMigrationItem
		lastErr error
	)
	for {
		items, err := activeChannelMigrations(ctx, cluster.MustNode(1), candidate.ChannelID, candidate.ChannelType)
		if err != nil {
			lastErr = err
		} else {
			last = items
			if item, ok := findExpectedReplicaRepair(items, candidate); ok && item.Phase == "verify_membership" {
				hits := 0
				for _, endpoint := range failpoints {
					countCtx, cancelCount := context.WithTimeout(ctx, 2*time.Second)
					count, countErr := endpoint.Count(countCtx, replicaReplaceVerifyFailpoint)
					cancelCount()
					if countErr == nil {
						hits += count
					}
				}
				if hits > 0 {
					return heldReplicaReplace{channelMigrationItem: item, hits: hits}
				}
				lastErr = fmt.Errorf("task at verify_membership but failpoint not hit yet")
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("replica replace did not hold at verify_membership: last=%+v lastErr=%v\n%s", last, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

// disableReplicaReplaceFailpoints clears the hold on every node except skip.
func disableReplicaReplaceFailpoints(failpoints map[uint64]suite.GofailEndpoint, skip uint64) {
	for nodeID, endpoint := range failpoints {
		if nodeID == skip {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = endpoint.Disable(ctx, replicaReplaceVerifyFailpoint)
		cancel()
	}
}

func writeReplicaReplaceFailoverReport(t *testing.T, report replicaReplaceFailoverReport) {
	t.Helper()

	data, err := json.MarshalIndent(report, "", "  ")
	require.NoError(t, err)
	t.Logf("replica replace failover report:\n%s", data)
	path := os.Getenv("WK_E2E_REPLICA_REPLACE_FAILOVER_REPORT")
	if path == "" {
		return
	}
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, append(data, '\n'), 0o644))
}
