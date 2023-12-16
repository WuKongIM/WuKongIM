package raftgroup

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
)

type raftWorker struct {
	mu      sync.RWMutex
	rafts   map[uint32]*Raft
	stopper *syncutil.Stopper
	wklog.Log
	workerID   uint64
	notifyChan chan struct{}
	propChan   chan msgWithResult
	recvcChan  chan *RaftMessageReq

	readyRaft *readyRaft
	heartbeat time.Duration

	messageQueue *MessageQueue
}

func newRaftWorker(workerID uint64, heartbeat time.Duration, receiveQueueLength uint64, maxReceiveQueueSize uint64) *raftWorker {
	rw := &raftWorker{
		stopper:      syncutil.NewStopper(),
		Log:          wklog.NewWKLog("raftWorker"),
		workerID:     workerID,
		notifyChan:   make(chan struct{}, 1),
		readyRaft:    newReadyRaft(),
		heartbeat:    heartbeat,
		rafts:        make(map[uint32]*Raft),
		propChan:     make(chan msgWithResult, 256),
		recvcChan:    make(chan *RaftMessageReq, 256),
		messageQueue: NewMessageQueue(receiveQueueLength, false, 1, maxReceiveQueueSize),
	}

	return rw
}

func (r *raftWorker) start() {

	r.stopper.RunWorker(func() {
		r.stepWorkerLoop()
	})
}

func (r *raftWorker) stop() {
	r.stopper.Stop()
}

func (r *raftWorker) addRaft(raft *Raft) {
	r.mu.Lock()
	r.rafts[raft.opts.ShardID] = raft
	r.mu.Unlock()
}

func (r *raftWorker) removeRaft(shardID uint32) {
	r.mu.Lock()
	delete(r.rafts, shardID)
	r.mu.Unlock()
}

func (r *raftWorker) getRaft(shardID uint32) *Raft {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.rafts[shardID]
}

func (r *raftWorker) stepWorkerLoop() {
	ticker := time.NewTicker(r.heartbeat) // TODO: 这个时间应该可配置
	var err error
	for {
		r.processMsgs()
		r.processReady()
		select {
		case <-ticker.C:
			for _, raft := range r.rafts {
				raft.Tick()
			}
		case <-r.notifyChan:
		case m := <-r.recvcChan:
			err = r.processStep(m)
			if err != nil {
				r.Warn("failed to step raft message", zap.Error(err))
			}
		case pm := <-r.propChan:
			msg := pm.m
			err = r.processStep(msg)
			if pm.result != nil {
				pm.result <- err
				close(pm.result)
			}
		case <-r.stopper.ShouldStop():
			return

		}
	}
}

func (r *raftWorker) notify() {
	select {
	case r.notifyChan <- struct{}{}:
	default:
	}
}

func (r *raftWorker) recvRaftMessage(req *RaftMessageReq) {
	if added, stopped := r.messageQueue.Add(req); !added || stopped {
		r.Warn("dropped an incoming message")
	}
}

func (r *raftWorker) processMsgs() {
	msgs := r.messageQueue.Get()
	for _, m := range msgs {
		raft := r.getRaft(m.ShardID)
		if raft != nil {
			err := raft.Step(m.Message)
			if err != nil {
				r.Warn("failed to step raft message", zap.Error(err))
			}
		} else {
			r.Warn("raft not found", zap.Uint32("shardID", m.ShardID))
		}
	}
}

func (r *raftWorker) processReady() {
	active := r.getReadyRaft()
	if len(active) == 0 { // 如果没有激活的raft，就所有的都处理一下
		for sid := range r.rafts {
			active[sid] = struct{}{}
		}
	}
	if len(active) == 0 {
		return
	}
	for rid := range active {
		raftNode, ok := r.rafts[rid]
		if !ok {
			continue
		}
		raftNode.HandleReady()
	}
}

func (r *raftWorker) processStep(req *RaftMessageReq) error {
	raft := r.getRaft(req.ShardID)
	if raft != nil {
		err := raft.Step(req.Message)
		if err != nil {
			return err
		}
	} else {
		return fmt.Errorf("raft[%d] not found", req.ShardID)
	}
	return nil
}

func (r *raftWorker) propose(ctx context.Context, sharedID uint32, data []byte) error {
	return r.step(ctx, &RaftMessageReq{
		ShardID: sharedID,
		Message: raftpb.Message{
			Type:    raftpb.MsgProp,
			Entries: []raftpb.Entry{{Data: data}},
		},
	})
}

func (r *raftWorker) step(ctx context.Context, m *RaftMessageReq) error {
	return r.stepWithWaitOption(ctx, m, true)
}

func (r *raftWorker) stepWithNoWait(m *RaftMessageReq) error {
	return r.stepWithWaitOption(context.Background(), m, false)
}

func (r *raftWorker) stepWithWaitOption(ctx context.Context, m *RaftMessageReq, wait bool) error {
	if m.Message.Type != raftpb.MsgProp {
		select {
		case r.recvcChan <- m:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-r.stopper.ShouldStop():
			return raft.ErrStopped
		}
	}

	ch := r.propChan
	pm := msgWithResult{m: m}
	if wait {
		pm.result = make(chan error, 1)
	}
	select {
	case ch <- pm:
		if !wait {
			return nil
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-r.stopper.ShouldStop():
		return raft.ErrStopped
	}
	select {
	case err := <-pm.result:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-r.stopper.ShouldStop():
		return raft.ErrStopped
	}
	return nil

}

func (wr *raftWorker) raftReady(id uint32) {
	wr.readyRaft.setRaftReady(id)
	wr.notify()
}

func (wr *raftWorker) getReadyRaft() map[uint32]struct{} {

	return wr.readyRaft.getReadyRaft()
}

type msgWithResult struct {
	m      *RaftMessageReq
	result chan error
}
