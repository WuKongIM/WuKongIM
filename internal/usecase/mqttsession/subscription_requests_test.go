package mqttsession_test

import (
	"testing"
	"time"

	app "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionRequestsValidateBounds(t *testing.T) {
	f := setupSubscriptions(t)
	for _, change := range []func(*app.SubscriptionRequestOptions){
		func(o *app.SubscriptionRequestOptions) { o.Subscriptions = nil },
		func(o *app.SubscriptionRequestOptions) { o.Subscriptions = &app.Subscriptions{} },
		func(o *app.SubscriptionRequestOptions) { o.Attempts = -1 },
		func(o *app.SubscriptionRequestOptions) { o.Attempts = 257 },
		func(o *app.SubscriptionRequestOptions) { o.Retry = time.Nanosecond },
		func(o *app.SubscriptionRequestOptions) { o.Retry = 2 * time.Second },
		func(o *app.SubscriptionRequestOptions) { o.Timeout = -1 },
		func(o *app.SubscriptionRequestOptions) { o.Timeout = 2 * time.Minute },
	} {
		options := app.SubscriptionRequestOptions{Subscriptions: f.subscriptions}
		change(&options)
		_, err := app.NewSubscriptionRequests(options)
		require.ErrorIs(t, err, app.ErrInvalid)
	}
	_, err := app.NewSubscriptionRequests(app.SubscriptionRequestOptions{Subscriptions: f.subscriptions})
	require.NoError(t, err)
}
