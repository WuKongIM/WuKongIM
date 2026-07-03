package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

var (
	// ErrDesiredPluginNotFound reports that no node-local desired state exists.
	ErrDesiredPluginNotFound = errors.New("plugin desired state not found")
	// ErrPluginNoRequired reports that a plugin operation omitted the plugin number.
	ErrPluginNoRequired = errors.New("plugin no required")
	// ErrPluginCallerMismatch reports a mismatch between the authenticated caller and plugin number.
	ErrPluginCallerMismatch = errors.New("plugin caller mismatch")
	// ErrPluginNotFound reports that a requested plugin observation does not exist.
	ErrPluginNotFound = errors.New("plugin not found")
	// ErrRuntimeRequired reports that the plugin usecase was built without a runtime port.
	ErrRuntimeRequired = errors.New("plugin runtime required")
	// ErrDesiredStoreRequired reports that a plugin config mutation has no desired-state store.
	ErrDesiredStoreRequired = errors.New("plugin desired store required")
	// ErrInvokerRequired reports that the plugin usecase was built without an invocation port.
	ErrInvokerRequired = errors.New("plugin invoker required")
	// ErrInvalidPluginNo reports a plugin number that is not safe for node-local lifecycle storage.
	ErrInvalidPluginNo = errors.New("invalid plugin no")
	// ErrMessageSenderRequired reports that host message/send needs the message usecase port.
	ErrMessageSenderRequired = errors.New("plugin message sender required")
	// ErrDefaultSenderUIDRequired reports that plugin-origin sends without fromUid need a default sender.
	ErrDefaultSenderUIDRequired = errors.New("plugin default sender uid required")
	// ErrMessageReaderRequired reports that host channel/messages needs the message reader port.
	ErrMessageReaderRequired = errors.New("plugin message reader required")
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
	// ErrReceiveBindingReaderRequired reports that Receive hook selection needs a UID binding reader.
	ErrReceiveBindingReaderRequired = errors.New("plugin receive binding reader required")
)

const (
	// SecretHidden is the manager-visible placeholder for configured secret values.
	SecretHidden = "******"
)

var pluginNoPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Method identifies a plugin hook advertised by a plugin manifest.
type Method string

const (
	// MethodSend is invoked before a message append.
	MethodSend Method = "Send"
	// MethodPersistAfter is invoked after a durable message is committed.
	MethodPersistAfter Method = "PersistAfter"
	// MethodReceive is invoked for eligible offline bound recipients.
	MethodReceive Method = "Receive"
	// MethodConfigUpdate notifies a running plugin that its config changed.
	MethodConfigUpdate Method = "ConfigUpdate"
)

// Status describes the node-local runtime state observed for a plugin.
type Status string

const (
	// StatusStarting means the process has started but has not completed host startup.
	StatusStarting Status = "starting"
	// StatusRunning means the plugin has an active host RPC connection.
	StatusRunning Status = "running"
	// StatusOffline means the plugin is not connected on this node.
	StatusOffline Status = "offline"
	// StatusError means the runtime observed a plugin process or host RPC error.
	StatusError Status = "error"
	// StatusDisabled means this node should not invoke the plugin.
	StatusDisabled Status = "disabled"
)

// ObservedPlugin is the node-local runtime view consumed by v2 plugin hooks.
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
	// ReplySync reports whether reply hooks wait for plugin completion.
	ReplySync bool
	// ConfigTemplateRaw stores pluginproto.ConfigTemplate bytes from the manifest.
	ConfigTemplateRaw []byte
	// Status is the current node-local process status.
	Status Status
	// Enabled reports whether this node should invoke the plugin.
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
	// Enabled controls whether this node should run and invoke the plugin.
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
	// Plugins contains node-local plugin rows.
	Plugins []LocalPlugin
}

// LocalPlugin is the manager-facing summary of one node-local plugin.
type LocalPlugin struct {
	// NodeID identifies the node whose local plugin registry was read.
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
	// Methods lists hook methods advertised by the plugin.
	Methods []Method
	// Priority controls hook ordering; larger values run first.
	Priority int
	// PersistAfterSync reports whether PersistAfter waits for plugin completion.
	PersistAfterSync bool
	// ReplySync reports whether Receive waits for plugin completion.
	ReplySync bool
	// Status is the current node-local process status.
	Status Status
	// Enabled reports whether this node should invoke the plugin.
	Enabled bool
	// IsAI is reserved for legacy Receive AI hook display compatibility.
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

// Runtime stores node-local observed plugin state for the v2 usecase.
type Runtime interface {
	RegisterObserved(context.Context, ObservedPlugin) error
	MarkClosed(context.Context, string) error
	List() []ObservedPlugin
}

// DesiredStore persists node-local plugin config and enable state.
type DesiredStore interface {
	// Get returns the desired state for one plugin.
	Get(context.Context, string) (DesiredPlugin, error)
	// Save writes the desired state for one plugin.
	Save(context.Context, DesiredPlugin) error
	// Delete removes the desired state for one plugin.
	Delete(context.Context, string) error
}

// Invoker calls or sends PDK-compatible plugin hook payloads.
type Invoker interface {
	RequestPlugin(context.Context, string, string, []byte) ([]byte, error)
	SendPlugin(string, uint32, []byte) error
}

// MessageSender submits plugin-origin messages through the v2 message usecase.
type MessageSender interface {
	Send(context.Context, message.SendCommand) (message.SendResult, error)
}

// MessageReader reads authoritative channel message pages for plugin host RPCs.
type MessageReader interface {
	SyncMessages(context.Context, message.ChannelMessageQuery) (message.ChannelMessagePage, error)
}

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
	// Leader is the currently observed or preferred Slot leader. Zero means unknown.
	Leader uint64
	// Term is the observed or desired leader term when available.
	Term uint32
	// Replicas is the desired replica set for the Slot.
	Replicas []uint64
}

// ClusterReader exposes the authoritative cluster snapshot needed by legacy host RPCs.
type ClusterReader interface {
	ClusterSnapshot(context.Context) (ClusterSnapshot, error)
}

// ChannelOwnerReader resolves authoritative channel ownership for host RPC routing.
type ChannelOwnerReader interface {
	// ChannelOwnerNode returns the node that owns the channel.
	ChannelOwnerNode(context.Context, message.ChannelID) (uint64, error)
}

// ConversationReader reads authoritative UID conversation channel lists for host RPCs.
type ConversationReader interface {
	// ConversationChannels returns recent conversation channels for a UID in reader-defined order.
	ConversationChannels(context.Context, string, int) ([]message.ChannelID, error)
}

// PluginBinding records a cluster-authoritative UID to plugin association.
type PluginBinding struct {
	// PluginNo is the plugin selected for a UID.
	PluginNo string
	// UID is the user id whose offline Receive hook targets the plugin.
	UID string
}

// ReceiveBindingReader reads cluster-authoritative UID to plugin bindings.
type ReceiveBindingReader interface {
	// ListPluginBindingsByUID lists all plugin bindings for one UID.
	ListPluginBindingsByUID(context.Context, string) ([]PluginBinding, error)
}

// SystemUIDChecker identifies internal system senders that should not trigger Receive hooks.
type SystemUIDChecker interface {
	// IsSystemUID reports whether uid is an internal system sender.
	IsSystemUID(uid string) bool
}

// HTTPForwarder forwards plugin HTTP route requests to another cluster node.
type HTTPForwarder interface {
	// ForwardPluginHTTP calls the target node's node-local plugin route provider.
	ForwardPluginHTTP(context.Context, uint64, *pluginproto.ForwardHttpReq) (*pluginproto.HttpResponse, error)
}

// Observer records low-cardinality synchronous plugin hook events.
type Observer interface {
	ObserveSendInvoke(result string, d time.Duration)
	ObserveReceiveInvoke(result string, d time.Duration)
}

// Options configures the v2 plugin usecase.
type Options struct {
	Runtime Runtime
	Invoker Invoker
	// DesiredStore persists plugin config and enable state.
	DesiredStore DesiredStore
	// Messages submits plugin-origin /message/send host RPCs.
	Messages MessageSender
	// DefaultSenderUID is used when legacy plugin send requests omit fromUid.
	DefaultSenderUID string
	// MessageReader reads authoritative channel message pages for plugin host RPCs.
	MessageReader MessageReader
	// ClusterReader reads authoritative cluster state for plugin host RPCs.
	ClusterReader ClusterReader
	// ChannelOwners resolves authoritative channel owner nodes for plugin host RPCs.
	ChannelOwners ChannelOwnerReader
	// Conversations reads authoritative UID conversation channels for plugin host RPCs.
	Conversations ConversationReader
	// HTTPForwarder forwards plugin HTTP route requests to remote nodes.
	HTTPForwarder HTTPForwarder
	// HTTPForwardMaxBodyBytes limits plugin HTTP forward request and response bodies.
	HTTPForwardMaxBodyBytes int64
	// ReceiveBindings reads UID to plugin bindings for Receive hook selection.
	ReceiveBindings ReceiveBindingReader
	// SystemUIDs identifies internal system senders that should not trigger Receive hooks.
	SystemUIDs SystemUIDChecker
	// ReceiveDedupeTTL bounds duplicate Receive hook suppression for messageID+UID pairs.
	ReceiveDedupeTTL time.Duration
	// Clock supplies timestamps for deterministic Receive dedupe tests.
	Clock func() time.Time
	// FailOpen lets synchronous Send hook infrastructure failures preserve the original send.
	FailOpen bool
	Observer Observer
	Logger   wklog.Logger
	// NodeID identifies the local cluster node for node-scoped plugin responses.
	NodeID uint64
}

// App orchestrates v2 plugin lifecycle, selection, and hook invocation usecases.
type App struct {
	runtime                Runtime
	invoker                Invoker
	desired                DesiredStore
	messages               MessageSender
	messageReader          MessageReader
	clusterReader          ClusterReader
	channelOwners          ChannelOwnerReader
	conversations          ConversationReader
	httpForwarder          HTTPForwarder
	httpForwardLimit       int64
	receiveBindings        ReceiveBindingReader
	systemUIDs             SystemUIDChecker
	defaultSenderUID       string
	receiveDedupeTTL       time.Duration
	receiveDedupeNextSweep time.Time
	receiveDedupeMu        sync.Mutex
	receiveDedupe          map[string]time.Time
	desiredMu              sync.RWMutex
	desiredCache           map[string]desiredCacheEntry
	clock                  func() time.Time
	failOpen               bool
	observer               Observer
	logger                 wklog.Logger
	nodeID                 uint64
}

type desiredCacheEntry struct {
	state DesiredPlugin
	found bool
}
