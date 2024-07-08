package clusterevent

import (
	"io"
	"os"

	"go.uber.org/atomic"
	"go.uber.org/zap"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/lni/goutils/syncutil"
)

type Server struct {
	stopper *syncutil.Stopper
	opts    *Options
	wklog.Log
	cfgServer *clusterconfig.Server // 配置服务

	remoteCfg *pb.Config // 远程配置
	localCfg  *pb.Config // 本地配置

	remoteCfgPath string
	localCfgPath  string
	localCfgFile  *os.File

	// 用于记录每个节点的最后一次的回应心跳的tick间隔
	pongTickMap map[uint64]int

	stopped atomic.Bool

	pongC chan uint64
}

func New(opts *Options) *Server {

	err := os.MkdirAll(opts.ConfigDir, os.ModePerm)
	if err != nil {
		wklog.Panic("create config dir error", zap.Error(err))
	}

	localCfgPath := opts.ConfigDir + "/local.json"
	remoteCfgPath := opts.ConfigDir + "/remote.json"

	s := &Server{
		remoteCfgPath: remoteCfgPath,
		localCfgPath:  localCfgPath,
		localCfg:      &pb.Config{},
		opts:          opts,
		Log:           wklog.NewWKLog("clusterevent"),
		stopper:       syncutil.NewStopper(),
		pongTickMap:   make(map[uint64]int),
		pongC:         make(chan uint64, 100),
	}

	s.cfgServer = clusterconfig.New(clusterconfig.NewOptions(
		clusterconfig.WithNodeId(opts.NodeId),
		clusterconfig.WithInitNodes(opts.InitNodes),
		clusterconfig.WithSeed(opts.Seed),
		clusterconfig.WithSlotCount(opts.SlotCount),
		clusterconfig.WithSlotMaxReplicaCount(opts.SlotMaxReplicaCount),
		clusterconfig.WithChannelMaxReplicaCount(opts.ChannelMaxReplicaCount),
		clusterconfig.WithConfigPath(remoteCfgPath),
		clusterconfig.WithSend(opts.Send),
		clusterconfig.WithOnAppliedConfig(s.onAppliedConfig),
		clusterconfig.WithCluster(opts.Cluster),
		clusterconfig.WithElectionIntervalTick(opts.ElectionIntervalTick),
		clusterconfig.WithHeartbeatIntervalTick(opts.HeartbeatIntervalTick),
		clusterconfig.WithTickInterval(opts.TickInterval),
	))
	err = s.loadLocalConfig()
	if err != nil {
		s.Panic("Load local config failed!", zap.Error(err))
	}
	return s
}

func (s *Server) Start() error {
	err := s.cfgServer.Start()
	if err != nil {
		return err
	}
	s.remoteCfg = s.cfgServer.Config().Clone()
	s.stopper.RunWorker(s.loop)
	return nil
}

func (s *Server) Stop() {

	s.stopped.Store(true)
	s.stopper.Stop()
	s.cfgServer.Stop()

}

func (s *Server) IsLeader() bool {
	return s.cfgServer.IsLeader()
}

func (s *Server) onAppliedConfig() {

	cfg := s.cfgServer.Config()
	s.remoteCfg = cfg.Clone()

	if s.opts.OnClusterConfigChange != nil {
		s.opts.OnClusterConfigChange(cfg)
	}
}

func (s *Server) AddMessage(m reactor.Message) {
	s.cfgServer.AddMessage(m)

	if s.IsLeader() && m.MsgType == replica.MsgPong {
		s.pongC <- m.From
	}
}

func (s *Server) loadLocalConfig() error {
	clusterCfgPath := s.localCfgPath
	var err error
	s.localCfgFile, err = os.OpenFile(clusterCfgPath, os.O_RDWR|os.O_CREATE, os.ModePerm)
	if err != nil {
		s.Panic("Open cluster config file failed!", zap.Error(err))
	}

	data, err := io.ReadAll(s.localCfgFile)
	if err != nil {
		s.Panic("Read cluster config file failed!", zap.Error(err))
	}
	if len(data) > 0 {
		if err := wkutil.ReadJSONByByte(data, s.localCfg); err != nil {
			s.Panic("Unmarshal cluster config failed!", zap.Error(err))
		}
	}
	return nil
}

func (s *Server) Nodes() []*pb.Node {

	return s.cfgServer.Nodes()
}

func (s *Server) Slots() []*pb.Slot {

	return s.cfgServer.Slots()
}

func (s *Server) SlotCount() uint32 {
	return s.cfgServer.SlotCount()
}

func (s *Server) Node(nodeId uint64) *pb.Node {

	return s.cfgServer.Node(nodeId)
}

func (s *Server) NodeOnline(nodeId uint64) bool {

	return s.cfgServer.NodeOnline(nodeId)
}

func (s *Server) Config() *pb.Config {

	return s.cfgServer.Config()
}

func (s *Server) LeaderId() uint64 {

	return s.cfgServer.LeaderId()
}

func (s *Server) Slot(slotId uint32) *pb.Slot {

	return s.cfgServer.Slot(slotId)
}

func (s *Server) AllowVoteAndJoinedNodeCount() int {

	return s.cfgServer.AllowVoteAndJoinedNodeCount()
}

func (s *Server) AllowVoteAndJoinedNodes() []*pb.Node {

	return s.cfgServer.AllowVoteAndJoinedNodes()
}

func (s *Server) AllowVoteAndJoinedOnlineNodes() []*pb.Node {

	return s.cfgServer.AllowVoteAndJoinedOnlineNodes()
}

func (s *Server) ProposeJoin(node *pb.Node) error {

	return s.cfgServer.ProposeJoin(node)
}

func (s *Server) ProposeMigrateSlot(slotId uint32, fromNodeId, toNodeId uint64) error {

	return s.cfgServer.ProposeMigrateSlot(slotId, fromNodeId, toNodeId)
}

func (s *Server) ProposeSlots(slots []*pb.Slot) error {

	return s.cfgServer.ProposeSlots(slots)
}

func (s *Server) ProposeJoined(nodeId uint64, slots []*pb.Slot) error {

	return s.cfgServer.ProposeJoined(nodeId, slots)

}

// GetLogsInReverseOrder 获取日志
func (s *Server) GetLogsInReverseOrder(startLogIndex uint64, endLogIndex uint64, limit int) ([]replica.Log, error) {

	return s.cfgServer.GetLogsInReverseOrder(startLogIndex, endLogIndex, limit)
}

func (s *Server) saveLocalConfig(cfg *pb.Config) error {

	err := s.localCfgFile.Truncate(0)
	if err != nil {
		return err
	}
	if _, err := s.localCfgFile.WriteAt([]byte(wkutil.ToJSON(cfg)), 0); err != nil {
		return err
	}
	return nil
}

func (s *Server) AppliedLogIndex() (uint64, error) {

	return s.cfgServer.AppliedLogIndex()
}

func (s *Server) LastLogIndex() (uint64, error) {

	return s.cfgServer.LastLogIndex()
}

func (s *Server) NodeConfigVersion(nodeId uint64) uint64 {
	return s.cfgServer.NodeConfigVersion(nodeId)
}
