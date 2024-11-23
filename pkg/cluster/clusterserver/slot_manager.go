package cluster

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

var _ reactor.IRequest = &slotManager{}

type slotManager struct {
	slotReactor *reactor.Reactor
	opts        *Options
	s           *Server
}

func newSlotManager(s *Server) *slotManager {

	sm := &slotManager{
		opts: s.opts,
		s:    s,
	}

	sm.slotReactor = reactor.New(reactor.NewOptions(
		reactor.WithNodeId(s.opts.NodeId),
		reactor.WithSend(sm.onSend),
		reactor.WithIsCommittedAfterApplied(true),
		reactor.WithReactorType(reactor.ReactorTypeSlot),
		reactor.WithRequest(sm),
		reactor.WithSubReactorNum(s.opts.SlotReactorSubCount),
	))

	return sm
}

func (s *slotManager) start() error {

	return s.slotReactor.Start()
}

func (s *slotManager) stop() {
	s.slotReactor.Stop()
}

func (s *slotManager) proposeAndWait(ctx context.Context, slotId uint32, logs []replica.Log) ([]reactor.ProposeResult, error) {
	return s.slotReactor.ProposeAndWait(ctx, SlotIdToKey(slotId), logs)
}

func (s *slotManager) add(st *slot) {
	s.slotReactor.AddHandler(st.key, st)
}

func (s *slotManager) get(slotId uint32) *slot {
	handler := s.slotReactor.Handler(SlotIdToKey(slotId))
	if handler != nil {
		return handler.(*slot)
	}
	return nil
}

func (s *slotManager) remove(slotId uint32) {
	s.slotReactor.RemoveHandler(SlotIdToKey(slotId))
}

func (s *slotManager) addMessage(m reactor.Message) {
	s.slotReactor.AddMessage(m)
}

func (s *slotManager) iterate(f func(*slot) bool) {
	s.slotReactor.IteratorHandler(func(h reactor.IHandler) bool {
		return f(h.(*slot))
	})
}

func (s *slotManager) exist(slotId uint32) bool {
	return s.slotReactor.ExistHandler(SlotIdToKey(slotId))
}

func (s *slotManager) onSend(m reactor.Message) {
	s.opts.Send(ShardTypeSlot, m)
}

func (s *slotManager) GetConfig(req reactor.ConfigReq) (reactor.ConfigResp, error) {

	handler := s.slotReactor.Handler(req.HandlerKey)
	if handler != nil {
		slot := handler.(*slot)
		cfg := slot.st

		var role = replica.RoleFollower
		if cfg.Leader == s.opts.NodeId {
			role = replica.RoleLeader
		}
		return reactor.ConfigResp{
			HandlerKey: req.HandlerKey,
			Config: replica.Config{
				MigrateFrom: cfg.MigrateFrom,
				MigrateTo:   cfg.MigrateTo,
				Replicas:    cfg.Replicas,
				Learners:    cfg.Learners,
				Role:        role,
				Leader:      cfg.Leader,
				Term:        cfg.Term,
			},
		}, nil
	}
	return reactor.EmptyConfigResp, nil
}

func (s *slotManager) GetLeaderTermStartIndex(req reactor.LeaderTermStartIndexReq) (uint64, error) {
	reqBytes, err := req.Marshal()
	if err != nil {
		return 0, err
	}
	if req.LeaderId == s.opts.NodeId { // 如果是自己，直接返回0，0表示不需要解决冲突
		return 0, nil
	}
	resp, err := s.request(req.LeaderId, "/slot/leaderTermStartIndex", reqBytes)
	if err != nil {
		return 0, err
	}
	if resp.Status != proto.StatusOK {
		return 0, fmt.Errorf("get leader term start index failed, status: %v", resp.Status)
	}
	if len(resp.Body) > 0 {
		return binary.BigEndian.Uint64(resp.Body), nil
	}
	return 0, nil
}

func (s *slotManager) Append(req reactor.AppendLogReq) error {

	return s.opts.SlotLogStorage.Append(req)
}

func (s *slotManager) request(toNodeId uint64, path string, body []byte) (*proto.Response, error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), s.opts.ReqTimeout)
	defer cancel()
	return s.s.RequestWithContext(timeoutCtx, toNodeId, path, body)
}
