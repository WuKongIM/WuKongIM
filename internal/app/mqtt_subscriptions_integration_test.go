//go:build integration

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	runtime "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/internal/usecase/user"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

// This test controls source proof explicitly; its successful receipt exercises
// intent orchestration only and must never be installed in product composition.
type mqttSubscriptionProjectionFixture struct {
	establish func(context.Context, sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjectionReceipt, error)
	remove    func(context.Context, sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjectionReceipt, error)
}

func (p *mqttSubscriptionProjectionFixture) Establish(ctx context.Context, r sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjectionReceipt, error) {
	return p.establish(ctx, r)
}
func (p *mqttSubscriptionProjectionFixture) Remove(ctx context.Context, r sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjectionReceipt, error) {
	return p.remove(ctx, r)
}
func mqttSubscriptionFixtureReceipt(r sessioncase.SubscriptionProjectionRequest) sessioncase.SubscriptionProjectionReceipt {
	s := r.Subscription
	return sessioncase.SubscriptionProjectionReceipt{Namespace: s.Namespace, ClientID: s.ClientID, SessionGeneration: s.SessionGeneration, Topic: s.Topic, SubscriptionGeneration: s.Generation, IntentRevision: s.Revision, OperationID: s.OperationID}
}

func TestMQTTSubscriptionIntentSingleNodeCluster(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.Cluster.Slots.HashSlotCount = 256
	a, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, a.Stop(ctx))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	require.NoError(t, a.Start(ctx))
	node := a.cluster.(*cluster.Node)
	waitSingleNodeClusterNodeSchedulable(t, node, cfg.NodeID)
	id := ch.ChannelID{ID: "group", Type: 2}
	waitSingleNodeClusterRouteLeader(t, node, id.ID, cfg.NodeID)
	seedGroupSendPermission(t, node, id, "alice")
	require.NoError(t, node.UpsertDeviceMetadata(ctx, meta.Device{UID: "alice", DeviceFlag: 1, Token: "secret", DeviceLevel: 1}))
	owners, err := runtime.NewOwners(runtime.OwnerOptions{NodeID: cfg.NodeID, BootID: "subscription-integration", Capacity: 4, MaxOperations: 8, PendingTimeout: time.Second, MaxLease: time.Minute, CloseRetry: time.Second})
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require.NoError(t, owners.Close(ctx))
	})
	sessions, err := sessioncase.New(sessioncase.Options{Store: node, Owners: owners, Isolation: owners, Tokens: user.New(user.Options{DeviceReader: mqttAcquisitionDeviceReader{node: node}}), Wills: mqttWillAuthorizer{messages: a.Messages()}, LeaseDuration: 10 * time.Second, CleanupTimeout: time.Second, SessionExpiryLimitSec: 86400, QuotaMessages: 100, QuotaBytes: 1 << 20, WindowLimit: 16})
	require.NoError(t, err)
	command := sessioncase.ConnectCommand{UID: "alice", Token: "secret", DeviceFlag: 1, SessionExpirySec: 60, ReceiveMaximum: 16, MaxPacketBytes: 1 << 20, CloseTransport: func(context.Context) error { return nil }}
	command.Key.Namespace = "main"
	command.Key.ClientID = "subscriptions"
	first, err := sessions.Connect(ctx, command)
	require.NoError(t, err)
	unavailable := errors.New("fixture: source protection unavailable")
	projection := &mqttSubscriptionProjectionFixture{establish: func(context.Context, sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjectionReceipt, error) {
		return sessioncase.SubscriptionProjectionReceipt{}, unavailable
	}}
	authorization, err := newMQTTReceiveAuthorization(node)
	require.NoError(t, err)
	subscriptions, err := sessioncase.NewSubscriptions(sessioncase.SubscriptionOptions{Store: node, Owners: owners, Authorization: authorization, Projection: projection})
	require.NoError(t, err)
	request := sessioncase.SubscriptionRequest{Topic: "wk/v1/groups/Z3JvdXA/messages", TargetKind: meta.MQTTSubscriptionGroup, TargetID: id.ID, RequestedQoS: 1, NoLocal: true}
	read := func() meta.MQTTSubscription {
		r, err := node.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadSubscription, Namespace: "main", ClientID: "subscriptions", SessionGeneration: first.Owner.SessionGeneration, Topic: request.Topic})
		require.NoError(t, err)
		require.Len(t, r.Subscriptions, 1)
		return r.Subscriptions[0]
	}
	_, err = subscriptions.Subscribe(ctx, first.Owner, request)
	require.ErrorIs(t, err, unavailable)
	pending := read()
	require.Greater(t, pending.AuthorizationVersion, uint64(1))
	require.Equal(t, meta.MQTTSubscriptionPreparing, pending.Stage)
	resumed, err := sessions.Connect(ctx, command)
	require.NoError(t, err)
	require.True(t, resumed.SessionPresent)
	projection.establish = func(ctx context.Context, r sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjectionReceipt, error) {
		require.Equal(t, pending, r.Subscription)
		require.Equal(t, resumed.Owner, r.Owner)
		// A real parent revision change must not reset the persisted child intent.
		_, err := sessions.Renew(ctx, resumed.Owner)
		require.NoError(t, err)
		return mqttSubscriptionFixtureReceipt(r), nil
	}
	active, err := subscriptions.Reconcile(ctx, resumed.Owner, request.Topic)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSubscriptionActive, active.Stage)
	require.Equal(t, pending.Generation, active.Generation)
	require.Equal(t, pending.OperationID, active.OperationID)
	_, err = subscriptions.Subscribe(ctx, first.Owner, request)
	require.Error(t, err)
	require.NoError(t, node.RemoveChannelSubscribers(ctx, id.ID, 2, []string{"alice"}, 2))
	_, err = subscriptions.Subscribe(ctx, resumed.Owner, request)
	require.ErrorIs(t, err, sessioncase.ErrSubscriptionDenied)
	require.NoError(t, node.AddChannelSubscribers(ctx, id.ID, 2, []string{"alice"}, 2))
	_, err = subscriptions.Subscribe(ctx, resumed.Owner, request)
	require.ErrorIs(t, err, sessioncase.ErrSubscriptionRevoked)
	require.NoError(t, node.RemoveChannelSubscribers(ctx, id.ID, 2, []string{"alice"}, 2))
	projection.remove = func(context.Context, sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjectionReceipt, error) {
		return sessioncase.SubscriptionProjectionReceipt{}, unavailable
	}
	_, err = subscriptions.Unsubscribe(ctx, resumed.Owner, request.Topic)
	require.ErrorIs(t, err, unavailable)
	require.Equal(t, meta.MQTTSubscriptionRemoving, read().Stage)
	projection.remove = func(_ context.Context, r sessioncase.SubscriptionProjectionRequest) (sessioncase.SubscriptionProjectionReceipt, error) {
		return mqttSubscriptionFixtureReceipt(r), nil
	}
	removed, err := subscriptions.Reconcile(ctx, resumed.Owner, request.Topic)
	require.NoError(t, err)
	require.Equal(t, meta.MQTTSubscriptionRemoved, removed.Stage)
	require.Zero(t, owners.Snapshot().Operations)
	t.Log("mqtt_subscription_intent_evidence: hash_slots=256 authoritative_intent=true source_unavailable_no_activation=true owner_resume=true stable_generation=true concurrent_parent_renewal=true revoked_can_remove=true same_version_rejoin_revoked=true receive_authority=real projection=controlled distributed_source_proof=false")
}
