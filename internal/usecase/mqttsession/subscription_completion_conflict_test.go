package mqttsession_test

import (
	"context"
	"errors"
	"testing"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

// A projection receipt cannot make a stale parent CAS succeed. Only a definite
// rejection and fresh identical child/Owner allow bounded completion rebasing.
func TestSubscriptionCompletionRebasesOnlyDefiniteUnchangedIntent(t *testing.T) {
	for _, removing := range []bool{false, true} {
		stage := meta.MQTTSubscriptionActive
		if removing {
			stage = meta.MQTTSubscriptionRemoved
		}
		for _, mode := range []string{"renew-once", "renew-forever", "changed-child", "changed-owner", "unchanged-parent", "port-conflict", "lost-reply", "canceled", "revoked"} {
			t.Run(stageName(removing)+"/"+mode, func(t *testing.T) {
				f := setupSubscriptions(t)
				request := subscriptionRequest()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if removing {
					_, err := f.subscriptions.Subscribe(ctx, f.connection.Owner, request)
					require.NoError(t, err)
				}
				calls := 0
				lost := errors.New("completion reply unavailable")
				f.store.mutate = func(ctx context.Context, m meta.MQTTSubscriptionMutation) (meta.MQTTSessionCASResult, error) {
					if m.Subscription.Stage != stage {
						return f.store.commitSubscription(ctx, m)
					}
					calls++
					switch mode {
					case "port-conflict":
						return meta.MQTTSessionCASResult{}, app.ErrConflict
					case "unchanged-parent":
						return meta.MQTTSessionCASResult{Status: meta.MQTTSessionCASConflict, CurrentRevision: m.ExpectedRevision}, nil
					case "lost-reply":
						result, err := f.store.commitSubscription(ctx, m)
						require.NoError(t, err)
						require.Equal(t, meta.MQTTSessionCASApplied, result.Status)
						return meta.MQTTSessionCASResult{}, lost
					}
					if calls == 1 || mode == "renew-forever" {
						_, err := f.service.Renew(ctx, f.connection.Owner)
						require.NoError(t, err)
					}
					result, err := f.store.commitSubscription(ctx, m)
					if mode == "canceled" {
						cancel()
					}
					if mode == "revoked" {
						f.denied = true
					}
					if mode == "changed-child" || mode == "changed-owner" {
						f.store.query = func(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
							row, err := f.store.sessionStore.ReadMQTT(ctx, q)
							if mode == "changed-owner" && row.Session != nil {
								row.Session.OwnerGeneration++
							}
							if mode == "changed-child" && len(row.Subscriptions) == 1 {
								row.Subscriptions[0].NoLocal = !row.Subscriptions[0].NoLocal
							}
							return row, err
						}
					}
					return result, err
				}
				var err error
				if removing {
					_, err = f.subscriptions.Unsubscribe(ctx, f.connection.Owner, request.Topic)
				} else {
					_, err = f.subscriptions.Subscribe(ctx, f.connection.Owner, request)
				}
				f.store.query = nil
				child := f.subscription(t, request.Topic)
				if mode == "renew-once" || removing && mode == "revoked" {
					require.NoError(t, err)
					require.Equal(t, 2, calls)
					require.Equal(t, stage, child.Stage)
				} else {
					require.Error(t, err)
					require.ErrorIs(t, err, app.ErrSubscriptionUnconfirmed)
					want := 1
					if mode == "renew-forever" {
						want = 3
					}
					require.Equal(t, want, calls)
					if mode == "lost-reply" {
						require.ErrorIs(t, err, lost)
						require.Equal(t, stage, child.Stage)
					} else if removing {
						require.Equal(t, meta.MQTTSubscriptionRemoving, child.Stage)
					} else {
						require.Equal(t, meta.MQTTSubscriptionPreparing, child.Stage)
					}
				}
				require.Zero(t, f.owners.Snapshot().Operations)
			})
		}
	}
}

func stageName(removing bool) string {
	if removing {
		return "remove"
	}
	return "establish"
}

func TestSubscriptionCompletionAcceptsExactRemovalAfterDefiniteCASRejection(t *testing.T) {
	f := setupSubscriptions(t)
	ctx := context.Background()
	request := subscriptionRequest()
	_, err := f.subscriptions.Subscribe(ctx, f.connection.Owner, request)
	require.NoError(t, err)
	calls := 0
	f.store.mutate = func(ctx context.Context, m meta.MQTTSubscriptionMutation) (meta.MQTTSessionCASResult, error) {
		if m.Subscription.Stage != meta.MQTTSubscriptionRemoved {
			return f.store.commitSubscription(ctx, m)
		}
		calls++
		result, err := f.store.commitSubscription(ctx, m)
		require.NoError(t, err)
		require.Equal(t, meta.MQTTSessionCASApplied, result.Status)
		return meta.MQTTSessionCASResult{Status: meta.MQTTSessionCASConflict, CurrentRevision: result.CurrentRevision}, nil
	}
	existed, err := f.subscriptions.Unsubscribe(ctx, f.connection.Owner, request.Topic)
	require.NoError(t, err)
	require.True(t, existed)
	require.Equal(t, 1, calls)
	require.Equal(t, meta.MQTTSubscriptionRemoved, f.subscription(t, request.Topic).Stage)
	require.Zero(t, f.owners.Snapshot().Operations)
}
