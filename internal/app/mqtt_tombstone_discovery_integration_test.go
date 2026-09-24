//go:build integration

package app

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Controlled binding release leaves the real managed loop with no live consumer
// rows. Only discovery plus ordinary maintenance can retire the shared prefix.
func TestMQTTReplayWorkerRetiresSourceWithOnlyTombstones(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	voters := []cluster.ControlVoter{{NodeID: 1, Addr: freeSendackSmokeTCPAddr(t)}, {NodeID: 2, Addr: freeSendackSmokeTCPAddr(t)}, {NodeID: 3, Addr: freeSendackSmokeTCPAddr(t)}}
	var nodes []*cluster.Node
	var workers []*runtime.ReplayWorker
	t.Cleanup(func() {
		stop, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		for _, w := range workers {
			require.NoError(t, w.Stop(stop))
		}
		var joined sync.WaitGroup
		for _, n := range nodes {
			joined.Add(1)
			go func(n *cluster.Node) {
				defer joined.Done()
				if err := n.Stop(stop); err != nil {
					t.Errorf("stop: %v", err)
				}
			}(n)
		}
		joined.Wait()
	})
	for _, v := range voters {
		cfg := cluster.Config{NodeID: v.NodeID, ListenAddr: v.Addr, DataDir: filepath.Join(root, fmt.Sprintf("node-%d", v.NodeID)), Control: cluster.ControlConfig{ClusterID: "mqtt-tombstone-cleanup", Voters: voters, AllowBootstrap: true}, Slots: cluster.SlotConfig{InitialSlotCount: 2, HashSlotCount: 256, ReplicaCount: 3}}
		cfg.Channel.ReplicaCount = 3
		cfg.HealthReport.Interval = 200 * time.Millisecond
		n, err := cluster.New(cfg)
		require.NoError(t, err)
		nodes = append(nodes, n)
	}
	started := make(chan error, len(nodes))
	for _, n := range nodes {
		go func(n *cluster.Node) { started <- n.Start(ctx) }(n)
	}
	for range nodes {
		require.NoError(t, <-started)
	}
	require.Eventually(t, func() bool {
		for _, n := range nodes {
			probe, done := context.WithTimeout(ctx, 200*time.Millisecond)
			err := n.ProbeWriteReady(probe)
			done()
			if err != nil {
				return false
			}
		}
		return true
	}, 20*time.Second, 50*time.Millisecond)
	id := ch.ChannelID{ID: "last-consumer", Type: 2}
	seedGroupSendPermission(t, nodes[0], id, "alice")
	m := meta.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 2, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2}, MinISR: 2, Status: uint8(ch.StatusActive)}
	require.NoError(t, nodes[0].Propose(ctx, cluster.ProposeRequest{Key: id.ID, Command: metafsm.EncodeUpsertChannelRuntimeMetaCommand(m)}))
	ids := &mqttSourcePreparationIDs{}
	ids.next.Store(40000)
	protector, err := clusterinfra.NewMQTTSourceProtector(clusterinfra.MQTTSourceProtectorOptions{Node: nodes[0], MessageIDs: ids})
	require.NoError(t, err)
	source, err := protector.ProtectMQTTSource(ctx, sessioncase.SourceChannel{ID: id.ID, Type: id.Type})
	require.NoError(t, err)
	owner := meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: string(ch.ChannelKeyForID(id)), Generation: source.Generation}
	b := meta.MQTTSourceBinding{Key: meta.MQTTSourceBindingKey{Owner: owner, Namespace: "main", ClientID: "departed", SessionGeneration: 1, SubscriptionGeneration: 1}, UID: "alice", Topic: "topic", OperationID: "subscribe", Revision: 1, IntentRevision: 1, Stage: meta.MQTTBindingPreparing, RecoveryAtMS: 1000, UpdatedAtMS: 1000}
	written, err := nodes[0].CompareAndSwapMQTTSourceBinding(ctx, 0, b)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionCASApplied, written.Status)
	_, err = nodes[0].AppendChannel(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: ids.Next(), FromUID: "alice", Payload: []byte("pending shared cleanup"), ServerTimestampMS: time.Now().UnixMilli()}})
	require.NoError(t, err)
	coordinator, err := newMQTTReplayCoordinator(nodes[0], ids)
	require.NoError(t, err)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		seed, e := coordinator.Step(ctx, owner, contract.ReplayCursor{})
		require.NoError(c, e)
		require.True(c, seed.Anchored || seed.TargetComplete)
	}, 15*time.Second, 30*time.Millisecond)
	plan, err := nodes[0].PlanChannelMQTTReplay(ctx, ch.MQTTReplayPlanRequest{ChannelID: id, Generation: source.Generation, ExpectedChannelEpoch: 1, ExpectedLeaderEpoch: 1, ExpectedRouteGeneration: 1})
	require.NoError(t, err)
	require.True(t, plan.HasAnchor)
	// This fixture supplies release permission; production finalization remains
	// separate. Removing all index-4 witnesses is the behavior under test here.
	b.Revision, b.Stage, b.ProgressRevision, b.ReleaseReason = 2, meta.MQTTBindingRemoving, 2, meta.MQTTBindingSessionEnded
	written, err = nodes[0].CompareAndSwapMQTTSourceBinding(ctx, 1, b)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionCASApplied, written.Status)
	b.Revision, b.Stage, b.RecoveryAtMS, b.ProtectionRevision = 3, meta.MQTTBindingRemoved, 0, 2
	written, err = nodes[0].CompareAndSwapMQTTSourceBinding(ctx, 2, b)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionCASApplied, written.Status)
	route, err := nodes[0].RouteKey(id.ID)
	require.NoError(t, err)
	active, err := nodes[1].ReadMQTTRecovery(ctx, route.HashSlot, meta.MQTTRead{Kind: meta.MQTTReadSourceOwners, Limit: 1})
	require.NoError(t, err)
	require.Empty(t, active.SourceOwners)
	var decisions, completions atomic.Int32
	for _, n := range nodes {
		w, e := newMQTTReplayWorker(n, ids, runtime.ReplayWorkerOptions{Interval: 20 * time.Millisecond, PagesPerTurn: 32, Observe: func(o runtime.ReplayObservation) {
			decisions.Add(int32(o.RetirementCommits))
			completions.Add(int32(o.Completed))
		}})
		require.NoError(t, e)
		workers = append(workers, w)
		require.NoError(t, w.Start(ctx))
	}
	require.Eventually(t, func() bool { return decisions.Load() > 0 }, 15*time.Second, 20*time.Millisecond, "only tombstones remain: background discovery must still reach retirement")
	completed := completions.Load()
	require.Eventually(t, func() bool { return completions.Load() >= completed+6 }, 15*time.Second, 20*time.Millisecond)
	for _, w := range workers {
		require.NoError(t, w.Stop(ctx))
	}
	for i, n := range nodes {
		require.NoError(t, n.Stop(ctx))
		factory := channelstore.NewMessageDBFactory(filepath.Join(root, fmt.Sprintf("node-%d", i+1), "messages"))
		func() {
			defer factory.Close()
			store, e := factory.ChannelStore(ch.ChannelKeyForID(id), id)
			require.NoError(t, e)
			defer store.Close()
			decision, found, e := store.(channelstore.MQTTReplayLatestRetirementReader).LoadLatestMQTTReplayRetirement(ctx, source.Generation)
			require.NoError(t, e)
			require.True(t, found, "replica %d must durably learn cleanup after the last consumer left", i+1)
			require.Equal(t, plan.Anchor.Anchor, decision.Retirement.Anchor)
			ready, e := store.(channelstore.MQTTReplayReadinessReader).ReadMQTTReplayReadiness(ctx, decision.Manifest.LastOffset)
			require.NoError(t, e)
			require.True(t, ready.Covered)
			_, e = store.(channelstore.MQTTReplayAnchorTransfer).ExportMQTTReplayAnchor(ctx, plan.Anchor.Manifest.LastOffset, ch.MQTTReplayRange{Generation: source.Generation, From: plan.Anchor.Anchor.StartAfter + 1, Through: plan.Anchor.Anchor.Through, Limit: 256, MaxBytes: 1 << 20})
			require.Error(t, e, "read-only export must reject automatically retired content on replica %d", i+1)
		}()
	}
	t.Log("mqtt_tombstone_cleanup_evidence: nodes=3 hash_slots=256 no_active_binding=true managed_retirement=true all_replica_reopen=true applied_baseline=true retained_coverage=true binding_release=controlled product_listener=false")
}
