package clusterv2

// ProposeRequest submits one Slot metadata command through clusterv2 routing.
type ProposeRequest struct {
	// Key is the logical routing key used when Target does not carry an explicit hash slot.
	Key string
	// Command is the encoded Slot state machine command payload.
	Command []byte
	// Target optionally carries an explicit hash slot and physical Slot target.
	Target ProposeTarget
}

// ProposeTarget identifies an explicit hash slot and optional physical Slot target.
type ProposeTarget struct {
	// HashSlot is the logical hash slot. It is valid only when HasHashSlot is true.
	HashSlot uint16
	// HasHashSlot distinguishes an explicit hash slot 0 from an omitted hash slot.
	HasHashSlot bool
	// SlotID is the physical Slot ID. It is valid only when HasSlotID is true.
	SlotID uint32
	// HasSlotID indicates whether SlotID was explicitly provided by the caller.
	HasSlotID bool
}

// Route describes the current routing decision for one hash slot.
type Route struct {
	// HashSlot is the logical hash slot selected for the request.
	HashSlot uint16
	// SlotID is the physical Slot that owns HashSlot.
	SlotID uint32
	// Leader is the best-known Slot Raft leader node ID. Zero means unknown.
	Leader uint64
	// Peers are the desired Slot replica node IDs from the latest control snapshot.
	Peers []uint64
	// Revision is the control snapshot revision that produced this route.
	Revision uint64
}

// Snapshot summarizes local clusterv2 readiness and routing state.
type Snapshot struct {
	// NodeID is this node's stable cluster identity.
	NodeID uint64
	// ControllerLead is the best-known Controller leader node ID.
	ControllerLead uint64
	// StateRevision is the latest applied control snapshot revision.
	StateRevision uint64
	// RoutesReady reports whether a valid route table is installed.
	RoutesReady bool
	// SlotsReady reports whether local assigned Slots have been opened or bootstrapped.
	SlotsReady bool
	// ChannelsReady reports whether the ChannelV2 service is available.
	ChannelsReady bool
	// SlotCount is the number of physical Slots in the current control view.
	SlotCount uint32
	// HashSlotCount is the number of logical hash slots in the current route table.
	HashSlotCount uint16
}
