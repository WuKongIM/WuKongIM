package service

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
)

// Config wires the v0 channelv2 service facade.
type Config struct {
	LocalNode    ch.NodeID
	ReactorCount int
	MailboxSize  int
	Store        store.Factory
}

type cluster struct {
	group *reactor.Group
}

// New constructs a v0 channelv2 cluster facade.
func New(cfg Config) (ch.Cluster, error) {
	if cfg.LocalNode == 0 || cfg.Store == nil {
		return nil, ch.ErrInvalidConfig
	}
	group, err := reactor.NewGroup(reactor.Config{LocalNode: cfg.LocalNode, ReactorCount: cfg.ReactorCount, MailboxSize: cfg.MailboxSize, Store: cfg.Store})
	if err != nil {
		return nil, err
	}
	return &cluster{group: group}, nil
}

func (c *cluster) Tick(ctx context.Context) error { return nil }

func (c *cluster) Close() error {
	if c == nil || c.group == nil {
		return nil
	}
	return c.group.Close()
}
