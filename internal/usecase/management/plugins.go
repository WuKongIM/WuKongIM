package management

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	pluginusecase "github.com/WuKongIM/WuKongIM/internal/usecase/plugin"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"google.golang.org/protobuf/proto"
)

const (
	defaultPluginBindingListLimit = 50
	maxPluginBindingListLimit     = 200

	// BindingWarningPluginMissing reports that a durable binding references no observed local plugin.
	BindingWarningPluginMissing = "plugin_missing"
	// BindingWarningPluginUnavailable reports that a bound plugin is not currently runnable.
	BindingWarningPluginUnavailable = "plugin_unavailable"
	// BindingWarningPluginDisabled reports that desired state disables a bound plugin.
	BindingWarningPluginDisabled = "plugin_disabled"
	// BindingWarningReceiveUnsupported reports that a bound plugin does not advertise Receive.
	BindingWarningReceiveUnsupported = "receive_unsupported"
)

var (
	// ErrPluginNodeUnavailable reports that the requested node plugin reader is unavailable.
	ErrPluginNodeUnavailable = errors.New("internal/usecase/management: plugin node unavailable")
	// ErrPluginNodeIDRequired reports that a node-scoped plugin read omitted the node id.
	ErrPluginNodeIDRequired = errors.New("internal/usecase/management: plugin node_id required")
	// ErrPluginBindingsUnavailable reports that plugin binding storage is unavailable.
	ErrPluginBindingsUnavailable = errors.New("internal/usecase/management: plugin bindings unavailable")
	// ErrPluginBindingSelectorRequired reports that a binding list request has no selector.
	ErrPluginBindingSelectorRequired = errors.New("internal/usecase/management: plugin binding selector required")
	// ErrPluginBindingSelectorAmbiguous reports that a binding list request has multiple selectors.
	ErrPluginBindingSelectorAmbiguous = errors.New("internal/usecase/management: plugin binding selector ambiguous")
	// ErrPluginBindingUIDRequired reports that a binding mutation omitted the UID.
	ErrPluginBindingUIDRequired = errors.New("internal/usecase/management: plugin binding uid required")
)

// PluginReader exposes node-local plugin lifecycle state.
type PluginReader interface {
	// ListLocalPlugins returns all node-local plugin rows with desired metadata.
	ListLocalPlugins(context.Context) (pluginusecase.LocalPluginList, error)
	// GetLocalPlugin returns one node-local plugin row with desired metadata.
	GetLocalPlugin(context.Context, string) (pluginusecase.LocalPluginDetail, error)
	// UpdateLocalConfig persists desired config for one node-local plugin.
	UpdateLocalConfig(context.Context, string, json.RawMessage) (pluginusecase.LocalPluginDetail, error)
	// RestartLocalPlugin restarts one node-local plugin process.
	RestartLocalPlugin(context.Context, string) (pluginusecase.LocalPluginDetail, error)
	// UninstallLocalPlugin disables desired state and removes one node-local plugin process.
	UninstallLocalPlugin(context.Context, string) error
}

// RemotePluginReader reads plugin runtime snapshots from another node.
type RemotePluginReader interface {
	// NodePlugins returns all observed plugins from one selected cluster node.
	NodePlugins(context.Context, uint64) ([]Plugin, error)
	// NodePlugin returns one observed plugin from one selected cluster node.
	NodePlugin(context.Context, uint64, string) (Plugin, error)
	// UpdateNodePluginConfig persists desired config on one selected cluster node.
	UpdateNodePluginConfig(context.Context, uint64, string, json.RawMessage) (Plugin, error)
	// RestartNodePlugin restarts one node-local plugin process on one selected cluster node.
	RestartNodePlugin(context.Context, uint64, string) (Plugin, error)
	// UninstallNodePlugin disables desired state and removes one node-local plugin on one selected cluster node.
	UninstallNodePlugin(context.Context, uint64, string) error
}

// PluginBindingStore reads and mutates cluster-authoritative UID plugin bindings.
type PluginBindingStore interface {
	// ListPluginBindingsByUID lists all durable bindings for one UID.
	ListPluginBindingsByUID(context.Context, string) ([]PluginBinding, error)
	// BindPluginUser creates or updates one UID binding.
	BindPluginUser(context.Context, PluginBinding) error
	// UnbindPluginUser removes one UID binding.
	UnbindPluginUser(context.Context, string, string) error
}

// PluginBindingPluginScanner optionally provides plugin-centric binding pages.
type PluginBindingPluginScanner interface {
	// ListPluginBindingsByPluginNo lists bindings for one plugin using an opaque cursor.
	ListPluginBindingsByPluginNo(context.Context, string, string, int) ([]PluginBinding, string, bool, error)
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
	// ConfigTemplate is the plugin-declared config schema.
	ConfigTemplate *pluginproto.ConfigTemplate
	// Config is the desired config with secret values already redacted.
	Config map[string]any
	// CreatedAt records when desired state was first persisted.
	CreatedAt *time.Time
	// UpdatedAt records when desired state last changed.
	UpdatedAt *time.Time
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

// PluginBinding records a cluster-authoritative UID to plugin association.
type PluginBinding struct {
	// UID is the user id whose offline Receive hook targets the plugin.
	UID string
	// PluginNo is the plugin selected for the UID.
	PluginNo string
	// CreatedAt records when the durable binding was first created.
	CreatedAt time.Time
	// UpdatedAt records when the durable binding was last updated.
	UpdatedAt time.Time
}

// PluginBindingWarning describes non-fatal manager-visible binding metadata.
type PluginBindingWarning struct {
	// Code is a stable machine-readable warning code.
	Code string
	// Message is a human-readable warning summary.
	Message string
	// UID identifies the binding user when relevant.
	UID string
	// PluginNo identifies the binding plugin when relevant.
	PluginNo string
}

// PluginBindingDetail contains one binding row plus optional runtime enrichment.
type PluginBindingDetail struct {
	// Binding is the durable UID to plugin association.
	Binding PluginBinding
	// Plugin is node-local plugin metadata when observed locally.
	Plugin *Plugin
	// Warnings contains row-scoped non-fatal metadata.
	Warnings []PluginBindingWarning
}

// PluginBindingListRequest selects one manager-facing plugin binding page.
type PluginBindingListRequest struct {
	// UID selects bindings for one user. It is mutually exclusive with PluginNo.
	UID string
	// PluginNo selects bindings for one plugin. It is mutually exclusive with UID.
	PluginNo string
	// Cursor resumes plugin-centric pagination.
	Cursor string
	// Limit bounds plugin-centric page size.
	Limit int
}

// PluginBindingListResponse is the normalized manager binding list result.
type PluginBindingListResponse struct {
	// UID is the normalized UID selector when listing by UID.
	UID string
	// PluginNo is the normalized plugin selector when listing by plugin.
	PluginNo string
	// Bindings contains durable binding rows.
	Bindings []PluginBinding
	// Details contains enriched binding rows when available.
	Details []PluginBindingDetail
	// Cursor is the opaque cursor for the next plugin-centric page.
	Cursor string
	// HasMore reports whether another plugin-centric page exists.
	HasMore bool
	// Warnings contains non-fatal binding metadata.
	Warnings []PluginBindingWarning
}

// PluginBindingMutationRequest selects one UID to plugin binding mutation.
type PluginBindingMutationRequest struct {
	// UID is the user id whose binding is mutated.
	UID string
	// PluginNo is the plugin selected for the UID.
	PluginNo string
}

// PluginBindingMutationResponse reports one accepted plugin binding mutation.
type PluginBindingMutationResponse struct {
	// Binding is the mutated UID to plugin association.
	Binding PluginBinding
	// Changed reports whether the mutation was accepted by the authoritative store.
	Changed bool
	// Warnings contains non-fatal binding metadata.
	Warnings []PluginBindingWarning
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
		return NodePluginList{NodeID: nodeID}, nil
	}
	local, err := a.plugins.ListLocalPlugins(ctx)
	if err != nil {
		return NodePluginList{}, err
	}
	return NodePluginList{NodeID: nodeID, Plugins: managementPluginsFromLocal(nodeID, local.Plugins)}, nil
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
	local, err := a.plugins.GetLocalPlugin(ctx, no)
	if err != nil {
		return Plugin{}, err
	}
	return managementPluginFromLocal(nodeID, local), nil
}

// UpdateNodePluginConfig persists desired plugin config on one node.
func (a *App) UpdateNodePluginConfig(ctx context.Context, nodeID uint64, pluginNo string, config json.RawMessage) (Plugin, error) {
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
		plugin, err := a.remotePlugins.UpdateNodePluginConfig(ctx, nodeID, no, append(json.RawMessage(nil), config...))
		if err != nil {
			return Plugin{}, err
		}
		return cloneManagementPlugin(nodeID, plugin), nil
	}
	if a == nil || a.plugins == nil {
		return Plugin{}, ErrPluginNodeUnavailable
	}
	detail, err := a.plugins.UpdateLocalConfig(ctx, no, append(json.RawMessage(nil), config...))
	if err != nil {
		return Plugin{}, err
	}
	return managementPluginFromLocal(nodeID, detail), nil
}

// RestartNodePlugin restarts one node-local plugin process.
func (a *App) RestartNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) (Plugin, error) {
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
		plugin, err := a.remotePlugins.RestartNodePlugin(ctx, nodeID, no)
		if err != nil {
			return Plugin{}, err
		}
		return cloneManagementPlugin(nodeID, plugin), nil
	}
	if a == nil || a.plugins == nil {
		return Plugin{}, ErrPluginNodeUnavailable
	}
	detail, err := a.plugins.RestartLocalPlugin(ctx, no)
	if err != nil {
		return Plugin{}, err
	}
	return managementPluginFromLocal(nodeID, detail), nil
}

// UninstallNodePlugin disables and removes one node-local plugin process.
func (a *App) UninstallNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if nodeID == 0 {
		return ErrPluginNodeIDRequired
	}
	no := strings.TrimSpace(pluginNo)
	if no == "" {
		return pluginusecase.ErrPluginNoRequired
	}
	localNodeID := a.localNodeID()
	if !a.pluginRequestTargetsLocal(nodeID, localNodeID) {
		if a == nil || a.remotePlugins == nil {
			return ErrPluginNodeUnavailable
		}
		return a.remotePlugins.UninstallNodePlugin(ctx, nodeID, no)
	}
	if a == nil || a.plugins == nil {
		return ErrPluginNodeUnavailable
	}
	return a.plugins.UninstallLocalPlugin(ctx, no)
}

// ListPluginBindings lists cluster-authoritative plugin bindings by UID or plugin number.
func (a *App) ListPluginBindings(ctx context.Context, req PluginBindingListRequest) (PluginBindingListResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return PluginBindingListResponse{}, err
	}
	uid := strings.TrimSpace(req.UID)
	pluginNo := strings.TrimSpace(req.PluginNo)
	switch {
	case uid == "" && pluginNo == "":
		return PluginBindingListResponse{}, ErrPluginBindingSelectorRequired
	case uid != "" && pluginNo != "":
		return PluginBindingListResponse{}, ErrPluginBindingSelectorAmbiguous
	}
	if a == nil || a.pluginBindings == nil {
		return PluginBindingListResponse{}, ErrPluginBindingsUnavailable
	}
	if uid != "" {
		bindings, err := a.pluginBindings.ListPluginBindingsByUID(ctx, uid)
		if err != nil {
			return PluginBindingListResponse{}, err
		}
		bindings = clonePluginBindings(bindings)
		details, warnings, err := a.pluginBindingDetails(ctx, bindings)
		if err != nil {
			return PluginBindingListResponse{}, err
		}
		return PluginBindingListResponse{UID: uid, Bindings: bindings, Details: details, Warnings: warnings}, nil
	}
	scanner, ok := a.pluginBindings.(PluginBindingPluginScanner)
	if !ok {
		return PluginBindingListResponse{}, ErrPluginBindingsUnavailable
	}
	limit := normalizePluginBindingLimit(req.Limit)
	bindings, cursor, hasMore, err := scanner.ListPluginBindingsByPluginNo(ctx, pluginNo, req.Cursor, limit)
	if err != nil {
		return PluginBindingListResponse{}, err
	}
	bindings = clonePluginBindings(bindings)
	details, warnings, err := a.pluginBindingDetails(ctx, bindings)
	if err != nil {
		return PluginBindingListResponse{}, err
	}
	return PluginBindingListResponse{PluginNo: pluginNo, Bindings: bindings, Details: details, Cursor: cursor, HasMore: hasMore, Warnings: warnings}, nil
}

// BindPluginUser creates or updates one cluster-authoritative UID binding.
func (a *App) BindPluginUser(ctx context.Context, req PluginBindingMutationRequest) (PluginBindingMutationResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return PluginBindingMutationResponse{}, err
	}
	uid, pluginNo, err := normalizePluginBindingMutation(req)
	if err != nil {
		return PluginBindingMutationResponse{}, err
	}
	if a == nil || a.pluginBindings == nil {
		return PluginBindingMutationResponse{}, ErrPluginBindingsUnavailable
	}
	now := a.now()
	binding := PluginBinding{UID: uid, PluginNo: pluginNo, CreatedAt: now, UpdatedAt: now}
	warnings, err := a.pluginBindingWarnings(ctx, binding)
	if err != nil {
		return PluginBindingMutationResponse{}, err
	}
	if err := a.pluginBindings.BindPluginUser(ctx, binding); err != nil {
		return PluginBindingMutationResponse{}, err
	}
	return PluginBindingMutationResponse{Binding: binding, Changed: true, Warnings: clonePluginBindingWarnings(warnings)}, nil
}

// UnbindPluginUser removes one cluster-authoritative UID binding.
func (a *App) UnbindPluginUser(ctx context.Context, req PluginBindingMutationRequest) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	uid, pluginNo, err := normalizePluginBindingMutation(req)
	if err != nil {
		return err
	}
	if a == nil || a.pluginBindings == nil {
		return ErrPluginBindingsUnavailable
	}
	return a.pluginBindings.UnbindPluginUser(ctx, uid, pluginNo)
}

func (a *App) pluginRequestTargetsLocal(nodeID, localNodeID uint64) bool {
	return localNodeID == 0 || nodeID == localNodeID
}

func normalizePluginBindingMutation(req PluginBindingMutationRequest) (string, string, error) {
	uid := strings.TrimSpace(req.UID)
	pluginNo := strings.TrimSpace(req.PluginNo)
	if uid == "" {
		return "", "", ErrPluginBindingUIDRequired
	}
	if pluginNo == "" {
		return "", "", pluginusecase.ErrPluginNoRequired
	}
	return uid, pluginNo, nil
}

func normalizePluginBindingLimit(limit int) int {
	if limit <= 0 {
		return defaultPluginBindingListLimit
	}
	if limit > maxPluginBindingListLimit {
		return maxPluginBindingListLimit
	}
	return limit
}

func (a *App) pluginBindingDetails(ctx context.Context, bindings []PluginBinding) ([]PluginBindingDetail, []PluginBindingWarning, error) {
	details := make([]PluginBindingDetail, 0, len(bindings))
	warnings := make([]PluginBindingWarning, 0)
	for _, binding := range bindings {
		rowWarnings, plugin, err := a.pluginBindingRuntimeMetadata(ctx, binding)
		if err != nil {
			return nil, nil, err
		}
		details = append(details, PluginBindingDetail{
			Binding:  binding,
			Plugin:   plugin,
			Warnings: clonePluginBindingWarnings(rowWarnings),
		})
		warnings = append(warnings, rowWarnings...)
	}
	return details, warnings, nil
}

func (a *App) pluginBindingWarnings(ctx context.Context, binding PluginBinding) ([]PluginBindingWarning, error) {
	warnings, _, err := a.pluginBindingRuntimeMetadata(ctx, binding)
	return warnings, err
}

func (a *App) pluginBindingRuntimeMetadata(ctx context.Context, binding PluginBinding) ([]PluginBindingWarning, *Plugin, error) {
	if a == nil || a.plugins == nil {
		return nil, nil, nil
	}
	local, err := a.plugins.GetLocalPlugin(ctx, binding.PluginNo)
	if errors.Is(err, pluginusecase.ErrPluginNotFound) {
		return []PluginBindingWarning{pluginBindingWarning(binding, BindingWarningPluginMissing, "plugin is not observed on this node")}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	plugin := managementPluginFromLocal(a.localNodeID(), local)
	switch {
	case !plugin.Enabled || plugin.Status == string(pluginusecase.StatusDisabled):
		return []PluginBindingWarning{pluginBindingWarning(binding, BindingWarningPluginDisabled, "plugin is disabled on this node")}, &plugin, nil
	case plugin.Status != string(pluginusecase.StatusRunning):
		return []PluginBindingWarning{pluginBindingWarning(binding, BindingWarningPluginUnavailable, "plugin is not running on this node")}, &plugin, nil
	case !pluginHasMethod(plugin.Methods, pluginusecase.MethodReceive):
		return []PluginBindingWarning{pluginBindingWarning(binding, BindingWarningReceiveUnsupported, "plugin does not support Receive")}, &plugin, nil
	default:
		return nil, &plugin, nil
	}
}

func pluginHasMethod(methods []pluginusecase.Method, want pluginusecase.Method) bool {
	for _, method := range methods {
		if method == want {
			return true
		}
	}
	return false
}

func pluginBindingWarning(binding PluginBinding, code, message string) PluginBindingWarning {
	return PluginBindingWarning{Code: code, Message: message, UID: binding.UID, PluginNo: binding.PluginNo}
}

func managementPluginsFromLocal(nodeID uint64, local []pluginusecase.LocalPlugin) []Plugin {
	out := make([]Plugin, 0, len(local))
	for _, plugin := range local {
		out = append(out, managementPluginFromLocal(nodeID, plugin))
	}
	return out
}

func managementPluginFromLocal(nodeID uint64, plugin pluginusecase.LocalPlugin) Plugin {
	return Plugin{
		NodeID:           nodeID,
		No:               plugin.No,
		Name:             plugin.Name,
		Version:          plugin.Version,
		ConfigTemplate:   cloneConfigTemplate(plugin.ConfigTemplate),
		Config:           cloneAnyMap(plugin.Config),
		CreatedAt:        cloneTimePtr(plugin.CreatedAt),
		UpdatedAt:        cloneTimePtr(plugin.UpdatedAt),
		Methods:          append([]pluginusecase.Method(nil), plugin.Methods...),
		Priority:         plugin.Priority,
		PersistAfterSync: plugin.PersistAfterSync,
		ReplySync:        plugin.ReplySync,
		Status:           string(plugin.Status),
		Enabled:          plugin.Enabled,
		IsAI:             plugin.IsAI,
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
	plugin.ConfigTemplate = cloneConfigTemplate(plugin.ConfigTemplate)
	plugin.Config = cloneAnyMap(plugin.Config)
	plugin.CreatedAt = cloneTimePtr(plugin.CreatedAt)
	plugin.UpdatedAt = cloneTimePtr(plugin.UpdatedAt)
	return plugin
}

func cloneConfigTemplate(template *pluginproto.ConfigTemplate) *pluginproto.ConfigTemplate {
	if template == nil {
		return nil
	}
	cloned, ok := proto.Clone(template).(*pluginproto.ConfigTemplate)
	if !ok {
		return nil
	}
	return cloned
}

func cloneAnyMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}

func clonePluginBindings(bindings []PluginBinding) []PluginBinding {
	return append([]PluginBinding(nil), bindings...)
}

func clonePluginBindingWarnings(warnings []PluginBindingWarning) []PluginBindingWarning {
	return append([]PluginBindingWarning(nil), warnings...)
}
