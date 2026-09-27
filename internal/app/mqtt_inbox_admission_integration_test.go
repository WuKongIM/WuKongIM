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

// The first two variants retain explicit qualification fixtures. Establishment
// covers a real initial directory and future source through native SEND; full
// product listener and safe inbox removal remain separate work.
func TestMQTTInboxAdmissionOfflineDirectoryBeforeFirstPersonSingleNodeCluster(t *testing.T) {
	runMQTTInboxFirstPerson(t, false, false)
}

func TestMQTTInboxAppenderOfflineDirectoryBeforeFirstPersonSingleNodeCluster(t *testing.T) {
	runMQTTInboxFirstPerson(t, true, false)
}

func TestMQTTInboxEstablishmentExistingAndFutureOfflinePersonSingleNodeCluster(t *testing.T) {
	runMQTTInboxFirstPerson(t, true, true)
}

func runMQTTInboxFirstPerson(t *testing.T, automatic, establish bool) {
	t.Helper()
	cfg := singleNodeClusterAppConfig(t)
	cfg.Cluster.Slots.HashSlotCount = 256
	a, err := New(cfg, func(a *App) { a.mqttInboxWrites = automatic })
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
	replay, err := newMQTTReplayWorker(node, a.messageIDs, runtime.ReplayWorkerOptions{HashSlotCount: 256, Interval: 20 * time.Millisecond, PagesPerTurn: 32})
	require.NoError(t, err)
	require.NoError(t, replay.Start(ctx))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, replay.Stop(ctx))
	})
	existingChannel := channelid.EncodePersonChannel("alice", "carol")
	var existing message.SendResult
	if establish {
		existing, err = a.Messages().Send(ctx, message.SendCommand{FromUID: "carol", DeviceFlag: 1, ChannelID: existingChannel, ChannelType: 1, ClientMsgNo: "before-inbox", Payload: []byte("before subscription"), Origin: message.SendOriginClient})
		require.NoError(t, err)
	}
	projection := &mqttSubscriptionProjectionFixture{establish: func(context.Context, sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjectionReceipt, error) {
		return sessioncase.SubscriptionProjectionReceipt{}, sessioncase.ErrReplayPending
	}}
	if establish {
		initial, initialErr := newMQTTInboxEstablishment(node, owners, auth, a.messageIDs)
		require.NoError(t, initialErr)
		projection.establish = initial.Establish
	}
	subscriptions, err := sessioncase.NewSubscriptions(sessioncase.SubscriptionOptions{Store: node, Owners: owners, Authorization: auth, Projection: projection})
	require.NoError(t, err)
	request := sessioncase.SubscriptionRequest{Topic: "wk/v1/users/YWxpY2U/inbox", TargetID: "alice", TargetKind: meta.MQTTSubscriptionUserInbox, RequestedQoS: 1}
	var sub meta.MQTTSubscription
	if establish {
		control, controlErr := sessioncase.NewSubscriptionRequests(sessioncase.SubscriptionRequestOptions{Subscriptions: subscriptions})
		require.NoError(t, controlErr)
		sub, err = control.Subscribe(ctx, connection.Owner, request)
		require.NoError(t, err)
		require.Equal(t, meta.MQTTSubscriptionActive, sub.Stage)
		qualificationKey := meta.MQTTSourceBindingKey{Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingUID, ID: "alice"}, Namespace: key.Namespace, ClientID: key.ClientID, SessionGeneration: sub.SessionGeneration, SubscriptionGeneration: sub.Generation}
		qualification, readErr := node.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: qualificationKey})
		require.NoError(t, readErr)
		require.Len(t, qualification.Bindings, 1)
		require.Equal(t, meta.MQTTBindingActive, qualification.Bindings[0].Stage)
		require.True(t, qualification.Bindings[0].DiscoveryDone)
		initial, readErr := node.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursors, Namespace: key.Namespace, ClientID: key.ClientID, SessionGeneration: sub.SessionGeneration, SubscriptionGeneration: sub.Generation, Limit: 64})
		require.NoError(t, readErr)
		require.Len(t, initial.DeliveryCursors, 1)
		require.Equal(t, "1:"+existingChannel, initial.DeliveryCursors[0].Key.SourceID)
		require.Greater(t, initial.DeliveryCursors[0].StartAfter, existing.MessageSeq, "subscription starts after pre-existing business content")
	} else {
		_, err = subscriptions.Subscribe(ctx, connection.Owner, request)
		require.ErrorIs(t, err, sessioncase.ErrReplayPending)
		intent, err := node.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: key.Namespace, ClientID: key.ClientID, SessionGeneration: connection.Owner.SessionGeneration, Topic: request.Topic})
		require.NoError(t, err)
		require.Len(t, intent.Subscriptions, 1)
		sub = intent.Subscriptions[0]
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
	}
	require.NoError(t, sessions.Disconnect(ctx, sessioncase.DisconnectCommand{Owner: connection.Owner, Normal: true}))
	channel := sessioncase.SourceChannel{ID: channelid.EncodePersonChannel("alice", "bob"), Type: 1}
	_, err = node.GetChannelRuntimeMetaFresh(ctx, channel.ID, 1)
	require.ErrorIs(t, err, meta.ErrNotFound)
	var sent message.SendResult
	sendFirst := func() {
		t.Helper()
		var sendErr error
		sent, sendErr = a.Messages().Send(ctx, message.SendCommand{FromUID: "bob", DeviceFlag: 1, ChannelID: channel.ID, ChannelType: 1, ClientMsgNo: "first-person", Payload: []byte("first offline person message"), Origin: message.SendOriginClient})
		require.NoError(t, sendErr)
	}
	if automatic {
		sendFirst()
	} else {
		admission, admissionErr := newMQTTInboxAdmission(node, a.messageIDs)
		require.NoError(t, admissionErr)
		results := node.AdmitPersonDirectoryTasks(ctx, []meta.PersonDirectoryTask{{ChannelID: channel.ID, ChannelType: 1, CreatedAt: time.Now().UnixMilli()}})
		require.Equal(t, []error{nil}, results)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			progress, stepErr := admission.Advance(ctx, channel)
			require.NoError(c, stepErr)
			require.True(c, progress.Ready)
		}, 10*time.Second, 25*time.Millisecond)
	}
	admitted, err := node.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadInboxAdmission, AdmissionChannel: channel.ID})
	require.NoError(t, err)
	require.NotNil(t, admitted.Admission)
	require.NotNil(t, admitted.Admission.Checkpoint)
	require.Equal(t, uint8(2), admitted.Admission.Checkpoint.Participant)
	for _, uid := range []string{"alice", "bob"} {
		directory, readErr := node.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadInboxDirectory, Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingUID, ID: uid}, Limit: 64})
		require.NoError(t, readErr)
		require.Contains(t, directory.Directory, meta.ChannelKey{ChannelID: channel.ID, ChannelType: 1})
	}
	cursors, err := node.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursors, Namespace: key.Namespace, ClientID: key.ClientID, SessionGeneration: sub.SessionGeneration, SubscriptionGeneration: sub.Generation, Limit: 64})
	require.NoError(t, err)
	wantCursors := 1
	if establish {
		wantCursors = 2
	}
	require.Len(t, cursors.DeliveryCursors, wantCursors)
	var cursor meta.MQTTDeliveryCursor
	for _, candidate := range cursors.DeliveryCursors {
		if candidate.Key.SourceID == "1:"+channel.ID {
			cursor = candidate
		}
	}
	require.NotZero(t, cursor.Key.SourceID)
	bindingKey := meta.MQTTSourceBindingKey{Owner: meta.MQTTBindingOwner{Kind: meta.MQTTBindingChannel, ID: cursor.Key.SourceID, Generation: cursor.Key.SourceGeneration}, Namespace: key.Namespace, ClientID: key.ClientID, SessionGeneration: sub.SessionGeneration, SubscriptionGeneration: sub.Generation}
	bindings, err := node.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSourceBinding, BindingKey: bindingKey})
	require.NoError(t, err)
	require.Len(t, bindings.Bindings, 1)
	prepared := sessioncase.PreparedInboxSource{Needed: true, Binding: bindings.Bindings[0], Cursor: cursor}
	require.True(t, prepared.Needed)
	require.Equal(t, meta.MQTTBindingActive, prepared.Binding.Stage)
	require.Greater(t, prepared.Cursor.StartAfter, uint64(0), "replicated activation precedes the business message")
	if !automatic {
		sendFirst()
	}
	require.Greater(t, sent.MessageSeq, prepared.Cursor.StartAfter)
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
	t.Logf("mqtt_inbox_admission_evidence: nodes=1 hash_slots=256 qualification_fixture=%t initial_existing_source=%t first_person_source=true offline_session=true real_source_protection=true real_cursor_commit=true native_send=true shared_replay=true offline_accounted=1 native_directory_projector=true bounded_admission=true automatic_append_hook=%t product_listener=false", !establish, establish, automatic)
}
