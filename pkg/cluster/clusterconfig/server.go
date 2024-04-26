package clusterconfig

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/bwmarrin/snowflake"
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
	cancelCtx     context.Context
	cancelFnc     context.CancelFunc
	cfgGenId      *snowflake.Node

	storage   *PebbleShardLogStorage
	initNodes map[uint64]string // 初始化节点

	wklog.Log
}

func New(opts *Options) *Server {

	dataDir := path.Dir(opts.ConfigPath)
	s := &Server{
		opts:       opts,
		handlerKey: "config",
		cfg:        NewConfig(opts),
		stopper:    syncutil.NewStopper(),
		Log:        wklog.NewWKLog("clusterconfig.server"),
		storage:    NewPebbleShardLogStorage(path.Join(dataDir, "cfglogdb")),
		initNodes:  opts.InitNodes,
	}
	reactorOptions := reactor.NewOptions(reactor.WithNodeId(opts.NodeId), reactor.WithSend(s.send), reactor.WithSubReactorNum(1), reactor.WithTaskPoolSize(10))
	s.configReactor = reactor.New(reactorOptions)
	s.cancelCtx, s.cancelFnc = context.WithCancel(context.Background())
	var err error
	s.cfgGenId, err = snowflake.NewNode(int64(opts.NodeId))
	if err != nil {
		s.Panic("snowflake.NewNode failed", zap.Error(err))
	}

	return s
}

func (s *Server) Start() error {

	err := s.storage.Open()
	if err != nil {
		return err
	}

	s.stopper.RunWorker(s.run)

	err = s.configReactor.Start()
	if err != nil {
		return err
	}
	s.handler = newHandler(s.cfg, s.storage, s.opts)
	s.configReactor.AddHandler("config", s.handler)
	return nil
}

func (s *Server) Stop() {
	s.cancelFnc()
	s.stopper.Stop()
	s.configReactor.Stop()
	err := s.storage.Close()
	if err != nil {
		s.Error("storage close error", zap.Error(err))
	}
}

// AddMessage 添加消息
func (s *Server) AddMessage(m reactor.Message) {
	s.configReactor.AddMessage(m)
}

// AppliedConfig 获取应用配置
func (s *Server) AppliedConfig() *pb.Config {
	return s.cfg.appliedConfig()
}

// Config 当前配置
func (s *Server) Config() *pb.Config {
	return s.cfg.config()
}

// SlotCount 获取槽数量
func (s *Server) SlotCount() uint32 {
	return s.cfg.slotCount()
}

func (s *Server) SlotReplicaCount() uint32 {
	return s.cfg.slotReplicaCount()
}

// Slot 获取槽信息
func (s *Server) Slot(id uint32) *pb.Slot {
	return s.cfg.slot(id)
}

func (s *Server) Slots() []*pb.Slot {
	return s.cfg.slots()
}

func (s *Server) Nodes() []*pb.Node {
	return s.cfg.nodes()
}

func (s *Server) Node(id uint64) *pb.Node {
	return s.cfg.node(id)
}

// NodeOnline 节点是否在线
func (s *Server) NodeOnline(nodeId uint64) bool {
	return s.cfg.nodeOnline(nodeId)
}

// AllowVoteNodes 获取允许投票的节点
func (s *Server) AllowVoteNodes() []*pb.Node {
	return s.cfg.allowVoteNodes()
}

// 获取允许投票的并且已经加入了的节点数量
func (s *Server) AllowVoteAndJoinedNodes() []*pb.Node {
	return s.cfg.allowVoteAndJoinedNodes()
}

// OnlineNodes 获取在线节点
func (s *Server) OnlineNodes() []*pb.Node {
	return s.cfg.onlineNodes()
}

func (s *Server) IsLeader() bool {
	return s.handler.isLeader()
}

func (s *Server) LeaderId() uint64 {
	return s.handler.LeaderId()
}

func (s *Server) SetIsPrepared(prepared bool) {
	s.handler.SetIsPrepared(prepared)
}

// NodeConfigVersion 获取节点的配置版本（只有主节点才有这个信息）
func (s *Server) NodeConfigVersion(nodeId uint64) uint64 {
	return s.handler.configVersion(nodeId)
}

func (s *Server) ProposeUpdateApiServerAddr(nodeId uint64, apiServerAddr string) error {
	node := s.Node(nodeId)
	if node == nil {
		s.Error("proposeUpdateApiServerAddr failed, node not found", zap.Uint64("nodeId", nodeId))
		return fmt.Errorf("node not found")
	}
	newCfg := s.cfg.cfg.Clone()
	for i, node := range newCfg.Nodes {
		if node.Id == nodeId {
			newCfg.Nodes[i].ApiServerAddr = apiServerAddr
			break
		}
	}
	data, err := newCfg.Marshal()
	if err != nil {
		s.Error("proposeUpdateApiServerAddr failed", zap.Error(err))
		return err
	}

	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, time.Second*5)
	defer cancel()

	err = s.proposeAndWait(timeoutCtx, []replica.Log{
		{
			Id:   uint64(s.cfgGenId.Generate().Int64()),
			Data: data,
		},
	})
	if err != nil {
		s.Error("proposeUpdateApiServerAddr failed", zap.Error(err))
		return err
	}
	return nil
}

func (s *Server) ProposeConfig(ctx context.Context, cfg *pb.Config) error {

	data, err := cfg.Marshal()
	if err != nil {
		s.Error("ProposeConfig failed", zap.Error(err))
		return err
	}

	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, time.Second*5)
	defer cancel()
	err = s.proposeAndWait(timeoutCtx, []replica.Log{
		{
			Id:   uint64(s.cfgGenId.Generate().Int64()),
			Data: data,
		},
	})
	if err != nil {
		s.Error("ProposeConfig failed", zap.Error(err))
		return err
	}
	return nil
}

func (s *Server) ProposeJoin(ctx context.Context, node *pb.Node) error {

	if s.cfg.hasWillJoinNode() || s.cfg.hasJoiningNode() {
		s.Error("ProposeJoin failed, there is a joining node")
		return fmt.Errorf("there is a joining node")

	}

	newCfg := s.cfg.cfg.Clone()

	exist := false
	for _, n := range newCfg.Nodes {
		if n.Id == node.Id {
			exist = true
			break
		}
	}
	if !exist {
		newCfg.Nodes = append(newCfg.Nodes, node)
	}

	data, err := newCfg.Marshal()
	if err != nil {
		s.Error("ProposeJoin failed", zap.Error(err))
		return err
	}

	err = s.proposeAndWait(ctx, []replica.Log{
		{
			Id:   uint64(s.cfgGenId.Generate().Int64()),
			Data: data,
		},
	})
	if err != nil {
		s.Error("ProposeJoin failed", zap.Error(err))
		return err
	}
	return nil
}

func (s *Server) proposeAndWait(ctx context.Context, logs []replica.Log) error {
	_, err := s.configReactor.ProposeAndWait(ctx, s.handlerKey, logs)
	return err
}

func (s *Server) send(m reactor.Message) {
	s.opts.Send(m)
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
	if len(s.cfg.nodes()) < len(s.initNodes) {
		for replicaId, addr := range s.initNodes {
			if !s.cfg.hasNode(replicaId) {
				s.cfg.addNode(&pb.Node{
					Id:          replicaId,
					AllowVote:   true,
					ClusterAddr: addr,
					Online:      true,
					CreatedAt:   time.Now().Unix(),
					Status:      pb.NodeStatus_NodeStatusJoined,
				})
				hasUpdate = true
			}
		}
	}

	if len(s.cfg.slots()) == 0 {

		fmt.Println("no slots")
		replicas := make([]uint64, 0, len(s.cfg.nodes())) // 有效副本集合
		for _, node := range s.cfg.nodes() {
			if !node.AllowVote || node.Status != pb.NodeStatus_NodeStatusJoined {
				continue
			}
			replicas = append(replicas, node.Id)
		}
		if len(replicas) > 0 {
			offset := 0
			replicaCount := s.opts.SlotMaxReplicaCount
			for i := uint32(0); i < s.opts.SlotCount; i++ {
				slot := &pb.Slot{
					Id: i,
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
		data, err := s.cfg.data()
		if err != nil {
			s.Error("get data error", zap.Error(err))
			return
		}
		fmt.Println("proposeAndWait....")
		err = s.proposeAndWait(s.cancelCtx, []replica.Log{
			{
				Id:   uint64(s.cfgGenId.Generate().Int64()),
				Data: data,
			},
		})
		if err != nil {
			s.Error("propose error", zap.Error(err))
		}
	}
}
