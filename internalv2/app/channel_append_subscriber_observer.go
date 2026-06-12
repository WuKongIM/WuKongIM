package app

import (
	"context"
	"time"

	clusterinfra "github.com/WuKongIM/WuKongIM/internalv2/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelappend"
	channelusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/channel"
)

const channelAppendSubscriberMutationTimeout = 2 * time.Second

// channelAppendSubscriberMutationObserver keeps channelappend subscriber caches aligned with API mutations.
type channelAppendSubscriberMutationObserver struct {
	app *App
}

func (o channelAppendSubscriberMutationObserver) ObserveSubscriberMutation(ctx context.Context, event channelusecase.SubscriberMutationEvent) {
	if o.app == nil {
		return
	}
	channelID := channelappend.ChannelID{
		ID:   event.ChannelID,
		Type: event.ChannelType,
	}
	if o.app.channelAppendMetadata != nil {
		o.app.channelAppendMetadata.Store(channelID, clusterChannelAppendMetadata(event))
	}
	if o.app.channelAppends == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	applyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), channelAppendSubscriberMutationTimeout)
	defer cancel()
	_ = o.app.channelAppends.ApplySubscriberMutation(applyCtx, channelappend.SubscriberMutationUpdate{
		ChannelID:                 channelID,
		Large:                     event.Large,
		SubscriberMutationVersion: event.SubscriberMutationVersion,
		Reset:                     event.Reset,
		AddedUIDs:                 append([]string(nil), event.AddedUIDs...),
		RemovedUIDs:               append([]string(nil), event.RemovedUIDs...),
	})
}

func clusterChannelAppendMetadata(event channelusecase.SubscriberMutationEvent) clusterinfra.ChannelAppendMetadata {
	return clusterinfra.ChannelAppendMetadata{
		Large:                     event.Large,
		SubscriberMutationVersion: event.SubscriberMutationVersion,
	}
}
