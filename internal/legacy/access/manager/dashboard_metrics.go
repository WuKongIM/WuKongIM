package manager

import (
	"net/http"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/gin-gonic/gin"
)

// DashboardMetricsResponse is the JSON response for GET /manager/dashboard/metrics.
type DashboardMetricsResponse struct {
	GeneratedAt   time.Time                   `json:"generated_at"`
	WindowSeconds int                         `json:"window_seconds"`
	StepSeconds   int                         `json:"step_seconds"`
	Points        int                         `json:"points"`
	Metrics       DashboardMetricsResponseMap `json:"metrics"`
}

// DashboardMetricsResponseMap holds all 10 metric series DTOs.
type DashboardMetricsResponseMap struct {
	SendPerSec              MetricSeriesDTO `json:"send_per_sec"`
	DeliverPerSec           MetricSeriesDTO `json:"deliver_per_sec"`
	Connections             MetricSeriesDTO `json:"connections"`
	SendLatencyP99Ms        MetricSeriesDTO `json:"send_latency_p99_ms"`
	DeliveryLatencyP99Ms    MetricSeriesDTO `json:"delivery_latency_p99_ms"`
	SendFailRatePercent     MetricSeriesDTO `json:"send_fail_rate_percent"`
	DeliveryFailRatePercent MetricSeriesDTO `json:"delivery_fail_rate_percent"`
	ActiveChannels          MetricSeriesDTO `json:"active_channels"`
	RetryQueueDepth         MetricSeriesDTO `json:"retry_queue_depth"`
	FanOutRate              MetricSeriesDTO `json:"fan_out_rate"`
}

// MetricSeriesDTO is the JSON shape for one metric time-series.
type MetricSeriesDTO struct {
	Latest float64   `json:"latest"`
	Peak   float64   `json:"peak"`
	Avg    float64   `json:"avg"`
	Series []float64 `json:"series"`
}

func metricSeriesDTO(ms metrics.MetricSeries) MetricSeriesDTO {
	series := ms.Series
	if series == nil {
		series = []float64{}
	}
	return MetricSeriesDTO{Latest: ms.Latest, Peak: ms.Peak, Avg: ms.Avg, Series: series}
}

func (s *Server) handleDashboardMetrics(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	windowStr := c.DefaultQuery("window", "30m")
	stepStr := c.DefaultQuery("step", "30s")

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

	result, err := s.management.GetDashboardMetrics(window, step)
	if err != nil {
		if err == metrics.ErrInsufficientData {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "dashboard metrics collector warming up")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	c.JSON(http.StatusOK, DashboardMetricsResponse{
		GeneratedAt:   result.GeneratedAt,
		WindowSeconds: result.WindowSeconds,
		StepSeconds:   result.StepSeconds,
		Points:        result.Points,
		Metrics: DashboardMetricsResponseMap{
			SendPerSec:              metricSeriesDTO(result.Metrics.SendPerSec),
			DeliverPerSec:           metricSeriesDTO(result.Metrics.DeliverPerSec),
			Connections:             metricSeriesDTO(result.Metrics.Connections),
			SendLatencyP99Ms:        metricSeriesDTO(result.Metrics.SendLatencyP99Ms),
			DeliveryLatencyP99Ms:    metricSeriesDTO(result.Metrics.DeliveryLatencyP99Ms),
			SendFailRatePercent:     metricSeriesDTO(result.Metrics.SendFailRatePercent),
			DeliveryFailRatePercent: metricSeriesDTO(result.Metrics.DeliveryFailRatePercent),
			ActiveChannels:          metricSeriesDTO(result.Metrics.ActiveChannels),
			RetryQueueDepth:         metricSeriesDTO(result.Metrics.RetryQueueDepth),
			FanOutRate:              metricSeriesDTO(result.Metrics.FanOutRate),
		},
	})
}
