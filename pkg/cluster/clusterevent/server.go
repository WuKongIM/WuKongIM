package clusterevent

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type Server struct {
	stopper      *syncutil.Stopper
	advanceC     chan struct{} // 推进
	cfgServer    *clusterconfig.Server
	preRemoteCfg *pb.Config // 上一次配置

	remoteCfgPath string
	localCfgPath  string
	localCfgFile  *os.File
	localCfg      *pb.Config
	opts          *Options
	wklog.Log

	msgs []Message

	// 用于记录每个节点的最后一次的回应心跳的tick间隔
	pongTickMap     map[uint64]int
	pongTickMapLock sync.RWMutex
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
		advanceC:      make(chan struct{}, 1),
		pongTickMap:   make(map[uint64]int),
	}
	s.cfgServer = clusterconfig.New(clusterconfig.NewOptions(
		clusterconfig.WithNodeId(opts.NodeId),
		clusterconfig.WithInitNodes(opts.InitNodes),
		clusterconfig.WithSlotCount(opts.SlotCount),
		clusterconfig.WithSlotMaxReplicaCount(opts.SlotMaxReplicaCount),
		clusterconfig.WithConfigPath(remoteCfgPath),
		clusterconfig.WithSend(opts.Send),
		clusterconfig.WithOnAppliedConfig(s.advance),
	))
	err = s.loadLocalConfig()
	if err != nil {
		s.Panic("Load local config failed!", zap.Error(err))
	}
	return s
}

func (s *Server) Start() error {
	s.preRemoteCfg = s.cfgServer.AppliedConfig().Clone()
	s.stopper.RunWorker(s.loop)
	return s.cfgServer.Start()
}

func (s *Server) Stop() {
	fmt.Println("clusterevent stop1")
	s.stopper.Stop()
	fmt.Println("clusterevent stop2")
	s.cfgServer.Stop()
	fmt.Println("clusterevent stop3")
}

func (s *Server) Step(m Message) {
	switch m.Type {
	case EventTypeNodeAdd:
		for _, n := range m.Nodes {
			exist := false
			for _, localNode := range s.localCfg.Nodes {
				if n.Equal(localNode) {
					exist = true
					break
				}
			}
			if !exist {
				s.localCfg.Nodes = append(s.localCfg.Nodes, n)
			}
		}
	case EventTypeNodeUpdate:
		for _, n := range m.Nodes {
			for i, localNode := range s.localCfg.Nodes {
				if n.Id == localNode.Id {
					s.localCfg.Nodes[i] = n
					break
				}
			}
		}
	case EventTypeNodeDelete:
		newNodes := make([]*pb.Node, 0, len(s.localCfg.Nodes))
		for _, localNode := range s.localCfg.Nodes {
			exist := false
			for _, n := range m.Nodes {
				if localNode.Id == n.Id {
					exist = true
					break
				}
			}
			if !exist {
				newNodes = append(newNodes, localNode)
			}
		}
		s.localCfg.Nodes = newNodes
	case EventTypeApiServerAddrUpdate:
		for _, n := range s.localCfg.Nodes {
			if n.Id == s.opts.NodeId {
				n.ApiServerAddr = s.opts.ApiServerAddr
				break
			}
		}
	}
}

func (s *Server) AddMessage(m reactor.Message) {
	s.cfgServer.AddMessage(m)

	if s.IsLeader() && m.MsgType == replica.MsgPong {
		s.pongTickMapLock.Lock()
		s.pongTickMap[m.From] = 0
		s.pongTickMapLock.Unlock()
	}
}

func (s *Server) SlotCount() uint32 {
	return s.cfgServer.SlotCount()
}

func (s *Server) Slot(id uint32) *pb.Slot {
	return s.cfgServer.Slot(id)
}

func (s *Server) Slots() []*pb.Slot {
	return s.cfgServer.Slots()
}

// NodeOnline 节点是否在线
func (s *Server) NodeOnline(nodeId uint64) bool {
	return s.cfgServer.NodeOnline(nodeId)
}

func (s *Server) Nodes() []*pb.Node {
	return s.cfgServer.Nodes()
}

func (s *Server) Node(id uint64) *pb.Node {
	return s.cfgServer.Node(id)
}

func (s *Server) IsLeader() bool {
	return s.cfgServer.IsLeader()
}

func (s *Server) LeaderId() uint64 {
	return s.cfgServer.LeaderId()
}

// AppliedConfig 获取应用配置
func (s *Server) AppliedConfig() *pb.Config {
	return s.cfgServer.AppliedConfig()
}

// Config 当前配置
func (s *Server) Config() *pb.Config {
	return s.cfgServer.Config()
}

// AllowVoteNodes 获取允许投票的节点
func (s *Server) AllowVoteNodes() []*pb.Node {
	return s.cfgServer.AllowVoteNodes()
}

func (s *Server) ProposeUpdateApiServerAddr(nodeId uint64, apiServerAddr string) error {
	return s.cfgServer.ProposeUpdateApiServerAddr(nodeId, apiServerAddr)
}

func (s *Server) ProposeConfig(ctx context.Context, cfg *pb.Config) error {
	return s.cfgServer.ProposeConfig(ctx, cfg)
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
