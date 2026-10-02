package channels

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

type mqttOriginalsForwarder interface {
	ForwardMQTTOriginals(context.Context, ch.NodeID, ch.MQTTReplayOriginalRequest) (ch.MQTTReplayOriginalResult, error)
}

// ReadMQTTOriginals captures one recovered plan and reads its anchored page
// between fresh authority checks. It reuses the existing four-reader admission,
// native replica refresh and immutable storage proof; no result is cached.
func (s *Service) ReadMQTTOriginals(ctx context.Context, q ch.MQTTReplayOriginalRequest) (ch.MQTTReplayOriginalResult, error) {
	return s.readMQTTOriginals(ctx, q, 0)
}
func (s *Service) handleForwardMQTTOriginals(ctx context.Context, q mqttOriginalsForwardRequest) (ch.MQTTReplayOriginalResult, error) {
	if s == nil || q.Leader == 0 || q.Leader != s.localNode {
		return ch.MQTTReplayOriginalResult{}, ch.ErrNotLeader
	}
	return s.readMQTTOriginals(ctx, q.Request, q.Leader)
}
func (s *Service) readMQTTOriginals(ctx context.Context, q ch.MQTTReplayOriginalRequest, serving ch.NodeID) (ch.MQTTReplayOriginalResult, error) {
	var empty ch.MQTTReplayOriginalResult
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if s == nil || !q.Valid() {
		return empty, ch.ErrInvalidConfig
	}
	m, err := s.mqttRepairAuthority(ctx, q.Request)
	if err != nil {
		return empty, err
	}
	if serving != 0 && m.Leader != serving {
		return empty, ch.ErrNotLeader
	}
	var result ch.MQTTReplayOriginalResult
	if m.Leader == s.localNode {
		planner, ok := s.runtime.(ch.MQTTReplayPlanner)
		if !ok {
			return empty, ch.ErrInvalidConfig
		}
		if err = s.applyRequestMetaContext(ctx, m); err != nil {
			return empty, err
		}
		result.Plan, err = planner.PlanMQTTReplay(ctx, q.PlanRequest())
		if err != nil {
			return empty, err
		}
		if err = ctx.Err(); err != nil {
			return empty, err
		}
		p := result.Plan
		if !p.ValidFor(q.PlanRequest()) || p.Source.StartAfter > q.StartAfter || p.Source.CommittedThrough < q.AccountedThrough {
			return empty, ch.ErrLogConflict
		}
		if !p.HasAnchor {
			return empty, ch.ErrNotReady
		}
		if p.Anchor.Anchor.Through < q.AccountedThrough {
			return empty, ch.ErrLogConflict
		}
		if s.replicaCommitRefresh == nil {
			return empty, ch.ErrInvalidConfig
		}
		// Refresh is a bounded native hint, never proof from the plan's captured HW.
		// Its authority is checked by replication and again here after the read.
		if err = s.replicaCommitRefresh.RequestCommittedReplicaRefresh(ctx, mqttReplicaAuthority(m)); err != nil {
			return empty, err
		}
		if err = ctx.Err(); err != nil {
			return empty, err
		}
		select {
		case s.mqttConsumerReads <- struct{}{}:
			defer func() { <-s.mqttConsumerReads }()
		default:
			return empty, ch.ErrBackpressured
		}
		if s.store == nil {
			return empty, ch.ErrInvalidConfig
		}
		handle, openErr := s.store.ChannelStore(ch.ChannelKeyForID(q.Request.ChannelID), q.Request.ChannelID)
		if openErr != nil {
			return empty, openErr
		}
		defer handle.Close()
		reader, ok := handle.(channelstore.MQTTReplayConsumerReader)
		if !ok {
			return empty, ch.ErrInvalidConfig
		}
		result.Page, err = reader.ReadMQTTReplayAnchor(ctx, p.Anchor.Manifest.LastOffset, q.Request.Range)
	} else {
		forward, ok := s.forward.(mqttOriginalsForwarder)
		if !ok {
			return empty, ch.ErrInvalidConfig
		}
		result, err = forward.ForwardMQTTOriginals(ctx, m.Leader, q)
	}
	if err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if err = s.recheckMQTTRepairAuthority(ctx, q.Request, m); err != nil {
		return empty, err
	}
	if !result.ValidFor(q) {
		return empty, ch.ErrLogConflict
	}
	return result, nil
}

var _ ch.MQTTReplayOriginalReader = (*Service)(nil)
