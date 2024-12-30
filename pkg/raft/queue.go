package raft

import (
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type queue struct {
	logs []Log
	wklog.Log
	storageOffset uint64 // 存储的日志偏移，比如日志 1 2 3 4 5 6 7 8 9，存储的日志偏移是6 表示1 2 3 4 5 6已经存储
	lastLogIndex  uint64 // 最后一条日志下标
}

func newQueue(storageOffset, lastLogIndex uint64) *queue {
	return &queue{
		Log:           wklog.NewWKLog("queue"),
		storageOffset: storageOffset,
		lastLogIndex:  lastLogIndex,
	}
}

func (r *queue) append(log ...Log) {
	appendStartLogIndex := log[0].Index - 1
	if appendStartLogIndex == r.storageOffset+uint64(len(r.logs)) {
		r.logs = append(r.logs, log...)
		r.lastLogIndex = log[len(log)-1].Index
		return
	} else {
		r.Warn("append log index is not continuous", zap.Uint64("appendStartLogIndex", appendStartLogIndex), zap.Uint64("offset", r.storageOffset), zap.Int("logsLen", len(r.logs)))
	}
}

// storageTo 截取日志到指定日志下标，比如storageTo(6)，如果日志是1 2 3 4 5 6 7 8 9，截取后是7 8 9
func (r *queue) storageTo(logIndex uint64) {
	if logIndex <= r.storageOffset {
		return
	}
	if logIndex > r.lastLogIndex {
		r.Panic("storageTo logIndex is out of bound", zap.Uint64("logIndex", logIndex), zap.Uint64("lastLogIndex", r.lastLogIndex))
	}
	r.logs = r.logs[logIndex-r.storageOffset:]
	r.storageOffset = logIndex
}
