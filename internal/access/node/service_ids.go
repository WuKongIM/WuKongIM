package node

const (
	presenceRPCServiceID              uint8 = 5
	deliverySubmitRPCServiceID        uint8 = 6
	deliveryPushRPCServiceID          uint8 = 7
	deliveryAckRPCServiceID           uint8 = 8
	deliveryOfflineRPCServiceID       uint8 = 9
	conversationFactsRPCServiceID     uint8 = 13
	channelAppendRPCServiceID         uint8 = 33
	channelMessagesRPCServiceID       uint8 = 36
	channelLeaderRepairRPCServiceID   uint8 = 37
	channelLeaderEvaluateRPCServiceID uint8 = 38
	runtimeSummaryRPCServiceID        uint8 = 39
	connectionsRPCServiceID           uint8 = 40
	connectionRPCServiceID            uint8 = 41
	diagnosticsRPCServiceID           uint8 = 42
	channelRetentionRPCServiceID      uint8 = 43
	deliveryTagRPCServiceID           uint8 = 44
	systemUIDCacheRPCServiceID        uint8 = 45
	channelLeaderTransferRPCServiceID uint8 = 46
	// 47 is used by slot/proxy channel migration, 48 by channel transport fence/drain, 49 by slot/proxy CMD conversation state, and 53 by slot/proxy plugin binding.
	cmdSyncRPCServiceID             uint8 = 50
	diagnosticsTrackingRPCServiceID uint8 = 51
	monitorMetricsRPCServiceID      uint8 = 52
	pluginHTTPForwardRPCServiceID   uint8 = 54
)
