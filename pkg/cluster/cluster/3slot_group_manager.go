package cluster

type slotGroupManager struct {
	opts      *Options
	slotGroup *slotGroup
}

func newSlotGroupManager(opts *Options) *slotGroupManager {
	return &slotGroupManager{
		opts:      opts,
		slotGroup: newSlotGroup(opts),
	}
}

func (s *slotGroupManager) start() error {
	return s.slotGroup.start()
}

func (s *slotGroupManager) stop() {
	s.slotGroup.stop()
}

func (s *slotGroupManager) exist(slotId uint32) bool {
	return s.slotGroup.exist(slotId)
}

func (s *slotGroupManager) addSlot(st *slot) {
	s.slotGroup.addSlot(st)
}

func (s *slotGroupManager) slot(slotId uint32) *slot {

	return s.slotGroup.slot(slotId)
}

func (s *slotGroupManager) proposeAndWaitCommit(slotId uint32, data []byte) error {
	st := s.slot(slotId)
	if st == nil {
		return ErrSlotNotFound
	}
	return st.proposeAndWaitCommit(data, s.opts.proposeTimeout)
}
