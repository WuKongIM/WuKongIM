package cluster

import "sync"

type SlotManager struct {
	sync.RWMutex
	slotMap map[uint32]*Slot
}

func NewSlotManager() *SlotManager {

	return &SlotManager{
		slotMap: make(map[uint32]*Slot),
	}
}

func (s *SlotManager) AddSlot(st *Slot) {
	s.Lock()
	defer s.Unlock()
	s.slotMap[st.slotID] = st
}

func (s *SlotManager) RemoveSlot(slotID uint32) {
	s.Lock()
	defer s.Unlock()
	delete(s.slotMap, slotID)
}

func (s *SlotManager) GetSlot(slotID uint32) *Slot {
	s.RLock()
	defer s.RUnlock()
	return s.slotMap[slotID]
}

func (s *SlotManager) GetSlots() []*Slot {
	s.RLock()
	defer s.RUnlock()
	slots := make([]*Slot, 0, len(s.slotMap))
	for _, slot := range s.slotMap {
		slots = append(slots, slot)
	}
	return slots
}
