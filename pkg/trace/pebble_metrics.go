package trace

import (
	"context"

	"github.com/cockroachdb/pebble/v2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

/**
1. 读写放大指标
读放大(Read Amplification)：这是衡量读取效率的关键指标，计算为L0子层数量加上非空的其他层数量。 metrics.go:469-477

写放大(Write Amplification)：通过计算每个层级的写入字节与输入字节的比率来衡量。可以通过LevelMetrics.WriteAmp()方法获取。 metrics.go:143-149

2. 压缩相关指标
压缩债务(Compaction Debt)：表示LSM树达到稳定状态需要压缩的字节数估计值。 metrics.go:178-180

压缩计数：各种类型的压缩操作计数，包括默认压缩、删除压缩、移动压缩等。 metrics.go:166-176

进行中的压缩：当前正在进行的压缩数量和涉及的字节数。 metrics.go:181-186

3. 存储空间指标
磁盘空间使用：数据库使用的总磁盘空间，包括活跃和过时的文件。 metrics.go:430-445

表文件统计：活跃、过时和僵尸表文件的数量和大小。 metrics.go:2057-2069

4. 延迟指标
WAL同步延迟：预写日志同步操作的延迟，使用Prometheus直方图记录。 metrics.go:397-400 metrics.go:414-420

二级缓存延迟：如果使用二级缓存，其读写操作的延迟。 shared_cache.go:74-84

5. 缓存指标
块缓存命中率：块缓存的命中和未命中次数，可用于计算命中率。
缓存大小：块缓存和文件缓存的当前大小。
6. 内存使用指标
内存表大小：内存表使用的内存量。 metrics.go:228-229
7. 层级指标
每层文件数量和大小：每个层级的表文件数量和总大小。 metrics.go:121-124

层级分数：每个层级相对于其目标大小的比率，用于压缩选择。

8. 写入停顿指标
写入停顿次数和持续时间：由于资源限制导致的写入停顿的次数和总持续时间。 replay.go:135-136
9. 吞吐量指标
写入吞吐量：WAL和刷新操作的写入吞吐量。 metrics.go:208-209
10. 墓碑和范围键指标
墓碑计数：数据库中的墓碑数量，影响读取性能和压缩选择。 metrics.go:2062-2063

**/

// PebbleMetrics 结构体用于管理所有Pebble相关的OpenTelemetry指标
type PebbleMetrics struct {
	// 读写放大指标
	readAmp  metric.Float64Gauge
	writeAmp metric.Float64Gauge

	// 压缩相关指标
	compactionDebt  metric.Float64Gauge
	compactionCount metric.Int64Counter
	inProgressBytes metric.Float64Gauge
	inProgressCount metric.Float64Gauge

	// 存储空间指标
	diskSpaceUsage metric.Float64Gauge
	tableCount     metric.Float64Gauge
	tableSize      metric.Float64Gauge

	// 延迟指标
	// walFsyncLatency metric.Float64Histogram

	// 缓存指标
	blockCacheSize   metric.Float64Gauge
	blockCacheHits   metric.Int64Counter
	blockCacheMisses metric.Int64Counter

	// 内存使用指标
	memTableSize  metric.Float64Gauge
	memTableCount metric.Float64Gauge

	// 写入停顿指标
	// writeStallCount    metric.Int64Counter
	// writeStallDuration metric.Float64Counter

	// 吞吐量指标
	flushThroughput    metric.Float64Gauge
	flushCount         metric.Int64Counter
	flushNumInProgress metric.Float64Gauge

	// 墓碑指标
	tombstoneCount metric.Float64Gauge

	// 指标记录器
	meter metric.Meter
}

// 初始化所有OpenTelemetry指标
func NewPebbleMetrics(meter metric.Meter) (*PebbleMetrics, error) {
	pm := &PebbleMetrics{
		meter: meter,
	}

	var err error

	// 读写放大指标
	pm.readAmp, err = meter.Float64Gauge("pebble_read_amplification",
		metric.WithDescription("读放大因子，计算为L0子层数量加上非空的其他层数量"))
	if err != nil {
		return nil, err
	}

	pm.writeAmp, err = meter.Float64Gauge("pebble_write_amplification",
		metric.WithDescription("写放大因子，计算为写入字节与输入字节的比率"))
	if err != nil {
		return nil, err
	}

	// 压缩相关指标
	pm.compactionDebt, err = meter.Float64Gauge("pebble_compaction_debt_bytes",
		metric.WithDescription("LSM树达到稳定状态需要压缩的字节数估计值"))
	if err != nil {
		return nil, err
	}

	pm.compactionCount, err = meter.Int64Counter("pebble_compaction_count_total",
		metric.WithDescription("压缩操作计数"))
	if err != nil {
		return nil, err
	}

	pm.inProgressBytes, err = meter.Float64Gauge("pebble_compaction_in_progress_bytes",
		metric.WithDescription("正在进行的压缩涉及的字节数"))
	if err != nil {
		return nil, err
	}

	pm.inProgressCount, err = meter.Float64Gauge("pebble_compaction_in_progress_count",
		metric.WithDescription("正在进行的压缩数量"))
	if err != nil {
		return nil, err
	}

	// 存储空间指标
	pm.diskSpaceUsage, err = meter.Float64Gauge("pebble_disk_space_usage_bytes",
		metric.WithDescription("数据库使用的总磁盘空间"))
	if err != nil {
		return nil, err
	}

	pm.tableCount, err = meter.Float64Gauge("pebble_table_count",
		metric.WithDescription("表文件数量"))
	if err != nil {
		return nil, err
	}

	pm.tableSize, err = meter.Float64Gauge("pebble_table_size_bytes",
		metric.WithDescription("表文件大小"))
	if err != nil {
		return nil, err
	}

	// 延迟指标
	// pm.walFsyncLatency, err = meter.Float64Histogram("pebble_wal_fsync_latency_seconds",
	// 	metric.WithDescription("WAL同步操作的延迟"))
	// if err != nil {
	// 	return nil, err
	// }

	// 缓存指标
	pm.blockCacheSize, err = meter.Float64Gauge("pebble_block_cache_size_bytes",
		metric.WithDescription("块缓存大小"))
	if err != nil {
		return nil, err
	}

	pm.blockCacheHits, err = meter.Int64Counter("pebble_block_cache_hits_total",
		metric.WithDescription("块缓存命中次数"))
	if err != nil {
		return nil, err
	}

	pm.blockCacheMisses, err = meter.Int64Counter("pebble_block_cache_misses_total",
		metric.WithDescription("块缓存未命中次数"))
	if err != nil {
		return nil, err
	}

	// 内存使用指标
	pm.memTableSize, err = meter.Float64Gauge("pebble_memtable_size_bytes",
		metric.WithDescription("内存表大小"))
	if err != nil {
		return nil, err
	}

	pm.memTableCount, err = meter.Float64Gauge("pebble_memtable_count",
		metric.WithDescription("内存表数量"))
	if err != nil {
		return nil, err
	}

	// 写入停顿指标
	// pm.writeStallCount, err = meter.Int64Counter("pebble_write_stall_count_total",
	// 	metric.WithDescription("写入停顿次数"))
	// if err != nil {
	// 	return nil, err
	// }

	// pm.writeStallDuration, err = meter.Float64Counter("pebble_write_stall_duration_seconds_total",
	// 	metric.WithDescription("写入停顿总持续时间"))
	// if err != nil {
	// 	return nil, err
	// }

	// 吞吐量指标
	pm.flushThroughput, err = meter.Float64Gauge("pebble_flush_throughput_bytes_per_second",
		metric.WithDescription("刷新操作的吞吐量"))
	if err != nil {
		return nil, err
	}
	pm.flushCount, err = meter.Int64Counter("pebble_flush_count_total",
		metric.WithDescription("刷新操作次数"))
	if err != nil {
		return nil, err
	}
	// 刷新正在处理的数量
	pm.flushNumInProgress, err = meter.Float64Gauge("pebble_flush_num_in_progress",
		metric.WithDescription("刷新正在处理的数量"))
	if err != nil {
		return nil, err
	}

	// 墓碑指标
	pm.tombstoneCount, err = meter.Float64Gauge("pebble_tombstone_count",
		metric.WithDescription("数据库中的墓碑数量"))
	if err != nil {
		return nil, err
	}

	return pm, nil
}

// 更新所有Pebble指标
func (pm *PebbleMetrics) Update(ctx context.Context, db *pebble.DB, attributes ...attribute.KeyValue) {
	metrics := db.Metrics()
	var attrs metric.MeasurementOption
	if len(attributes) > 0 {
		attrs = metric.WithAttributes(attributes...)
	} else {
		attrs = metric.WithAttributes()
	}

	// 更新读写放大指标
	pm.readAmp.Record(ctx, float64(metrics.ReadAmp()), attrs)

	// 计算总写放大
	var totalBytesIn, totalBytesWritten uint64
	for _, level := range metrics.Levels {
		totalBytesIn += level.BytesIn
		totalBytesWritten += level.BytesFlushed + level.BytesCompacted
	}
	if totalBytesIn > 0 {
		pm.writeAmp.Record(ctx, float64(totalBytesWritten)/float64(totalBytesIn), attrs)
	}

	// 更新压缩相关指标
	pm.compactionDebt.Record(ctx, float64(metrics.Compact.EstimatedDebt), attrs)
	pm.compactionCount.Add(ctx, int64(metrics.Compact.Count), attrs)
	pm.inProgressBytes.Record(ctx, float64(metrics.Compact.InProgressBytes), attrs)
	pm.inProgressCount.Record(ctx, float64(metrics.Compact.NumInProgress), attrs)

	// 更新存储空间指标
	pm.diskSpaceUsage.Record(ctx, float64(metrics.DiskSpaceUsage()), attrs)

	// 更新每个层级的表文件统计 - 这里简化为总数
	var totalTablesCount, totalTablesSize int64
	for _, level := range metrics.Levels {
		totalTablesCount += level.NumFiles
		totalTablesSize += level.Size
	}
	pm.tableCount.Record(ctx, float64(totalTablesCount), attrs)
	pm.tableSize.Record(ctx, float64(totalTablesSize), attrs)

	// 更新缓存指标
	pm.blockCacheSize.Record(ctx, float64(metrics.BlockCache.Size), attrs)
	pm.blockCacheHits.Add(ctx, metrics.BlockCache.Hits, attrs)
	pm.blockCacheMisses.Add(ctx, metrics.BlockCache.Misses, attrs)

	// 更新内存使用指标
	pm.memTableSize.Record(ctx, float64(metrics.MemTable.Size), attrs)
	pm.memTableCount.Record(ctx, float64(metrics.MemTable.Count), attrs)

	// 更新吞吐量指标
	pm.flushThroughput.Record(ctx, float64(metrics.Flush.WriteThroughput.Bytes)/float64(metrics.Flush.WriteThroughput.WorkDuration.Seconds()), attrs)
	pm.flushCount.Add(ctx, metrics.Flush.Count, attrs)
	pm.flushNumInProgress.Record(ctx, float64(metrics.Flush.NumInProgress), attrs)

	// 更新墓碑指标
	pm.tombstoneCount.Record(ctx, float64(metrics.Keys.TombstoneCount), attrs)

}
