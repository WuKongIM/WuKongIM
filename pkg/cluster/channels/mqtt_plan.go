package channels

import (
	"context"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

type mqttPlanForwardRequest struct {
	// Leader binds the serving node and prevents recursive forwarding.
	Leader  ch.NodeID
	Request ch.MQTTReplayPlanRequest
}
type mqttPlanForwarder interface {
	ForwardMQTTPlan(context.Context, ch.NodeID, ch.MQTTReplayPlanRequest) (ch.MQTTReplayPlan, error)
}

// PlanMQTTReplay surrounds the reactor's coherent read with fresh Slot authority.
// The plan is scheduling evidence, not source-release or replica-readiness proof.
func (s *Service) PlanMQTTReplay(ctx context.Context, q ch.MQTTReplayPlanRequest) (ch.MQTTReplayPlan, error) {
	return s.planMQTTReplay(ctx, q, 0)
}

func (s *Service) handleForwardMQTTPlan(ctx context.Context, q mqttPlanForwardRequest) (ch.MQTTReplayPlan, error) {
	if s == nil || q.Leader == 0 || q.Leader != s.localNode {
		return ch.MQTTReplayPlan{}, ch.ErrNotLeader
	}
	return s.planMQTTReplay(ctx, q.Request, q.Leader)
}

func (s *Service) planMQTTReplay(ctx context.Context, q ch.MQTTReplayPlanRequest, serving ch.NodeID) (ch.MQTTReplayPlan, error) {
	var empty ch.MQTTReplayPlan
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
	// Immutable planning shares recovery's stable-fence authority check. Copy and
	// anchor admission continue to require unfenced metadata independently.
	authorityRequest := ch.MQTTReplayRequest{ChannelID: q.ChannelID, ExpectedChannelEpoch: q.ExpectedChannelEpoch, ExpectedLeaderEpoch: q.ExpectedLeaderEpoch, ExpectedRouteGeneration: q.ExpectedRouteGeneration}
	m, err := s.mqttRepairAuthority(ctx, authorityRequest)
	if err != nil {
		return empty, err
	}
	if serving != 0 && m.Leader != serving {
		return empty, ch.ErrNotLeader
	}
	var p ch.MQTTReplayPlan
	if m.Leader == s.localNode {
		planner, ok := s.runtime.(ch.MQTTReplayPlanner)
		if !ok {
			return empty, ch.ErrInvalidConfig
		}
		if err = s.applyRuntimeMetaContext(ctx, m, true, true); err != nil {
			return empty, err
		}
		p, err = planner.PlanMQTTReplay(ctx, q)
	} else {
		f, ok := s.forward.(mqttPlanForwarder)
		if !ok {
			return empty, ch.ErrInvalidConfig
		}
		p, err = f.ForwardMQTTPlan(ctx, m.Leader, q)
	}
	if err != nil {
		return empty, err
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	if err = s.recheckMQTTRepairAuthority(ctx, authorityRequest, m); err != nil {
		return empty, err
	}
	if !p.ValidFor(q) {
		return empty, ch.ErrLogConflict
	}
	return p, nil
}

func (g *ServiceGateway) handleForwardMQTTPlan(ctx context.Context, q mqttPlanForwardRequest) (ch.MQTTReplayPlan, error) {
	s, err := g.service()
	if err != nil {
		return ch.MQTTReplayPlan{}, err
	}
	return s.handleForwardMQTTPlan(ctx, q)
}

var _ ch.MQTTReplayPlanner = (*Service)(nil)
