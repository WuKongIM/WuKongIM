//go:build e2e

package message_event_stream

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const legacyStreamSetting = 1 << 1

type messageEventEnvelope struct {
	Status int                 `json:"status"`
	Data   messageEventPayload `json:"data"`
}

type messageEventPayload struct {
	ClientMsgNo  string `json:"client_msg_no"`
	EventKey     string `json:"event_key"`
	EventID      string `json:"event_id"`
	MsgEventSeq  uint64 `json:"msg_event_seq"`
	StreamStatus string `json:"stream_status"`
}

type channelMessageSyncPage struct {
	Messages []channelMessageSyncItem `json:"messages"`
}

type channelMessageSyncItem struct {
	ClientMsgNo string                `json:"client_msg_no"`
	End         int                   `json:"end"`
	EndReason   int                   `json:"end_reason"`
	StreamData  []byte                `json:"stream_data"`
	EventMeta   *messageEventMeta     `json:"event_meta"`
	EventHint   *messageEventSyncHint `json:"event_sync_hint"`
}

type messageEventMeta struct {
	HasEvents       bool                  `json:"has_events"`
	Completed       bool                  `json:"completed"`
	EventVersion    uint64                `json:"event_version"`
	LastMsgEventSeq uint64                `json:"last_msg_event_seq"`
	EventCount      int                   `json:"event_count"`
	OpenEventCount  int                   `json:"open_event_count"`
	Events          []messageEventKeyMeta `json:"events"`
}

type messageEventKeyMeta struct {
	EventKey        string         `json:"event_key"`
	Status          string         `json:"status"`
	LastMsgEventSeq uint64         `json:"last_msg_event_seq"`
	Snapshot        map[string]any `json:"snapshot"`
	EndReason       int            `json:"end_reason"`
}

type messageEventSyncHint struct {
	ClientMsgNo     string `json:"client_msg_no"`
	FromMsgEventSeq uint64 `json:"from_msg_event_seq"`
}

type messageEventSlotLeaderTransferResponse struct {
	SlotID       uint32             `json:"slot_id"`
	TargetNode   uint64             `json:"target_node"`
	ActualLeader uint64             `json:"actual_leader"`
	Created      bool               `json:"created"`
	Task         *suite.SlotTaskDTO `json:"task,omitempty"`
	Message      string             `json:"message"`
}

func TestWukongIMMessageEventStreamBuffersUntilFinishAndExposesMetrics(t *testing.T) {
	node := suite.New(t).StartSingleNodeCluster()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const (
		channelID   = "e2e-message-event-stream-room"
		aliceUID    = "message-event-alice"
		clientMsgNo = "message-event-stream-base-1"
	)
	require.NoError(t, suite.PostChannel(ctx, node.APIAddr(), map[string]any{
		"channel_id":   channelID,
		"channel_type": frame.ChannelTypeGroup,
		"reset":        1,
		"subscribers":  []string{aliceUID},
	}), node.DumpDiagnostics())

	send, err := suite.PostMessageSend(ctx, node.APIAddr(), map[string]any{
		"from_uid":      aliceUID,
		"channel_id":    channelID,
		"channel_type":  frame.ChannelTypeGroup,
		"client_msg_no": clientMsgNo,
		"setting":       legacyStreamSetting,
		"payload":       base64.StdEncoding.EncodeToString([]byte("stream base")),
	})
	require.NoError(t, err, node.DumpDiagnostics())
	require.Equal(t, uint8(frame.ReasonSuccess), send.Reason, node.DumpDiagnostics())
	require.NotZero(t, send.MessageSeq, node.DumpDiagnostics())

	mainDelta := postMessageEvent(t, ctx, *node, map[string]any{
		"channel_id":    channelID,
		"channel_type":  frame.ChannelTypeGroup,
		"from_uid":      aliceUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt-main-delta",
		"event_key":     "main",
		"event_type":    "stream.delta",
		"payload":       map[string]any{"kind": "text", "delta": "hello "},
	})
	require.Equal(t, uint64(0), mainDelta.Data.MsgEventSeq, node.DumpDiagnostics())
	require.Equal(t, "open", mainDelta.Data.StreamStatus, node.DumpDiagnostics())
	suite.RequireMetricAtLeastEventually(t, *node, "wukongim_message_event_append_total", map[string]string{
		"path":       "cache",
		"event_type": "stream.delta",
		"result":     "ok",
	}, 1)
	suite.RequireMetricAtLeastEventually(t, *node, "wukongim_message_event_stream_cache_sessions", nil, 1)
	suite.RequireMetricAtLeastEventually(t, *node, "wukongim_message_event_stream_cache_open_lanes", nil, 1)

	toolDelta := postMessageEvent(t, ctx, *node, map[string]any{
		"channel_id":    channelID,
		"channel_type":  frame.ChannelTypeGroup,
		"from_uid":      aliceUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt-tool-delta",
		"event_key":     "tool",
		"event_type":    "stream.delta",
		"payload":       map[string]any{"kind": "text", "delta": "lookup"},
	})
	require.Equal(t, uint64(0), toolDelta.Data.MsgEventSeq, node.DumpDiagnostics())
	suite.RequireMetricAtLeastEventually(t, *node, "wukongim_message_event_stream_cache_open_lanes", nil, 2)

	finish := postMessageEvent(t, ctx, *node, map[string]any{
		"channel_id":    channelID,
		"channel_type":  frame.ChannelTypeGroup,
		"from_uid":      aliceUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt-finish",
		"event_type":    "stream.finish",
		"payload":       map[string]any{"end_reason": 3},
	})
	require.NotZero(t, finish.Data.MsgEventSeq, node.DumpDiagnostics())
	require.Equal(t, "closed", finish.Data.StreamStatus, node.DumpDiagnostics())
	suite.RequireMetricAtLeastEventually(t, *node, "wukongim_message_event_append_total", map[string]string{
		"path":       "finish_batch",
		"event_type": "stream.finish",
		"result":     "ok",
	}, 1)
	suite.RequireMetricAtLeastEventually(t, *node, "wukongim_message_event_propose_total", map[string]string{
		"path":   "finish_batch",
		"result": "ok",
	}, 1)
	suite.RequireMetricAtLeastEventually(t, *node, "wukongim_message_event_propose_batch_events_sum", map[string]string{
		"path":   "finish_batch",
		"result": "ok",
	}, 3)
	requireMetricEqualsEventually(t, *node, "wukongim_message_event_stream_cache_sessions", nil, 0)
	requireMetricEqualsEventually(t, *node, "wukongim_message_event_stream_cache_open_lanes", nil, 0)

	restartSingleNodeCluster(t, node)
	msg := requireStreamMessageEventMetaEventually(t, *node, aliceUID, channelID, clientMsgNo, send.MessageSeq, 10*time.Second)
	require.NotNil(t, msg.EventHint, node.DumpDiagnostics())
	require.Equal(t, clientMsgNo, msg.EventHint.ClientMsgNo, node.DumpDiagnostics())
	require.Equal(t, 1, msg.End, node.DumpDiagnostics())
	require.Equal(t, 3, msg.EndReason, node.DumpDiagnostics())
	require.Contains(t, string(msg.StreamData), "hello ", node.DumpDiagnostics())
	require.NotNil(t, msg.EventMeta, node.DumpDiagnostics())
	require.True(t, msg.EventMeta.HasEvents, node.DumpDiagnostics())
	require.True(t, msg.EventMeta.Completed, node.DumpDiagnostics())
	require.Equal(t, 2, msg.EventMeta.EventCount, node.DumpDiagnostics())
	require.Equal(t, 0, msg.EventMeta.OpenEventCount, node.DumpDiagnostics())
	require.GreaterOrEqual(t, msg.EventMeta.LastMsgEventSeq, uint64(2), node.DumpDiagnostics())
	requireEventLane(t, msg.EventMeta, "main", "closed", "hello ")
	requireEventLane(t, msg.EventMeta, "tool", "closed", "lookup")
}

func TestWukongIMMessageEventStreamFollowerForwardAndLeaderChangeFailClosed(t *testing.T) {
	cluster := suite.New(t).StartThreeNodeCluster(suite.WithManagerHTTP())
	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	require.NoError(t, cluster.WaitClusterReady(readyCtx), cluster.DumpDiagnostics())

	managerNode := cluster.MustNode(1)
	initialSlots := managerSlotsForNodeEventually(t, cluster, managerNode, managerNode.Spec.ID)
	slot, initialLeader, targetLeader := chooseTransferableMessageEventSlot(t, initialSlots)
	channelID, ok := channelIDForMessageEventSlot(initialSlots, slot, fmt.Sprintf("e2e-message-event-forward-%d", time.Now().UnixNano()))
	require.True(t, ok, "channel id for slot %d not found in slots=%+v", slot.SlotID, initialSlots)
	ingress := firstNodeExcept(t, cluster, initialLeader)
	leaderNode := cluster.MustNode(initialLeader)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const (
		aliceUID    = "message-event-forward-alice"
		clientMsgNo = "message-event-forward-base-1"
	)
	require.NoError(t, suite.PostChannel(ctx, ingress.APIAddr(), map[string]any{
		"channel_id":   channelID,
		"channel_type": frame.ChannelTypeGroup,
		"reset":        1,
		"subscribers":  []string{aliceUID},
	}), cluster.DumpDiagnostics())

	send, err := suite.PostMessageSend(ctx, ingress.APIAddr(), map[string]any{
		"from_uid":      aliceUID,
		"channel_id":    channelID,
		"channel_type":  frame.ChannelTypeGroup,
		"client_msg_no": clientMsgNo,
		"setting":       legacyStreamSetting,
		"payload":       base64.StdEncoding.EncodeToString([]byte("stream base")),
	})
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, uint8(frame.ReasonSuccess), send.Reason, cluster.DumpDiagnostics())

	postStreamDeltas(t, ctx, *ingress, channelID, aliceUID, clientMsgNo, "evt-main-delta", "evt-tool-delta")
	suite.RequireMetricAtLeastEventually(t, *ingress, "wukongim_message_event_append_total", map[string]string{
		"path":       "forward",
		"event_type": "stream.delta",
		"result":     "ok",
	}, 2)
	suite.RequireMetricAtLeastEventually(t, *leaderNode, "wukongim_message_event_append_total", map[string]string{
		"path":       "cache",
		"event_type": "stream.delta",
		"result":     "ok",
	}, 2)
	suite.RequireMetricAtLeastEventually(t, *leaderNode, "wukongim_message_event_stream_cache_open_lanes", nil, 2)

	transferCtx, cancelTransfer := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelTransfer()
	accepted := postSlotLeaderTransfer(t, transferCtx, cluster, slot.SlotID, targetLeader)
	require.Equal(t, slot.SlotID, accepted.SlotID)
	require.NotZero(t, accepted.ActualLeader)
	moved := requireSlotLeaderMoved(t, transferCtx, cluster, managerNode, slot.SlotID, initialLeader)
	newLeader := moved.NodeLog.LeaderID
	require.NotEqual(t, initialLeader, newLeader)
	newLeaderNode := cluster.MustNode(newLeader)
	finishIngress := firstNodeExcept(t, cluster, newLeader)

	requireMetricEqualsEventually(t, *leaderNode, "wukongim_message_event_stream_cache_sessions", nil, 0)
	requireMetricEqualsEventually(t, *leaderNode, "wukongim_message_event_stream_cache_open_lanes", nil, 0)

	err = postMessageEventError(ctx, *finishIngress, map[string]any{
		"channel_id":    channelID,
		"channel_type":  frame.ChannelTypeGroup,
		"from_uid":      aliceUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt-finish-before-replay",
		"event_type":    "stream.finish",
		"payload":       map[string]any{"end_reason": 3},
	})
	require.Error(t, err, cluster.DumpDiagnostics())
	require.Contains(t, err.Error(), "message event stream cache miss", cluster.DumpDiagnostics())
	suite.RequireMetricAtLeastEventually(t, *newLeaderNode, "wukongim_message_event_append_total", map[string]string{
		"path":       "finish_batch",
		"event_type": "stream.finish",
		"result":     "cache_miss",
	}, 1)

	postStreamDeltas(t, ctx, *finishIngress, channelID, aliceUID, clientMsgNo, "evt-main-delta-replay", "evt-tool-delta-replay")
	finish := postMessageEvent(t, ctx, *finishIngress, map[string]any{
		"channel_id":    channelID,
		"channel_type":  frame.ChannelTypeGroup,
		"from_uid":      aliceUID,
		"client_msg_no": clientMsgNo,
		"event_id":      "evt-finish",
		"event_type":    "stream.finish",
		"payload":       map[string]any{"end_reason": 3},
	})
	require.NotZero(t, finish.Data.MsgEventSeq, cluster.DumpDiagnostics())
	require.Equal(t, "closed", finish.Data.StreamStatus, cluster.DumpDiagnostics())
	suite.RequireMetricAtLeastEventually(t, *newLeaderNode, "wukongim_message_event_propose_total", map[string]string{
		"path":   "finish_batch",
		"result": "ok",
	}, 1)
	suite.RequireMetricAtLeastEventually(t, *newLeaderNode, "wukongim_message_event_propose_batch_events_sum", map[string]string{
		"path":   "finish_batch",
		"result": "ok",
	}, 3)
	requireMetricEqualsEventually(t, *newLeaderNode, "wukongim_message_event_stream_cache_open_lanes", nil, 0)

	msg := requireStreamMessageEventMetaEventually(t, *finishIngress, aliceUID, channelID, clientMsgNo, send.MessageSeq, 15*time.Second)
	require.NotNil(t, msg.EventMeta, cluster.DumpDiagnostics())
	require.True(t, msg.EventMeta.Completed, cluster.DumpDiagnostics())
	require.Equal(t, 2, msg.EventMeta.EventCount, cluster.DumpDiagnostics())
	requireEventLane(t, msg.EventMeta, "main", "closed", "hello ")
	requireEventLane(t, msg.EventMeta, "tool", "closed", "lookup")
}

func postMessageEvent(t *testing.T, ctx context.Context, node suite.StartedNode, body map[string]any) messageEventEnvelope {
	t.Helper()

	var out messageEventEnvelope
	_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+"/message/event", body, &out)
	require.NoError(t, err, node.DumpDiagnostics())
	require.Equal(t, 200, out.Status, node.DumpDiagnostics())
	return out
}

func postMessageEventError(ctx context.Context, node suite.StartedNode, body map[string]any) error {
	var out messageEventEnvelope
	_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+"/message/event", body, &out)
	return err
}

func postStreamDeltas(t *testing.T, ctx context.Context, node suite.StartedNode, channelID, fromUID, clientMsgNo, mainEventID, toolEventID string) {
	t.Helper()

	mainDelta := postMessageEvent(t, ctx, node, map[string]any{
		"channel_id":    channelID,
		"channel_type":  frame.ChannelTypeGroup,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      mainEventID,
		"event_key":     "main",
		"event_type":    "stream.delta",
		"payload":       map[string]any{"kind": "text", "delta": "hello "},
	})
	require.Equal(t, uint64(0), mainDelta.Data.MsgEventSeq, node.DumpDiagnostics())
	require.Equal(t, "open", mainDelta.Data.StreamStatus, node.DumpDiagnostics())

	toolDelta := postMessageEvent(t, ctx, node, map[string]any{
		"channel_id":    channelID,
		"channel_type":  frame.ChannelTypeGroup,
		"from_uid":      fromUID,
		"client_msg_no": clientMsgNo,
		"event_id":      toolEventID,
		"event_key":     "tool",
		"event_type":    "stream.delta",
		"payload":       map[string]any{"kind": "text", "delta": "lookup"},
	})
	require.Equal(t, uint64(0), toolDelta.Data.MsgEventSeq, node.DumpDiagnostics())
}

func restartSingleNodeCluster(t *testing.T, node *suite.StartedNode) {
	t.Helper()

	require.NotNil(t, node)
	require.NotNil(t, node.Process)
	require.NoError(t, node.Restart(node.Process.BinaryPath))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, suite.WaitHTTPReady(ctx, node.APIAddr(), "/readyz"), node.DumpDiagnostics())
	require.NoError(t, suite.WaitWKProtoReady(ctx, node.GatewayAddr()), node.DumpDiagnostics())
}

func requireMetricEqualsEventually(t *testing.T, node suite.StartedNode, name string, labels map[string]string, want float64) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var last float64
	var lastErr error
	for {
		last, lastErr = suite.FetchMetricValue(ctx, node.APIAddr(), name, labels)
		if lastErr == nil && last == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("metric %s%v = %v err=%v, want %v\n%s", name, labels, last, lastErr, want, node.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func requireStreamMessageEventMetaEventually(t *testing.T, node suite.StartedNode, loginUID, channelID, clientMsgNo string, startSeq uint64, timeout time.Duration) channelMessageSyncItem {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var lastPage channelMessageSyncPage
	var lastErr error
	for {
		var page channelMessageSyncPage
		_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+"/channel/messagesync", map[string]any{
			"login_uid":          loginUID,
			"channel_id":         channelID,
			"channel_type":       frame.ChannelTypeGroup,
			"start_message_seq":  startSeq,
			"limit":              10,
			"event_summary_mode": "full",
		}, &page)
		if err == nil {
			lastPage = page
			for _, msg := range page.Messages {
				if msg.ClientMsgNo == clientMsgNo && msg.EventMeta != nil && msg.EventMeta.Completed {
					return msg
				}
			}
			lastErr = fmt.Errorf("message %s with completed event meta not found", clientMsgNo)
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("message event meta timed out: lastPage=%#v lastErr=%v\n%s", lastPage, lastErr, node.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func requireEventLane(t *testing.T, meta *messageEventMeta, eventKey, status, text string) {
	t.Helper()

	for _, lane := range meta.Events {
		if lane.EventKey != eventKey {
			continue
		}
		require.Equal(t, status, lane.Status)
		require.NotZero(t, lane.LastMsgEventSeq)
		require.Contains(t, fmt.Sprint(lane.Snapshot), text)
		return
	}
	t.Fatalf("event lane %s not found in %#v", eventKey, meta.Events)
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
		if item.Task != nil || item.NodeLog == nil || item.NodeLog.LeaderID == 0 {
			return false
		}
	}
	return true
}

func chooseTransferableMessageEventSlot(t *testing.T, slots []suite.SlotDTO) (suite.SlotDTO, uint64, uint64) {
	t.Helper()

	for _, slot := range slots {
		if slot.NodeLog == nil || slot.NodeLog.LeaderID == 0 {
			continue
		}
		source := slot.NodeLog.LeaderID
		for _, peer := range slot.Assignment.DesiredPeers {
			if peer != source {
				return slot, source, peer
			}
		}
	}
	t.Fatalf("no transferable message event slot found in slots=%+v", slots)
	return suite.SlotDTO{}, 0, 0
}

func channelIDForMessageEventSlot(slots []suite.SlotDTO, slot suite.SlotDTO, prefix string) (string, bool) {
	hashSlotCount, ok := hashSlotCountFromSlots(slots)
	if !ok || slot.HashSlots == nil {
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

func firstNodeExcept(t *testing.T, cluster *suite.StartedCluster, excluded uint64) *suite.StartedNode {
	t.Helper()

	for i := range cluster.Nodes {
		if cluster.Nodes[i].Spec.ID != excluded {
			return &cluster.Nodes[i]
		}
	}
	t.Fatalf("no node found outside excluded node %d", excluded)
	return nil
}

func postSlotLeaderTransfer(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, slotID uint32, target uint64) messageEventSlotLeaderTransferResponse {
	t.Helper()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	nodeErrors := make(map[string]string)
	for {
		for _, node := range cluster.Nodes {
			var out messageEventSlotLeaderTransferResponse
			_, err := suite.PostJSON(ctx, fmt.Sprintf("http://%s/manager/slots/%d/leader-transfer", node.ManagerAddr(), slotID), map[string]any{
				"target_node": target,
			}, &out)
			if err == nil {
				return out
			}
			nodeErrors[fmt.Sprintf("node %d (%s)", node.Spec.ID, node.ManagerAddr())] = err.Error()
		}

		select {
		case <-ctx.Done():
			t.Fatalf("no manager node accepted slot leader transfer slot=%d target=%d errors=%s\n%s", slotID, target, formatNodeErrors(nodeErrors), cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func requireSlotLeaderMoved(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, managerNode *suite.StartedNode, slotID uint32, source uint64) suite.SlotDTO {
	t.Helper()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	var last suite.SlotDTO
	var lastErr error
	for {
		slots, err := managerSlotsForNode(ctx, managerNode, managerNode.Spec.ID)
		if err == nil {
			if slot, ok := findSlot(slots, slotID); ok {
				last = slot
				if slot.Task == nil && slot.NodeLog != nil && slot.NodeLog.LeaderID != 0 && slot.NodeLog.LeaderID != source {
					return slot
				}
				lastErr = fmt.Errorf("slot %d leader/task = %+v/%+v", slotID, slot.NodeLog, slot.Task)
			} else {
				lastErr = fmt.Errorf("slot %d missing from slots %+v", slotID, slots)
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf("slot leader did not move from source %d: last=%+v lastErr=%v\n%s", source, last, lastErr, cluster.DumpDiagnostics())
		case <-ticker.C:
		}
	}
}

func findSlot(items []suite.SlotDTO, slotID uint32) (suite.SlotDTO, bool) {
	for _, item := range items {
		if item.SlotID == slotID {
			return item, true
		}
	}
	return suite.SlotDTO{}, false
}

func formatNodeErrors(nodeErrors map[string]string) string {
	if len(nodeErrors) == 0 {
		return "<none>"
	}
	keys := make([]string, 0, len(nodeErrors))
	for key := range nodeErrors {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("%s: %s", key, nodeErrors[key]))
	}
	return strings.Join(lines, "; ")
}
