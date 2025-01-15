package cluster

const (
	MsgTypeUnknown uint32 = iota
	MsgTypeSlot           // slot
	MsgTypeChannel        // channel
	MsgTypeNode           // node
)
