package replica

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type replicaLog struct {
	unstable *unstable
	wklog.Log
	opts *Options

	lastLogIndex uint64 // 最后一条日志下标

	storagedIndex uint64 // 已存储的日志下标

	committedIndex uint64 // 已提交的日志下标

	appliedIndex uint64 // 已应用的日志下标

}

func newReplicaLog(opts *Options) *replicaLog {
	rg := &replicaLog{
		unstable: newUnstable(opts.LogPrefix),
		Log:      wklog.NewWKLog(fmt.Sprintf("replicaLog[%d:%s]", opts.NodeId, opts.LogPrefix)),
		opts:     opts,
	}

	// lastIndex, term, err := opts.Storage.LastIndexAndTerm()
	// if err != nil {
	// 	rg.Panic("get last index failed", zap.Error(err), zap.Uint64("lastIndex", lastIndex), zap.Uint32("term", term))
	// }

	lastIndex := opts.LastIndex

	if opts.LastIndex < opts.AppliedIndex {
		rg.Panic("last index is less than applied index", zap.Uint64("lastIndex", opts.LastIndex), zap.Uint64("appliedIndex", opts.AppliedIndex))
	}

	rg.committedIndex = opts.AppliedIndex
	rg.appliedIndex = opts.AppliedIndex

	rg.updateLastIndex(lastIndex)

	return rg
}

func (r *replicaLog) updateLastIndex(lastIndex uint64) {
	r.lastLogIndex = lastIndex
	r.storagedIndex = lastIndex
	r.unstable.offset = lastIndex
	r.unstable.offsetInProgress = lastIndex

	if r.committedIndex > lastIndex {
		r.committedIndex = lastIndex
	}

}

func (r *replicaLog) appendLog(logs ...Log) {
	lastLog := logs[len(logs)-1]
	r.unstable.truncateAndAppend(logs)
	r.lastLogIndex = lastLog.Index

	// for _, log := range logs {
	// 	r.Info("append log.......", zap.Uint64("index", log.Index), zap.Uint32("term", log.Term))
	// }

}

func (r *replicaLog) storagedTo(index uint64) {
	if index < r.storagedIndex || index > r.lastLogIndex {
		r.Panic("storagedTo index out of bound", zap.Uint64("index", index), zap.Uint64("storagedIndex", r.storagedIndex), zap.Uint64("lastLogIndex", r.lastLogIndex))
	}
	r.storagedIndex = index
	// r.Info("storagedTo--------->", zap.Uint64("index", index))
}

// 需要存储的日志
func (r *replicaLog) nextStorageLogs() []Log {
	if r.storagedIndex >= r.lastLogIndex {
		return nil
	}

	return r.unstable.slice(r.unstable.offsetIndex(r.storagedIndex+1), r.unstable.offsetIndex(r.lastLogIndex+1))
}

func (r *replicaLog) appliedTo(i uint64) {
	if i < r.appliedIndex {
		r.Warn("applied index less than appliedIndex", zap.Uint64("i", i), zap.Uint64("appliedIndex", r.appliedIndex))
		return
	}
	r.appliedIndex = i

	r.unstable.appliedTo(i)
}

func (r *replicaLog) getLogsFromUnstable(startLogIndex, endLogIndex uint64, maxSize logEncodingSize) ([]Log, bool, error) {
	lo := r.unstable.offsetIndex(startLogIndex)
	hi := r.unstable.offsetIndex(endLogIndex)
	if lo == hi {
		return nil, false, nil
	}
	if len(r.unstable.logs) == 0 {
		return nil, false, nil
	}
	if lo >= r.unstable.offset {
		logs, exceed := limitSize(r.unstable.slice(lo, hi), maxSize)
		return logs[:len(logs):len(logs)], exceed, nil
	}

	return nil, false, nil
}

func (r *replicaLog) mustCheckOutOfBounds(lo, hi uint64) error {

	if lo > hi {
		r.Panic(fmt.Sprintf("invalid slice %d > %d", lo, hi))
	}
	fi := r.firstIndex()
	if lo < fi {
		r.Panic("mustCheckOutOfBounds err", zap.Uint64("lo", lo), zap.Uint64("firstIndex", fi))
		return ErrCompacted
	}

	length := r.lastIndex() + 1 - fi
	if hi > fi+length {
		r.Panic(fmt.Sprintf("slice[%d,%d) out of bound [%d,%d]", lo, hi, fi, r.lastIndex()))
	}
	return nil
}

func (r *replicaLog) lastIndex() uint64 {
	return r.lastLogIndex
}

func (r *replicaLog) firstIndex() uint64 {
	if i, ok := r.unstable.maybeFirstIndex(); ok {
		return i
	}
	i, err := r.opts.Storage.FirstIndex()
	if err != nil {
		r.Panic("get first index failed", zap.Error(err))
	}
	return i
}

func (r *replicaLog) lastIndexAndTerm() (uint64, uint32) {
	if len(r.unstable.logs) > 0 {
		lg := r.unstable.lastLog()
		return lg.Index, lg.Term
	}
	lastIndex, lastTerm, err := r.opts.Storage.LastIndexAndTerm() // 获取当前节点最后一条日志下标和任期
	if err != nil {
		r.Panic("canVote: get last index failed", zap.Error(err))
		return 0, 0
	}
	return lastIndex, lastTerm
}
