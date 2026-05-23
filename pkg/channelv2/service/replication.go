package service

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
)

// HandlePull serves a follower pull request on the local leader.
func (c *cluster) HandlePull(ctx context.Context, req transport.PullRequest) (transport.PullResponse, error) {
	future, err := c.group.Submit(ctx, req.ChannelKey, reactor.Event{Kind: reactor.EventPull, Key: req.ChannelKey, Pull: req, OpID: c.group.NextOpID()})
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
