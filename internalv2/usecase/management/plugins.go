package management

import (
	"context"
	"errors"
	"strings"
	"time"

	pluginusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/plugin"
)

var (
	// ErrPluginNodeUnavailable reports that the requested node plugin reader is unavailable.
	ErrPluginNodeUnavailable = errors.New("internalv2/usecase/management: plugin node unavailable")
	// ErrPluginNodeIDRequired reports that a node-scoped plugin read omitted the node id.
	ErrPluginNodeIDRequired = errors.New("internalv2/usecase/management: plugin node_id required")
)

// PluginReader exposes node-local plugin runtime snapshots.
type PluginReader interface {
	// ListPlugins returns all node-local observed plugin rows.
	ListPlugins(context.Context) ([]pluginusecase.ObservedPlugin, error)
	// GetPlugin returns one node-local observed plugin row.
	GetPlugin(context.Context, string) (pluginusecase.ObservedPlugin, error)
}

// RemotePluginReader reads plugin runtime snapshots from another node.
type RemotePluginReader interface {
	// NodePlugins returns all observed plugins from one selected cluster node.
	NodePlugins(context.Context, uint64) ([]Plugin, error)
	// NodePlugin returns one observed plugin from one selected cluster node.
	NodePlugin(context.Context, uint64, string) (Plugin, error)
}

// NodePluginList is the manager-facing node-scoped plugin inventory.
type NodePluginList struct {
	// NodeID identifies the node whose local plugin registry was read.
	NodeID uint64
	// Plugins contains ordered node-local plugin rows.
	Plugins []Plugin
}

// Plugin is the manager-facing node-local plugin runtime DTO.
type Plugin struct {
	// NodeID identifies the node whose local plugin registry was read.
	NodeID uint64
	// No is the stable plugin number.
	No string
	// Name is the human-readable plugin name.
	Name string
	// Version is the plugin version.
	Version string
	// Methods lists hook methods advertised by the plugin.
	Methods []pluginusecase.Method
	// Priority controls hook ordering; larger values run first.
	Priority int
	// PersistAfterSync reports whether PersistAfter waits for plugin completion.
	PersistAfterSync bool
	// ReplySync reports whether reply hooks wait for plugin completion.
	ReplySync bool
	// Status is the current node-local runtime status.
	Status string
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

// ListNodePlugins returns one node's local plugin inventory.
func (a *App) ListNodePlugins(ctx context.Context, nodeID uint64) (NodePluginList, error) {
	if err := ctxErr(ctx); err != nil {
		return NodePluginList{}, err
	}
	if nodeID == 0 {
		return NodePluginList{}, ErrPluginNodeIDRequired
	}
	localNodeID := a.localNodeID()
	if !a.pluginRequestTargetsLocal(nodeID, localNodeID) {
		if a == nil || a.remotePlugins == nil {
			return NodePluginList{}, ErrPluginNodeUnavailable
		}
		plugins, err := a.remotePlugins.NodePlugins(ctx, nodeID)
		if err != nil {
			return NodePluginList{}, err
		}
		return NodePluginList{NodeID: nodeID, Plugins: cloneManagementPlugins(nodeID, plugins)}, nil
	}
	if a == nil || a.plugins == nil {
		return NodePluginList{}, ErrPluginNodeUnavailable
	}
	observed, err := a.plugins.ListPlugins(ctx)
	if err != nil {
		return NodePluginList{}, err
	}
	return NodePluginList{NodeID: nodeID, Plugins: managementPluginsFromObserved(nodeID, observed)}, nil
}

// GetNodePlugin returns one node-local plugin detail.
func (a *App) GetNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) (Plugin, error) {
	if err := ctxErr(ctx); err != nil {
		return Plugin{}, err
	}
	if nodeID == 0 {
		return Plugin{}, ErrPluginNodeIDRequired
	}
	no := strings.TrimSpace(pluginNo)
	if no == "" {
		return Plugin{}, pluginusecase.ErrPluginNoRequired
	}
	localNodeID := a.localNodeID()
	if !a.pluginRequestTargetsLocal(nodeID, localNodeID) {
		if a == nil || a.remotePlugins == nil {
			return Plugin{}, ErrPluginNodeUnavailable
		}
		plugin, err := a.remotePlugins.NodePlugin(ctx, nodeID, no)
		if err != nil {
			return Plugin{}, err
		}
		return cloneManagementPlugin(nodeID, plugin), nil
	}
	if a == nil || a.plugins == nil {
		return Plugin{}, ErrPluginNodeUnavailable
	}
	observed, err := a.plugins.GetPlugin(ctx, no)
	if err != nil {
		return Plugin{}, err
	}
	return managementPluginFromObserved(nodeID, observed), nil
}

func (a *App) pluginRequestTargetsLocal(nodeID, localNodeID uint64) bool {
	return localNodeID == 0 || nodeID == localNodeID
}

func managementPluginsFromObserved(nodeID uint64, observed []pluginusecase.ObservedPlugin) []Plugin {
	out := make([]Plugin, 0, len(observed))
	for _, plugin := range observed {
		out = append(out, managementPluginFromObserved(nodeID, plugin))
	}
	return out
}

func managementPluginFromObserved(nodeID uint64, plugin pluginusecase.ObservedPlugin) Plugin {
	return Plugin{
		NodeID:           nodeID,
		No:               plugin.No,
		Name:             plugin.Name,
		Version:          plugin.Version,
		Methods:          append([]pluginusecase.Method(nil), plugin.Methods...),
		Priority:         plugin.Priority,
		PersistAfterSync: plugin.PersistAfterSync,
		ReplySync:        plugin.ReplySync,
		Status:           string(plugin.Status),
		Enabled:          plugin.Enabled,
		PID:              plugin.PID,
		LastSeenAt:       plugin.LastSeenAt,
		LastError:        plugin.LastError,
	}
}

func cloneManagementPlugins(nodeID uint64, plugins []Plugin) []Plugin {
	out := make([]Plugin, 0, len(plugins))
	for _, plugin := range plugins {
		out = append(out, cloneManagementPlugin(nodeID, plugin))
	}
	return out
}

func cloneManagementPlugin(nodeID uint64, plugin Plugin) Plugin {
	if plugin.NodeID == 0 {
		plugin.NodeID = nodeID
	}
	plugin.Methods = append([]pluginusecase.Method(nil), plugin.Methods...)
	return plugin
}
