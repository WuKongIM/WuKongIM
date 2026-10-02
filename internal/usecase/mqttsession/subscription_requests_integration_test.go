//go:build integration

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

func TestSubscriptionRequestsAwaitExplicitPendingWithoutResettingIntent(t *testing.T) {
	f := setupSubscriptions(t)
	ctx := context.Background()
	request := subscriptionRequest()
	control, err := app.NewSubscriptionRequests(app.SubscriptionRequestOptions{Subscriptions: f.subscriptions, Attempts: 3, Retry: time.Millisecond})
	require.NoError(t, err)
	calls := 0
	var first meta.MQTTSubscription
	establish := f.project.establish
	f.project.establish = func(ctx context.Context, r app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
		calls++
		if calls == 1 {
			first = r.Subscription
		} else {
			require.Equal(t, first, r.Subscription)
		}
		if calls < 3 {
			return app.SubscriptionProjectionReceipt{}, app.ErrReplayPending
		}
		return establish(ctx, r)
	}
	active, err := control.Subscribe(ctx, f.connection.Owner, request)
	require.NoError(t, err)
	require.Equal(t, 3, calls)
	require.Equal(t, meta.MQTTSubscriptionActive, active.Stage)
	require.Equal(t, first.Generation, active.Generation)
	require.Equal(t, first.OperationID, active.OperationID)
	calls = 0
	remove := f.project.remove
	f.project.remove = func(ctx context.Context, r app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
		calls++
		if calls == 1 {
			first = r.Subscription
		} else {
			require.Equal(t, first, r.Subscription)
		}
		if calls < 3 {
			return app.SubscriptionProjectionReceipt{}, app.ErrSourceDrainPending
		}
		return remove(ctx, r)
	}
	existed, err := control.Unsubscribe(ctx, f.connection.Owner, request.Topic)
	require.NoError(t, err)
	require.True(t, existed)
	require.Equal(t, 3, calls)
	require.Equal(t, meta.MQTTSubscriptionRemoved, f.subscription(t, request.Topic).Stage)
	existed, err = control.Unsubscribe(ctx, f.connection.Owner, request.Topic)
	require.NoError(t, err)
	require.False(t, existed)
	require.Equal(t, 3, calls)
	require.Zero(t, f.owners.Snapshot().Operations)
}

func TestSubscriptionRequestsRetainPendingAndDoNotRetryUncertainResults(t *testing.T) {
	for _, mode := range []string{"exhausted", "unknown", "conflict", "denied", "panic", "canceled", "deadline", "late-success", "wrong-pending"} {
		t.Run(mode, func(t *testing.T) {
			f := setupSubscriptions(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			attempts := 3
			timeout := time.Second
			if mode == "deadline" {
				timeout = time.Millisecond
			}
			control, err := app.NewSubscriptionRequests(app.SubscriptionRequestOptions{Subscriptions: f.subscriptions, Attempts: attempts, Retry: 5 * time.Millisecond, Timeout: timeout})
			require.NoError(t, err)
			calls := 0
			unknown := errors.New("unavailable")
			f.project.establish = func(ctx context.Context, r app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
				calls++
				switch mode {
				case "unknown":
					return app.SubscriptionProjectionReceipt{}, unknown
				case "conflict":
					return app.SubscriptionProjectionReceipt{}, app.ErrConflict
				case "denied":
					return app.SubscriptionProjectionReceipt{}, app.ErrSubscriptionDenied
				case "panic":
					panic("private")
				case "canceled":
					cancel()
				case "late-success":
					cancel()
					return projectionReceipt(r), nil
				case "wrong-pending":
					return app.SubscriptionProjectionReceipt{}, app.ErrSourceDrainPending
				}
				return app.SubscriptionProjectionReceipt{}, app.ErrReplayPending
			}
			row, err := control.Subscribe(ctx, f.connection.Owner, subscriptionRequest())
			require.Error(t, err)
			require.Zero(t, row)
			if mode == "exhausted" {
				require.Equal(t, attempts, calls)
				require.ErrorIs(t, err, app.ErrReplayPending)
			} else {
				require.LessOrEqual(t, calls, 1)
			}
			if calls > 0 {
				require.Equal(t, meta.MQTTSubscriptionPreparing, f.subscription(t, subscriptionRequest().Topic).Stage)
			}
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}
