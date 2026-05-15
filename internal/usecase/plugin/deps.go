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
	// Invoker sends byte-oriented hook requests to running plugins.
	Invoker Invoker
	// NodeID identifies the local cluster node for node-scoped responses.
	NodeID uint64
	// Clock supplies timestamps for deterministic tests.
	Clock func() time.Time
}
