package mqttsession_test

import (
	"context"
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

type groupReplayConfirmation func(context.Context, meta.MQTTBindingOwner, uint64) error

func (f groupReplayConfirmation) Confirm(c context.Context, o meta.MQTTBindingOwner, start uint64) error {
	return f(c, o, start)
}

func TestGroupProjectionCompletesOnlyAfterSharedRecoveryAndPreservesResume(t *testing.T) {
	f := setupGroupSource(t)
	ctx := context.Background()
	s := &drainStore{progressStore: &progressStore{groupSourceStore: f.store}}
	pending := true
	var boundary uint64
	var source meta.MQTTBindingOwner
	projection, e := app.NewGroupProjection(app.GroupProjectionOptions{Store: s, Owners: f.owners, Authorization: f.opts.Authorization, Sources: f.opts.Sources, Now: func() time.Time { return f.now }, Replay: groupReplayConfirmation(func(_ context.Context, o meta.MQTTBindingOwner, start uint64) error {
		source, boundary = o, start
		if pending {
			return app.ErrReplayPending
		}
		return nil
	})})
	require.NoError(t, e)
	f.options.Projection = projection
	subs, e := app.NewSubscriptions(f.options)
	require.NoError(t, e)
	_, e = subs.Reconcile(ctx, f.connection.Owner, f.intent.Topic)
	require.ErrorIs(t, e, app.ErrReplayPending)
	require.Equal(t, meta.MQTTSubscriptionPreparing, f.subscription(t, f.intent.Topic).Stage)
	require.EqualValues(t, 10, boundary)
	first, e := s.ReadMQTT(ctx, meta.MQTTRead{Kind: meta.MQTTReadDeliveryCursors, Namespace: f.intent.Namespace, ClientID: f.intent.ClientID, SessionGeneration: f.intent.SessionGeneration, SubscriptionGeneration: f.intent.Generation, Limit: 2})
	require.NoError(t, e)
	require.Len(t, first.DeliveryCursors, 1)
	resumed, e := f.service.Connect(ctx, command())
	require.NoError(t, e)
	f.tail = 20
	pending = false
	active, e := subs.Reconcile(ctx, resumed.Owner, f.intent.Topic)
	require.NoError(t, e)
	require.Equal(t, meta.MQTTSubscriptionActive, active.Stage)
	require.Equal(t, f.intent.Generation, active.Generation)
	require.EqualValues(t, 10, boundary)
	require.Equal(t, first.DeliveryCursors[0].Key.SourceGeneration, source.Generation)
	f.denied = true
	existed, e := subs.Unsubscribe(ctx, resumed.Owner, f.intent.Topic)
	require.NoError(t, e)
	require.True(t, existed)
	require.Equal(t, meta.MQTTSubscriptionRemoved, f.subscription(t, f.intent.Topic).Stage)
	require.Zero(t, f.owners.Snapshot().Operations)
}

func TestGroupProjectionRejectsChangedOrForgedIntent(t *testing.T) {
	for _, mode := range []string{"uid", "snapshot", "inbox", "permission_after_replay", "canceled_after_replay", "unavailable_replay"} {
		t.Run(mode, func(t *testing.T) {
			f := setupGroupSource(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			projection, e := app.NewGroupProjection(app.GroupProjectionOptions{Store: &drainStore{progressStore: &progressStore{groupSourceStore: f.store}}, Owners: f.owners, Authorization: f.opts.Authorization, Sources: f.opts.Sources, Now: func() time.Time { return f.now }, Replay: groupReplayConfirmation(func(context.Context, meta.MQTTBindingOwner, uint64) error {
				calls++
				switch mode {
				case "permission_after_replay":
					f.version++
				case "canceled_after_replay":
					cancel()
				case "unavailable_replay":
					return context.DeadlineExceeded
				}
				return nil
			})})
			require.NoError(t, e)
			request := app.SubscriptionProjectionRequest{Owner: f.connection.Owner, UID: "alice", Subscription: f.intent}
			switch mode {
			case "uid":
				request.UID = "foreign"
			case "snapshot":
				request.Subscription.Revision++
			case "inbox":
				request.Subscription.TargetKind = meta.MQTTSubscriptionUserInbox
			}
			receipt, e := projection.Establish(ctx, request)
			require.Error(t, e)
			require.Zero(t, receipt)
			require.Equal(t, meta.MQTTSubscriptionPreparing, f.subscription(t, f.intent.Topic).Stage)
			if mode == "uid" || mode == "snapshot" || mode == "inbox" {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}
