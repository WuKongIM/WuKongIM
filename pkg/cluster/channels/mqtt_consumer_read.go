package channels

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

const mqttConsumerConcurrent = 4

type mqttConsumerReadForwardRequest struct {
	Leader  ch.NodeID
	Request ch.MQTTReplayConsumerRequest
}
type mqttConsumerReadForwarder interface {
	ForwardMQTTConsumerRead(context.Context, ch.NodeID, ch.MQTTReplayConsumerRequest) (ch.MQTTReplayConsumerPage, error)
}

// ReadMQTTReplay routes one bounded page under fresh authority. Stable write
// fences permit this immutable read; foreground maintenance still gates entry.
func (s *Service) ReadMQTTReplay(ctx context.Context, q ch.MQTTReplayConsumerRequest) (ch.MQTTReplayConsumerPage, error) {
	return s.readMQTTReplay(ctx, q, 0)
}

func (s *Service) handleForwardMQTTConsumerRead(ctx context.Context, q mqttConsumerReadForwardRequest) (ch.MQTTReplayConsumerPage, error) {
	if s == nil || q.Leader == 0 || q.Leader != s.localNode {
		return ch.MQTTReplayConsumerPage{}, ch.ErrNotLeader
	}
	return s.readMQTTReplay(ctx, q.Request, q.Leader)
}

func (s *Service) readMQTTReplay(ctx context.Context, q ch.MQTTReplayConsumerRequest, serving ch.NodeID) (ch.MQTTReplayConsumerPage, error) {
	var empty ch.MQTTReplayConsumerPage
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
	if serving != 0 && serving != m.Leader {
		return empty, ch.ErrNotLeader
	}
	var page ch.MQTTReplayConsumerPage
	if m.Leader == s.localNode {
		select {
		case s.mqttConsumerReads <- struct{}{}:
			defer func() { <-s.mqttConsumerReads }()
		default:
			return empty, ch.ErrBackpressured
		}
		if s.store == nil {
			return empty, ch.ErrInvalidConfig
		}
		handle, e := s.store.ChannelStore(ch.ChannelKeyForID(q.Request.ChannelID), q.Request.ChannelID)
		if e != nil {
			return empty, e
		}
		defer handle.Close()
		reader, ok := handle.(channelstore.MQTTReplayConsumerReader)
		if !ok {
			return empty, ch.ErrInvalidConfig
		}
		page, err = reader.ReadMQTTReplayAnchor(ctx, q.AnchorPosition, q.Request.Range)
	} else {
		forward, ok := s.forward.(mqttConsumerReadForwarder)
		if !ok {
			return empty, ch.ErrInvalidConfig
		}
		page, err = forward.ForwardMQTTConsumerRead(ctx, m.Leader, q)
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
	if !page.ValidFor(q.Request.ChannelID, q.Request.Range) {
		return empty, ch.ErrLogConflict
	}
	return page, nil
}

func (g *ServiceGateway) handleForwardMQTTConsumerRead(ctx context.Context, q mqttConsumerReadForwardRequest) (ch.MQTTReplayConsumerPage, error) {
	s, err := g.service()
	if err != nil {
		return ch.MQTTReplayConsumerPage{}, err
	}
	return s.handleForwardMQTTConsumerRead(ctx, q)
}

var _ ch.MQTTReplayConsumerReader = (*Service)(nil)
