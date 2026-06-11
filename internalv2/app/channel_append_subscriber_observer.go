package app

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelappend"
	channelusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/channel"
)

const channelAppendSubscriberMutationTimeout = 2 * time.Second

// channelAppendSubscriberMutationObserver keeps channelappend subscriber caches aligned with API mutations.
type channelAppendSubscriberMutationObserver struct {
	app *App
}

func (o channelAppendSubscriberMutationObserver) ObserveSubscriberMutation(ctx context.Context, event channelusecase.SubscriberMutationEvent) {
	if o.app == nil || o.app.channelAppends == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	applyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), channelAppendSubscriberMutationTimeout)
	defer cancel()
	_ = o.app.channelAppends.ApplySubscriberMutation(applyCtx, channelappend.SubscriberMutationUpdate{
		ChannelID: channelappend.ChannelID{
			ID:   event.ChannelID,
			Type: event.ChannelType,
		},
		Large:                     event.Large,
		SubscriberMutationVersion: event.SubscriberMutationVersion,
		Reset:                     event.Reset,
		AddedUIDs:                 append([]string(nil), event.AddedUIDs...),
		RemovedUIDs:               append([]string(nil), event.RemovedUIDs...),
	})
}
