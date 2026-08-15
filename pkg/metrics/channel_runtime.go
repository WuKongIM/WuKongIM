package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var channelRuntimeAppendBatchRecordBuckets = []float64{1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024}
var channelRuntimeWaiterBuckets = []float64{0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024}
var channelRuntimeAppendBatchByteBuckets = []float64{64, 256, 1024, 4096, 16384, 65536, 262144, 524288, 1048576, 4194304}
var channelRuntimeDurationBuckets = []float64{0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5}
var channelRuntimeISRAnomalyReasons = []string{"isr_insufficient", "no_leader", "replica_gap"}
var channelRuntimeMetaCreateResults = []string{"created", "already_existing", "error"}
var channelRuntimeMetaCreateBatchResults = []string{"ok", "recovered", "error"}

const maxMaterializedLogicalSlotGroups uint32 = 256

// ChannelRuntimeMetrics keeps legacy collectors and exposes promoted names through Registry gather aliases.
type ChannelRuntimeMetrics struct {
	reactorMailboxDepth      *prometheus.GaugeVec
	workerQueueDepth         *prometheus.GaugeVec
	workerQueueCapacity      *prometheus.GaugeVec
	workerInflight           *prometheus.GaugeVec
	workerInflightPeak       *prometheus.GaugeVec
	activeRuntimes           *prometheus.GaugeVec
	activationRejectedTotal  *prometheus.CounterVec
	followerParked           *prometheus.GaugeVec
	recoveryProbeTotal       *prometheus.CounterVec
	pullTotal                *prometheus.CounterVec
	pullBatchItems           *prometheus.HistogramVec
	pullBatchRecords         *prometheus.HistogramVec
	pullBatchPayloadBytes    *prometheus.HistogramVec
	pullBatchDuration        *prometheus.HistogramVec
	leaderPullStageDuration  *prometheus.HistogramVec
	leaderPullWaiters        prometheus.Histogram
	pullHintTotal            *prometheus.CounterVec
	pullHintReceiveTotal     *prometheus.CounterVec
	pendingMetaCurrent       *prometheus.GaugeVec
	pendingMetaTotal         *prometheus.CounterVec
	needMetaPullTotal        *prometheus.CounterVec
	metaCacheTotal           *prometheus.CounterVec
	metaCreatedTotal         *prometheus.CounterVec
	metaCreateQueueDepth     *prometheus.GaugeVec
	metaCreateCoalescedTotal *prometheus.CounterVec
	metaCreateBatchTotal     *prometheus.CounterVec
	metaCreateBatchItems     *prometheus.HistogramVec
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
	workerAdmissionTotal     *prometheus.CounterVec
	workerBatchItems         *prometheus.HistogramVec
	rpcPullTotal             *prometheus.CounterVec
}

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
		workerQueueCapacity: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelv2_worker_queue_capacity",
			Help:        "Configured bounded task capacity in each Channel runtime worker pool.",
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
		pullBatchItems: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelv2_pull_batch_items",
			Help:        "Number of logical pull requests in each leader-side PullBatch service call.",
			ConstLabels: labels,
			Buckets:     channelRuntimeAppendBatchRecordBuckets,
		}, []string{"result"}),
		pullBatchRecords: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelv2_pull_batch_records",
			Help:        "Number of records returned by each leader-side PullBatch service call.",
			ConstLabels: labels,
			Buckets:     channelRuntimeAppendBatchRecordBuckets,
		}, []string{"result"}),
		pullBatchPayloadBytes: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelv2_pull_batch_payload_bytes",
			Help:        "Logical record bytes used by pull budgets and returned by each leader-side PullBatch service call.",
			ConstLabels: labels,
			Buckets:     channelRuntimeAppendBatchByteBuckets,
		}, []string{"result"}),
		pullBatchDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelv2_pull_batch_duration_seconds",
			Help:        "Leader-side PullBatch service latency by bounded stage.",
			ConstLabels: labels,
			Buckets:     channelRuntimeDurationBuckets,
		}, []string{"stage", "result"}),
		leaderPullStageDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelv2_leader_pull_stage_duration_seconds",
			Help:        "Sampled leader-side Pull synchronous latency by bounded handler stage.",
			ConstLabels: labels,
			Buckets:     channelRuntimeDurationBuckets,
		}, []string{"stage"}),
		leaderPullWaiters: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "wukongim_channelv2_leader_pull_completed_waiters",
			Help:        "Number of append waiters completed by one sampled leader-side Pull AckOffset application.",
			ConstLabels: labels,
			Buckets:     channelRuntimeWaiterBuckets,
		}),
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
		metaCreatedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wukongim_channelv2_meta_created_total",
			Help: "Total authoritative initial Channel runtime metadata create outcomes by logical Slot Raft Group.",
		}, []string{"slot_id", "result"}),
		metaCreateQueueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "wukongim_channelv2_meta_create_queue_depth",
			Help: "Current unique Channel runtime metadata creates queued behind the active batch by logical Slot Raft Group.",
		}, []string{"slot_id"}),
		metaCreateCoalescedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wukongim_channelv2_meta_create_coalesced_total",
			Help: "Total duplicate initial metadata create waiters coalesced onto an existing logical create by Slot Raft Group.",
		}, []string{"slot_id"}),
		metaCreateBatchTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wukongim_channelv2_meta_create_batch_total",
			Help: "Total bounded initial metadata create batches by logical Slot Raft Group and closed result.",
		}, []string{"slot_id", "result"}),
		metaCreateBatchItems: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "wukongim_channelv2_meta_create_batch_items",
			Help:    "Number of unique initial metadata creates submitted in each Slot-owned batch.",
			Buckets: channelRuntimeAppendBatchRecordBuckets,
		}, []string{"slot_id", "result"}),
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
		workerAdmissionTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channelv2_worker_admission_total",
			Help:        "Total Channel runtime worker admission outcomes by pool and bounded task kind.",
			ConstLabels: labels,
		}, []string{"pool", "kind", "result"}),
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

	// CounterVec collectors do not emit a family until at least one bounded
	// label tuple exists. Materialize true zeroes for clean-cluster observation
	// without recording an event. NewWithLogicalSlots extends this first group
	// to the complete configured topology.
	_ = m.activationRejectedTotal.WithLabelValues("max_channels")
	m.materializeMetaCreateSlots(1)

	registry.MustRegister(
		m.reactorMailboxDepth,
		m.workerQueueDepth,
		m.workerQueueCapacity,
		m.workerInflight,
		m.workerInflightPeak,
		m.activeRuntimes,
		m.activationRejectedTotal,
		m.followerParked,
		m.recoveryProbeTotal,
		m.pullTotal,
		m.pullBatchItems,
		m.pullBatchRecords,
		m.pullBatchPayloadBytes,
		m.pullBatchDuration,
		m.leaderPullStageDuration,
		m.leaderPullWaiters,
		m.pullHintTotal,
		m.pullHintReceiveTotal,
		m.pendingMetaCurrent,
		m.pendingMetaTotal,
		m.needMetaPullTotal,
		m.metaCacheTotal,
		m.metaCreatedTotal,
		m.metaCreateQueueDepth,
		m.metaCreateCoalescedTotal,
		m.metaCreateBatchTotal,
		m.metaCreateBatchItems,
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
		m.workerAdmissionTotal,
		m.workerBatchItems,
		m.rpcPullTotal,
	)

	return m
}

func (m *ChannelRuntimeMetrics) materializeMetaCreateSlots(count uint32) {
	if m == nil {
		return
	}
	if count == 0 {
		count = 1
	}
	if count > maxMaterializedLogicalSlotGroups {
		count = maxMaterializedLogicalSlotGroups
	}
	for slotID := uint32(1); slotID <= count; slotID++ {
		for _, result := range channelRuntimeMetaCreateResults {
			_ = m.metaCreatedTotal.WithLabelValues(strconv.FormatUint(uint64(slotID), 10), result)
		}
	}
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

// SetWorkerQueueCapacity publishes the configured bound for one closed worker pool label.
func (m *ChannelRuntimeMetrics) SetWorkerQueueCapacity(pool string, capacity int) {
	if m == nil {
		return
	}
	m.workerQueueCapacity.WithLabelValues(pool).Set(float64(capacity))
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

// ObservePullBatch records one leader-side PullBatch service observation.
func (m *ChannelRuntimeMetrics) ObservePullBatch(result string, items int, records int, payloadBytes int, submit time.Duration, await time.Duration, maxSequentialAwait time.Duration, total time.Duration) {
	if m == nil {
		return
	}
	m.pullBatchItems.WithLabelValues(result).Observe(float64(items))
	m.pullBatchRecords.WithLabelValues(result).Observe(float64(records))
	m.pullBatchPayloadBytes.WithLabelValues(result).Observe(float64(payloadBytes))
	m.pullBatchDuration.WithLabelValues("submit", result).Observe(submit.Seconds())
	m.pullBatchDuration.WithLabelValues("await", result).Observe(await.Seconds())
	m.pullBatchDuration.WithLabelValues("max_sequential_await", result).Observe(maxSequentialAwait.Seconds())
	m.pullBatchDuration.WithLabelValues("total", result).Observe(total.Seconds())
}

// ObserveLeaderPullStage records one bounded leader-side Pull handler stage.
func (m *ChannelRuntimeMetrics) ObserveLeaderPullStage(stage string, d time.Duration) {
	if m == nil {
		return
	}
	m.leaderPullStageDuration.WithLabelValues(stage).Observe(d.Seconds())
}

// ObserveLeaderPullCompletedWaiters records the append waiters released by one AckOffset application.
func (m *ChannelRuntimeMetrics) ObserveLeaderPullCompletedWaiters(count int) {
	if m == nil {
		return
	}
	if count < 0 {
		count = 0
	}
	m.leaderPullWaiters.Observe(float64(count))
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

// ObserveMetaCreate records one authoritative initial metadata create outcome.
func (m *ChannelRuntimeMetrics) ObserveMetaCreate(slotID uint32, result string) {
	if m == nil {
		return
	}
	m.metaCreatedTotal.WithLabelValues(strconv.FormatUint(uint64(slotID), 10), normalizeMetaCreateResult(result)).Inc()
}

// SetMetaCreateQueueDepth publishes the current bounded unique queue depth.
func (m *ChannelRuntimeMetrics) SetMetaCreateQueueDepth(slotID uint32, depth int) {
	if m == nil {
		return
	}
	if depth < 0 {
		depth = 0
	}
	m.metaCreateQueueDepth.WithLabelValues(strconv.FormatUint(uint64(slotID), 10)).Set(float64(depth))
}

// ObserveMetaCreateCoalesced records one duplicate waiter joined to existing work.
func (m *ChannelRuntimeMetrics) ObserveMetaCreateCoalesced(slotID uint32) {
	if m == nil {
		return
	}
	m.metaCreateCoalescedTotal.WithLabelValues(strconv.FormatUint(uint64(slotID), 10)).Inc()
}

// ObserveMetaCreateBatch records one bounded physical batch and its logical size.
func (m *ChannelRuntimeMetrics) ObserveMetaCreateBatch(slotID uint32, result string, items int) {
	if m == nil {
		return
	}
	result = normalizeMetaCreateBatchResult(result)
	if items < 0 {
		items = 0
	}
	slot := strconv.FormatUint(uint64(slotID), 10)
	m.metaCreateBatchTotal.WithLabelValues(slot, result).Inc()
	m.metaCreateBatchItems.WithLabelValues(slot, result).Observe(float64(items))
}

func normalizeMetaCreateBatchResult(result string) string {
	for _, allowed := range channelRuntimeMetaCreateBatchResults {
		if result == allowed {
			return result
		}
	}
	return "error"
}

func normalizeMetaCreateResult(result string) string {
	switch result {
	case "created", "already_existing", "error":
		return result
	default:
		return "error"
	}
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

func (m *ChannelRuntimeMetrics) ObserveWorkerAdmission(pool string, kind string, result string) {
	if m == nil {
		return
	}
	m.workerAdmissionTotal.WithLabelValues(pool, kind, result).Inc()
}

func (m *ChannelRuntimeMetrics) ObserveWorkerBatch(kind string, result string, items int) {
	if m == nil || items <= 0 {
		return
	}
	m.workerBatchItems.WithLabelValues(kind, result).Observe(float64(items))
}
