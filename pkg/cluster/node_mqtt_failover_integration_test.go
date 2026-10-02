//go:build integration

package cluster

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMQTTFailoverThreeNodeRecoversMissingContentAfterLeaderStops(t *testing.T) {
	voters := []ControlVoter{{NodeID: 1, Addr: freeTCPAddr(t)}, {NodeID: 2, Addr: freeTCPAddr(t)}, {NodeID: 3, Addr: freeTCPAddr(t)}}
	var nodes []*Node
	for _, v := range voters {
		cfg := Config{NodeID: v.NodeID, ListenAddr: v.Addr, DataDir: t.TempDir(), Control: ControlConfig{ClusterID: "mqtt-failover", Voters: voters, AllowBootstrap: true}, Slots: SlotConfig{InitialSlotCount: 2, HashSlotCount: 256, ReplicaCount: 3}}
		cfg.Channel.ReplicaCount = 3
		cfg.ChannelMigration = ChannelMigrationConfig{EnabledSet: true, Enabled: false}
		cfg.HealthReport.Interval = 200 * time.Millisecond
		n, err := New(cfg)
		require.NoError(t, err)
		nodes = append(nodes, n)
	}
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	id := ch.ChannelID{ID: "mqtt-failover", Type: 2}
	route := waitRouteKeyLeaderConverged(t, nodes, id.ID)
	transferSlotLeaderAndWait(t, nodes, route.SlotID, 1)
	waitNodeWriteReady(t, nodes[0])
	meta := metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 2, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2}, MinISR: 2, Status: uint8(ch.StatusActive)}
	require.NoError(t, nodes[0].defaultSlotProxy.UpsertChannelRuntimeMeta(ctx, meta))
	m := ch.Meta{ID: id, Key: ch.ChannelKeyForID(id), Epoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 2, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	var source ch.MQTTSourceSnapshot
	var err error
	require.Eventually(t, func() bool {
		source, err = nodes[0].EnsureChannelMQTTSource(ctx, ch.MQTTSourceRequest{ChannelID: id, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, MessageID: 900, ServerTimestampMS: 1000})
		return err == nil
	}, 5*time.Second, 20*time.Millisecond)
	_, err = nodes[0].AppendChannel(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: 901, FromUID: "sender", Payload: []byte("survives stopped leader"), ServerTimestampMS: 1001}})
	require.NoError(t, err)
	q := ch.MQTTReplayRequest{ChannelID: id, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, Range: ch.MQTTReplayRange{Generation: source.Generation, From: 1, Through: 2, Limit: 256, MaxBytes: 1 << 20}}
	var receipt ch.MQTTReplayCopyReceipt
	require.Eventually(t, func() bool { receipt, err = nodes[0].CopyChannelMQTTReplay(ctx, q); return err == nil }, 5*time.Second, 20*time.Millisecond)
	anchor, err := nodes[0].CommitChannelMQTTReplayAnchor(ctx, ch.MQTTReplayAnchorRequest{Meta: m, Copy: receipt, MessageID: 902, ServerTimestampMS: 1002})
	require.NoError(t, err)
	// Expand native ISR only for the fixture. The previously non-voting replica
	// has the native anchor but deliberately has not received shared content.
	meta.ChannelEpoch++
	meta.ISR = []uint64{1, 2, 3}
	require.NoError(t, nodes[0].defaultSlotProxy.UpsertChannelRuntimeMeta(ctx, meta))
	var target ch.RuntimeProbeChannel
	require.Eventually(t, func() bool {
		if _, e := nodes[0].ProbeChannel(ctx, 2, id.ID, id.Type); e != nil {
			return false
		}
		target, err = nodes[0].ProbeChannel(ctx, 3, id.ID, id.Type)
		return err == nil && target.HW >= anchor.Manifest.LastOffset && target.ReplayReadiness != nil && !target.ReplayReadiness.Covered
	}, 5*time.Second, 20*time.Millisecond)
	stopNodes(t, nodes[1])
	survivors := []*Node{nodes[0], nodes[2]}
	// Full readiness also requires capacity for new three-replica channels;
	// this existing channel and its Slot can recover on the surviving quorum.
	require.Eventually(t, func() bool {
		_, e := nodes[0].GetChannelRuntimeMetaFresh(ctx, id.ID, int64(id.Type))
		return e == nil
	}, 5*time.Second, 20*time.Millisecond)
	route = waitRouteKeyLeaderConverged(t, survivors, id.ID)
	owner := clusterNodeByID(t, survivors, route.Leader)
	store := requireNodeMigrationStore(t, owner)
	task, err := store.CreateLeaderFailover(ctx, channels.CreateLeaderFailoverRequest{ChannelID: id, TaskID: "mqtt-dead-leader", DesiredLeader: 3, ObservedHW: target.HW, ObservedLeaderEpoch: target.LeaderEpoch})
	require.NoError(t, err)
	selected := &mqttMigrationTaskSource{store: store, id: id, taskID: task.TaskID}
	executor := channels.NewMigrationExecutor(channels.MigrationExecutorConfig{LocalNode: owner.NodeID(), Source: selected, Store: store, Runtime: owner, Meta: owner, FailoverPhaseLimit: 8})
	readTask := func() metadb.ChannelMigrationTask {
		current, ok, e := store.Get(ctx, id, task.TaskID)
		require.NoError(t, e)
		require.True(t, ok)
		return current
	}
	drive := func(done func(metadb.ChannelMigrationTask) bool) {
		t.Helper()
		var lastErr error
		var current metadb.ChannelMigrationTask
		if !assert.Eventually(t, func() bool {
			lastErr = executor.RunOnce(ctx)
			current = readTask()
			return lastErr == nil && done(current)
		}, 15*time.Second, 30*time.Millisecond) {
			t.Logf("failover task=%+v err=%v", current, lastErr)
			for _, n := range survivors {
				p, e := n.ChannelRuntimeProbe(ctx, ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{id}})
				t.Logf("node %d runtime=%+v err=%v", n.NodeID(), p, e)
			}
			t.FailNow()
		}
	}
	drive(func(current metadb.ChannelMigrationTask) bool {
		return current.Phase == metadb.ChannelMigrationPhaseVerifyNewLeader
	})
	waiting := readTask()
	current, err := owner.GetChannelRuntimeMetaFresh(ctx, id.ID, int64(id.Type))
	require.NoError(t, err)
	require.Equal(t, uint64(3), current.Leader, "native leader must exist before shared recovery")
	require.Equal(t, task.TaskID, current.WriteFenceToken)
	require.Eventually(t, func() bool {
		target, err = owner.ProbeChannel(ctx, 3, id.ID, id.Type)
		return err == nil && !target.RecoveryRequired
	}, 5*time.Second, 20*time.Millisecond)
	require.NotNil(t, target.ReplayReadiness)
	require.False(t, target.ReplayReadiness.Covered)
	for range 3 {
		require.NoError(t, executor.RunOnce(ctx))
		require.Equal(t, waiting, readTask())
	}
	_, err = owner.AppendChannel(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: 903, FromUID: "sender", Payload: []byte("must stay fenced"), ServerTimestampMS: 1003}})
	require.Error(t, err)
	recovery := ch.MQTTReplayRecoveryRequest{Target: 3, Source: ch.MQTTReplayPlanRequest{ChannelID: id, ExpectedChannelEpoch: current.ChannelEpoch, ExpectedLeaderEpoch: current.LeaderEpoch, ExpectedRouteGeneration: current.RouteGeneration, Generation: source.Generation}, TargetAnchor: anchor.Manifest.LastOffset, ScanLimit: 64}
	require.Eventually(t, func() bool {
		result, e := owner.StepChannelMQTTReplayRecovery(ctx, recovery)
		return e == nil && result.Plan.Complete
	}, 10*time.Second, 30*time.Millisecond)
	drive(func(current metadb.ChannelMigrationTask) bool {
		return current.Status == metadb.ChannelMigrationStatusCompleted
	})
	current, err = owner.GetChannelRuntimeMetaFresh(ctx, id.ID, int64(id.Type))
	require.NoError(t, err)
	require.Equal(t, uint64(3), current.Leader)
	require.Empty(t, current.WriteFenceToken)
	read := func(n *Node) ch.MQTTReplayPage {
		st, e := n.defaultChannelStore.ChannelStore(m.Key, id)
		require.NoError(t, e)
		defer st.Close()
		page, e := st.(channelstore.MQTTReplayAnchorTransfer).ExportMQTTReplayAnchor(ctx, anchor.Manifest.LastOffset, q.Range)
		require.NoError(t, e)
		return page
	}
	require.Equal(t, read(nodes[0]), read(nodes[2]), "new leader must retain the independently anchored content")
	writeCtx, stopWrite := context.WithTimeout(ctx, 5*time.Second)
	defer stopWrite()
	for writeCtx.Err() == nil {
		_, err = owner.AppendChannel(writeCtx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: 904, FromUID: "sender", ClientMsgNo: "after-failover", Payload: []byte("writes resumed"), ServerTimestampMS: 1004}})
		if err == nil {
			break
		}
		select {
		case <-writeCtx.Done():
		case <-time.After(20 * time.Millisecond):
		}
	}
	require.NoError(t, err, "ordinary writes must route to the new leader after recovery")

	t.Log("mqtt_failover_evidence: nodes=3 hash_slots=256 stopped_source=true real_slot_task=true native_leader_before_content=true fence_retained=true surviving_donor=true replay_verified=true writes_resumed=true scanner_selection=false product_listener=false")
}
