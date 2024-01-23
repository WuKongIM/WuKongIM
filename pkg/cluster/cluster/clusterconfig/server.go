package clusterconfig

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig/pb"
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
	commitWait    *commitWait
	wklog.Log
}

func New(nodeId uint64, optList ...Option) *Server {
	opts := NewOptions()
	for _, opt := range optList {
		opt(opts)
	}
	opts.NodeId = nodeId

	s := &Server{
		configManager: NewConfigManager(opts),
		opts:          opts,
		stopper:       syncutil.NewStopper(),
		Log:           wklog.NewWKLog(fmt.Sprintf("Server[%d]", nodeId)),
		recvc:         make(chan Message, 100),
		readyc:        make(chan Ready),
		commitWait:    newCommitWait(),
	}

	s.node = NewNode(opts)
	s.node.SetConfigData(s.configManager.GetConfigData())
	return s
}

func (s *Server) Start() error {
	s.stopper.RunWorker(s.run)
	s.stopper.RunWorker(s.loopReady)

	s.opts.Transport.OnMessage(func(msg Message) {
		select {
		case s.recvc <- msg:
		case <-s.stopper.ShouldStop():
			return
		}
	})
	return nil
}

func (s *Server) Stop() {
	s.stopper.Stop()
	s.configManager.Close()
}

func (s *Server) ProposeConfigChange() error {
	waitC := s.commitWait.addWaitIndex(s.ConfigManager().Version())
	err := s.node.ProposeConfigChange(s.configManager.Version(), s.configManager.GetConfigData())
	if err != nil {
		return err
	}
	select {
	case <-waitC:
		return nil
	case <-s.stopper.ShouldStop():
		return ErrStopped
	}
}

func (s *Server) Step(ctx context.Context, m Message) error {
	select {
	case s.recvc <- m:
		if m.Type == EventApplyResp {
			s.Info("apply config success", zap.Uint64("configVersion", m.ConfigVersion))
			s.commitWait.commitIndex(m.ConfigVersion)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.stopper.ShouldStop():
		return ErrStopped
	}
}

func (s *Server) IsLeader() bool {
	return s.node.isLeader()
}

func (s *Server) ConfigManager() *ConfigManager {
	return s.configManager
}

func (s *Server) loopReady() {
	for {
		select {
		case rd := <-s.readyc:
			for _, msg := range rd.Messages {
				s.handleMessage(msg)
			}
		case <-s.stopper.ShouldStop():
			return
		}
	}
}

func (s *Server) handleMessage(m Message) {
	var (
		err error
	)
	if m.To == s.opts.NodeId {
		if m.Type == EventApply {
			s.handleApplyReq(m)
		} else {
			err = s.Step(context.Background(), m)
			if err != nil {
				s.Error("node step error", zap.Error(err))
			}
		}

		return
	}
	if m.To == None {
		s.Warn("message to none", zap.Uint64("from", m.From), zap.Uint64("to", m.To), zap.String("msgType", m.Type.String()))
		return
	}
	err = s.opts.Transport.Send(m)
	if err != nil {
		s.Error("send message error", zap.Error(err))
	}
}

func (s *Server) handleApplyReq(m Message) {
	if len(m.Config) == 0 {
		s.Panic("config is empty")
		return
	}
	newCfg := &pb.Config{}
	err := s.configManager.UnmarshalConfigData(m.Config, newCfg)
	if err != nil {
		s.Panic("unmarshal config error", zap.Error(err))
		return
	}
	err = s.configManager.UpdateConfig(newCfg)
	if err != nil {
		s.Panic("update config error", zap.Error(err))
		return
	}
	err = s.Step(context.Background(), Message{
		From:          m.To,
		To:            m.From,
		Type:          EventApplyResp,
		Term:          m.Term,
		ConfigVersion: m.ConfigVersion,
		Config:        m.Config,
	})
	if err != nil {
		s.Error("node step error", zap.Error(err))
		return
	}
	s.commitWait.commitIndex(m.ConfigVersion)
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
			leader = s.node.state.leader
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
