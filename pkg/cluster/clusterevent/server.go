package clusterevent

import (
	"io"
	"os"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig"
	pb "github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/cpb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
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
	s.stopper.Stop()
	s.cfgServer.Stop()
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
	}
}

func (s *Server) AddMessage(m reactor.Message) {
	s.cfgServer.AddMessage(m)
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
