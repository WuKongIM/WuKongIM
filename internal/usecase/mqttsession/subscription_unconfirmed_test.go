package mqttsession_test

import (
	"context"
	"testing"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

// The protocol may send a negative per-filter reply only when failure cannot
// leave a new preparing/replaced intent that later becomes active silently.
func TestSubscriptionFailureDistinguishesUnconfirmedDurableIntent(t *testing.T) {
	for _, mode := range []string{"before-intent", "projection", "preparing-retry", "replacing", "removing"} {
		t.Run(mode, func(t *testing.T) {
			f := setupSubscriptions(t)
			ctx := context.Background()
			request := subscriptionRequest()
			switch mode {
			case "before-intent":
				f.denied = true
			case "projection":
				f.project.establish = func(context.Context, app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
					return app.SubscriptionProjectionReceipt{}, app.ErrSubscriptionDenied
				}
			case "preparing-retry":
				f.project.establish = func(context.Context, app.SubscriptionProjectionRequest) (app.SubscriptionProjectionReceipt, error) {
					return app.SubscriptionProjectionReceipt{}, app.ErrReplayPending
				}
				_, err := f.subscriptions.Subscribe(ctx, f.connection.Owner, request)
				require.ErrorIs(t, err, app.ErrReplayPending)
				f.denied = true
			case "replacing", "removing":
				_, err := f.subscriptions.Subscribe(ctx, f.connection.Owner, request)
				require.NoError(t, err)
				if mode == "replacing" {
					request.NoLocal = !request.NoLocal
				}
				f.store.mutate = func(context.Context, meta.MQTTSubscriptionMutation) (meta.MQTTSessionCASResult, error) {
					return meta.MQTTSessionCASResult{}, app.ErrSubscriptionDenied
				}
			}
			var err error
			if mode == "removing" {
				_, err = f.subscriptions.Unsubscribe(ctx, f.connection.Owner, request.Topic)
			} else {
				_, err = f.subscriptions.Subscribe(ctx, f.connection.Owner, request)
			}
			require.ErrorIs(t, err, app.ErrSubscriptionDenied)
			if mode == "before-intent" {
				require.NotErrorIs(t, err, app.ErrSubscriptionUnconfirmed)
			} else {
				require.ErrorIs(t, err, app.ErrSubscriptionUnconfirmed)
			}
		})
	}
}
