package manager

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/gin-gonic/gin"
)

// MonitorMetricsResponse is the JSON response for GET /manager/monitor/metrics.
type MonitorMetricsResponse struct {
	// GeneratedAt is the time when the monitor metrics response was assembled.
	GeneratedAt time.Time `json:"generated_at"`
	// WindowSeconds is the requested query window size in seconds.
	WindowSeconds int `json:"window_seconds"`
	// StepSeconds is the requested chart bucket size in seconds.
	StepSeconds int `json:"step_seconds"`
	// Points is the number of points in every returned series.
	Points int `json:"points"`
	// Scope describes the observation scope for this first monitor API version.
	Scope MonitorMetricsScopeDTO `json:"scope"`
	// Capabilities reports which interactive filters are backed by real data.
	Capabilities MonitorMetricsCapabilitiesDTO `json:"capabilities"`
	// Nodes lists nodes that can be represented by the current response.
	Nodes []MonitorMetricsNodeDTO `json:"nodes"`
	// Metrics contains available real metric series keyed by stable snake_case names.
	Metrics map[string]MonitorMetricSeriesDTO `json:"metrics"`
}

// MonitorMetricsScopeDTO describes the source scope for monitor metrics.
type MonitorMetricsScopeDTO struct {
	// View is the stable scope name.
	View string `json:"view"`
	// LocalNodeID identifies the node that owns the local collector.
	LocalNodeID uint64 `json:"local_node_id"`
	// NodeID identifies the selected node for node-scoped responses.
	NodeID uint64 `json:"node_id,omitempty"`
}

// MonitorMetricsCapabilitiesDTO reports UI capabilities backed by real data.
type MonitorMetricsCapabilitiesDTO struct {
	// NodeFilter is true only when the API can return per-node series.
	NodeFilter bool `json:"node_filter"`
}

// MonitorMetricsNodeDTO describes one node visible to the monitor response.
type MonitorMetricsNodeDTO struct {
	// NodeID is the cluster node identifier.
	NodeID uint64 `json:"node_id"`
	// Name is the display name for the node.
	Name string `json:"name"`
	// IsLocal reports whether this node owns the local collector.
	IsLocal bool `json:"is_local"`
	// Available reports whether this node has metric series in the response.
	Available bool `json:"available"`
}

// MonitorMetricSeriesDTO is one timestamped monitor metric series.
type MonitorMetricSeriesDTO struct {
	// Key is the stable snake_case metric key.
	Key string `json:"key"`
	// Unit is the display unit for values.
	Unit string `json:"unit"`
	// Latest is the latest value in the series.
	Latest float64 `json:"latest"`
	// Peak is the maximum value in the series.
	Peak float64 `json:"peak"`
	// Avg is the average value in the series.
	Avg float64 `json:"avg"`
	// Points contains timestamped values for chart rendering.
	Points []MonitorMetricPointDTO `json:"points"`
}

// MonitorMetricPointDTO is one chart point for a monitor metric.
type MonitorMetricPointDTO struct {
	// At is the chart bucket start time.
	At time.Time `json:"at"`
	// Value is the metric value for the bucket.
	Value float64 `json:"value"`
}

func (s *Server) handleMonitorMetrics(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	windowStr := c.DefaultQuery("window", "5m")
	stepStr := c.DefaultQuery("step", "5s")
	nodeID, ok := parseMonitorMetricsNodeID(c)
	if !ok {
		return
	}

	window, err := time.ParseDuration(windowStr)
	if err != nil || window < time.Minute || window > time.Hour {
		jsonError(c, http.StatusBadRequest, "invalid_param", "window must be between 1m and 1h")
		return
	}

	step, err := time.ParseDuration(stepStr)
	if err != nil || step < 5*time.Second || step > 60*time.Second {
		jsonError(c, http.StatusBadRequest, "invalid_param", "step must be between 5s and 60s")
		return
	}

	if window%step != 0 {
		jsonError(c, http.StatusBadRequest, "invalid_param", "window must be evenly divisible by step")
		return
	}

	result, err := s.management.GetMonitorMetrics(c.Request.Context(), nodeID, window, step)
	if err != nil {
		if errors.Is(err, metrics.ErrInsufficientData) {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "monitor metrics collector warming up")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	c.JSON(http.StatusOK, monitorMetricsResponse(result))
}

func monitorMetricsResponse(result managementusecase.MonitorMetricsResult) MonitorMetricsResponse {
	return MonitorMetricsResponse{
		GeneratedAt:   result.GeneratedAt,
		WindowSeconds: result.WindowSeconds,
		StepSeconds:   result.StepSeconds,
		Points:        result.Points,
		Scope: MonitorMetricsScopeDTO{
			View:        result.Scope.View,
			LocalNodeID: result.Scope.LocalNodeID,
			NodeID:      result.Scope.NodeID,
		},
		Capabilities: MonitorMetricsCapabilitiesDTO{
			NodeFilter: result.Capabilities.NodeFilter,
		},
		Nodes:   monitorMetricsNodeDTOs(result.Nodes),
		Metrics: monitorMetricSeriesDTOs(result.Metrics),
	}
}

func parseMonitorMetricsNodeID(c *gin.Context) (uint64, bool) {
	value := c.Query("node_id")
	if value == "" || value == "all" {
		return 0, true
	}
	nodeID, err := strconv.ParseUint(value, 10, 64)
	if err != nil || nodeID == 0 {
		jsonError(c, http.StatusBadRequest, "invalid_param", "node_id must be a positive integer")
		return 0, false
	}
	return nodeID, true
}

func monitorMetricsNodeDTOs(nodes []managementusecase.MonitorMetricsNode) []MonitorMetricsNodeDTO {
	out := make([]MonitorMetricsNodeDTO, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, MonitorMetricsNodeDTO{
			NodeID:    node.NodeID,
			Name:      node.Name,
			IsLocal:   node.IsLocal,
			Available: node.Available,
		})
	}
	return out
}

func monitorMetricSeriesDTOs(metricsMap map[string]managementusecase.MonitorMetricSeries) map[string]MonitorMetricSeriesDTO {
	out := make(map[string]MonitorMetricSeriesDTO, len(metricsMap))
	for key, series := range metricsMap {
		out[key] = MonitorMetricSeriesDTO{
			Key:    series.Key,
			Unit:   series.Unit,
			Latest: series.Latest,
			Peak:   series.Peak,
			Avg:    series.Avg,
			Points: monitorMetricPointDTOs(series.Points),
		}
	}
	return out
}

func monitorMetricPointDTOs(points []managementusecase.MonitorMetricPoint) []MonitorMetricPointDTO {
	out := make([]MonitorMetricPointDTO, 0, len(points))
	for _, point := range points {
		out = append(out, MonitorMetricPointDTO{At: point.At, Value: point.Value})
	}
	return out
}
