//go:build integration

package app

import (
	"context"
	"testing"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/internal/usecase/user"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Inbox qualification is an explicit fixture until the full directory projection
// is wired. Source protection/cursor commits, native SEND, shared replay and
// offline accounting use real cluster paths; this is not product acceptance.
func TestMQTTInboxSourceOfflineFirstPersonSingleNodeCluster(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.Cluster.Slots.HashSlotCount = 256
	a, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, a.Stop(ctx))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	require.NoError(t, a.Start(ctx))
	node := a.cluster.(*cluster.Node)
	waitSingleNodeClusterNodeSchedulable(t, node, cfg.NodeID)
	waitSingleNodeClusterRouteLeader(t, node, "alice", cfg.NodeID)
	require.NoError(t, node.UpsertDeviceMetadata(ctx, meta.Device{UID: "alice", DeviceFlag: 1, Token: "secret", DeviceLevel: 1}))
	owners, err := runtime.NewOwners(runtime.OwnerOptions{NodeID: cfg.NodeID, BootID: "inbox-source", Capacity: 4, MaxOperations: 8, PendingTimeout: time.Second, MaxLease: time.Minute, CloseRetry: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require.NoError(t, owners.Close(ctx))
	})
	sessions, err := sessioncase.New(sessioncase.Options{Store: node, Owners: owners, Isolation: owners, Tokens: user.New(user.Options{DeviceReader: mqttAcquisitionDeviceReader{node: node}}), Wills: mqttWillAuthorizer{messages: a.Messages()}, LeaseDuration: 30 * time.Second, CleanupTimeout: time.Second, SessionExpiryLimitSec: 86400, QuotaMessages: 100, QuotaBytes: 1 << 20, WindowLimit: 16})
	require.NoError(t, err)
	key := contract.Key{Namespace: "main", ClientID: "inbox-first-person"}
	connection, err := sessions.Connect(ctx, sessioncase.ConnectCommand{Key: key, UID: "alice", Token: "secret", DeviceFlag: 1, SessionExpirySec: 60, ReceiveMaximum: 16, MaxPacketBytes: 1 << 20, CloseTransport: func(context.Context) error { return nil }})
	require.NoError(t, err)
	auth, err := newMQTTReceiveAuthorization(node)
	require.NoError(t, err)
	projection := &mqttSubscriptionProjectionFixture{establish: func(context.Context, sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjectionReceipt, error) {
		return sessioncase.SubscriptionProjectionReceipt{}, sessioncase.ErrReplayPending
	}}
	subscriptions, err := sessioncase.NewSubscriptions(sessioncase.SubscriptionOptions{Store: node, Owners: owners, Authorization: auth, Projection: projection})
	require.NoError(t, err)
	request := sessioncase.SubscriptionRequest{Topic: "wk/v1/users/YWxpY2U/inbox", TargetID: "alice", TargetKind: meta.MQTTSubscriptionUserInbox, RequestedQoS: 1}
	_, err = subscriptions.Subscribe(ctx, connection.Owner, request)
	require.ErrorIs(t, err, sessioncase.ErrReplayPending)
	intent, err := node.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: key.Namespace, ClientID: key.ClientID, SessionGeneration: connection.Owner.SessionGeneration, Topic: request.Topic})
	require.NoError(t, err)
	require.Len(t, intent.Subscriptions, 1)
	sub := intent.Subscriptions[0]
	qualification := meta.MQTTSourceBinding{Key: meta.MQTTSourceBindingKey{Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingUID, ID: "alice"}, Namespace: key.Namespace, ClientID: key.ClientID, SessionGeneration: sub.SessionGeneration, SubscriptionGeneration: sub.Generation}, UID: "alice", Topic: sub.Topic, OperationID: sub.OperationID, Revision: 1, IntentRevision: sub.Revision, Stage: meta.MQTTBindingPreparing, UpdatedAtMS: time.Now().UnixMilli(), RecoveryAtMS: time.Now().UnixMilli()}
	result, err := node.CompareAndSwapMQTTSourceBinding(ctx, 0, qualification)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionCASApplied, result.Status)
	directory, err := node.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadInboxDirectory, Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingUID, ID: "alice"}, Limit: 64})
	require.NoError(t, err)
	require.True(t, directory.Done)
	require.Empty(t, directory.Directory)
	qualification.Revision, qualification.Stage, qualification.DiscoveryDone = 2, meta.MQTTBindingActive, true
	result, err = node.CompareAndSwapMQTTSourceBinding(ctx, 1, qualification)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionCASApplied, result.Status)
	projection.establish = func(_ context.Context, r sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjectionReceipt, error) {
		return mqttSubscriptionFixtureReceipt(r), nil
	}
	active, err := subscriptions.Reconcile(ctx, connection.Owner, sub.Topic)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSubscriptionActive, active.Stage)
	require.NoError(t, sessions.Disconnect(ctx, sessioncase.DisconnectCommand{Owner: connection.Owner, Normal: true}))
	channel := sessioncase.SourceChannel{ID: channelid.EncodePersonChannel("alice", "bob"), Type: 1}
	_, err = node.GetChannelRuntimeMetaFresh(ctx, channel.ID, 1)
	require.ErrorIs(t, err, meta.ErrNotFound)
	sources, err := newMQTTInboxSources(node, a.messageIDs)
	require.NoError(t, err)
	prepared, err := sources.Prepare(ctx, qualification.Key, channel)
	require.NoError(t, err)
	require.True(t, prepared.Needed)
	require.Equal(t, meta.MQTTBindingActive, prepared.Binding.Stage)
	require.Greater(t, prepared.Cursor.StartAfter, uint64(0), "replicated activation precedes the business message")
	sent, err := a.Messages().Send(ctx, message.SendCommand{FromUID: "bob", DeviceFlag: 1, ChannelID: channel.ID, ChannelType: 1, ClientMsgNo: "first-person", Payload: []byte("first offline person message"), Origin: message.SendOriginClient})
	require.NoError(t, err)
	require.Greater(t, sent.MessageSeq, prepared.Cursor.StartAfter)
	replay, err := newMQTTReplayWorker(node, a.messageIDs, runtime.ReplayWorkerOptions{HashSlotCount: 256, Interval: 20 * time.Millisecond, PagesPerTurn: 32})
	require.NoError(t, err)
	require.NoError(t, replay.Start(ctx))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, replay.Stop(ctx))
	})
	confirmation, err := newMQTTReplayCoordinator(node, a.messageIDs)
	require.NoError(t, err)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		require.NoError(c, confirmation.Confirm(ctx, prepared.Binding.Key.Owner, sent.MessageSeq))
	}, 10*time.Second, 20*time.Millisecond)
	accounting, err := newMQTTAccounting(node, auth)
	require.NoError(t, err)
	accounted, err := accounting.Account(ctx, prepared.Cursor.Key)
	require.NoError(t, err)
	require.EqualValues(t, 1, accounted.AddedMessages)
	require.EqualValues(t, len("first offline person message"), accounted.AddedBytes)
	state, err := node.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursor, CursorKey: prepared.Cursor.Key})
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSessionOffline, state.Session.State)
	require.EqualValues(t, 1, state.Session.PendingMessages)
	require.EqualValues(t, 1, state.DeliveryCursors[0].PendingMessages)
	require.Equal(t, prepared.Cursor.StartAfter, state.DeliveryCursors[0].StartAfter)
	t.Log("mqtt_inbox_source_evidence: nodes=1 hash_slots=256 qualification_fixture=true first_person_source=true offline_session=true real_source_protection=true real_cursor_commit=true native_send=true shared_replay=true offline_accounted=1 future_admission=false product_listener=false")
}
