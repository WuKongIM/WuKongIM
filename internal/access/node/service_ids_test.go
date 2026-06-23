package node

import "testing"

func TestRPCServiceIDsAreUniqueAndNonZero(t *testing.T) {
	ids := map[string]uint8{
		"presence":              presenceRPCServiceID,
		"deliverySubmit":        deliverySubmitRPCServiceID,
		"deliveryPush":          deliveryPushRPCServiceID,
		"deliveryAck":           deliveryAckRPCServiceID,
		"deliveryOffline":       deliveryOfflineRPCServiceID,
		"conversationFacts":     conversationFactsRPCServiceID,
		"channelAppend":         channelAppendRPCServiceID,
		"channelPlaneAppend":    channelPlaneAppendRPCServiceID,
		"channelMessages":       channelMessagesRPCServiceID,
		"channelLeaderRepair":   channelLeaderRepairRPCServiceID,
		"channelLeaderEvaluate": channelLeaderEvaluateRPCServiceID,
		"runtimeSummary":        runtimeSummaryRPCServiceID,
		"connections":           connectionsRPCServiceID,
		"connection":            connectionRPCServiceID,
		"diagnostics":           diagnosticsRPCServiceID,
		"channelRetention":      channelRetentionRPCServiceID,
		"deliveryTag":           deliveryTagRPCServiceID,
		"systemUIDCache":        systemUIDCacheRPCServiceID,
		"channelLeaderTransfer": channelLeaderTransferRPCServiceID,
		"cmdSync":               cmdSyncRPCServiceID,
		"diagnosticsTracking":   diagnosticsTrackingRPCServiceID,
		"pluginHTTPForward":     pluginHTTPForwardRPCServiceID,
		"pluginManagement":      pluginManagementRPCServiceID,
		"pluginCommitted":       pluginCommittedRPCServiceID,
	}
	seen := make(map[uint8]string, len(ids))
	for name, id := range ids {
		if id == 0 {
			t.Fatalf("%s service id is zero", name)
		}
		if existing, ok := seen[id]; ok {
			t.Fatalf("duplicate service id %d: %s and %s", id, existing, name)
		}
		seen[id] = name
	}
	for reserved, owner := range map[uint8]string{47: "slot/proxy channel migration", 48: "channel transport fence/drain", 49: "slot/proxy CMD conversation state", 52: "retired manager monitor metrics", 53: "slot/proxy plugin binding"} {
		if name, ok := seen[reserved]; ok {
			t.Fatalf("service id %d is reserved for %s but used by %s", reserved, owner, name)
		}
	}
}
