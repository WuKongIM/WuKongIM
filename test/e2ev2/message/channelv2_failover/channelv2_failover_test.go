//go:build e2e

package channelv2_failover

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/routing"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2ev2/suite"
	"github.com/stretchr/testify/require"
)

func TestChannelV2ThreeNodeLeaderFailoverAfterNodeKill(t *testing.T) {
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(fastRecoveryOptions()...)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	channelsByLeader := createChannelsLedByEveryNode(t, cluster)
	leaders := sortedLeaderIDs(channelsByLeader)
	require.Len(t, leaders, 3, cluster.DumpDiagnostics())

	killedNode := leaders[0]
	affected := channelsByLeader[killedNode]
	unaffected := channelsByLeader[leaders[1]]
	survivor := firstSurvivingNode(cluster, killedNode)

	require.NoError(t, cluster.MustNode(killedNode).Stop(), cluster.DumpDiagnostics())

	unaffectedDuringRepair := sendGroupMessage(t, survivor, unaffected.ChannelID, unaffected.ChannelType, "during-repair")
	require.Greater(t, unaffectedDuringRepair.Seq, unaffected.Pre.Seq, cluster.DumpDiagnostics())

	requireNodeUnschedulableEventually(t, cluster, survivor, killedNode)
	afterFailover := sendGroupMessageWithin(t, survivor, affected.ChannelID, affected.ChannelType, "after-failover", 30*time.Second)
	require.Greater(t, afterFailover.Seq, affected.Pre.Seq, cluster.DumpDiagnostics())

	requireMessageOnceEventually(t, cluster, survivor, affected.ChannelID, affected.ChannelType, affected.Pre)
	requireMessageOnceEventually(t, cluster, survivor, affected.ChannelID, affected.ChannelType, afterFailover)
}

func TestChannelV2NewPlacementFailsClosedWhenReplicaCountUnavailable(t *testing.T) {
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(fastRecoveryOptions()...)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	const stoppedNode = uint64(3)
	survivor := cluster.MustNode(1)
	channelID := fmt.Sprintf("e2ev2-cv2-placement-unavailable-%d", time.Now().UnixNano())
	createGroupChannel(t, survivor, channelID, frame.ChannelTypeGroup)

	require.NoError(t, cluster.MustNode(stoppedNode).Stop(), cluster.DumpDiagnostics())
	requireNodeUnschedulableEventually(t, cluster, survivor, stoppedNode)

	_, err := postGroupMessage(context.Background(), survivor, channelID, frame.ChannelTypeGroup, "placement-unavailable")
	require.Error(t, err, cluster.DumpDiagnostics())
	requireRetryablePlacementError(t, err)

	require.NoError(t, cluster.StartStoppedNode(stoppedNode), cluster.DumpDiagnostics())
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	requireNodeSchedulableEventually(t, cluster, survivor, stoppedNode)

	recovered := sendGroupMessage(t, survivor, channelID, frame.ChannelTypeGroup, "placement-recovered")
	require.NotZero(t, recovered.Seq, cluster.DumpDiagnostics())
}

func fastRecoveryOptions() []suite.Option {
	opts := []suite.Option{suite.WithManagerHTTP()}
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		opts = append(opts, suite.WithNodeConfigOverrides(nodeID, map[string]string{
			"WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL":         "500ms",
			"WK_CLUSTER_NODE_HEALTH_REPORT_TTL":              "2s",
			"WK_CHANNEL_MIGRATION_ENABLE":                    "true",
			"WK_CHANNEL_MIGRATION_SCAN_INTERVAL":             "100ms",
			"WK_CHANNEL_MIGRATION_SCAN_LIMIT":                "16",
			"WK_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK":        "2",
			"WK_CHANNEL_MIGRATION_MAX_TASKS_PER_TICK":        "2",
			"WK_CHANNEL_MIGRATION_TASK_LIMIT":                "2",
			"WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT":       "1ms",
			"WK_CLUSTER_CHANNEL_STORE_APPEND_BATCH_MAX_WAIT": "1ms",
		}))
	}
	return opts
}

type failoverChannel struct {
	ChannelID   string
	ChannelType uint8
	Leader      uint64
	Pre         sentMessage
}

type sentMessage struct {
	ID          uint64
	Seq         uint64
	ClientMsgNo string
}

func createChannelsLedByEveryNode(t *testing.T, cluster *suite.StartedCluster) map[uint64]failoverChannel {
	t.Helper()

	out := make(map[uint64]failoverChannel)
	origin := cluster.MustNode(1)
	slots := cluster.ManagerClient(t, origin.Spec.ID).MustSlots(t)
	for _, slot := range slots {
		leader := slot.Assignment.PreferredLeaderID
		if leader == 0 {
			leader = slot.Runtime.PreferredLeaderID
		}
		if leader == 0 {
			continue
		}
		if _, ok := out[leader]; ok {
			continue
		}
		channelID, ok := channelIDForPhysicalSlot(slots, slot, fmt.Sprintf("e2ev2-cv2-failover-%d-%d", time.Now().UnixNano(), leader))
		require.True(t, ok, "channel id for slot %d leader %d not found in slots=%+v", slot.SlotID, leader, slots)
		createGroupChannel(t, origin, channelID, frame.ChannelTypeGroup)
		pre := sendGroupMessage(t, origin, channelID, frame.ChannelTypeGroup, "before-stop")
		out[leader] = failoverChannel{
			ChannelID:   channelID,
			ChannelType: frame.ChannelTypeGroup,
			Leader:      leader,
			Pre:         pre,
		}
		if len(out) == 3 {
			break
		}
	}
	require.Len(t, out, 3, cluster.DumpDiagnostics())
	return out
}

func createGroupChannel(t *testing.T, node *suite.StartedNode, channelID string, channelType uint8) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, suite.PostChannel(ctx, node.APIAddr(), map[string]any{
		"channel_id":   channelID,
		"channel_type": channelType,
		"subscribers":  []string{"cv2-ha-sender", "cv2-ha-member-a", "cv2-ha-member-b"},
	}), node.DumpDiagnostics())
}

func sendGroupMessage(t *testing.T, node *suite.StartedNode, channelID string, channelType uint8, label string) sentMessage {
	t.Helper()
	return sendGroupMessageWithin(t, node, channelID, channelType, label, 10*time.Second)
}

func sendGroupMessageWithin(t *testing.T, node *suite.StartedNode, channelID string, channelType uint8, label string, timeout time.Duration) sentMessage {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var last sentMessage
	var lastErr error
	for {
		msg, err := postGroupMessage(ctx, node, channelID, channelType, label)
		if err == nil {
			return msg
		}
		last = msg
		lastErr = err
		select {
		case <-ctx.Done():
			require.NoError(t, lastErr, "last=%+v\n%s", last, node.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func postGroupMessage(ctx context.Context, node *suite.StartedNode, channelID string, channelType uint8, label string) (sentMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	clientMsgNo := fmt.Sprintf("%s-%d", label, time.Now().UnixNano())
	resp, err := suite.PostMessageSend(ctx, node.APIAddr(), map[string]any{
		"from_uid":      "cv2-ha-sender",
		"channel_id":    channelID,
		"channel_type":  channelType,
		"client_msg_no": clientMsgNo,
		"payload":       base64.StdEncoding.EncodeToString([]byte(label)),
	})
	if err != nil {
		return sentMessage{ClientMsgNo: clientMsgNo}, err
	}
	if resp.Reason != uint8(frame.ReasonSuccess) {
		return sentMessage{ClientMsgNo: clientMsgNo}, fmt.Errorf("send reason = %d, want success", resp.Reason)
	}
	if resp.MessageID == 0 || resp.MessageSeq == 0 {
		return sentMessage{ClientMsgNo: clientMsgNo}, fmt.Errorf("send returned uncommitted ack: message_id=%d message_seq=%d", resp.MessageID, resp.MessageSeq)
	}
	return sentMessage{ID: uint64(resp.MessageID), Seq: resp.MessageSeq, ClientMsgNo: clientMsgNo}, nil
}

func channelIDForPhysicalSlot(slots []suite.SlotDTO, slot suite.SlotDTO, prefix string) (string, bool) {
	hashSlotCount, ok := hashSlotCountFromSlots(slots)
	if !ok {
		return "", false
	}
	if slot.HashSlots == nil {
		return "", false
	}
	targets := make(map[uint16]struct{}, len(slot.HashSlots.Items))
	for _, item := range slot.HashSlots.Items {
		targets[item] = struct{}{}
	}
	for i := 0; i < 10000; i++ {
		channelID := fmt.Sprintf("%s-%04d", prefix, i)
		if _, ok := targets[routing.HashSlotForKey(channelID, hashSlotCount)]; ok {
			return channelID, true
		}
	}
	return "", false
}

func hashSlotCountFromSlots(slots []suite.SlotDTO) (uint16, bool) {
	var max uint16
	found := false
	for _, slot := range slots {
		if slot.HashSlots == nil {
			continue
		}
		for _, item := range slot.HashSlots.Items {
			if !found || item > max {
				max = item
			}
			found = true
		}
	}
	if !found || max == ^uint16(0) {
		return 0, false
	}
	return max + 1, true
}

type managerMessagePage struct {
	Items []managerMessageItem `json:"items"`
}

type managerMessageItem struct {
	MessageID   uint64 `json:"message_id"`
	MessageSeq  uint64 `json:"message_seq"`
	ClientMsgNo string `json:"client_msg_no"`
}

func requireMessageOnceEventually(t *testing.T, cluster *suite.StartedCluster, node *suite.StartedNode, channelID string, channelType uint8, want sentMessage) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var lastItems []managerMessageItem
	var lastErr error
	for {
		items, err := managerMessagesByClientMsgNo(ctx, node, channelID, channelType, want.ClientMsgNo)
		if err == nil {
			lastItems = items
			if len(items) == 1 && items[0].MessageID == want.ID && items[0].MessageSeq == want.Seq {
				return
			}
			lastErr = fmt.Errorf("items = %+v, want exactly one message id=%d seq=%d", items, want.ID, want.Seq)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("manager message %s timed out: lastItems=%+v lastErr=%v\n%s", want.ClientMsgNo, lastItems, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func managerMessagesByClientMsgNo(ctx context.Context, node *suite.StartedNode, channelID string, channelType uint8, clientMsgNo string) ([]managerMessageItem, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var page managerMessagePage
	query := url.Values{}
	query.Set("channel_id", channelID)
	query.Set("channel_type", fmt.Sprintf("%d", channelType))
	query.Set("client_msg_no", clientMsgNo)
	query.Set("limit", "10")
	_, err := suite.GetJSON(reqCtx, "http://"+node.ManagerAddr()+"/manager/messages?"+query.Encode(), &page)
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

func requireNodeUnschedulableEventually(t *testing.T, cluster *suite.StartedCluster, managerNode *suite.StartedNode, nodeID uint64) {
	t.Helper()
	requireNodeHealthEventually(t, cluster, managerNode, nodeID, func(node suite.NodeDTO) bool {
		return !node.Health.Fresh || !node.Health.RuntimeReady || node.Health.Status != "alive"
	}, "unschedulable")
}

func requireNodeSchedulableEventually(t *testing.T, cluster *suite.StartedCluster, managerNode *suite.StartedNode, nodeID uint64) {
	t.Helper()
	requireNodeHealthEventually(t, cluster, managerNode, nodeID, func(node suite.NodeDTO) bool {
		return node.Health.Fresh && node.Health.RuntimeReady && node.Health.Status == "alive"
	}, "schedulable")
}

func requireNodeHealthEventually(t *testing.T, cluster *suite.StartedCluster, managerNode *suite.StartedNode, nodeID uint64, check func(suite.NodeDTO) bool, label string) {
	t.Helper()

	client := cluster.ManagerClient(t, managerNode.Spec.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var last suite.NodeDTO
	var lastErr error
	for {
		reqCtx, reqCancel := context.WithTimeout(ctx, 2*time.Second)
		nodes, err := client.ListNodes(reqCtx)
		reqCancel()
		if err == nil {
			if node, ok := findNode(nodes, nodeID); ok {
				last = node
				if check(node) {
					return
				}
				lastErr = fmt.Errorf("node health = %+v, want %s", node.Health, label)
			} else {
				lastErr = fmt.Errorf("node %d missing in manager nodes", nodeID)
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("node %d did not become %s: last=%+v lastErr=%v\n%s", nodeID, label, last, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func findNode(nodes suite.NodeListDTO, nodeID uint64) (suite.NodeDTO, bool) {
	for _, node := range nodes.Items {
		if node.NodeID == nodeID {
			return node, true
		}
	}
	return suite.NodeDTO{}, false
}

func sortedLeaderIDs(channelsByLeader map[uint64]failoverChannel) []uint64 {
	leaders := make([]uint64, 0, len(channelsByLeader))
	for leader := range channelsByLeader {
		leaders = append(leaders, leader)
	}
	sort.Slice(leaders, func(i, j int) bool { return leaders[i] < leaders[j] })
	return leaders
}

func firstSurvivingNode(cluster *suite.StartedCluster, stoppedNode uint64) *suite.StartedNode {
	for i := range cluster.Nodes {
		if cluster.Nodes[i].Spec.ID != stoppedNode {
			return &cluster.Nodes[i]
		}
	}
	panic("no surviving node")
}

func requireRetryablePlacementError(t *testing.T, err error) {
	t.Helper()
	msg := strings.ToLower(err.Error())
	for _, token := range []string{"not_ready", "not ready", "route", "placement", "unavailable", "503"} {
		if strings.Contains(msg, token) {
			return
		}
	}
	t.Fatalf("send error = %v, want retryable placement/not-ready error", err)
}
