package metrics

import (
	"sync/atomic"
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

const channelLogStorageStore = "channel_log"

type StorageMetrics struct {
	diskUsageBytes                  *prometheus.GaugeVec
	pebbleDiskUsageBytes            *prometheus.GaugeVec
	pebbleReadAmplification         *prometheus.GaugeVec
	pebbleMemTableSizeBytes         *prometheus.GaugeVec
	pebbleMemTableCount             *prometheus.GaugeVec
	pebbleWALFiles                  *prometheus.GaugeVec
	pebbleWALSizeBytes              *prometheus.GaugeVec
	pebbleWALPhysicalSizeBytes      *prometheus.GaugeVec
	pebbleWALBytesIn                *prometheus.GaugeVec
	pebbleWALBytesWritten           *prometheus.GaugeVec
	pebbleSSTableSizeBytes          *prometheus.GaugeVec
	pebbleFlushBytesWritten         *prometheus.GaugeVec
	pebbleCompactionBytesRead       *prometheus.GaugeVec
	pebbleCompactionBytesWritten    *prometheus.GaugeVec
	pebbleFlushCount                *prometheus.GaugeVec
	pebbleFlushesInProgress         *prometheus.GaugeVec
	pebbleCompactionCount           *prometheus.GaugeVec
	pebbleCompactionEstimatedDebt   *prometheus.GaugeVec
	pebbleCompactionInProgressBytes *prometheus.GaugeVec
	pebbleCompactionsInProgress     *prometheus.GaugeVec
	pebbleWriteStalls               *prometheus.GaugeVec
	pebbleWriteStallSeconds         *prometheus.GaugeVec
	pebbleWriteStallMaxSeconds      *prometheus.GaugeVec
	pebbleWriteStallActive          *prometheus.GaugeVec
	pebbleWALFsyncCount             *prometheus.GaugeVec
	pebbleWALFsyncSeconds           *prometheus.GaugeVec
	pebbleWALFsyncSlow              *prometheus.GaugeVec
	pebbleDiskSlowEvents            *prometheus.GaugeVec
	pebbleDiskSlowMaxSeconds        *prometheus.GaugeVec
	messageExactFreshAppends        *prometheus.GaugeVec
	messagePredecessorCacheHits     *prometheus.GaugeVec
	messagePredecessorValidations   *prometheus.GaugeVec
	messageIdempotencyNegativeSkips *prometheus.GaugeVec
	messageIdempotencyPointReads    *prometheus.GaugeVec
	commitQueueDepth                *prometheus.GaugeVec
	commitBatchRecords              *prometheus.HistogramVec
	commitBatchBytes                *prometheus.HistogramVec
	commitBatchRequests             *prometheus.HistogramVec
	commitBatchDuration             *prometheus.HistogramVec
	commitRequestDuration           *prometheus.HistogramVec
	channelEntrySnapshot            *storageChannelEntryAtomicSnapshot
	channelEntriesActive            prometheus.GaugeFunc
	channelLeasesOutstanding        prometheus.GaugeFunc
	channelBackgroundPins           prometheus.GaugeFunc
	channelAcquiresTotal            prometheus.CounterFunc
	channelReleasesTotal            prometheus.CounterFunc
	channelReclaimsTotal            prometheus.CounterFunc
}

type storageChannelEntryAtomicSnapshot struct {
	activeEntries     atomic.Uint64
	outstandingLeases atomic.Uint64
	backgroundPins    atomic.Uint64
	acquireTotal      atomic.Uint64
	releaseTotal      atomic.Uint64
	reclaimTotal      atomic.Uint64
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

// StoragePebbleObservation describes one Pebble-backed storage engine snapshot.
type StoragePebbleObservation struct {
	// SequencedExactFreshAppends is the number of allocator-proven fresh exact appends.
	SequencedExactFreshAppends uint64
	// DurablePredecessorCacheHits is the number of exact predecessor checks served from memory.
	DurablePredecessorCacheHits uint64
	// DurablePredecessorValidations is the number of complete durable predecessor proofs after cache misses.
	DurablePredecessorValidations uint64
	// IdempotencyNegativeFilterSkips is the number of durable negative point reads avoided.
	IdempotencyNegativeFilterSkips uint64
	// IdempotencyPointReads is the number of possible filter hits verified durably.
	IdempotencyPointReads uint64
	// DiskSpaceUsageBytes is the engine's local disk usage, including live and obsolete files.
	DiskSpaceUsageBytes uint64
	// ReadAmplification is the current LSM read amplification estimate.
	ReadAmplification int
	// MemTableSizeBytes is the bytes allocated by active memtables and flushable batches.
	MemTableSizeBytes uint64
	// MemTableCount is the number of active memtables.
	MemTableCount int64
	// WALFiles is the number of live WAL files.
	WALFiles int64
	// WALSizeBytes is the live logical size of WAL files.
	WALSizeBytes uint64
	// WALPhysicalSizeBytes is the physical on-disk size of WAL files.
	WALPhysicalSizeBytes uint64
	// WALBytesIn is the logical bytes written to the WAL.
	WALBytesIn uint64
	// WALBytesWritten is the physical bytes written to the WAL.
	WALBytesWritten uint64
	// SSTableSizeBytes is the current physical size of live SSTables across all levels.
	SSTableSizeBytes uint64
	// FlushBytesWritten is the cumulative bytes written to SSTables by flushes.
	FlushBytesWritten uint64
	// CompactionBytesRead is the cumulative SSTable bytes read by compactions.
	CompactionBytesRead uint64
	// CompactionBytesWritten is the cumulative SSTable bytes written by compactions.
	CompactionBytesWritten uint64
	// FlushCount is the number of completed flushes since the engine opened.
	FlushCount int64
	// FlushesInProgress is the current number of flushes in progress.
	FlushesInProgress int64
	// CompactionCount is the number of completed compactions since the engine opened.
	CompactionCount int64
	// CompactionEstimatedDebtBytes is Pebble's estimate of bytes that need compaction.
	CompactionEstimatedDebtBytes uint64
	// CompactionInProgressBytes is the bytes being written by in-progress compactions.
	CompactionInProgressBytes int64
	// CompactionsInProgress is the current number of compactions in progress.
	CompactionsInProgress int64
	// WriteStallMemTableCount counts Pebble write stalls caused by the memtable stop-writes threshold.
	WriteStallMemTableCount int64
	// WriteStallL0Count counts Pebble write stalls caused by the L0 stop-writes threshold.
	WriteStallL0Count int64
	// WriteStallOtherCount counts Pebble write stalls with any other reason.
	WriteStallOtherCount int64
	// WriteStallTotalNanos is the cumulative write-stall duration, including an open stall.
	WriteStallTotalNanos int64
	// WriteStallMaxNanos is the longest single write stall, including an open stall.
	WriteStallMaxNanos int64
	// WriteStallActive reports whether writes are stalled at snapshot time.
	WriteStallActive bool
	// WALFsyncCount is the number of WAL fsyncs observed since the store opened.
	WALFsyncCount uint64
	// WALFsyncSumNanos is the cumulative WAL fsync duration.
	WALFsyncSumNanos int64
	// WALFsyncOver100ms counts WAL fsyncs slower than 100ms at histogram bucket resolution.
	WALFsyncOver100ms uint64
	// WALFsyncOver1s counts WAL fsyncs slower than 1s at histogram bucket resolution.
	WALFsyncOver1s uint64
	// WALFsyncOver5s counts WAL fsyncs slower than 5s at histogram bucket resolution.
	WALFsyncOver5s uint64
	// DiskSlowWALEvents counts slow-disk reports on WAL files.
	DiskSlowWALEvents int64
	// DiskSlowWALMaxNanos is the longest reported WAL disk operation.
	DiskSlowWALMaxNanos int64
	// DiskSlowOtherEvents counts slow-disk reports on SST, manifest and other files.
	DiskSlowOtherEvents int64
	// DiskSlowOtherMaxNanos is the longest reported non-WAL disk operation.
	DiskSlowOtherMaxNanos int64
}

// StorageChannelEntryObservation describes aggregate ownership for the channel_log registry.
type StorageChannelEntryObservation struct {
	// ActiveEntries is the number of canonical channel entries currently retained.
	ActiveEntries uint64
	// OutstandingLeases is the number of caller-owned channel store handles.
	OutstandingLeases uint64
	// BackgroundPins is the number of commit-owned channel entry references.
	BackgroundPins uint64
	// AcquireTotal is the cumulative number of successful channel store acquisitions.
	AcquireTotal uint64
	// ReleaseTotal is the cumulative number of terminal channel store releases.
	ReleaseTotal uint64
	// ReclaimTotal is the cumulative number of zero-reference channel entries reclaimed.
	ReclaimTotal uint64
}

func newStorageMetrics(registry prometheus.Registerer, labels prometheus.Labels) *StorageMetrics {
	channelLabels := make(prometheus.Labels, len(labels)+1)
	for name, value := range labels {
		channelLabels[name] = value
	}
	channelLabels["store"] = channelLogStorageStore
	channelEntries := &storageChannelEntryAtomicSnapshot{}
	m := &StorageMetrics{
		diskUsageBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_disk_usage_bytes",
			Help:        "Disk usage by storage subsystem in bytes.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleDiskUsageBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_disk_usage_bytes",
			Help:        "Pebble local disk usage by storage subsystem in bytes.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleReadAmplification: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_read_amplification",
			Help:        "Pebble LSM read amplification by storage subsystem.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleMemTableSizeBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_memtable_size_bytes",
			Help:        "Pebble memtable and flushable batch memory by storage subsystem in bytes.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleMemTableCount: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_memtable_count",
			Help:        "Pebble active memtable count by storage subsystem.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleWALFiles: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_wal_files",
			Help:        "Pebble live WAL file count by storage subsystem.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleWALSizeBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_wal_size_bytes",
			Help:        "Pebble live logical WAL size by storage subsystem in bytes.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleWALPhysicalSizeBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_wal_physical_size_bytes",
			Help:        "Pebble physical WAL size by storage subsystem in bytes.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleWALBytesIn: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_wal_bytes_in",
			Help:        "Pebble logical bytes written to the WAL by storage subsystem.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleWALBytesWritten: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_wal_bytes_written",
			Help:        "Pebble physical bytes written to the WAL by storage subsystem.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleSSTableSizeBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_sstable_size_bytes",
			Help:        "Pebble live SSTable size across all levels by storage subsystem.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleFlushBytesWritten: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_flush_bytes_written",
			Help:        "Pebble cumulative bytes written to SSTables by flushes since engine open.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleCompactionBytesRead: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_compaction_bytes_read",
			Help:        "Pebble cumulative SSTable bytes read by compactions since engine open.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleCompactionBytesWritten: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_compaction_bytes_written",
			Help:        "Pebble cumulative bytes written to SSTables by compactions since engine open.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleFlushCount: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_flush_count",
			Help:        "Pebble completed flush count by storage subsystem since engine open.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleFlushesInProgress: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_flushes_in_progress",
			Help:        "Pebble flushes currently in progress by storage subsystem.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleCompactionCount: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_compaction_count",
			Help:        "Pebble completed compaction count by storage subsystem since engine open.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleCompactionEstimatedDebt: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_compaction_estimated_debt_bytes",
			Help:        "Pebble estimated compaction debt by storage subsystem in bytes.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleCompactionInProgressBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_compaction_in_progress_bytes",
			Help:        "Pebble bytes being written by in-progress compactions by storage subsystem.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleCompactionsInProgress: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_compactions_in_progress",
			Help:        "Pebble compactions currently in progress by storage subsystem.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleWriteStalls: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_write_stalls",
			Help:        "Pebble write stalls since the store opened by storage subsystem and reason (memtable, l0, other).",
			ConstLabels: labels,
		}, []string{"store", "reason"}),
		pebbleWriteStallSeconds: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_write_stall_seconds",
			Help:        "Cumulative Pebble write-stall seconds since the store opened, including an open stall.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleWriteStallMaxSeconds: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_write_stall_max_seconds",
			Help:        "Longest single Pebble write stall since the store opened, including an open stall.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleWriteStallActive: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_write_stall_active",
			Help:        "1 when Pebble writes are stalled at sample time by storage subsystem.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleWALFsyncCount: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_wal_fsync_count",
			Help:        "Pebble WAL fsyncs since the store opened by storage subsystem.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleWALFsyncSeconds: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_wal_fsync_seconds",
			Help:        "Cumulative Pebble WAL fsync seconds since the store opened.",
			ConstLabels: labels,
		}, []string{"store"}),
		pebbleWALFsyncSlow: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_wal_fsync_slow",
			Help:        "Pebble WAL fsyncs slower than threshold (100ms, 1s, 5s) since the store opened, at histogram bucket resolution.",
			ConstLabels: labels,
		}, []string{"store", "threshold"}),
		pebbleDiskSlowEvents: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_disk_slow_events",
			Help:        "Pebble slow-disk reports since the store opened by file class (wal, other); one slow operation may be reported on several health ticks.",
			ConstLabels: labels,
		}, []string{"store", "file"}),
		pebbleDiskSlowMaxSeconds: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_pebble_disk_slow_max_seconds",
			Help:        "Longest reported Pebble slow-disk operation since the store opened by file class (wal, other).",
			ConstLabels: labels,
		}, []string{"store", "file"}),
		messageExactFreshAppends: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_message_exact_fresh_appends",
			Help:        "Allocator-proven fresh exact message appends since the message store opened.",
			ConstLabels: labels,
		}, []string{"store"}),
		messagePredecessorCacheHits: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_message_predecessor_cache_hits",
			Help:        "Exact predecessor validations served from committed canonical or warm state.",
			ConstLabels: labels,
		}, []string{"store"}),
		messagePredecessorValidations: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_message_predecessor_durable_validations",
			Help:        "Complete durable exact predecessor proofs performed after cache misses.",
			ConstLabels: labels,
		}, []string{"store"}),
		messageIdempotencyNegativeSkips: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_message_idempotency_negative_filter_skips",
			Help:        "Durable idempotency negative point reads avoided by the bounded membership filter.",
			ConstLabels: labels,
		}, []string{"store"}),
		messageIdempotencyPointReads: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_storage_message_idempotency_point_reads",
			Help:        "Possible idempotency membership hits verified through durable point reads.",
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
		channelEntrySnapshot: channelEntries,
		channelEntriesActive: prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name:        "wukongim_storage_channel_entries_active",
			Help:        "Canonical channel entries currently retained by the local message store.",
			ConstLabels: channelLabels,
		}, func() float64 {
			return float64(channelEntries.activeEntries.Load())
		}),
		channelLeasesOutstanding: prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name:        "wukongim_storage_channel_leases_outstanding",
			Help:        "Caller-owned channel store leases currently retained by the local message store.",
			ConstLabels: channelLabels,
		}, func() float64 {
			return float64(channelEntries.outstandingLeases.Load())
		}),
		channelBackgroundPins: prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name:        "wukongim_storage_channel_background_pins",
			Help:        "Commit-owned channel entry references currently retained by the local message store.",
			ConstLabels: channelLabels,
		}, func() float64 {
			return float64(channelEntries.backgroundPins.Load())
		}),
		channelAcquiresTotal: prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name:        "wukongim_storage_channel_acquires_total",
			Help:        "Successful channel store acquisitions reported by the local message store.",
			ConstLabels: channelLabels,
		}, func() float64 {
			return float64(channelEntries.acquireTotal.Load())
		}),
		channelReleasesTotal: prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name:        "wukongim_storage_channel_releases_total",
			Help:        "Terminal channel store lease releases reported by the local message store.",
			ConstLabels: channelLabels,
		}, func() float64 {
			return float64(channelEntries.releaseTotal.Load())
		}),
		channelReclaimsTotal: prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name:        "wukongim_storage_channel_reclaims_total",
			Help:        "Zero-reference canonical channel entries reclaimed by the local message store.",
			ConstLabels: channelLabels,
		}, func() float64 {
			return float64(channelEntries.reclaimTotal.Load())
		}),
	}

	registry.MustRegister(
		m.diskUsageBytes,
		m.pebbleDiskUsageBytes,
		m.pebbleReadAmplification,
		m.pebbleMemTableSizeBytes,
		m.pebbleMemTableCount,
		m.pebbleWALFiles,
		m.pebbleWALSizeBytes,
		m.pebbleWALPhysicalSizeBytes,
		m.pebbleWALBytesIn,
		m.pebbleWALBytesWritten,
		m.pebbleSSTableSizeBytes,
		m.pebbleFlushBytesWritten,
		m.pebbleCompactionBytesRead,
		m.pebbleCompactionBytesWritten,
		m.pebbleFlushCount,
		m.pebbleFlushesInProgress,
		m.pebbleCompactionCount,
		m.pebbleCompactionEstimatedDebt,
		m.pebbleCompactionInProgressBytes,
		m.pebbleCompactionsInProgress,
		m.pebbleWriteStalls,
		m.pebbleWriteStallSeconds,
		m.pebbleWriteStallMaxSeconds,
		m.pebbleWriteStallActive,
		m.pebbleWALFsyncCount,
		m.pebbleWALFsyncSeconds,
		m.pebbleWALFsyncSlow,
		m.pebbleDiskSlowEvents,
		m.pebbleDiskSlowMaxSeconds,
		m.messageExactFreshAppends,
		m.messagePredecessorCacheHits,
		m.messagePredecessorValidations,
		m.messageIdempotencyNegativeSkips,
		m.messageIdempotencyPointReads,
		m.commitQueueDepth,
		m.commitBatchRequests,
		m.commitBatchRecords,
		m.commitBatchBytes,
		m.commitBatchDuration,
		m.commitRequestDuration,
		m.channelEntriesActive,
		m.channelLeasesOutstanding,
		m.channelBackgroundPins,
		m.channelAcquiresTotal,
		m.channelReleasesTotal,
		m.channelReclaimsTotal,
	)

	for _, store := range storageStores {
		m.diskUsageBytes.WithLabelValues(store).Set(0)
		m.SetPebbleMetrics(store, StoragePebbleObservation{})
	}

	return m
}

// SetChannelEntryMetrics replaces the latest absolute channel_log registry snapshot.
// Counter functions expose the source totals directly, so repeated polling does not double count.
func (m *StorageMetrics) SetChannelEntryMetrics(obs StorageChannelEntryObservation) {
	if m == nil || m.channelEntrySnapshot == nil {
		return
	}
	m.channelEntrySnapshot.activeEntries.Store(obs.ActiveEntries)
	m.channelEntrySnapshot.outstandingLeases.Store(obs.OutstandingLeases)
	m.channelEntrySnapshot.backgroundPins.Store(obs.BackgroundPins)
	m.channelEntrySnapshot.acquireTotal.Store(obs.AcquireTotal)
	m.channelEntrySnapshot.releaseTotal.Store(obs.ReleaseTotal)
	m.channelEntrySnapshot.reclaimTotal.Store(obs.ReclaimTotal)
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

func (m *StorageMetrics) SetPebbleMetrics(store string, obs StoragePebbleObservation) {
	if m == nil {
		return
	}
	if store == "" {
		store = "unknown"
	}
	m.pebbleDiskUsageBytes.WithLabelValues(store).Set(float64(obs.DiskSpaceUsageBytes))
	m.pebbleReadAmplification.WithLabelValues(store).Set(float64(obs.ReadAmplification))
	m.pebbleMemTableSizeBytes.WithLabelValues(store).Set(float64(obs.MemTableSizeBytes))
	m.pebbleMemTableCount.WithLabelValues(store).Set(float64(obs.MemTableCount))
	m.pebbleWALFiles.WithLabelValues(store).Set(float64(obs.WALFiles))
	m.pebbleWALSizeBytes.WithLabelValues(store).Set(float64(obs.WALSizeBytes))
	m.pebbleWALPhysicalSizeBytes.WithLabelValues(store).Set(float64(obs.WALPhysicalSizeBytes))
	m.pebbleWALBytesIn.WithLabelValues(store).Set(float64(obs.WALBytesIn))
	m.pebbleWALBytesWritten.WithLabelValues(store).Set(float64(obs.WALBytesWritten))
	m.pebbleSSTableSizeBytes.WithLabelValues(store).Set(float64(obs.SSTableSizeBytes))
	m.pebbleFlushBytesWritten.WithLabelValues(store).Set(float64(obs.FlushBytesWritten))
	m.pebbleCompactionBytesRead.WithLabelValues(store).Set(float64(obs.CompactionBytesRead))
	m.pebbleCompactionBytesWritten.WithLabelValues(store).Set(float64(obs.CompactionBytesWritten))
	m.pebbleFlushCount.WithLabelValues(store).Set(float64(obs.FlushCount))
	m.pebbleFlushesInProgress.WithLabelValues(store).Set(float64(obs.FlushesInProgress))
	m.pebbleCompactionCount.WithLabelValues(store).Set(float64(obs.CompactionCount))
	m.pebbleCompactionEstimatedDebt.WithLabelValues(store).Set(float64(obs.CompactionEstimatedDebtBytes))
	m.pebbleCompactionInProgressBytes.WithLabelValues(store).Set(float64(obs.CompactionInProgressBytes))
	m.pebbleCompactionsInProgress.WithLabelValues(store).Set(float64(obs.CompactionsInProgress))
	m.pebbleWriteStalls.WithLabelValues(store, "memtable").Set(float64(obs.WriteStallMemTableCount))
	m.pebbleWriteStalls.WithLabelValues(store, "l0").Set(float64(obs.WriteStallL0Count))
	m.pebbleWriteStalls.WithLabelValues(store, "other").Set(float64(obs.WriteStallOtherCount))
	m.pebbleWriteStallSeconds.WithLabelValues(store).Set(float64(obs.WriteStallTotalNanos) / 1e9)
	m.pebbleWriteStallMaxSeconds.WithLabelValues(store).Set(float64(obs.WriteStallMaxNanos) / 1e9)
	stallActive := 0.0
	if obs.WriteStallActive {
		stallActive = 1
	}
	m.pebbleWriteStallActive.WithLabelValues(store).Set(stallActive)
	m.pebbleWALFsyncCount.WithLabelValues(store).Set(float64(obs.WALFsyncCount))
	m.pebbleWALFsyncSeconds.WithLabelValues(store).Set(float64(obs.WALFsyncSumNanos) / 1e9)
	m.pebbleWALFsyncSlow.WithLabelValues(store, "100ms").Set(float64(obs.WALFsyncOver100ms))
	m.pebbleWALFsyncSlow.WithLabelValues(store, "1s").Set(float64(obs.WALFsyncOver1s))
	m.pebbleWALFsyncSlow.WithLabelValues(store, "5s").Set(float64(obs.WALFsyncOver5s))
	m.pebbleDiskSlowEvents.WithLabelValues(store, "wal").Set(float64(obs.DiskSlowWALEvents))
	m.pebbleDiskSlowEvents.WithLabelValues(store, "other").Set(float64(obs.DiskSlowOtherEvents))
	m.pebbleDiskSlowMaxSeconds.WithLabelValues(store, "wal").Set(float64(obs.DiskSlowWALMaxNanos) / 1e9)
	m.pebbleDiskSlowMaxSeconds.WithLabelValues(store, "other").Set(float64(obs.DiskSlowOtherMaxNanos) / 1e9)
	m.messageExactFreshAppends.WithLabelValues(store).Set(float64(obs.SequencedExactFreshAppends))
	m.messagePredecessorCacheHits.WithLabelValues(store).Set(float64(obs.DurablePredecessorCacheHits))
	m.messagePredecessorValidations.WithLabelValues(store).Set(float64(obs.DurablePredecessorValidations))
	m.messageIdempotencyNegativeSkips.WithLabelValues(store).Set(float64(obs.IdempotencyNegativeFilterSkips))
	m.messageIdempotencyPointReads.WithLabelValues(store).Set(float64(obs.IdempotencyPointReads))
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
