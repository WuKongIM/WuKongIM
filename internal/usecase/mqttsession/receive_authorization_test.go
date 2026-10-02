package mqttsession_test

import (
	"context"
	"errors"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

type receiveReader func(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error)

func (f receiveReader) ReadMQTT(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
	return f(ctx, q)
}

func TestReceiveAuthorizationUsesNativeMembershipIncarnation(t *testing.T) {
	f := setupSubscriptions(t)
	ctx := context.Background()
	s := f.store.db.HashSlot(7)
	authority, err := app.NewReceiveAuthorization(app.ReceiveAuthorizationOptions{Store: f.store})
	require.NoError(t, err)
	f.options.Authorization = authority
	f.subscriptions, err = app.NewSubscriptions(f.options)
	require.NoError(t, err)
	request := subscriptionRequest()
	checkDenied := func() {
		t.Helper()
		v, e := authority.AuthorizeSubscription(ctx, "alice", request)
		require.Zero(t, v)
		require.ErrorIs(t, e, app.ErrSubscriptionDenied)
	}
	checkDenied()
	require.NoError(t, s.CreateChannel(ctx, meta.Channel{ChannelID: "group", ChannelType: 2, AllowStranger: 1, Large: 1, Ban: 1, SendBan: 1}))
	checkDenied() // SEND flags and open membership never grant receive permission.
	require.NoError(t, s.AddSubscribers(ctx, "group", 2, []string{"alice"}, 1))
	first, err := f.subscriptions.Subscribe(ctx, f.connection.Owner, request)
	require.NoError(t, err)
	require.Greater(t, first.AuthorizationVersion, uint64(1), "sending mute does not remove receiving")
	require.NoError(t, s.AddSubscribers(ctx, "group", 2, []string{"alice", "bob"}, 1))
	stable, err := authority.AuthorizeSubscription(ctx, "alice", request)
	require.NoError(t, err)
	require.Equal(t, first.AuthorizationVersion, stable)
	require.NoError(t, s.RemoveSubscribers(ctx, "group", 2, []string{"alice"}, 1))
	checkDenied()
	require.NoError(t, s.AddSubscribers(ctx, "group", 2, []string{"alice"}, 1))
	_, err = f.subscriptions.Subscribe(ctx, f.connection.Owner, request)
	require.ErrorIs(t, err, app.ErrSubscriptionRevoked)
	_, err = f.subscriptions.Unsubscribe(ctx, f.connection.Owner, request.Topic)
	require.NoError(t, err)
	fresh, err := f.subscriptions.Subscribe(ctx, f.connection.Owner, request)
	require.NoError(t, err)
	require.Greater(t, fresh.AuthorizationVersion, first.AuthorizationVersion)
	require.NoError(t, s.UpsertChannel(ctx, meta.Channel{ChannelID: "group", ChannelType: 2, Disband: 1}))
	checkDenied()
	require.NoError(t, s.DeleteChannel(ctx, "group", 2))
	checkDenied()
	require.NoError(t, s.CreateChannel(ctx, meta.Channel{ChannelID: "group", ChannelType: 2}))
	require.NoError(t, s.AddSubscribers(ctx, "group", 2, []string{"alice"}, 1))
	recreated, err := authority.AuthorizeSubscription(ctx, "alice", request)
	require.NoError(t, err)
	require.Greater(t, recreated, fresh.AuthorizationVersion)
}

func TestReceiveAuthorizationBoundsSelfInboxAndCancellation(t *testing.T) {
	calls := 0
	reader := receiveReader(func(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
		calls++
		t.Fatal("unexpected metadata call")
		return meta.MQTTReadResult{}, nil
	})
	a, err := app.NewReceiveAuthorization(app.ReceiveAuthorizationOptions{Store: reader})
	require.NoError(t, err)
	r := subscriptionRequest()
	r.TargetKind = meta.MQTTSubscriptionUserInbox
	r.TargetID = "alice"
	r.Topic = "wk/v1/users/YWxpY2U/inbox"
	v, err := a.AuthorizeSubscription(context.Background(), "alice", r)
	require.NoError(t, err)
	require.Zero(t, v)
	_, err = a.AuthorizeSubscription(context.Background(), "bob", r)
	require.ErrorIs(t, err, app.ErrSubscriptionDenied)
	for _, bad := range []app.SubscriptionRequest{{}, {Topic: "a/+", TargetID: "group", TargetKind: meta.MQTTSubscriptionGroup}, {Topic: "a", TargetID: "group", TargetKind: meta.MQTTSubscriptionGroup, RequestedQoS: 3}} {
		_, err = a.AuthorizeSubscription(context.Background(), "alice", bad)
		require.ErrorIs(t, err, app.ErrInvalid)
	}
	_, err = a.AuthorizeSubscription(context.Background(), "", subscriptionRequest())
	require.ErrorIs(t, err, app.ErrInvalid)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = a.AuthorizeSubscription(ctx, "alice", r)
	require.ErrorIs(t, err, context.Canceled)
	_, err = a.AuthorizeSubscription(ctx, "alice", subscriptionRequest())
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, calls)
	for _, o := range []app.ReceiveAuthorizationOptions{{}, {Store: reader, Timeout: -1}, {Store: reader, Timeout: time.Hour}} {
		_, err = app.NewReceiveAuthorization(o)
		require.ErrorIs(t, err, app.ErrInvalid)
	}
}

func TestReceiveAuthorizationRejectsAmbiguousEvidenceAndCallbackFailure(t *testing.T) {
	unavailable := errors.New("slot unavailable")
	for _, test := range []struct {
		name       string
		mutate     func(*meta.MQTTReadResult)
		dependency error
		panic      bool
		want       error
	}{
		{name: "valid"},
		{name: "missing_channel", mutate: func(r *meta.MQTTReadResult) { r.Membership.Channel = nil }, want: app.ErrSubscriptionDenied},
		{name: "absent_member", mutate: func(r *meta.MQTTReadResult) { r.Membership.Member = nil }, want: app.ErrSubscriptionDenied},
		{name: "incomplete", mutate: func(r *meta.MQTTReadResult) { r.Done = false }, want: app.ErrEvidence},
		{name: "missing_view", mutate: func(r *meta.MQTTReadResult) { r.Membership = nil }, want: app.ErrEvidence},
		{name: "wrong_uid", mutate: func(r *meta.MQTTReadResult) { r.Membership.Member.UID = "bob" }, want: app.ErrEvidence},
		{name: "wrong_channel", mutate: func(r *meta.MQTTReadResult) { r.Membership.Key.ChannelID = "other" }, want: app.ErrEvidence},
		{name: "sequence_regressed", mutate: func(r *meta.MQTTReadResult) { r.Membership.Sequence = 1 }, want: app.ErrEvidence},
		{name: "unrelated_result", mutate: func(r *meta.MQTTReadResult) { r.SourceOwners = []meta.MQTTBindingOwner{{}} }, want: app.ErrEvidence},
		{name: "cursor", mutate: func(r *meta.MQTTReadResult) { r.After.Topic = "other" }, want: app.ErrEvidence},
		{name: "unavailable", dependency: unavailable, want: unavailable},
		{name: "panic", panic: true, want: app.ErrSubscriptionCallback},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, err := app.NewReceiveAuthorization(app.ReceiveAuthorizationOptions{Store: receiveReader(func(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
				deadline, ok := ctx.Deadline()
				require.True(t, ok)
				require.InDelta(t, 5*time.Second, time.Until(deadline), float64(time.Second))
				require.Equal(t, meta.MQTTRead{Kind: meta.MQTTReadMembership, MembershipKey: meta.SubscriberKey{ChannelID: "group", ChannelType: 2, UID: "alice"}}, q)
				if test.panic {
					panic("private token")
				}
				r := meta.MQTTReadResult{Done: true, Membership: &meta.MQTTMembershipView{Key: q.MembershipKey, Sequence: 7, Channel: &meta.Channel{ChannelID: "group", ChannelType: 2}, Member: &meta.Subscriber{ChannelID: "group", ChannelType: 2, UID: "alice", Incarnation: 7}}}
				if test.mutate != nil {
					test.mutate(&r)
				}
				return r, test.dependency
			})})
			require.NoError(t, err)
			v, err := a.AuthorizeSubscription(context.Background(), "alice", subscriptionRequest())
			if test.want == nil {
				require.NoError(t, err)
				require.EqualValues(t, 7, v)
			} else {
				require.ErrorIs(t, err, test.want)
				require.Zero(t, v)
				require.NotContains(t, err.Error(), "private token")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	a, err := app.NewReceiveAuthorization(app.ReceiveAuthorizationOptions{Store: receiveReader(func(context.Context, meta.MQTTRead) (meta.MQTTReadResult, error) {
		cancel()
		return meta.MQTTReadResult{}, nil
	})})
	require.NoError(t, err)
	_, err = a.AuthorizeSubscription(ctx, "alice", subscriptionRequest())
	require.ErrorIs(t, err, context.Canceled, "late dependency result cannot authorize or deny after cancellation")
}

func TestReceiveAuthorizationRejoinEndsOldSenderAndPreservesDebt(t *testing.T) {
	f, _, _ := setupExchangeRecovery(t, true)
	ctx := context.Background()
	native := f.store.db.HashSlot(7)
	require.NoError(t, native.CreateChannel(ctx, meta.Channel{ChannelID: "group", ChannelType: 2}))
	require.NoError(t, native.ImportSubscriberSequence(ctx, 7))
	require.NoError(t, native.ImportSubscribers(ctx, "group", 2, []meta.Subscriber{{ChannelID: "group", ChannelType: 2, UID: "alice", Incarnation: 7}}))
	authority, err := app.NewReceiveAuthorization(app.ReceiveAuthorizationOptions{Store: f.store})
	require.NoError(t, err)
	current, err := f.service.Connect(ctx, command())
	require.NoError(t, err)
	require.True(t, current.SessionPresent)
	before := f.row(t)
	closed := 0
	stream, err := makeSender(t, f, authority).Open(ctx, current, deliverySink{enqueue: func(context.Context, app.PreparedDelivery, bool) (app.DeliveryDisposition, error) {
		t.Fatal("old exchange escaped rejoin")
		return app.DeliveryQueued, nil
	}, close: func(_ context.Context, reason meta.MQTTSessionEndReason) error {
		require.Equal(t, meta.MQTTSessionRevoked, reason)
		closed++
		return nil
	}})
	require.NoError(t, err)
	require.NoError(t, native.RemoveSubscribers(ctx, "group", 2, []string{"alice"}, 1))
	require.NoError(t, native.AddSubscribers(ctx, "group", 2, []string{"alice"}, 1))
	turn, err := stream.Turn(ctx, f.key)
	require.ErrorIs(t, err, app.ErrSubscriptionRevoked)
	require.True(t, turn.Ended)
	require.Equal(t, 1, closed)
	after := f.row(t)
	require.Equal(t, before.PendingMessages, after.PendingMessages)
	require.Equal(t, before.OutboundInflight, after.OutboundInflight)
	next, err := f.service.Connect(ctx, command())
	require.NoError(t, err)
	require.False(t, next.SessionPresent)
	require.Greater(t, next.Owner.SessionGeneration, current.Owner.SessionGeneration)
	f.connection = next
	f.options.Authorization = authority
	f.subscriptions, err = app.NewSubscriptions(f.options)
	require.NoError(t, err)
	sub, err := f.subscriptions.Subscribe(ctx, next.Owner, subscriptionRequest())
	require.NoError(t, err)
	require.Greater(t, sub.AuthorizationVersion, uint64(7))
}
