package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var channelV2AppendBatchRecordBuckets = []float64{1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024}
var channelV2AppendBatchByteBuckets = []float64{64, 256, 1024, 4096, 16384, 65536, 262144, 524288, 1048576, 4194304}
var channelV2DurationBuckets = []float64{0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5}

type ChannelV2Metrics struct {
	reactorMailboxDepth *prometheus.GaugeVec
	workerQueueDepth    *prometheus.GaugeVec
	appendBatchRecords  prometheus.Histogram
	appendBatchBytes    prometheus.Histogram
	appendBatchWait     prometheus.Histogram
	appendDuration      *prometheus.HistogramVec
	workerTaskDuration  *prometheus.HistogramVec
}

func newChannelV2Metrics(registry prometheus.Registerer, labels prometheus.Labels) *ChannelV2Metrics {
	m := &ChannelV2Metrics{
		reactorMailboxDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelv2_reactor_mailbox_depth",
			Help:        "Number of pending events in each ChannelV2 reactor mailbox.",
			ConstLabels: labels,
		}, []string{"reactor_id", "priority"}),
		workerQueueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_channelv2_worker_queue_depth",
			Help:        "Number of pending tasks in each ChannelV2 worker pool.",
			ConstLabels: labels,
		}, []string{"pool"}),
		appendBatchRecords: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "wukongim_channelv2_append_batch_records",
			Help:        "Number of records collected into each ChannelV2 append batch.",
			ConstLabels: labels,
			Buckets:     channelV2AppendBatchRecordBuckets,
		}),
		appendBatchBytes: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "wukongim_channelv2_append_batch_bytes",
			Help:        "Payload bytes collected into each ChannelV2 append batch.",
			ConstLabels: labels,
			Buckets:     channelV2AppendBatchByteBuckets,
		}),
		appendBatchWait: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "wukongim_channelv2_append_batch_wait_duration_seconds",
			Help:        "Elapsed time from the first queued ChannelV2 append request to append batch flush.",
			ConstLabels: labels,
			Buckets:     channelV2DurationBuckets,
		}),
		appendDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelv2_append_duration_seconds",
			Help:        "ChannelV2 append latency in seconds.",
			ConstLabels: labels,
			Buckets:     channelV2DurationBuckets,
		}, []string{"commit_mode"}),
		workerTaskDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_channelv2_worker_task_duration_seconds",
			Help:        "ChannelV2 worker task latency in seconds.",
			ConstLabels: labels,
			Buckets:     channelV2DurationBuckets,
		}, []string{"kind", "result"}),
	}

	registry.MustRegister(
		m.reactorMailboxDepth,
		m.workerQueueDepth,
		m.appendBatchRecords,
		m.appendBatchBytes,
		m.appendBatchWait,
		m.appendDuration,
		m.workerTaskDuration,
	)

	return m
}

func (m *ChannelV2Metrics) SetReactorMailboxDepth(reactorID int, priority string, depth int) {
	if m == nil {
		return
	}
	m.reactorMailboxDepth.WithLabelValues(strconv.Itoa(reactorID), priority).Set(float64(depth))
}

func (m *ChannelV2Metrics) SetWorkerQueueDepth(pool string, depth int) {
	if m == nil {
		return
	}
	m.workerQueueDepth.WithLabelValues(pool).Set(float64(depth))
}

func (m *ChannelV2Metrics) ObserveAppendBatch(records int, bytes int, wait time.Duration) {
	if m == nil {
		return
	}
	m.appendBatchRecords.Observe(float64(records))
	m.appendBatchBytes.Observe(float64(bytes))
	m.appendBatchWait.Observe(wait.Seconds())
}

func (m *ChannelV2Metrics) ObserveAppendLatency(commitMode string, d time.Duration) {
	if m == nil {
		return
	}
	m.appendDuration.WithLabelValues(commitMode).Observe(d.Seconds())
}

func (m *ChannelV2Metrics) ObserveWorkerResult(kind string, result string, d time.Duration) {
	if m == nil {
		return
	}
	m.workerTaskDuration.WithLabelValues(kind, result).Observe(d.Seconds())
}
