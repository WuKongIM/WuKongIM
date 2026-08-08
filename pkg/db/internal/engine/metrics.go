package engine

// MetricsSnapshot is a stable, Pebble-neutral view of one local storage engine.
type MetricsSnapshot struct {
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
	// FlushCount is the number of completed flushes since this engine opened.
	FlushCount int64
	// FlushesInProgress is the current number of flushes in progress.
	FlushesInProgress int64
	// CompactionCount is the number of completed compactions since this engine opened.
	CompactionCount int64
	// CompactionEstimatedDebtBytes is Pebble's estimate of bytes that need compaction.
	CompactionEstimatedDebtBytes uint64
	// CompactionInProgressBytes is the bytes being written by in-progress compactions.
	CompactionInProgressBytes int64
	// CompactionsInProgress is the current number of compactions in progress.
	CompactionsInProgress int64
}

// MetricsSnapshot returns a stable storage-engine metrics snapshot.
func (e *DB) MetricsSnapshot() MetricsSnapshot {
	if e == nil || e.pdb == nil {
		return MetricsSnapshot{}
	}
	pdb := e.pdb
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
	return MetricsSnapshot{
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
}
