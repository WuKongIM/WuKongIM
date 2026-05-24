package service

import (
	"context"
	"errors"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
)

// HandlePull serves a follower pull request on the local leader.
func (c *cluster) HandlePull(ctx context.Context, req transport.PullRequest) (transport.PullResponse, error) {
	future, err := c.group.Submit(ctx, req.ChannelKey, reactor.Event{Kind: reactor.EventPull, Key: req.ChannelKey, Context: ctx, Pull: req, OpID: c.group.NextOpID()})
	if err != nil {
		return transport.PullResponse{}, err
	}
	result, err := future.Await(ctx)
	if err != nil {
		return transport.PullResponse{}, err
	}
	return result.Pull, nil
}

// HandleAck serves a follower progress ACK on the local leader.
func (c *cluster) HandleAck(ctx context.Context, req transport.AckRequest) error {
	future, err := c.group.Submit(ctx, req.ChannelKey, reactor.Event{Kind: reactor.EventAck, Key: req.ChannelKey, Ack: req})
	if err != nil {
		return err
	}
	_, err = future.Await(ctx)
	return err
}

// HandlePullHint serves a leader pull hint and lazily activates unloaded followers.
func (c *cluster) HandlePullHint(ctx context.Context, req transport.PullHintRequest) error {
	if err := c.ensurePullHintChannelState(ctx, req); err != nil {
		return err
	}
	future, err := c.group.Submit(ctx, req.ChannelKey, reactor.Event{Kind: reactor.EventPullHint, Key: req.ChannelKey, PullHint: req})
	if err != nil {
		return err
	}
	_, err = future.Await(ctx)
	return err
}

// HandleNotify maps legacy transport compatibility nudges onto PullHint handling.
func (c *cluster) HandleNotify(ctx context.Context, req transport.NotifyRequest) error {
	err := c.HandlePullHint(ctx, transport.PullHintRequest{
		ChannelKey:      req.ChannelKey,
		ChannelID:       req.ChannelID,
		Epoch:           req.Epoch,
		LeaderEpoch:     req.LeaderEpoch,
		Leader:          req.Leader,
		LeaderLEO:       req.LeaderLEO,
		ActivityVersion: req.LeaderLEO,
		Reason:          transport.PullHintReasonAppend,
	})
	if errors.Is(err, ch.ErrChannelNotFound) || errors.Is(err, ch.ErrStaleMeta) {
		return nil
	}
	return err
}

func (c *cluster) ensurePullHintChannelState(ctx context.Context, req transport.PullHintRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	loaded, err := c.group.HasChannelState(ctx, req.ChannelKey)
	if err != nil {
		return err
	}
	if loaded {
		return nil
	}
	if c.metaResolver == nil {
		return ch.ErrChannelNotFound
	}
	meta, err := c.metaResolver.ResolveChannelMeta(ctx, req.ChannelID)
	if err != nil {
		return err
	}
	if meta.ID == (ch.ChannelID{}) {
		meta.ID = req.ChannelID
	}
	if meta.Key == "" {
		meta.Key = ch.ChannelKeyForID(meta.ID)
	}
	local := c.localNode
	if meta.Key != req.ChannelKey || meta.ID != req.ChannelID || meta.Epoch != req.Epoch || meta.LeaderEpoch != req.LeaderEpoch ||
		meta.Leader != req.Leader || meta.Leader == local || meta.Status != ch.StatusActive ||
		!metaContainsReplica(meta.Replicas, local) {
		return ch.ErrStaleMeta
	}
	return c.applyMeta(ctx, meta)
}

func metaContainsReplica(replicas []ch.NodeID, node ch.NodeID) bool {
	for _, replica := range replicas {
		if replica == node {
			return true
		}
	}
	return false
}
