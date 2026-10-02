//go:build integration

package cluster

import (
	"bytes"
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestMQTTReplayRoutingThreeNodeRecoveryAndIsolation(t *testing.T) {
	voters := []ControlVoter{{NodeID: 1, Addr: freeTCPAddr(t)}, {NodeID: 2, Addr: freeTCPAddr(t)}, {NodeID: 3, Addr: freeTCPAddr(t)}}
	var nodes []*Node
	for _, v := range voters {
		cfg := Config{NodeID: v.NodeID, ListenAddr: v.Addr, DataDir: t.TempDir(), Control: ControlConfig{ClusterID: "mqtt-replay-routing", Voters: voters, AllowBootstrap: true}, Slots: SlotConfig{InitialSlotCount: 2, HashSlotCount: 256, ReplicaCount: 3}}
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
	id := ch.ChannelID{ID: "mqtt-replay-channel", Type: 2}
	meta := metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 2, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2, 3}, MinISR: 2, Status: uint8(ch.StatusActive)}
	require.NoError(t, nodes[0].defaultSlotProxy.UpsertChannelRuntimeMeta(ctx, meta))
	source, err := nodes[0].EnsureChannelMQTTSource(ctx, ch.MQTTSourceRequest{ChannelID: id, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, MessageID: 501, ServerTimestampMS: 1000})
	require.NoError(t, err)
	for i := uint64(0); i < 3; i++ {
		_, err := nodes[0].AppendChannel(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: 502 + i, FromUID: "sender", RedDot: true, Expire: 60, Payload: bytes.Repeat([]byte{byte(i + 1)}, 8192), ServerTimestampMS: 1001 + int64(i)}})
		require.NoError(t, err)
	}
	q := ch.MQTTReplayRequest{ChannelID: id, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1,
		Range: ch.MQTTReplayRange{Generation: source.Generation, From: 1, Through: 4, Limit: 1, MaxBytes: 1 << 20}}
	first, err := nodes[0].PrepareChannelMQTTReplay(ctx, q)
	require.NoError(t, err)
	require.Equal(t, uint64(1), first.After.Through)
	q.Range.Limit = 256
	retry, err := nodes[2].PrepareChannelMQTTReplay(ctx, q)
	require.NoError(t, err)
	require.Equal(t, first, retry)
	q.Range.From = 2
	second, err := nodes[0].PrepareChannelMQTTReplay(ctx, q)
	require.NoError(t, err)
	require.Equal(t, first.After, second.Before)
	require.Equal(t, uint64(4), second.After.Through)
	require.Greater(t, len(second.Records[0].Content), 4096)
	q.Range.From = 1
	all, err := nodes[0].PrepareChannelMQTTReplay(ctx, q)
	require.NoError(t, err)
	meta.Leader, meta.LeaderEpoch, meta.RouteGeneration = 3, 2, 2
	require.NoError(t, nodes[0].defaultSlotProxy.UpsertChannelRuntimeMeta(ctx, meta))
	_, err = nodes[0].PrepareChannelMQTTReplay(ctx, q)
	require.Error(t, err)
	q.ExpectedLeaderEpoch, q.ExpectedRouteGeneration = 2, 2
	recovered, err := nodes[0].PrepareChannelMQTTReplay(ctx, q)
	require.NoError(t, err)
	require.Equal(t, all, recovered, "another voter must derive identical shared original content")
	route := waitRouteKeyLeaderConverged(t, nodes, id.ID)
	transferSlotLeaderAndWait(t, nodes, route.SlotID, 3)
	stopNodes(t, nodes[2])
	replacement, err := New(nodes[2].cfg)
	require.NoError(t, err)
	nodes[2] = replacement
	startNode(t, replacement)
	waitClusterReady(t, nodes...)
	waitNodeWriteReady(t, nodes[0])
	waitRouteKeyLeaderConverged(t, nodes, id.ID)
	actual, err := nodes[0].GetChannelRuntimeMetaFresh(ctx, id.ID, 2)
	require.NoError(t, err)
	q.ExpectedChannelEpoch, q.ExpectedLeaderEpoch, q.ExpectedRouteGeneration = actual.ChannelEpoch, actual.LeaderEpoch, actual.RouteGeneration
	restarted, err := nodes[0].PrepareChannelMQTTReplay(ctx, q)
	require.NoError(t, err)
	require.Equal(t, all, restarted)
	route = waitRouteKeyLeaderConverged(t, nodes, id.ID)
	transferSlotLeaderAndWait(t, nodes, route.SlotID, 3)
	_, err = nodes[2].PrepareChannelMQTTReplay(ctx, q)
	require.NoError(t, err)
	stopNodes(t, nodes[0], nodes[1])
	blocked, done := context.WithTimeout(context.Background(), 300*time.Millisecond)
	_, err = nodes[2].PrepareChannelMQTTReplay(blocked, q)
	done()
	require.Error(t, err, "warm replay cannot bypass fresh Slot quorum")
	t.Log("mqtt_replay_route_evidence: nodes=3 hash_slots=256 physical_slots=2 tcp=true disk=true remote_paging=true leader_recovery=true restart=true immutable_digest=true isolated_warm_replay_rejected=true product_listener=false")
}
