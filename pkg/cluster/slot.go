package cluster

import "time"

type Slot struct {
}

func NewSlot() *Slot {
	return &Slot{}
}

func (s *Slot) Start() error {

	return nil
}

func (s *Slot) AddReplica(slotID SlotID, nodeID NodeID) error {

	return nil
}

func (s *Slot) RemoveReplica(slotID SlotID, nodeID NodeID) error {

	return nil
}

func (s *Slot) WaitLeader(timeout time.Duration) error {
	return nil
}

func (s *Slot) MustWaitLeader(timeout time.Duration) {
}
