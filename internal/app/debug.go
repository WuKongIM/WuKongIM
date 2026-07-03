package app

func (a *App) debugConfigSnapshot() any {
	if a == nil {
		return map[string]any{}
	}
	return map[string]any{
		"node_id":            diagnosticsNodeID(a.cfg),
		"node_data_dir":      a.cfg.DataDir,
		"cluster_listen":     a.cfg.Cluster.ListenAddr,
		"api_listen":         a.cfg.API.ListenAddr,
		"gateway_listeners":  len(a.cfg.Gateway.Listeners),
		"metrics_enable":     a.cfg.Observability.MetricsEnabled,
		"debug_api_enable":   a.cfg.Observability.DebugAPIEnabled,
		"diagnostics_enable": a.cfg.Observability.Diagnostics.Enabled,
	}
}

func (a *App) debugClusterSnapshot() any {
	if a == nil {
		return map[string]any{}
	}
	clusterCfg := defaultClusterConfig(a.cfg)
	return map[string]any{
		"node_id":                  clusterCfg.NodeID,
		"cluster_listen":           clusterCfg.ListenAddr,
		"cluster_id":               clusterCfg.Control.ClusterID,
		"controller_role":          string(clusterCfg.Control.Role),
		"controller_voters":        len(clusterCfg.Control.Voters),
		"initial_slot_count":       clusterCfg.Slots.InitialSlotCount,
		"hash_slot_count":          clusterCfg.Slots.HashSlotCount,
		"slot_replica_count":       clusterCfg.Slots.ReplicaCount,
		"channel_replica_count":    clusterCfg.Channel.ReplicaCount,
		"channel_reactor_count":    clusterCfg.Channel.ReactorCount,
		"channel_max_loaded_count": clusterCfg.Channel.MaxChannels,
	}
}
