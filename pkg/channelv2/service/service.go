package service

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
)

// Config wires the v0 channelv2 service facade.
type Config struct {
	LocalNode    ch.NodeID
	ReactorCount int
	MailboxSize  int
	Store        store.Factory
	Transport    transport.Client
	PullMaxBytes int
}

type cluster struct {
	localNode    ch.NodeID
	group        *reactor.Group
	transport    transport.Client
	pullMaxBytes int
	metas        map[ch.ChannelKey]ch.Meta
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
	if cfg.PullMaxBytes <= 0 {
		cfg.PullMaxBytes = 64 * 1024
	}
	return &cluster{localNode: cfg.LocalNode, group: group, transport: cfg.Transport, pullMaxBytes: cfg.PullMaxBytes, metas: make(map[ch.ChannelKey]ch.Meta)}, nil
}

func (c *cluster) Tick(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for key, meta := range c.metas {
		if meta.Leader == c.localNode || c.transport == nil || meta.Status != ch.StatusActive {
			continue
		}
		fetch, _ := c.Fetch(ctx, ch.FetchRequest{ChannelID: meta.ID, FromSeq: 1, Limit: 1, MaxBytes: 1})
		next := fetch.CommittedSeq + 1
		pull, err := c.transport.Pull(ctx, meta.Leader, transport.PullRequest{ChannelKey: key, ChannelID: meta.ID, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, Follower: c.localNode, NextOffset: next, MaxBytes: c.pullMaxBytes})
		if err != nil || len(pull.Records) == 0 {
			continue
		}
		future, err := c.group.Submit(ctx, key, reactor.Event{Kind: reactor.EventApplyRecords, Key: key, PullResponse: pull})
		if err != nil {
			continue
		}
		result, err := future.Await(ctx)
		if err != nil {
			continue
		}
		_ = c.transport.Ack(ctx, meta.Leader, transport.AckRequest{ChannelKey: key, Epoch: meta.Epoch, LeaderEpoch: meta.LeaderEpoch, Follower: c.localNode, MatchOffset: result.ApplyLEO})
	}
	return ctx.Err()
}

func (c *cluster) Close() error {
	if c == nil || c.group == nil {
		return nil
	}
	return c.group.Close()
}
