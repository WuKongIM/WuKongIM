package plugin

import (
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
)

const (
	// SecretHidden is the manager-visible placeholder for configured secret values.
	SecretHidden = "******"
)

var (
	// ErrDesiredPluginNotFound reports that no node-local desired state exists.
	ErrDesiredPluginNotFound = errors.New("plugin desired state not found")
	// ErrPluginNotFound reports that the runtime has no observed plugin with the requested number.
	ErrPluginNotFound = errors.New("plugin not found")
	// ErrPluginInfoRequired reports that plugin startup did not include plugin metadata.
	ErrPluginInfoRequired = errors.New("plugin info required")
	// ErrPluginNoRequired reports that a plugin operation did not include a plugin number.
	ErrPluginNoRequired = errors.New("plugin no required")
	// ErrRuntimeRequired reports that the plugin usecase was built without a runtime port.
	ErrRuntimeRequired = errors.New("plugin runtime required")
	// ErrDesiredStoreRequired reports that the plugin usecase was built without a desired-state store.
	ErrDesiredStoreRequired = errors.New("plugin desired store required")
	// ErrInvokerRequired reports that a runnable plugin hook cannot be invoked without an invoker.
	ErrInvokerRequired = errors.New("plugin invoker required")
	// ErrPluginIdentityMismatch reports a mismatch between transport UID and plugin manifest number.
	ErrPluginIdentityMismatch = errors.New("plugin identity mismatch")
	// ErrPluginIdentityRequired reports that startup did not include a transport plugin UID.
	ErrPluginIdentityRequired = errors.New("plugin identity required")
	// ErrInvalidPluginNo reports a plugin number that is not filename-safe.
	ErrInvalidPluginNo = errors.New("invalid plugin no")
	// ErrMessageSenderRequired reports that host message/send needs the message usecase port.
	ErrMessageSenderRequired = errors.New("plugin message sender required")
	// ErrMessageReaderRequired reports that host channel/messages needs the message reader port.
	ErrMessageReaderRequired = errors.New("plugin message reader required")
	// ErrDefaultSenderUIDRequired reports that plugin-origin sends without fromUid need a default sender.
	ErrDefaultSenderUIDRequired = errors.New("plugin default sender uid required")
	// ErrClusterReaderRequired reports that host cluster/config needs the cluster reader port.
	ErrClusterReaderRequired = errors.New("plugin cluster reader required")
	// ErrChannelOwnerReaderRequired reports that host belong-node lookup needs the channel owner reader port.
	ErrChannelOwnerReaderRequired = errors.New("plugin channel owner reader required")
	// ErrChannelRequired reports that a host RPC omitted a required channel.
	ErrChannelRequired = errors.New("plugin channel required")
	// ErrChannelOwnerUnknown reports that a channel owner could not be determined authoritatively.
	ErrChannelOwnerUnknown = errors.New("plugin channel owner unknown")
	// ErrConversationReaderRequired reports that host conversation/channels needs the conversation reader port.
	ErrConversationReaderRequired = errors.New("plugin conversation reader required")
	// ErrConversationUIDRequired reports that host conversation/channels omitted the UID.
	ErrConversationUIDRequired = errors.New("plugin conversation uid required")
	// ErrHTTPForwarderRequired reports that remote plugin HTTP forward needs a node forwarder.
	ErrHTTPForwarderRequired = errors.New("plugin http forwarder required")
	// ErrHTTPForwardFanoutDeferred reports that broadcast HTTP forward is not implemented in phase 1.
	ErrHTTPForwardFanoutDeferred = errors.New("plugin http forward fanout deferred")
	// ErrHTTPForwardBodyTooLarge reports that a plugin HTTP body exceeds the configured limit.
	ErrHTTPForwardBodyTooLarge = errors.New("plugin http forward body too large")
	// ErrHTTPForwardHeaderTooLarge reports that plugin HTTP headers exceed the configured limit.
	ErrHTTPForwardHeaderTooLarge = errors.New("plugin http forward headers too large")
)

var pluginNoPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Method identifies a plugin hook advertised by a plugin manifest.
type Method string

const (
	// MethodSend is invoked before a message is appended.
	MethodSend Method = "Send"
	// MethodPersistAfter is invoked after a durable message is committed.
	MethodPersistAfter Method = "PersistAfter"
	// MethodReceive is invoked for eligible offline bound recipients.
	MethodReceive Method = "Receive"
	// MethodRoute forwards HTTP-compatible plugin routes.
	MethodRoute Method = "Route"
	// MethodConfigUpdate notifies a running plugin that its config changed.
	MethodConfigUpdate Method = "ConfigUpdate"
)

// Status describes the node-local runtime state observed for a plugin.
type Status string

const (
	// StatusStarting means the local process has started but has not completed host startup.
	StatusStarting Status = "starting"
	// StatusRunning means the plugin has an active host RPC connection.
	StatusRunning Status = "running"
	// StatusOffline means the plugin is not connected on this node.
	StatusOffline Status = "offline"
	// StatusError means the runtime observed a plugin process or host RPC error.
	StatusError Status = "error"
	// StatusDisabled means desired state prevents this node from running the plugin.
	StatusDisabled Status = "disabled"
)

// ObservedPlugin is the node-local runtime view consumed by plugin usecases.
type ObservedPlugin struct {
	// No is the stable plugin number used as the runtime UID.
	No string
	// Name is the human-readable plugin name from the manifest.
	Name string
	// Version is the plugin version from the manifest.
	Version string
	// Methods lists hook methods advertised by the plugin.
	Methods []Method
	// Priority controls hook ordering; larger values run first.
	Priority int
	// PersistAfterSync reports whether PersistAfter waits for plugin completion.
	PersistAfterSync bool
	// ReplySync reports whether Receive waits for plugin completion.
	ReplySync bool
	// ConfigTemplateRaw stores pluginproto.ConfigTemplate bytes from the manifest.
	ConfigTemplateRaw []byte
	// Status is the current node-local process status.
	Status Status
	// Enabled reports whether this node should run the plugin.
	Enabled bool
	// PID is the local operating-system process id when available.
	PID int
	// LastSeenAt records the latest runtime observation time.
	LastSeenAt time.Time
	// LastError stores the latest runtime error message.
	LastError string
}

// DesiredPlugin is the durable node-local desired state for one plugin.
type DesiredPlugin struct {
	// No is the stable plugin number.
	No string
	// Config stores plugin configuration as a JSON object.
	Config json.RawMessage
	// Enabled controls whether this node should run the plugin.
	Enabled bool
	// CreatedAt records when desired state was first persisted.
	CreatedAt time.Time
	// UpdatedAt records when desired state last changed.
	UpdatedAt time.Time
}

// LocalPluginList is a node-scoped plugin inventory response.
type LocalPluginList struct {
	// NodeID is the node whose local plugin inventory was read.
	NodeID uint64
	// Plugins contains observed local plugins sorted by plugin number.
	Plugins []LocalPlugin
}

// LocalPlugin is the manager-facing summary of one node-local plugin.
type LocalPlugin struct {
	// NodeID is the node whose local plugin inventory was read.
	NodeID uint64
	// No is the stable plugin number.
	No string
	// Name is the human-readable plugin name.
	Name string
	// Version is the plugin version.
	Version string
	// ConfigTemplate is the plugin-declared config schema.
	ConfigTemplate *pluginproto.ConfigTemplate
	// Config is the desired config with secret values redacted.
	Config map[string]any
	// CreatedAt records when desired state was first persisted.
	CreatedAt *time.Time
	// UpdatedAt records when desired state last changed.
	UpdatedAt *time.Time
	// Status is the current node-local runtime status.
	Status Status
	// Enabled reports whether this node should run the plugin.
	Enabled bool
	// Methods lists hook methods advertised by the plugin.
	Methods []Method
	// Priority controls hook ordering; larger values run first.
	Priority int
	// PersistAfterSync reports whether PersistAfter waits for plugin completion.
	PersistAfterSync bool
	// ReplySync reports whether Receive waits for plugin completion.
	ReplySync bool
	// IsAI is set when the plugin supports the legacy Receive AI hook.
	IsAI uint8
	// PID is the local operating-system process id when available.
	PID int
	// LastSeenAt records the latest runtime observation time.
	LastSeenAt time.Time
	// LastError stores the latest runtime error message.
	LastError string
}

// LocalPluginDetail is the manager-facing detail of one node-local plugin.
type LocalPluginDetail = LocalPlugin

// ClusterSnapshot is the authoritative cluster view exposed to legacy plugins.
type ClusterSnapshot struct {
	// Nodes contains cluster node metadata.
	Nodes []ClusterNode
	// Slots contains physical Slot placement and runtime leadership metadata.
	Slots []ClusterSlot
}

// ClusterNode is one node in a plugin-compatible cluster snapshot.
type ClusterNode struct {
	// ID is the stable cluster node identifier.
	ID uint64
	// ClusterAddr is the node-to-node cluster RPC address.
	ClusterAddr string
	// APIServerAddr is the node API address when an adapter can provide it.
	APIServerAddr string
	// Online reports whether the controller currently considers the node alive.
	Online bool
}

// ClusterSlot is one physical Slot in a plugin-compatible cluster snapshot.
type ClusterSlot struct {
	// ID is the physical Slot identifier.
	ID uint32
	// Leader is the currently observed Slot leader. Zero means unknown.
	Leader uint64
	// Term is the observed leader term when available.
	Term uint32
	// Replicas is the desired replica set for the Slot.
	Replicas []uint64
}
