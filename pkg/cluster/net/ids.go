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
	// RPCChannelPull serves Channel follower pull requests.
	RPCChannelPull
	// RPCChannelAck serves Channel follower acknowledgements.
	RPCChannelAck
	// RPCChannelPullHint serves Channel pull hints.
	RPCChannelPullHint
	// RPCChannelNotify serves legacy Channel notify requests.
	RPCChannelNotify
	// RPCControlStateSync serves Controller state sync requests.
	RPCControlStateSync
	// RPCControlReportNode serves node report requests.
	RPCControlReportNode
	// RPCControlReportSlots serves Slot runtime report requests.
	RPCControlReportSlots
	// RPCChannelAppend forwards one Channel append request to the channel leader.
	RPCChannelAppend
	// RPCChannelAppendBatch forwards one Channel append batch request to the channel leader.
	RPCChannelAppendBatch
	// RPCControlRaft carries Controller Raft protocol messages.
	RPCControlRaft
	// RPCControlTaskResult serves Controller task result and progress writes.
	RPCControlTaskResult
	// RPCPresenceAuthority serves internal UID connection authority requests.
	RPCPresenceAuthority
	// RPCPresenceOwner serves internal owner-node connection actions.
	RPCPresenceOwner
	// RPCDeliveryPush serves internal owner-node delivery push batches.
	RPCDeliveryPush
	// RPCDeliveryFanout serves internal authority-node delivery fanout tasks.
	RPCDeliveryFanout
	// RPCChannelPullBatch serves grouped Channel follower pull requests.
	RPCChannelPullBatch
	// RPCChannelPullHintBatch serves grouped Channel pull hints.
	RPCChannelPullHintBatch
	// RPCChannelLastVisible serves routed Channel last-visible message reads.
	RPCChannelLastVisible
	// RPCConversationAuthority serves internal UID conversation authority cache requests.
	RPCConversationAuthority
	// RPCChannelAuthoritySend serves internal SEND forwarding requests to the channel append authority.
	RPCChannelAuthoritySend
	// RPCManagerConnection serves internal owner-node manager connection inventory requests.
	RPCManagerConnection
	// RPCManagerLogs serves internal node-local manager distributed log reads.
	RPCManagerLogs
	// RPCManagerControllerRaft serves internal node-local manager Controller Raft operations.
	RPCManagerControllerRaft
	// RPCManagerSlotRaft serves internal node-local manager Slot Raft operations.
	RPCManagerSlotRaft
	// RPCManagerChannels serves internal node-local manager channel list reads.
	RPCManagerChannels
	// RPCManagerDBInspect serves internal node-local manager DB inspect reads.
	RPCManagerDBInspect
	// RPCManagerAppLogs serves internal selected-node ordinary application log reads.
	RPCManagerAppLogs
	// RPCManagerDiagnostics serves internal node-local diagnostics trace reads and tracking rules.
	RPCManagerDiagnostics
	// RPCManagerPlugins serves internal node-local manager plugin inventory reads.
	RPCManagerPlugins
	// RPCPluginBindingScan serves cluster Slot-leader plugin binding index scans.
	RPCPluginBindingScan
)

const (
	// RPCControlWrite serves generic Controller writes.
	RPCControlWrite uint8 = 64 + iota
	// RPCManagerMessageRetention serves internal manager message retention forwarding.
	RPCManagerMessageRetention
	// RPCNodeLifecycle serves internal seed join and readiness RPCs.
	RPCNodeLifecycle
	// RPCSlotStatus serves cluster Slot leader status snapshots.
	RPCSlotStatus
	// RPCManagerTaskAudit serves internal retained Controller task audit reads.
	RPCManagerTaskAudit
	// RPCChannelMigrationMeta serves cluster Slot-leader channel migration state reads.
	RPCChannelMigrationMeta
	// RPCMessageEventAppend forwards message event appends and compact state reads to the Slot leader.
	RPCMessageEventAppend
	// RPCManagerNodeConfig serves internal selected-node effective config reads.
	RPCManagerNodeConfig
	// RPCManagerLatestMessages serves node-local newest-message index reads.
	RPCManagerLatestMessages
)

func transportServiceAlias(serviceID uint8) string {
	switch serviceID {
	case MsgSlotRaft:
		return "slot raft"
	case MsgSlotRaftBatch:
		return "slot raft batch"
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
	case RPCManagerNodeConfig:
		return "manager node config"
	case RPCManagerLatestMessages:
		return "manager latest messages"
	case RPCManagerMessageRetention:
		return "manager message retention"
	case RPCNodeLifecycle:
		return "node lifecycle"
	case RPCControlWrite:
		return "controller write"
	case RPCPluginBindingScan:
		return "plugin binding scan"
	case RPCSlotStatus:
		return "slot status"
	case RPCManagerTaskAudit:
		return "manager task audit"
	case RPCChannelMigrationMeta:
		return "channel migration meta"
	case RPCMessageEventAppend:
		return "message event append"
	default:
		return "unknown service"
	}
}
