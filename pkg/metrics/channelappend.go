package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var channelAppendItemBuckets = []float64{1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024}

// ChannelAppendMetrics exposes internal channel authority writer metrics.
type ChannelAppendMetrics struct {
	routerTotal              *prometheus.CounterVec
	routerDuration           *prometheus.HistogramVec
	routerItems              *prometheus.HistogramVec
	localAdmissionTotal      *prometheus.CounterVec
	localAdmissionItems      *prometheus.HistogramVec
	writerAdmission          *prometheus.GaugeVec
	writerAdmissionCap       *prometheus.GaugeVec
	writerPoolRunning        *prometheus.GaugeVec
	writerPoolCap            *prometheus.GaugeVec
	writerStateItems         *prometheus.GaugeVec
	postCommitHandoff        *prometheus.GaugeVec
	postCommitHandoffCap     *prometheus.GaugeVec
	postCommitRetryQueue     *prometheus.GaugeVec
	postCommitRetryContended *prometheus.GaugeVec
	effectPoolSubmit         *prometheus.CounterVec
	effectPoolInflight       *prometheus.GaugeVec
	effectPoolCap            *prometheus.GaugeVec
	effectPoolSaturated      *prometheus.GaugeVec
	effectTotal              *prometheus.CounterVec
	effectDuration           *prometheus.HistogramVec
	effectItems              *prometheus.HistogramVec
}

func newChannelAppendMetrics(registry prometheus.Registerer, labels prometheus.Labels) *ChannelAppendMetrics {
	m := &ChannelAppendMetrics{
		routerTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelappend_router_total",
			Help:        "Total internal channel append router groups by path and result.",
			ConstLabels: labels,
		}, []string{"path", "result"}),
		routerDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelappend_router_duration_seconds",
			Help:        "Internal channel append router group latency in seconds.",
			ConstLabels: labels,
			Buckets:     channelRuntimeDurationBuckets,
		}, []string{"path", "result"}),
		routerItems: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelappend_router_items",
			Help:        "Number of SEND items in each internal channel append router group.",
			ConstLabels: labels,
			Buckets:     channelAppendItemBuckets,
		}, []string{"path", "result"}),
		localAdmissionTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelappend_local_admission_total",
			Help:        "Total local channel authority writer admission attempts.",
			ConstLabels: labels,
		}, []string{"result"}),
		localAdmissionItems: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelappend_local_admission_items",
			Help:        "Number of SEND items in each local channel authority writer admission attempt.",
			ConstLabels: labels,
			Buckets:     channelAppendItemBuckets,
		}, []string{"result"}),
		writerAdmission: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelappend_writer_admission_depth",
			Help:        "Current admitted-but-incomplete internal channel append items.",
			ConstLabels: labels,
		}, nil),
		writerAdmissionCap: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelappend_writer_admission_capacity",
			Help:        "Configured admitted item capacity for the internal channel append group.",
			ConstLabels: labels,
		}, nil),
		writerPoolRunning: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelappend_writer_pool_running",
			Help:        "Current running workers in the internal channel append foreground append pool.",
			ConstLabels: labels,
		}, nil),
		writerPoolCap: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelappend_writer_pool_capacity",
			Help:        "Configured internal channel append foreground append pool capacity.",
			ConstLabels: labels,
		}, nil),
		writerStateItems: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelappend_writer_state_items",
			Help:        "Current channel append state item counts by kind.",
			ConstLabels: labels,
		}, []string{"kind"}),
		postCommitHandoff: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelappend_post_commit_handoff_depth",
			Help:        "Current durable messages owning a channel append post-commit reservation.",
			ConstLabels: labels,
		}, nil),
		postCommitHandoffCap: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelappend_post_commit_handoff_capacity",
			Help:        "Configured durable-message reservation capacity for channel append post-commit work.",
			ConstLabels: labels,
		}, nil),
		postCommitRetryQueue: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelappend_post_commit_retry_queue_depth",
			Help:        "Current de-duplicated channel writers waiting in the post-commit retry FIFO.",
			ConstLabels: labels,
		}, nil),
		postCommitRetryContended: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelappend_post_commit_retry_contended",
			Help:        "Whether an older post-commit retry writer is waiting for or owns the retry turn.",
			ConstLabels: labels,
		}, nil),
		effectPoolSubmit: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelappend_effect_pool_submit_total",
			Help:        "Total internal channel append effect pool submit attempts by stage and result.",
			ConstLabels: labels,
		}, []string{"stage", "result"}),
		effectPoolInflight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelappend_effect_pool_inflight",
			Help:        "Current internal channel append effect pool inflight workers by stage.",
			ConstLabels: labels,
		}, []string{"stage"}),
		effectPoolCap: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelappend_effect_pool_capacity",
			Help:        "Configured internal channel append effect pool capacity by stage.",
			ConstLabels: labels,
		}, []string{"stage"}),
		effectPoolSaturated: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelappend_effect_pool_saturated",
			Help:        "Whether the internal channel append effect pool is saturated by stage.",
			ConstLabels: labels,
		}, []string{"stage"}),
		effectTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelappend_effect_total",
			Help:        "Total internal channel append asynchronous effects by stage and result.",
			ConstLabels: labels,
		}, []string{"stage", "result"}),
		effectDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelappend_effect_duration_seconds",
			Help:        "Internal channel append asynchronous effect latency in seconds.",
			ConstLabels: labels,
			Buckets:     channelRuntimeDurationBuckets,
		}, []string{"stage", "result"}),
		effectItems: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelappend_effect_items",
			Help:        "Number of logical items handled by each internal channel append effect.",
			ConstLabels: labels,
			Buckets:     channelAppendItemBuckets,
		}, []string{"stage", "result"}),
	}
	// Materialize idle writer-state series so quiescence checks can distinguish zero backlog from a missing metric contract.
	for _, kind := range []string{"pending_append", "append_inflight", "post_commit_backlog"} {
		m.writerStateItems.WithLabelValues(kind).Set(0)
	}
	m.postCommitHandoff.WithLabelValues().Set(0)
	m.postCommitHandoffCap.WithLabelValues().Set(0)
	m.postCommitRetryQueue.WithLabelValues().Set(0)
	m.postCommitRetryContended.WithLabelValues().Set(0)
	// Materialize the append/ok histogram without recording a synthetic observation.
	_ = m.effectItems.WithLabelValues("append", "ok")

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
		m.postCommitHandoff,
		m.postCommitHandoffCap,
		m.postCommitRetryQueue,
		m.postCommitRetryContended,
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
func (m *ChannelAppendMetrics) ObserveRouter(path, result string, items int, dur time.Duration) {
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
func (m *ChannelAppendMetrics) ObserveLocalAdmission(result string, items int) {
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
func (m *ChannelAppendMetrics) SetWriterPressure(admissionDepth int, admissionCapacity int, workerRunning int, workerCapacity int, pendingAppendItems int, appendInflightItems int, postCommitBacklog int, postCommitHandoffDepth int, postCommitHandoffCapacity int, postCommitRetryQueueDepth int, postCommitRetryContended bool) {
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
	m.postCommitHandoff.WithLabelValues().Set(float64(postCommitHandoffDepth))
	m.postCommitHandoffCap.WithLabelValues().Set(float64(postCommitHandoffCapacity))
	m.postCommitRetryQueue.WithLabelValues().Set(float64(postCommitRetryQueueDepth))
	if postCommitRetryContended {
		m.postCommitRetryContended.WithLabelValues().Set(1)
		return
	}
	m.postCommitRetryContended.WithLabelValues().Set(0)
}

// ObserveEffectPool records effect pool admission and pressure.
func (m *ChannelAppendMetrics) ObserveEffectPool(stage, result string, inflight int, capacity int, saturated bool) {
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

// ObserveEffect records one asynchronous channel append effect.
func (m *ChannelAppendMetrics) ObserveEffect(stage, result string, items int, dur time.Duration) {
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
