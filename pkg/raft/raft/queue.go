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

	return &queue{
		Log:            wklog.NewWKLog(fmt.Sprintf("queue[%s]", key)),
		storedIndex:    lastLogIndex,
		lastLogIndex:   lastLogIndex,
		committedIndex: appliedLogIndex,
		appliedIndex:   appliedLogIndex,
	}
}

func (r *queue) append(log ...types.Log) error {
	appendStartLogIndex := log[0].Index - 1

	if appendStartLogIndex == r.lastLogIndex {
		r.logs = append(r.logs, log...)
		r.lastLogIndex = log[len(log)-1].Index
		return nil
	} else {
		r.Warn("append log index is not continuous", zap.Uint64("appendStartLogIndex", appendStartLogIndex), zap.Uint64("lastLogIndex", r.lastLogIndex), zap.Int("logsLen", len(log)))
		return nil
	}
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

}
