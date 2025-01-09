package raft

import (
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"go.uber.org/zap"
)

// Propose 提案
func (r *Raft) Propose(id uint64, data []byte) (*types.ProposeResp, error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), r.opts.ProposeTimeout)
	defer cancel()
	resps, err := r.proposeBatch(timeoutCtx, []types.ProposeReq{
		{
			Id:   id,
			Data: data,
		},
	}, nil)
	if err != nil {
		return nil, err
	}
	return resps[0], nil
}

// ProposeUntilApplied 提案直到应用（提案后等待日志被应用）
func (r *Raft) ProposeUntilAppliedTimeout(ctx context.Context, id uint64, data []byte) (*types.ProposeResp, error) {
	resps, err := r.ProposeBatchUntilAppliedTimeout(ctx, []types.ProposeReq{
		{
			Id:   id,
			Data: data,
		},
	})
	if err != nil {
		return nil, err
	}
	return resps[0], nil
}

func (r *Raft) ProposeUntilApplied(id uint64, data []byte) (*types.ProposeResp, error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), r.opts.ProposeTimeout)
	defer cancel()
	return r.ProposeUntilAppliedTimeout(timeoutCtx, id, data)
}

// ProposeBatchTimeout 批量提案
func (r *Raft) ProposeBatchTimeout(ctx context.Context, reqs []types.ProposeReq) ([]*types.ProposeResp, error) {
	return r.proposeBatch(ctx, reqs, nil)
}

// ProposeBatchUntilAppliedTimeout 批量提案（等待应用）
func (r *Raft) ProposeBatchUntilAppliedTimeout(ctx context.Context, reqs []types.ProposeReq) ([]*types.ProposeResp, error) {
	// 等待应用
	var (
		applyProcess *progress
		resps        []*types.ProposeResp
		err          error
		needWait     = true
	)
	if !r.node.IsLeader() {
		// 如果不是leader，则转发给leader
		resps, err = r.fowardPropose(ctx, reqs)
		if err != nil {
			return nil, err
		}
		maxLogIndex := resps[len(resps)-1].Index
		// 如果最大的日志下标大于已应用的日志下标，则不需要等待
		if r.node.queue.appliedIndex >= maxLogIndex {
			needWait = false
		}
		if needWait {
			applyProcess = r.wait.waitApply(maxLogIndex)
		}
	} else {
		resps, err = r.proposeBatch(ctx, reqs, func(logs []types.Log) {
			maxLogIndex := logs[len(logs)-1].Index
			// 如果最大的日志下标大于已应用的日志下标，则不需要等待
			if r.node.queue.appliedIndex >= maxLogIndex {
				needWait = false
			}
			if needWait {
				applyProcess = r.wait.waitApply(maxLogIndex)
			}

		})
		if err != nil {
			return nil, err
		}
	}

	if needWait {
		select {
		case <-applyProcess.waitC:
			return resps, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-r.stopper.ShouldStop():
			return nil, ErrStopped
		}
	} else {
		return resps, nil
	}
}

func (r *Raft) proposeBatch(ctx context.Context, reqs types.ProposeReqSet, stepBefore func(logs []types.Log)) ([]*types.ProposeResp, error) {

	r.node.Lock()
	defer r.node.Unlock()
	lastLogIndex := r.node.queue.lastLogIndex
	logs := make([]types.Log, 0, len(reqs))
	for i, req := range reqs {
		logIndex := lastLogIndex + 1 + uint64(i)
		logs = append(logs, types.Log{
			Id:    req.Id,
			Term:  r.node.cfg.Term,
			Index: logIndex,
			Data:  req.Data,
		})
	}

	if stepBefore != nil {
		stepBefore(logs)
	}

	err := r.StepWait(ctx, types.Event{
		Type: types.Propose,
		Logs: logs,
	})
	if err != nil {
		return nil, err
	}

	resps := make([]*types.ProposeResp, 0, len(reqs))
	for i, req := range reqs {
		logIndex := lastLogIndex + 1 + uint64(i)
		resps = append(resps, &types.ProposeResp{
			Id:    req.Id,
			Index: logIndex,
		})
	}
	return resps, nil
}

func (r *Raft) handleSendPropose(e types.Event) {
	var reqs types.ProposeReqSet
	err := reqs.Unmarshal(e.Logs[0].Data)
	if err != nil {
		r.Error("unmarshal propose req failed", zap.Error(err))
		r.sendProposeRespError(reqs, e.From)
		return
	}
	if !r.node.IsLeader() {
		r.Error("handleSendPropose: node is leader, but receive propose from other node", zap.Uint64("leaderId", r.node.LeaderId()), zap.Uint64("from", e.From))
		r.sendProposeRespError(reqs, e.From)
		return
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), r.opts.ProposeTimeout)
	defer cancel()
	resps, err := r.ProposeBatchUntilAppliedTimeout(timeoutCtx, reqs)
	if err != nil {
		r.Error("handleSendPropose: propose batch failed", zap.Error(err))
		r.sendProposeRespError(reqs, e.From)
		return
	}
	data, err := types.ProposeRespSet(resps).Marshal()
	if err != nil {
		r.Error("marshal propose resp failed", zap.Error(err))
		r.sendProposeRespError(reqs, e.From)
		return
	}

	r.opts.Transport.Send(types.Event{
		From: r.opts.NodeId,
		To:   e.From,
		Type: types.SendProposeResp,
		Logs: []types.Log{
			{
				Data: data,
			},
		},
		Reason: types.ReasonOk,
	})
}

func (r *Raft) sendProposeRespError(reqs types.ProposeReqSet, to uint64) {
	resps := types.ProposeRespSet{}
	for _, req := range reqs {
		resps = append(resps, &types.ProposeResp{
			Id:    req.Id,
			Index: 0,
		})
	}
	data, err := resps.Marshal()
	if err != nil {
		r.Error("marshal propose resp failed", zap.Error(err))
		return
	}
	r.opts.Transport.Send(types.Event{
		From:   r.opts.NodeId,
		To:     to,
		Type:   types.SendProposeResp,
		Reason: types.ReasonError,
		Logs: []types.Log{
			{
				Data: data,
			},
		},
	})
}

func (r *Raft) handleSendProposeResp(e types.Event) {

	var resps types.ProposeRespSet
	err := resps.Unmarshal(e.Logs[0].Data)
	if err != nil {
		r.Error("unmarshal propose resp failed", zap.Error(err))
		return
	}

	key := fmt.Sprintf("%d", resps[len(resps)-1].Id)
	if e.Reason == types.ReasonError {
		r.Error("handleSendProposeResp: receive propose resp error", zap.Uint64("from", e.From))
		r.fowardProposeWait.Trigger(key, errors.New("receive propose resp error"))
		return
	}
	r.fowardProposeWait.Trigger(key, resps)
}

func (r *Raft) fowardPropose(ctx context.Context, reqs types.ProposeReqSet) ([]*types.ProposeResp, error) {
	data, err := reqs.Marshal()
	if err != nil {
		return nil, err
	}

	key := fmt.Sprintf("%d", reqs[len(reqs)-1].Id)
	waitC := r.fowardProposeWait.Register(key)

	r.opts.Transport.Send(types.Event{
		From: r.node.opts.NodeId,
		To:   r.node.LeaderId(),
		Type: types.SendPropose,
		Logs: []types.Log{
			{
				Data: data,
			},
		},
	})

	select {
	case result := <-waitC:
		if result == nil {
			return nil, errors.New("foward propose failed")
		}
		err, ok := result.(error)
		if ok {
			return nil, err
		}
		return result.(types.ProposeRespSet), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.stopper.ShouldStop():
		return nil, ErrStopped
	}
}
