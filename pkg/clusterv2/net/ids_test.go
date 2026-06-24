package clusternet

import "testing"

func TestRPCServiceIDsAreUniqueAndNonZero(t *testing.T) {
	ids := rpcServiceIDsForTest()
	seen := make(map[uint8]string, len(ids))
	for name, id := range ids {
		if id == 0 {
			t.Fatalf("%s service id is zero", name)
		}
		if prev, ok := seen[id]; ok {
			t.Fatalf("%s and %s share service id %d", prev, name, id)
		}
		seen[id] = name
	}
}

func rpcServiceIDsForTest() map[string]uint8 {
	return map[string]uint8{
		"msg_slot_raft":             MsgSlotRaft,
		"msg_slot_raft_batch":       MsgSlotRaftBatch,
		"slot_forward_propose":      RPCSlotForwardPropose,
		"channel_pull":              RPCChannelPull,
		"channel_ack":               RPCChannelAck,
		"channel_pull_hint":         RPCChannelPullHint,
		"channel_notify":            RPCChannelNotify,
		"control_state_sync":        RPCControlStateSync,
		"control_report_node":       RPCControlReportNode,
		"control_report_slots":      RPCControlReportSlots,
		"channel_append":            RPCChannelAppend,
		"channel_append_batch":      RPCChannelAppendBatch,
		"control_raft":              RPCControlRaft,
		"control_task_result":       RPCControlTaskResult,
		"presence_authority":        RPCPresenceAuthority,
		"presence_owner":            RPCPresenceOwner,
		"delivery_push":             RPCDeliveryPush,
		"delivery_fanout":           RPCDeliveryFanout,
		"channel_pull_batch":        RPCChannelPullBatch,
		"channel_pull_hint_batch":   RPCChannelPullHintBatch,
		"channel_last_visible":      RPCChannelLastVisible,
		"conversation_authority":    RPCConversationAuthority,
		"channel_authority_send":    RPCChannelAuthoritySend,
		"manager_connection":        RPCManagerConnection,
		"manager_logs":              RPCManagerLogs,
		"manager_controller_raft":   RPCManagerControllerRaft,
		"manager_slot_raft":         RPCManagerSlotRaft,
		"manager_channels":          RPCManagerChannels,
		"manager_db_inspect":        RPCManagerDBInspect,
		"manager_app_logs":          RPCManagerAppLogs,
		"manager_diagnostics":       RPCManagerDiagnostics,
		"manager_plugins":           RPCManagerPlugins,
		"manager_message_retention": RPCManagerMessageRetention,
		"node_lifecycle":            RPCNodeLifecycle,
		"plugin_binding_scan":       RPCPluginBindingScan,
		"slot_status":               RPCSlotStatus,
		"control_write":             RPCControlWrite,
	}
}
