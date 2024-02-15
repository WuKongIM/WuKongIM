package clusterconfig

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type Server struct {
	configManager *ConfigManager // 配置管理
	opts          *Options
	node          *Node
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
		commitWait:    newCommitWait(),
	}
	opts.AppliedConfigVersion = s.configManager.GetConfig().Version
	s.node = NewNode(opts)
	s.node.SetConfigData(s.configManager.GetConfigData())
	return s
}

func (s *Server) Start() error {
	s.stopper.RunWorker(s.run)

	if s.opts.Transport != nil {
		s.opts.Transport.OnMessage(func(msg Message) {
			select {
			case s.recvc <- msg:
			case <-s.stopper.ShouldStop():
				return
			}
		})
	}
	return nil
}

func (s *Server) Stop() {
	s.stopper.Stop()
	s.configManager.Close()
}

func (s *Server) ProposeConfigChange(version uint64, cfgData []byte) error {
	waitC := s.commitWait.addWaitIndex(version)
	err := s.node.ProposeConfigChange(version, cfgData)
	if err != nil {
		return err
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), s.opts.ProposeTimeout)
	defer cancel()
	select {
	case <-waitC:
		return nil
	case <-timeoutCtx.Done():
		return timeoutCtx.Err()
	case <-s.stopper.ShouldStop():

		return ErrStopped
	}
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

func (s *Server) IsLeader() bool {
	return s.node.isLeader()
}

func (s *Server) SetReplicas(replicas []uint64) {
	s.node.opts.Replicas = replicas
}

func (s *Server) AddReplicaIfNotExists(replica uint64) {
	if wkutil.ArrayContainsUint64(s.node.opts.Replicas, replica) {
		return
	}
	s.node.opts.Replicas = append(s.node.opts.Replicas, replica)
}

func (s *Server) Leader() uint64 {
	return s.node.state.leader
}

func (s *Server) ConfigManager() *ConfigManager {
	return s.configManager
}

func (s *Server) handleMessage(m Message) {
	var (
		err error
	)
	if m.To == s.opts.NodeId {
		if m.Type == EventApply {
			s.handleApplyReq(m)
		} else {
			err = s.node.Step(m)
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
	s.Info("receive apply config", zap.Uint64("configVersion", m.ConfigVersion), zap.Uint64("committedVersion", m.CommittedVersion))
	// s.Info("receive apply config", zap.Uint64("configVersion", m.ConfigVersion))
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
	s.Info("apply config success", zap.Uint64("configVersion", m.ConfigVersion))
	err = s.node.Step(Message{
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
	var err error
	tick := time.NewTicker(time.Millisecond * 200)
	for {
		if s.node.HasReady() {
			rd = s.node.Ready()
			s.node.AcceptReady(rd)
		} else {
			rd = EmptyReady
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
		if !IsEmptyReady(rd) {
			for _, msg := range rd.Messages {
				s.handleMessage(msg)
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
		case <-s.stopper.ShouldStop():
			return
		}
	}
}

type ClusterEvent struct {
	Messages []Message
}

type Message struct {
	version          uint16    // 数据版本
	Type             EventType // 消息类型
	Term             uint32    // 当前任期
	From             uint64    // 源节点
	To               uint64    // 目标节点
	ConfigVersion    uint64    // 配置版本
	CommittedVersion uint64    // 已提交的配置版本
	Config           []byte    // 配置

}

func (m Message) Marshal() []byte {
	enc := wkproto.NewEncoder()
	defer enc.End()
	enc.WriteUint16(m.version)
	enc.WriteUint32(uint32(m.Type))
	enc.WriteUint32(m.Term)
	enc.WriteUint64(m.From)
	enc.WriteUint64(m.To)
	enc.WriteUint64(m.ConfigVersion)
	enc.WriteUint64(m.CommittedVersion)
	enc.WriteBytes(m.Config)
	return enc.Bytes()
}

func (m *Message) Unmarshal(data []byte) error {
	dec := wkproto.NewDecoder(data)
	var err error
	if m.version, err = dec.Uint16(); err != nil {
		return err
	}
	var ty uint32
	if ty, err = dec.Uint32(); err != nil {
		return err
	}
	m.Type = EventType(ty)

	if m.Term, err = dec.Uint32(); err != nil {
		return err
	}
	if m.From, err = dec.Uint64(); err != nil {
		return err
	}

	if m.To, err = dec.Uint64(); err != nil {
		return err
	}
	if m.ConfigVersion, err = dec.Uint64(); err != nil {
		return err
	}
	if m.CommittedVersion, err = dec.Uint64(); err != nil {
		return err
	}
	if m.Config, err = dec.BinaryAll(); err != nil {
		return err
	}
	return nil
}
