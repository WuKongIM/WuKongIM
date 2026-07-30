//go:build integration

package cluster

import (
	"context"
	"strings"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestClusterGetMessageEventStatesBatchReadsLeaderCacheFromFollower(t *testing.T) {
	nodes := newDefaultThreeNodeCluster(t)
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)

	channelID := "message-event-remote-cache"
	route := waitRouteKeyLeaderConverged(t, nodes, channelID)
	follower := firstNonLeaderNode(t, nodes, route.Leader)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := follower.AppendMessageEvent(ctx, metadb.MessageEventAppend{
		ChannelID:   channelID,
		ChannelType: 2,
		ClientMsgNo: "cmn-remote-cache",
		EventID:     "evt-remote-delta",
		EventKey:    "main",
		EventType:   metadb.EventTypeStreamDelta,
		Visibility:  metadb.VisibilityPublic,
		OccurredAt:  1000,
		Payload:     []byte(`{"kind":"text","delta":"remote"}`),
		UpdatedAt:   1001,
	}); err != nil {
		t.Fatalf("AppendMessageEvent(follower delta) error = %v", err)
	}

	key := metadb.MessageEventMessageKey{ChannelID: channelID, ChannelType: 2, ClientMsgNo: "cmn-remote-cache"}
	got, err := follower.GetMessageEventStatesBatch(ctx, []metadb.MessageEventMessageKey{key}, 10)
	if err != nil {
		t.Fatalf("GetMessageEventStatesBatch(follower) error = %v", err)
	}
	if len(got[key]) != 1 || got[key][0].Status != metadb.EventStatusOpen || got[key][0].LastMsgEventSeq != 0 {
		t.Fatalf("follower state batch = %#v, want leader cached open state", got[key])
	}
	if !strings.Contains(string(got[key][0].SnapshotPayload), "remote") {
		t.Fatalf("follower snapshot = %s, want leader cached text", got[key][0].SnapshotPayload)
	}
}
