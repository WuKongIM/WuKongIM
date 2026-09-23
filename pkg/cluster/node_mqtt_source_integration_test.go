//go:build integration

package cluster

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestMQTTSourceRoutingThreeNodeAuthorityAndRecovery(t *testing.T) {
	voters := []ControlVoter{{NodeID: 1, Addr: freeTCPAddr(t)}, {NodeID: 2, Addr: freeTCPAddr(t)}, {NodeID: 3, Addr: freeTCPAddr(t)}}
	var nodes []*Node
	for _, v := range voters {
		cfg := Config{NodeID: v.NodeID, ListenAddr: v.Addr, DataDir: t.TempDir(), Control: ControlConfig{ClusterID: "mqtt-source-routing", Voters: voters, AllowBootstrap: true}, Slots: SlotConfig{InitialSlotCount: 2, HashSlotCount: 256, ReplicaCount: 3}}
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
	id := ch.ChannelID{ID: "mqtt-protected-channel", Type: 2}
	meta := metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 2, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2, 3}, MinISR: 2, Status: uint8(ch.StatusActive)}
	require.NoError(t, nodes[0].defaultSlotProxy.UpsertChannelRuntimeMeta(ctx, meta))
	q := ch.MQTTSourceRequest{ChannelID: id, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, MessageID: 501, ServerTimestampMS: 1000}
	source, err := nodes[0].EnsureChannelMQTTSource(ctx, q)
	require.NoError(t, err)
	require.Equal(t, uint64(1), source.CommittedThrough)
	message, err := nodes[0].AppendChannel(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: 502, FromUID: "sender", Payload: []byte("protected"), ServerTimestampMS: 1001}})
	require.NoError(t, err)
	require.Equal(t, uint64(2), message.MessageSeq)
	repeated, err := nodes[2].EnsureChannelMQTTSource(ctx, q)
	require.NoError(t, err)
	require.Equal(t, source.Generation, repeated.Generation)
	require.Equal(t, uint64(2), repeated.CommittedThrough)
	// Drive authoritative metadata to another existing voter; its quorum owner
	// must recover the exact committed prefix before returning source evidence.
	meta.Leader = 3
	meta.LeaderEpoch = 2
	meta.RouteGeneration = 2
	require.NoError(t, nodes[0].defaultSlotProxy.UpsertChannelRuntimeMeta(ctx, meta))
	_, err = nodes[0].EnsureChannelMQTTSource(ctx, q)
	require.Error(t, err)
	q.ExpectedLeaderEpoch = 2
	q.ExpectedRouteGeneration = 2
	recovered, err := nodes[0].EnsureChannelMQTTSource(ctx, q)
	require.NoError(t, err)
	require.Equal(t, source.Generation, recovered.Generation)
	require.Equal(t, source.StartAfter, recovered.StartAfter)
	route := waitRouteKeyLeaderConverged(t, nodes, id.ID)
	transferSlotLeaderAndWait(t, nodes, route.SlotID, 3)
	stopNodes(t, nodes[2])
	replacement, err := New(nodes[2].cfg)
	require.NoError(t, err)
	nodes[2] = replacement
	startNode(t, replacement)
	waitClusterReady(t, nodes...)
	// Startup readiness precedes fresh Slot authority after a leader restart.
	waitNodeWriteReady(t, nodes[0])
	waitRouteKeyLeaderConverged(t, nodes, id.ID)
	actualMeta, err := nodes[0].GetChannelRuntimeMetaFresh(ctx, id.ID, 2)
	require.NoError(t, err)
	q.ExpectedLeaderEpoch = actualMeta.LeaderEpoch
	q.ExpectedRouteGeneration = actualMeta.RouteGeneration
	restarted, err := nodes[0].EnsureChannelMQTTSource(ctx, q)
	require.NoError(t, err)
	require.Equal(t, source.Generation, restarted.Generation)
	route = waitRouteKeyLeaderConverged(t, nodes, id.ID)
	transferSlotLeaderAndWait(t, nodes, route.SlotID, 3)
	_, err = nodes[2].EnsureChannelMQTTSource(ctx, q)
	require.NoError(t, err)
	stopNodes(t, nodes[0], nodes[1])
	blocked, done := context.WithTimeout(context.Background(), 300*time.Millisecond)
	_, err = nodes[2].GetChannelRuntimeMetaFresh(blocked, id.ID, 2)
	done()
	require.Error(t, err)
	blocked, done = context.WithTimeout(context.Background(), 300*time.Millisecond)
	_, err = nodes[2].EnsureChannelMQTTSource(blocked, q)
	done()
	require.Error(t, err, "warm source cannot bypass fresh Slot quorum")
	t.Log("mqtt_source_route_evidence: nodes=3 hash_slots=256 physical_slots=2 tcp=true disk=true remote_activation=true leader_recovery=true restart=true isolated_warm_source_rejected=true product_listener=false")
}
