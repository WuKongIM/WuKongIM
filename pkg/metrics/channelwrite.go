package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var channelWriteItemBuckets = []float64{1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024}

// ChannelWriteMetrics exposes internalv2 channel authority write reactor metrics.
type ChannelWriteMetrics struct {
	routerTotal         *prometheus.CounterVec
	routerDuration      *prometheus.HistogramVec
	routerItems         *prometheus.HistogramVec
	localAdmissionTotal *prometheus.CounterVec
	localAdmissionItems *prometheus.HistogramVec
	reactorMailbox      *prometheus.GaugeVec
	reactorMailboxCap   *prometheus.GaugeVec
	reactorEffectSlots  *prometheus.GaugeVec
	reactorEffectCap    *prometheus.GaugeVec
	reactorStateItems   *prometheus.GaugeVec
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
			Help:        "Total local channel authority reactor admission attempts.",
			ConstLabels: labels,
		}, []string{"reactor_id", "result"}),
		localAdmissionItems: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelwrite_local_admission_items",
			Help:        "Number of SEND items in each local channel authority reactor admission attempt.",
			ConstLabels: labels,
			Buckets:     channelWriteItemBuckets,
		}, []string{"reactor_id", "result"}),
		reactorMailbox: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelwrite_reactor_mailbox_depth",
			Help:        "Current internalv2 channel write reactor mailbox depth.",
			ConstLabels: labels,
		}, []string{"reactor_id"}),
		reactorMailboxCap: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelwrite_reactor_mailbox_capacity",
			Help:        "Configured internalv2 channel write reactor mailbox capacity.",
			ConstLabels: labels,
		}, []string{"reactor_id"}),
		reactorEffectSlots: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelwrite_reactor_effect_slots",
			Help:        "Current accepted prepare/append/post-commit slots in each channel write reactor.",
			ConstLabels: labels,
		}, []string{"reactor_id"}),
		reactorEffectCap: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelwrite_reactor_effect_slots_capacity",
			Help:        "Configured accepted effect slot capacity for each channel write reactor.",
			ConstLabels: labels,
		}, []string{"reactor_id"}),
		reactorStateItems: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelwrite_reactor_state_items",
			Help:        "Current channel write state item counts by reactor and kind.",
			ConstLabels: labels,
		}, []string{"reactor_id", "kind"}),
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
		m.reactorMailbox,
		m.reactorMailboxCap,
		m.reactorEffectSlots,
		m.reactorEffectCap,
		m.reactorStateItems,
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

// ObserveLocalAdmission records one local reactor admission attempt.
func (m *ChannelWriteMetrics) ObserveLocalAdmission(reactorID int, result string, items int) {
	if m == nil {
		return
	}
	if items < 0 {
		items = 0
	}
	id := strconv.Itoa(reactorID)
	m.localAdmissionTotal.WithLabelValues(id, result).Inc()
	m.localAdmissionItems.WithLabelValues(id, result).Observe(float64(items))
}

// SetReactorPressure sets current local reactor pressure gauges.
func (m *ChannelWriteMetrics) SetReactorPressure(reactorID int, mailboxDepth int, mailboxCapacity int, effectSlots int, effectSlotsCapacity int, pendingAppendItems int, appendInflightItems int, postCommitBacklog int) {
	if m == nil {
		return
	}
	id := strconv.Itoa(reactorID)
	m.reactorMailbox.WithLabelValues(id).Set(float64(mailboxDepth))
	m.reactorMailboxCap.WithLabelValues(id).Set(float64(mailboxCapacity))
	m.reactorEffectSlots.WithLabelValues(id).Set(float64(effectSlots))
	m.reactorEffectCap.WithLabelValues(id).Set(float64(effectSlotsCapacity))
	m.reactorStateItems.WithLabelValues(id, "pending_append").Set(float64(pendingAppendItems))
	m.reactorStateItems.WithLabelValues(id, "append_inflight").Set(float64(appendInflightItems))
	m.reactorStateItems.WithLabelValues(id, "post_commit_backlog").Set(float64(postCommitBacklog))
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
