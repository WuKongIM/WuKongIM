//go:build e2e

package channel_failover

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestChannelThreeNodeLeaderFailoverAfterNodeKill(t *testing.T) {
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

	require.NoError(t, cluster.StartStoppedNode(killedNode), cluster.DumpDiagnostics())
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
	requireNodeSchedulableEventually(t, cluster, survivor, killedNode)

	afterRestart := sendGroupMessageWithin(t, cluster.MustNode(killedNode), affected.ChannelID, affected.ChannelType, "after-old-leader-restart", 20*time.Second)
	require.Greater(t, afterRestart.Seq, afterFailover.Seq, cluster.DumpDiagnostics())
	requireMessageOnceEventually(t, cluster, survivor, affected.ChannelID, affected.ChannelType, afterRestart)
}

func TestChannelNewPlacementFailsClosedWhenReplicaCountUnavailable(t *testing.T) {
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster(fastRecoveryOptions()...)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	const stoppedNode = uint64(3)
	survivor := cluster.MustNode(1)
	channelID := fmt.Sprintf("e2e-channel-placement-unavailable-%d", time.Now().UnixNano())
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

func TestChannelFollowerReplicaRepairAfterNodeKill(t *testing.T) {
	s := suite.New(t)
	opts := fastRecoveryOptionsForNodes(4, map[string]string{
		"WK_CLUSTER_CHANNEL_REPLICA_N": "3",
	})
	opts = append(opts, suite.WithDynamicJoinToken("join-secret"))
	cluster := s.StartThreeNodeCluster(opts...)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	manager := cluster.ManagerClient(t, 1)
	node4 := cluster.StartSeedJoinNode(t, suite.SeedJoinNodeConfig{
		NodeID:    4,
		JoinToken: "join-secret",
	})
	manager.EventuallyNodeReadiness(t, node4.Spec.ID, true, 20*time.Second)

	candidate := createFollowerRepairCandidate(t, cluster)
	t.Logf("follower repair candidate: channel=%s/%d leader=%d slotLeader=%d replicas=%v stoppedFollower=%d secondStoppedFollower=%d spare=%d slot=%d hashSlot=%d observedSlotLeaders=%v",
		candidate.ChannelID, candidate.ChannelType, candidate.Leader, candidate.SlotLeader, candidate.Replicas, candidate.StoppedFollower, candidate.SecondStoppedFollower, candidate.SpareNode, candidate.SlotID, candidate.HashSlot, candidate.ObservedSlotLeaders)
	manager.MustActivateNode(t, node4.Spec.ID)
	requireNodeSchedulableEventually(t, cluster, cluster.MustNode(1), node4.Spec.ID)
	require.NoError(t, cluster.MustNode(candidate.StoppedFollower).Stop(), cluster.DumpDiagnostics())
	requireNodeUnschedulableEventually(t, cluster, cluster.MustNode(candidate.Leader), candidate.StoppedFollower)

	requireReplicaRepairCompletedEventually(t, cluster, cluster.MustNode(candidate.SlotLeader), candidate, 45*time.Second)
	afterRepair := sendGroupMessageWithin(t, cluster.MustNode(candidate.Leader), candidate.ChannelID, candidate.ChannelType, "follower-repair-after", 20*time.Second)
	require.Greater(t, afterRepair.Seq, candidate.Pre.Seq, cluster.DumpDiagnostics())

	require.NoError(t, cluster.MustNode(candidate.SecondStoppedFollower).Stop(), cluster.DumpDiagnostics())
	requireNodeUnschedulableEventually(t, cluster, cluster.MustNode(candidate.Leader), candidate.SecondStoppedFollower)

	afterSecondStop := sendGroupMessageWithin(t, cluster.MustNode(candidate.Leader), candidate.ChannelID, candidate.ChannelType, "follower-repair-after-second-stop", 20*time.Second)
	require.Greater(t, afterSecondStop.Seq, afterRepair.Seq, cluster.DumpDiagnostics())
	requireMessageOnceEventually(t, cluster, cluster.MustNode(candidate.Leader), candidate.ChannelID, candidate.ChannelType, candidate.Pre)
	requireMessageOnceEventually(t, cluster, cluster.MustNode(candidate.Leader), candidate.ChannelID, candidate.ChannelType, afterRepair)
	requireMessageOnceEventually(t, cluster, cluster.MustNode(candidate.Leader), candidate.ChannelID, candidate.ChannelType, afterSecondStop)
}

func fastRecoveryOptions() []suite.Option {
	return fastRecoveryOptionsForNodes(3, nil)
}

func fastRecoveryOptionsForNodes(nodeCount int, extra map[string]string) []suite.Option {
	opts := []suite.Option{suite.WithManagerHTTP()}
	for nodeID := uint64(1); nodeID <= uint64(nodeCount); nodeID++ {
		overrides := map[string]string{
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
		}
		for key, value := range extra {
			overrides[key] = value
		}
		opts = append(opts, suite.WithNodeConfigOverrides(nodeID, overrides))
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

type followerRepairCandidate struct {
	ChannelID             string
	ChannelType           uint8
	Leader                uint64
	SlotLeader            uint64
	SlotID                uint32
	HashSlot              uint16
	StoppedFollower       uint64
	SecondStoppedFollower uint64
	SpareNode             uint64
	Replicas              []uint64
	ObservedSlotLeaders   []uint64
	Pre                   sentMessage
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
		channelID, ok := channelIDForPhysicalSlot(slots, slot, fmt.Sprintf("e2e-channel-failover-%d-%d", time.Now().UnixNano(), leader))
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

func createFollowerRepairCandidate(t *testing.T, cluster *suite.StartedCluster) followerRepairCandidate {
	t.Helper()

	origin := cluster.MustNode(1)
	slots := cluster.ManagerClient(t, origin.Spec.ID).MustSlots(t)
	observedSlotLeaders := slotLeaderObservationsBySlot(t, cluster, origin, []uint64{1, 2, 3})
	nodeIDs := clusterNodeIDs(cluster)
	for i := 0; i < 200; i++ {
		channelID := fmt.Sprintf("e2e-channel-follower-repair-%d-%02d", time.Now().UnixNano(), i)
		placement, ok := deterministicChannelPlacement(slots, observedSlotLeaders, nodeIDs, channelID, frame.ChannelTypeGroup, 3)
		require.True(t, ok, "deterministic placement for %s not found in slots=%+v", channelID, slots)
		if placement.SlotLeader == 0 || len(placement.ObservedSlotLeaders) != 1 || uint64InList(placement.Replicas, 4) {
			continue
		}
		firstFollower, secondFollower := repairFollowers(placement.Replicas, placement.Leader, append([]uint64{1}, placement.ObservedSlotLeaders...)...)
		spareNode, ok := firstNodeNotInReplicas(cluster, placement.Replicas)
		if placement.Leader == 0 || firstFollower == 0 || secondFollower == 0 || !ok {
			continue
		}
		if placement.Leader == 4 || spareNode != 4 {
			continue
		}
		createGroupChannel(t, origin, channelID, frame.ChannelTypeGroup)
		pre := sendGroupMessageWithin(t, origin, channelID, frame.ChannelTypeGroup, "follower-repair-before", 20*time.Second)
		return followerRepairCandidate{
			ChannelID:             channelID,
			ChannelType:           frame.ChannelTypeGroup,
			Leader:                placement.Leader,
			SlotLeader:            placement.SlotLeader,
			SlotID:                placement.SlotID,
			HashSlot:              placement.HashSlot,
			StoppedFollower:       firstFollower,
			SecondStoppedFollower: secondFollower,
			SpareNode:             spareNode,
			Replicas:              placement.Replicas,
			ObservedSlotLeaders:   placement.ObservedSlotLeaders,
			Pre:                   pre,
		}
	}
	t.Fatalf("no follower-repair candidate found\n%s", cluster.DumpDiagnostics())
	return followerRepairCandidate{}
}

func createGroupChannel(t *testing.T, node *suite.StartedNode, channelID string, channelType uint8) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, suite.PostChannel(ctx, node.APIAddr(), map[string]any{
		"channel_id":   channelID,
		"channel_type": channelType,
		"subscribers":  []string{"channel-ha-sender", "channel-ha-member-a", "channel-ha-member-b"},
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
		"from_uid":      "channel-ha-sender",
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

type deterministicPlacement struct {
	Leader              uint64
	SlotLeader          uint64
	SlotID              uint32
	HashSlot            uint16
	Replicas            []uint64
	ObservedSlotLeaders []uint64
}

func deterministicChannelPlacement(slots []suite.SlotDTO, slotLeaders map[uint32][]uint64, nodeIDs []uint64, channelID string, channelType uint8, replicaCount int) (deterministicPlacement, bool) {
	slot, ok := slotForChannelID(slots, channelID)
	if !ok {
		return deterministicPlacement{}, false
	}
	replicas := selectDeterministicReplicas(channelID, channelType, nodeIDs, replicaCount)
	if len(replicas) != replicaCount {
		return deterministicPlacement{}, false
	}
	leader := replicas[0]
	if preferred := slot.Assignment.PreferredLeaderID; preferred != 0 && uint64InList(replicas, preferred) {
		leader = preferred
	} else if preferred := slot.Runtime.PreferredLeaderID; preferred != 0 && uint64InList(replicas, preferred) {
		leader = preferred
	}
	hashSlotCount, ok := hashSlotCountFromSlots(slots)
	if !ok {
		return deterministicPlacement{}, false
	}
	observed := append([]uint64(nil), slotLeaders[slot.SlotID]...)
	slotLeader, _ := singleObservedSlotLeader(observed)
	return deterministicPlacement{
		Leader:              leader,
		SlotLeader:          slotLeader,
		SlotID:              slot.SlotID,
		HashSlot:            routing.HashSlotForKey(channelID, hashSlotCount),
		Replicas:            replicas,
		ObservedSlotLeaders: observed,
	}, true
}

func slotForChannelID(slots []suite.SlotDTO, channelID string) (suite.SlotDTO, bool) {
	hashSlotCount, ok := hashSlotCountFromSlots(slots)
	if !ok {
		return suite.SlotDTO{}, false
	}
	hashSlot := routing.HashSlotForKey(channelID, hashSlotCount)
	for _, slot := range slots {
		if slot.HashSlots == nil {
			continue
		}
		for _, item := range slot.HashSlots.Items {
			if item == hashSlot {
				return slot, true
			}
		}
	}
	return suite.SlotDTO{}, false
}

func selectDeterministicReplicas(channelID string, channelType uint8, candidates []uint64, replicaCount int) []uint64 {
	uniq := append([]uint64(nil), candidates...)
	sort.Slice(uniq, func(i, j int) bool { return uniq[i] < uniq[j] })
	n := 0
	for _, candidate := range uniq {
		if candidate == 0 {
			continue
		}
		if n == 0 || uniq[n-1] != candidate {
			uniq[n] = candidate
			n++
		}
	}
	uniq = uniq[:n]
	if len(uniq) < replicaCount || replicaCount <= 0 {
		return nil
	}
	channelKey := string(ch.ChannelKeyForID(ch.ChannelID{ID: channelID, Type: channelType}))
	type scoredNode struct {
		node  uint64
		score uint64
	}
	scored := make([]scoredNode, 0, len(uniq))
	for _, nodeID := range uniq {
		scored = append(scored, scoredNode{node: nodeID, score: testRendezvousScore(channelKey, nodeID)})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].node < scored[j].node
		}
		return scored[i].score > scored[j].score
	})
	out := make([]uint64, 0, replicaCount)
	for i := 0; i < replicaCount; i++ {
		out = append(out, scored[i].node)
	}
	return out
}

func testRendezvousScore(channelID string, node uint64) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(channelID))
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], node)
	_, _ = h.Write(buf[:])
	return h.Sum64()
}

type channelMigrationPage struct {
	Items []channelMigrationItem `json:"items"`
}

type channelMigrationItem struct {
	TaskID     string `json:"task_id"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	Phase      string `json:"phase"`
	SourceNode uint64 `json:"source_node"`
	TargetNode uint64 `json:"target_node"`
	Blocker    string `json:"blocker_message"`
}

func requireReplicaRepairCompletedEventually(t *testing.T, cluster *suite.StartedCluster, node *suite.StartedNode, candidate followerRepairCandidate, timeout time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var seen bool
	var last channelMigrationItem
	var lastItems []channelMigrationItem
	var lastErr error
	for {
		items, err := activeChannelMigrations(ctx, node, candidate.ChannelID, candidate.ChannelType)
		if err == nil {
			lastItems = items
			expected, ok := findExpectedReplicaRepair(items, candidate)
			switch {
			case ok:
				seen = true
				last = expected
				if expected.Status == "blocked" || expected.Status == "failed" {
					t.Fatalf("replica repair task stopped early: task=%+v\n%s", expected, cluster.DumpDiagnostics())
				}
				lastErr = fmt.Errorf("replica repair still active: task=%+v", expected)
			case seen && len(items) == 0:
				return
			default:
				lastErr = fmt.Errorf("active migrations = %+v, want replica_replace source=%d target=%d", items, candidate.StoppedFollower, candidate.SpareNode)
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("replica repair did not complete: seen=%v last=%+v lastItems=%+v lastErr=%v\n%s", seen, last, lastItems, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func activeChannelMigrations(ctx context.Context, node *suite.StartedNode, channelID string, channelType uint8) ([]channelMigrationItem, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var page channelMigrationPage
	query := url.Values{}
	query.Set("channel_id", channelID)
	query.Set("channel_type", fmt.Sprintf("%d", channelType))
	query.Set("limit", "10")
	_, err := suite.GetJSON(reqCtx, "http://"+node.ManagerAddr()+"/manager/channel-migrations/active?"+query.Encode(), &page)
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

func findExpectedReplicaRepair(items []channelMigrationItem, candidate followerRepairCandidate) (channelMigrationItem, bool) {
	for _, item := range items {
		if item.Kind == "replica_replace" &&
			item.SourceNode == candidate.StoppedFollower &&
			item.TargetNode == candidate.SpareNode {
			return item, true
		}
	}
	return channelMigrationItem{}, false
}

type channelRuntimeMetaPage struct {
	Items []channelRuntimeMetaItem `json:"items"`
}

type channelRuntimeMetaItem struct {
	ChannelID   string   `json:"channel_id"`
	ChannelType int64    `json:"channel_type"`
	SlotID      uint32   `json:"slot_id"`
	Leader      uint64   `json:"leader"`
	SlotLeader  uint64   `json:"slot_leader"`
	Replicas    []uint64 `json:"replicas"`
	ISR         []uint64 `json:"isr"`
	Status      string   `json:"status"`
}

func requireChannelRuntimeMetaEventually(t *testing.T, node *suite.StartedNode, channelID string, channelType uint8) channelRuntimeMetaItem {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var last channelRuntimeMetaItem
	var lastErr error
	for {
		meta, err := channelRuntimeMeta(ctx, node, channelID, channelType)
		if err == nil {
			last = meta
			if meta.Leader != 0 && meta.SlotLeader != 0 && len(meta.Replicas) > 0 && meta.Status == "active" {
				return meta
			}
			lastErr = fmt.Errorf("runtime meta = %+v, want active leader/slot leader/replicas", meta)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("channel runtime meta %s/%d timed out: last=%+v lastErr=%v\n%s", channelID, channelType, last, lastErr, node.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func channelRuntimeMeta(ctx context.Context, node *suite.StartedNode, channelID string, channelType uint8) (channelRuntimeMetaItem, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var page channelRuntimeMetaPage
	query := url.Values{}
	query.Set("channel_id", channelID)
	query.Set("limit", "10")
	_, err := suite.GetJSON(reqCtx, "http://"+node.ManagerAddr()+"/manager/channel-runtime-meta?"+query.Encode(), &page)
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

type managerSlotsPage struct {
	Items []suite.SlotDTO `json:"items"`
}

func managerSlotsForNodeEventually(t *testing.T, cluster *suite.StartedCluster, managerNode *suite.StartedNode, nodeID uint64) []suite.SlotDTO {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var lastItems []suite.SlotDTO
	var lastErr error
	for {
		items, err := managerSlotsForNode(ctx, managerNode, nodeID)
		if err == nil {
			lastItems = items
			if slotsHaveNodeLogLeaders(items) {
				return items
			}
			lastErr = fmt.Errorf("slot node logs = %+v", items)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("manager slots for node %d did not expose slot leaders: lastItems=%+v lastErr=%v\n%s", nodeID, lastItems, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func managerSlotsForNode(ctx context.Context, managerNode *suite.StartedNode, nodeID uint64) ([]suite.SlotDTO, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var page managerSlotsPage
	query := url.Values{}
	query.Set("node_id", fmt.Sprintf("%d", nodeID))
	_, err := suite.GetJSON(reqCtx, "http://"+managerNode.ManagerAddr()+"/manager/slots?"+query.Encode(), &page)
	if err != nil {
		return nil, err
	}
	sort.Slice(page.Items, func(i, j int) bool { return page.Items[i].SlotID < page.Items[j].SlotID })
	return page.Items, nil
}

func slotsHaveNodeLogLeaders(items []suite.SlotDTO) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if item.NodeLog == nil || item.NodeLog.LeaderID == 0 {
			return false
		}
	}
	return true
}

func slotLeaderObservationsBySlot(t *testing.T, cluster *suite.StartedCluster, managerNode *suite.StartedNode, nodeIDs []uint64) map[uint32][]uint64 {
	t.Helper()

	observed := make(map[uint32]map[uint64]struct{})
	for _, nodeID := range nodeIDs {
		slots := managerSlotsForNodeEventually(t, cluster, managerNode, nodeID)
		for _, slot := range slots {
			if slot.NodeLog == nil || slot.NodeLog.LeaderID == 0 {
				continue
			}
			if observed[slot.SlotID] == nil {
				observed[slot.SlotID] = make(map[uint64]struct{})
			}
			observed[slot.SlotID][slot.NodeLog.LeaderID] = struct{}{}
		}
	}
	out := make(map[uint32][]uint64, len(observed))
	for slotID, leaders := range observed {
		items := make([]uint64, 0, len(leaders))
		for leader := range leaders {
			items = append(items, leader)
		}
		sort.Slice(items, func(i, j int) bool { return items[i] < items[j] })
		out[slotID] = items
	}
	return out
}

func singleObservedSlotLeader(leaders []uint64) (uint64, bool) {
	if len(leaders) != 1 || leaders[0] == 0 {
		return 0, false
	}
	return leaders[0], true
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

func repairFollowers(replicas []uint64, leader uint64, avoidFirstStop ...uint64) (uint64, uint64) {
	followers := make([]uint64, 0, len(replicas))
	for _, nodeID := range replicas {
		if nodeID != leader {
			followers = append(followers, nodeID)
		}
	}
	if len(followers) < 2 {
		return 0, 0
	}
	first := followers[0]
	avoid := make(map[uint64]struct{}, len(avoidFirstStop))
	for _, nodeID := range avoidFirstStop {
		if nodeID != 0 {
			avoid[nodeID] = struct{}{}
		}
	}
	for _, nodeID := range followers {
		if _, ok := avoid[nodeID]; !ok {
			first = nodeID
			break
		}
	}
	if _, ok := avoid[first]; ok {
		return 0, 0
	}
	for _, nodeID := range followers {
		if nodeID != first {
			return first, nodeID
		}
	}
	return first, 0
}

func firstNodeNotInReplicas(cluster *suite.StartedCluster, replicas []uint64) (uint64, bool) {
	for _, node := range cluster.Nodes {
		if !uint64InList(replicas, node.Spec.ID) {
			return node.Spec.ID, true
		}
	}
	return 0, false
}

func clusterNodeIDs(cluster *suite.StartedCluster) []uint64 {
	if cluster == nil {
		return nil
	}
	out := make([]uint64, 0, len(cluster.Nodes))
	for _, node := range cluster.Nodes {
		out = append(out, node.Spec.ID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func uint64InList(items []uint64, want uint64) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
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
