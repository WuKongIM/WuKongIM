package raftgroup

import (
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
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
			Logs:   e.Logs,
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
			rg.Advance()
			return
		}
		if e.Reason != types.ReasonOnlySync && e.Index > e.StoredIndex {
			rg.Debug("index is greater than stored index", zap.String("key", r.Key()), zap.Uint64("index", e.Index), zap.Uint64("storedIndex", e.StoredIndex))
			rg.AddEvent(r.Key(), types.Event{
				To:     e.From,
				Type:   types.GetLogsResp,
				Index:  e.Index,
				Reason: types.ReasonOk,
			})
			rg.Advance()
			return
		}
		// 获取日志数据
		logs, err := rg.opts.Storage.GetLogs(r.Key(), e.Index, e.StoredIndex+1, rg.opts.MaxLogSizePerBatch)
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
		err = rg.opts.Storage.DeleteLeaderTermStartIndexGreaterThanTerm(r.Key(), e.LastLogTerm)
		if err != nil {
			rg.Error("delete leader term start index failed", zap.Error(err), zap.Uint32("LastLogTerm", e.LastLogTerm))
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
		// rg.wait.didCommit(r.Key(), e.EndIndex-1)
		var lastLogIndex uint64
		if !rg.opts.NotNeedApplied {
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
			lastLogIndex = logs[len(logs)-1].Index
		} else {
			lastLogIndex = e.EndIndex - 1
		}

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
		term, err := rg.opts.Storage.LeaderTermGreaterEqThan(r.Key(), e.LastLogTerm+1)
		if err != nil {
			rg.Error("LeaderTermGreaterEqThan: get leader last term failed", zap.Error(err))
			return 0, types.ReasonError
		}
		// 获取副本的最新日志任期+1的开始日志下标
		termStartIndex, err := rg.opts.Storage.GetTermStartIndex(r.Key(), term)
		if err != nil {
			rg.Error("get term start index failed", zap.Error(err))
			return 0, types.ReasonError
		}
		if termStartIndex > 0 {
			return termStartIndex - 1, types.ReasonOk
		}
		return termStartIndex, types.ReasonOk
	} else {
		termStartIndex, err := rg.opts.Storage.GetTermStartIndex(r.Key(), leaderLastLogTerm)
		if err != nil {
			rg.Error("get term start index failed", zap.Error(err))
			return 0, types.ReasonError
		}
		rg.Foucs("getTrunctLogIndex: lastLogTerm > leaderLastLogTerm", zap.Uint32("e.LastLogTerm", e.LastLogTerm), zap.Uint32("leaderLastLogTerm", leaderLastLogTerm), zap.Uint64("termStartIndex", termStartIndex))
		if termStartIndex > 0 {
			return termStartIndex - 1, types.ReasonOk
		}
		return termStartIndex, types.ReasonOk
	}
}

func (rg *RaftGroup) handleRoleChangeReq(r IRaft, e types.Event) {

	err := rg.goPool.Submit(func() {

		var (
			newCfg        types.Config
			err           error
			respEventType types.EventType
		)
		switch e.Type {
		case types.LearnerToLeaderReq,
			types.LearnerToFollowerReq:
			newCfg, err = rg.learnTo(r, e.From)
			if err != nil {
				rg.Error("learn switch failed", zap.Error(err), zap.String("key", r.Key()), zap.String("type", e.Type.String()), zap.String("cfg", r.Config().String()))
			}
			if e.Type == types.LearnerToFollowerReq {
				respEventType = types.LearnerToFollowerResp
			} else if e.Type == types.LearnerToLeaderReq {
				respEventType = types.LearnerToLeaderResp
			}
		case types.FollowerToLeaderReq:
			newCfg, err = rg.followerToLeader(r, e.From)
			if err != nil {
				rg.Error("follower switch to leader failed", zap.Error(err), zap.String("key", r.Key()), zap.String("cfg", r.Config().String()))
			}
			respEventType = types.FollowerToLeaderResp
		default:
			err = errors.New("unknown role switch")
		}

		if err != nil {
			rg.AddEvent(r.Key(), types.Event{
				Type:   respEventType,
				From:   e.From,
				Reason: types.ReasonError,
			})
			return
		}

		err = rg.opts.Storage.SaveConfig(r.Key(), newCfg)
		if err != nil {
			rg.Error("change role failed", zap.Error(err))
			rg.AddEvent(r.Key(), types.Event{
				Type:   respEventType,
				From:   e.From,
				Reason: types.ReasonError,
			})
			return
		}
		rg.AddEvent(r.Key(), types.Event{
			Type:   respEventType,
			From:   e.From,
			Reason: types.ReasonOk,
			Config: newCfg,
		})
		rg.Advance()
	})
	if err != nil {
		rg.Error("submit role change req failed", zap.Error(err))

		var respEventType types.EventType

		switch e.Type {
		case types.LearnerToLeaderReq:
			respEventType = types.LearnerToLeaderResp
		case types.LearnerToFollowerReq:
			respEventType = types.LearnerToFollowerResp
		case types.FollowerToLeaderReq:
			respEventType = types.FollowerToLeaderResp
		}

		rg.AddEvent(r.Key(), types.Event{
			Type:   respEventType,
			From:   e.From,
			Reason: types.ReasonError,
		})
	}
}

// 学习者转换
func (rg *RaftGroup) learnTo(r IRaft, learnerId uint64) (types.Config, error) {
	cfg := r.Config().Clone()

	if learnerId != cfg.MigrateTo {
		rg.Warn("learnerId not equal migrateTo", zap.Uint64("learnerId", learnerId), zap.Uint64("migrateTo", cfg.MigrateTo))
		return types.Config{}, errors.New("learnerId not equal migrateTo")
	}

	cfg.Learners = wkutil.RemoveUint64(cfg.Learners, learnerId)

	if !wkutil.ArrayContainsUint64(cfg.Replicas, cfg.MigrateTo) {
		cfg.Replicas = append(cfg.Replicas, cfg.MigrateTo)
	}

	if cfg.MigrateFrom != cfg.MigrateTo {
		cfg.Replicas = wkutil.RemoveUint64(cfg.Replicas, cfg.MigrateFrom)
	}

	if cfg.MigrateFrom == r.LeaderId() { // 学习者转领导
		cfg.Leader = cfg.MigrateTo
	}
	cfg.MigrateFrom = 0
	cfg.MigrateTo = 0

	return cfg, nil
}

// follower转换成leader
func (rg *RaftGroup) followerToLeader(r IRaft, followerId uint64) (types.Config, error) {
	cfg := r.Config().Clone()
	if !wkutil.ArrayContainsUint64(cfg.Replicas, followerId) {
		rg.Error("followerToLeader: follower not in replicas", zap.Uint64("followerId", followerId))
		return types.Config{}, fmt.Errorf("follower not in replicas")
	}

	cfg.Leader = followerId
	cfg.Term = cfg.Term + 1
	cfg.MigrateFrom = 0
	cfg.MigrateTo = 0

	return cfg, nil

}

func (rg *RaftGroup) handleDestory(r IRaft) {
	rg.RemoveRaft(r)
}
