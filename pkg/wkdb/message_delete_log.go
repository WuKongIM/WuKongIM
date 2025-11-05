package wkdb

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/cockroachdb/pebble"
	"go.uber.org/zap"
)

// MessageDeleteLog 消息删除日志
// 用于记录消息范围删除操作，当节点故障恢复后可以通过此记录补偿执行缺失的删除操作
type MessageDeleteLog struct {
	Id          uint64 // 主键
	ChannelId   string // 频道ID
	ChannelType uint8  // 频道类型
	StartSeq    uint64 // 删除起始序号
	EndSeq      uint64 // 删除结束序号
	LogIndex    uint64 // Raft 日志索引
	DeletedAt   int64  // 删除时间戳（Unix 秒）
}

// MessageDeleteLogDB 消息删除日志数据库接口
type MessageDeleteLogDB interface {
	// SaveDeleteLog 保存删除日志
	SaveDeleteLog(log *MessageDeleteLog) error

	// GetDeleteLogsSinceLogIndex 获取某个频道在指定 Raft 日志索引之后的所有删除记录
	GetDeleteLogsSinceLogIndex(channelId string, channelType uint8, sinceLogIndex uint64) ([]*MessageDeleteLog, error)

	// GetDeleteLogsByChannel 获取某个频道的所有删除记录（用于调试和审计）
	GetDeleteLogsByChannel(channelId string, channelType uint8, limit int) ([]*MessageDeleteLog, error)

	// CleanupOldDeleteLogs 清理指定时间之前的删除记录
	CleanupOldDeleteLogs(beforeTime int64) error

	// GetDeleteLogsCount 获取删除记录总数（用于监控）
	GetDeleteLogsCount() (int64, error)
}

// SaveDeleteLog 保存删除日志
func (wk *wukongDB) SaveDeleteLog(log *MessageDeleteLog) error {
	wk.metrics.SaveDeleteLogAdd(1)

	if log.Id == 0 {
		log.Id = wk.NextPrimaryKey()
	}

	if log.DeletedAt == 0 {
		log.DeletedAt = time.Now().Unix()
	}

	if wk.opts.EnableCost {
		start := time.Now()
		defer func() {
			wk.Info("saveDeleteLog done", zap.Duration("cost", time.Since(start)),
				zap.String("channelId", log.ChannelId),
				zap.Uint8("channelType", log.ChannelType),
				zap.Uint64("startSeq", log.StartSeq),
				zap.Uint64("endSeq", log.EndSeq),
				zap.Uint64("logIndex", log.LogIndex))
		}()
	}

	db := wk.channelBatchDb(log.ChannelId, log.ChannelType)
	batch := db.NewBatch()

	// 写入主表数据
	if err := wk.writeDeleteLogColumns(batch, log); err != nil {
		return err
	}

	// 写入二级索引（按频道）
	indexKey := key.NewMessageDeleteLogSecondIndex(log.ChannelId, log.ChannelType, log.Id)
	batch.Set(indexKey, nil)

	// 写入二级索引（按时间）
	timeIndexKey := key.NewMessageDeleteLogSecondIndexByTime(uint64(log.DeletedAt), log.Id)
	batch.Set(timeIndexKey, nil)

	return batch.CommitWait()
}

// GetDeleteLogsSinceLogIndex 获取某个频道在指定 Raft 日志索引之后的所有删除记录
func (wk *wukongDB) GetDeleteLogsSinceLogIndex(channelId string, channelType uint8, sinceLogIndex uint64) ([]*MessageDeleteLog, error) {
	wk.metrics.GetDeleteLogsSinceLogIndexAdd(1)

	if wk.opts.EnableCost {
		start := time.Now()
		defer func() {
			wk.Info("getDeleteLogsSinceLogIndex done", zap.Duration("cost", time.Since(start)),
				zap.String("channelId", channelId),
				zap.Uint8("channelType", channelType),
				zap.Uint64("sinceLogIndex", sinceLogIndex))
		}()
	}

	// 通过频道二级索引查询
	db := wk.channelDb(channelId, channelType)
	prefix := key.NewMessageDeleteLogChannelSecondIndexPrefix(channelId, channelType)

	iterOpts := &pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: append(prefix, 0xff),
	}

	iter := db.NewIter(iterOpts)
	defer iter.Close()

	var logs []*MessageDeleteLog

	for iter.First(); iter.Valid(); iter.Next() {
		// 从二级索引获取主键
		indexKey := iter.Key()
		if len(indexKey) < key.TableMessageDeleteLog.SecondIndexSize {
			continue
		}

		// 解析主键
		primaryKey := binary.BigEndian.Uint64(indexKey[14:])

		// 读取完整记录
		log, err := wk.loadDeleteLog(db, primaryKey)
		if err != nil {
			wk.Warn("load delete log failed", zap.Error(err), zap.Uint64("primaryKey", primaryKey))
			continue
		}

		// 过滤：只返回 logIndex > sinceLogIndex 的记录
		if log.LogIndex > sinceLogIndex {
			logs = append(logs, log)
		}
	}

	if err := iter.Error(); err != nil {
		return nil, err
	}

	return logs, nil
}

// GetDeleteLogsByChannel 获取某个频道的所有删除记录（用于调试和审计）
func (wk *wukongDB) GetDeleteLogsByChannel(channelId string, channelType uint8, limit int) ([]*MessageDeleteLog, error) {
	wk.metrics.GetDeleteLogsByChannelAdd(1)

	db := wk.channelDb(channelId, channelType)
	prefix := key.NewMessageDeleteLogChannelSecondIndexPrefix(channelId, channelType)

	iterOpts := &pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: append(prefix, 0xff),
	}

	iter := db.NewIter(iterOpts)
	defer iter.Close()

	var logs []*MessageDeleteLog

	for iter.First(); iter.Valid(); iter.Next() {
		// 从二级索引获取主键
		indexKey := iter.Key()
		if len(indexKey) < key.TableMessageDeleteLog.SecondIndexSize {
			continue
		}

		// 解析主键
		primaryKey := binary.BigEndian.Uint64(indexKey[14:])

		// 读取完整记录
		log, err := wk.loadDeleteLog(db, primaryKey)
		if err != nil {
			wk.Warn("load delete log failed", zap.Error(err), zap.Uint64("primaryKey", primaryKey))
			continue
		}

		logs = append(logs, log)

		if limit > 0 && len(logs) >= limit {
			break
		}
	}

	if err := iter.Error(); err != nil {
		return nil, err
	}

	return logs, nil
}

// CleanupOldDeleteLogs 清理指定时间之前的删除记录
func (wk *wukongDB) CleanupOldDeleteLogs(beforeTime int64) error {
	wk.metrics.CleanupOldDeleteLogsAdd(1)

	start := time.Now()
	defer func() {
		wk.Info("cleanupOldDeleteLogs done", zap.Duration("cost", time.Since(start)),
			zap.Int64("beforeTime", beforeTime),
			zap.Time("beforeDate", time.Unix(beforeTime, 0)))
	}()

	// 遍历所有分片数据库
	var totalDeleted int64
	for _, db := range wk.wkdbs {
		deleted, err := wk.cleanupOldDeleteLogsInDB(db.db, beforeTime)
		if err != nil {
			return err
		}
		totalDeleted += deleted
	}

	wk.Info("cleaned up old delete logs", zap.Int64("count", totalDeleted))
	return nil
}

// cleanupOldDeleteLogsInDB 在单个数据库中清理旧记录
func (wk *wukongDB) cleanupOldDeleteLogsInDB(db *pebble.DB, beforeTime int64) (int64, error) {
	prefix := key.NewMessageDeleteLogTimeSecondIndexPrefix()

	iterOpts := &pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: append(prefix, 0xff),
	}

	iter := db.NewIter(iterOpts)
	defer iter.Close()

	var keysToDelete [][]byte
	var deleted int64

	for iter.First(); iter.Valid(); iter.Next() {
		// 解析时间索引键
		indexKey := iter.Key()
		if len(indexKey) < key.TableMessageDeleteLog.SecondIndexSize {
			continue
		}

		// 提取时间戳
		deletedAt := int64(binary.BigEndian.Uint64(indexKey[6:]))

		// 如果时间超过阈值，标记为删除
		if deletedAt < beforeTime {
			// 提取主键
			primaryKey := binary.BigEndian.Uint64(indexKey[14:])

			// 读取完整记录以获取频道信息（用于构建索引键）
			log, err := wk.loadDeleteLog(db, primaryKey)
			if err != nil {
				wk.Warn("load delete log for cleanup failed", zap.Error(err), zap.Uint64("primaryKey", primaryKey))
				continue
			}

			// 收集所有要删除的键
			// 1. 时间索引
			keysToDelete = append(keysToDelete, append([]byte(nil), indexKey...))

			// 2. 频道索引
			channelIndexKey := key.NewMessageDeleteLogSecondIndex(log.ChannelId, log.ChannelType, primaryKey)
			keysToDelete = append(keysToDelete, channelIndexKey)

			// 3. 主表数据（所有列）
			for _, col := range [][]byte{
				key.TableMessageDeleteLog.Column.ChannelId[:],
				key.TableMessageDeleteLog.Column.ChannelType[:],
				key.TableMessageDeleteLog.Column.StartSeq[:],
				key.TableMessageDeleteLog.Column.EndSeq[:],
				key.TableMessageDeleteLog.Column.LogIndex[:],
				key.TableMessageDeleteLog.Column.DeletedAt[:],
			} {
				columnKey := key.NewMessageDeleteLogColumnKey(primaryKey, [2]byte{col[0], col[1]})
				keysToDelete = append(keysToDelete, columnKey)
			}

			deleted++
		}
	}

	if err := iter.Error(); err != nil {
		return 0, err
	}

	// 批量删除
	if len(keysToDelete) > 0 {
		batch := db.NewBatch()
		for _, k := range keysToDelete {
			if err := batch.Delete(k, wk.noSync); err != nil {
				return 0, err
			}
		}
		if err := batch.Commit(wk.sync); err != nil {
			return 0, err
		}
	}

	return deleted, nil
}

// GetDeleteLogsCount 获取删除记录总数（用于监控）
func (wk *wukongDB) GetDeleteLogsCount() (int64, error) {
	var count int64

	// 遍历所有分片数据库
	for _, db := range wk.wkdbs {
		prefix := key.NewMessageDeleteLogTimeSecondIndexPrefix()

		iterOpts := &pebble.IterOptions{
			LowerBound: prefix,
			UpperBound: append(prefix, 0xff),
		}

		iter := db.db.NewIter(iterOpts)
		for iter.First(); iter.Valid(); iter.Next() {
			count++
		}
		iter.Close()

		if err := iter.Error(); err != nil {
			return 0, err
		}
	}

	return count, nil
}

// writeDeleteLogColumns 写入删除日志的所有列
func (wk *wukongDB) writeDeleteLogColumns(batch *Batch, log *MessageDeleteLog) error {
	// ChannelId
	batch.Set(
		key.NewMessageDeleteLogColumnKey(log.Id, key.TableMessageDeleteLog.Column.ChannelId),
		[]byte(log.ChannelId),
	)

	// ChannelType
	batch.Set(
		key.NewMessageDeleteLogColumnKey(log.Id, key.TableMessageDeleteLog.Column.ChannelType),
		[]byte{log.ChannelType},
	)

	// StartSeq
	startSeqBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(startSeqBytes, log.StartSeq)
	batch.Set(
		key.NewMessageDeleteLogColumnKey(log.Id, key.TableMessageDeleteLog.Column.StartSeq),
		startSeqBytes,
	)

	// EndSeq
	endSeqBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(endSeqBytes, log.EndSeq)
	batch.Set(
		key.NewMessageDeleteLogColumnKey(log.Id, key.TableMessageDeleteLog.Column.EndSeq),
		endSeqBytes,
	)

	// LogIndex
	logIndexBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(logIndexBytes, log.LogIndex)
	batch.Set(
		key.NewMessageDeleteLogColumnKey(log.Id, key.TableMessageDeleteLog.Column.LogIndex),
		logIndexBytes,
	)

	// DeletedAt
	deletedAtBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(deletedAtBytes, uint64(log.DeletedAt))
	batch.Set(
		key.NewMessageDeleteLogColumnKey(log.Id, key.TableMessageDeleteLog.Column.DeletedAt),
		deletedAtBytes,
	)

	return nil
}

// loadDeleteLog 从数据库加载完整的删除日志记录
func (wk *wukongDB) loadDeleteLog(db *pebble.DB, primaryKey uint64) (*MessageDeleteLog, error) {
	log := &MessageDeleteLog{
		Id: primaryKey,
	}

	// 读取 ChannelId
	channelIdKey := key.NewMessageDeleteLogColumnKey(primaryKey, key.TableMessageDeleteLog.Column.ChannelId)
	channelIdData, closer, err := db.Get(channelIdKey)
	if err != nil {
		return nil, fmt.Errorf("get channelId failed: %w", err)
	}
	log.ChannelId = string(channelIdData)
	closer.Close()

	// 读取 ChannelType
	channelTypeKey := key.NewMessageDeleteLogColumnKey(primaryKey, key.TableMessageDeleteLog.Column.ChannelType)
	channelTypeData, closer, err := db.Get(channelTypeKey)
	if err != nil {
		return nil, fmt.Errorf("get channelType failed: %w", err)
	}
	log.ChannelType = channelTypeData[0]
	closer.Close()

	// 读取 StartSeq
	startSeqKey := key.NewMessageDeleteLogColumnKey(primaryKey, key.TableMessageDeleteLog.Column.StartSeq)
	startSeqData, closer, err := db.Get(startSeqKey)
	if err != nil {
		return nil, fmt.Errorf("get startSeq failed: %w", err)
	}
	log.StartSeq = binary.BigEndian.Uint64(startSeqData)
	closer.Close()

	// 读取 EndSeq
	endSeqKey := key.NewMessageDeleteLogColumnKey(primaryKey, key.TableMessageDeleteLog.Column.EndSeq)
	endSeqData, closer, err := db.Get(endSeqKey)
	if err != nil {
		return nil, fmt.Errorf("get endSeq failed: %w", err)
	}
	log.EndSeq = binary.BigEndian.Uint64(endSeqData)
	closer.Close()

	// 读取 LogIndex
	logIndexKey := key.NewMessageDeleteLogColumnKey(primaryKey, key.TableMessageDeleteLog.Column.LogIndex)
	logIndexData, closer, err := db.Get(logIndexKey)
	if err != nil {
		return nil, fmt.Errorf("get logIndex failed: %w", err)
	}
	log.LogIndex = binary.BigEndian.Uint64(logIndexData)
	closer.Close()

	// 读取 DeletedAt
	deletedAtKey := key.NewMessageDeleteLogColumnKey(primaryKey, key.TableMessageDeleteLog.Column.DeletedAt)
	deletedAtData, closer, err := db.Get(deletedAtKey)
	if err != nil {
		return nil, fmt.Errorf("get deletedAt failed: %w", err)
	}
	log.DeletedAt = int64(binary.BigEndian.Uint64(deletedAtData))
	closer.Close()

	return log, nil
}
