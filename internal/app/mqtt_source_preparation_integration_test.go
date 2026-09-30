//go:build integration

package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/internal/usecase/user"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	gr "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mqttSourcePreparationIDs struct{ next atomic.Uint64 }

func (i *mqttSourcePreparationIDs) Next() uint64 { return i.next.Add(1) }

type mqttSourcePreparationLostReply struct {
	*cluster.Node
	lose bool
}

func (s *mqttSourcePreparationLostReply) MutateMQTTDeliveryCursor(ctx context.Context, m meta.MQTTDeliveryCursorMutation) (meta.MQTTDeliveryCursorResult, error) {
	r, e := s.Node.MutateMQTTDeliveryCursor(ctx, m)
	if e == nil && s.lose {
		s.lose = false
		return meta.MQTTDeliveryCursorResult{}, context.DeadlineExceeded
	}
	return r, e
}

// Real source/Session Slot commits and Channel protection are composed here.
// Receive authority uses native membership. The separate group projection helper
// additionally verifies concrete establishment and removal receipts.
func TestMQTTGroupSourcePreparationThreeNodeRecovery(t *testing.T) {
	rootDir := t.TempDir() // Node shutdown must run before directory removal.
	voters := []cluster.ControlVoter{{NodeID: 1, Addr: freeSendackSmokeTCPAddr(t)}, {NodeID: 2, Addr: freeSendackSmokeTCPAddr(t)}, {NodeID: 3, Addr: freeSendackSmokeTCPAddr(t)}}
	var nodes []*cluster.Node
	var owners []*runtime.Owners
	var sessions []*sessioncase.App
	var replayWorkers []*runtime.ReplayWorker
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, w := range replayWorkers {
			require.NoError(t, w.Stop(ctx))
		}
		for _, o := range owners {
			require.NoError(t, o.Close(ctx))
		}
		var wg sync.WaitGroup
		for _, n := range nodes {
			wg.Add(1)
			go func(n *cluster.Node) {
				defer wg.Done()
				if err := n.Stop(ctx); err != nil {
					t.Errorf("node stop: %v", err)
				}
			}(n)
		}
		wg.Wait()
	})
	for _, v := range voters {
		cfg := cluster.Config{NodeID: v.NodeID, ListenAddr: v.Addr, DataDir: filepath.Join(rootDir, fmt.Sprintf("node-%d", v.NodeID)), Control: cluster.ControlConfig{ClusterID: "mqtt-source-preparation", Voters: voters, AllowBootstrap: true}, Slots: cluster.SlotConfig{InitialSlotCount: 2, HashSlotCount: 256, ReplicaCount: 3}}
		cfg.Channel.ReplicaCount = 3
		cfg.HealthReport.Interval = 200 * time.Millisecond
		n, err := cluster.New(cfg)
		require.NoError(t, err)
		nodes = append(nodes, n)
		o, err := runtime.NewOwners(runtime.OwnerOptions{NodeID: v.NodeID, BootID: fmt.Sprintf("source-preparation-%d", v.NodeID), Capacity: 8, MaxOperations: 8, PendingTimeout: time.Minute, MaxLease: time.Minute, CloseRetry: time.Second})
		require.NoError(t, err)
		owners = append(owners, o)
		n.RegisterRPC(accessnode.MQTTOwnerRPCServiceID, accessnode.MQTTOwnerRPC{Owners: o})
		s, err := sessioncase.New(sessioncase.Options{Store: n, Owners: o, Isolation: accessnode.NewMQTTOwnerClient(n), Tokens: user.New(user.Options{DeviceReader: mqttAcquisitionDeviceReader{node: n}}), Wills: mqttWillAuthorizer{}, LeaseDuration: 30 * time.Second, CleanupTimeout: time.Second, SessionExpiryLimitSec: 86400, QuotaMessages: 100, QuotaBytes: 1 << 20, WindowLimit: 16})
		require.NoError(t, err)
		sessions = append(sessions, s)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	started := make(chan error, 3)
	for _, n := range nodes {
		go func(n *cluster.Node) { started <- n.Start(ctx) }(n)
	}
	for range nodes {
		require.NoError(t, <-started)
	}
	require.Eventually(t, func() bool {
		for _, n := range nodes {
			c, done := context.WithTimeout(ctx, 200*time.Millisecond)
			err := n.ProbeWriteReady(c)
			done()
			if err != nil {
				return false
			}
		}
		return true
	}, 20*time.Second, 50*time.Millisecond)
	id := ch.ChannelID{ID: "group", Type: 2}
	seedGroupSendPermission(t, nodes[0], id, "alice")
	runtimeMeta := meta.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 2, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2}, MinISR: 2, Status: uint8(ch.StatusActive)}
	require.NoError(t, nodes[0].Propose(ctx, cluster.ProposeRequest{Key: id.ID, Command: metafsm.EncodeUpsertChannelRuntimeMetaCommand(runtimeMeta)}))
	require.NoError(t, nodes[0].UpsertDeviceMetadata(ctx, meta.Device{UID: "alice", DeviceFlag: 1, DeviceLevel: 1, Token: "secret"}))
	cmd := sessioncase.ConnectCommand{Key: contract.Key{Namespace: "main", ClientID: "prepared"}, UID: "alice", Token: "secret", DeviceFlag: 1, SessionExpirySec: 60, ReceiveMaximum: 16, MaxPacketBytes: 1 << 20, CloseTransport: func(context.Context) error { return nil }}
	first, err := sessions[0].Connect(ctx, cmd)
	require.NoError(t, err)
	authorize, err := newMQTTReceiveAuthorization(nodes[0])
	require.NoError(t, err)
	subs, err := sessioncase.NewSubscriptions(sessioncase.SubscriptionOptions{Store: nodes[0], Owners: owners[0], Authorization: authorize, Projection: &mqttSubscriptionProjectionFixture{establish: func(context.Context, sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjectionReceipt, error) {
		return sessioncase.SubscriptionProjectionReceipt{}, sessioncase.ErrEvidence
	}}})
	require.NoError(t, err)
	topic := "wk/v1/groups/Z3JvdXA/messages"
	_, err = subs.Subscribe(ctx, first.Owner, sessioncase.SubscriptionRequest{Topic: topic, TargetKind: meta.MQTTSubscriptionGroup, TargetID: id.ID, RequestedQoS: 1})
	require.Error(t, err)
	ids := &mqttSourcePreparationIDs{}
	ids.next.Store(10000)
	adapter, err := clusterinfra.NewMQTTSourceProtector(clusterinfra.MQTTSourceProtectorOptions{Node: nodes[0], MessageIDs: ids})
	require.NoError(t, err)
	sources, err := sessioncase.NewGroupSources(sessioncase.GroupSourceOptions{Store: &mqttSourcePreparationLostReply{Node: nodes[0], lose: true}, Owners: owners[0], Authorization: authorize, Sources: adapter})
	require.NoError(t, err)
	_, err = sources.Prepare(ctx, first.Owner, topic)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	read, err := nodes[2].ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: "main", ClientID: "prepared", SessionGeneration: first.Owner.SessionGeneration, Topic: topic})
	require.NoError(t, err)
	require.Len(t, read.Subscriptions, 1)
	intent := read.Subscriptions[0]
	cursors, err := nodes[2].ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursors, Namespace: "main", ClientID: "prepared", SessionGeneration: first.Owner.SessionGeneration, SubscriptionGeneration: intent.Generation, Limit: 2})
	require.NoError(t, err)
	require.Len(t, cursors.DeliveryCursors, 1)
	boundary := cursors.DeliveryCursors[0].StartAfter
	_, err = nodes[0].AppendChannel(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: 20001, FromUID: "alice", Payload: []byte("after interrupted preparation"), ServerTimestampMS: time.Now().UnixMilli()}})
	require.NoError(t, err)
	_, err = nodes[0].AppendChannel(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: 20003, FromUID: "alice", Payload: []byte("second publication"), ServerTimestampMS: time.Now().UnixMilli()}})
	require.NoError(t, err)
	resumed, err := sessions[2].Connect(ctx, cmd)
	require.NoError(t, err)
	require.True(t, resumed.SessionPresent)
	adapter, err = clusterinfra.NewMQTTSourceProtector(clusterinfra.MQTTSourceProtectorOptions{Node: nodes[2], MessageIDs: ids})
	require.NoError(t, err)
	sources, err = sessioncase.NewGroupSources(sessioncase.GroupSourceOptions{Store: nodes[2], Owners: owners[2], Authorization: authorize, Sources: adapter})
	require.NoError(t, err)
	prepared, err := sources.Prepare(ctx, resumed.Owner, topic)
	require.NoError(t, err)
	require.Equal(t, boundary, prepared.Cursor.StartAfter)
	require.Equal(t, meta.MQTTBindingActive, prepared.Binding.Stage)
	read, err = nodes[1].ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: "main", ClientID: "prepared", SessionGeneration: resumed.Owner.SessionGeneration, Topic: topic})
	require.NoError(t, err)
	require.Equal(t, intent, read.Subscriptions[0])
	// Discover the source from durable obligations, then run the production turn
	// coordinator through fresh metadata, copy/anchor RPCs and learner recovery.
	route, err := nodes[0].RouteKey(id.ID)
	require.NoError(t, err)
	discovered, err := nodes[0].ReadMQTTRecovery(ctx, route.HashSlot, meta.MQTTRead{Kind: meta.MQTTReadSourceOwners, Limit: 64})
	require.NoError(t, err)
	require.Equal(t, []meta.MQTTBindingOwner{prepared.Binding.Key.Owner}, discovered.SourceOwners)
	// Establish one accepted anchor while the learner still lacks shared content,
	// then require the managed workers to recover it with business writes fenced.
	coordinator, err := newMQTTReplayCoordinator(nodes[0], ids)
	require.NoError(t, err)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		seed, e := coordinator.Step(ctx, prepared.Binding.Key.Owner, contract.ReplayCursor{})
		require.NoError(c, e)
		// An uncertain previous commit may already have installed the anchor;
		// a verified recovery result proves the same setup condition on retry.
		require.True(c, seed.Anchored || seed.TargetComplete)
	}, 15*time.Second, 30*time.Millisecond)
	runtimeMeta.WriteFenceToken, runtimeMeta.WriteFenceVersion = "replay-migration", 1
	runtimeMeta.WriteFenceReason = uint8(ch.WriteFenceReasonReplicaReplace)
	runtimeMeta.WriteFenceUntilMS = time.Now().Add(time.Minute).UnixMilli()
	require.NoError(t, nodes[0].Propose(ctx, cluster.ProposeRequest{Key: id.ID, Command: metafsm.EncodeUpsertChannelRuntimeMetaCommand(runtimeMeta)}))
	runtimeMeta, err = nodes[0].GetChannelRuntimeMetaFresh(ctx, id.ID, int64(id.Type))
	require.NoError(t, err)
	require.Equal(t, "replay-migration", runtimeMeta.WriteFenceToken)
	// The migration cutover applies fenced metadata before draining the leader;
	// this fixture follows that same runtime-application boundary.
	require.NoError(t, nodes[0].ApplyChannelMeta(ctx, runtimeMeta.Leader, runtimeMeta))
	_, err = nodes[0].AppendChannel(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: 20002, FromUID: "alice", Payload: []byte("fenced"), ServerTimestampMS: time.Now().UnixMilli()}})
	require.True(t, errors.Is(err, ch.ErrWriteFenced) || errors.Is(err, ch.ErrNotReady), "new authority must reject business admission: %v", err)
	var anchored, repaired, completed, attempts, failures atomic.Int32
	for _, n := range nodes {
		w, e := newMQTTReplayWorker(n, ids, runtime.ReplayWorkerOptions{Registry: gr.New(), HashSlotCount: 256, Interval: 20 * time.Millisecond, PagesPerTurn: 32, Observe: func(o runtime.ReplayObservation) {
			anchored.Add(int32(o.Anchored))
			repaired.Add(int32(o.Repaired))
			completed.Add(int32(o.Completed))
			attempts.Add(int32(o.Attempts))
			failures.Add(int32(o.Failures))
		}}, nil)
		require.NoError(t, e)
		replayWorkers = append(replayWorkers, w)
		require.NoError(t, w.Start(ctx))
	}
	if !assert.Eventually(t, func() bool { return repaired.Load() > 0 && completed.Load() >= 3 }, 10*time.Second, 30*time.Millisecond) {
		t.Logf("fenced recovery: attempts=%d failures=%d anchored=%d repaired=%d complete=%d authority=%+v", attempts.Load(), failures.Load(), anchored.Load(), repaired.Load(), completed.Load(), runtimeMeta)
		for i, n := range nodes {
			probe, e := n.ChannelRuntimeProbe(ctx, ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{id}})
			t.Logf("node %d runtime: %+v err=%v", i+1, probe, e)
		}
		p, e := nodes[0].PlanChannelMQTTReplay(ctx, ch.MQTTReplayPlanRequest{ChannelID: id, ExpectedChannelEpoch: runtimeMeta.ChannelEpoch, ExpectedLeaderEpoch: runtimeMeta.LeaderEpoch, ExpectedRouteGeneration: runtimeMeta.RouteGeneration, Generation: prepared.Binding.Key.Owner.Generation})
		t.Logf("fenced plan: %+v err=%v", p, e)
		t.FailNow()
	}
	// Require actual physical retention on all three replicas while the managed
	// worker is running. This read/write path does not request source release;
	// success therefore proves the background coordinator performed it.
	planRequest := ch.MQTTReplayPlanRequest{ChannelID: id, ExpectedChannelEpoch: runtimeMeta.ChannelEpoch, ExpectedLeaderEpoch: runtimeMeta.LeaderEpoch, ExpectedRouteGeneration: runtimeMeta.RouteGeneration, Generation: prepared.Binding.Key.Owner.Generation}
	plan, err := nodes[0].PlanChannelMQTTReplay(ctx, planRequest)
	require.NoError(t, err)
	require.True(t, plan.HasAnchor)
	// Native quorum replication can keep a follower only on disk. Retention's
	// runtime facade needs its current role installed before exercising cleanup;
	// this activates no MQTT source release and copies no shared content.
	for _, replica := range runtimeMeta.Replicas {
		require.NoError(t, nodes[0].ApplyChannelMeta(ctx, replica, runtimeMeta))
	}
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		for _, n := range nodes {
			trim, e := n.ApplyChannelRetentionBoundary(ctx, id, plan.Anchor.Manifest.LastOffset, ch.RetentionApplyOptions{MaxTrimMessages: 256, MaxTrimBytes: 1 << 20})
			require.NoError(c, e)
			require.Equal(c, plan.Anchor.Anchor.Through, trim.PhysicalRetentionThroughSeq)
			require.Equal(c, plan.Anchor.Manifest.LastOffset, trim.LocalRetentionThroughSeq)
		}
	}, 10*time.Second, 30*time.Millisecond)
	for _, w := range replayWorkers {
		require.NoError(t, w.Stop(ctx))
	}
	require.Zero(t, anchored.Load(), "fenced workers must never create a new anchor")
	// The worker already imported learner content. This independently verifies
	// coverage and must not perform an import on the test's behalf.
	covered, err := nodes[0].StepChannelMQTTReplayRecovery(ctx, ch.MQTTReplayRecoveryRequest{Target: 3, Source: planRequest, TargetAnchor: plan.Anchor.Manifest.LastOffset, ScanLimit: 64})
	require.NoError(t, err)
	require.True(t, covered.Plan.Complete)
	require.False(t, covered.Repaired)
	ready, err := nodes[0].ProbeChannel(ctx, 3, id.ID, id.Type)
	require.NoError(t, err)
	require.NotNil(t, ready.ReplayReadiness)
	require.True(t, ready.ReplayReadiness.Covered)
	require.True(t, ready.WriteFence.Set())
	// Both origins forward to node 2. Bounded consumer pages must survive
	// physical original-log trim and a stable migration fence without repair.
	consumerRead := ch.MQTTReplayConsumerRequest{AnchorPosition: plan.Anchor.Manifest.LastOffset, Request: ch.MQTTReplayRequest{
		ChannelID: id, ExpectedChannelEpoch: planRequest.ExpectedChannelEpoch,
		ExpectedLeaderEpoch: planRequest.ExpectedLeaderEpoch, ExpectedRouteGeneration: planRequest.ExpectedRouteGeneration,
		Range: ch.MQTTReplayRange{Generation: planRequest.Generation, From: prepared.Cursor.StartAfter + 1, Through: plan.Anchor.Anchor.Through, Limit: 1, MaxBytes: 1 << 20},
	}}
	firstPage, err := nodes[0].ReadChannelMQTTReplay(ctx, consumerRead)
	require.NoError(t, err)
	require.True(t, firstPage.ValidFor(id, consumerRead.Request.Range))
	require.Len(t, firstPage.Records, 1)
	require.Equal(t, uint64(20001), firstPage.Records[0].Message.MessageID)
	require.False(t, firstPage.Records[0].Internal)
	require.Equal(t, []byte("after interrupted preparation"), firstPage.Records[0].Message.Payload)
	require.Equal(t, "alice", firstPage.Records[0].Message.FromUID)
	require.Less(t, firstPage.After.Through, plan.Anchor.Anchor.Through)
	otherPage, err := nodes[2].ReadChannelMQTTReplay(ctx, consumerRead)
	require.NoError(t, err)
	require.Equal(t, firstPage, otherPage)
	firstPage.Records[0].Message.Payload[0] ^= 0xff
	againPage, err := nodes[0].ReadChannelMQTTReplay(ctx, consumerRead)
	require.NoError(t, err)
	require.Equal(t, otherPage, againPage, "caller mutation must not change shared content")
	consumerRead.Request.Range.From = otherPage.After.Through + 1
	nextPage, err := nodes[2].ReadChannelMQTTReplay(ctx, consumerRead)
	require.NoError(t, err)
	require.True(t, nextPage.ValidFor(id, consumerRead.Request.Range))
	require.Len(t, nextPage.Records, 1)
	require.Equal(t, uint64(20003), nextPage.Records[0].Message.MessageID)
	require.Equal(t, otherPage.After, nextPage.Before)
	consumerRead.AnchorPosition += 100
	unprovenPage, err := nodes[0].ReadChannelMQTTReplay(ctx, consumerRead)
	require.Error(t, err)
	require.Zero(t, unprovenPage)
	_, err = nodes[0].AppendChannel(ctx, ch.AppendRequest{ChannelID: id, Message: ch.Message{MessageID: 20002, FromUID: "alice", Payload: []byte("fenced"), ServerTimestampMS: time.Now().UnixMilli()}})
	require.True(t, errors.Is(err, ch.ErrWriteFenced) || errors.Is(err, ch.ErrNotReady), "recovered fenced authority must still reject writes: %v", err)
	_, err = sources.Prepare(ctx, first.Owner, topic)
	require.Error(t, err)
	// The controlled migration has verified all replica coverage. Clear its
	// fixture-owned fence before exercising consumer-authorized retirement.
	runtimeMeta.WriteFenceToken, runtimeMeta.WriteFenceReason, runtimeMeta.WriteFenceUntilMS = "", 0, 0
	runtimeMeta.WriteFenceVersion++
	require.NoError(t, nodes[0].Propose(ctx, cluster.ProposeRequest{Key: id.ID, Command: metafsm.EncodeUpsertChannelRuntimeMetaCommand(runtimeMeta)}))
	for _, replica := range runtimeMeta.Replicas {
		require.NoError(t, nodes[0].ApplyChannelMeta(ctx, replica, runtimeMeta))
	}
	progress := verifyMQTTConsumerProgress(t, ctx, nodes, owners[2], resumed, sessions[2], prepared, ids, plan.Anchor, authorize)
	verifyMQTTSourceDrain(t, ctx, nodes, owners, sessions, authorize, adapter)
	verifyMQTTPendingRemoval(t, ctx, nodes, owners, sessions, authorize)
	verifyMQTTPendingEstablishment(t, ctx, nodes, owners, sessions, authorize)
	verifyMQTTSessionReclamation(t, ctx, nodes, owners, sessions, authorize)
	verifyMQTTGroupProjection(t, ctx, nodes, owners, sessions, authorize, ids)
	verifyMQTTReceiveRejoin(t, ctx, nodes, owners, sessions, replayWorkers, ids)
	require.NoError(t, nodes[0].RemoveChannelSubscribers(ctx, id.ID, 2, []string{"alice"}, 2))
	_, err = sources.Prepare(ctx, resumed.Owner, topic)
	require.ErrorIs(t, err, sessioncase.ErrSubscriptionDenied)
	zero := uint32(0)
	require.NoError(t, sessions[2].Disconnect(ctx, sessioncase.DisconnectCommand{Owner: resumed.Owner, Normal: true, SessionExpirySec: &zero}))
	ended, err := progress.Reconcile(ctx, prepared.Binding.Key)
	require.NoError(t, err)
	require.True(t, ended.Changed)
	require.True(t, ended.NeedsRemoval)
	require.Equal(t, meta.MQTTBindingRemoving, ended.Binding.Stage)
	require.Equal(t, meta.MQTTBindingSessionEnded, ended.Binding.ReleaseReason)
	require.Equal(t, prepared.Binding.ProtectionRevision, ended.Binding.ProtectionRevision)
	verifyMQTTSourceRemoval(t, ctx, nodes, ended.Binding)
	// Reopen each stopped replica and inspect its own durable decision/coverage.
	// These read-only checks cannot apply retirement on the worker's behalf.
	for i, node := range nodes {
		require.NoError(t, node.Stop(ctx))
		factory := channelstore.NewMessageDBFactory(filepath.Join(rootDir, fmt.Sprintf("node-%d", i+1), "messages"))
		func() {
			defer factory.Close()
			store, e := factory.ChannelStore(ch.ChannelKeyForID(id), id)
			require.NoError(t, e)
			defer store.Close()
			state, e := store.Load(ctx)
			require.NoError(t, e)
			t.Logf("mqtt_retirement_reopen: replica=%d state=%+v", i+1, state)
			decision, found, e := store.(channelstore.MQTTReplayLatestRetirementReader).LoadLatestMQTTReplayRetirement(ctx, prepared.Binding.Key.Owner.Generation)
			require.NoError(t, e)
			require.True(t, found, "replica %d must durably learn the retirement decision", i+1)
			require.Equal(t, plan.Anchor.Anchor, decision.Retirement.Anchor)
			reader := store.(channelstore.MQTTReplayReadinessReader)
			_, e = reader.ReadMQTTReplayReadiness(ctx, plan.Anchor.Manifest.LastOffset)
			require.Error(t, e, "the applied newer baseline cannot be used at historical HW on replica %d", i+1)
			ready, e := reader.ReadMQTTReplayReadiness(ctx, decision.Manifest.LastOffset)
			require.NoError(t, e)
			require.True(t, ready.Covered, "replica %d retains valid coverage after automatic retirement", i+1)
			_, e = store.(channelstore.MQTTReplayAnchorTransfer).ExportMQTTReplayAnchor(ctx, plan.Anchor.Manifest.LastOffset, ch.MQTTReplayRange{Generation: prepared.Binding.Key.Owner.Generation, From: plan.Anchor.Anchor.StartAfter + 1, Through: plan.Anchor.Anchor.Through, Limit: 256, MaxBytes: 1 << 20})
			require.Error(t, e, "replica %d must refuse retired content without applying GC during verification", i+1)
		}()
	}
	t.Log("mqtt_automatic_retirement_evidence: background_first_commit=true all_replica_baselines_applied=true independent_disk_reopen=true historical_cut_rejected=true current_coverage=true physical_compaction_unasserted=true product_listener=false")
	t.Log("mqtt_source_preparation_evidence: nodes=3 hash_slots=256 tcp=true disk=true remote_channel_protection=true cursor_commit_reply_lost=true owner_1_to_3=true original_boundary_preserved=true subscription_preparing_before_controlled_window_admission=true permission_incarnation=native distinct_source_discovery=true replay_turn_coordinator=true learner_content_recovered=true automatic_scheduler=true source_release_all_replicas=true original_trim=true write_fenced_recovery=true writes_remain_fenced=true session_end_retained_removal=true full_projection=false product_listener=false")
}
