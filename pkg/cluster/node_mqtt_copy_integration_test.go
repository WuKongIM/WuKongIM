//go:build integration

package cluster

import (
	"bytes"
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMQTTCopyThreeNodeDurabilityAndIsolation(t *testing.T) {
	voters := []ControlVoter{{NodeID: 1, Addr: freeTCPAddr(t)}, {NodeID: 2, Addr: freeTCPAddr(t)}, {NodeID: 3, Addr: freeTCPAddr(t)}}
	var nodes []*Node
	for _, v := range voters {
		cfg := Config{NodeID: v.NodeID, ListenAddr: v.Addr, DataDir: t.TempDir(), Control: ControlConfig{ClusterID: "mqtt-copy-quorum", Voters: voters, AllowBootstrap: true}, Slots: SlotConfig{InitialSlotCount: 2, HashSlotCount: 256, ReplicaCount: 3}}
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
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	id := ch.ChannelID{ID: "mqtt-copy-channel", Type: 2}
	meta := metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 2, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2, 3}, MinISR: 3, Status: uint8(ch.StatusActive)}
	require.NoError(t, nodes[0].defaultSlotProxy.UpsertChannelRuntimeMeta(ctx, meta))
	source, err := nodes[0].EnsureChannelMQTTSource(ctx, ch.MQTTSourceRequest{ChannelID: id, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, MessageID: 801, ServerTimestampMS: 1000})
	require.NoError(t, err)
	for i := uint64(0); i < 3; i++ {
		_, err = nodes[0].AppendChannel(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: 802 + i, FromUID: "sender", RedDot: true, Expire: 60, Payload: bytes.Repeat([]byte{byte(i + 1)}, 8192), ServerTimestampMS: 1001 + int64(i)}})
		require.NoError(t, err)
	}
	q := ch.MQTTReplayRequest{ChannelID: id, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, Range: ch.MQTTReplayRange{Generation: source.Generation, From: 1, Through: 4, Limit: 256, MaxBytes: 1 << 20}}
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		for _, n := range nodes {
			st, e := n.defaultChannelStore.ChannelStore(ch.ChannelKeyForID(id), id)
			if e != nil {
				t.Logf("copy diagnostic node=%d open=%v", n.NodeID(), e)
				continue
			}
			state, loadErr := st.Load(context.Background())
			proof, found, sourceErr := st.(channelstore.MQTTSourceReader).LoadCommittedMQTTSource(context.Background(), 4)
			t.Logf("copy diagnostic node=%d state=%+v load=%v source=%+v found=%v error=%v", n.NodeID(), state, loadErr, proof, found, sourceErr)
			_ = st.Close()
		}
	})
	var receipt ch.MQTTReplayCopyReceipt
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		receipt, err = nodes[0].CopyChannelMQTTReplay(ctx, q)
		require.NoError(c, err)
	}, 5*time.Second, 20*time.Millisecond)
	require.Equal(t, []ch.NodeID{1, 2, 3}, receipt.Copies)
	require.Equal(t, 3, receipt.WriteQuorum)
	require.Equal(t, uint64(4), receipt.After.Through)
	want, err := nodes[0].PrepareChannelMQTTReplay(ctx, q)
	require.NoError(t, err)
	readReplica := func(n *Node) {
		st, e := n.defaultChannelStore.ChannelStore(ch.ChannelKeyForID(id), id)
		require.NoError(t, e)
		defer st.Close()
		p, e := st.(channelstore.MQTTReplayPreparer).PrepareMQTTReplay(ctx, q.Range)
		require.NoError(t, e)
		require.Equal(t, want, p)
		// Copy-only work preserves the committed original rows on every replica.
		original, e := st.ReadLog(ctx, channelstore.ReadLogRequest{FromOffset: 1, MaxOffset: 4, MaxBytes: 1 << 20})
		require.NoError(t, e)
		require.Len(t, original.Records, 4)
	}
	for _, n := range nodes {
		readReplica(n)
	}
	stopNodes(t, nodes[0])
	replacement, err := New(nodes[0].cfg)
	require.NoError(t, err)
	nodes[0] = replacement
	startNode(t, replacement)
	waitClusterReady(t, nodes...)
	waitNodeWriteReady(t, nodes[1])
	readReplica(replacement)
	// A new leader must bind a new authority, while retaining identical full content.
	meta.Leader = 3
	meta.LeaderEpoch = 2
	meta.RouteGeneration = 2
	meta.MinISR = 2
	require.NoError(t, nodes[1].defaultSlotProxy.UpsertChannelRuntimeMeta(ctx, meta))
	_, err = nodes[1].CopyChannelMQTTReplay(ctx, q)
	require.Error(t, err)
	q.ExpectedLeaderEpoch = 2
	q.ExpectedRouteGeneration = 2
	require.Eventually(t, func() bool { receipt, err = nodes[1].CopyChannelMQTTReplay(ctx, q); return err == nil }, 5*time.Second, 20*time.Millisecond)
	require.Equal(t, want.After, receipt.After)
	require.Contains(t, receipt.Copies, ch.NodeID(3))
	require.GreaterOrEqual(t, len(receipt.Copies), 2)
	route := waitRouteKeyLeaderConverged(t, nodes, id.ID)
	transferSlotLeaderAndWait(t, nodes, route.SlotID, 3)
	_, err = nodes[2].CopyChannelMQTTReplay(ctx, q)
	require.NoError(t, err)
	stopNodes(t, nodes[0], nodes[1])
	blocked, done := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer done()
	failed, err := nodes[2].CopyChannelMQTTReplay(blocked, q)
	require.Error(t, err)
	require.Zero(t, failed)
	t.Log("mqtt_copy_quorum_evidence: nodes=3 hash_slots=256 physical_slots=2 tcp=true disk=true all_voter_copy=true full_content_equal=true follower_restart=true leader_change=true originals_preserved=true isolated_receipt_rejected=true source_release=false product_listener=false")
}
