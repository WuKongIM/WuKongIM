package manager

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
	"github.com/gin-gonic/gin"
)

const (
	defaultRuntimeWorkqueuesWindow = 10 * time.Second
	minRuntimeWorkqueuesWindow     = 2 * time.Second
	defaultRuntimeWorkqueuesLimit  = 100
	maxRuntimeWorkqueuesLimit      = 200
	runtimeWorkqueuesScopeLocal    = "local_node"
)

// RuntimeWorkqueuesResponse is the manager runtime workqueue pressure body.
type RuntimeWorkqueuesResponse struct {
	// GeneratedAt is the UTC time when the runtime pressure snapshot was produced.
	GeneratedAt time.Time `json:"generated_at"`
	// WindowSeconds is the aggregation window represented in seconds.
	WindowSeconds int `json:"window_seconds"`
	// Scope identifies the local node represented by this response.
	Scope RuntimeWorkqueuesScope `json:"scope"`
	// Summary contains aggregate pressure counts and the hottest queue.
	Summary RuntimeWorkqueuesSummary `json:"summary"`
	// Items lists workqueue pressure rows in provider order.
	Items []RuntimeWorkqueueItem `json:"items"`
	// Sources reports local collector and metrics availability.
	Sources RuntimeWorkqueuesSources `json:"sources"`
}

// RuntimeWorkqueuesScope identifies the local node runtime view.
type RuntimeWorkqueuesScope struct {
	// View is always local_node for manager runtime workqueue snapshots.
	View string `json:"view"`
	// NodeID is the local cluster node ID.
	NodeID uint64 `json:"node_id"`
	// NodeName is the local cluster node name.
	NodeName string `json:"node_name"`
	// Ready reports whether the local node is ready.
	Ready bool `json:"ready"`
}

// RuntimeWorkqueuesSummary summarizes pressure levels across returned rows.
type RuntimeWorkqueuesSummary struct {
	// OverallLevel is the provider pressure level, or ok when unavailable.
	OverallLevel string `json:"overall_level"`
	// Total is the number of returned pressure rows.
	Total int `json:"total"`
	// OK is the number of rows with ok pressure.
	OK int `json:"ok"`
	// Busy is the number of rows with busy pressure.
	Busy int `json:"busy"`
	// Degraded is the number of rows with degraded pressure.
	Degraded int `json:"degraded"`
	// Critical is the number of rows with critical pressure.
	Critical int `json:"critical"`
	// Hottest is the first pressure row when one is available.
	Hottest *RuntimeWorkqueueHotItem `json:"hottest,omitempty"`
}

// RuntimeWorkqueueHotItem identifies the highest-ranked pressure row.
type RuntimeWorkqueueHotItem struct {
	// Component is the subsystem that owns the pressure signal.
	Component string `json:"component"`
	// Pool is the worker or resource pool name.
	Pool string `json:"pool"`
	// Queue is the queue name.
	Queue string `json:"queue"`
	// Priority is the queue priority label.
	Priority string `json:"priority"`
	// Level is the pressure level for this item.
	Level string `json:"level"`
	// Score is a normalized pressure score.
	Score float64 `json:"score"`
}

// RuntimeWorkqueueItem describes one manager-facing workqueue pressure row.
type RuntimeWorkqueueItem struct {
	// Component is the subsystem that owns the pressure signal.
	Component string `json:"component"`
	// Pool is the worker or resource pool name.
	Pool string `json:"pool"`
	// Queue is the queue name.
	Queue string `json:"queue"`
	// Priority is the queue priority label.
	Priority string `json:"priority"`
	// Level is the pressure level for this item.
	Level string `json:"level"`
	// Score is a normalized pressure score.
	Score float64 `json:"score"`
	// Depth is the current queue depth.
	Depth int64 `json:"depth"`
	// Capacity is the queue capacity used to score depth pressure.
	Capacity int64 `json:"capacity"`
	// Inflight is the current number of running tasks.
	Inflight int64 `json:"inflight"`
	// Workers is the worker capacity used to score inflight pressure.
	Workers int64 `json:"workers"`
	// WaitP99MS is p99 queue wait time in milliseconds.
	WaitP99MS float64 `json:"wait_p99_ms"`
	// TaskP99MS is p99 task execution time in milliseconds.
	TaskP99MS float64 `json:"task_p99_ms"`
	// AdmissionErrorPerSec is rejected or timed-out admissions per second.
	AdmissionErrorPerSec float64 `json:"admission_error_per_sec"`
	// Hint is a short operator-facing remediation clue.
	Hint string `json:"hint"`
}

// RuntimeWorkqueuesSources reports the data sources used for the manager runtime view.
type RuntimeWorkqueuesSources struct {
	// Collector reports top collector availability and sample count.
	Collector RuntimeWorkqueuesCollectorSource `json:"collector"`
	// Metrics reports whether Prometheus metrics are enabled or required.
	Metrics RuntimeWorkqueuesMetricsSource `json:"metrics"`
	// Notes lists non-fatal source omissions or partial data details.
	Notes []string `json:"notes"`
}

// RuntimeWorkqueuesCollectorSource reports local top collector availability.
type RuntimeWorkqueuesCollectorSource struct {
	// Available reports whether the collector contributed data.
	Available bool `json:"available"`
	// SampleCount is the number of samples used from the collector.
	SampleCount int `json:"sample_count"`
}

// RuntimeWorkqueuesMetricsSource reports metrics integration status.
type RuntimeWorkqueuesMetricsSource struct {
	// Enabled reports whether the Prometheus metrics endpoint is enabled.
	Enabled bool `json:"enabled"`
	// Required reports whether metrics are required for this runtime view.
	Required bool `json:"required"`
}

type runtimeWorkqueuesQuery struct {
	window time.Duration
	limit  int
}

func parseRuntimeWorkqueuesQuery(c *gin.Context) (runtimeWorkqueuesQuery, error) {
	query := runtimeWorkqueuesQuery{
		window: defaultRuntimeWorkqueuesWindow,
		limit:  defaultRuntimeWorkqueuesLimit,
	}
	if raw := strings.TrimSpace(c.Query("window")); raw != "" {
		window, err := time.ParseDuration(raw)
		if err != nil {
			return query, fmt.Errorf("window invalid")
		}
		query.window = window
	}
	if query.window < minRuntimeWorkqueuesWindow {
		return query, fmt.Errorf("window invalid")
	}
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			return query, fmt.Errorf("limit invalid")
		}
		if limit > maxRuntimeWorkqueuesLimit {
			limit = maxRuntimeWorkqueuesLimit
		}
		query.limit = limit
	}
	return query, nil
}

func (s *Server) handleRuntimeWorkqueues(c *gin.Context) {
	if s == nil || s.top == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "top snapshot provider is not configured")
		return
	}
	query, err := parseRuntimeWorkqueuesQuery(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	snapshot, err := s.top.SnapshotTop(c.Request.Context(), accessapi.TopSnapshotQuery{
		View:   accessapi.TopViewRuntime,
		Window: query.window,
		Limit:  query.limit,
	})
	if err != nil {
		if errors.Is(err, accessapi.ErrTopWarmingUp) {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "top collector warming up")
			return
		}
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "top snapshot unavailable")
		return
	}
	c.JSON(http.StatusOK, mapRuntimeWorkqueuesSnapshot(snapshot, query.window))
}

func mapRuntimeWorkqueuesSnapshot(snapshot accessapi.TopSnapshot, fallbackWindow time.Duration) RuntimeWorkqueuesResponse {
	windowSeconds := snapshot.WindowSeconds
	if windowSeconds == 0 {
		windowSeconds = int(fallbackWindow / time.Second)
	}
	items := make([]RuntimeWorkqueueItem, 0)
	summary := RuntimeWorkqueuesSummary{OverallLevel: "ok"}
	if snapshot.Pressure != nil {
		if snapshot.Pressure.OverallLevel != "" {
			summary.OverallLevel = snapshot.Pressure.OverallLevel
		}
		items = make([]RuntimeWorkqueueItem, 0, len(snapshot.Pressure.Top))
		for _, item := range snapshot.Pressure.Top {
			items = append(items, mapRuntimeWorkqueueItem(item))
		}
	}
	summary.Total = len(items)
	for _, item := range items {
		switch item.Level {
		case "ok":
			summary.OK++
		case "busy":
			summary.Busy++
		case "degraded":
			summary.Degraded++
		case "critical":
			summary.Critical++
		}
	}
	if len(items) > 0 {
		summary.Hottest = &RuntimeWorkqueueHotItem{
			Component: items[0].Component,
			Pool:      items[0].Pool,
			Queue:     items[0].Queue,
			Priority:  items[0].Priority,
			Level:     items[0].Level,
			Score:     items[0].Score,
		}
	}
	notes := append([]string(nil), snapshot.Sources.Notes...)
	if notes == nil {
		notes = []string{}
	}
	return RuntimeWorkqueuesResponse{
		GeneratedAt:   snapshot.GeneratedAt,
		WindowSeconds: windowSeconds,
		Scope: RuntimeWorkqueuesScope{
			View:     runtimeWorkqueuesScopeLocal,
			NodeID:   snapshot.Node.ID,
			NodeName: snapshot.Node.Name,
			Ready:    snapshot.Node.Ready,
		},
		Summary: summary,
		Items:   items,
		Sources: RuntimeWorkqueuesSources{
			Collector: RuntimeWorkqueuesCollectorSource{
				Available:   snapshot.Sources.Collector.Available,
				SampleCount: snapshot.Sources.Collector.SampleCount,
			},
			Metrics: RuntimeWorkqueuesMetricsSource{
				Enabled:  snapshot.Sources.Metrics.Enabled,
				Required: snapshot.Sources.Metrics.Required,
			},
			Notes: notes,
		},
	}
}

func mapRuntimeWorkqueueItem(item accessapi.TopPressureItem) RuntimeWorkqueueItem {
	return RuntimeWorkqueueItem{
		Component:            item.Component,
		Pool:                 item.Pool,
		Queue:                item.Queue,
		Priority:             item.Priority,
		Level:                item.Level,
		Score:                item.Score,
		Depth:                item.Depth,
		Capacity:             item.Capacity,
		Inflight:             item.Inflight,
		Workers:              item.Workers,
		WaitP99MS:            item.WaitP99MS,
		TaskP99MS:            item.TaskP99MS,
		AdmissionErrorPerSec: item.AdmissionErrorPerSec,
		Hint:                 item.Hint,
	}
}
