package raftgroup

import (
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"go.uber.org/zap"
)

// 处理存储请求
func (rg *RaftGroup) handleStoreReq(r IRaft, e types.Event) {

	err := rg.goPool.Submit(func() {
		err := rg.opts.Storage.AppendLogs(r.Key(), e.Logs, e.TermStartIndexInfo)
		if err != nil {
			rg.Error("append logs failed", zap.Error(err))
		}
		reason := types.ReasonOk
		if err != nil {
			reason = types.ReasonError
		}

		rg.AddEvent(r.Key(), types.Event{
			Type:   types.StoreResp,
			Index:  e.Logs[len(e.Logs)-1].Index,
			Reason: reason,
		})
		rg.Advance()
	})
	if err != nil {
		rg.Error("submit append logs failed", zap.Error(err))
		rg.AddEvent(r.Key(), types.Event{
			Type:   types.StoreResp,
			Reason: types.ReasonError,
		})
		rg.Advance()
	}
}

func (rg *RaftGroup) handleGetLogsReq(r IRaft, e types.Event) {
	err := rg.goPool.Submit(func() {
		// 获取裁断日志下标
		var (
			trunctIndex uint64
		)
		if e.Reason != types.ReasonOnlySync {
			var treason types.Reason
			trunctIndex, treason = rg.getTrunctLogIndex(r, e)
			if treason != types.ReasonOk {
				rg.AddEvent(r.Key(), types.Event{
					To:     e.From,
					Type:   types.GetLogsResp,
					Reason: treason,
				})
				return
			}
		}
		// 需要裁剪
		if trunctIndex > 0 {
			rg.AddEvent(r.Key(), types.Event{
				To:     e.From,
				Type:   types.GetLogsResp,
				Index:  trunctIndex,
				Reason: types.ReasonTruncate,
			})
			return
		}
		// 获取日志数据
		logs, err := rg.opts.Storage.GetLogs(r.Key(), e.Index, e.StoredIndex+1, rg.opts.MaxLogCountPerBatch)
		if err != nil {
			rg.Error("get logs failed", zap.Error(err))
			rg.AddEvent(r.Key(), types.Event{
				To:     e.From,
				Type:   types.GetLogsResp,
				Index:  e.Index,
				Reason: types.ReasonError,
			})
			return
		}
		if len(logs) == 0 {
			rg.Warn("logs is empty", zap.String("key", r.Key()), zap.Uint64("index", e.Index), zap.Uint64("storedIndex", e.StoredIndex))
		}
		rg.AddEvent(r.Key(), types.Event{
			To:     e.From,
			Type:   types.GetLogsResp,
			Index:  e.Index,
			Logs:   logs,
			Reason: types.ReasonOk,
		})
		rg.Advance()
	})
	if err != nil {
		rg.Error("submit get logs failed", zap.Error(err))
		rg.AddEvent(r.Key(), types.Event{
			Type:   types.GetLogsResp,
			Reason: types.ReasonError,
		})
	}
}

func (rg *RaftGroup) handleTruncateReq(r IRaft, e types.Event) {
	err := rg.goPool.Submit(func() {
		err := rg.opts.Storage.TruncateLogTo(r.Key(), e.Index)
		if err != nil {
			rg.Error("truncate logs failed", zap.Error(err))
			rg.AddEvent(r.Key(), types.Event{
				Type:   types.TruncateResp,
				Reason: types.ReasonError,
			})
			return
		}
		// 删除本地的leader term start index
		err = rg.opts.Storage.DeleteLeaderTermStartIndexGreaterThanTerm(r.Key(), e.Term)
		if err != nil {
			rg.Error("delete leader term start index failed", zap.Error(err), zap.Uint32("term", e.Term))
			rg.AddEvent(r.Key(), types.Event{
				Type:   types.TruncateResp,
				Reason: types.ReasonError,
			})
			return
		}
		rg.AddEvent(r.Key(), types.Event{
			Type:   types.TruncateResp,
			Index:  e.Index,
			Reason: types.ReasonOk,
		})
	})
	if err != nil {
		rg.Error("submit truncate req failed", zap.Error(err))
		rg.AddEvent(r.Key(), types.Event{
			Type:   types.TruncateResp,
			Reason: types.ReasonError,
		})
	}
}

func (rg *RaftGroup) handleApplyReq(r IRaft, e types.Event) {
	err := rg.goPool.Submit(func() {
		// 已提交
		rg.wait.didCommit(r.Key(), e.EndIndex-1)

		logs, err := rg.opts.Storage.GetLogs(r.Key(), e.StartIndex, e.EndIndex, 0)
		if err != nil {
			rg.Error("get logs failed", zap.Error(err))
			rg.AddEvent(r.Key(), types.Event{
				Type:   types.ApplyResp,
				Reason: types.ReasonError,
			})
			return
		}
		if len(logs) == 0 {
			rg.Error("logs is empty", zap.String("key", r.Key()), zap.Uint64("startIndex", e.StartIndex), zap.Uint64("endIndex", e.EndIndex))
			rg.AddEvent(r.Key(), types.Event{
				Type:   types.ApplyResp,
				Reason: types.ReasonError,
			})
			return
		}
		err = rg.opts.Storage.Apply(r.Key(), logs)
		if err != nil {
			rg.Error("apply logs failed", zap.Error(err))
			rg.AddEvent(r.Key(), types.Event{
				Type:   types.ApplyResp,
				Reason: types.ReasonError,
			})
			return
		}
		lastLogIndex := logs[len(logs)-1].Index
		rg.AddEvent(r.Key(), types.Event{
			Type:   types.ApplyResp,
			Reason: types.ReasonOk,
			Index:  lastLogIndex,
		})
		// 已应用
		rg.wait.didApply(r.Key(), lastLogIndex)
		rg.Advance()
	})
	if err != nil {
		rg.Error("submit apply req failed", zap.Error(err))
		rg.AddEvent(r.Key(), types.Event{
			Type:   types.ApplyResp,
			Reason: types.ReasonError,
		})
	}
}

// 根据副本的同步数据，来获取副本的需要裁剪的日志下标，如果不需要裁剪，则返回0
func (rg *RaftGroup) getTrunctLogIndex(r IRaft, e types.Event) (uint64, types.Reason) {
	leaderLastLogTerm, err := rg.opts.Storage.LeaderLastLogTerm(r.Key())
	if err != nil {
		rg.Error("get leader last log term failed", zap.Error(err))
		return 0, types.ReasonError
	}
	// 如果副本的最新日志任期大于领导的最新日志任期，则不合法，副本最新日志任期不可能大于领导最新日志任期
	if e.LastLogTerm > leaderLastLogTerm {
		rg.Error("log term is greater than leader term", zap.Uint32("lastLogTerm", e.LastLogTerm), zap.Uint32("leaderLastLogTerm", leaderLastLogTerm))
		return 0, types.ReasonError
	}
	// 副本的最新日志任期为0，说明副本没有日志，不需要裁剪
	if e.LastLogTerm == 0 {
		return 0, types.ReasonOk
	}
	// 如果副本的最新日志任期等于当前领导的最新日志任期，则不需要裁剪
	if e.LastLogTerm == leaderLastLogTerm {
		return 0, types.ReasonOk
	}

	// 如果副本的最新日志任期小于当前领导的最新日志任期，则需要裁剪
	if e.LastLogTerm < leaderLastLogTerm {
		// 获取副本的最新日志任期+1的开始日志下标
		termStartIndex, err := rg.opts.Storage.GetTermStartIndex(r.Key(), e.LastLogTerm+1)
		if err != nil {
			rg.Error("get term start index failed", zap.Error(err))
			return 0, types.ReasonError
		}
		if termStartIndex > 0 {
			return termStartIndex - 1, types.ReasonOk
		}
		return termStartIndex, types.ReasonOk
	}
	return 0, types.ReasonOk
}
