package raftgroup

import (
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"go.uber.org/zap"
)

// 处理存储请求
func (rg *RaftGroup) handleStoreReq(r IRaft, e types.Event) {

	err := rg.goPool.Submit(func() {
		err := rg.opts.Storage.AppendLogs(r, e.Logs, e.TermStartIndexInfo)
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
	})
	if err != nil {
		rg.Error("submit append logs failed", zap.Error(err))
		rg.AddEvent(r.Key(), types.Event{
			Type:   types.StoreResp,
			Reason: types.ReasonError,
		})
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
				Reason: types.ReasonTrunctate,
			})
			return
		}
		// 获取日志数据
		logs, err := rg.opts.Storage.GetLogs(r, e.Index, rg.opts.MaxLogCountPerBatch)
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
		rg.AddEvent(r.Key(), types.Event{
			To:     e.From,
			Type:   types.GetLogsResp,
			Index:  e.Index,
			Logs:   logs,
			Reason: types.ReasonOk,
		})
	})
	if err != nil {
		rg.Error("submit get logs failed", zap.Error(err))
		rg.AddEvent(r.Key(), types.Event{
			Type:   types.GetLogsResp,
			Reason: types.ReasonError,
		})
	}
}

// 根据副本的同步数据，来获取副本的需要裁剪的日志下标，如果不需要裁剪，则返回0
func (rg *RaftGroup) getTrunctLogIndex(r IRaft, e types.Event) (uint64, types.Reason) {
	leaderLastLogTerm, err := rg.opts.Storage.LeaderLastLogTerm(r)
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
		termStartIndex, err := rg.opts.Storage.GetTermStartIndex(r, e.LastLogTerm+1)
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
