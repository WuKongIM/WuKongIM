package plugin

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

var (
	// ErrPluginNoRequired reports that a plugin operation omitted the plugin number.
	ErrPluginNoRequired = errors.New("plugin no required")
	// ErrPluginCallerMismatch reports a mismatch between the authenticated caller and plugin number.
	ErrPluginCallerMismatch = errors.New("plugin caller mismatch")
	// ErrPluginNotFound reports that a requested plugin observation does not exist.
	ErrPluginNotFound = errors.New("plugin not found")
	// ErrRuntimeRequired reports that the plugin usecase was built without a runtime port.
	ErrRuntimeRequired = errors.New("plugin runtime required")
	// ErrInvokerRequired reports that the plugin usecase was built without an invocation port.
	ErrInvokerRequired = errors.New("plugin invoker required")
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
)

// Method identifies a plugin hook advertised by a plugin manifest.
type Method string

const (
	// MethodSend is invoked before a message append.
	MethodSend Method = "Send"
	// MethodPersistAfter is invoked after a durable message is committed.
	MethodPersistAfter Method = "PersistAfter"
)

// Status describes the node-local runtime state observed for a plugin.
type Status string

const (
	// StatusRunning means the plugin has an active host RPC connection.
	StatusRunning Status = "running"
	// StatusOffline means the plugin is not connected on this node.
	StatusOffline Status = "offline"
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

// Runtime stores node-local observed plugin state for the v2 usecase.
type Runtime interface {
	RegisterObserved(context.Context, ObservedPlugin) error
	MarkClosed(context.Context, string) error
	List() []ObservedPlugin
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

// Observer records low-cardinality synchronous plugin hook events.
type Observer interface {
	ObserveSendInvoke(result string, d time.Duration)
}

// Options configures the v2 plugin usecase.
type Options struct {
	Runtime Runtime
	Invoker Invoker
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
	// FailOpen lets synchronous Send hook infrastructure failures preserve the original send.
	FailOpen bool
	Observer Observer
	Logger   wklog.Logger
}

// App orchestrates v2 plugin lifecycle, selection, and hook invocation usecases.
type App struct {
	runtime          Runtime
	invoker          Invoker
	messages         MessageSender
	messageReader    MessageReader
	clusterReader    ClusterReader
	channelOwners    ChannelOwnerReader
	conversations    ConversationReader
	defaultSenderUID string
	failOpen         bool
	observer         Observer
	logger           wklog.Logger
}
