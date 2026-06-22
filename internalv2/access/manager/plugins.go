package manager

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	pluginusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/plugin"
	"github.com/gin-gonic/gin"
)

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
	nodeID, err := parseRequiredPluginNodeID(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}
	pluginNo := strings.TrimSpace(c.Param("plugin_no"))
	if pluginNo == "" {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid plugin_no")
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

func writePluginError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, managementusecase.ErrPluginNodeIDRequired):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
	case errors.Is(err, pluginusecase.ErrPluginNoRequired):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid plugin_no")
	case errors.Is(err, pluginusecase.ErrPluginNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "plugin not found")
	case errors.Is(err, managementusecase.ErrPluginNodeUnavailable):
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

func pluginMethodStrings(methods []pluginusecase.Method) []string {
	out := make([]string, 0, len(methods))
	for _, method := range methods {
		out = append(out, string(method))
	}
	return out
}
