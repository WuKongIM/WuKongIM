package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var channelWriteItemBuckets = []float64{1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024}

// ChannelWriteMetrics exposes internalv2 channel authority writer metrics.
type ChannelWriteMetrics struct {
	routerTotal         *prometheus.CounterVec
	routerDuration      *prometheus.HistogramVec
	routerItems         *prometheus.HistogramVec
	localAdmissionTotal *prometheus.CounterVec
	localAdmissionItems *prometheus.HistogramVec
	writerAdmission     *prometheus.GaugeVec
	writerAdmissionCap  *prometheus.GaugeVec
	writerPoolRunning   *prometheus.GaugeVec
	writerPoolCap       *prometheus.GaugeVec
	writerStateItems    *prometheus.GaugeVec
	effectPoolSubmit    *prometheus.CounterVec
	effectPoolInflight  *prometheus.GaugeVec
	effectPoolCap       *prometheus.GaugeVec
	effectPoolSaturated *prometheus.GaugeVec
	effectTotal         *prometheus.CounterVec
	effectDuration      *prometheus.HistogramVec
	effectItems         *prometheus.HistogramVec
}

func newChannelWriteMetrics(registry prometheus.Registerer, labels prometheus.Labels) *ChannelWriteMetrics {
	m := &ChannelWriteMetrics{
		routerTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelwrite_router_total",
			Help:        "Total internalv2 channel write router groups by path and result.",
			ConstLabels: labels,
		}, []string{"path", "result"}),
		routerDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelwrite_router_duration_seconds",
			Help:        "Internalv2 channel write router group latency in seconds.",
			ConstLabels: labels,
			Buckets:     channelV2DurationBuckets,
		}, []string{"path", "result"}),
		routerItems: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelwrite_router_items",
			Help:        "Number of SEND items in each internalv2 channel write router group.",
			ConstLabels: labels,
			Buckets:     channelWriteItemBuckets,
		}, []string{"path", "result"}),
		localAdmissionTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelwrite_local_admission_total",
			Help:        "Total local channel authority writer admission attempts.",
			ConstLabels: labels,
		}, []string{"result"}),
		localAdmissionItems: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelwrite_local_admission_items",
			Help:        "Number of SEND items in each local channel authority writer admission attempt.",
			ConstLabels: labels,
			Buckets:     channelWriteItemBuckets,
		}, []string{"result"}),
		writerAdmission: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelwrite_writer_admission_depth",
			Help:        "Current admitted-but-incomplete internalv2 channel write items.",
			ConstLabels: labels,
		}, nil),
		writerAdmissionCap: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelwrite_writer_admission_capacity",
			Help:        "Configured admitted item capacity for the internalv2 channel write group.",
			ConstLabels: labels,
		}, nil),
		writerPoolRunning: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelwrite_writer_pool_running",
			Help:        "Current running workers in the internalv2 channel write shared pool.",
			ConstLabels: labels,
		}, nil),
		writerPoolCap: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelwrite_writer_pool_capacity",
			Help:        "Configured internalv2 channel write shared pool capacity.",
			ConstLabels: labels,
		}, nil),
		writerStateItems: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelwrite_writer_state_items",
			Help:        "Current channel write state item counts by kind.",
			ConstLabels: labels,
		}, []string{"kind"}),
		effectPoolSubmit: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelwrite_effect_pool_submit_total",
			Help:        "Total internalv2 channel write shared effect pool submit attempts by stage and result.",
			ConstLabels: labels,
		}, []string{"stage", "result"}),
		effectPoolInflight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelwrite_effect_pool_inflight",
			Help:        "Current internalv2 channel write shared effect pool inflight workers by stage.",
			ConstLabels: labels,
		}, []string{"stage"}),
		effectPoolCap: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelwrite_effect_pool_capacity",
			Help:        "Configured internalv2 channel write shared effect pool capacity by stage.",
			ConstLabels: labels,
		}, []string{"stage"}),
		effectPoolSaturated: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelwrite_effect_pool_saturated",
			Help:        "Whether the internalv2 channel write shared effect pool is saturated by stage.",
			ConstLabels: labels,
		}, []string{"stage"}),
		effectTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelwrite_effect_total",
			Help:        "Total internalv2 channel write asynchronous effects by stage and result.",
			ConstLabels: labels,
		}, []string{"stage", "result"}),
		effectDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelwrite_effect_duration_seconds",
			Help:        "Internalv2 channel write asynchronous effect latency in seconds.",
			ConstLabels: labels,
			Buckets:     channelV2DurationBuckets,
		}, []string{"stage", "result"}),
		effectItems: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelwrite_effect_items",
			Help:        "Number of logical items handled by each internalv2 channel write effect.",
			ConstLabels: labels,
			Buckets:     channelWriteItemBuckets,
		}, []string{"stage", "result"}),
	}

	registry.MustRegister(
		m.routerTotal,
		m.routerDuration,
		m.routerItems,
		m.localAdmissionTotal,
		m.localAdmissionItems,
		m.writerAdmission,
		m.writerAdmissionCap,
		m.writerPoolRunning,
		m.writerPoolCap,
		m.writerStateItems,
		m.effectPoolSubmit,
		m.effectPoolInflight,
		m.effectPoolCap,
		m.effectPoolSaturated,
		m.effectTotal,
		m.effectDuration,
		m.effectItems,
	)

	return m
}

// ObserveRouter records one foreground router group.
func (m *ChannelWriteMetrics) ObserveRouter(path, result string, items int, dur time.Duration) {
	if m == nil {
		return
	}
	if items < 0 {
		items = 0
	}
	m.routerTotal.WithLabelValues(path, result).Inc()
	m.routerDuration.WithLabelValues(path, result).Observe(dur.Seconds())
	m.routerItems.WithLabelValues(path, result).Observe(float64(items))
}

// ObserveLocalAdmission records one local writer admission attempt.
func (m *ChannelWriteMetrics) ObserveLocalAdmission(result string, items int) {
	if m == nil {
		return
	}
	if items < 0 {
		items = 0
	}
	m.localAdmissionTotal.WithLabelValues(result).Inc()
	m.localAdmissionItems.WithLabelValues(result).Observe(float64(items))
}

// SetWriterPressure sets current local writer-group pressure gauges.
func (m *ChannelWriteMetrics) SetWriterPressure(admissionDepth int, admissionCapacity int, workerRunning int, workerCapacity int, pendingAppendItems int, appendInflightItems int, postCommitBacklog int) {
	if m == nil {
		return
	}
	m.writerAdmission.WithLabelValues().Set(float64(admissionDepth))
	m.writerAdmissionCap.WithLabelValues().Set(float64(admissionCapacity))
	m.writerPoolRunning.WithLabelValues().Set(float64(workerRunning))
	m.writerPoolCap.WithLabelValues().Set(float64(workerCapacity))
	m.writerStateItems.WithLabelValues("pending_append").Set(float64(pendingAppendItems))
	m.writerStateItems.WithLabelValues("append_inflight").Set(float64(appendInflightItems))
	m.writerStateItems.WithLabelValues("post_commit_backlog").Set(float64(postCommitBacklog))
}

// ObserveEffectPool records shared effect pool admission and pressure.
func (m *ChannelWriteMetrics) ObserveEffectPool(stage, result string, inflight int, capacity int, saturated bool) {
	if m == nil {
		return
	}
	if stage == "" {
		stage = "unknown"
	}
	if result == "" {
		result = "unknown"
	}
	if inflight < 0 {
		inflight = 0
	}
	if capacity < 0 {
		capacity = 0
	}
	if result != "released" {
		m.effectPoolSubmit.WithLabelValues(stage, result).Inc()
	}
	m.effectPoolInflight.WithLabelValues(stage).Set(float64(inflight))
	m.effectPoolCap.WithLabelValues(stage).Set(float64(capacity))
	if saturated {
		m.effectPoolSaturated.WithLabelValues(stage).Set(1)
		return
	}
	m.effectPoolSaturated.WithLabelValues(stage).Set(0)
}

// ObserveEffect records one asynchronous channel write effect.
func (m *ChannelWriteMetrics) ObserveEffect(stage, result string, items int, dur time.Duration) {
	if m == nil {
		return
	}
	if items < 0 {
		items = 0
	}
	m.effectTotal.WithLabelValues(stage, result).Inc()
	m.effectDuration.WithLabelValues(stage, result).Observe(dur.Seconds())
	m.effectItems.WithLabelValues(stage, result).Observe(float64(items))
}
