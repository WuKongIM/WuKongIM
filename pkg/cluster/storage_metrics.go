package cluster

import (
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/raftlog"
)

// StorageEngineMetrics is a stable view of one local storage engine.
type StorageEngineMetrics struct {
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

// StorageChannelEntryMetricsSnapshot describes aggregate channel entry ownership.
type StorageChannelEntryMetricsSnapshot struct {
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

// StorageStoreMetricsSnapshot describes one named physical storage engine.
type StorageStoreMetricsSnapshot struct {
	// Store is the low-cardinality physical store name used by storage metrics.
	Store string
	// Engine contains the current storage engine metrics for Store.
	Engine StorageEngineMetrics
	// ChannelEntries contains channel registry ownership metrics when Store is channel_log.
	ChannelEntries StorageChannelEntryMetricsSnapshot
}

// StorageMetricsSnapshot describes local storage engines hosted by the Node.
type StorageMetricsSnapshot struct {
	// Stores contains one snapshot per default local storage engine.
	Stores []StorageStoreMetricsSnapshot
}

// StorageMetricsSnapshot returns metrics for default local storage engines owned by this Node.
func (n *Node) StorageMetricsSnapshot() StorageMetricsSnapshot {
	if n == nil {
		return StorageMetricsSnapshot{}
	}
	stores := make([]StorageStoreMetricsSnapshot, 0, 3)
	if n.defaultChannelStore != nil {
		snapshot := n.defaultChannelStore.MetricsSnapshot()
		stores = append(stores, StorageStoreMetricsSnapshot{
			Store:          "channel_log",
			Engine:         storageMetricsFromChannelStore(snapshot),
			ChannelEntries: storageChannelEntryMetricsFromChannelStore(snapshot),
		})
	}
	if n.defaultSlotMetaDB != nil {
		stores = append(stores, StorageStoreMetricsSnapshot{
			Store:  "meta",
			Engine: storageMetricsFromMetaDB(n.defaultSlotMetaDB.MetricsSnapshot()),
		})
	}
	if n.defaultSlotRaftDB != nil {
		stores = append(stores, StorageStoreMetricsSnapshot{
			Store:  "raft",
			Engine: storageMetricsFromRaftLog(n.defaultSlotRaftDB.MetricsSnapshot()),
		})
	}
	return StorageMetricsSnapshot{Stores: stores}
}

func storageChannelEntryMetricsFromChannelStore(snapshot channelstore.MessageDBFactoryMetricsSnapshot) StorageChannelEntryMetricsSnapshot {
	return StorageChannelEntryMetricsSnapshot{
		ActiveEntries:     snapshot.ChannelEntries.ActiveEntries,
		OutstandingLeases: snapshot.ChannelEntries.OutstandingLeases,
		BackgroundPins:    snapshot.ChannelEntries.BackgroundPins,
		AcquireTotal:      snapshot.ChannelEntries.AcquireTotal,
		ReleaseTotal:      snapshot.ChannelEntries.ReleaseTotal,
		ReclaimTotal:      snapshot.ChannelEntries.ReclaimTotal,
	}
}

func storageMetricsFromChannelStore(snapshot channelstore.MessageDBFactoryMetricsSnapshot) StorageEngineMetrics {
	return StorageEngineMetrics{
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
	}
}

func storageMetricsFromMetaDB(snapshot metadb.EngineMetricsSnapshot) StorageEngineMetrics {
	return StorageEngineMetrics{
		DiskSpaceUsageBytes:          snapshot.DiskSpaceUsageBytes,
		ReadAmplification:            snapshot.ReadAmplification,
		MemTableSizeBytes:            snapshot.MemTableSizeBytes,
		MemTableCount:                snapshot.MemTableCount,
		WALFiles:                     snapshot.WALFiles,
		WALSizeBytes:                 snapshot.WALSizeBytes,
		WALPhysicalSizeBytes:         snapshot.WALPhysicalSizeBytes,
		WALBytesIn:                   snapshot.WALBytesIn,
		WALBytesWritten:              snapshot.WALBytesWritten,
		SSTableSizeBytes:             snapshot.SSTableSizeBytes,
		FlushBytesWritten:            snapshot.FlushBytesWritten,
		CompactionBytesRead:          snapshot.CompactionBytesRead,
		CompactionBytesWritten:       snapshot.CompactionBytesWritten,
		FlushCount:                   snapshot.FlushCount,
		FlushesInProgress:            snapshot.FlushesInProgress,
		CompactionCount:              snapshot.CompactionCount,
		CompactionEstimatedDebtBytes: snapshot.CompactionEstimatedDebtBytes,
		CompactionInProgressBytes:    snapshot.CompactionInProgressBytes,
		CompactionsInProgress:        snapshot.CompactionsInProgress,
	}
}

func storageMetricsFromRaftLog(snapshot raftlog.MetricsSnapshot) StorageEngineMetrics {
	return StorageEngineMetrics{
		DiskSpaceUsageBytes:          snapshot.DiskSpaceUsageBytes,
		ReadAmplification:            snapshot.ReadAmplification,
		MemTableSizeBytes:            snapshot.MemTableSizeBytes,
		MemTableCount:                snapshot.MemTableCount,
		WALFiles:                     snapshot.WALFiles,
		WALSizeBytes:                 snapshot.WALSizeBytes,
		WALPhysicalSizeBytes:         snapshot.WALPhysicalSizeBytes,
		WALBytesIn:                   snapshot.WALBytesIn,
		WALBytesWritten:              snapshot.WALBytesWritten,
		SSTableSizeBytes:             snapshot.SSTableSizeBytes,
		FlushBytesWritten:            snapshot.FlushBytesWritten,
		CompactionBytesRead:          snapshot.CompactionBytesRead,
		CompactionBytesWritten:       snapshot.CompactionBytesWritten,
		FlushCount:                   snapshot.FlushCount,
		FlushesInProgress:            snapshot.FlushesInProgress,
		CompactionCount:              snapshot.CompactionCount,
		CompactionEstimatedDebtBytes: snapshot.CompactionEstimatedDebtBytes,
		CompactionInProgressBytes:    snapshot.CompactionInProgressBytes,
		CompactionsInProgress:        snapshot.CompactionsInProgress,
	}
}
