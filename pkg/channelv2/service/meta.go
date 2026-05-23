package service

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
)

func (c *cluster) ApplyMeta(meta ch.Meta) error {
	if meta.Key == "" {
		meta.Key = ch.ChannelKeyForID(meta.ID)
	}
	future, err := c.group.Submit(context.Background(), meta.Key, reactor.Event{Kind: reactor.EventApplyMeta, Key: meta.Key, Meta: meta})
	if err != nil {
		return err
	}
	_, err = future.Await(context.Background())
	return err
}
