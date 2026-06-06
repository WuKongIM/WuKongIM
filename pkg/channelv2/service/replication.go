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

// HandlePullBatch serves grouped follower pull requests on the local leader.
func (c *cluster) HandlePullBatch(ctx context.Context, req transport.PullBatchRequest) (transport.PullBatchResponse, error) {
	resp := transport.PullBatchResponse{Items: make([]transport.PullBatchItemResult, len(req.Items))}
	futures := make([]*reactor.Future, len(req.Items))
	for i, item := range req.Items {
		future, err := c.group.Submit(ctx, item.ChannelKey, reactor.Event{Kind: reactor.EventPull, Key: item.ChannelKey, Context: ctx, Pull: item, OpID: c.group.NextOpID()})
		if err != nil {
			resp.Items[i].Err = err
			continue
		}
		futures[i] = future
	}
	for i, future := range futures {
		if future == nil {
			continue
		}
		result, err := future.Await(ctx)
		if err != nil {
			resp.Items[i].Err = err
			continue
		}
		resp.Items[i].Response = result.Pull
	}
	return resp, nil
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

// HandlePullHintBatch serves grouped leader pull hints through owning reactors.
func (c *cluster) HandlePullHintBatch(ctx context.Context, req transport.PullHintBatchRequest) (transport.PullHintBatchResponse, error) {
	resp := transport.PullHintBatchResponse{Items: make([]transport.PullHintBatchItemResult, len(req.Items))}
	futures := make([]*reactor.Future, len(req.Items))
	for i, item := range req.Items {
		future, err := c.group.Submit(ctx, item.ChannelKey, reactor.Event{Kind: reactor.EventPullHint, Key: item.ChannelKey, PullHint: item})
		c.observePullHintReceived(item.Reason, pullHintReceiveStageSubmit, err)
		if err != nil {
			resp.Items[i].Err = err
			continue
		}
		futures[i] = future
	}
	for i, future := range futures {
		if future == nil {
			continue
		}
		_, err := future.Await(ctx)
		resp.Items[i].Err = err
		c.observePullHintReceived(req.Items[i].Reason, pullHintReceiveStageAwait, err)
	}
	return resp, nil
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
