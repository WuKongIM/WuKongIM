package store

import messagedb "github.com/WuKongIM/WuKongIM/pkg/db/message"

// MessageDBFactoryMetricsSnapshot is a stable view of the factory-owned message store.
type MessageDBFactoryMetricsSnapshot struct {
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
	}
}
