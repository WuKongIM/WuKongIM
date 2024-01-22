package cluster

type SlotGroupManager struct {
}

func NewSlotGroupManager() *SlotGroupManager {
	return &SlotGroupManager{}
}

func (s *SlotGroupManager) Exist(id uint64) bool {
	return false
}

func (s *SlotGroupManager) AddSlot(slot *Slot) {
}
