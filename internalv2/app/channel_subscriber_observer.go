package app

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelwrite"
	channelusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/channel"
)

const channelWriteSubscriberMutationTimeout = 2 * time.Second

// channelWriteSubscriberMutationObserver keeps channelwrite subscriber caches aligned with API mutations.
type channelWriteSubscriberMutationObserver struct {
	app *App
}

func (o channelWriteSubscriberMutationObserver) ObserveSubscriberMutation(ctx context.Context, event channelusecase.SubscriberMutationEvent) {
	if o.app == nil || o.app.channelWrites == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	applyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), channelWriteSubscriberMutationTimeout)
	defer cancel()
	_ = o.app.channelWrites.ApplySubscriberMutation(applyCtx, channelwrite.SubscriberMutationUpdate{
		ChannelID: channelwrite.ChannelID{
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
