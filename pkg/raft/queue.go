package raft

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type queue struct {
	logs []Log
	wklog.Log

	// 存储日志偏移下标，比如日志 1 2 3 4 5 6 7 8 9，存储的日志偏移是6 表示1 2 3 4 5 6已经存储
	storedIndex    uint64
	lastLogIndex   uint64 // 最后一条日志下标
	committedIndex uint64 // 已提交的日志下标
	appending      bool   // 是否在追加中
}

func newQueue(appliedLogIndex, lastLogIndex uint64, nodeId uint64) *queue {
	return &queue{
		Log:            wklog.NewWKLog(fmt.Sprintf("queue[%d]", nodeId)),
		storedIndex:    lastLogIndex,
		lastLogIndex:   lastLogIndex,
		committedIndex: appliedLogIndex,
	}
}

func (r *queue) append(log ...Log) error {
	appendStartLogIndex := log[0].Index - 1

	if appendStartLogIndex == r.storedIndex+uint64(len(r.logs)) {
		r.logs = append(r.logs, log...)
		r.lastLogIndex = log[len(log)-1].Index
		return nil
	} else {
		r.Warn("append log index is not continuous", zap.Uint64("appendStartLogIndex", appendStartLogIndex), zap.Uint64("storedIndex", r.storedIndex), zap.Int("logsLen", len(r.logs)))
		return ErrLogIndexNotContinuous
	}
}

func (r *queue) hasNextStoreLogs() bool {
	if r.appending {
		return false
	}
	return r.storedIndex < r.lastLogIndex
}

func (r *queue) nextStoreLogs(maxSize int) []Log {
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

// storeTo 存储日志到指定日志下标，比如storeTo(6)，如果日志是1 2 3 4 5 6 7 8 9，截取后是7 8 9
func (r *queue) storeTo(logIndex uint64) {
	if logIndex <= r.storedIndex {
		return
	}
	if logIndex > r.lastLogIndex {
		r.Panic("appendTo logIndex is out of bound", zap.Uint64("logIndex", logIndex), zap.Uint64("lastLogIndex", r.lastLogIndex))
	}
	r.logs = r.logs[logIndex-r.storedIndex:]
	r.storedIndex = logIndex
	r.appending = false
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

}
