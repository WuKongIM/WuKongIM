package channels

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

type mqttRetirementSelectionForwardRequest struct {
	Leader  ch.NodeID
	Request ch.MQTTReplayRetirementSelectionRequest
}
type mqttRetirementSelectionForwarder interface {
	ForwardMQTTReplayRetirementSelection(context.Context, ch.NodeID, ch.MQTTReplayRetirementSelectionRequest) (ch.MQTTReplayRetirementSelection, error)
}

// SelectMQTTReplayRetirement binds a read-only reverse journal page to current
// routing and the original capture, without advancing any committed watermark.
func (s *Service) SelectMQTTReplayRetirement(ctx context.Context, q ch.MQTTReplayRetirementSelectionRequest) (ch.MQTTReplayRetirementSelection, error) {
	return s.selectMQTTReplayRetirement(ctx, q, 0)
}

func (s *Service) handleMQTTReplayRetirementSelection(ctx context.Context, q mqttRetirementSelectionForwardRequest) (ch.MQTTReplayRetirementSelection, error) {
	if s == nil || q.Leader == 0 || q.Leader != s.localNode {
		return ch.MQTTReplayRetirementSelection{}, ch.ErrNotLeader
	}
	return s.selectMQTTReplayRetirement(ctx, q.Request, s.localNode)
}

func (s *Service) selectMQTTReplayRetirement(ctx context.Context, q ch.MQTTReplayRetirementSelectionRequest, serving ch.NodeID) (ch.MQTTReplayRetirementSelection, error) {
	var empty ch.MQTTReplayRetirementSelection
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
	e := q.Source
	authority := ch.MQTTReplayRequest{ChannelID: e.ChannelID, ExpectedChannelEpoch: e.ExpectedChannelEpoch, ExpectedLeaderEpoch: e.ExpectedLeaderEpoch, ExpectedRouteGeneration: e.ExpectedRouteGeneration}
	m, err := s.mqttRepairAuthority(ctx, authority)
	if err != nil {
		return empty, err
	}
	if serving != 0 && m.Leader != serving {
		return empty, ch.ErrNotLeader
	}
	var p ch.MQTTReplayRetirementSelection
	if m.Leader == s.localNode {
		p, err = s.readMQTTRetirementSelection(ctx, q)
	} else if f, ok := s.forward.(mqttRetirementSelectionForwarder); ok {
		p, err = f.ForwardMQTTReplayRetirementSelection(ctx, m.Leader, q)
	} else {
		return empty, ch.ErrInvalidConfig
	}
	if err != nil {
		return empty, err
	}
	if err = s.recheckMQTTRepairAuthority(ctx, authority, m); err != nil {
		return empty, err
	}
	if !q.Accepts(p) {
		return empty, ch.ErrLogConflict
	}
	return p, nil
}

// readMQTTRetirementSelection shares the bounded immutable-proof read budget
// with donors and closes its lease on every path, including panic unwinding.
func (s *Service) readMQTTRetirementSelection(ctx context.Context, q ch.MQTTReplayRetirementSelectionRequest) (ch.MQTTReplayRetirementSelection, error) {
	var empty ch.MQTTReplayRetirementSelection
	if s.store == nil {
		return empty, ch.ErrInvalidConfig
	}
	select {
	case s.mqttRepairDonors <- struct{}{}:
		defer func() { <-s.mqttRepairDonors }()
	default:
		return empty, ch.ErrBackpressured
	}
	handle, err := s.store.ChannelStore(ch.ChannelKeyForID(q.Source.ChannelID), q.Source.ChannelID)
	if err != nil {
		return empty, err
	}
	if handle == nil {
		return empty, ch.ErrInvalidConfig
	}
	defer handle.Close()
	selector, ok := handle.(channelstore.MQTTReplayRetirementSelector)
	if !ok {
		return empty, ch.ErrInvalidConfig
	}
	return selector.SelectMQTTReplayRetirementAnchor(ctx, q.Scan())
}

func (g *ServiceGateway) handleMQTTReplayRetirementSelection(ctx context.Context, q mqttRetirementSelectionForwardRequest) (ch.MQTTReplayRetirementSelection, error) {
	s, err := g.service()
	if err != nil {
		return ch.MQTTReplayRetirementSelection{}, err
	}
	return s.handleMQTTReplayRetirementSelection(ctx, q)
}

var _ ch.MQTTReplayRetirementSelector = (*Service)(nil)
