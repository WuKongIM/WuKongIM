package trace

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/atomic"
)

type dbMetrics struct {
	wklog.Log
	ctx  context.Context
	opts *Options

	// ========== compact 压缩相关 ==========
	compactTotalCount       atomic.Int64
	compactDefaultCount     atomic.Int64
	compactDeleteOnlyCount  atomic.Int64
	compactElisionOnlyCount atomic.Int64
	compactMoveCount        atomic.Int64
	compactReadCount        atomic.Int64
	compactRewriteCount     atomic.Int64
	compactMultiLevelCount  atomic.Int64
	compactEstimatedDebt    atomic.Int64
	compactInProgressBytes  atomic.Int64
	compactNumInProgress    atomic.Int64
	compactMarkedFiles      atomic.Int64

	// ========== flush 相关 ==========
	flushCount              atomic.Int64
	flushBytes              atomic.Int64
	flushNumInProgress      atomic.Int64
	flushAsIngestCount      atomic.Int64
	flushAsIngestTableCount atomic.Int64
	flushAsIngestBytes      atomic.Int64

	// ========== memtable 内存表相关 ==========
	memTableSize        atomic.Int64
	memTableCount       atomic.Int64
	memTableZombieSize  atomic.Int64
	memTableZombieCount atomic.Int64

	// ========== Snapshots 镜像相关 ==========
	snapshotsCount atomic.Int64

	// ========== TableCache 相关 ==========
	tableCacheSize  atomic.Int64
	tableCacheCount atomic.Int64

	// ========== TableIters 相关 ==========
	tableItersCount atomic.Int64

	// ========== WAL 相关 ==========

	walFilesCount           atomic.Int64
	walSize                 atomic.Int64
	walPhysicalSize         atomic.Int64
	walObsoleteFilesCount   atomic.Int64
	walObsoletePhysicalSize atomic.Int64
	walBytesIn              atomic.Int64
	walBytesWritten         atomic.Int64
	logWriterBytes          atomic.Int64

	diskSpaceUsage atomic.Int64

	// ========== level 相关 ==========
	levelNumFiles        atomic.Int64
	levelFileSize        atomic.Int64
	levelCompactScore    atomic.Int64
	levelBytesIn         atomic.Int64
	levelBytesIngested   atomic.Int64
	levelBytesMoved      atomic.Int64
	levelBytesRead       atomic.Int64
	levelBytesCompacted  atomic.Int64
	levelBytesFlushed    atomic.Int64
	levelTablesCompacted atomic.Int64
	levelTablesFlushed   atomic.Int64
	levelTablesIngested  atomic.Int64
	levelTablesMoved     atomic.Int64

	// ========== message 相关 ==========
	messageAppendBatchCount atomic.Int64
}

func newDBMetrics(opts *Options) *dbMetrics {
	m := &dbMetrics{
		Log:  wklog.NewWKLog("dbMetrics"),
		opts: opts,
	}

	// ========== compact 压缩相关 ==========
	compactTotalCount := NewInt64ObservableGauge("db_compact_total_count")
	compactDefaultCount := NewInt64ObservableGauge("db_compact_default_count")
	compactDeleteOnlyCount := NewInt64ObservableGauge("db_compact_delete_only_count")
	compactElisionOnlyCount := NewInt64ObservableGauge("db_compact_elision_only_count")
	compactMoveCount := NewInt64ObservableGauge("db_compact_move_count")
	compactReadCount := NewInt64ObservableGauge("db_compact_read_count")
	compactRewriteCount := NewInt64ObservableGauge("db_compact_rewrite_count")
	compactMultiLevelCount := NewInt64ObservableGauge("db_compact_multi_level_count")
	compactEstimatedDebt := NewInt64ObservableGauge("db_compact_estimated_debt")
	compactInProgressBytes := NewInt64ObservableGauge("db_compact_in_progress_bytes")
	compactNumInProgress := NewInt64ObservableGauge("db_compact_num_in_progress")
	compactMarkedFiles := NewInt64ObservableGauge("db_compact_marked_files")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(compactTotalCount, m.compactTotalCount.Load())
		obs.ObserveInt64(compactDefaultCount, m.compactDefaultCount.Load())
		obs.ObserveInt64(compactDeleteOnlyCount, m.compactDeleteOnlyCount.Load())
		obs.ObserveInt64(compactElisionOnlyCount, m.compactElisionOnlyCount.Load())
		obs.ObserveInt64(compactMoveCount, m.compactMoveCount.Load())
		obs.ObserveInt64(compactReadCount, m.compactReadCount.Load())
		obs.ObserveInt64(compactRewriteCount, m.compactRewriteCount.Load())
		obs.ObserveInt64(compactMultiLevelCount, m.compactMultiLevelCount.Load())
		obs.ObserveInt64(compactEstimatedDebt, m.compactEstimatedDebt.Load())
		obs.ObserveInt64(compactInProgressBytes, m.compactInProgressBytes.Load())
		obs.ObserveInt64(compactNumInProgress, m.compactNumInProgress.Load())
		obs.ObserveInt64(compactMarkedFiles, m.compactMarkedFiles.Load())
		return nil
	}, compactTotalCount, compactDefaultCount, compactDeleteOnlyCount, compactElisionOnlyCount, compactMoveCount, compactReadCount, compactRewriteCount, compactMultiLevelCount, compactEstimatedDebt, compactInProgressBytes, compactNumInProgress, compactMarkedFiles)

	// ========== flush 相关 ==========
	flushCount := NewInt64ObservableGauge("db_flush_count")
	flushBytes := NewInt64ObservableGauge("db_flush_bytes")
	flushNumInProgress := NewInt64ObservableGauge("db_flush_num_in_progress")
	flushAsIngestCount := NewInt64ObservableGauge("db_flush_as_ingest_count")
	flushAsIngestTableCount := NewInt64ObservableGauge("db_flush_as_ingest_table_count")
	flushAsIngestBytes := NewInt64ObservableGauge("db_flush_as_ingest_bytes")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(flushCount, m.flushCount.Load())
		obs.ObserveInt64(flushBytes, m.flushBytes.Load())
		obs.ObserveInt64(flushNumInProgress, m.flushNumInProgress.Load())
		obs.ObserveInt64(flushAsIngestCount, m.flushAsIngestCount.Load())
		obs.ObserveInt64(flushAsIngestTableCount, m.flushAsIngestTableCount.Load())
		obs.ObserveInt64(flushAsIngestBytes, m.flushAsIngestBytes.Load())
		return nil
	}, flushCount, flushBytes, flushNumInProgress, flushAsIngestCount, flushAsIngestTableCount, flushAsIngestBytes)

	// ========== memtable 内存表相关 ==========
	memTableSize := NewInt64ObservableGauge("db_memtable_size")
	memTableCount := NewInt64ObservableGauge("db_memtable_count")
	memTableZombieSize := NewInt64ObservableGauge("db_memtable_zombie_size")
	memTableZombieCount := NewInt64ObservableGauge("db_memtable_zombie_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(memTableSize, m.memTableSize.Load())
		obs.ObserveInt64(memTableCount, m.memTableCount.Load())
		obs.ObserveInt64(memTableZombieSize, m.memTableZombieSize.Load())
		obs.ObserveInt64(memTableZombieCount, m.memTableZombieCount.Load())
		return nil
	}, memTableSize, memTableCount, memTableZombieSize, memTableZombieCount)

	// ========== Snapshots 镜像相关 ==========
	snapshotsCount := NewInt64ObservableGauge("db_snapshots_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(snapshotsCount, m.snapshotsCount.Load())
		return nil
	}, snapshotsCount)

	// ========== TableCache 相关 ==========
	tableCacheSize := NewInt64ObservableGauge("db_table_cache_size")
	tableCacheCount := NewInt64ObservableGauge("db_table_cache_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(tableCacheSize, m.tableCacheSize.Load())
		obs.ObserveInt64(tableCacheCount, m.tableCacheCount.Load())
		return nil
	}, tableCacheSize, tableCacheCount)

	// ========== TableIters 相关 ==========
	tableItersCount := NewInt64ObservableGauge("db_table_iters_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(tableItersCount, m.tableItersCount.Load())
		return nil
	}, tableItersCount)

	// ========== WAL 相关 ==========
	walFilesCount := NewInt64ObservableGauge("db_wal_files_count")
	walSize := NewInt64ObservableGauge("db_wal_size")
	walPhysicalSize := NewInt64ObservableGauge("db_wal_physical_size")
	walObsoleteFilesCount := NewInt64ObservableGauge("db_wal_obsolete_files_count")
	walObsoletePhysicalSize := NewInt64ObservableGauge("db_wal_obsolete_physical_size")
	walBytesIn := NewInt64ObservableGauge("db_wal_bytes_in")
	walBytesWritten := NewInt64ObservableGauge("db_wal_bytes_written")

	diskSpaceUsage := NewInt64ObservableGauge("db_disk_space_usage")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(walFilesCount, m.walFilesCount.Load())
		obs.ObserveInt64(walSize, m.walSize.Load())
		obs.ObserveInt64(walPhysicalSize, m.walPhysicalSize.Load())
		obs.ObserveInt64(walObsoleteFilesCount, m.walObsoleteFilesCount.Load())
		obs.ObserveInt64(walObsoletePhysicalSize, m.walObsoletePhysicalSize.Load())
		obs.ObserveInt64(walBytesIn, m.walBytesIn.Load())
		obs.ObserveInt64(walBytesWritten, m.walBytesWritten.Load())
		obs.ObserveInt64(diskSpaceUsage, m.diskSpaceUsage.Load())
		return nil
	}, walFilesCount, walSize, walPhysicalSize, walObsoleteFilesCount, walObsoletePhysicalSize, walBytesIn, walBytesWritten, diskSpaceUsage)

	// ========== Log Writer 相关 ==========
	logWriterBytes := NewInt64ObservableGauge("db_log_writer_bytes")
	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(logWriterBytes, m.logWriterBytes.Load())
		return nil
	}, logWriterBytes)

	// ========== level 相关 ==========

	levelNumFiles := NewInt64ObservableGauge("db_alllevel_num_files")
	levelFileSize := NewInt64ObservableGauge("db_alllevel_file_size")
	levelCompactScore := NewInt64ObservableGauge("db_alllevel_compact_score")
	levelBytesIn := NewInt64ObservableGauge("db_alllevel_bytes_in")
	levelBytesIngested := NewInt64ObservableGauge("db_alllevel_bytes_ingested")
	levelBytesMoved := NewInt64ObservableGauge("db_alllevel_bytes_moved")
	levelBytesRead := NewInt64ObservableGauge("db_alllevel_bytes_read")
	levelBytesCompacted := NewInt64ObservableGauge("db_alllevel_bytes_compacted")
	levelBytesFlushed := NewInt64ObservableGauge("db_alllevel_bytes_flushed")
	levelTablesCompacted := NewInt64ObservableGauge("db_alllevel_tables_compacted")
	levelTablesFlushed := NewInt64ObservableGauge("db_alllevel_tables_flushed")
	levelTablesIngested := NewInt64ObservableGauge("db_alllevel_tables_ingested")
	levelTablesMoved := NewInt64ObservableGauge("db_alllevel_tables_moved")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(levelNumFiles, m.levelNumFiles.Load())
		obs.ObserveInt64(levelFileSize, m.levelFileSize.Load())
		obs.ObserveInt64(levelCompactScore, m.levelCompactScore.Load())
		obs.ObserveInt64(levelBytesIn, m.levelBytesIn.Load())
		obs.ObserveInt64(levelBytesIngested, m.levelBytesIngested.Load())
		obs.ObserveInt64(levelBytesMoved, m.levelBytesMoved.Load())
		obs.ObserveInt64(levelBytesRead, m.levelBytesRead.Load())
		obs.ObserveInt64(levelBytesCompacted, m.levelBytesCompacted.Load())
		obs.ObserveInt64(levelBytesFlushed, m.levelBytesFlushed.Load())
		obs.ObserveInt64(levelTablesCompacted, m.levelTablesCompacted.Load())
		obs.ObserveInt64(levelTablesFlushed, m.levelTablesFlushed.Load())
		obs.ObserveInt64(levelTablesIngested, m.levelTablesIngested.Load())
		obs.ObserveInt64(levelTablesMoved, m.levelTablesMoved.Load())
		return nil
	}, levelNumFiles, levelFileSize,
		levelCompactScore, levelBytesIn,
		levelBytesIngested, levelBytesMoved,
		levelBytesRead, levelBytesCompacted,
		levelBytesFlushed, levelTablesCompacted,
		levelTablesFlushed, levelTablesIngested, levelTablesMoved,
	)

	// ========== message 相关 ==========

	messageAppendBatchCount := NewInt64ObservableCounter("db_message_append_batch_count")
	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(messageAppendBatchCount, m.messageAppendBatchCount.Load())
		return nil
	}, messageAppendBatchCount)

	return m
}

// ========== compact 压缩相关 ==========
func (m *dbMetrics) CompactTotalCountSet(shardId uint32, v int64) {
	m.compactTotalCount.Store(v)
}
func (m *dbMetrics) CompactDefaultCountSet(shardId uint32, v int64) {
	m.compactDefaultCount.Store(v)
}
func (m *dbMetrics) CompactDeleteOnlyCountSet(shardId uint32, v int64) {
	m.compactDeleteOnlyCount.Store(v)
}
func (m *dbMetrics) CompactElisionOnlyCountSet(shardId uint32, v int64) {
	m.compactElisionOnlyCount.Store(v)
}
func (m *dbMetrics) CompactMoveCountSet(shardId uint32, v int64) {
	m.compactMoveCount.Store(v)
}
func (m *dbMetrics) CompactReadCountSet(shardId uint32, v int64) {
	m.compactReadCount.Store(v)
}
func (m *dbMetrics) CompactRewriteCountSet(shardId uint32, v int64) {
	m.compactRewriteCount.Store(v)
}
func (m *dbMetrics) CompactMultiLevelCount(shardId uint32, v int64) {
	m.compactMultiLevelCount.Store(v)
}
func (m *dbMetrics) CompactEstimatedDebtSet(shardId uint32, v int64) {
	m.compactEstimatedDebt.Store(v)
}
func (m *dbMetrics) CompactInProgressBytesSet(shardId uint32, v int64) {
	m.compactInProgressBytes.Store(v)
}
func (m *dbMetrics) CompactNumInProgressSet(shardId uint32, v int64) {
	m.compactNumInProgress.Store(v)
}
func (m *dbMetrics) CompactMarkedFilesSet(shardId uint32, v int64) {
	m.compactMarkedFiles.Store(v)
}

// ========== flush 相关 ==========
func (m *dbMetrics) FlushCountAdd(shardId uint32, v int64) {
	m.flushCount.Store(v)
}
func (m *dbMetrics) FlushBytesAdd(shardId uint32, v int64) {
	m.flushBytes.Store(v)
}
func (m *dbMetrics) FlushNumInProgressAdd(shardId uint32, v int64) {
	m.flushNumInProgress.Store(v)
}
func (m *dbMetrics) FlushAsIngestCountAdd(shardId uint32, v int64) {
	m.flushAsIngestCount.Store(v)
}
func (m *dbMetrics) FlushAsIngestTableCountAdd(shardId uint32, v int64) {
	m.flushAsIngestTableCount.Store(v)
}
func (m *dbMetrics) FlushAsIngestBytesAdd(shardId uint32, v int64) {
	m.flushAsIngestBytes.Store(v)
}

// ========== memtable 内存表相关 ==========
func (m *dbMetrics) MemTableSizeSet(shardId uint32, v int64) {

	m.memTableSize.Store(v)

}
func (m *dbMetrics) MemTableCountSet(shardId uint32, v int64) {

	m.memTableCount.Store(v)
}
func (m *dbMetrics) MemTableZombieSizeSet(shardId uint32, v int64) {

	m.memTableZombieSize.Store(v)

}
func (m *dbMetrics) MemTableZombieCountSet(shardId uint32, v int64) {

	m.memTableZombieCount.Store(v)
}

// ========== Snapshots 镜像相关 ==========
func (m *dbMetrics) SnapshotsCountSet(shardId uint32, v int64) {

	m.snapshotsCount.Store(v)
}

// ========== TableCache 相关 ==========
func (m *dbMetrics) TableCacheSizeSet(shardId uint32, v int64) {
	m.tableCacheSize.Store(v)
}
func (m *dbMetrics) TableCacheCountSet(shardId uint32, v int64) {
	m.tableCacheCount.Store(v)
}

// ========== TableIters 相关 ==========
func (m *dbMetrics) TableItersCountSet(shardId uint32, v int64) {
	m.tableItersCount.Store(v)
}

// ========== WAL 相关 ==========

func (m *dbMetrics) WALFilesCountSet(shardId uint32, v int64) {
	m.walFilesCount.Store(v)
}
func (m *dbMetrics) WALSizeSet(shardId uint32, v int64) {
	m.walFilesCount.Store(v)
}
func (m *dbMetrics) WALPhysicalSizeSet(shardId uint32, v int64) {
	m.walPhysicalSize.Store(v)
}
func (m *dbMetrics) WALObsoleteFilesCountSet(shardId uint32, v int64) {
	m.walObsoleteFilesCount.Store(v)
}
func (m *dbMetrics) WALObsoletePhysicalSizeSet(shardId uint32, v int64) {
	m.walObsoletePhysicalSize.Store(v)
}
func (m *dbMetrics) WALBytesInSet(shardId uint32, v int64) {
	m.walBytesIn.Store(v)
}
func (m *dbMetrics) WALBytesWrittenSet(shardId uint32, v int64) {
	m.walBytesWritten.Store(v)
}

func (m *dbMetrics) DiskSpaceUsageSet(shardId uint32, v int64) {
	m.diskSpaceUsage.Store(v)
}

// ========== Log Writer 相关 ==========
func (m *dbMetrics) LogWriterBytesSet(shardId uint32, v int64) {
	m.logWriterBytes.Store(v)
}

// ========== level 相关 ==========

func (m *dbMetrics) LevelNumFilesSet(shardId uint32, v int64) {
	m.levelNumFiles.Store(v)
}

func (m *dbMetrics) LevelFileSizeSet(shardId uint32, v int64) {
	m.levelFileSize.Store(v)

}
func (m *dbMetrics) LevelCompactScoreSet(shardId uint32, v int64) {
	m.levelCompactScore.Store(v)
}

func (m *dbMetrics) LevelBytesInSet(shardId uint32, v int64) {
	m.levelBytesIn.Store(v)
}
func (m *dbMetrics) LevelBytesIngestedSet(shardId uint32, v int64) {
	m.levelBytesIngested.Store(v)
}
func (m *dbMetrics) LevelBytesMovedSet(shardId uint32, v int64) {
	m.levelBytesMoved.Store(v)
}
func (m *dbMetrics) LevelBytesReadSet(shardId uint32, v int64) {
	m.levelBytesRead.Store(v)
}
func (m *dbMetrics) LevelBytesCompactedSet(shardId uint32, v int64) {
	m.levelBytesCompacted.Store(v)
}
func (m *dbMetrics) LevelBytesFlushedSet(shardId uint32, v int64) {
	m.levelBytesFlushed.Store(v)
}
func (m *dbMetrics) LevelTablesCompactedSet(shardId uint32, v int64) {
	m.levelTablesCompacted.Store(v)
}
func (m *dbMetrics) LevelTablesFlushedSet(shardId uint32, v int64) {
	m.levelTablesFlushed.Store(v)
}
func (m *dbMetrics) LevelTablesIngestedSet(shardId uint32, v int64) {
	m.levelTablesIngested.Store(v)
}
func (m *dbMetrics) LevelTablesMovedSet(shardId uint32, v int64) {
	m.levelTablesMoved.Store(v)
}

// ========== message 相关 ==========
func (m *dbMetrics) MessageAppendBatchCountAdd(v int64) {
	m.messageAppendBatchCount.Add(v)
}
