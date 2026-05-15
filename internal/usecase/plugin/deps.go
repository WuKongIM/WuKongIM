package plugin

import (
	"context"
	"time"
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

// Invoker sends byte-oriented requests and messages to running plugins.
type Invoker interface {
	// RequestPlugin issues a request to one plugin path.
	RequestPlugin(ctx context.Context, no, path string, body []byte) ([]byte, error)
	// SendPlugin sends a one-way message to one plugin.
	SendPlugin(no string, msgType uint32, body []byte) error
	// Stop asks one plugin to stop through the host RPC stop path.
	Stop(ctx context.Context, no string) error
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
	// NodeID identifies the local cluster node for node-scoped responses.
	NodeID uint64
	// Clock supplies timestamps for deterministic tests.
	Clock func() time.Time
}
