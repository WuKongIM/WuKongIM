package clusterconfig

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type Server struct {
	configReactor *reactor.Reactor
	cfg           *Config
	opts          *Options
	handlerKey    string
	handler       *handler
	stopper       *syncutil.Stopper
	wklog.Log
}

func New(opts *Options) *Server {
	s := &Server{
		opts:       opts,
		handlerKey: "config",
		cfg:        NewConfig(opts),
		stopper:    syncutil.NewStopper(),
		Log:        wklog.NewWKLog("clusterconfig.server"),
	}
	reactorOptions := reactor.NewOptions(reactor.WithNodeId(opts.NodeId), reactor.WithSend(s.send), reactor.WithSubReactorNum(1), reactor.WithTaskPoolSize(10))
	s.configReactor = reactor.New(reactorOptions)

	return s
}

func (s *Server) Start() error {

	s.stopper.RunWorker(s.run)

	err := s.configReactor.Start()
	if err != nil {
		return err
	}
	s.handler = newHandler(s.cfg, s.opts)
	s.configReactor.AddHandler("config", s.handler)
	return nil
}

func (s *Server) Stop() {
	s.stopper.Stop()
	s.configReactor.Stop()
}

func (s *Server) AddMessage(m replica.Message) {
	s.configReactor.AddMessage(reactor.Message{
		HandlerKey: s.handlerKey,
		Message:    m,
	})
}

func (s *Server) AppliedConfig() *pb.Config {
	return s.cfg.appliedConfig()
}

func (s *Server) send(m reactor.Message) {
	s.opts.Send(m.Message)
}

func (s *Server) run() {
	tk := time.NewTicker(time.Millisecond * 250)
	for {
		select {
		case <-tk.C:
			s.checkClusterConfig()
		case <-s.stopper.ShouldStop():
			return
		}
	}
}

func (s *Server) checkClusterConfig() {
	if !s.handler.isLeader() {
		return
	}
	hasUpdate := false
	if len(s.cfg.nodes()) != len(s.opts.InitNodes) {
		for replicaId, addr := range s.opts.InitNodes {
			if !s.cfg.hasNode(replicaId) {
				s.cfg.addNode(&pb.Node{
					Id:          replicaId,
					AllowVote:   true,
					ClusterAddr: addr,
					Online:      true,
					CreatedAt:   time.Now().Unix(),
					Status:      pb.NodeStatus_NodeStatusDone,
				})
				hasUpdate = true
			}
		}
	}

	if len(s.cfg.slots()) == 0 {
		replicas := make([]uint64, 0, len(s.cfg.nodes())) // 有效副本集合
		for _, node := range s.cfg.nodes() {
			if !node.AllowVote || node.Status != pb.NodeStatus_NodeStatusDone {
				continue
			}
			replicas = append(replicas, node.Id)
		}
		if len(replicas) > 0 {
			offset := 0
			replicaCount := s.opts.SlotMaxReplicaCount
			for i := uint32(0); i < s.opts.SlotCount; i++ {
				slot := &pb.Slot{
					Id:           i,
					ReplicaCount: s.opts.SlotCount,
				}
				if len(replicas) <= int(replicaCount) {
					slot.Replicas = replicas
				} else {
					slot.Replicas = make([]uint64, 0, replicaCount)
					for i := uint32(0); i < replicaCount; i++ {
						idx := (offset + int(i)) % len(replicas)
						slot.Replicas = append(slot.Replicas, replicas[idx])
					}
				}
				offset++
				// 随机选举一个领导者
				randomIndex := globalRand.Intn(len(slot.Replicas))
				slot.Term = 1
				slot.Leader = slot.Replicas[randomIndex]
				s.cfg.addSlot(slot)
				hasUpdate = true
			}
		}
	}

	if hasUpdate {
		s.cfg.setVersion(s.cfg.version() + 1)
		data, err := s.cfg.data()
		if err != nil {
			s.Error("get data error", zap.Error(err))
			return
		}
		err = s.configReactor.Propose(s.handlerKey, []replica.Log{
			{
				Id:    s.cfg.id(),
				Index: s.cfg.version(),
				Term:  s.cfg.term(),
				Data:  data,
			},
		})
		if err != nil {
			s.Error("propose error", zap.Error(err))
		}
	}
}
