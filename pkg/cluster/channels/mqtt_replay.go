package channels

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

type mqttReplayForwardRequest struct {
	// Leader binds a single serving node and prevents recursive forwarding.
	Leader  ch.NodeID
	Request ch.MQTTReplayRequest
}

type mqttReplayForwarder interface {
	ForwardMQTTReplay(context.Context, ch.NodeID, ch.MQTTReplayRequest) (ch.MQTTReplayPage, error)
}

func validMQTTReplayRequest(req ch.MQTTReplayRequest) bool {
	return req.Valid() && len(req.ChannelID.ID) <= 1024 && utf8.ValidString(req.ChannelID.ID) && !strings.ContainsRune(req.ChannelID.ID, 0)
}

// PrepareMQTTReplay routes one bounded page through fresh Channel authority.
// Local preparation cannot authorize source release or substitute for a copy quorum.
func (s *Service) PrepareMQTTReplay(ctx context.Context, req ch.MQTTReplayRequest) (ch.MQTTReplayPage, error) {
	return s.prepareMQTTReplay(ctx, req, 0)
}

func (s *Service) handleForwardMQTTReplay(ctx context.Context, req mqttReplayForwardRequest) (ch.MQTTReplayPage, error) {
	if s == nil || req.Leader == 0 || req.Leader != s.localNode {
		return ch.MQTTReplayPage{}, ch.ErrNotLeader
	}
	return s.prepareMQTTReplay(ctx, req.Request, req.Leader)
}

func (s *Service) prepareMQTTReplay(ctx context.Context, req ch.MQTTReplayRequest, serving ch.NodeID) (ch.MQTTReplayPage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return ch.MQTTReplayPage{}, err
	}
	if s == nil || !validMQTTReplayRequest(req) {
		return ch.MQTTReplayPage{}, ch.ErrInvalidConfig
	}
	reader, ok := s.metaSource.(FreshChannelMetaSource)
	if !ok {
		return ch.MQTTReplayPage{}, ch.ErrInvalidConfig
	}
	meta, err := reader.ResolveChannelMetaFresh(ctx, req.ChannelID)
	if err != nil {
		return ch.MQTTReplayPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return ch.MQTTReplayPage{}, err
	}
	if err = validateMQTTChannelAuthority(req.ChannelID, req.ExpectedChannelEpoch, req.ExpectedLeaderEpoch, req.ExpectedRouteGeneration, meta); err != nil {
		return ch.MQTTReplayPage{}, err
	}
	if serving != 0 && meta.Leader != serving {
		return ch.MQTTReplayPage{}, ch.ErrNotLeader
	}
	var page ch.MQTTReplayPage
	if meta.Leader == s.localNode {
		preparer, ok := s.runtime.(ch.MQTTReplayPreparer)
		if !ok {
			return ch.MQTTReplayPage{}, ch.ErrInvalidConfig
		}
		if err = s.applyRequestMetaContext(ctx, meta); err != nil {
			return ch.MQTTReplayPage{}, err
		}
		page, err = preparer.PrepareMQTTReplay(ctx, req)
	} else {
		forward, ok := s.forward.(mqttReplayForwarder)
		if !ok {
			return ch.MQTTReplayPage{}, ch.ErrInvalidConfig
		}
		page, err = forward.ForwardMQTTReplay(ctx, meta.Leader, req)
	}
	if err != nil {
		return ch.MQTTReplayPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return ch.MQTTReplayPage{}, err
	}
	current, err := reader.ResolveChannelMetaFresh(ctx, req.ChannelID)
	if err != nil {
		return ch.MQTTReplayPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return ch.MQTTReplayPage{}, err
	}
	if err = validateMQTTChannelAuthority(req.ChannelID, req.ExpectedChannelEpoch, req.ExpectedLeaderEpoch, req.ExpectedRouteGeneration, current); err != nil {
		return ch.MQTTReplayPage{}, err
	}
	if current.Leader != meta.Leader || current.MinISR != meta.MinISR || current.Status != meta.Status || !sameWriteFence(current.WriteFence, meta.WriteFence) ||
		!slices.Equal(current.Replicas, meta.Replicas) || !slices.Equal(current.ISR, meta.ISR) {
		return ch.MQTTReplayPage{}, ch.ErrStaleMeta
	}
	if !page.ValidFor(req.Range) {
		return ch.MQTTReplayPage{}, ch.ErrLogConflict
	}
	return page, nil
}

func (g *ServiceGateway) handleForwardMQTTReplay(ctx context.Context, req mqttReplayForwardRequest) (ch.MQTTReplayPage, error) {
	s, err := g.service()
	if err != nil {
		return ch.MQTTReplayPage{}, err
	}
	return s.handleForwardMQTTReplay(ctx, req)
}

var _ ch.MQTTReplayPreparer = (*Service)(nil)
