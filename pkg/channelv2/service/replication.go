package service

import (
	"context"
	"errors"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
)

const (
	pullHintReceiveStageSubmit = "submit"
	pullHintReceiveStageAwait  = "await"
)

type pullHintReceiveObserver interface {
	ObservePullHintReceived(reason transport.PullHintReason, stage string, err error)
}

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

// HandleAck serves a stopped-follower lifecycle ACK on the local leader.
func (c *cluster) HandleAck(ctx context.Context, req transport.AckRequest) error {
	future, err := c.group.Submit(ctx, req.ChannelKey, reactor.Event{Kind: reactor.EventAck, Key: req.ChannelKey, Ack: req})
	if err != nil {
		return err
	}
	_, err = future.Await(ctx)
	return err
}

// HandlePullHint serves a leader pull hint through the owning reactor.
func (c *cluster) HandlePullHint(ctx context.Context, req transport.PullHintRequest) error {
	future, err := c.group.Submit(ctx, req.ChannelKey, reactor.Event{Kind: reactor.EventPullHint, Key: req.ChannelKey, PullHint: req})
	c.observePullHintReceived(req.Reason, pullHintReceiveStageSubmit, err)
	if err != nil {
		return err
	}
	_, err = future.Await(ctx)
	c.observePullHintReceived(req.Reason, pullHintReceiveStageAwait, err)
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
	if errors.Is(err, ch.ErrChannelNotFound) || errors.Is(err, ch.ErrStaleMeta) || errors.Is(err, ch.ErrInvalidConfig) || errors.Is(err, ch.ErrNotReplica) {
		return nil
	}
	return err
}

func (c *cluster) observePullHintReceived(reason transport.PullHintReason, stage string, err error) {
	if c == nil {
		return
	}
	observer, ok := c.observer.(pullHintReceiveObserver)
	if !ok {
		return
	}
	observer.ObservePullHintReceived(reason, stage, err)
}
