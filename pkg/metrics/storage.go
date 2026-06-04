package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var storageStores = []string{
	"meta",
	"raft",
	"channel_log",
	"controller_meta",
	"controller_raft",
}

var storageCommitRequestDurationBuckets = []float64{0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 15, 30}

type StorageMetrics struct {
	diskUsageBytes        *prometheus.GaugeVec
	commitQueueDepth      *prometheus.GaugeVec
	commitBatchRecords    *prometheus.HistogramVec
	commitBatchBytes      *prometheus.HistogramVec
	commitBatchRequests   *prometheus.HistogramVec
	commitBatchDuration   *prometheus.HistogramVec
	commitRequestDuration *prometheus.HistogramVec
}

// StorageCommitBatchObservation describes one grouped storage commit attempt.
type StorageCommitBatchObservation struct {
	Requests        int
	Records         int
	Bytes           int
	CollectDuration time.Duration
	BuildDuration   time.Duration
	CommitDuration  time.Duration
	PublishDuration time.Duration
	TotalDuration   time.Duration
}

// StorageCommitRequestObservation describes one logical storage commit request wait.
type StorageCommitRequestObservation struct {
	Records  int
	Bytes    int
	Duration time.Duration
}

func newStorageMetrics(registry prometheus.Registerer, labels prometheus.Labels) *StorageMetrics {
	m := &StorageMetrics{
		diskUsageBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_disk_usage_bytes",
			Help:        "Disk usage by storage subsystem in bytes.",
			ConstLabels: labels,
		}, []string{"store"}),
		commitQueueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_commit_queue_depth",
			Help:        "Number of logical requests waiting in a grouped storage commit queue.",
			ConstLabels: labels,
		}, []string{"store"}),
		commitBatchRequests: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_storage_commit_batch_requests",
			Help:        "Number of logical requests collected into each grouped storage commit.",
			ConstLabels: labels,
			Buckets:     gatewayAsyncSendBatchRecordBuckets,
		}, []string{"store"}),
		commitBatchRecords: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_storage_commit_batch_records",
			Help:        "Number of logical records collected into each grouped storage commit.",
			ConstLabels: labels,
			Buckets:     gatewayAsyncSendBatchRecordBuckets,
		}, []string{"store"}),
		commitBatchBytes: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_storage_commit_batch_bytes",
			Help:        "Approximate payload bytes collected into each grouped storage commit.",
			ConstLabels: labels,
			Buckets:     gatewayAsyncSendBatchByteBuckets,
		}, []string{"store"}),
		commitBatchDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_storage_commit_batch_duration_seconds",
			Help:        "Grouped storage commit stage latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"store", "stage", "result"}),
		commitRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_storage_commit_request_duration_seconds",
			Help:        "Caller-visible logical storage commit request latency in seconds.",
			ConstLabels: labels,
			Buckets:     storageCommitRequestDurationBuckets,
		}, []string{"store", "lane", "result"}),
	}

	registry.MustRegister(
		m.diskUsageBytes,
		m.commitQueueDepth,
		m.commitBatchRequests,
		m.commitBatchRecords,
		m.commitBatchBytes,
		m.commitBatchDuration,
		m.commitRequestDuration,
	)

	for _, store := range storageStores {
		m.diskUsageBytes.WithLabelValues(store).Set(0)
	}

	return m
}

func (m *StorageMetrics) SetDiskUsage(usageByStore map[string]int64) {
	if m == nil {
		return
	}

	known := make(map[string]struct{}, len(storageStores))
	for _, store := range storageStores {
		known[store] = struct{}{}
		m.diskUsageBytes.WithLabelValues(store).Set(float64(usageByStore[store]))
	}
	for store, size := range usageByStore {
		if _, ok := known[store]; ok {
			continue
		}
		m.diskUsageBytes.WithLabelValues(store).Set(float64(size))
	}
}

func (m *StorageMetrics) SetCommitQueueDepth(store string, depth int) {
	if m == nil {
		return
	}
	if store == "" {
		store = "unknown"
	}
	m.commitQueueDepth.WithLabelValues(store).Set(float64(depth))
}

func (m *StorageMetrics) ObserveCommitBatch(store string, result string, obs StorageCommitBatchObservation) {
	if m == nil {
		return
	}
	if store == "" {
		store = "unknown"
	}
	if result == "" {
		result = "unknown"
	}
	m.commitBatchRequests.WithLabelValues(store).Observe(float64(obs.Requests))
	m.commitBatchRecords.WithLabelValues(store).Observe(float64(obs.Records))
	m.commitBatchBytes.WithLabelValues(store).Observe(float64(obs.Bytes))
	m.commitBatchDuration.WithLabelValues(store, "collect", result).Observe(obs.CollectDuration.Seconds())
	m.commitBatchDuration.WithLabelValues(store, "build", result).Observe(obs.BuildDuration.Seconds())
	m.commitBatchDuration.WithLabelValues(store, "commit", result).Observe(obs.CommitDuration.Seconds())
	m.commitBatchDuration.WithLabelValues(store, "publish", result).Observe(obs.PublishDuration.Seconds())
	m.commitBatchDuration.WithLabelValues(store, "total", result).Observe(obs.TotalDuration.Seconds())
}

func (m *StorageMetrics) ObserveCommitRequest(store string, lane string, result string, obs StorageCommitRequestObservation) {
	if m == nil {
		return
	}
	if store == "" {
		store = "unknown"
	}
	if lane == "" {
		lane = "default"
	}
	if result == "" {
		result = "unknown"
	}
	m.commitRequestDuration.WithLabelValues(store, lane, result).Observe(obs.Duration.Seconds())
}
