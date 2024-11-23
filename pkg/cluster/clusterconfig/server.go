package clusterconfig

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/bwmarrin/snowflake"
	"go.uber.org/zap"
)

type Server struct {
	configReactor *reactor.Reactor
	cfg           *Config
	opts          *Options
	handlerKey    string
	handler       *handler
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
		Log:        wklog.NewWKLog(fmt.Sprintf("clusterconfig.server[%d]", opts.NodeId)),
		storage:    NewPebbleShardLogStorage(path.Join(dataDir, "cfglogdb")),
		initNodes:  opts.InitNodes,
	}
	reactorOptions := reactor.NewOptions(
		reactor.WithNodeId(opts.NodeId),
		reactor.WithReactorType(reactor.ReactorTypeConfig),
		reactor.WithSend(s.send),
		reactor.WithSubReactorNum(1),
		reactor.WithTaskPoolSize(10),
		reactor.WithRequest(NewRequest(s)),
		reactor.WithIsCommittedAfterApplied(true),
		reactor.WithTickInterval(opts.TickInterval),
		// reactor.WithOnAppendLogs(func(reqs []reactor.AppendLogReq) error {
		// 	if len(reqs) == 1 {
		// 		return s.storage.AppendLog(reqs[0].Logs)
		// 	}
		// 	var logs []replica.Log
		// 	for _, req := range reqs {
		// 		logs = append(logs, req.Logs...)
		// 	}

		// 	return s.storage.AppendLog(logs)
		// }),
	)
	s.configReactor = reactor.New(reactorOptions)
	s.cancelCtx, s.cancelFnc = context.WithCancel(context.Background())
	var err error
	s.cfgGenId, err = snowflake.NewNode(int64(opts.NodeId))
	if err != nil {
		s.Panic("snowflake.NewNode failed", zap.Error(err))
	}

	return s
}

func (s *Server) SwitchConfig(cfg *pb.Config) error {

	replicas := make([]uint64, 0, len(cfg.Nodes))
	for _, node := range cfg.Nodes {
		if len(cfg.Learners) > 0 && wkutil.ArrayContainsUint64(cfg.Learners, node.Id) {
			continue
		}
		replicas = append(replicas, node.Id)
	}

	replicaCfg := replica.Config{
		MigrateFrom: cfg.MigrateFrom,
		MigrateTo:   cfg.MigrateTo,
		Learners:    cfg.Learners,
		Replicas:    replicas,
		Term:        cfg.Term,
		Version:     cfg.Version,
	}

	err := s.configReactor.StepWait(s.handlerKey, replica.Message{
		MsgType: replica.MsgConfigResp,
		Config:  replicaCfg,
	})
	return err
}

func (s *Server) Start() error {

	s.setRoutes() // 设置路由

	err := s.storage.Open()
	if err != nil {
		return err
	}

	err = s.configReactor.Start()
	if err != nil {
		return err
	}
	s.handler = newHandler(s.cfg, s.storage, s)
	s.configReactor.AddHandler(s.handlerKey, s.handler)

	if !s.cfg.isInitialized() { // 没有初始化
		replicas := make([]uint64, 0, len(s.opts.InitNodes))
		for replicaId := range s.opts.InitNodes {
			replicas = append(replicas, replicaId)
		}
		// var role = replica.RoleUnknown
		var learners []uint64
		if strings.TrimSpace(s.opts.Seed) != "" {
			learners = []uint64{s.opts.NodeId}
		}

		s.configReactor.Step(s.handlerKey, replica.Message{
			MsgType: replica.MsgConfigResp,
			Config: replica.Config{
				Replicas: replicas,
				Learners: learners,
			},
		})

	} else {
		// 切换配置
		err = s.SwitchConfig(s.cfg.config())
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *Server) Stop() {
	s.cancelFnc()
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

// 获取允许投票的并且已经加入了的节点集合
func (s *Server) AllowVoteAndJoinedNodes() []*pb.Node {
	return s.cfg.allowVoteAndJoinedNodes()
}

// 获取允许投票的并且已经加入了的节点数量
func (s *Server) AllowVoteAndJoinedNodeCount() int {
	return s.cfg.allowVoteAndJoinedNodeCount()
}

// 获取允许投票的并且已经加入了的在线节点数量
func (s *Server) AllowVoteAndJoinedOnlineNodeCount() int {
	return s.cfg.allowVoteAndJoinedOnlineNodeCount()
}

// 获取允许投票的并且已经加入了的在线节点
func (s *Server) AllowVoteAndJoinedOnlineNodes() []*pb.Node {

	return s.cfg.allowVoteAndJoinedOnlineNodes()
}

// OnlineNodes 获取在线节点
func (s *Server) OnlineNodes() []*pb.Node {
	return s.cfg.onlineNodes()
}

func (s *Server) IsLeader() bool {
	return s.handler.isLeader()
}

// ConfigIsInitialized 配置是否初始化
func (s *Server) ConfigIsInitialized() bool {
	return s.cfg.isInitialized()
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

// func (s *Server) ProposeUpdateApiServerAddr(nodeId uint64, apiServerAddr string) error {

// 	s.proposeLock.Lock()
// 	defer s.proposeLock.Unlock()

// 	node := s.Node(nodeId)
// 	if node == nil {
// 		s.Error("proposeUpdateApiServerAddr failed, node not found", zap.Uint64("nodeId", nodeId))
// 		return fmt.Errorf("node not found")
// 	}
// 	newCfg := s.cfg.cfg.Clone()
// 	for i, node := range newCfg.Nodes {
// 		if node.Id == nodeId {
// 			newCfg.Nodes[i].ApiServerAddr = apiServerAddr
// 			break
// 		}
// 	}
// 	data, err := newCfg.Marshal()
// 	if err != nil {
// 		s.Error("proposeUpdateApiServerAddr failed", zap.Error(err))
// 		return err
// 	}

// 	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, time.Second*5)
// 	defer cancel()

// 	err = s.proposeAndWait(timeoutCtx, []replica.Log{
// 		{
// 			Id:   uint64(s.cfgGenId.Generate().Int64()),
// 			Data: data,
// 		},
// 	})
// 	if err != nil {
// 		s.Error("proposeUpdateApiServerAddr failed", zap.Error(err))
// 		return err
// 	}
// 	return nil
// }

// func (s *Server) ProposeConfig(ctx context.Context, cfg *pb.Config) error {

// 	s.proposeLock.Lock()
// 	defer s.proposeLock.Unlock()

// 	data, err := cfg.Marshal()
// 	if err != nil {
// 		s.Error("ProposeConfig failed", zap.Error(err))
// 		return err
// 	}

// 	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, time.Second*5)
// 	defer cancel()
// 	err = s.proposeAndWait(timeoutCtx, []replica.Log{
// 		{
// 			Id:   uint64(s.cfgGenId.Generate().Int64()),
// 			Data: data,
// 		},
// 	})
// 	if err != nil {
// 		s.Error("ProposeConfig failed", zap.Error(err))
// 		return err
// 	}
// 	return nil
// }

// func (s *Server) ProposeJoin(ctx context.Context, node *pb.Node) error {

// 	if s.cfg.hasWillJoinNode() || s.cfg.hasJoiningNode() {
// 		s.Error("ProposeJoin failed, there is a joining node")
// 		return fmt.Errorf("there is a joining node")

// 	}

// 	newCfg := s.cfg.cfg.Clone()

// 	exist := false
// 	for _, n := range newCfg.Nodes {
// 		if n.Id == node.Id {
// 			exist = true
// 			break
// 		}
// 	}
// 	if !exist {
// 		newCfg.Nodes = append(newCfg.Nodes, node)
// 	}

// 	data, err := newCfg.Marshal()
// 	if err != nil {
// 		s.Error("ProposeJoin failed", zap.Error(err))
// 		return err
// 	}

// 	err = s.proposeAndWait(ctx, []replica.Log{
// 		{
// 			Id:   uint64(s.cfgGenId.Generate().Int64()),
// 			Data: data,
// 		},
// 	})
// 	if err != nil {
// 		s.Error("ProposeJoin failed", zap.Error(err))
// 		return err
// 	}
// 	return nil
// }

func (s *Server) proposeAndWait(logs []replica.Log) error {
	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ProposeTimeout)
	defer cancel()

	if !s.IsLeader() { // 如果不是领导，则向领导请求提按
		return s.requestPropose(timeoutCtx, s.LeaderId(), logs)
	}

	_, err := s.configReactor.ProposeAndWait(timeoutCtx, s.handlerKey, logs)
	return err
}

func (s *Server) requestPropose(ctx context.Context, nodeId uint64, logs []replica.Log) error {

	logset := replica.LogSet(logs)
	data, err := logset.Marshal()
	if err != nil {
		return err
	}
	resp, err := s.opts.Cluster.RequestWithContext(ctx, nodeId, "/clusterconfig/propose", data)
	if err != nil {
		s.Error("request propose error", zap.Error(err))
		return err
	}

	if resp.Status != proto.StatusOK {
		return fmt.Errorf("propose failed, status: %d", resp.Status)
	}

	return err
}

func (s *Server) send(m reactor.Message) {
	s.opts.Send(m)
}

// func (s *Server) checkClusterConfig() {
// 	if !s.handler.isLeader() {
// 		return
// 	}
// 	hasUpdate := false
// 	if len(s.cfg.nodes()) < len(s.initNodes) {
// 		for replicaId, addr := range s.initNodes {
// 			if !s.cfg.hasNode(replicaId) {
// 				s.cfg.addNode(&pb.Node{
// 					Id:          replicaId,
// 					AllowVote:   true,
// 					ClusterAddr: addr,
// 					Online:      true,
// 					CreatedAt:   time.Now().Unix(),
// 					Status:      pb.NodeStatus_NodeStatusJoined,
// 				})
// 				hasUpdate = true
// 			}
// 		}
// 	}

// 	if len(s.cfg.slots()) == 0 {

// 		replicas := make([]uint64, 0, len(s.cfg.nodes())) // 有效副本集合
// 		for _, node := range s.cfg.nodes() {
// 			if !node.AllowVote || node.Status != pb.NodeStatus_NodeStatusJoined {
// 				continue
// 			}
// 			replicas = append(replicas, node.Id)
// 		}
// 		if len(replicas) > 0 {
// 			offset := 0
// 			replicaCount := s.opts.SlotMaxReplicaCount
// 			for i := uint32(0); i < s.opts.SlotCount; i++ {
// 				slot := &pb.Slot{
// 					Id: i,
// 				}
// 				if len(replicas) <= int(replicaCount) {
// 					slot.Replicas = replicas
// 				} else {
// 					slot.Replicas = make([]uint64, 0, replicaCount)
// 					for i := uint32(0); i < replicaCount; i++ {
// 						idx := (offset + int(i)) % len(replicas)
// 						slot.Replicas = append(slot.Replicas, replicas[idx])
// 					}
// 				}
// 				offset++
// 				// 随机选举一个领导者
// 				randomIndex := globalRand.Intn(len(slot.Replicas))
// 				slot.Term = 1
// 				slot.Leader = slot.Replicas[randomIndex]
// 				s.cfg.addSlot(slot)
// 				hasUpdate = true
// 			}
// 		}
// 	}

// 	if hasUpdate {
// 		data, err := s.cfg.data()
// 		if err != nil {
// 			s.Error("get data error", zap.Error(err))
// 			return
// 		}
// 		err = s.proposeAndWait(s.cancelCtx, []replica.Log{
// 			{
// 				Id:   uint64(s.cfgGenId.Generate().Int64()),
// 				Data: data,
// 			},
// 		})
// 		if err != nil {
// 			s.Error("propose error", zap.Error(err))
// 		}
// 	}
// }

// 是否需要加入集群
func (s *Server) needJoin() bool {
	if strings.TrimSpace(s.opts.Seed) == "" {
		return false
	}
	seedNodeId, _, _ := seedNode(s.opts.Seed) // New里已经验证过seed了  所以这里不必再处理error了
	seedNode := s.Node(seedNodeId)
	return seedNode == nil
}

func seedNode(seed string) (uint64, string, error) {
	seedArray := strings.Split(seed, "@")
	if len(seedArray) < 2 {
		return 0, "", errors.New("seed format error")
	}
	seedNodeIDStr := seedArray[0]
	seedAddr := seedArray[1]
	seedNodeID, err := strconv.ParseUint(seedNodeIDStr, 10, 64)
	if err != nil {
		return 0, "", err
	}
	return seedNodeID, seedAddr, nil
}

func (s *Server) GetLogsInReverseOrder(startLogIndex uint64, endLogIndex uint64, limit int) ([]replica.Log, error) {
	return s.storage.GetLogsInReverseOrder(startLogIndex, endLogIndex, limit)
}

func (s *Server) AppliedLogIndex() (uint64, error) {
	return s.storage.AppliedIndex()
}

func (s *Server) LastLogIndex() (uint64, error) {
	return s.storage.LastIndex()
}
