package service

import (
	"context"
	"errors"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
)

const (
	pullHintReceiveStageStateCheck   = "state_check"
	pullHintReceiveStageLoaded       = "loaded"
	pullHintReceiveStageMetaResolve  = "meta_resolve"
	pullHintReceiveStageMetaHint     = "meta_hint"
	pullHintReceiveStageMetaValidate = "meta_validate"
	pullHintReceiveStageMetaApply    = "meta_apply"
	pullHintReceiveStageSubmit       = "submit"
	pullHintReceiveStageAwait        = "await"
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

// HandlePullHint serves a leader pull hint and lazily activates unloaded followers.
func (c *cluster) HandlePullHint(ctx context.Context, req transport.PullHintRequest) error {
	if err := c.ensurePullHintChannelState(ctx, req); err != nil {
		return err
	}
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
	c.observePullHintReceived(req.Reason, pullHintReceiveStageStateCheck, err)
	if err != nil {
		return err
	}
	if loaded {
		c.observePullHintReceived(req.Reason, pullHintReceiveStageLoaded, nil)
		return nil
	}
	meta, err := c.resolvePullHintMeta(ctx, req)
	c.observePullHintReceived(req.Reason, pullHintReceiveStageMetaResolve, err)
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
		c.observePullHintReceived(req.Reason, pullHintReceiveStageMetaValidate, ch.ErrStaleMeta)
		return ch.ErrStaleMeta
	}
	err = c.applyMeta(ctx, meta)
	c.observePullHintReceived(req.Reason, pullHintReceiveStageMetaApply, err)
	return err
}

func (c *cluster) resolvePullHintMeta(ctx context.Context, req transport.PullHintRequest) (ch.Meta, error) {
	if c.metaResolver != nil {
		meta, err := c.metaResolver.ResolveChannelMeta(ctx, req.ChannelID)
		if err == nil || !errors.Is(err, ch.ErrChannelNotFound) {
			return meta, err
		}
	}
	return ch.Meta{}, ch.ErrChannelNotFound
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

func metaContainsReplica(replicas []ch.NodeID, node ch.NodeID) bool {
	for _, replica := range replicas {
		if replica == node {
			return true
		}
	}
	return false
}
