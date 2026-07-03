package management

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	pluginusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/plugin"
)

const (
	defaultPluginBindingListLimit = 50
	maxPluginBindingListLimit     = 200
)

var (
	// ErrPluginNodeUnavailable reports that node-local plugin management cannot be reached.
	ErrPluginNodeUnavailable = errors.New("management: plugin node unavailable")
	// ErrPluginNodeUnsupported reports that the target node does not support plugin management RPCs.
	ErrPluginNodeUnsupported = errors.New("management: plugin node management unsupported")
	// ErrPluginNodeIDRequired reports that a node-scoped plugin request did not select a node.
	ErrPluginNodeIDRequired = errors.New("management: plugin node_id required")
	// ErrPluginBindingsUnavailable reports that cluster-authoritative plugin binding storage is not configured.
	ErrPluginBindingsUnavailable = errors.New("management: plugin bindings unavailable")
	// ErrPluginBindingSelectorRequired reports that a binding list request has no selector.
	ErrPluginBindingSelectorRequired = errors.New("management: plugin binding selector required")
	// ErrPluginBindingSelectorAmbiguous reports that a binding list request has multiple selectors.
	ErrPluginBindingSelectorAmbiguous = errors.New("management: plugin binding selector ambiguous")
)

type pluginNodeStatusError interface {
	PluginNodeStatus() string
}

// PluginNodeClient reads and mutates node-local plugin state without coupling management to node RPCs.
type PluginNodeClient interface {
	// ListNodePlugins returns the target node's local plugin inventory.
	ListNodePlugins(ctx context.Context, nodeID uint64) (pluginusecase.LocalPluginList, error)
	// GetNodePlugin returns one plugin detail from the target node.
	GetNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) (pluginusecase.LocalPluginDetail, error)
	// UpdateNodePluginConfig persists desired config on the target node.
	UpdateNodePluginConfig(ctx context.Context, nodeID uint64, pluginNo string, config json.RawMessage) (pluginusecase.LocalPluginDetail, error)
	// RestartNodePlugin restarts one node-local plugin process.
	RestartNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) (pluginusecase.LocalPluginDetail, error)
	// UninstallNodePlugin disables and removes one node-local plugin process.
	UninstallNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) error
}

// PluginBindingUsecase mutates cluster-authoritative UID to plugin bindings.
type PluginBindingUsecase interface {
	// ListBindingsByUID lists all plugin bindings for one UID.
	ListBindingsByUID(ctx context.Context, uid string) (pluginusecase.BindingList, error)
	// ListBindingsByPluginNo lists bindings for one plugin using an opaque cursor.
	ListBindingsByPluginNo(ctx context.Context, pluginNo, cursor string, limit int) (pluginusecase.BindingPage, error)
	// BindPluginUser creates or updates one UID to plugin binding.
	BindPluginUser(ctx context.Context, uid, pluginNo string) (pluginusecase.BindingMutationResult, error)
	// UnbindPluginUser removes one UID to plugin binding.
	UnbindPluginUser(ctx context.Context, uid, pluginNo string) error
}

// UpdatePluginConfigRequest records a node-scoped plugin config update.
type UpdatePluginConfigRequest struct {
	// NodeID identifies the node whose desired plugin config is mutated.
	NodeID uint64
	// PluginNo is the stable plugin number.
	PluginNo string
	// Config is the desired plugin config JSON object.
	Config json.RawMessage
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
	Bindings []pluginusecase.PluginBinding
	// Details contains enriched binding rows when the binding usecase can provide them.
	Details []pluginusecase.BindingDetail
	// Cursor is the opaque cursor for the next plugin-centric page.
	Cursor string
	// HasMore reports whether another plugin-centric page exists.
	HasMore bool
	// Warnings contains non-fatal binding metadata.
	Warnings []pluginusecase.BindingWarning
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
	Binding pluginusecase.PluginBinding
	// Changed reports whether the mutation was accepted by the authoritative store.
	Changed bool
	// Warnings contains non-fatal binding metadata.
	Warnings []pluginusecase.BindingWarning
}

// ListNodePlugins returns one node's local plugin inventory through the configured adapter.
func (a *App) ListNodePlugins(ctx context.Context, nodeID uint64) (pluginusecase.LocalPluginList, error) {
	if err := validatePluginNodeID(nodeID); err != nil {
		return pluginusecase.LocalPluginList{}, err
	}
	if a == nil || a.plugins == nil {
		return pluginusecase.LocalPluginList{}, ErrPluginNodeUnavailable
	}
	list, err := a.plugins.ListNodePlugins(ctx, nodeID)
	return list, normalizePluginNodeError(err)
}

// GetNodePlugin returns one node-local plugin detail.
func (a *App) GetNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) (pluginusecase.LocalPluginDetail, error) {
	pluginNo = strings.TrimSpace(pluginNo)
	if err := validatePluginNodeRequest(nodeID, pluginNo); err != nil {
		return pluginusecase.LocalPluginDetail{}, err
	}
	if a == nil || a.plugins == nil {
		return pluginusecase.LocalPluginDetail{}, ErrPluginNodeUnavailable
	}
	detail, err := a.plugins.GetNodePlugin(ctx, nodeID, pluginNo)
	return detail, normalizePluginNodeError(err)
}

// UpdateNodePluginConfig persists desired plugin config on one node.
func (a *App) UpdateNodePluginConfig(ctx context.Context, nodeID uint64, pluginNo string, config json.RawMessage) (pluginusecase.LocalPluginDetail, error) {
	pluginNo = strings.TrimSpace(pluginNo)
	if err := validatePluginNodeRequest(nodeID, pluginNo); err != nil {
		return pluginusecase.LocalPluginDetail{}, err
	}
	if a == nil || a.plugins == nil {
		return pluginusecase.LocalPluginDetail{}, ErrPluginNodeUnavailable
	}
	detail, err := a.plugins.UpdateNodePluginConfig(ctx, nodeID, pluginNo, append(json.RawMessage(nil), config...))
	return detail, normalizePluginNodeError(err)
}

// RestartNodePlugin restarts one node-local plugin process.
func (a *App) RestartNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) (pluginusecase.LocalPluginDetail, error) {
	pluginNo = strings.TrimSpace(pluginNo)
	if err := validatePluginNodeRequest(nodeID, pluginNo); err != nil {
		return pluginusecase.LocalPluginDetail{}, err
	}
	if a == nil || a.plugins == nil {
		return pluginusecase.LocalPluginDetail{}, ErrPluginNodeUnavailable
	}
	detail, err := a.plugins.RestartNodePlugin(ctx, nodeID, pluginNo)
	return detail, normalizePluginNodeError(err)
}

// UninstallNodePlugin disables and removes one node-local plugin process.
func (a *App) UninstallNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) error {
	pluginNo = strings.TrimSpace(pluginNo)
	if err := validatePluginNodeRequest(nodeID, pluginNo); err != nil {
		return err
	}
	if a == nil || a.plugins == nil {
		return ErrPluginNodeUnavailable
	}
	return normalizePluginNodeError(a.plugins.UninstallNodePlugin(ctx, nodeID, pluginNo))
}

// ListPluginBindings lists cluster-authoritative plugin bindings by UID or plugin number.
func (a *App) ListPluginBindings(ctx context.Context, req PluginBindingListRequest) (PluginBindingListResponse, error) {
	if a == nil || a.pluginBindings == nil {
		return PluginBindingListResponse{}, ErrPluginBindingsUnavailable
	}
	uid := strings.TrimSpace(req.UID)
	pluginNo := strings.TrimSpace(req.PluginNo)
	switch {
	case uid == "" && pluginNo == "":
		return PluginBindingListResponse{}, ErrPluginBindingSelectorRequired
	case uid != "" && pluginNo != "":
		return PluginBindingListResponse{}, ErrPluginBindingSelectorAmbiguous
	case uid != "":
		list, err := a.pluginBindings.ListBindingsByUID(ctx, uid)
		if err != nil {
			return PluginBindingListResponse{}, err
		}
		return PluginBindingListResponse{UID: uid, Bindings: pluginBindingsFromDetails(list.Bindings), Details: cloneBindingDetails(list.Bindings), Warnings: append([]pluginusecase.BindingWarning(nil), list.Warnings...)}, nil
	default:
		limit := normalizePluginBindingLimit(req.Limit)
		page, err := a.pluginBindings.ListBindingsByPluginNo(ctx, pluginNo, req.Cursor, limit)
		if err != nil {
			return PluginBindingListResponse{}, err
		}
		details := cloneBindingDetails(page.Details)
		return PluginBindingListResponse{PluginNo: pluginNo, Bindings: append([]pluginusecase.PluginBinding(nil), page.Bindings...), Details: details, Cursor: page.Cursor, HasMore: page.HasMore, Warnings: append([]pluginusecase.BindingWarning(nil), page.Warnings...)}, nil
	}
}

// BindPluginUser creates or updates one cluster-authoritative UID binding.
func (a *App) BindPluginUser(ctx context.Context, req PluginBindingMutationRequest) (PluginBindingMutationResponse, error) {
	uid, pluginNo, err := normalizePluginBindingMutation(req)
	if err != nil {
		return PluginBindingMutationResponse{}, err
	}
	if a == nil || a.pluginBindings == nil {
		return PluginBindingMutationResponse{}, ErrPluginBindingsUnavailable
	}
	result, err := a.pluginBindings.BindPluginUser(ctx, uid, pluginNo)
	if err != nil {
		return PluginBindingMutationResponse{}, err
	}
	return PluginBindingMutationResponse{Binding: result.Binding, Changed: true, Warnings: append([]pluginusecase.BindingWarning(nil), result.Warnings...)}, nil
}

// UnbindPluginUser removes one cluster-authoritative UID binding.
func (a *App) UnbindPluginUser(ctx context.Context, req PluginBindingMutationRequest) error {
	uid, pluginNo, err := normalizePluginBindingMutation(req)
	if err != nil {
		return err
	}
	if a == nil || a.pluginBindings == nil {
		return ErrPluginBindingsUnavailable
	}
	return a.pluginBindings.UnbindPluginUser(ctx, uid, pluginNo)
}

func validatePluginNodeID(nodeID uint64) error {
	if nodeID == 0 {
		return ErrPluginNodeIDRequired
	}
	return nil
}

func validatePluginNodeRequest(nodeID uint64, pluginNo string) error {
	if err := validatePluginNodeID(nodeID); err != nil {
		return err
	}
	if pluginNo == "" {
		return pluginusecase.ErrPluginNoRequired
	}
	return nil
}

func normalizePluginBindingMutation(req PluginBindingMutationRequest) (string, string, error) {
	uid := strings.TrimSpace(req.UID)
	pluginNo := strings.TrimSpace(req.PluginNo)
	if uid == "" {
		return "", "", pluginusecase.ErrBindingUIDRequired
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

func normalizePluginNodeError(err error) error {
	if err == nil {
		return nil
	}
	var statusErr pluginNodeStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.PluginNodeStatus() {
		case "unsupported":
			return fmt.Errorf("%w: %v", ErrPluginNodeUnsupported, err)
		case "unavailable":
			return fmt.Errorf("%w: %v", ErrPluginNodeUnavailable, err)
		}
	}
	return err
}

func pluginBindingsFromDetails(details []pluginusecase.BindingDetail) []pluginusecase.PluginBinding {
	bindings := make([]pluginusecase.PluginBinding, 0, len(details))
	for _, detail := range details {
		bindings = append(bindings, detail.Binding)
	}
	return bindings
}

func cloneBindingDetails(details []pluginusecase.BindingDetail) []pluginusecase.BindingDetail {
	out := make([]pluginusecase.BindingDetail, 0, len(details))
	for _, detail := range details {
		cloned := detail
		if detail.Plugin != nil {
			plugin := *detail.Plugin
			cloned.Plugin = &plugin
		}
		cloned.Warnings = append([]pluginusecase.BindingWarning(nil), detail.Warnings...)
		out = append(out, cloned)
	}
	return out
}
