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
	// RPCControlTaskResult serves ControllerV2 task result and progress writes.
	RPCControlTaskResult
	// RPCPresenceAuthority serves internalv2 UID connection authority requests.
	RPCPresenceAuthority
	// RPCPresenceOwner serves internalv2 owner-node connection actions.
	RPCPresenceOwner
	// RPCDeliveryPush serves internalv2 owner-node delivery push batches.
	RPCDeliveryPush
	// RPCDeliveryFanout serves internalv2 authority-node delivery fanout tasks.
	RPCDeliveryFanout
	// RPCChannelPullBatch serves grouped ChannelV2 follower pull requests.
	RPCChannelPullBatch
	// RPCChannelPullHintBatch serves grouped ChannelV2 pull hints.
	RPCChannelPullHintBatch
	// RPCChannelLastVisible serves routed ChannelV2 last-visible message reads.
	RPCChannelLastVisible
	// RPCConversationAuthority serves internalv2 UID conversation authority cache requests.
	RPCConversationAuthority
	// RPCChannelAuthoritySend serves internalv2 SEND forwarding requests to the channel append authority.
	RPCChannelAuthoritySend
	// RPCManagerConnection serves internalv2 owner-node manager connection inventory requests.
	RPCManagerConnection
	// RPCManagerLogs serves internalv2 node-local manager distributed log reads.
	RPCManagerLogs
	// RPCManagerControllerRaft serves internalv2 node-local manager Controller Raft operations.
	RPCManagerControllerRaft
	// RPCManagerSlotRaft serves internalv2 node-local manager Slot Raft operations.
	RPCManagerSlotRaft
	// RPCManagerChannels serves internalv2 node-local manager channel list reads.
	RPCManagerChannels
	// RPCManagerDBInspect serves internalv2 node-local manager DB inspect reads.
	RPCManagerDBInspect
	// RPCManagerAppLogs serves internalv2 selected-node ordinary application log reads.
	RPCManagerAppLogs
	// RPCManagerDiagnostics serves internalv2 node-local diagnostics trace reads and tracking rules.
	RPCManagerDiagnostics
	// RPCManagerPlugins serves internalv2 node-local manager plugin inventory reads.
	RPCManagerPlugins
	// RPCPluginBindingScan serves clusterv2 Slot-leader plugin binding index scans.
	RPCPluginBindingScan
)

func transportServiceAlias(serviceID uint8) string {
	switch serviceID {
	case RPCSlotForwardPropose:
		return "slot propose"
	case RPCChannelPull:
		return "channel pull"
	case RPCChannelAck:
		return "channel ack"
	case RPCChannelPullHint:
		return "channel pull hint"
	case RPCChannelNotify:
		return "channel notify"
	case RPCControlStateSync:
		return "controller state sync"
	case RPCControlReportNode:
		return "controller node report"
	case RPCControlReportSlots:
		return "slot runtime report"
	case RPCChannelAppend:
		return "channel append"
	case RPCChannelAppendBatch:
		return "channel append batch"
	case RPCControlRaft:
		return "controller raft"
	case RPCControlTaskResult:
		return "controller task result"
	case RPCPresenceAuthority:
		return "presence authority"
	case RPCPresenceOwner:
		return "presence owner action"
	case RPCDeliveryPush:
		return "delivery push"
	case RPCDeliveryFanout:
		return "delivery fanout"
	case RPCChannelPullBatch:
		return "channel pull batch"
	case RPCChannelPullHintBatch:
		return "channel pull hint batch"
	case RPCChannelLastVisible:
		return "channel last visible"
	case RPCConversationAuthority:
		return "conversation authority"
	case RPCChannelAuthoritySend:
		return "send authority"
	case RPCManagerConnection:
		return "manager connections"
	case RPCManagerLogs:
		return "manager logs"
	case RPCManagerControllerRaft:
		return "manager controller raft"
	case RPCManagerSlotRaft:
		return "manager slot raft"
	case RPCManagerChannels:
		return "manager channels"
	case RPCManagerDBInspect:
		return "manager DB inspect"
	case RPCManagerAppLogs:
		return "manager app logs"
	case RPCManagerDiagnostics:
		return "manager diagnostics"
	case RPCManagerPlugins:
		return "manager plugins"
	case RPCPluginBindingScan:
		return "plugin binding scan"
	default:
		return "unknown service"
	}
}
