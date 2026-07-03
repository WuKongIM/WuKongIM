package manager

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/management"
	pluginusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/plugin"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/gin-gonic/gin"
)

const maxManagerPluginConfigBytes = 1 << 20

// NodePluginsResponse is the node-scoped plugin inventory response body.
type NodePluginsResponse struct {
	// NodeID identifies the node whose local plugin inventory was read.
	NodeID uint64 `json:"node_id"`
	// Total is the number of returned plugins.
	Total int `json:"total"`
	// Items contains node-local plugin summaries.
	Items []PluginDTO `json:"items"`
}

// PluginDTO is the manager-facing plugin detail response body.
type PluginDTO struct {
	// NodeID identifies the node whose local plugin inventory was read.
	NodeID uint64 `json:"node_id"`
	// PluginNo is the stable plugin number.
	PluginNo string `json:"plugin_no"`
	// Name is the human-readable plugin name.
	Name string `json:"name"`
	// Version is the plugin version.
	Version string `json:"version"`
	// ConfigTemplate is the plugin-declared config schema.
	ConfigTemplate *pluginproto.ConfigTemplate `json:"config_template,omitempty"`
	// Config is the desired config with secret values already redacted by the usecase.
	Config map[string]any `json:"config,omitempty"`
	// CreatedAt records when desired state was first persisted.
	CreatedAt *time.Time `json:"created_at,omitempty"`
	// UpdatedAt records when desired state last changed.
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	// Status is the current node-local runtime status.
	Status string `json:"status"`
	// Enabled reports whether this node should run the plugin.
	Enabled bool `json:"enabled"`
	// Methods lists hook methods advertised by the plugin.
	Methods []string `json:"methods"`
	// Priority controls hook ordering; larger values run first.
	Priority int `json:"priority"`
	// PersistAfterSync reports whether PersistAfter waits for plugin completion.
	PersistAfterSync bool `json:"persist_after_sync"`
	// ReplySync reports whether Receive waits for plugin completion.
	ReplySync bool `json:"reply_sync"`
	// IsAI is set when the plugin supports the legacy Receive AI hook.
	IsAI uint8 `json:"is_ai"`
	// PID is the local operating-system process id when available.
	PID int `json:"pid"`
	// LastSeenAt records the latest runtime observation time.
	LastSeenAt time.Time `json:"last_seen_at"`
	// LastError stores the latest runtime error message.
	LastError string `json:"last_error"`
}

// PluginMutationResponse is the manager response for plugin lifecycle writes.
type PluginMutationResponse struct {
	// NodeID identifies the node whose local plugin was mutated.
	NodeID uint64 `json:"node_id"`
	// PluginNo is the stable plugin number.
	PluginNo string `json:"plugin_no"`
	// Changed reports whether the mutation was accepted.
	Changed bool `json:"changed"`
	// Plugin contains the latest plugin detail when available.
	Plugin *PluginDTO `json:"plugin,omitempty"`
}

// PluginBindingsResponse is the manager plugin binding list response body.
type PluginBindingsResponse struct {
	// Items contains durable binding rows with non-fatal row warnings.
	Items []PluginBindingDTO `json:"items"`
	// Total is the number of returned rows.
	Total int `json:"total"`
	// NextCursor is the opaque cursor for the next page.
	NextCursor string `json:"next_cursor,omitempty"`
	// HasMore reports whether another page exists.
	HasMore bool `json:"has_more"`
	// Warnings contains page-level non-fatal metadata.
	Warnings []PluginBindingWarningDTO `json:"warnings,omitempty"`
}

// PluginBindingDTO is one manager-facing plugin binding row.
type PluginBindingDTO struct {
	// UID is the user id whose offline Receive hook targets the plugin.
	UID string `json:"uid"`
	// PluginNo is the plugin selected for the UID.
	PluginNo string `json:"plugin_no"`
	// Plugin is local plugin metadata when enrichment is available.
	Plugin *PluginDTO `json:"plugin,omitempty"`
	// Warnings contains row-scoped non-fatal metadata.
	Warnings []PluginBindingWarningDTO `json:"warnings"`
}

// PluginBindingWarningDTO describes non-fatal binding metadata.
type PluginBindingWarningDTO struct {
	// Code is a stable machine-readable warning code.
	Code string `json:"code"`
	// Message is a human-readable warning summary.
	Message string `json:"message"`
	// UID identifies the binding user when relevant.
	UID string `json:"uid,omitempty"`
	// PluginNo identifies the binding plugin when relevant.
	PluginNo string `json:"plugin_no,omitempty"`
}

// PluginBindingMutationResponseDTO is the manager plugin binding mutation response body.
type PluginBindingMutationResponseDTO struct {
	// Binding is the mutated UID to plugin association.
	Binding PluginBindingDTO `json:"binding"`
	// Changed reports whether the mutation was accepted.
	Changed bool `json:"changed"`
	// Warnings contains non-fatal metadata.
	Warnings []PluginBindingWarningDTO `json:"warnings,omitempty"`
}

type pluginBindingMutationBody struct {
	UID      string `json:"uid"`
	PluginNo string `json:"plugin_no"`
}

func (s *Server) handleNodePlugins(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, err := parseNodeIDParam(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}
	list, err := s.management.ListNodePlugins(c.Request.Context(), nodeID)
	if err != nil {
		writePluginError(c, err)
		return
	}
	if list.NodeID == 0 {
		list.NodeID = nodeID
	}
	c.JSON(http.StatusOK, nodePluginsResponseDTO(list))
}

func (s *Server) handleNodePlugin(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, pluginNo, ok := parseNodePluginParams(c)
	if !ok {
		return
	}
	detail, err := s.management.GetNodePlugin(c.Request.Context(), nodeID, pluginNo)
	if err != nil {
		writePluginError(c, err)
		return
	}
	if detail.NodeID == 0 {
		detail.NodeID = nodeID
	}
	c.JSON(http.StatusOK, pluginDTO(detail))
}

func (s *Server) handleNodePluginConfigUpdate(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, pluginNo, ok := parseNodePluginParams(c)
	if !ok {
		return
	}
	config, err := readPluginConfigBody(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid plugin config")
		return
	}
	detail, err := s.management.UpdateNodePluginConfig(c.Request.Context(), nodeID, pluginNo, config)
	if err != nil {
		writePluginError(c, err)
		return
	}
	if detail.NodeID == 0 {
		detail.NodeID = nodeID
	}
	dto := pluginDTO(detail)
	c.JSON(http.StatusOK, PluginMutationResponse{NodeID: nodeID, PluginNo: pluginNo, Changed: true, Plugin: &dto})
}

func (s *Server) handleNodePluginRestart(c *gin.Context) {
	s.handleNodePluginDetailMutation(c, func(nodeID uint64, pluginNo string) (pluginusecase.LocalPluginDetail, error) {
		return s.management.RestartNodePlugin(c.Request.Context(), nodeID, pluginNo)
	})
}

func (s *Server) handleNodePluginUninstall(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, pluginNo, ok := parseNodePluginParams(c)
	if !ok {
		return
	}
	if err := s.management.UninstallNodePlugin(c.Request.Context(), nodeID, pluginNo); err != nil {
		writePluginError(c, err)
		return
	}
	c.JSON(http.StatusOK, PluginMutationResponse{NodeID: nodeID, PluginNo: pluginNo, Changed: true})
}

func (s *Server) handleNodePluginDetailMutation(c *gin.Context, mutate func(uint64, string) (pluginusecase.LocalPluginDetail, error)) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, pluginNo, ok := parseNodePluginParams(c)
	if !ok {
		return
	}
	detail, err := mutate(nodeID, pluginNo)
	if err != nil {
		writePluginError(c, err)
		return
	}
	if detail.NodeID == 0 {
		detail.NodeID = nodeID
	}
	dto := pluginDTO(detail)
	c.JSON(http.StatusOK, PluginMutationResponse{NodeID: nodeID, PluginNo: pluginNo, Changed: true, Plugin: &dto})
}

func (s *Server) handlePluginBindings(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	limit, err := parseOptionalPositiveInt(c.Query("limit"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid limit")
		return
	}
	resp, err := s.management.ListPluginBindings(c.Request.Context(), managementusecase.PluginBindingListRequest{
		UID:      c.Query("uid"),
		PluginNo: c.Query("plugin_no"),
		Cursor:   c.Query("cursor"),
		Limit:    limit,
	})
	if err != nil {
		writePluginError(c, err)
		return
	}
	c.JSON(http.StatusOK, pluginBindingsResponseDTO(resp))
}

func (s *Server) handlePluginBindingCreate(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	req, err := parsePluginBindingMutation(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid plugin binding request")
		return
	}
	resp, err := s.management.BindPluginUser(c.Request.Context(), req)
	if err != nil {
		writePluginError(c, err)
		return
	}
	c.JSON(http.StatusOK, pluginBindingMutationResponseDTO(resp))
}

func (s *Server) handlePluginBindingDelete(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	req, err := parsePluginBindingMutation(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid plugin binding request")
		return
	}
	if err := s.management.UnbindPluginUser(c.Request.Context(), req); err != nil {
		writePluginError(c, err)
		return
	}
	c.JSON(http.StatusOK, PluginBindingMutationResponseDTO{Binding: PluginBindingDTO{UID: req.UID, PluginNo: req.PluginNo, Warnings: []PluginBindingWarningDTO{}}, Changed: true})
}

func parseNodePluginParams(c *gin.Context) (uint64, string, bool) {
	nodeID, err := parseNodeIDParam(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return 0, "", false
	}
	pluginNo := strings.TrimSpace(c.Param("plugin_no"))
	if pluginNo == "" {
		jsonError(c, http.StatusBadRequest, "bad_request", "plugin_no required")
		return 0, "", false
	}
	return nodeID, pluginNo, true
}

func readPluginConfigBody(c *gin.Context) (json.RawMessage, error) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxManagerPluginConfigBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) == 0 || len(body) > maxManagerPluginConfigBytes || !json.Valid(body) {
		return nil, errors.New("invalid plugin config")
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, errors.New("plugin config must be object")
	}
	return append(json.RawMessage(nil), body...), nil
}

func parsePluginBindingMutation(c *gin.Context) (managementusecase.PluginBindingMutationRequest, error) {
	body := pluginBindingMutationBody{UID: c.Query("uid"), PluginNo: c.Query("plugin_no")}
	if c.Request != nil && c.Request.Body != nil {
		var fromBody pluginBindingMutationBody
		if err := bindOptionalJSON(c, &fromBody); err != nil {
			return managementusecase.PluginBindingMutationRequest{}, err
		}
		if fromBody.UID != "" || fromBody.PluginNo != "" {
			body = fromBody
		}
	}
	return managementusecase.PluginBindingMutationRequest{UID: strings.TrimSpace(body.UID), PluginNo: strings.TrimSpace(body.PluginNo)}, nil
}

func parseOptionalPositiveInt(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func nodePluginsResponseDTO(list pluginusecase.LocalPluginList) NodePluginsResponse {
	items := make([]PluginDTO, 0, len(list.Plugins))
	for _, item := range list.Plugins {
		if item.NodeID == 0 {
			item.NodeID = list.NodeID
		}
		items = append(items, pluginDTO(item))
	}
	return NodePluginsResponse{NodeID: list.NodeID, Total: len(items), Items: items}
}

func pluginDTO(item pluginusecase.LocalPlugin) PluginDTO {
	return PluginDTO{
		NodeID:           item.NodeID,
		PluginNo:         item.No,
		Name:             item.Name,
		Version:          item.Version,
		ConfigTemplate:   item.ConfigTemplate,
		Config:           item.Config,
		CreatedAt:        item.CreatedAt,
		UpdatedAt:        item.UpdatedAt,
		Status:           string(item.Status),
		Enabled:          item.Enabled,
		Methods:          pluginMethodStrings(item.Methods),
		Priority:         item.Priority,
		PersistAfterSync: item.PersistAfterSync,
		ReplySync:        item.ReplySync,
		IsAI:             item.IsAI,
		PID:              item.PID,
		LastSeenAt:       item.LastSeenAt,
		LastError:        item.LastError,
	}
}

func pluginMethodStrings(methods []pluginusecase.Method) []string {
	out := make([]string, 0, len(methods))
	for _, method := range methods {
		out = append(out, string(method))
	}
	return out
}

func pluginBindingsResponseDTO(resp managementusecase.PluginBindingListResponse) PluginBindingsResponse {
	details := resp.Details
	if len(details) == 0 {
		details = make([]pluginusecase.BindingDetail, 0, len(resp.Bindings))
		for _, binding := range resp.Bindings {
			details = append(details, pluginusecase.BindingDetail{Binding: binding})
		}
	}
	items := make([]PluginBindingDTO, 0, len(details))
	for _, detail := range details {
		items = append(items, pluginBindingDTO(detail))
	}
	return PluginBindingsResponse{Items: items, Total: len(items), NextCursor: resp.Cursor, HasMore: resp.HasMore, Warnings: pluginBindingWarningDTOs(resp.Warnings)}
}

func pluginBindingDTO(detail pluginusecase.BindingDetail) PluginBindingDTO {
	var plugin *PluginDTO
	if detail.Plugin != nil {
		dto := pluginDTO(*detail.Plugin)
		plugin = &dto
	}
	return PluginBindingDTO{UID: detail.Binding.UID, PluginNo: detail.Binding.PluginNo, Plugin: plugin, Warnings: pluginBindingWarningDTOs(detail.Warnings)}
}

func pluginBindingMutationResponseDTO(resp managementusecase.PluginBindingMutationResponse) PluginBindingMutationResponseDTO {
	return PluginBindingMutationResponseDTO{
		Binding:  PluginBindingDTO{UID: resp.Binding.UID, PluginNo: resp.Binding.PluginNo, Warnings: []PluginBindingWarningDTO{}},
		Changed:  resp.Changed,
		Warnings: pluginBindingWarningDTOs(resp.Warnings),
	}
}

func pluginBindingWarningDTOs(warnings []pluginusecase.BindingWarning) []PluginBindingWarningDTO {
	out := make([]PluginBindingWarningDTO, 0, len(warnings))
	for _, warning := range warnings {
		out = append(out, PluginBindingWarningDTO{Code: warning.Code, Message: warning.Message, UID: warning.UID, PluginNo: warning.PluginNo})
	}
	return out
}

func writePluginError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, managementusecase.ErrPluginNodeUnsupported):
		jsonError(c, http.StatusNotImplemented, "not_implemented", "plugin management unsupported")
	case errors.Is(err, managementusecase.ErrPluginNodeUnavailable), errors.Is(err, managementusecase.ErrPluginBindingsUnavailable):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", err.Error())
	case errors.Is(err, managementusecase.ErrPluginNodeIDRequired),
		errors.Is(err, managementusecase.ErrPluginBindingSelectorRequired),
		errors.Is(err, managementusecase.ErrPluginBindingSelectorAmbiguous),
		errors.Is(err, pluginusecase.ErrPluginNoRequired),
		errors.Is(err, pluginusecase.ErrBindingUIDRequired),
		errors.Is(err, pluginusecase.ErrInvalidPluginNo):
		jsonError(c, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, pluginusecase.ErrPluginNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "plugin not found")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
