package plugin

// PluginBinding records a cluster-authoritative UID to plugin association.
// Task 8 adds the durable slot-backed binding store; Task 6 only needs the DTO
// for deterministic Receive selection at the usecase boundary.
type PluginBinding struct {
	// PluginNo is the plugin selected for a UID.
	PluginNo string
	// UID is the user id whose offline Receive hook targets the plugin.
	UID string
}
