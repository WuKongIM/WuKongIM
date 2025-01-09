package clusterconfig

import (
	"context"
	"os"
	"path"

	"github.com/WuKongIM/WuKongIM/pkg/cluster2/node/types"
	pb "github.com/WuKongIM/WuKongIM/pkg/cluster2/node/types"
	"github.com/WuKongIM/WuKongIM/pkg/raft/raft"
	rafttypes "github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/bwmarrin/snowflake"
	"go.uber.org/zap"
)

type Server struct {
	opts *Options
	raft *raft.Raft

	config *Config // 分布式配置对象
	// 配置日志存储
	storage *PebbleShardLogStorage

	cfgGenId *snowflake.Node

	wklog.Log

	listeners []IEvent
}

func New(opts *Options) *Server {
	dataDir := path.Dir(opts.ConfigPath)

	// 是否存在dataDir,不存在则创建
	if !pathExists(dataDir) {
		err := mkDirAll(dataDir)
		if err != nil {
			panic(err)
		}
	}

	s := &Server{
		opts:   opts,
		config: NewConfig(opts),
		Log:    wklog.NewWKLog("node"),
	}

	s.storage = NewPebbleShardLogStorage(path.Join(dataDir, "cfglogdb"), s)

	var err error
	s.cfgGenId, err = snowflake.NewNode(int64(opts.NodeId))
	if err != nil {
		s.Panic("snowflake.NewNode failed", zap.Error(err))
	}

	return s
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil || os.IsExist(err)
}

func mkDirAll(p string) error {
	return os.MkdirAll(p, os.ModePerm)
}

func (s *Server) Start() error {

	err := s.storage.Open()
	if err != nil {
		return err
	}
	err = s.initRaft()
	if err != nil {
		return err
	}
	return s.raft.Start()
}

func (s *Server) Options() *Options {
	return s.opts
}

func (s *Server) initRaft() error {
	var raftConfig rafttypes.Config
	if s.config.isInitialized() {
		raftConfig = configToRaftConfig(s.config)
	} else {
		replicas := make([]uint64, 0, len(s.opts.InitNodes))
		for nodeId := range s.opts.InitNodes {
			replicas = append(replicas, nodeId)
		}
		raftConfig = rafttypes.Config{
			Replicas: replicas,
			Term:     1,
		}
	}

	s.raft = raft.New(raft.NewOptions(
		raft.WithNodeId(s.opts.NodeId),
		raft.WithTransport(newRaftTransport(s)),
		raft.WithStorage(s.storage),
		raft.WithElectionOn(true),
	))
	s.raft.Step(rafttypes.Event{
		Type:   rafttypes.ConfChange,
		Config: raftConfig,
	})

	return nil
}

func (s *Server) Stop() {
	s.raft.Stop()
	s.storage.Close()
}

func (s *Server) AddEventListener(listener IEvent) {
	s.listeners = append(s.listeners, listener)
}

func (s *Server) RemoveEventListener(listener IEvent) {
	for i, l := range s.listeners {
		if l == listener {
			s.listeners = append(s.listeners[:i], s.listeners[i+1:]...)
			break
		}
	}
}

func (s *Server) NotifyConfigChangeEvent() {
	for _, l := range s.listeners {
		l.OnConfigChange(s.GetClusterConfig())
	}
}

func (s *Server) Propose(id uint64, data []byte) (*rafttypes.ProposeResp, error) {
	return s.raft.Propose(id, data)
}

func (s *Server) ProposeUntilAppliedTimeout(ctx context.Context, id uint64, data []byte) (*rafttypes.ProposeResp, error) {

	return s.raft.ProposeUntilAppliedTimeout(ctx, id, data)
}

func (s *Server) ProposeUntilApplied(id uint64, data []byte) (*rafttypes.ProposeResp, error) {
	return s.raft.ProposeUntilApplied(id, data)
}

func (s *Server) ProposeBatchTimeout(ctx context.Context, reqs []rafttypes.ProposeReq) ([]*rafttypes.ProposeResp, error) {
	return s.raft.ProposeBatchTimeout(ctx, reqs)
}

func (s *Server) ProposeBatchUntilAppliedTimeout(ctx context.Context, reqs []rafttypes.ProposeReq) ([]*rafttypes.ProposeResp, error) {
	return s.raft.ProposeBatchUntilAppliedTimeout(ctx, reqs)
}

func (s *Server) StepRaftEvent(e rafttypes.Event) {
	if s.raft == nil {
		return
	}
	s.raft.Step(e)
}

func (s *Server) IsLeader() bool {
	if s.raft == nil {
		return false
	}
	return s.raft.IsLeader()
}

func (s *Server) LeaderId() uint64 {
	if s.raft == nil {
		return 0
	}
	return s.raft.LeaderId()
}

func (s *Server) GetClusterConfig() *types.Config {
	if s.config.cfg == nil {
		return nil
	}
	return s.config.cfg
}

// 是否已初始化
func (s *Server) IsInitialized() bool {
	return s.config.isInitialized()
}

func (s *Server) Node(id uint64) *pb.Node {
	return s.config.node(id)
}

func (s *Server) Nodes() []*pb.Node {
	return s.config.nodes()
}
func (s *Server) Slots() []*pb.Slot {
	return s.config.slots()
}

// AllowVoteNodes 获取允许投票的节点
func (s *Server) AllowVoteNodes() []*pb.Node {
	return s.config.allowVoteNodes()
}

// 获取允许投票的并且已经加入了的节点集合
func (s *Server) AllowVoteAndJoinedNodes() []*pb.Node {
	return s.config.allowVoteAndJoinedNodes()
}

// 获取允许投票的并且已经加入了的节点数量
func (s *Server) AllowVoteAndJoinedNodeCount() int {
	return s.config.allowVoteAndJoinedNodeCount()
}

// 获取允许投票的并且已经加入了的在线节点数量
func (s *Server) AllowVoteAndJoinedOnlineNodeCount() int {
	return s.config.allowVoteAndJoinedOnlineNodeCount()
}

// 获取允许投票的并且已经加入了的在线节点
func (s *Server) AllowVoteAndJoinedOnlineNodes() []*pb.Node {

	return s.config.allowVoteAndJoinedOnlineNodes()
}

// 生成配置ID
func (s *Server) genConfigId() uint64 {
	return uint64(s.cfgGenId.Generate().Int64())
}

func configToRaftConfig(cfg *Config) rafttypes.Config {

	replicas := make([]uint64, 0, len(cfg.nodes()))
	for _, node := range cfg.nodes() {
		replicas = append(replicas, node.Id)
	}

	return rafttypes.Config{
		Replicas:    replicas,
		MigrateFrom: cfg.cfg.MigrateFrom,
		MigrateTo:   cfg.cfg.MigrateTo,
		Learners:    cfg.cfg.Learners,
		Term:        cfg.cfg.Term,
	}
}
