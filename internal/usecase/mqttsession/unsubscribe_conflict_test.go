package mqttsession_test

import (
	"context"
	"errors"
	"testing"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestUnsubscribeRebasesOnlyDefiniteUnchangedIntentConflict(t *testing.T) {
	for _, mode := range []string{"renew-once", "renew-forever", "changed-child", "changed-owner", "unchanged-parent", "port-conflict", "lost-reply", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			f := setupSubscriptions(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := subscriptionRequest()
			original, err := f.subscriptions.Subscribe(ctx, f.connection.Owner, r)
			require.NoError(t, err)
			calls := 0
			lost := errors.New("removal reply unavailable")
			f.store.mutate = func(ctx context.Context, m meta.MQTTSubscriptionMutation) (meta.MQTTSessionCASResult, error) {
				if m.Subscription.Stage != meta.MQTTSubscriptionRemoving {
					return f.store.commitSubscription(ctx, m)
				}
				calls++
				switch mode {
				case "port-conflict":
					return meta.MQTTSessionCASResult{}, app.ErrConflict
				case "unchanged-parent":
					return meta.MQTTSessionCASResult{Status: meta.MQTTSessionCASConflict, CurrentRevision: m.ExpectedRevision}, nil
				case "lost-reply":
					result, e := f.store.commitSubscription(ctx, m)
					require.NoError(t, e)
					require.Equal(t, meta.MQTTSessionCASApplied, result.Status)
					return meta.MQTTSessionCASResult{}, lost
				case "changed-child":
					changed := m
					changed.Subscription = original
					changed.Subscription.NoLocal = !original.NoLocal
					changed.Subscription.Revision = m.ExpectedRevision + 1
					_, e := f.store.commitSubscription(ctx, changed)
					require.NoError(t, e)
				default:
					if calls == 1 || mode == "renew-forever" {
						_, e := f.service.Renew(ctx, f.connection.Owner)
						require.NoError(t, e)
					}
				}
				result, e := f.store.commitSubscription(ctx, m)
				if mode == "canceled" {
					cancel()
				}
				if mode == "changed-owner" {
					f.store.query = func(ctx context.Context, q meta.MQTTRead) (meta.MQTTReadResult, error) {
						current, e := f.store.sessionStore.ReadMQTT(ctx, q)
						if current.Session != nil {
							current.Session.OwnerGeneration++
						}
						return current, e
					}
				}
				return result, e
			}
			existed, err := f.subscriptions.Unsubscribe(ctx, f.connection.Owner, r.Topic)
			f.store.query = nil
			child := f.subscription(t, r.Topic)
			if mode == "renew-once" {
				require.NoError(t, err)
				require.True(t, existed)
				require.Equal(t, 2, calls)
				require.Equal(t, meta.MQTTSubscriptionRemoved, child.Stage)
				require.Len(t, f.removed, 1)
			} else {
				require.Error(t, err)
				require.False(t, existed)
				require.Empty(t, f.removed)
				want := 1
				if mode == "renew-forever" {
					want = 3
				}
				require.Equal(t, want, calls)
				if mode == "lost-reply" {
					require.ErrorIs(t, err, lost)
					require.Equal(t, meta.MQTTSubscriptionRemoving, child.Stage)
				} else {
					require.Equal(t, meta.MQTTSubscriptionActive, child.Stage)
				}
			}
			require.Equal(t, original.Generation, child.Generation)
			require.Equal(t, original.OperationID, child.OperationID)
			require.Equal(t, original.NoLocal != (mode == "changed-child"), child.NoLocal)
			require.Zero(t, f.owners.Snapshot().Operations)
		})
	}
}
