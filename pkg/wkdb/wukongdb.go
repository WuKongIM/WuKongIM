package wkdb

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash"
	"hash/fnv"
	"path/filepath"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/bwmarrin/snowflake"
	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
)

var _ DB = (*wukongDB)(nil)

type wukongDB struct {
	dbs      []*pebble.DB
	shardNum uint32 // 分区数量，这个一但设置就不能修改
	opts     *Options
	sync     *pebble.WriteOptions
	endian   binary.ByteOrder
	wklog.Log
	prmaryKeyGen *snowflake.Node // 消息ID生成器
	noSync       *pebble.WriteOptions
	dblock       *dblock
	cancelCtx    context.Context
	cancelFunc   context.CancelFunc

	h hash.Hash32
}

func NewWukongDB(opts *Options) DB {
	prmaryKeyGen, err := snowflake.NewNode(int64(opts.NodeId))
	if err != nil {
		panic(err)
	}
	cancelCtx, cancelFunc := context.WithCancel(context.Background())
	return &wukongDB{
		opts:         opts,
		shardNum:     uint32(opts.ShardNum),
		prmaryKeyGen: prmaryKeyGen,
		endian:       binary.BigEndian,
		cancelCtx:    cancelCtx,
		cancelFunc:   cancelFunc,
		h:            fnv.New32(),
		sync: &pebble.WriteOptions{
			Sync: true,
		},
		noSync: &pebble.WriteOptions{
			Sync: false,
		},
		Log:    wklog.NewWKLog("wukongDB"),
		dblock: newDBLock(),
	}
}

func (wk *wukongDB) defaultPebbleOptions() *pebble.Options {
	blockSize := 32 * 1024
	sz := 16 * 1024 * 1024
	levelSizeMultiplier := 2

	lopts := make([]pebble.LevelOptions, 0)
	var numOfLevels int64 = 7
	for l := int64(0); l < numOfLevels; l++ {
		opt := pebble.LevelOptions{
			// Compression:    pebble.NoCompression,
			BlockSize:      blockSize,
			TargetFileSize: 16 * 1024 * 1024,
		}
		sz = sz * levelSizeMultiplier
		lopts = append(lopts, opt)
	}
	return &pebble.Options{
		Levels:             lopts,
		FormatMajorVersion: pebble.FormatNewest,
		// 控制写缓冲区的大小。较大的写缓冲区可以减少磁盘写入次数，但会占用更多内存。
		MemTableSize: wk.opts.MemTableSize,
		// 当队列中的MemTables的大小超过 MemTableStopWritesThreshold*MemTableSize 时，将停止写入，
		// 直到被刷到磁盘，这个值不能小于2
		MemTableStopWritesThreshold: 4,
		// MANIFEST 文件的大小
		MaxManifestFileSize:       128 * 1024 * 1024,
		LBaseMaxBytes:             4 * 1024 * 1024 * 1024,
		L0CompactionFileThreshold: 8,
		L0StopWritesThreshold:     24,
	}
}

func (wk *wukongDB) Open() error {

	wk.dblock.start()

	opts := wk.defaultPebbleOptions()
	for i := 0; i < int(wk.shardNum); i++ {

		db, err := pebble.Open(filepath.Join(wk.opts.DataDir, "wukongimdb", fmt.Sprintf("shard%03d", i)), opts)
		if err != nil {
			return err
		}
		wk.dbs = append(wk.dbs, db)
	}

	go wk.collectMetricsLoop()

	return nil
}

func (wk *wukongDB) Close() error {
	wk.cancelFunc()
	for _, db := range wk.dbs {
		if err := db.Close(); err != nil {
			wk.Error("close db error", zap.Error(err))
		}
	}
	wk.dblock.stop()
	return nil
}

func (wk *wukongDB) shardDB(v string) *pebble.DB {
	shardId := wk.shardId(v)
	return wk.dbs[shardId]
}

func (wk *wukongDB) shardId(v string) uint32 {
	if v == "" {
		wk.Panic("shardId key is empty")
	}
	if wk.opts.ShardNum == 1 {
		return 0
	}
	h := fnv.New32()
	h.Write([]byte(v))

	return h.Sum32() % wk.shardNum
}

func (wk *wukongDB) shardDBById(id uint32) *pebble.DB {
	return wk.dbs[id]
}

func (wk *wukongDB) defaultShardDB() *pebble.DB {
	return wk.dbs[0]
}

func (wk *wukongDB) channelSlotId(channelId string, channelType uint8) uint32 {
	return wkutil.GetSlotNum(int(wk.opts.SlotCount), channelId)
}

func (wk *wukongDB) collectMetricsLoop() {
	tk := time.NewTicker(time.Second * 5)
	defer tk.Stop()

	for {
		select {
		case <-tk.C:
			wk.collectMetrics()
		case <-wk.cancelCtx.Done():
			return
		}
	}
}

func (wk *wukongDB) collectMetrics() {

	for i := uint32(0); i < uint32(wk.shardNum); i++ {
		ms := wk.dbs[i].Metrics()

		// ========== compact 压缩相关 ==========
		trace.GlobalTrace.Metrics.DB().CompactTotalCountSet(i, ms.Compact.Count)
		trace.GlobalTrace.Metrics.DB().CompactDefaultCountSet(i, ms.Compact.DefaultCount)
		trace.GlobalTrace.Metrics.DB().CompactDeleteOnlyCountSet(i, ms.Compact.DeleteOnlyCount)
		trace.GlobalTrace.Metrics.DB().CompactElisionOnlyCountSet(i, ms.Compact.ElisionOnlyCount)
		trace.GlobalTrace.Metrics.DB().CompactEstimatedDebtSet(i, int64(ms.Compact.EstimatedDebt))
		trace.GlobalTrace.Metrics.DB().CompactInProgressBytesSet(i, ms.Compact.InProgressBytes)
		trace.GlobalTrace.Metrics.DB().CompactMarkedFilesSet(i, int64(ms.Compact.MarkedFiles))
		trace.GlobalTrace.Metrics.DB().CompactMoveCountSet(i, ms.Compact.MoveCount)
		trace.GlobalTrace.Metrics.DB().CompactMultiLevelCount(i, ms.Compact.MultiLevelCount)
		trace.GlobalTrace.Metrics.DB().CompactNumInProgressSet(i, ms.Compact.NumInProgress)
		trace.GlobalTrace.Metrics.DB().CompactReadCountSet(i, ms.Compact.ReadCount)
		trace.GlobalTrace.Metrics.DB().CompactRewriteCountSet(i, ms.Compact.RewriteCount)

		// ========== flush 相关 ==========
		trace.GlobalTrace.Metrics.DB().FlushCountAdd(i, int64(ms.Flush.Count))
		trace.GlobalTrace.Metrics.DB().FlushBytesAdd(i, ms.Flush.WriteThroughput.Bytes)
		trace.GlobalTrace.Metrics.DB().FlushNumInProgressAdd(i, ms.Flush.NumInProgress)
		trace.GlobalTrace.Metrics.DB().FlushAsIngestCountAdd(i, int64(ms.Flush.AsIngestCount))
		trace.GlobalTrace.Metrics.DB().FlushAsIngestTableCountAdd(i, int64(ms.Flush.AsIngestTableCount))
		trace.GlobalTrace.Metrics.DB().FlushAsIngestBytesAdd(i, int64(ms.Flush.AsIngestBytes))

		// ========== memtable 内存表相关 ==========
		trace.GlobalTrace.Metrics.DB().MemTableCountSet(i, int64(ms.MemTable.Count))
		trace.GlobalTrace.Metrics.DB().MemTableSizeSet(i, int64(ms.MemTable.Size))
		trace.GlobalTrace.Metrics.DB().MemTableZombieSizeSet(i, int64(ms.MemTable.ZombieSize))
		trace.GlobalTrace.Metrics.DB().MemTableZombieCountSet(i, ms.MemTable.ZombieCount)

		// ========== Snapshots 镜像相关 ==========
		trace.GlobalTrace.Metrics.DB().SnapshotsCountSet(i, int64(ms.Snapshots.Count))

		// ========== TableCache 相关 ==========
		trace.GlobalTrace.Metrics.DB().TableCacheSizeSet(i, ms.TableCache.Size)
		trace.GlobalTrace.Metrics.DB().TableCacheCountSet(i, ms.TableCache.Count)
		trace.GlobalTrace.Metrics.DB().TableItersCountSet(i, ms.TableIters)

		// ========== WAL 相关 ==========
		trace.GlobalTrace.Metrics.DB().WALFilesCountSet(i, ms.WAL.Files)
		trace.GlobalTrace.Metrics.DB().WALSizeSet(i, int64(ms.WAL.Size))
		trace.GlobalTrace.Metrics.DB().WALPhysicalSizeSet(i, int64(ms.WAL.PhysicalSize))
		trace.GlobalTrace.Metrics.DB().WALObsoleteFilesCountSet(i, ms.WAL.ObsoleteFiles)
		trace.GlobalTrace.Metrics.DB().WALObsoletePhysicalSizeSet(i, int64(ms.WAL.ObsoletePhysicalSize))
		trace.GlobalTrace.Metrics.DB().WALBytesInSet(i, int64(ms.WAL.BytesIn))
		trace.GlobalTrace.Metrics.DB().WALBytesWrittenSet(i, int64(ms.WAL.BytesWritten))

		// ========== Write 相关 ==========
		trace.GlobalTrace.Metrics.DB().LogWriterBytesSet(i, ms.LogWriter.WriteThroughput.Bytes)

		trace.GlobalTrace.Metrics.DB().DiskSpaceUsageSet(i, int64(ms.DiskSpaceUsage()))

		// ========== level 相关 ==========

		trace.GlobalTrace.Metrics.DB().LevelNumFilesSet(i, ms.Total().NumFiles)
		trace.GlobalTrace.Metrics.DB().LevelFileSizeSet(i, int64(ms.Total().Size))
		trace.GlobalTrace.Metrics.DB().LevelCompactScoreSet(i, int64(ms.Total().Score))
		trace.GlobalTrace.Metrics.DB().LevelBytesInSet(i, int64(ms.Total().BytesIn))
		trace.GlobalTrace.Metrics.DB().LevelBytesIngestedSet(i, int64(ms.Total().BytesIngested))
		trace.GlobalTrace.Metrics.DB().LevelBytesMovedSet(i, int64(ms.Total().BytesMoved))
		trace.GlobalTrace.Metrics.DB().LevelBytesReadSet(i, int64(ms.Total().BytesRead))
		trace.GlobalTrace.Metrics.DB().LevelBytesCompactedSet(i, int64(ms.Total().BytesCompacted))
		trace.GlobalTrace.Metrics.DB().LevelBytesFlushedSet(i, int64(ms.Total().BytesFlushed))
		trace.GlobalTrace.Metrics.DB().LevelTablesCompactedSet(i, int64(ms.Total().TablesCompacted))
		trace.GlobalTrace.Metrics.DB().LevelTablesFlushedSet(i, int64(ms.Total().TablesFlushed))
		trace.GlobalTrace.Metrics.DB().LevelTablesIngestedSet(i, int64(ms.Total().TablesIngested))
		trace.GlobalTrace.Metrics.DB().LevelTablesMovedSet(i, int64(ms.Total().TablesMoved))

	}
}

func (wk *wukongDB) NextPrimaryKey() uint64 {
	return uint64(wk.prmaryKeyGen.Generate().Int64())
}
