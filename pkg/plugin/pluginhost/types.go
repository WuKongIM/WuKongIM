package pluginhost

import "time"

// Method identifies a byte-oriented plugin hook supported by the runtime.
type Method string

const (
	MethodSend         Method = "Send"
	MethodPersistAfter Method = "PersistAfter"
	MethodReceive      Method = "Receive"
	MethodRoute        Method = "Route"
	MethodConfigUpdate Method = "ConfigUpdate"
)

// Status describes the node-local runtime state observed for a plugin.
type Status string

const (
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusOffline  Status = "offline"
	StatusError    Status = "error"
	StatusDisabled Status = "disabled"
)

// ObservedPlugin is the node-local runtime view of a plugin process.
type ObservedPlugin struct {
	// No is the stable plugin number used as the runtime UID.
	No string
	// Name is the human-readable plugin name from the manifest.
	Name string
	// Version is the plugin version from the manifest.
	Version string
	// Methods lists byte-oriented hook methods advertised by the plugin.
	Methods []Method
	// Priority controls hook ordering; larger values run before smaller values.
	Priority int
	// PersistAfterSync reports whether PersistAfter waits for plugin completion.
	PersistAfterSync bool
	// ReplySync reports whether replies wait for plugin completion.
	ReplySync bool
	// ConfigTemplateRaw stores the raw config template bytes from the manifest.
	ConfigTemplateRaw []byte
	// Status is the current node-local process status.
	Status Status
	// Enabled reports whether this plugin is allowed to run on this node.
	Enabled bool
	// PID is the process ID when the plugin has a running local process.
	PID int
	// LastSeenAt records the last successful runtime observation time.
	LastSeenAt time.Time
	// LastError stores the most recent runtime error message, if any.
	LastError string
}
