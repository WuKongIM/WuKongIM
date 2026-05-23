package service

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
)

func (c *cluster) Fetch(ctx context.Context, req ch.FetchRequest) (ch.FetchResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	key := ch.ChannelKeyForID(req.ChannelID)
	future, err := c.group.Submit(ctx, key, reactor.Event{Kind: reactor.EventFetch, Key: key, Fetch: req})
	if err != nil {
		return ch.FetchResult{}, err
	}
	result, err := future.Await(ctx)
	if err != nil {
		return ch.FetchResult{}, err
	}
	return result.Fetch, nil
}
