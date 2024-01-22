package clusterconfig

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type Server struct {
	configManager *ConfigManager
	opts          *Options
	node          *Node
	readyc        chan Ready
	recvc         chan Message
	stopper       *syncutil.Stopper
	wklog.Log
}

func New(nodeId uint64, optList ...Option) *Server {
	opts := NewOptions()
	for _, opt := range optList {
		opt(opts)
	}
	opts.NodeId = nodeId

	nd := NewNode(opts)
	return &Server{
		configManager: NewConfigManager(opts),
		opts:          opts,
		node:          nd,
		stopper:       syncutil.NewStopper(),
		Log:           wklog.NewWKLog(fmt.Sprintf("Server[%d]", nodeId)),
		recvc:         make(chan Message),
		readyc:        make(chan Ready),
	}
}

func (s *Server) Start() error {
	s.stopper.RunWorker(s.run)
	return nil
}

func (s *Server) Stop() {
	s.stopper.Stop()
}

func (s *Server) Step(ctx context.Context, m Message) error {
	select {
	case s.recvc <- m:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.stopper.ShouldStop():
		return ErrStopped
	}
}

func (s *Server) ConfigManager() *ConfigManager {
	return s.configManager
}

func (s *Server) run() {
	var rd Ready
	leader := None
	var readyc chan Ready
	var err error
	tick := time.NewTicker(time.Millisecond * 200)
	for {
		if s.node.HasReady() {
			rd = s.node.Ready()
			readyc = s.readyc
		}
		if leader != s.node.state.leader {
			if s.node.HasLeader() {
				if leader == None {
					s.Info("elected leader", zap.Uint32("term", s.node.state.term), zap.Uint64("leader", s.node.state.leader))
				} else {
					s.Info("changed leader", zap.Uint32("term", s.node.state.term), zap.Uint64("leader", s.node.state.leader))
				}
			} else {
				s.Info("lost leader", zap.Uint32("term", s.node.state.term), zap.Uint64("leader", s.node.state.leader))
			}
		}
		select {
		case <-tick.C:
			s.node.Tick()
		case msg := <-s.recvc:
			err = s.node.Step(msg)
			if err != nil {
				s.Error("node step error", zap.Error(err))
			}
		case readyc <- rd:
			s.node.AcceptReady(rd)
			readyc = nil
		case <-s.stopper.ShouldStop():
			return
		}
	}
}

type ClusterEvent struct {
	Messages []Message
}

type Message struct {
	Type             EventType // 消息类型
	Term             uint32    // 当前任期
	From             uint64    // 源节点
	To               uint64    // 目标节点
	ConfigVersion    uint64    // 配置版本
	CommittedVersion uint64    // 已提交的配置版本
	Config           []byte    // 配置
}

func (s *Server) Tick() {

}
