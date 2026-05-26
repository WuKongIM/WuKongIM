package clusternet

const (
	// MsgSlotRaft carries one Slot Raft message.
	MsgSlotRaft uint8 = 32 + iota
	// MsgSlotRaftBatch carries a batch of Slot Raft messages.
	MsgSlotRaftBatch
)

const (
	// RPCSlotForwardPropose forwards one Slot metadata proposal to the Slot leader.
	RPCSlotForwardPropose uint8 = 1 + iota
	// RPCChannelPull serves ChannelV2 follower pull requests.
	RPCChannelPull
	// RPCChannelAck serves ChannelV2 follower acknowledgements.
	RPCChannelAck
	// RPCChannelPullHint serves ChannelV2 pull hints.
	RPCChannelPullHint
	// RPCChannelNotify serves legacy ChannelV2 notify requests.
	RPCChannelNotify
	// RPCControlStateSync serves ControllerV2 state sync requests.
	RPCControlStateSync
	// RPCControlReportNode serves node report requests.
	RPCControlReportNode
	// RPCControlReportSlots serves Slot runtime report requests.
	RPCControlReportSlots
	// RPCChannelAppend forwards one ChannelV2 append request to the channel leader.
	RPCChannelAppend
	// RPCChannelAppendBatch forwards one ChannelV2 append batch request to the channel leader.
	RPCChannelAppendBatch
	// RPCControlRaft carries ControllerV2 Raft protocol messages.
	RPCControlRaft
)
