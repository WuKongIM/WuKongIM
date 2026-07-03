package cluster

import (
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/raftlog"
)

// StorageEngineMetrics is a stable view of one local storage engine.
type StorageEngineMetrics struct {
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

// StorageStoreMetricsSnapshot describes one named physical storage engine.
type StorageStoreMetricsSnapshot struct {
	// Store is the low-cardinality physical store name used by storage metrics.
	Store string
	// Engine contains the current storage engine metrics for Store.
	Engine StorageEngineMetrics
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
		stores = append(stores, StorageStoreMetricsSnapshot{
			Store:  "channel_log",
			Engine: storageMetricsFromChannelStore(n.defaultChannelStore.MetricsSnapshot()),
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

func storageMetricsFromChannelStore(snapshot channelstore.MessageDBFactoryMetricsSnapshot) StorageEngineMetrics {
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
		FlushCount:                   snapshot.FlushCount,
		FlushesInProgress:            snapshot.FlushesInProgress,
		CompactionCount:              snapshot.CompactionCount,
		CompactionEstimatedDebtBytes: snapshot.CompactionEstimatedDebtBytes,
		CompactionInProgressBytes:    snapshot.CompactionInProgressBytes,
		CompactionsInProgress:        snapshot.CompactionsInProgress,
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
		FlushCount:                   snapshot.FlushCount,
		FlushesInProgress:            snapshot.FlushesInProgress,
		CompactionCount:              snapshot.CompactionCount,
		CompactionEstimatedDebtBytes: snapshot.CompactionEstimatedDebtBytes,
		CompactionInProgressBytes:    snapshot.CompactionInProgressBytes,
		CompactionsInProgress:        snapshot.CompactionsInProgress,
	}
}
