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

// Pending turns may reuse a positively committed original boundary within one
// request, while permission, ownership, cancellation and unknown results remain
// live fences. This is not a cross-request preparation cache.
func TestGroupProjectionRetainsPreparationOnlyWithinCurrentRequest(t *testing.T) {
	for _, mode := range []string{"complete", "renewed", "permission_changed", "unknown", "canceled", "fenced", "new_request"} {
		t.Run(mode, func(t *testing.T) {
			f := setupGroupSource(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			projection, err := app.NewGroupProjection(app.GroupProjectionOptions{Store: &drainStore{progressStore: &progressStore{groupSourceStore: f.store}}, Owners: f.owners, Authorization: f.opts.Authorization, Sources: f.opts.Sources, Now: func() time.Time { return f.now }, Replay: groupReplayConfirmation(func(_ context.Context, _ meta.MQTTBindingOwner, start uint64) error {
				calls++
				require.EqualValues(t, 10, start)
				if calls == 1 {
					f.tail = 20
					var err error
					switch mode {
					case "renewed":
						_, err = f.service.Renew(ctx, f.connection.Owner)
					case "permission_changed":
						f.version++
					case "canceled":
						cancel()
					case "fenced":
						err = f.owners.Fence(f.connection.Owner)
					}
					require.NoError(t, err)
					return app.ErrReplayPending
				}
				if mode == "unknown" {
					return app.ErrSubscriptionCallback
				}
				return nil
			})})
			require.NoError(t, err)
			f.options.Projection = projection
			subs, err := app.NewSubscriptions(f.options)
			require.NoError(t, err)
			attempts := 3
			if mode == "new_request" {
				attempts = 1
			}
			requests, err := app.NewSubscriptionRequests(app.SubscriptionRequestOptions{Subscriptions: subs, Attempts: attempts, Retry: time.Millisecond})
			require.NoError(t, err)
			row, err := requests.Subscribe(ctx, f.connection.Owner, subscriptionRequest())
			switch mode {
			case "complete", "renewed":
				require.NoError(t, err)
				require.Equal(t, meta.MQTTSubscriptionActive, row.Stage)
			case "new_request":
				require.ErrorIs(t, err, app.ErrReplayPending)
				require.Equal(t, 2, f.calls)
				row, err = requests.Subscribe(ctx, f.connection.Owner, subscriptionRequest())
				require.NoError(t, err)
				require.Equal(t, meta.MQTTSubscriptionActive, row.Stage)
			default:
				require.Error(t, err)
				require.Zero(t, row)
				require.Equal(t, meta.MQTTSubscriptionPreparing, f.subscription(t, f.intent.Topic).Stage)
			}
			want := 2
			if mode == "new_request" {
				want = 3
			}
			require.Equal(t, want, f.calls, "same-request replay readiness must not repeat source preparation")
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}
