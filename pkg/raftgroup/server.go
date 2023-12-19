package raftgroup

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path"
	"sync"
	"time"

	"github.com/RussellLuo/timingwheel"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.etcd.io/raft/v3/raftpb"
	"go.uber.org/zap"
)

type Server struct {
	mu struct {
		sync.RWMutex
		workers []*raftWorker
	}
	wklog.Log
	opts         *Options
	shardStorage *shardStorage

	timingWheel *timingwheel.TimingWheel
	metrics     *metrics
}

func New(nodeID uint64, addr string, opts ...Option) *Server {

	defaultOpts := NewOptions()
	defaultOpts.NodeID = nodeID
	defaultOpts.Addr = addr
	defaultOpts.DataDir = path.Join(os.TempDir(), "server", fmt.Sprintf("%d", nodeID))
	if len(opts) > 0 {
		for _, opt := range opts {
			opt(defaultOpts)
		}
	}

	rg := &Server{
		Log:         wklog.NewWKLog(fmt.Sprintf("RaftGroup[%d]", defaultOpts.NodeID)),
		opts:        defaultOpts,
		timingWheel: timingwheel.NewTimingWheel(defaultOpts.TimingWheelTick, defaultOpts.TimingWheelSize),
		metrics:     newMetrics(),
	}

	// 创建数据目录
	err := os.MkdirAll(defaultOpts.DataDir, 0755)
	if err != nil {
		rg.Panic("create data dir error", zap.Error(err))
	}

	rg.shardStorage = newShardStorage(defaultOpts.DataDir)

	if defaultOpts.ConnectionManager == nil {
		defaultOpts.ConnectionManager = NewConnectionManager()
	}
	if defaultOpts.Transport == nil {
		defaultOpts.Transport = NewTransport(defaultOpts.NodeID, defaultOpts.Addr, defaultOpts.ConnectionManager)
	}

	// 初始化worker
	rg.mu.workers = make([]*raftWorker, defaultOpts.WorkerCount)
	for i := 0; i < defaultOpts.WorkerCount; i++ {
		rg.mu.workers[i] = newRaftWorker(uint64(i), defaultOpts.Heartbeat, defaultOpts.ReceiveQueueLength, defaultOpts.MaxReceiveQueueSize)
	}

	return rg
}

func (s *Server) Start() error {

	s.Schedule(time.Second*5, func() {
		s.metrics.printMetrics(fmt.Sprintf("Server:%d", s.opts.NodeID))
	})

	s.timingWheel.Start()

	s.opts.Transport.OnRecvRaftMessage(s.onRecvRaftMessage)

	err := s.opts.Transport.Start()
	if err != nil {
		return err
	}
	err = s.shardStorage.Open()
	if err != nil {
		s.Panic("open shard storage error", zap.Error(err))
	}

	for _, w := range s.mu.workers {
		w.start()
	}
	return nil
}

func (s *Server) Stop() {

	s.timingWheel.Stop()

	for _, w := range s.mu.workers {
		w.stop()

		for _, raft := range w.rafts {
			raft.opts.Storage.Close()
		}
	}

	err := s.shardStorage.Close()
	if err != nil {
		s.Warn("close shard storage error", zap.Error(err))
	}
	s.opts.Transport.Stop()
}

func (s *Server) AddRaft(nodeID uint64, shardID uint32, listenAddr string, opts ...RaftOption) *Raft {
	raftOpts := NewRaftOptions()
	raftOpts.NodeID = nodeID
	raftOpts.ShardID = shardID
	raftOpts.Addr = listenAddr

	// 获取 大于 s.opts.Heartbeat 并小于 2倍s.opts.Heartbeat的随机数
	randHeartbeat := time.Duration(rand.Int63n(int64(s.opts.Heartbeat))) + s.opts.Heartbeat

	raftOpts.Heartbeat = randHeartbeat
	raftOpts.onSetReady = func(raft *Raft) {
		s.getWorker(raft.opts.ShardID).raftReady(raft.opts.ShardID)
	}
	raftOpts.OnSend = s.sendMessage
	storage := NewRaftStorage(s.opts.DataDir, nodeID, shardID, s.shardStorage)
	err := storage.Open()
	if err != nil {
		s.Panic("open raft storage error", zap.Error(err))
	}
	raftOpts.Storage = storage
	applied, err := s.shardStorage.GetApplied(shardID)
	if err != nil {
		s.Panic("get applied error", zap.Error(err))
	}
	raftOpts.Applied = applied
	if len(opts) > 0 {
		for _, opt := range opts {
			opt(raftOpts)
		}
	}
	raftOpts.OnApply = s.onApply
	raftOpts.OnRaftTimeoutCampaign = s.onRaftTimeoutCampaign
	rt := NewRaft(raftOpts)
	s.addRaft(rt)
	return rt
}

func (s *Server) addRaft(rt *Raft) {
	s.getWorker(rt.opts.ShardID).addRaft(rt)
}

func (s *Server) RemoveRaft(shardID uint32) {
	s.getWorker(shardID).removeRaft(shardID)
}

func (s *Server) GetRaft(id uint32) *Raft {
	return s.getWorker(id).getRaft(id)
}

func (s *Server) Bootstrap(shardID uint32) error {
	raft := s.getWorker(shardID).getRaft(shardID)
	if raft == nil {
		return fmt.Errorf("raft not found")
	}
	return raft.Bootstrap()
}

func (s *Server) Propose(ctx context.Context, shardID uint32, data []byte) error {
	worker := s.getWorker(shardID)
	return worker.propose(ctx, shardID, data)
}

func (s *Server) WaitAllLeaderChange(timeout time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	tick := time.NewTicker(time.Millisecond * 200)
	defer cancel()
	for {
		select {
		case <-timeoutCtx.Done():
			return timeoutCtx.Err()
		case <-tick.C:
			hasAllLeader := true
			for _, worker := range s.mu.workers {
				for _, raft := range worker.rafts {
					if raft.leaderID.Load() == 0 {
						hasAllLeader = false
					}
				}
			}
			if hasAllLeader {
				return nil
			}
		}
	}
}

func (s *Server) getWorker(shardID uint32) *raftWorker {
	return s.mu.workers[shardID%uint32(len(s.mu.workers))]
}

func (s *Server) sendMessage(ctx context.Context, raft *Raft, msg raftpb.Message) error {
	s.metrics.sendMsgCountAdd(1)
	s.metrics.sendMsgBytesAdd(uint64(msg.Size()))
	return s.opts.Transport.Send(ctx, &RaftMessageReq{
		Message: msg,
		ShardID: raft.opts.ShardID,
	})
}

func (s *Server) onRecvRaftMessage(req *RaftMessageReq) {
	s.metrics.recvMsgCountAdd(1)
	s.metrics.recvMsgBytesAdd(uint64(req.Message.Size()))

	s.getWorker(req.ShardID).recvRaftMessage(req)
}

func (s *Server) onApply(raft *Raft, entries []raftpb.Entry) error {
	for _, entry := range entries {
		if entry.Type == raftpb.EntryConfChange {
			var cc raftpb.ConfChange
			err := cc.Unmarshal(entry.Data)
			if err != nil {
				return err
			}
			switch cc.Type {
			case raftpb.ConfChangeAddNode:

				addr, err := getAddrByConfContext(cc.Context)
				if err != nil {
					s.Panic("get addr by conf context error", zap.Error(err))
				}
				s.opts.ConnectionManager.AddNode(cc.NodeID, addr)
				confstate := raft.ApplyConfChange(cc)
				err = raft.opts.Storage.SetConfState(confstate)
				if err != nil {
					return err
				}
			case raftpb.ConfChangeRemoveNode:
				s.opts.ConnectionManager.RemoveNode(cc.NodeID)
			}

		}
	}
	return nil
}

func (s *Server) onRaftTimeoutCampaign(raft *Raft) {
	s.metrics.campaignTimeoutCountAdd(1)
}

// Schedule 延迟任务
func (s *Server) Schedule(interval time.Duration, f func()) *timingwheel.Timer {
	return s.timingWheel.ScheduleFunc(&everyScheduler{
		Interval: interval,
	}, f)
}

type everyScheduler struct {
	Interval time.Duration
}

func (s *everyScheduler) Next(prev time.Time) time.Time {
	return prev.Add(s.Interval)
}
