package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
)

type slotManager struct {
	slotReactor *reactor.Reactor
	opts        *Options
}

func newSlotManager(opts *Options) *slotManager {

	sm := &slotManager{
		opts: opts,
	}

	sm.slotReactor = reactor.New(reactor.NewOptions(
		reactor.WithNodeId(opts.NodeId),
		reactor.WithSend(sm.onSend),
		reactor.WithIsCommittedAfterApplied(true),
		reactor.WithReactorType(reactor.ReactorTypeSlot),
		reactor.WithOnAppendLogs(func(reqs []reactor.AppendLogReq) error {
			if reqs == nil {
				return nil
			}
			return opts.SlotLogStorage.AppendLogBatch(reqs)
		}),
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

func (s *slotManager) get(slotId uint32) reactor.IHandler {
	return s.slotReactor.Handler(SlotIdToKey(slotId))
}

func (s *slotManager) addMessage(m reactor.Message) {
	s.slotReactor.AddMessage(m)
}

func (s *slotManager) slotLen() int {
	return s.slotReactor.HandlerLen()
}

func (s *slotManager) exist(slotId uint32) bool {
	return s.slotReactor.ExistHandler(SlotIdToKey(slotId))
}

func (s *slotManager) onSend(m reactor.Message) {
	s.opts.Send(ShardTypeSlot, m)
}
