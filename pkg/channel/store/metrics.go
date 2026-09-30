package store

import messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"

// MessageDBFactoryMetricsSnapshot is a stable view of the factory-owned message store.
type MessageDBFactoryMetricsSnapshot struct {
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
	// ChannelEntries contains aggregate canonical entry ownership for the message store.
	ChannelEntries messagedb.ChannelEntryMetricsSnapshot
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
	// CompactionEstimatedDebtBytes is the engine's estimate of bytes that need compaction.
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

// MetricsSnapshot returns metrics for the factory-owned message DB engine.
func (f *MessageDBFactory) MetricsSnapshot() MessageDBFactoryMetricsSnapshot {
	if f == nil || f.engine == nil {
		return MessageDBFactoryMetricsSnapshot{}
	}
	snapshot := messageFactoryMetricsFromSnapshot(f.engine.MetricsSnapshot())
	snapshot.ChannelEntries = f.ChannelEntryMetricsSnapshot()
	return snapshot
}

// ChannelEntryMetricsSnapshot returns aggregate channel entry ownership for the factory engine.
func (f *MessageDBFactory) ChannelEntryMetricsSnapshot() messagedb.ChannelEntryMetricsSnapshot {
	if f == nil || f.engine == nil {
		return messagedb.ChannelEntryMetricsSnapshot{}
	}
	return f.engine.ChannelEntryMetricsSnapshot()
}

func messageFactoryMetricsFromSnapshot(snapshot messagedb.EngineMetricsSnapshot) MessageDBFactoryMetricsSnapshot {
	return MessageDBFactoryMetricsSnapshot{
		SequencedExactFreshAppends:     snapshot.SequencedExactFreshAppends,
		DurablePredecessorCacheHits:    snapshot.DurablePredecessorCacheHits,
		DurablePredecessorValidations:  snapshot.DurablePredecessorValidations,
		IdempotencyNegativeFilterSkips: snapshot.IdempotencyNegativeFilterSkips,
		IdempotencyPointReads:          snapshot.IdempotencyPointReads,
		DiskSpaceUsageBytes:            snapshot.DiskSpaceUsageBytes,
		ReadAmplification:              snapshot.ReadAmplification,
		MemTableSizeBytes:              snapshot.MemTableSizeBytes,
		MemTableCount:                  snapshot.MemTableCount,
		WALFiles:                       snapshot.WALFiles,
		WALSizeBytes:                   snapshot.WALSizeBytes,
		WALPhysicalSizeBytes:           snapshot.WALPhysicalSizeBytes,
		WALBytesIn:                     snapshot.WALBytesIn,
		WALBytesWritten:                snapshot.WALBytesWritten,
		SSTableSizeBytes:               snapshot.SSTableSizeBytes,
		FlushBytesWritten:              snapshot.FlushBytesWritten,
		CompactionBytesRead:            snapshot.CompactionBytesRead,
		CompactionBytesWritten:         snapshot.CompactionBytesWritten,
		FlushCount:                     snapshot.FlushCount,
		FlushesInProgress:              snapshot.FlushesInProgress,
		CompactionCount:                snapshot.CompactionCount,
		CompactionEstimatedDebtBytes:   snapshot.CompactionEstimatedDebtBytes,
		CompactionInProgressBytes:      snapshot.CompactionInProgressBytes,
		CompactionsInProgress:          snapshot.CompactionsInProgress,
		WriteStallMemTableCount:        snapshot.WriteStallMemTableCount,
		WriteStallL0Count:              snapshot.WriteStallL0Count,
		WriteStallOtherCount:           snapshot.WriteStallOtherCount,
		WriteStallTotalNanos:           snapshot.WriteStallTotalNanos,
		WriteStallMaxNanos:             snapshot.WriteStallMaxNanos,
		WriteStallActive:               snapshot.WriteStallActive,
		WALFsyncCount:                  snapshot.WALFsyncCount,
		WALFsyncSumNanos:               snapshot.WALFsyncSumNanos,
		WALFsyncOver100ms:              snapshot.WALFsyncOver100ms,
		WALFsyncOver1s:                 snapshot.WALFsyncOver1s,
		WALFsyncOver5s:                 snapshot.WALFsyncOver5s,
		DiskSlowWALEvents:              snapshot.DiskSlowWALEvents,
		DiskSlowWALMaxNanos:            snapshot.DiskSlowWALMaxNanos,
		DiskSlowOtherEvents:            snapshot.DiskSlowOtherEvents,
		DiskSlowOtherMaxNanos:          snapshot.DiskSlowOtherMaxNanos,
	}
}
