//go:build integration

package cluster

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	channels "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exact task reads avoid a background scanner racing the deliberately paused
// recovery interval; phase execution and all state transitions remain real.
type mqttMigrationTaskSource struct {
	store  *channels.MigrationStore
	id     ch.ChannelID
	taskID string
}

func (s *mqttMigrationTaskSource) ListRunnableMigrationTasks(ctx context.Context, _ uint64, _ int) ([]metadb.ChannelMigrationTask, error) {
	task, ok, err := s.store.Get(ctx, s.id, s.taskID)
	if err != nil || !ok || task.IsTerminal() {
		return nil, err
	}
	return []metadb.ChannelMigrationTask{task}, nil
}

func TestMQTTMigrationThreeNodeWaitsForReplayAndResumes(t *testing.T) {
	voters := []ControlVoter{{NodeID: 1, Addr: freeTCPAddr(t)}, {NodeID: 2, Addr: freeTCPAddr(t)}, {NodeID: 3, Addr: freeTCPAddr(t)}}
	var nodes []*Node
	for _, v := range voters {
		cfg := Config{NodeID: v.NodeID, ListenAddr: v.Addr, DataDir: t.TempDir(), Control: ControlConfig{ClusterID: "mqtt-migration", Voters: voters, AllowBootstrap: true}, Slots: SlotConfig{InitialSlotCount: 2, HashSlotCount: 256, ReplicaCount: 3}}
		cfg.Channel.ReplicaCount = 2
		cfg.ChannelMigration = ChannelMigrationConfig{EnabledSet: true, Enabled: false}
		cfg.HealthReport.Interval = 200 * time.Millisecond
		n, err := New(cfg)
		require.NoError(t, err)
		nodes = append(nodes, n)
	}
	startNodes(t, nodes...)
	t.Cleanup(func() { stopNodes(t, nodes...) })
	waitClusterReady(t, nodes...)
	waitNodeWriteReady(t, nodes[0])
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	id := ch.ChannelID{ID: "mqtt-migration", Type: 2}
	waitRouteKeyLeaderConverged(t, nodes, id.ID)
	m := ch.Meta{ID: id, Key: ch.ChannelKeyForID(id), Epoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 2, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	require.NoError(t, nodes[0].defaultSlotProxy.UpsertChannelRuntimeMeta(ctx, metadb.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 2, Replicas: []uint64{1, 2}, ISR: []uint64{1, 2}, MinISR: 2, Status: uint8(ch.StatusActive)}))
	var source ch.MQTTSourceSnapshot
	var err error
	require.Eventually(t, func() bool {
		source, err = nodes[0].EnsureChannelMQTTSource(ctx, ch.MQTTSourceRequest{ChannelID: id, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, MessageID: 800, ServerTimestampMS: 1000})
		return err == nil
	}, 5*time.Second, 20*time.Millisecond)
	_, err = nodes[0].AppendChannel(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: 801, FromUID: "sender", Payload: []byte("must survive migration"), ServerTimestampMS: 1001}})
	require.NoError(t, err)
	q := ch.MQTTReplayRequest{ChannelID: id, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1, Range: ch.MQTTReplayRange{Generation: source.Generation, From: 1, Through: 2, Limit: 256, MaxBytes: 1 << 20}}
	var receipt ch.MQTTReplayCopyReceipt
	require.Eventually(t, func() bool { receipt, err = nodes[0].CopyChannelMQTTReplay(ctx, q); return err == nil }, 5*time.Second, 20*time.Millisecond)
	anchor, err := nodes[0].CommitChannelMQTTReplayAnchor(ctx, ch.MQTTReplayAnchorRequest{Meta: m, Copy: receipt, MessageID: 802, ServerTimestampMS: 1002})
	require.NoError(t, err)
	route := waitRouteKeyLeaderConverged(t, nodes, id.ID)
	owner := clusterNodeByID(t, nodes, route.Leader)
	store := requireNodeMigrationStore(t, owner)
	created, err := store.CreateReplicaReplace(ctx, channels.CreateReplicaReplaceRequest{ChannelID: id, TaskID: "mqtt-replace", SourceNode: 1, TargetNode: 3})
	require.NoError(t, err)
	selected := &mqttMigrationTaskSource{store: store, id: id, taskID: created.TaskID}
	executor := channels.NewMigrationExecutor(channels.MigrationExecutorConfig{LocalNode: owner.NodeID(), Source: selected, Store: store, Runtime: owner, Meta: owner})
	readTask := func() metadb.ChannelMigrationTask {
		task, found, e := store.Get(ctx, id, selected.taskID)
		require.NoError(t, e)
		require.True(t, found)
		return task
	}
	drive := func(done func(metadb.ChannelMigrationTask) bool) {
		t.Helper()
		var lastErr error
		var lastTask metadb.ChannelMigrationTask
		if !assert.Eventually(t, func() bool {
			lastErr = executor.RunOnce(ctx)
			lastTask = readTask()
			return lastErr == nil && done(lastTask)
		}, 15*time.Second, 30*time.Millisecond) {
			t.Logf("migration task=%+v err=%v", lastTask, lastErr)
			for _, n := range nodes {
				p, e := n.ChannelRuntimeProbe(ctx, ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{id}})
				t.Logf("node %d runtime=%+v err=%v", n.NodeID(), p, e)
				st, e := n.defaultChannelStore.ChannelStore(m.Key, id)
				if e == nil {
					state, loadErr := st.Load(ctx)
					t.Logf("node %d stored=%+v err=%v", n.NodeID(), state, loadErr)
					ready, readyErr := st.(channelstore.MQTTReplayReadinessReader).ReadMQTTReplayReadiness(ctx, state.HW)
					t.Logf("node %d readiness=%+v err=%v", n.NodeID(), ready, readyErr)
					_ = st.Close()
				}
			}
			t.FailNow()
		}
	}
	drive(func(task metadb.ChannelMigrationTask) bool {
		return task.Phase == metadb.ChannelMigrationPhaseFinalTargetCatchUp
	})
	waiting := readTask()
	var lagging ch.RuntimeProbeChannel
	require.Eventually(t, func() bool {
		lagging, err = owner.ProbeChannel(ctx, 3, id.ID, id.Type)
		return err == nil && lagging.HW >= waiting.CutoverLEO
	}, 5*time.Second, 20*time.Millisecond)
	require.NotNil(t, lagging.ReplayReadiness)
	require.False(t, lagging.ReplayReadiness.Covered)
	for range 3 {
		require.NoError(t, executor.RunOnce(ctx))
		require.Equal(t, waiting, readTask(), "native catch-up must not admit promotion or block future recovery")
	}
	current, err := owner.GetChannelRuntimeMetaFresh(ctx, id.ID, int64(id.Type))
	require.NoError(t, err)
	require.Contains(t, current.Replicas, uint64(1))
	require.NotContains(t, current.ISR, uint64(3))
	require.Equal(t, created.TaskID, current.WriteFenceToken)
	recovery := ch.MQTTReplayRecoveryRequest{Target: 3, Source: ch.MQTTReplayPlanRequest{ChannelID: id, ExpectedChannelEpoch: current.ChannelEpoch, ExpectedLeaderEpoch: current.LeaderEpoch, ExpectedRouteGeneration: current.RouteGeneration, Generation: source.Generation}, TargetAnchor: anchor.Manifest.LastOffset, ScanLimit: 64}
	require.Eventually(t, func() bool {
		result, e := owner.StepChannelMQTTReplayRecovery(ctx, recovery)
		return e == nil && result.Plan.Complete
	}, 10*time.Second, 30*time.Millisecond)
	drive(func(task metadb.ChannelMigrationTask) bool {
		return task.Status == metadb.ChannelMigrationStatusCompleted
	})
	current, err = owner.GetChannelRuntimeMetaFresh(ctx, id.ID, int64(id.Type))
	require.NoError(t, err)
	require.ElementsMatch(t, []uint64{2, 3}, current.Replicas)
	require.ElementsMatch(t, []uint64{2, 3}, current.ISR)
	require.Empty(t, current.WriteFenceToken)
	created, err = store.CreateLeaderTransfer(ctx, channels.CreateLeaderTransferRequest{ChannelID: id, TaskID: "mqtt-transfer", DesiredLeader: 3})
	require.NoError(t, err)
	selected.taskID = created.TaskID
	drive(func(task metadb.ChannelMigrationTask) bool {
		return task.Status == metadb.ChannelMigrationStatusCompleted
	})
	current, err = owner.GetChannelRuntimeMetaFresh(ctx, id.ID, int64(id.Type))
	require.NoError(t, err)
	require.Equal(t, uint64(3), current.Leader)
	require.Empty(t, current.WriteFenceToken)
	ready, err := owner.ProbeChannel(ctx, 3, id.ID, id.Type)
	require.NoError(t, err)
	require.NotNil(t, ready.ReplayReadiness)
	require.True(t, ready.ReplayReadiness.Covered)
	require.False(t, ready.RecoveryRequired)
	t.Log("mqtt_migration_evidence: nodes=3 hash_slots=256 tcp=true disk=true slot_tasks=true native_complete_content_missing=true waiting_runnable=true fenced_recovery=true replica_replaced=true leader_transferred=true source_release=false product_listener=false")
}
