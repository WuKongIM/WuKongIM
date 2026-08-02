package app

import (
	"context"
	"testing"

	clusterinfra "github.com/WuKongIM/WuKongIM/internal/infra/cluster"
	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	channelusecase "github.com/WuKongIM/WuKongIM/internal/usecase/channel"
)

func TestChannelAppendSubscriberMutationObserverRefreshesMetadataCache(t *testing.T) {
	cache := clusterinfra.NewChannelAppendMetadataCache()
	app := &App{channelAppendMetadata: cache}
	observer := channelAppendSubscriberMutationObserver{app: app}

	observer.ObserveSubscriberMutation(context.Background(), channelusecase.SubscriberMutationEvent{
		ChannelKey: channelusecase.ChannelKey{
			ChannelID:   "g1",
			ChannelType: 2,
		},
		Large:                     true,
		SubscriberMutationVersion: 7,
	})

	metadata, ok := cache.Lookup(channelappend.ChannelID{ID: "g1", Type: 2})
	if !ok || !metadata.Large || metadata.SubscriberMutationVersion != 7 {
		t.Fatalf("metadata cache = %#v ok=%v, want large version 7", metadata, ok)
	}
}
