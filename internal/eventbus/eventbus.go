package eventbus

type slotLeaderChangeListener func(slotId uint32, oldLeaderId uint64, newLeaderId uint64)

type EventBus struct {
	slotLeaderChangeListeners []slotLeaderChangeListener
}

var G = NewEventBus()

func NewEventBus() *EventBus {
	return &EventBus{}
}

func (e *EventBus) ListenSlotLeaderChange(listener slotLeaderChangeListener) {
	e.slotLeaderChangeListeners = append(e.slotLeaderChangeListeners, listener)
}

func (e *EventBus) NotifySlotLeaderChange(slotId uint32, oldLeaderId, newLeader uint64) {
	for _, listener := range e.slotLeaderChangeListeners {
		listener(slotId, oldLeaderId, newLeader)
	}
}
