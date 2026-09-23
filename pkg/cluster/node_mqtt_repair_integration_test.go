//go:build integration

package cluster

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestMQTTRepairThreeNodeLearnerRestartAndIsolation(t *testing.T) {
	voters := []ControlVoter{{NodeID: 1, Addr: freeTCPAddr(t)}, {NodeID: 2, Addr: freeTCPAddr(t)}, {NodeID: 3, Addr: freeTCPAddr(t)}}
	var nodes []*Node
	for _, v := range voters {
		cfg := Config{NodeID: v.NodeID, ListenAddr: v.Addr, DataDir: t.TempDir(), Control: ControlConfig{ClusterID: "mqtt-repair", Voters: voters, AllowBootstrap: true}, Slots: SlotConfig{InitialSlotCount: 2, HashSlotCount: 256, ReplicaCount: 3}}
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
	id := ch.ChannelID{ID: "mqtt-repair-channel", Type: 2}
	m := ch.Meta{Key: ch.ChannelKeyForID(id), ID: id, Epoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 2, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, nodes[0].defaultSlotProxy.UpsertChannelRuntimeMeta(ctx, metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 2, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2}, MinISR: 2, Status: uint8(ch.StatusActive)}))
	source, err := nodes[0].EnsureChannelMQTTSource(ctx, ch.MQTTSourceRequest{ChannelID: id, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, MessageID: 700, ServerTimestampMS: 1000})
	require.NoError(t, err)
	_, err = nodes[0].AppendChannel(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: 701, FromUID: "sender", RedDot: true, Payload: []byte("immutable repaired content"), ServerTimestampMS: 1001}})
	require.NoError(t, err)
	replay := ch.MQTTReplayRequest{ChannelID: id, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, Range: ch.MQTTReplayRange{Generation: source.Generation, From: 1, Through: 2, Limit: 256, MaxBytes: 1 << 20}}
	var receipt ch.MQTTReplayCopyReceipt
	require.Eventually(t, func() bool { receipt, err = nodes[0].CopyChannelMQTTReplay(ctx, replay); return err == nil }, 5*time.Second, 20*time.Millisecond)
	proof, err := nodes[0].CommitChannelMQTTReplayAnchor(ctx, ch.MQTTReplayAnchorRequest{Meta: m, Copy: receipt, MessageID: 702, ServerTimestampMS: 1002})
	require.NoError(t, err)
	// Reconfirming existing content nudges native propagation of its own HW; the
	// repair request cannot manufacture a receiver checkpoint.
	_, err = nodes[0].CopyChannelMQTTReplay(ctx, replay)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		st, e := nodes[2].defaultChannelStore.ChannelStore(m.Key, id)
		if e != nil {
			return false
		}
		defer st.Close()
		p, found, e := st.(channelstore.MQTTReplayAnchorReader).LoadMQTTReplayAnchor(ctx, proof.Manifest.LastOffset)
		return e == nil && found && p == proof
	}, 5*time.Second, 20*time.Millisecond)
	read := func(n *Node) (ch.MQTTReplayPage, error) {
		st, e := n.defaultChannelStore.ChannelStore(m.Key, id)
		if e != nil {
			return ch.MQTTReplayPage{}, e
		}
		defer st.Close()
		return st.(channelstore.MQTTReplayAnchorTransfer).ExportMQTTReplayAnchor(ctx, proof.Manifest.LastOffset, replay.Range)
	}
	_, err = read(nodes[2])
	require.Error(t, err, "learner journal must not imply shared content")
	want, err := read(nodes[1])
	require.NoError(t, err)
	q := ch.MQTTReplayRepairRequest{Target: 3, Donor: 2, AnchorPosition: proof.Manifest.LastOffset, Request: replay}
	prefix, err := nodes[0].RepairChannelMQTTReplay(ctx, q)
	require.NoError(t, err)
	require.Equal(t, proof.Prefix(), prefix)
	got, err := read(nodes[2])
	require.NoError(t, err)
	require.Equal(t, want, got)
	q.Donor = 1
	prefix, err = nodes[0].RepairChannelMQTTReplay(ctx, q)
	require.NoError(t, err)
	require.Equal(t, proof.Prefix(), prefix)
	stopNodes(t, nodes[2])
	replacement, err := New(nodes[2].cfg)
	require.NoError(t, err)
	nodes[2] = replacement
	startNode(t, replacement)
	waitClusterReady(t, nodes...)
	waitNodeWriteReady(t, nodes[0])
	got, err = read(replacement)
	require.NoError(t, err)
	require.Equal(t, want, got)
	prefix, err = nodes[0].RepairChannelMQTTReplay(ctx, q)
	require.NoError(t, err)
	require.Equal(t, proof.Prefix(), prefix)
	route := waitRouteKeyLeaderConverged(t, nodes, id.ID)
	transferSlotLeaderAndWait(t, nodes, route.SlotID, 3)
	stopNodes(t, nodes[0], nodes[1])
	blocked, done := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer done()
	failed, err := nodes[2].RepairChannelMQTTReplay(blocked, q)
	require.Error(t, err)
	require.Zero(t, failed)
	t.Log("mqtt_repair_evidence: nodes=3 hash_slots=256 physical_slots=2 tcp=true disk=true learner_target=true independent_anchor=true donor_rotation=true restart=true exact_retry=true isolated_rejected=true source_release=false automatic_scheduler=false product_listener=false")
}
