package service

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/reactor"
)

func (c *cluster) ApplyMeta(meta ch.Meta) error {
	return c.applyMeta(context.Background(), meta)
}

func (c *cluster) applyMeta(ctx context.Context, meta ch.Meta) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if meta.Key == "" {
		meta.Key = ch.ChannelKeyForID(meta.ID)
	}
	future, err := c.group.Submit(ctx, meta.Key, reactor.Event{Kind: reactor.EventApplyMeta, Key: meta.Key, Meta: meta})
	if err != nil {
		return err
	}
	_, err = future.Await(ctx)
	return err
}
