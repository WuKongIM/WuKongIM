package manager

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	pluginusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/plugin"
	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/gin-gonic/gin"
)

const maxManagerPluginConfigBytes = 1 << 20

// NodePluginsResponse is the manager node-scoped plugin list response body.
type NodePluginsResponse struct {
	// NodeID identifies the node whose local plugin registry was read.
	NodeID uint64 `json:"node_id"`
	// Total is the number of returned plugin rows.
	Total int `json:"total"`
	// Items contains the plugin rows ordered by the usecase.
	Items []PluginDTO `json:"items"`
}

// PluginDTO is the manager HTTP view of one node-local plugin runtime row.
type PluginDTO struct {
	// NodeID identifies the node whose local plugin registry was read.
	NodeID uint64 `json:"node_id"`
	// PluginNo is the stable plugin number used by the runtime.
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
	// Methods lists hook method names advertised by the plugin.
	Methods []string `json:"methods"`
	// Priority controls hook ordering; larger values run first.
	Priority int `json:"priority"`
	// PersistAfterSync reports whether PersistAfter waits for plugin completion.
	PersistAfterSync bool `json:"persist_after_sync"`
	// ReplySync reports whether reply hooks wait for plugin completion.
	ReplySync bool `json:"reply_sync"`
	// Status is the current node-local runtime status.
	Status string `json:"status"`
	// Enabled reports whether this node should invoke the plugin.
	Enabled bool `json:"enabled"`
	// IsAI is reserved for legacy Receive AI hook display compatibility.
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
	nodeID, err := parseRequiredPluginNodeID(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}
	resp, err := s.management.ListNodePlugins(c.Request.Context(), nodeID)
	if err != nil {
		writePluginError(c, err)
		return
	}
	if resp.NodeID == 0 {
		resp.NodeID = nodeID
	}
	items := pluginDTOs(resp.Plugins)
	c.JSON(http.StatusOK, NodePluginsResponse{
		NodeID: resp.NodeID,
		Total:  len(items),
		Items:  items,
	})
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
	plugin, err := s.management.GetNodePlugin(c.Request.Context(), nodeID, pluginNo)
	if err != nil {
		writePluginError(c, err)
		return
	}
	if plugin.NodeID == 0 {
		plugin.NodeID = nodeID
	}
	c.JSON(http.StatusOK, pluginDTO(plugin))
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
	s.handleNodePluginDetailMutation(c, func(nodeID uint64, pluginNo string) (managementusecase.Plugin, error) {
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

func (s *Server) handleNodePluginDetailMutation(c *gin.Context, mutate func(uint64, string) (managementusecase.Plugin, error)) {
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
	req := managementusecase.PluginBindingListRequest{
		UID:      strings.TrimSpace(c.Query("uid")),
		PluginNo: strings.TrimSpace(c.Query("plugin_no")),
		Cursor:   c.Query("cursor"),
		Limit:    limit,
	}
	if req.UID == "" && req.PluginNo == "" {
		writePluginError(c, managementusecase.ErrPluginBindingSelectorRequired)
		return
	}
	if req.UID != "" && req.PluginNo != "" {
		writePluginError(c, managementusecase.ErrPluginBindingSelectorAmbiguous)
		return
	}
	resp, err := s.management.ListPluginBindings(c.Request.Context(), req)
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
	if req.UID == "" {
		writePluginError(c, managementusecase.ErrPluginBindingUIDRequired)
		return
	}
	if req.PluginNo == "" {
		writePluginError(c, pluginusecase.ErrPluginNoRequired)
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
	if req.UID == "" {
		writePluginError(c, managementusecase.ErrPluginBindingUIDRequired)
		return
	}
	if req.PluginNo == "" {
		writePluginError(c, pluginusecase.ErrPluginNoRequired)
		return
	}
	if err := s.management.UnbindPluginUser(c.Request.Context(), req); err != nil {
		writePluginError(c, err)
		return
	}
	c.JSON(http.StatusOK, PluginBindingMutationResponseDTO{
		Binding: PluginBindingDTO{UID: req.UID, PluginNo: req.PluginNo, Warnings: []PluginBindingWarningDTO{}},
		Changed: true,
	})
}

func parseRequiredPluginNodeID(raw string) (uint64, error) {
	if raw == "" {
		return 0, strconv.ErrSyntax
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

func parseNodePluginParams(c *gin.Context) (uint64, string, bool) {
	nodeID, err := parseRequiredPluginNodeID(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return 0, "", false
	}
	pluginNo := strings.TrimSpace(c.Param("plugin_no"))
	if pluginNo == "" {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid plugin_no")
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
	return managementusecase.PluginBindingMutationRequest{
		UID:      strings.TrimSpace(body.UID),
		PluginNo: strings.TrimSpace(body.PluginNo),
	}, nil
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

func writePluginError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, managementusecase.ErrPluginBindingsUnavailable):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "plugin bindings unavailable")
	case errors.Is(err, managementusecase.ErrPluginNodeIDRequired):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
	case errors.Is(err, managementusecase.ErrPluginBindingSelectorRequired):
		jsonError(c, http.StatusBadRequest, "bad_request", "plugin binding selector required")
	case errors.Is(err, managementusecase.ErrPluginBindingSelectorAmbiguous):
		jsonError(c, http.StatusBadRequest, "bad_request", "plugin binding selector ambiguous")
	case errors.Is(err, managementusecase.ErrPluginBindingUIDRequired):
		jsonError(c, http.StatusBadRequest, "bad_request", "plugin binding uid required")
	case errors.Is(err, pluginusecase.ErrPluginNoRequired):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid plugin_no")
	case errors.Is(err, pluginusecase.ErrInvalidPluginNo):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid plugin_no")
	case errors.Is(err, pluginusecase.ErrPluginNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "plugin not found")
	case errors.Is(err, managementusecase.ErrPluginNodeUnavailable), errors.Is(err, pluginusecase.ErrRuntimeRequired), errors.Is(err, pluginusecase.ErrDesiredStoreRequired):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "plugin node unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

func pluginDTOs(plugins []managementusecase.Plugin) []PluginDTO {
	out := make([]PluginDTO, 0, len(plugins))
	for _, plugin := range plugins {
		out = append(out, pluginDTO(plugin))
	}
	return out
}

func pluginDTO(plugin managementusecase.Plugin) PluginDTO {
	return PluginDTO{
		NodeID:           plugin.NodeID,
		PluginNo:         plugin.No,
		Name:             plugin.Name,
		Version:          plugin.Version,
		ConfigTemplate:   plugin.ConfigTemplate,
		Config:           plugin.Config,
		CreatedAt:        plugin.CreatedAt,
		UpdatedAt:        plugin.UpdatedAt,
		Methods:          pluginMethodStrings(plugin.Methods),
		Priority:         plugin.Priority,
		PersistAfterSync: plugin.PersistAfterSync,
		ReplySync:        plugin.ReplySync,
		Status:           plugin.Status,
		Enabled:          plugin.Enabled,
		IsAI:             plugin.IsAI,
		PID:              plugin.PID,
		LastSeenAt:       plugin.LastSeenAt,
		LastError:        plugin.LastError,
	}
}

func pluginBindingsResponseDTO(resp managementusecase.PluginBindingListResponse) PluginBindingsResponse {
	details := resp.Details
	if len(details) == 0 {
		details = make([]managementusecase.PluginBindingDetail, 0, len(resp.Bindings))
		for _, binding := range resp.Bindings {
			details = append(details, managementusecase.PluginBindingDetail{Binding: binding})
		}
	}
	items := make([]PluginBindingDTO, 0, len(details))
	for _, detail := range details {
		items = append(items, pluginBindingDTO(detail))
	}
	return PluginBindingsResponse{
		Items:      items,
		Total:      len(items),
		NextCursor: resp.Cursor,
		HasMore:    resp.HasMore,
		Warnings:   pluginBindingWarningDTOs(resp.Warnings),
	}
}

func pluginBindingDTO(detail managementusecase.PluginBindingDetail) PluginBindingDTO {
	var plugin *PluginDTO
	if detail.Plugin != nil {
		dto := pluginDTO(*detail.Plugin)
		plugin = &dto
	}
	return PluginBindingDTO{
		UID:      detail.Binding.UID,
		PluginNo: detail.Binding.PluginNo,
		Plugin:   plugin,
		Warnings: pluginBindingWarningDTOs(detail.Warnings),
	}
}

func pluginBindingMutationResponseDTO(resp managementusecase.PluginBindingMutationResponse) PluginBindingMutationResponseDTO {
	return PluginBindingMutationResponseDTO{
		Binding: PluginBindingDTO{
			UID:      resp.Binding.UID,
			PluginNo: resp.Binding.PluginNo,
			Warnings: []PluginBindingWarningDTO{},
		},
		Changed:  resp.Changed,
		Warnings: pluginBindingWarningDTOs(resp.Warnings),
	}
}

func pluginBindingWarningDTOs(warnings []managementusecase.PluginBindingWarning) []PluginBindingWarningDTO {
	out := make([]PluginBindingWarningDTO, 0, len(warnings))
	for _, warning := range warnings {
		out = append(out, PluginBindingWarningDTO{
			Code:     warning.Code,
			Message:  warning.Message,
			UID:      warning.UID,
			PluginNo: warning.PluginNo,
		})
	}
	return out
}

func pluginMethodStrings(methods []pluginusecase.Method) []string {
	out := make([]string, 0, len(methods))
	for _, method := range methods {
		out = append(out, string(method))
	}
	return out
}
