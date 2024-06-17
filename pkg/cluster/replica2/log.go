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

	storagingIndex uint64 // 正在存储中的日志下标
	storagedIndex  uint64 // 已存储的日志下标

	committedIndex uint64 // 已提交的日志下标

	applyingIndex uint64 // 正在应用的日志下标

	appliedIndex uint64 // 已应用的日志下标

	storaging bool // 是否正在追加日志
	applying  bool // 是否正在应用日志
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

	rg.Debug("new replica log", zap.Uint64("lastIndex", lastIndex), zap.Uint64("appliedIndex", opts.AppliedIndex))

	rg.committedIndex = opts.AppliedIndex
	rg.appliedIndex = opts.AppliedIndex
	rg.applyingIndex = opts.AppliedIndex

	rg.updateLastIndex(lastIndex)

	return rg
}

func (r *replicaLog) updateLastIndex(lastIndex uint64) {
	r.lastLogIndex = lastIndex
	r.storagedIndex = lastIndex
	r.storagingIndex = lastIndex
	r.unstable.offset = lastIndex + 1
	r.unstable.offsetInProgress = lastIndex + 1
}

func (r *replicaLog) appendLog(logs ...Log) {
	lastLog := logs[len(logs)-1]
	r.unstable.truncateAndAppend(logs)
	r.lastLogIndex = lastLog.Index
	fmt.Println("appendLog--------->", r.lastLogIndex)
}

func (r *replicaLog) storagingTo(index uint64) {
	r.storagingIndex = index
	r.storaging = true
}

func (r *replicaLog) storagedTo(index uint64) {
	r.storagedIndex = index
	r.storagingIndex = index
	r.storaging = false
}

// 是否有需要存储的日志
func (r *replicaLog) hasStorage() bool {
	if r.storaging {
		return false
	}
	return r.storagingIndex < r.lastLogIndex
}

// 需要存储的日志
func (r *replicaLog) nextStorageLogs() []Log {
	if r.storagingIndex >= r.lastLogIndex {
		return nil
	}

	return r.unstable.slice(r.storagingIndex+1, r.lastLogIndex+1)
}

// 是否有需要应用的日志
func (r *replicaLog) hasApply() bool {
	if r.applying {
		return false
	}
	i := min(r.storagedIndex, r.committedIndex)
	return r.applyingIndex < i
}

func (r *replicaLog) committedTo(index uint64) {
	if index < r.committedIndex {
		r.Panic("commit index less than committed index", zap.Uint64("commitIndex", index), zap.Uint64("committedIndex", r.committedIndex))
	}
	r.committedIndex = index
}

func (r *replicaLog) applyingTo(index uint64) {
	if index < r.applyingIndex {
		r.Panic("apply index less than applying index", zap.Uint64("applyIndex", index), zap.Uint64("applyingIndex", r.applyingIndex))
	}
	r.applyingIndex = index
	r.applying = true
}

func (r *replicaLog) appliedTo(i uint64) {
	r.appliedIndex = i
	r.applyingIndex = i
	r.applying = false
	r.unstable.appliedTo(i)
}

func (r *replicaLog) getLogsFromUnstable(lo, hi uint64, maxSize logEncodingSize) ([]Log, bool, error) {
	if err := r.mustCheckOutOfBounds(lo, hi); err != nil {
		return nil, false, err
	}
	if lo == hi {
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
		r.Error("mustCheckOutOfBounds err", zap.Uint64("lo", lo), zap.Uint64("firstIndex", fi))
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
