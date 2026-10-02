//go:build integration

package cluster

import (
	"context"
	"strings"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
	"github.com/stretchr/testify/require"
)

func TestWillReceiptRoutingThreeNodeTrimFailoverRestartAndIsolation(t *testing.T) {
	voters := []ControlVoter{{NodeID: 1, Addr: freeTCPAddr(t)}, {NodeID: 2, Addr: freeTCPAddr(t)}, {NodeID: 3, Addr: freeTCPAddr(t)}}
	var nodes []*Node
	for _, v := range voters {
		cfg := Config{NodeID: v.NodeID, ListenAddr: v.Addr, DataDir: t.TempDir(), Control: ControlConfig{ClusterID: "will-receipt-routing", Voters: voters, AllowBootstrap: true}, Slots: SlotConfig{InitialSlotCount: 2, HashSlotCount: 256, ReplicaCount: 3}}
		cfg.HealthReport.Interval = 200 * time.Millisecond
		cfg.Channel.ReplicaCount = 3
		n, err := New(cfg)
		require.NoError(t, err)
		nodes = append(nodes, n)
	}
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)
	waitNodeWriteReady(t, nodes[0])
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	id := ch.ChannelID{ID: "will-receipt-routed", Type: 2}
	meta := metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 2, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2, 3}, MinISR: 2, Status: uint8(ch.StatusActive)}
	require.NoError(t, nodes[0].defaultSlotProxy.UpsertChannelRuntimeMeta(ctx, meta))
	key := "mqtt-will-v1:" + strings.Repeat("a", 64)
	md, err := publication.Encode(publication.Metadata{Source: publication.SourceWill, QoS: 1, PublisherNamespace: "n", PublisherClientID: "c", OriginalTopic: "t", ServerWillKey: key})
	require.NoError(t, err)
	msg, err := nodes[0].AppendChannel(ctx, ch.AppendRequest{ChannelID: id, CommitMode: ch.CommitModeQuorum, Message: ch.Message{MessageID: 502, FromUID: "sender", ClientMsgNo: "client", Payload: []byte("will body"), PublicationMetadata: md, ServerTimestampMS: 1001}})
	require.NoError(t, err)
	q := ch.WillReceiptRequest{ChannelID: id, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, FromUID: "sender", ServerWillKey: key}
	first, err := nodes[0].ReadChannelWillReceipt(ctx, q)
	require.NoError(t, err)
	require.True(t, first.Found)
	require.Equal(t, msg.MessageSeq, first.Receipt.MessageSeq)
	require.EqualValues(t, 502, first.Receipt.MessageID)
	require.EqualValues(t, 1001, first.Receipt.ServerTimestampMS)
	meta.Leader = 3
	meta.LeaderEpoch = 2
	meta.RouteGeneration = 2
	require.NoError(t, nodes[0].defaultSlotProxy.UpsertChannelRuntimeMeta(ctx, meta))
	_, err = nodes[0].ReadChannelWillReceipt(ctx, q)
	require.Error(t, err)
	q.ExpectedLeaderEpoch = 2
	q.ExpectedRouteGeneration = 2
	recovered, err := nodes[0].ReadChannelWillReceipt(ctx, q)
	require.NoError(t, err)
	require.Equal(t, first.Receipt, recovered.Receipt)
	lease, err := nodes[2].localChannelStoreFactory().ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	_, err = lease.AdoptRetentionBoundary(ctx, msg.MessageSeq, "committed")
	require.NoError(t, err)
	trim, err := lease.TrimMessagesThrough(ctx, msg.MessageSeq, channelstore.RetentionTrimOptions{MaxMessages: 64, MaxBytes: 1 << 20})
	require.NoError(t, err)
	require.Equal(t, msg.MessageSeq, trim.DeletedThroughSeq)
	require.NoError(t, lease.Close())
	trimmed, err := nodes[0].ReadChannelWillReceipt(ctx, q)
	require.NoError(t, err)
	require.Equal(t, first.Receipt, trimmed.Receipt)
	stopNodes(t, nodes[2])
	replacement, err := New(nodes[2].cfg)
	require.NoError(t, err)
	nodes[2] = replacement
	startNode(t, replacement)
	waitClusterReady(t, nodes...)
	waitNodeWriteReady(t, nodes[0])
	waitRouteKeyLeaderConverged(t, nodes, id.ID)
	current, err := nodes[0].GetChannelRuntimeMetaFresh(ctx, id.ID, 2)
	require.NoError(t, err)
	q.ExpectedChannelEpoch = current.ChannelEpoch
	q.ExpectedLeaderEpoch = current.LeaderEpoch
	q.ExpectedRouteGeneration = current.RouteGeneration
	restarted, err := nodes[0].ReadChannelWillReceipt(ctx, q)
	require.NoError(t, err)
	require.Equal(t, first.Receipt, restarted.Receipt)
	route := waitRouteKeyLeaderConverged(t, nodes, id.ID)
	transferSlotLeaderAndWait(t, nodes, route.SlotID, current.Leader)
	serving := nodes[int(current.Leader)-1]
	_, err = serving.ReadChannelWillReceipt(ctx, q)
	require.NoError(t, err)
	for _, node := range nodes {
		if node != serving {
			stopNodes(t, node)
		}
	}
	blocked, done := context.WithTimeout(context.Background(), 300*time.Millisecond)
	_, err = serving.ReadChannelWillReceipt(blocked, q)
	done()
	require.Error(t, err, "warm retained receipt must not bypass fresh Slot quorum")
	t.Log("will_receipt_route_evidence: nodes=3 hash_slots=256 physical_slots=2 tcp=true disk=true leader_change=true serving_original_trim=true restart=true isolated_warm_receipt_rejected=true original_identity_time_preserved=true product_listener=false")
}
