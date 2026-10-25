package raftlog

import (
	"time"

	dto "github.com/prometheus/client_model/go"
)

// MetricsSnapshot is a stable view of the raft log engine's local storage state.
type MetricsSnapshot struct {
	// Available distinguishes a live engine snapshot from nil or closing storage.
	Available bool
	// BlockCacheSizeBytes is the current retained block-cache bytes, not its capacity.
	BlockCacheSizeBytes int64
	// BlockCacheHits counts block reads served from the cache since this store opened.
	BlockCacheHits int64
	// BlockCacheMisses counts block reads that missed the cache since this store opened.
	BlockCacheMisses int64
	// MemTableReservedSizeBytes is cache accounting for active, recycled and reader-pinned
	// memtable arenas. It excludes flushable-batch backing and is not total cache reservations or RSS.
	MemTableReservedSizeBytes uint64
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
	// WALFsyncCount is the number of actual WAL fsyncs since this engine opened.
	WALFsyncCount uint64
	// WALFsyncSumNanos is cumulative actual WAL fsync time, not Save queue time.
	WALFsyncSumNanos int64
	// WALFsyncOver100ms, WALFsyncOver1s and WALFsyncOver5s count fsyncs above
	// the nearest lower native histogram bucket boundary for each threshold.
	WALFsyncOver100ms uint64
	WALFsyncOver1s    uint64
	WALFsyncOver5s    uint64
	// WriteStall counts are Pebble write-stop reports, separated by reason.
	WriteStallMemTableCount int64
	WriteStallL0Count       int64
	WriteStallOtherCount    int64
	// WriteStallTotalNanos and WriteStallMaxNanos include an active stall.
	WriteStallTotalNanos int64
	WriteStallMaxNanos   int64
	// WriteStallActive reports whether Pebble is currently delaying writes.
	WriteStallActive bool
	// DiskSlow events count reports, including repeated reports of one slow
	// operation. WAL files and other files retain separate counts and maxima.
	DiskSlowWALEvents     int64
	DiskSlowWALMaxNanos   int64
	DiskSlowOtherEvents   int64
	DiskSlowOtherMaxNanos int64
	// SSTableSizeBytes is the current physical size of live SSTables across all levels.
	SSTableSizeBytes uint64
	// FlushBytesWritten is the cumulative bytes written to SSTables by flushes.
	FlushBytesWritten uint64
	// CompactionBytesRead is the cumulative SSTable bytes read by compactions.
	CompactionBytesRead uint64
	// CompactionBytesWritten is the cumulative SSTable bytes written by compactions.
	CompactionBytesWritten uint64
	// FlushCount is the number of completed flushes since this engine opened.
	FlushCount int64
	// FlushesInProgress is the current number of flushes in progress.
	FlushesInProgress int64
	// CompactionCount is the number of completed compactions since this engine opened.
	CompactionCount int64
	// CompactionEstimatedDebtBytes is the engine's estimate of bytes that need compaction.
	CompactionEstimatedDebtBytes uint64
	// CompactionInProgressBytes is the bytes being written by in-progress compactions.
	CompactionInProgressBytes int64
	// CompactionsInProgress is the current number of compactions in progress.
	CompactionsInProgress int64
}

// MetricsSnapshot returns the raft log engine's current storage metrics.
func (db *DB) MetricsSnapshot() MetricsSnapshot {
	if db == nil {
		return MetricsSnapshot{}
	}
	// Use the existing close fence: no snapshot may outlive the point where
	// Close permits the Pebble handle to be destroyed.
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closing || db.db == nil {
		return MetricsSnapshot{}
	}
	pdb := db.db
	metrics := pdb.Metrics()
	if metrics == nil {
		return MetricsSnapshot{}
	}
	var sstableSizeBytes uint64
	var flushBytesWritten uint64
	var compactionBytesRead uint64
	var compactionBytesWritten uint64
	for i := range metrics.Levels {
		level := metrics.Levels[i]
		if level.TablesSize > 0 {
			sstableSizeBytes += uint64(level.TablesSize)
		}
		flushBytesWritten += level.TableBytesFlushed
		compactionBytesRead += level.TableBytesRead
		compactionBytesWritten += level.TableBytesCompacted
	}
	// Pebble v2.1.4 subtracts queue bytes (including flushable batches) from
	// arena-only reservations to form ZombieSize. Unsigned addition restores
	// that same snapshot's reservation even when the subtraction wraps. This
	// excludes separate flushable-batch reservations and does not estimate RSS.
	memTableReserved := metrics.MemTable.Size + metrics.MemTable.ZombieSize
	snapshot := MetricsSnapshot{
		Available:                    true,
		BlockCacheSizeBytes:          metrics.BlockCache.Size,
		BlockCacheHits:               metrics.BlockCache.Hits,
		BlockCacheMisses:             metrics.BlockCache.Misses,
		MemTableReservedSizeBytes:    memTableReserved,
		DiskSpaceUsageBytes:          metrics.DiskSpaceUsage(),
		ReadAmplification:            metrics.ReadAmp(),
		MemTableSizeBytes:            metrics.MemTable.Size,
		MemTableCount:                metrics.MemTable.Count,
		WALFiles:                     metrics.WAL.Files,
		WALSizeBytes:                 metrics.WAL.Size,
		WALPhysicalSizeBytes:         metrics.WAL.PhysicalSize,
		WALBytesIn:                   metrics.WAL.BytesIn,
		WALBytesWritten:              metrics.WAL.BytesWritten,
		SSTableSizeBytes:             sstableSizeBytes,
		FlushBytesWritten:            flushBytesWritten,
		CompactionBytesRead:          compactionBytesRead,
		CompactionBytesWritten:       compactionBytesWritten,
		FlushCount:                   metrics.Flush.Count,
		FlushesInProgress:            metrics.Flush.NumInProgress,
		CompactionCount:              metrics.Compact.Count,
		CompactionEstimatedDebtBytes: metrics.Compact.EstimatedDebt,
		CompactionInProgressBytes:    metrics.Compact.InProgressBytes,
		CompactionsInProgress:        metrics.Compact.NumInProgress,
	}
	// Pebble installs this histogram unconditionally during Open, including
	// before the first write. Its observations and bucket bounds are nanoseconds.
	var histogram dto.Metric
	if err := metrics.LogWriter.FsyncLatency.Write(&histogram); err == nil {
		populateWALFsync(&snapshot, histogram.GetHistogram())
	}
	db.storageEvents.populate(&snapshot)
	return snapshot
}

// populateWALFsync preserves the native histogram's cumulative population.
func populateWALFsync(snapshot *MetricsSnapshot, histogram *dto.Histogram) {
	snapshot.WALFsyncCount = histogram.GetSampleCount()
	snapshot.WALFsyncSumNanos = int64(histogram.GetSampleSum())
	atMost := func(limit time.Duration) uint64 {
		var count uint64
		for _, bucket := range histogram.GetBucket() {
			if bucket.GetUpperBound() > float64(limit) {
				break
			}
			count = bucket.GetCumulativeCount()
		}
		return count
	}
	snapshot.WALFsyncOver100ms = snapshot.WALFsyncCount - atMost(100*time.Millisecond)
	snapshot.WALFsyncOver1s = snapshot.WALFsyncCount - atMost(time.Second)
	snapshot.WALFsyncOver5s = snapshot.WALFsyncCount - atMost(5*time.Second)
}
