package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var channelRuntimeAppendBatchRecordBuckets = []float64{1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024}
var channelRuntimeAppendBatchByteBuckets = []float64{64, 256, 1024, 4096, 16384, 65536, 262144, 524288, 1048576, 4194304}
var channelRuntimeDurationBuckets = []float64{0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5}
var channelRuntimeISRAnomalyReasons = []string{"isr_insufficient", "no_leader", "replica_gap"}

type ChannelRuntimeMetrics struct {
	reactorMailboxDepth      *prometheus.GaugeVec
	workerQueueDepth         *prometheus.GaugeVec
	workerInflight           *prometheus.GaugeVec
	workerInflightPeak       *prometheus.GaugeVec
	activeRuntimes           *prometheus.GaugeVec
	activationRejectedTotal  *prometheus.CounterVec
	followerParked           *prometheus.GaugeVec
	recoveryProbeTotal       *prometheus.CounterVec
	pullTotal                *prometheus.CounterVec
	pullHintTotal            *prometheus.CounterVec
	pullHintReceiveTotal     *prometheus.CounterVec
	pendingMetaCurrent       *prometheus.GaugeVec
	pendingMetaTotal         *prometheus.CounterVec
	needMetaPullTotal        *prometheus.CounterVec
	metaCacheTotal           *prometheus.CounterVec
	isrAnomalyChannels       *prometheus.GaugeVec
	appendBatchRecords       prometheus.Histogram
	appendBatchBytes         prometheus.Histogram
	appendBatchWait          prometheus.Histogram
	appendDuration           *prometheus.HistogramVec
	appendStageDuration      *prometheus.HistogramVec
	appendWaitStageDuration  *prometheus.HistogramVec
	replicationStageDuration *prometheus.HistogramVec
	workerTaskDuration       *prometheus.HistogramVec
	workerTaskErrorTotal     *prometheus.CounterVec
	workerBatchItems         *prometheus.HistogramVec
	rpcPullTotal             *prometheus.CounterVec
}

// ChannelV2Metrics is a compatibility alias for the promoted Channel runtime metrics type.
type ChannelV2Metrics = ChannelRuntimeMetrics

func newChannelRuntimeMetrics(registry prometheus.Registerer, labels prometheus.Labels) *ChannelRuntimeMetrics {
	m := &ChannelRuntimeMetrics{
		reactorMailboxDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelv2_reactor_mailbox_depth",
			Help:        "Number of pending events in each Channel runtime reactor mailbox.",
			ConstLabels: labels,
		}, []string{"reactor_id", "priority"}),
		workerQueueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelv2_worker_queue_depth",
			Help:        "Number of pending tasks in each Channel runtime worker pool.",
			ConstLabels: labels,
		}, []string{"pool"}),
		workerInflight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelv2_worker_inflight",
			Help:        "Number of currently running tasks in each Channel runtime worker pool.",
			ConstLabels: labels,
		}, []string{"pool"}),
		workerInflightPeak: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelv2_worker_inflight_peak",
			Help:        "Peak number of concurrently running tasks observed in each Channel runtime worker pool since process start.",
			ConstLabels: labels,
		}, []string{"pool"}),
		activeRuntimes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelv2_active_runtimes",
			Help:        "Number of active Channel runtimes by reactor and local role.",
			ConstLabels: labels,
		}, []string{"reactor_id", "role"}),
		activationRejectedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelv2_activation_rejected_total",
			Help:        "Total Channel runtime activation rejections by reason.",
			ConstLabels: labels,
		}, []string{"reason"}),
		followerParked: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelv2_follower_parked",
			Help:        "Number of parked follower Channel runtimes by reactor.",
			ConstLabels: labels,
		}, []string{"reactor_id"}),
		recoveryProbeTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelv2_recovery_probe_total",
			Help:        "Total Channel runtime follower recovery probes by result.",
			ConstLabels: labels,
		}, []string{"result"}),
		pullTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelv2_pull_total",
			Help:        "Total Channel runtime follower pulls by result and empty response status.",
			ConstLabels: labels,
		}, []string{"result", "empty"}),
		pullHintTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelv2_pull_hint_total",
			Help:        "Total Channel runtime pull hints by reason, result, and low-cardinality error class.",
			ConstLabels: labels,
		}, []string{"reason", "result", "error"}),
		pullHintReceiveTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelv2_pull_hint_receive_total",
			Help:        "Total Channel runtime received pull hints by reason, receive stage, result, and low-cardinality error class.",
			ConstLabels: labels,
		}, []string{"reason", "stage", "result", "error"}),
		pendingMetaCurrent: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelv2_pending_meta_current",
			Help:        "Number of Channel runtime follower PendingMeta shells by reactor.",
			ConstLabels: labels,
		}, []string{"reactor_id"}),
		pendingMetaTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelv2_pending_meta_total",
			Help:        "Total Channel runtime follower PendingMeta lifecycle events by event and low-cardinality error class.",
			ConstLabels: labels,
		}, []string{"event", "error"}),
		needMetaPullTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelv2_need_meta_pull_total",
			Help:        "Total Channel runtime follower NeedMeta pull attempts by result and low-cardinality error class.",
			ConstLabels: labels,
		}, []string{"result", "error"}),
		metaCacheTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelv2_meta_cache_total",
			Help:        "Total Channel runtime metadata cache events by result.",
			ConstLabels: labels,
		}, []string{"result"}),
		isrAnomalyChannels: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelv2_isr_anomaly_channels",
			Help:        "Current count of Channel runtime metadata ISR anomalies by low-cardinality reason.",
			ConstLabels: labels,
		}, []string{"reason"}),
		appendBatchRecords: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "wukongim_channelv2_append_batch_records",
			Help:        "Number of records collected into each Channel runtime append batch.",
			ConstLabels: labels,
			Buckets:     channelRuntimeAppendBatchRecordBuckets,
		}),
		appendBatchBytes: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "wukongim_channelv2_append_batch_bytes",
			Help:        "Payload bytes collected into each Channel runtime append batch.",
			ConstLabels: labels,
			Buckets:     channelRuntimeAppendBatchByteBuckets,
		}),
		appendBatchWait: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "wukongim_channelv2_append_batch_wait_duration_seconds",
			Help:        "Elapsed time from the first queued Channel runtime append request to append batch flush.",
			ConstLabels: labels,
			Buckets:     channelRuntimeDurationBuckets,
		}),
		appendDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelv2_append_duration_seconds",
			Help:        "Channel runtime append latency in seconds.",
			ConstLabels: labels,
			Buckets:     channelRuntimeDurationBuckets,
		}, []string{"commit_mode"}),
		appendStageDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelv2_append_stage_duration_seconds",
			Help:        "Channel runtime client append stage latency in seconds.",
			ConstLabels: labels,
			Buckets:     channelRuntimeDurationBuckets,
		}, []string{"stage", "result"}),
		appendWaitStageDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelv2_append_wait_stage_duration_seconds",
			Help:        "Channel runtime admitted append future wait sub-stage latency in seconds.",
			ConstLabels: labels,
			Buckets:     channelRuntimeDurationBuckets,
		}, []string{"stage", "commit_mode", "result"}),
		replicationStageDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelv2_replication_stage_duration_seconds",
			Help:        "Channel runtime follower replication stage latency in seconds.",
			ConstLabels: labels,
			Buckets:     channelRuntimeDurationBuckets,
		}, []string{"stage", "result"}),
		workerTaskDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelv2_worker_task_duration_seconds",
			Help:        "Channel runtime worker task latency in seconds.",
			ConstLabels: labels,
			Buckets:     channelRuntimeDurationBuckets,
		}, []string{"kind", "result"}),
		workerTaskErrorTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelv2_worker_task_error_total",
			Help:        "Total Channel runtime worker task errors by kind and low-cardinality error class.",
			ConstLabels: labels,
		}, []string{"kind", "error"}),
		workerBatchItems: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelv2_worker_batch_items",
			Help:        "Number of logical worker tasks coalesced into each Channel runtime worker-side batch.",
			ConstLabels: labels,
			Buckets:     channelRuntimeAppendBatchRecordBuckets,
		}, []string{"kind", "result"}),
		rpcPullTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelv2_rpc_pull_total",
			Help:        "Total Channel runtime follower RPC pull tasks by result.",
			ConstLabels: labels,
		}, []string{"result"}),
	}

	registry.MustRegister(
		m.reactorMailboxDepth,
		m.workerQueueDepth,
		m.workerInflight,
		m.workerInflightPeak,
		m.activeRuntimes,
		m.activationRejectedTotal,
		m.followerParked,
		m.recoveryProbeTotal,
		m.pullTotal,
		m.pullHintTotal,
		m.pullHintReceiveTotal,
		m.pendingMetaCurrent,
		m.pendingMetaTotal,
		m.needMetaPullTotal,
		m.metaCacheTotal,
		m.isrAnomalyChannels,
		m.appendBatchRecords,
		m.appendBatchBytes,
		m.appendBatchWait,
		m.appendDuration,
		m.appendStageDuration,
		m.appendWaitStageDuration,
		m.replicationStageDuration,
		m.workerTaskDuration,
		m.workerTaskErrorTotal,
		m.workerBatchItems,
		m.rpcPullTotal,
	)

	return m
}

func (m *ChannelRuntimeMetrics) SetReactorMailboxDepth(reactorID int, priority string, depth int) {
	if m == nil {
		return
	}
	m.reactorMailboxDepth.WithLabelValues(strconv.Itoa(reactorID), priority).Set(float64(depth))
}

func (m *ChannelRuntimeMetrics) SetWorkerQueueDepth(pool string, depth int) {
	if m == nil {
		return
	}
	m.workerQueueDepth.WithLabelValues(pool).Set(float64(depth))
}

func (m *ChannelRuntimeMetrics) SetWorkerInflight(pool string, inflight int) {
	if m == nil {
		return
	}
	m.workerInflight.WithLabelValues(pool).Set(float64(inflight))
}

func (m *ChannelRuntimeMetrics) SetWorkerInflightPeak(pool string, peak int) {
	if m == nil {
		return
	}
	m.workerInflightPeak.WithLabelValues(pool).Set(float64(peak))
}

func (m *ChannelRuntimeMetrics) SetChannelRuntimeCount(reactorID int, role string, count int) {
	if m == nil {
		return
	}
	m.activeRuntimes.WithLabelValues(strconv.Itoa(reactorID), role).Set(float64(count))
}

func (m *ChannelRuntimeMetrics) ObserveChannelActivationRejected(reason string) {
	if m == nil {
		return
	}
	m.activationRejectedTotal.WithLabelValues(reason).Inc()
}

func (m *ChannelRuntimeMetrics) SetFollowerParkedCount(reactorID int, count int) {
	if m == nil {
		return
	}
	m.followerParked.WithLabelValues(strconv.Itoa(reactorID)).Set(float64(count))
}

func (m *ChannelRuntimeMetrics) ObserveFollowerRecoveryProbe(result string) {
	if m == nil {
		return
	}
	m.recoveryProbeTotal.WithLabelValues(result).Inc()
}

func (m *ChannelRuntimeMetrics) ObservePull(result string, empty bool) {
	if m == nil {
		return
	}
	m.pullTotal.WithLabelValues(result, strconv.FormatBool(empty)).Inc()
}

func (m *ChannelRuntimeMetrics) ObservePullHint(reason string, result string, errorClass string) {
	if m == nil {
		return
	}
	m.pullHintTotal.WithLabelValues(reason, result, errorClass).Inc()
}

func (m *ChannelRuntimeMetrics) ObservePullHintReceived(reason string, stage string, result string, errorClass string) {
	if m == nil {
		return
	}
	m.pullHintReceiveTotal.WithLabelValues(reason, stage, result, errorClass).Inc()
}

func (m *ChannelRuntimeMetrics) SetPendingMetaCount(reactorID int, count int) {
	if m == nil {
		return
	}
	m.pendingMetaCurrent.WithLabelValues(strconv.Itoa(reactorID)).Set(float64(count))
}

func (m *ChannelRuntimeMetrics) ObservePendingMeta(event string, errorClass string) {
	if m == nil {
		return
	}
	m.pendingMetaTotal.WithLabelValues(event, errorClass).Inc()
}

func (m *ChannelRuntimeMetrics) ObserveNeedMetaPull(result string, errorClass string) {
	if m == nil {
		return
	}
	m.needMetaPullTotal.WithLabelValues(result, errorClass).Inc()
}

func (m *ChannelRuntimeMetrics) ObserveMetaCache(result string) {
	if m == nil {
		return
	}
	m.metaCacheTotal.WithLabelValues(result).Inc()
}

// SetISRAnomalyChannels records bounded Channel runtime ISR anomaly counts by reason.
func (m *ChannelRuntimeMetrics) SetISRAnomalyChannels(counts map[string]int) {
	if m == nil {
		return
	}
	for _, reason := range channelRuntimeISRAnomalyReasons {
		m.isrAnomalyChannels.WithLabelValues(reason).Set(float64(counts[reason]))
	}
}

func (m *ChannelRuntimeMetrics) ObserveAppendBatch(records int, bytes int, wait time.Duration) {
	if m == nil {
		return
	}
	m.appendBatchRecords.Observe(float64(records))
	m.appendBatchBytes.Observe(float64(bytes))
	m.appendBatchWait.Observe(wait.Seconds())
}

func (m *ChannelRuntimeMetrics) ObserveAppendLatency(commitMode string, d time.Duration) {
	if m == nil {
		return
	}
	m.appendDuration.WithLabelValues(commitMode).Observe(d.Seconds())
}

func (m *ChannelRuntimeMetrics) ObserveAppendStage(stage string, result string, d time.Duration) {
	if m == nil {
		return
	}
	m.appendStageDuration.WithLabelValues(stage, result).Observe(d.Seconds())
}

func (m *ChannelRuntimeMetrics) ObserveAppendWaitStage(stage string, commitMode string, result string, d time.Duration) {
	if m == nil {
		return
	}
	m.appendWaitStageDuration.WithLabelValues(stage, commitMode, result).Observe(d.Seconds())
}

func (m *ChannelRuntimeMetrics) ObserveReplicationStage(stage string, result string, d time.Duration) {
	if m == nil {
		return
	}
	m.replicationStageDuration.WithLabelValues(stage, result).Observe(d.Seconds())
}

func (m *ChannelRuntimeMetrics) ObserveWorkerResult(kind string, result string, d time.Duration, errorClass ...string) {
	if m == nil {
		return
	}
	m.workerTaskDuration.WithLabelValues(kind, result).Observe(d.Seconds())
	if result == "err" {
		class := "other"
		if len(errorClass) > 0 && errorClass[0] != "" {
			class = errorClass[0]
		}
		m.workerTaskErrorTotal.WithLabelValues(kind, class).Inc()
	}
	if kind == "rpc_pull" {
		m.rpcPullTotal.WithLabelValues(result).Inc()
	}
}

func (m *ChannelRuntimeMetrics) ObserveWorkerBatch(kind string, result string, items int) {
	if m == nil || items <= 0 {
		return
	}
	m.workerBatchItems.WithLabelValues(kind, result).Observe(float64(items))
}
