package node

import (
	"fmt"
	"path"

	"github.com/WuKongIM/WuKongIM/pkg/raft/raft"
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
)

type Server struct {
	opts *Options
	raft *raft.Raft

	config *Config // 分布式配置对象
	// 配置日志存储
	storage *PebbleShardLogStorage
}

func New(opts *Options) *Server {
	dataDir := path.Dir(opts.ConfigPath)
	s := &Server{
		opts:    opts,
		config:  NewConfig(opts),
		storage: NewPebbleShardLogStorage(path.Join(dataDir, "cfglogdb")),
	}

	return s
}

func (s *Server) Start() error {
	fmt.Println("start---->")
	err := s.storage.Open()
	if err != nil {
		return err
	}
	fmt.Println("start---->2")
	err = s.initRaft()
	if err != nil {
		return err
	}
	fmt.Println("start---->3")
	return s.raft.Start()
}

func (s *Server) initRaft() error {
	var raftConfig types.Config
	if s.config.isInitialized() {
		raftConfig = configToRaftConfig(s.config)
	} else {
		replicas := make([]uint64, 0, len(s.opts.InitNodes))
		for nodeId := range s.opts.InitNodes {
			replicas = append(replicas, nodeId)
		}
		raftConfig = types.Config{
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
	s.raft.Step(types.Event{
		Type:   types.ConfChange,
		Config: raftConfig,
	})

	return nil
}

func (s *Server) Stop() {
	s.raft.Stop()
	s.storage.Close()
}

func (s *Server) AddEvent(e Event) {
	if e.Type == RaftEvent {
		s.raft.Step(e.Event)
	}
}

func (s *Server) Listener(f func(e Event)) {

}

func configToRaftConfig(cfg *Config) types.Config {

	replicas := make([]uint64, 0, len(cfg.nodes()))
	for _, node := range cfg.nodes() {
		replicas = append(replicas, node.Id)
	}

	return types.Config{
		Replicas:    replicas,
		MigrateFrom: cfg.cfg.MigrateFrom,
		MigrateTo:   cfg.cfg.MigrateTo,
		Learners:    cfg.cfg.Learners,
		Term:        cfg.cfg.Term,
	}
}
