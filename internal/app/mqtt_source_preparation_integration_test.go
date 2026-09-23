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

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/internal/usecase/user"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
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
// Permission incarnation is controlled; no full projection receipt is minted.
func TestMQTTGroupSourcePreparationThreeNodeRecovery(t *testing.T) {
	rootDir := t.TempDir() // Node shutdown must run before directory removal.
	voters := []cluster.ControlVoter{{NodeID: 1, Addr: freeSendackSmokeTCPAddr(t)}, {NodeID: 2, Addr: freeSendackSmokeTCPAddr(t)}, {NodeID: 3, Addr: freeSendackSmokeTCPAddr(t)}}
	var nodes []*cluster.Node
	var owners []*runtime.Owners
	var sessions []*sessioncase.App
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
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
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
	runtimeMeta := meta.ChannelRuntimeMeta{ChannelID: id.ID, ChannelType: 2, ChannelEpoch: 1, LeaderEpoch: 1, RouteGeneration: 1, Leader: 2, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2, 3}, MinISR: 2, Status: uint8(ch.StatusActive)}
	require.NoError(t, nodes[0].Propose(ctx, cluster.ProposeRequest{Key: id.ID, Command: metafsm.EncodeUpsertChannelRuntimeMetaCommand(runtimeMeta)}))
	require.NoError(t, nodes[0].UpsertDeviceMetadata(ctx, meta.Device{UID: "alice", DeviceFlag: 1, DeviceLevel: 1, Token: "secret"}))
	cmd := sessioncase.ConnectCommand{Key: contract.Key{Namespace: "main", ClientID: "prepared"}, UID: "alice", Token: "secret", DeviceFlag: 1, SessionExpirySec: 60, ReceiveMaximum: 16, MaxPacketBytes: 1 << 20, CloseTransport: func(context.Context) error { return nil }}
	first, err := sessions[0].Connect(ctx, cmd)
	require.NoError(t, err)
	authorize := mqttSubscriptionAuthorizationFixture(func(c context.Context, uid string, r sessioncase.SubscriptionRequest) (uint64, error) {
		channel, e := nodes[0].GetChannelMetadataAuthoritative(c, r.TargetID, 2)
		if e != nil {
			return 0, e
		}
		member, e := nodes[0].ContainsChannelSubscriberAuthoritative(c, r.TargetID, 2, uid)
		if e != nil {
			return 0, e
		}
		if channel.Disband != 0 || !member {
			return 0, sessioncase.ErrSubscriptionDenied
		}
		return 7, nil
	})
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
	_, err = sources.Prepare(ctx, first.Owner, topic)
	require.Error(t, err)
	require.NoError(t, nodes[0].RemoveChannelSubscribers(ctx, id.ID, 2, []string{"alice"}, 2))
	_, err = sources.Prepare(ctx, resumed.Owner, topic)
	require.ErrorIs(t, err, sessioncase.ErrSubscriptionDenied)
	t.Log("mqtt_source_preparation_evidence: nodes=3 hash_slots=256 tcp=true disk=true remote_channel_protection=true cursor_commit_reply_lost=true owner_1_to_3=true original_boundary_preserved=true subscription_still_preparing=true permission_incarnation=controlled full_projection=false product_listener=false")
}
