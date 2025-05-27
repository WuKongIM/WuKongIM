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
	"github.com/cockroachdb/pebble/v2"
	"github.com/lni/goutils/syncutil"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

var _ DB = (*wukongDB)(nil)

type wukongDB struct {
	dbs      []*pebble.DB
	wkdbs    []*BatchDB
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

	metrics trace.IDBMetrics

	channelSeqCache *channelSeqCache

	h hash.Hash32
}

func NewWukongDB(opts *Options) DB {
	prmaryKeyGen, err := snowflake.NewNode(int64(opts.NodeId))
	if err != nil {
		panic(err)
	}

	var metrics trace.IDBMetrics
	if trace.GlobalTrace != nil {
		metrics = trace.GlobalTrace.Metrics.DB()
	} else {
		metrics = trace.NewDBMetrics()
	}

	endian := binary.BigEndian

	cancelCtx, cancelFunc := context.WithCancel(context.Background())
	return &wukongDB{
		opts:            opts,
		shardNum:        uint32(opts.ShardNum),
		prmaryKeyGen:    prmaryKeyGen,
		endian:          endian,
		cancelCtx:       cancelCtx,
		cancelFunc:      cancelFunc,
		metrics:         metrics,
		channelSeqCache: newChannelSeqCache(10000, endian),
		h:               fnv.New32(),
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

		wkdb := NewBatchDB(i, db)
		wkdb.Start()
		wk.wkdbs = append(wk.wkdbs, wkdb)
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

	for _, wkd := range wk.wkdbs {
		wkd.Stop()
	}
	wk.dblock.stop()
	return nil
}

func (wk *wukongDB) shardDB(v string) *pebble.DB {
	shardId := wk.shardId(v)
	return wk.dbs[shardId]
}

func (wk *wukongDB) sharedBatchDB(v string) *BatchDB {
	shardId := wk.shardId(v)
	return wk.wkdbs[shardId]
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

func (wk *wukongDB) shardBatchDBById(id uint32) *BatchDB {
	return wk.wkdbs[id]
}

func (wk *wukongDB) defaultShardDB() *pebble.DB {
	return wk.dbs[0]
}

func (wk *wukongDB) defaultShardBatchDB() *BatchDB {
	return wk.wkdbs[0]
}

func (wk *wukongDB) channelSlotId(channelId string) uint32 {
	return wkutil.GetSlotNum(int(wk.opts.SlotCount), channelId)
}

func (wk *wukongDB) collectMetricsLoop() {
	tk := time.NewTicker(time.Second * 1)
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

/*
*

 1. 压缩健康度 (Compaction Health):
    Compact.EstimatedDebt: 极其重要。表示待压缩的数据量。如果这个值持续增长，说明写入速度超过了压缩速度，可能导致写放大增加和性能下降。
    Levels[0].NumFiles: L0 层的文件数量。L0 文件过多会显著增加读放大，是压缩跟不上的一个明显信号。
    Levels[0].Score: L0 层的压缩得分。通常得分 > 1 就需要进行 L0->L1 的压缩。持续高分也表示压缩压力大。
    (可选) Compact.NumInProgress: 当前正在进行的压缩任务数。可以辅助判断压缩是否繁忙。

 2. 写入停顿 (Write Stalls):
    WriteStallCount, WriteStallDuration: 非常重要。记录了因等待 MemTable 刷盘或等待 Compaction 释放空间而导致写入暂停的次数和总时长。频繁或长时间的停顿直接影响写入性能。(注意：你当前的代码似乎没有采集这两个 v2 指标，强烈建议加上)

 3. 缓存性能 (Cache Performance):
    BlockCache.Hits, BlockCache.Misses: 块缓存的命中和未命中次数。计算命中率 (Hits / (Hits + Misses)) 是衡量读取性能的关键，高命中率意味着更少的磁盘 I/O。(你当前只记录了 Size/Count，建议加上 Hits/Misses)
    TableCache.Hits, TableCache.Misses: 表缓存（用于缓存打开的 SSTable 文件描述符）的命中和未命中。计算命中率也很重要，低命中率可能导致文件打开/关闭开销增加。(同样建议加上 Hits/Misses)

 4. 资源使用 (Resource Usage):

    DiskSpaceUsage(): 数据库占用的总磁盘空间。监控其增长趋势。
    WAL.Size: 预写日志的总大小。过大的 WAL 可能影响节点恢复时间。
    MemTable.Size, MemTable.Count, MemTable.ZombieSize, MemTable.ZombieCount: 内存表的大小和数量（包括活跃的和等待刷盘的）。监控这些可以了解内存使用情况和刷盘压力。

 5. 效率指标 (Efficiency):
    WriteAmp(): 非常推荐。写放大系数，表示用户写入 1 字节数据，实际物理写入多少字节。是衡量 LSM 树写入效率的核心指标。(这是一个方法调用，你当前的代码没有调用它，建议加上)

*
*/
func (wk *wukongDB) collectMetrics() {
	metrics := trace.GlobalTrace.Metrics.Pebble()
	for i := uint32(0); i < uint32(wk.shardNum); i++ {
		db := wk.dbs[i]

		metrics.Update(context.Background(), db, attribute.String("db", "wukongimdb"), attribute.String("shard", fmt.Sprintf("shard%03d", i)))

	}
}

func (wk *wukongDB) NextPrimaryKey() uint64 {
	return uint64(wk.prmaryKeyGen.Generate().Int64())
}

// 批量提交
func Commits(bs []*Batch) error {
	if len(bs) == 0 {
		return nil
	}
	newBatchs := groupBatch(bs)
	if len(newBatchs) == 1 {
		return newBatchs[0].CommitWait()
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	g, _ := errgroup.WithContext(timeoutCtx)
	g.SetLimit(200)
	for _, b := range newBatchs {
		b1 := b
		g.Go(func() error {
			return b1.CommitWait()
		})
	}
	return g.Wait()
}

// 将batch集合操作按照db进行聚合到一起

func groupBatch(bs []*Batch) []*Batch {
	newBatchs := make([]*Batch, 0, len(bs))
	for _, b := range bs {
		exist := false
		for _, nb := range newBatchs {
			if nb.db == b.db {
				exist = true
				nb.setKvs = append(nb.setKvs, b.setKvs...)
				nb.delKvs = append(nb.delKvs, b.delKvs...)
				nb.delRangeKvs = append(nb.delRangeKvs, b.delRangeKvs...)
				break
			}
		}
		if !exist {
			newBatchs = append(newBatchs, b)
		}
	}
	return newBatchs
}

type BatchDB struct {
	db *pebble.DB

	batchChan chan *Batch

	stopper *syncutil.Stopper
	Index   int
}

func NewBatchDB(index int, db *pebble.DB) *BatchDB {
	return &BatchDB{
		batchChan: make(chan *Batch, 4000),
		stopper:   syncutil.NewStopper(),
		db:        db,
		Index:     index,
	}
}

func (wk *BatchDB) NewBatch() *Batch {

	return &Batch{
		db: wk,
	}
}

func (wk *BatchDB) Start() {
	for i := 0; i < 1; i++ {
		wk.stopper.RunWorker(wk.loop)
	}
}

func (wk *BatchDB) Stop() {
	wk.stopper.Stop()
}

func (wk *BatchDB) loop() {
	batchSize := 100
	done := false
	batches := make([]*Batch, 0, batchSize)
	for {
		select {
		case bt := <-wk.batchChan:
			// 获取所有的batch
			batches = append(batches, bt)
			for !done {
				select {
				case b := <-wk.batchChan:
					batches = append(batches, b)
					if len(batches) >= batchSize {
						done = true
					}
				default:
					done = true
				}
			}
			wk.executeBatch(batches) // 批量执行
			batches = batches[:0]
			done = false

		case <-wk.stopper.ShouldStop():
			return
		}
	}
}

func (wk *BatchDB) executeBatch(bs []*Batch) {

	bt := wk.db.NewBatch()
	defer bt.Close()

	// start := time.Now()

	for _, b := range bs {

		// fmt.Println("batch-->:", b.String())

		// trace.GlobalTrace.Metrics.DB().SetAdd(int64(len(b.setKvs)))
		// trace.GlobalTrace.Metrics.DB().DeleteAdd(int64(len(b.delKvs)))
		// trace.GlobalTrace.Metrics.DB().DeleteRangeAdd(int64(len(b.delRangeKvs)))

		for _, kv := range b.delKvs {
			if err := bt.Delete(kv.key, pebble.NoSync); err != nil {
				b.err = err
				break
			}
		}

		for _, kv := range b.delRangeKvs {
			if err := bt.DeleteRange(kv.key, kv.val, pebble.NoSync); err != nil {
				b.err = err
				break
			}
		}

		for _, kv := range b.setKvs {
			if err := bt.Set(kv.key, kv.val, pebble.NoSync); err != nil {
				b.err = err
				break
			}
		}

	}
	// trace.GlobalTrace.Metrics.DB().CommitAdd(1)
	err := bt.Commit(pebble.Sync)
	if err != nil {
		for _, b := range bs {
			b.err = err
			if b.waitC != nil {
				b.waitC <- err
			}
		}
		return
	}

	// end := time.Since(start)
	// fmt.Println("executeBatch耗时--->", end, len(bs))

	for _, b := range bs {
		if b.waitC != nil {
			b.waitC <- b.err
		}
		// 释放资源
		b.release()
	}

}

type Batch struct {
	db          *BatchDB
	setKvs      []kv
	delKvs      []kv
	delRangeKvs []kv
	waitC       chan error
	err         error
}

func (b *Batch) Set(key, value []byte) {
	// 预分配切片容量
	if cap(b.setKvs) == 0 {
		b.setKvs = make([]kv, 0, 100) // 假设预估大小为100
	}
	b.setKvs = append(b.setKvs, kv{
		key: key,
		val: value,
	})
}

func (b *Batch) Delete(key []byte) {
	b.delKvs = append(b.delKvs, kv{
		key: key,
		val: nil,
	})
}

func (b *Batch) DeleteRange(start, end []byte) {
	b.delRangeKvs = append(b.delRangeKvs, kv{
		key: start,
		val: end,
	})
}

func (b *Batch) Commit() error {
	b.db.batchChan <- b
	return nil
}

func (b *Batch) CommitWait() error {
	b.waitC = make(chan error, 1)
	b.db.batchChan <- b
	return <-b.waitC
}

func (b *Batch) release() {
	b.setKvs = nil
	b.delKvs = nil
	b.delRangeKvs = nil
	b.waitC = nil
	b.err = nil
}

func (b *Batch) String() string {
	return fmt.Sprintf("setKvs:%d, delKvs:%d, delRangeKvs:%d", len(b.setKvs), len(b.delKvs), len(b.delRangeKvs))
}

func (b *Batch) DbIndex() int {
	return b.db.Index
}

type kv struct {
	key []byte
	val []byte
}
