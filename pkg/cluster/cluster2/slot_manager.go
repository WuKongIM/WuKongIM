package cluster

import "github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"

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
	))

	return sm
}

func (s *slotManager) start() error {

	return s.slotReactor.Start()
}

func (s *slotManager) stop() {
	s.slotReactor.Stop()
}

func (s *slotManager) add(st *slot) {
	s.slotReactor.AddHandler(st.key, st)
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
