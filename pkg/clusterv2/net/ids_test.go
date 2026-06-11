package clusternet

import "testing"

func TestRPCServiceIDsAreUniqueAndNonZero(t *testing.T) {
	ids := map[string]uint8{
		"slot_forward_propose":    RPCSlotForwardPropose,
		"channel_pull":            RPCChannelPull,
		"channel_ack":             RPCChannelAck,
		"channel_pull_hint":       RPCChannelPullHint,
		"channel_notify":          RPCChannelNotify,
		"control_state_sync":      RPCControlStateSync,
		"control_report_node":     RPCControlReportNode,
		"control_report_slots":    RPCControlReportSlots,
		"channel_append":          RPCChannelAppend,
		"channel_append_batch":    RPCChannelAppendBatch,
		"control_raft":            RPCControlRaft,
		"presence_authority":      RPCPresenceAuthority,
		"presence_owner":          RPCPresenceOwner,
		"delivery_push":           RPCDeliveryPush,
		"delivery_fanout":         RPCDeliveryFanout,
		"channel_pull_batch":      RPCChannelPullBatch,
		"channel_pull_hint_batch": RPCChannelPullHintBatch,
		"channel_last_visible":    RPCChannelLastVisible,
		"conversation_authority":  RPCConversationAuthority,
		"channel_authority_send":  RPCChannelAuthoritySend,
	}
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
