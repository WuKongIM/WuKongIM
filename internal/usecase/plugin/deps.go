package plugin

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// Runtime exposes node-local plugin observations and process lifecycle controls.
type Runtime interface {
	// RegisterObserved inserts or replaces one observed plugin snapshot.
	RegisterObserved(ctx context.Context, info ObservedPlugin) error
	// Get returns one observed plugin snapshot by plugin number.
	Get(no string) (ObservedPlugin, bool)
	// List returns all observed plugin snapshots.
	List() []ObservedPlugin
	// Restart restarts one node-local plugin process.
	Restart(ctx context.Context, no string) error
	// Uninstall stops, disables, and removes one node-local plugin process.
	Uninstall(ctx context.Context, no string) error
	// SandboxDir returns the writable sandbox directory for one plugin.
	SandboxDir(no string) (string, error)
}

// DesiredStore persists node-local plugin config and enable state.
type DesiredStore interface {
	// Get returns the desired state for one plugin.
	Get(ctx context.Context, no string) (DesiredPlugin, error)
	// Save writes the desired state for one plugin.
	Save(ctx context.Context, state DesiredPlugin) error
	// Delete removes the desired state for one plugin.
	Delete(ctx context.Context, no string) error
}

// BindingStore persists cluster-authoritative UID to plugin bindings.
type BindingStore interface {
	// BindPluginUser creates or updates a UID to plugin binding.
	BindPluginUser(ctx context.Context, uid, pluginNo string) error
	// UnbindPluginUser removes a UID to plugin binding.
	UnbindPluginUser(ctx context.Context, uid, pluginNo string) error
	// ListPluginBindingsByUID lists all plugin bindings for one UID.
	ListPluginBindingsByUID(ctx context.Context, uid string) ([]PluginBinding, error)
	// ListPluginBindingsByPluginNo lists plugin-centric bindings using an opaque cursor.
	ListPluginBindingsByPluginNo(ctx context.Context, pluginNo, cursor string, limit int) (BindingPage, error)
	// ExistPluginBindingByUID reports whether a UID has any plugin binding.
	ExistPluginBindingByUID(ctx context.Context, uid string) (bool, error)
}

// SlotBindingStore is the slot proxy binding API adapted into BindingStore.
type SlotBindingStore interface {
	// BindPluginUser creates or updates a UID to plugin binding in the authoritative slot.
	BindPluginUser(ctx context.Context, uid, pluginNo string) error
	// UnbindPluginUser removes a UID to plugin binding in the authoritative slot.
	UnbindPluginUser(ctx context.Context, uid, pluginNo string) error
	// ListPluginBindingsByUID lists all slot-store plugin bindings for one UID.
	ListPluginBindingsByUID(ctx context.Context, uid string) ([]metadb.PluginUserBinding, error)
	// ListPluginBindingsByPluginNo lists slot-store plugin bindings using an opaque cursor.
	ListPluginBindingsByPluginNo(ctx context.Context, pluginNo, cursor string, limit int) ([]metadb.PluginUserBinding, string, bool, error)
	// ExistPluginBindingByUID reports whether a UID has any slot-store plugin binding.
	ExistPluginBindingByUID(ctx context.Context, uid string) (bool, error)
}

// Invoker sends byte-oriented requests and messages to running plugins.
type Invoker interface {
	// RequestPlugin issues a request to one plugin path.
	RequestPlugin(ctx context.Context, no, path string, body []byte) ([]byte, error)
	// SendPlugin sends a one-way message to one plugin.
	SendPlugin(no string, msgType uint32, body []byte) error
	// Stop asks one plugin to stop through the host RPC stop path.
	Stop(ctx context.Context, no string) error
}

// MessageSender is the message send usecase surface used by plugin-origin host RPCs.
type MessageSender interface {
	// Send routes a plugin-origin message through the normal message append pipeline.
	Send(ctx context.Context, cmd message.SendCommand) (message.SendResult, error)
}

// MessageReader is the authoritative channel message sync surface used by host RPCs.
type MessageReader interface {
	// SyncMessages returns one authoritative legacy-compatible channel message page.
	SyncMessages(ctx context.Context, query message.ChannelMessageQuery) (message.ChannelMessagePage, error)
}

// ClusterReader exposes the authoritative cluster snapshot needed by legacy host RPCs.
type ClusterReader interface {
	// ClusterSnapshot returns nodes and Slots without guessing from node-local state.
	ClusterSnapshot(ctx context.Context) (ClusterSnapshot, error)
}

// ChannelOwnerReader resolves authoritative channel ownership for host RPC routing.
type ChannelOwnerReader interface {
	// ChannelOwnerNode returns the node that owns the channel.
	ChannelOwnerNode(ctx context.Context, id channel.ChannelID) (uint64, error)
}

// ConversationReader exposes authoritative recent conversation channels.
type ConversationReader interface {
	// ConversationChannels returns recent conversation channels for a UID in reader-defined order.
	ConversationChannels(ctx context.Context, uid string, limit int) ([]channel.ChannelID, error)
}

// HTTPForwarder forwards plugin HTTP route requests to another cluster node.
type HTTPForwarder interface {
	// ForwardPluginHTTP calls the target node's node-local plugin route provider.
	ForwardPluginHTTP(ctx context.Context, nodeID uint64, req *pluginproto.ForwardHttpReq) (*pluginproto.HttpResponse, error)
}

// SystemUIDChecker identifies internal system senders that should not trigger Receive hooks.
type SystemUIDChecker interface {
	IsSystemUID(uid string) bool
}

// Options configures the plugin business usecase.
type Options struct {
	// Runtime provides node-local plugin registry and lifecycle operations.
	Runtime Runtime
	// DesiredStore persists node-local desired config and enable state.
	DesiredStore DesiredStore
	// BindingStore persists cluster-authoritative UID to plugin bindings.
	BindingStore BindingStore
	// BindingCache optionally caches short-lived UID binding lookups.
	BindingCache *BindingCache
	// Invoker sends byte-oriented hook requests to running plugins.
	Invoker Invoker
	// Messages routes plugin-origin sends through the message usecase.
	Messages MessageSender
	// MessageReader reads authoritative channel message pages for plugin host RPCs.
	MessageReader MessageReader
	// ClusterReader reads authoritative cluster state for plugin host RPCs.
	ClusterReader ClusterReader
	// ChannelOwners resolves authoritative channel owner nodes for plugin host RPCs.
	ChannelOwners ChannelOwnerReader
	// Conversations reads authoritative recent conversation channels for plugin host RPCs.
	Conversations ConversationReader
	// HTTPForwarder forwards plugin HTTP requests to another node through node RPC.
	HTTPForwarder HTTPForwarder
	// HTTPForwardMaxBodyBytes limits plugin HTTP forward request and response bodies.
	HTTPForwardMaxBodyBytes int64
	// FailOpen lets message sends continue when Send hooks fail.
	FailOpen bool
	// SystemUIDs identifies internal system senders that should not trigger Receive hooks.
	SystemUIDs SystemUIDChecker
	// DefaultSenderUID is used when legacy plugin send requests omit fromUid.
	DefaultSenderUID string
	// ReceiveDedupeTTL bounds duplicate Receive hook suppression for messageID+UID pairs.
	ReceiveDedupeTTL time.Duration
	// NodeID identifies the local cluster node for node-scoped responses.
	NodeID uint64
	// Clock supplies timestamps for deterministic tests.
	Clock func() time.Time
	// Logger records non-fatal plugin hook failures and diagnostics.
	Logger wklog.Logger
}
