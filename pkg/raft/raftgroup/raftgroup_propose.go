package raftgroup

import (
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"go.uber.org/zap"
)

func (rg *RaftGroup) Propose(raftKey string, id uint64, data []byte) (*types.ProposeResp, error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), rg.opts.ProposeTimeout)
	defer cancel()
	return rg.ProposeTimeout(timeoutCtx, raftKey, id, data)
}

func (rg *RaftGroup) ProposeUntilApplied(raftKey string, id uint64, data []byte) (*types.ProposeResp, error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), rg.opts.ProposeTimeout)
	defer cancel()

	resps, err := rg.ProposeBatchUntilAppliedTimeout(timeoutCtx, raftKey, types.ProposeReqSet{
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

func (rg *RaftGroup) ProposeTimeout(ctx context.Context, raftKey string, id uint64, data []byte) (*types.ProposeResp, error) {
	raft := rg.raftList.get(raftKey)
	if raft == nil {
		return nil, fmt.Errorf("raft not found, key:%s", raftKey)
	}
	resps, err := rg.proposeBatchTimeout(ctx, raft, []types.ProposeReq{
		{
			Id:   id,
			Data: data,
		},
	}, nil)
	if err != nil {
		return nil, err
	}

	if len(resps) != 0 {
		return resps[0], nil
	}
	return nil, fmt.Errorf("propose failed")
}

func (rg *RaftGroup) ProposeBatchTimeout(ctx context.Context, raftKey string, reqs types.ProposeReqSet) (types.ProposeRespSet, error) {
	raft := rg.raftList.get(raftKey)
	if raft == nil {
		return nil, fmt.Errorf("raft not found, key:%s", raftKey)
	}
	return rg.proposeBatchTimeout(ctx, raft, reqs, nil)
}

// ProposeBatchUntilAppliedTimeout 批量提案（等待应用）
func (rg *RaftGroup) ProposeBatchUntilAppliedTimeout(ctx context.Context, raftKey string, reqs types.ProposeReqSet) (types.ProposeRespSet, error) {
	// 等待应用
	var (
		applyProcess *progress
		resps        []*types.ProposeResp
		err          error
		needWait     = true
	)
	raft := rg.raftList.get(raftKey)
	if raft == nil {
		return nil, fmt.Errorf("raft not found, key:%s", raftKey)
	}

	if !raft.IsLeader() {
		// 如果不是leader，则转发给leader
		resps, err = rg.fowardPropose(ctx, raft, reqs)
		if err != nil {
			return nil, err
		}
		maxLogIndex := resps[len(resps)-1].Index
		// 如果最大的日志下标大于已应用的日志下标，则不需要等待
		// 如果最大的日志下标大于已应用的日志下标，则不需要等待
		if raft.AppliedIndex() >= maxLogIndex {
			needWait = false
		}
		if needWait {
			applyProcess = rg.wait.waitApply(raftKey, maxLogIndex)
		}
	} else {
		resps, err = rg.proposeBatchTimeout(ctx, raft, reqs, func(logs []types.Log) {
			maxLogIndex := logs[len(logs)-1].Index

			// 如果最大的日志下标大于已应用的日志下标，则不需要等待
			if raft.AppliedIndex() >= maxLogIndex {
				needWait = false
			}
			if needWait {
				applyProcess = rg.wait.waitApply(raftKey, maxLogIndex)
			}

		})
		if err != nil {
			if applyProcess != nil {
				rg.wait.put(applyProcess)
			}
			return nil, err
		}
	}

	if needWait {
		select {
		case <-applyProcess.waitC:
			rg.wait.put(applyProcess)
			return resps, nil
		case <-ctx.Done():
			rg.Error("propose batch until applied timeout", zap.String("raftKey", raftKey), zap.Any("resps", resps), zap.String("progress", applyProcess.String()))
			// rg.wait.put(applyProcess) // 这里不需要put，因为如果这里put了，那么在waitApply中wait会出现close of nil channel
			return nil, ctx.Err()
		case <-rg.stopper.ShouldStop():
			return nil, ErrGroupStopped
		}
	} else {
		return resps, nil
	}
}

// ProposeBatchTimeout 批量提案
func (rg *RaftGroup) proposeBatchTimeout(ctx context.Context, raft IRaft, reqs []types.ProposeReq, addBefore func(logs []types.Log)) ([]*types.ProposeResp, error) {

	raft.Lock()
	defer raft.Unlock()

	lastLogIndex := raft.LastLogIndex()
	logs := make([]types.Log, 0, len(reqs))
	for i, req := range reqs {
		logIndex := lastLogIndex + 1 + uint64(i)
		logs = append(logs, types.Log{
			Id:    req.Id,
			Term:  raft.LastTerm(),
			Index: logIndex,
			Data:  req.Data,
		})
	}

	if addBefore != nil {
		addBefore(logs)
	}

	waitC := make(chan error, 1)
	rg.mq.MustAdd(Event{
		RaftKey: raft.Key(),
		Event: types.Event{
			Type: types.Propose,
			Logs: logs,
		},
		WaitC: waitC,
	})

	rg.Advance()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("propose timeout")
	case err := <-waitC:
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
}

func (rg *RaftGroup) fowardPropose(ctx context.Context, raft IRaft, reqs types.ProposeReqSet) ([]*types.ProposeResp, error) {
	data, err := reqs.Marshal()
	if err != nil {
		return nil, err
	}

	key := fmt.Sprintf("%d", reqs[len(reqs)-1].Id)
	waitC := rg.fowardProposeWait.Register(key)

	rg.opts.Transport.Send(raft.Key(), types.Event{
		From: raft.NodeId(),
		To:   raft.LeaderId(),
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
			return nil, errors.New("raftgroup: foward propose failed")
		}
		err, ok := result.(error)
		if ok {
			return nil, err
		}
		return result.(types.ProposeRespSet), nil
	case <-ctx.Done():
		rg.Error("foward propose timeout", zap.String("raftKey", raft.Key()))
		return nil, ctx.Err()
	case <-rg.stopper.ShouldStop():
		return nil, ErrGroupStopped
	}
}

func (rg *RaftGroup) handleSendPropose(raftKey string, e types.Event) {

	raft := rg.raftList.get(raftKey)
	if raft == nil {
		rg.Error("raftgroup: raft not exist", zap.String("raftKey", raftKey))
		return
	}

	var reqs types.ProposeReqSet
	err := reqs.Unmarshal(e.Logs[0].Data)
	if err != nil {
		rg.Error("raftgroup: unmarshal propose req failed", zap.Error(err))
		rg.sendProposeRespError(raft, reqs, e.From)
		return
	}

	if !raft.IsLeader() {
		rg.Error("raftgroup: handleSendPropose: node is leader, but receive propose from other node", zap.Uint64("leaderId", raft.LeaderId()), zap.Uint64("from", e.From))
		rg.sendProposeRespError(raft, reqs, e.From)
		return
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), rg.opts.ProposeTimeout)
	defer cancel()
	resps, err := rg.proposeBatchTimeout(timeoutCtx, raft, reqs, nil)
	if err != nil {
		rg.Error("handleSendPropose: propose batch failed", zap.Error(err))
		rg.sendProposeRespError(raft, reqs, e.From)
		return
	}
	data, err := types.ProposeRespSet(resps).Marshal()
	if err != nil {
		rg.Error("raftgroup: marshal propose resp failed", zap.Error(err))
		rg.sendProposeRespError(raft, reqs, e.From)
		return
	}

	rg.opts.Transport.Send(raftKey, types.Event{
		From: raft.NodeId(),
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

func (rg *RaftGroup) sendProposeRespError(r IRaft, reqs types.ProposeReqSet, to uint64) {
	resps := types.ProposeRespSet{}
	for _, req := range reqs {
		resps = append(resps, &types.ProposeResp{
			Id:    req.Id,
			Index: 0,
		})
	}
	data, err := resps.Marshal()
	if err != nil {
		rg.Error("marshal propose resp failed", zap.Error(err))
		return
	}
	rg.opts.Transport.Send(r.Key(), types.Event{
		From:   r.NodeId(),
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

func (rg *RaftGroup) handleSendProposeResp(e types.Event) {

	var resps types.ProposeRespSet
	err := resps.Unmarshal(e.Logs[0].Data)
	if err != nil {
		rg.Error("unmarshal propose resp failed", zap.Error(err))
		return
	}

	key := fmt.Sprintf("%d", resps[len(resps)-1].Id)

	if e.Reason == types.ReasonError {
		rg.Error("handleSendProposeResp: receive propose resp error", zap.Uint64("from", e.From))
		rg.fowardProposeWait.Trigger(key, errors.New("receive propose resp error"))
		return
	}
	rg.fowardProposeWait.Trigger(key, resps)
}
