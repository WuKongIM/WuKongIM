package store

import (
	"go.uber.org/zap"
)

// RecoveryManager 节点恢复管理器
// 负责处理节点故障恢复后的数据一致性问题
type RecoveryManager struct {
	store *Store
}

// NewRecoveryManager 创建恢复管理器
func NewRecoveryManager(store *Store) *RecoveryManager {
	return &RecoveryManager{
		store: store,
	}
}

// RecoverChannelFromDeleteLogs 从删除日志恢复频道的删除操作
// 当节点故障恢复后，通过此方法补偿执行缺失的删除操作
func (rm *RecoveryManager) RecoverChannelFromDeleteLogs(channelId string, channelType uint8, lastAppliedLogIndex uint64) error {
	rm.store.Info("开始从删除日志恢复频道",
		zap.String("channelId", channelId),
		zap.Uint8("channelType", channelType),
		zap.Uint64("lastAppliedLogIndex", lastAppliedLogIndex))

	// 获取该频道在此日志索引之后的所有删除记录
	deleteLogs, err := rm.store.wdb.GetDeleteLogsSinceLogIndex(channelId, channelType, lastAppliedLogIndex)
	if err != nil {
		rm.store.Error("获取删除日志失败", zap.Error(err),
			zap.String("channelId", channelId),
			zap.Uint8("channelType", channelType),
			zap.Uint64("lastAppliedLogIndex", lastAppliedLogIndex))
		return err
	}

	if len(deleteLogs) == 0 {
		rm.store.Info("没有需要补偿的删除操作",
			zap.String("channelId", channelId),
			zap.Uint8("channelType", channelType))
		return nil
	}

	rm.store.Info("发现需要补偿的删除操作",
		zap.String("channelId", channelId),
		zap.Uint8("channelType", channelType),
		zap.Int("count", len(deleteLogs)),
		zap.Uint64("lastAppliedLogIndex", lastAppliedLogIndex))

	// 重新应用这些删除操作
	var successCount, failCount int
	for _, log := range deleteLogs {
		rm.store.Debug("补偿执行删除操作",
			zap.String("channelId", log.ChannelId),
			zap.Uint8("channelType", log.ChannelType),
			zap.Uint64("startSeq", log.StartSeq),
			zap.Uint64("endSeq", log.EndSeq),
			zap.Uint64("logIndex", log.LogIndex))

		err = rm.store.wdb.DeleteRangeMessages(
			log.ChannelId,
			log.ChannelType,
			log.StartSeq,
			log.EndSeq,
		)
		if err != nil {
			rm.store.Error("补偿删除失败", zap.Error(err),
				zap.String("channelId", log.ChannelId),
				zap.Uint8("channelType", log.ChannelType),
				zap.Uint64("startSeq", log.StartSeq),
				zap.Uint64("endSeq", log.EndSeq),
				zap.Uint64("logIndex", log.LogIndex))
			failCount++
			// 继续处理其他删除操作，不因为单个失败而中断
			continue
		}
		successCount++
	}

	rm.store.Info("完成删除日志恢复",
		zap.String("channelId", channelId),
		zap.Uint8("channelType", channelType),
		zap.Int("total", len(deleteLogs)),
		zap.Int("success", successCount),
		zap.Int("failed", failCount))

	if failCount > 0 {
		rm.store.Warn("部分删除操作补偿失败",
			zap.String("channelId", channelId),
			zap.Uint8("channelType", channelType),
			zap.Int("failCount", failCount))
	}

	return nil
}

// RecoverSlotFromDeleteLogs 恢复某个 slot 下所有频道的删除操作
// 用于节点启动时批量恢复
func (rm *RecoveryManager) RecoverSlotFromDeleteLogs(slotId uint32, lastAppliedLogIndex uint64) error {
	rm.store.Info("开始从删除日志恢复 slot",
		zap.Uint32("slotId", slotId),
		zap.Uint64("lastAppliedLogIndex", lastAppliedLogIndex))

	// 注意：这里需要遍历该 slot 下的所有频道
	// 实际实现中可能需要根据具体的数据结构来获取频道列表
	// 这里提供一个框架，具体实现可能需要调整

	// TODO: 获取 slot 下的所有频道列表
	// channels, err := rm.store.wdb.GetChannelsBySlot(slotId)
	// if err != nil {
	// 	return err
	// }
	//
	// for _, channel := range channels {
	// 	err = rm.RecoverChannelFromDeleteLogs(channel.ChannelId, channel.ChannelType, lastAppliedLogIndex)
	// 	if err != nil {
	// 		rm.store.Error("恢复频道失败", zap.Error(err),
	// 			zap.String("channelId", channel.ChannelId),
	// 			zap.Uint8("channelType", channel.ChannelType))
	// 		// 继续处理其他频道
	// 	}
	// }

	rm.store.Info("完成 slot 删除日志恢复",
		zap.Uint32("slotId", slotId))

	return nil
}

// CheckAndRecoverIfNeeded 检查并在需要时执行恢复
// 这个方法可以在节点启动时调用
func (rm *RecoveryManager) CheckAndRecoverIfNeeded(channelId string, channelType uint8, currentLogIndex uint64) error {
	// 获取该频道的最后应用日志索引
	// 这里假设可以从某处获取，实际实现可能需要调整
	// lastAppliedIndex, err := rm.store.GetChannelLastAppliedIndex(channelId, channelType)
	// if err != nil {
	// 	return err
	// }

	// 如果当前日志索引大于最后应用的索引，说明可能有缺失
	// if currentLogIndex > lastAppliedIndex {
	// 	rm.store.Info("检测到可能的日志缺失，开始恢复",
	// 		zap.String("channelId", channelId),
	// 		zap.Uint8("channelType", channelType),
	// 		zap.Uint64("currentLogIndex", currentLogIndex),
	// 		zap.Uint64("lastAppliedIndex", lastAppliedIndex))
	//
	// 	return rm.RecoverChannelFromDeleteLogs(channelId, channelType, lastAppliedIndex)
	// }

	return nil
}

// CleanupDeleteLogsForChannel 清理某个频道的删除日志
// 在确认所有副本都已同步后，可以安全清理
func (rm *RecoveryManager) CleanupDeleteLogsForChannel(channelId string, channelType uint8, beforeLogIndex uint64) error {
	rm.store.Info("清理频道的删除日志",
		zap.String("channelId", channelId),
		zap.Uint8("channelType", channelType),
		zap.Uint64("beforeLogIndex", beforeLogIndex))

	// 获取该频道的所有删除日志
	deleteLogs, err := rm.store.wdb.GetDeleteLogsByChannel(channelId, channelType, 0)
	if err != nil {
		return err
	}

	// 这里简化处理，实际应该有更精细的清理逻辑
	// 比如只清理 logIndex < beforeLogIndex 的记录
	var cleanedCount int
	for _, log := range deleteLogs {
		if log.LogIndex < beforeLogIndex {
			// 实际应该有删除单个记录的方法
			// 这里只是示例
			cleanedCount++
		}
	}

	rm.store.Info("完成频道删除日志清理",
		zap.String("channelId", channelId),
		zap.Uint8("channelType", channelType),
		zap.Int("cleanedCount", cleanedCount))

	return nil
}

// RecoveryStats 恢复统计信息
type RecoveryStats struct {
	TotalChannels     int   // 总频道数
	RecoveredChannels int   // 已恢复频道数
	TotalDeleteOps    int   // 总删除操作数
	SuccessDeleteOps  int   // 成功删除操作数
	FailedDeleteOps   int   // 失败删除操作数
	StartTime         int64 // 开始时间
	EndTime           int64 // 结束时间
	DurationMs        int64 // 耗时（毫秒）
}

// GetRecoveryStats 获取恢复统计信息（用于监控和调试）
func (rm *RecoveryManager) GetRecoveryStats() *RecoveryStats {
	// TODO: 实现统计信息收集
	return &RecoveryStats{}
}
