package raft

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type queue struct {
	logs []types.Log
	wklog.Log

	// 存储日志偏移下标，比如日志 1 2 3 4 5 6 7 8 9，存储的日志偏移是6 表示1 2 3 4 5 6已经存储
	storedIndex    uint64
	lastLogIndex   uint64 // 最后一条日志下标
	committedIndex uint64 // 已提交的日志下标
	appending      bool   // 是否在追加中
	applying       bool   // 是否在应用中
	appliedIndex   uint64
}

func newQueue(key string, appliedLogIndex, lastLogIndex uint64) *queue {

	q := &queue{
		Log:            wklog.NewWKLog(fmt.Sprintf("queue[%s]", key)),
		storedIndex:    lastLogIndex,
		lastLogIndex:   lastLogIndex,
		committedIndex: appliedLogIndex,
		appliedIndex:   appliedLogIndex,
	}
	return q
}

func (r *queue) append(incomingLogs ...types.Log) error {
	// 1. 参数验证：检查是否有日志需要追加
	if len(incomingLogs) == 0 {
		r.Debug("append operation skipped: no logs provided")
		return nil
	}

	// 2. 验证输入日志序列的连续性
	for i := 1; i < len(incomingLogs); i++ {
		previousLogIndex := incomingLogs[i-1].Index
		currentLogIndex := incomingLogs[i].Index

		if currentLogIndex != previousLogIndex+1 {
			r.Error("incoming log sequence is not continuous",
				zap.Uint64("previousLogIndex", previousLogIndex),
				zap.Uint64("currentLogIndex", currentLogIndex),
				zap.Uint64("expectedIndex", previousLogIndex+1),
				zap.Int("brokenAtPosition", i),
				zap.Int("totalIncomingLogs", len(incomingLogs)))
			return fmt.Errorf("incoming log sequence broken at position %d: expected index %d, got %d",
				i, previousLogIndex+1, currentLogIndex)
		}
	}

	// 3. 提取关键索引信息
	incomingFirstIndex := incomingLogs[0].Index
	incomingLastIndex := incomingLogs[len(incomingLogs)-1].Index
	incomingLogCount := len(incomingLogs)
	expectedPrevIndex := incomingFirstIndex - 1 // 新日志序列期望的前一个索引

	r.Debug("attempting to append logs",
		zap.Uint64("queueLastIndex", r.lastLogIndex),
		zap.Uint64("incomingFirstIndex", incomingFirstIndex),
		zap.Uint64("incomingLastIndex", incomingLastIndex),
		zap.Int("incomingLogCount", incomingLogCount),
		zap.Uint64("expectedPrevIndex", expectedPrevIndex))

	// 4. 场景1：正常的连续追加
	if expectedPrevIndex == r.lastLogIndex {
		r.logs = append(r.logs, incomingLogs...)
		r.lastLogIndex = incomingLastIndex

		r.Debug("logs appended successfully",
			zap.Uint64("newQueueLastIndex", r.lastLogIndex),
			zap.Uint64("appendedRangeFrom", incomingFirstIndex),
			zap.Uint64("appendedRangeTo", incomingLastIndex),
			zap.Int("appendedCount", incomingLogCount),
			zap.Int("totalQueueSize", len(r.logs)))
		return nil
	}

	// 5. 场景2：日志重叠或冲突处理
	if incomingFirstIndex <= r.lastLogIndex {
		overlapSize := r.lastLogIndex - incomingFirstIndex + 1

		r.Warn("detected log overlap or conflict",
			zap.Uint64("queueLastIndex", r.lastLogIndex),
			zap.Uint64("incomingFirstIndex", incomingFirstIndex),
			zap.Uint64("incomingLastIndex", incomingLastIndex),
			zap.Uint64("overlapSize", overlapSize),
			zap.Int("incomingLogCount", incomingLogCount))

		// 5a. 完全重复的日志：新日志完全在已有范围内
		if incomingLastIndex <= r.lastLogIndex {
			r.Info("ignoring completely duplicate logs",
				zap.Uint64("incomingRangeFrom", incomingFirstIndex),
				zap.Uint64("incomingRangeTo", incomingLastIndex),
				zap.Uint64("queueRangeFrom", r.storedIndex+1),
				zap.Uint64("queueRangeTo", r.lastLogIndex),
				zap.String("action", "no_change_needed"))
			return nil
		}

		// 5b. 部分重叠：需要截断现有日志并追加新日志
		truncateToIndex := incomingFirstIndex - 1
		originalQueueSize := len(r.logs)

		r.Warn("resolving log conflict by truncation",
			zap.Uint64("truncateToIndex", truncateToIndex),
			zap.Uint64("originalQueueLastIndex", r.lastLogIndex),
			zap.Int("originalQueueSize", originalQueueSize),
			zap.String("action", "truncate_and_reappend"))

		r.truncateLogTo(truncateToIndex)

		// 递归调用以追加新日志（此时应该是连续的）
		return r.append(incomingLogs...)
	}

	// 6. 场景3：日志间隙检测
	if incomingFirstIndex > r.lastLogIndex+1 {
		gapSize := incomingFirstIndex - r.lastLogIndex - 1

		r.Error("detected log gap - cannot append non-continuous logs",
			zap.Uint64("queueLastIndex", r.lastLogIndex),
			zap.Uint64("expectedNextIndex", r.lastLogIndex+1),
			zap.Uint64("incomingFirstIndex", incomingFirstIndex),
			zap.Uint64("gapSize", gapSize),
			zap.Uint64("missingRangeFrom", r.lastLogIndex+1),
			zap.Uint64("missingRangeTo", incomingFirstIndex-1),
			zap.Int("incomingLogCount", incomingLogCount))

		return fmt.Errorf("log gap detected: queue ends at %d, incoming starts at %d (missing %d logs from %d to %d)",
			r.lastLogIndex, incomingFirstIndex, gapSize, r.lastLogIndex+1, incomingFirstIndex-1)
	}

	// 7. 不应该到达的代码路径
	r.Error("unexpected append scenario encountered",
		zap.Uint64("queueLastIndex", r.lastLogIndex),
		zap.Uint64("expectedPrevIndex", expectedPrevIndex),
		zap.Uint64("incomingFirstIndex", incomingFirstIndex),
		zap.Uint64("incomingLastIndex", incomingLastIndex),
		zap.Int("incomingLogCount", incomingLogCount),
		zap.String("error", "logic_error_please_investigate"))

	return fmt.Errorf("unexpected append scenario: queue_last=%d, incoming_first=%d, incoming_last=%d, count=%d",
		r.lastLogIndex, incomingFirstIndex, incomingLastIndex, incomingLogCount)
}

func (r *queue) hasNextStoreLogs() bool {
	if r.appending {
		return false
	}
	return r.storedIndex < r.lastLogIndex
}

func (r *queue) nextStoreLogs(maxSize int) []types.Log {
	if len(r.logs) == 0 {
		return nil
	}
	endIndex := r.lastLogIndex - r.storedIndex
	if endIndex > uint64(len(r.logs)) {
		r.Panic("nextAppendLogs endIndex is out of bound", zap.Uint64("endIndex", endIndex), zap.Int("logsLen", len(r.logs)))
	}
	if maxSize != 0 && endIndex > uint64(maxSize) {
		endIndex = uint64(maxSize)
	}
	r.appending = true
	return r.logs[:endIndex]
}

func (r *queue) hasNextApplyLogs() bool {
	if r.applying {
		return false
	}
	return r.appliedIndex < r.committedIndex
}

func (r *queue) nextApplyLogs() (uint64, uint64) {
	if r.applying {
		return 0, 0
	}
	r.applying = true
	if r.appliedIndex > r.committedIndex {
		return 0, 0
	}

	return r.appliedIndex + 1, r.committedIndex + 1
}

// storeTo 存储日志到指定日志下标，比如storeTo(6)，如果日志是1 2 3 4 5 6 7 8 9，截取后是7 8 9
func (r *queue) storeTo(logIndex uint64) {
	r.appending = false
	if logIndex <= r.storedIndex {
		return
	}
	if logIndex > r.lastLogIndex {
		r.Panic("appendTo logIndex is out of bound", zap.Uint64("logIndex", logIndex), zap.Uint64("lastLogIndex", r.lastLogIndex))
	}
	r.logs = r.logs[logIndex-r.storedIndex:]
	r.storedIndex = logIndex

}

func (r *queue) appliedTo(index uint64) {
	r.applying = false
	if index < r.appliedIndex {
		r.Warn("applied index less than applappliedIndexiedLogIndex", zap.Uint64("index", index), zap.Uint64("appliedIndex", r.appliedIndex))
		return
	}
	r.appliedIndex = index
}

// truncateLogTo 截取日志到指定日志下标，比如truncateLogTo(6)，如果日志是1 2 3 4 5 6 7 8 9，截取后是1 2 3 4 5 6
func (r *queue) truncateLogTo(logIndex uint64) {

	if logIndex > r.lastLogIndex {
		r.Panic("truncate log index is out of bound", zap.Uint64("logIndex", logIndex), zap.Uint64("lastLogIndex", r.lastLogIndex))
	}
	if logIndex < r.storedIndex {
		r.storedIndex = logIndex
	} else {
		r.logs = r.logs[:logIndex-r.storedIndex]
	}

	r.lastLogIndex = logIndex

	r.Warn("truncate log to", zap.Uint64("logIndex", logIndex), zap.Uint64("lastLogIndex", r.lastLogIndex))

}
