package cluster

import (
	"context"
	"fmt"
	"math/rand"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/clusterevent"
	"github.com/WuKongIM/WuKongIM/pkg/clusterevent/pb"
	"github.com/WuKongIM/WuKongIM/pkg/gossip"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/lni/goutils/syncutil"
	"go.etcd.io/etcd/pkg/v3/idutil"
	"go.uber.org/atomic"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Server struct {
	gossipServer  *gossip.Server   // gossip协议服务，主要用于发现新节点
	opts          *Options         // 服务配置
	nodeManager   *nodeManager     // 节点管理
	clusterServer *wkserver.Server // 集群通讯服务
	stopper       syncutil.Stopper
	role          ServerRole    // 当前server的角色
	currentEpoch  atomic.Uint32 // 当前选举周期
	wklog.Log
	tickCount    atomic.Uint32 // 心跳计数
	electionLock sync.RWMutex

	leaderID                   atomic.Uint64 // leader节点id
	leaderClusterConfigVersion atomic.Uint32 // 领导者集群配置的版本号

	voteTo    map[uint32]uint64   // 投票情况 key为epoch value为投票给谁的节点
	votesFrom map[uint32][]uint64 // 投票情况 key为epoch value为投票的节点

	clusterEventManager *clusterevent.ClusterEventManager // 分布式事件管理者（触发器）

	slotManager    *SlotManager      // slot管理
	channelManager *ChannelManager   // channel管理
	reqIDGen       *idutil.Generator // 请求id生成

	cancelFnc context.CancelFunc
	cancelCtx context.Context

	stateMachine *stateMachine // 状态机
}

func NewServer(nodeID uint64, optList ...Option) *Server {
	opts := NewOptions()
	opts.NodeID = nodeID
	for _, opt := range optList {
		opt(opts)
	}
	ip, port := splitAddr(opts.ListenAddr)
	opts.clusterAddr = ip + ":" + strconv.Itoa(port+opts.offsetPort)
	s := &Server{
		opts:          opts,
		nodeManager:   newNodeManager(),
		clusterServer: wkserver.New(fmt.Sprintf("tcp://%s", opts.clusterAddr)),
		stopper:       *syncutil.NewStopper(),
		role:          ServerRoleUnknown,
		voteTo:        make(map[uint32]uint64),
		votesFrom:     make(map[uint32][]uint64),
		Log:           wklog.NewWKLog(fmt.Sprintf("cluster.Server[%d]", opts.NodeID)),
		reqIDGen:      idutil.NewGenerator(uint16(nodeID), time.Now()),
		stateMachine:  newStateMachine(path.Join(opts.DataDir, "datadb")),
	}
	s.slotManager = NewSlotManager(s)
	s.cancelCtx, s.cancelFnc = context.WithCancel(context.Background())
	wklog.Configure(&wklog.Options{
		Level: zapcore.Level(opts.LogLevel),
	})

	if len(opts.InitNodes) == 0 {
		s.Panic("init nodes is empty")
	}

	seeds := make([]string, 0)
	if strings.TrimSpace(opts.Join) != "" {
		seeds = append(seeds, opts.Join)

	}
	s.gossipServer = gossip.NewServer(nodeID, opts.ListenAddr, gossip.WithSeed(seeds), gossip.WithOnNodeEvent(func(event gossip.NodeEvent) {
		ip, port := splitAddr(event.Addr)
		switch event.EventType {
		case gossip.NodeEventJoin:
			s.addNode(event.NodeID, ip+":"+strconv.Itoa(port+opts.offsetPort))
		case gossip.NodeEventLeave:
			// s.removeNode(event.NodeID)

		}
	}))

	clusterEventOpts := clusterevent.NewOptions()
	clusterEventOpts.DataDir = opts.DataDir
	clusterEventOpts.InitNodes = opts.InitNodes
	clusterEventOpts.NodeID = opts.NodeID
	clusterEventOpts.SlotCount = opts.SlotCount
	clusterEventOpts.SlotReplicaCount = opts.SlotReplicaCount
	clusterEventOpts.Heartbeat = opts.Heartbeat
	s.clusterEventManager = clusterevent.NewClusterEventManager(clusterEventOpts)

	if len(s.clusterEventManager.GetSlots()) > 0 {
		for _, st := range s.clusterEventManager.GetSlots() {
			slot, err := s.newSlot(st)
			if err != nil {
				s.Panic("slot init failed", zap.Error(err))
			}
			s.slotManager.AddSlot(slot)
		}
		s.clusterEventManager.SetSlotIsInit(true)
	}
	s.channelManager = NewChannelManager(s)

	_, hasSelf := opts.InitNodes[opts.NodeID]                                        // 是否包含自己
	if strings.TrimSpace(opts.Join) == "" && (len(opts.InitNodes) == 1 && hasSelf) { // 如果没有加入节点并且也没有初始化节点 说明是单节点，则直接设置自己为master
		s.becomeLeader()
	} else {
		s.becomeFollow(s.currentEpoch.Load())
	}

	if len(opts.InitNodes) > 0 { // 如果有初始化节点，则加入初始化节点
		for nodeID, addr := range opts.InitNodes {
			ip, port := splitAddr(addr)
			s.addNode(nodeID, ip+":"+strconv.Itoa(port+opts.offsetPort))
		}
	}

	return s
}

func (s *Server) Start() error {

	s.stopper.RunWorker(s.loopTick)
	s.stopper.RunWorker(s.loopClusterEvent)

	err := s.stateMachine.open()
	if err != nil {
		return err
	}

	err = s.slotManager.Start()
	if err != nil {
		return err
	}

	err = s.channelManager.Start()
	if err != nil {
		return err
	}

	err = s.gossipServer.Start() // 开始gossip协议服务
	if err != nil {
		return err
	}

	s.clusterServer.OnMessage(func(conn wknet.Conn, msg *proto.Message) {
		fromNodeID, _ := strconv.ParseUint(conn.UID(), 10, 64)
		switch msg.MsgType {
		case MessageTypePing.Uint32(): // 领导发送ping
			s.handlePingRequest(fromNodeID, msg)
		case MessageTypePong.Uint32(): // 从节点回应pong
			s.handlePongResponse(fromNodeID, msg)
		case MessageTypeVoteRequest.Uint32(): // 发送投票请求
			s.handleVoteRequest(fromNodeID, msg)
		case MessageTypeVoteResponse.Uint32(): // 投票结果返回
			s.handleVoteResponse(fromNodeID, msg)
		case MessageTypeSlotLogSyncNotify.Uint32(): // slot日志同步通知
			s.handleSlotLogSyncNotify(fromNodeID, msg)
		case MessageTypeChannelMetaLogSyncNotify.Uint32(): // 频道元数据日志同步请求
			s.handleChannelMetaLogSyncNotify(fromNodeID, msg)
		case MessageTypeChannelMessageLogSyncNotify.Uint32(): // 频道消息日志同步请求
			s.handleChannelMessageLogSyncNotify(fromNodeID, msg)

		}
	})

	s.setRoutes()

	err = s.clusterServer.Start()
	if err != nil {
		return err
	}

	err = s.clusterEventManager.Start()
	if err != nil {
		return err
	}

	fmt.Println("start---success....")

	return nil
}

func (s *Server) Stop() {

	s.cancelFnc()

	s.stopper.Stop()

	s.clusterEventManager.Stop()

	s.gossipServer.Stop()
	s.clusterServer.Stop()

	for _, node := range s.nodeManager.getAllNode() {
		node.stop()
	}
	s.slotManager.Stop()

	s.channelManager.Stop()

	s.stateMachine.close()

}

func (s *Server) Options() *Options {
	return s.opts
}

func (s *Server) GetSlotLogs(slotID uint32, startLogIndex uint64, limit uint32) ([]replica.Log, error) {
	slot := s.slotManager.GetSlot(slotID)
	if slot == nil {
		return nil, fmt.Errorf("GetSlotLogs: slot[%d] not found", slotID)
	}
	return slot.GetLogs(startLogIndex, limit)
}

func (s *Server) GetChannelLogs(channelID string, channelType uint8, startLogIndex uint64, limit uint32) ([]replica.Log, error) {
	channel, err := s.channelManager.GetChannel(channelID, channelType)
	if err != nil {
		return nil, err
	}
	if channel == nil {
		return nil, fmt.Errorf("channel[%s:%d] not found", channelID, channelType)
	}
	return channel.GetLogs(startLogIndex, limit)
}

// 模拟节点在线状态
func (s *Server) FakeSetNodeOnline(nodeID uint64, online bool) {
	node := s.nodeManager.getNode(nodeID)
	if node == nil {
		return
	}
	node.online = online
	s.clusterEventManager.SetNodeOnline(nodeID, online)
}

func (s *Server) addNode(id uint64, addr string) {
	if s.opts.NodeID == id {
		return
	}
	node := newNode(id, fmt.Sprintf("%d", s.opts.NodeID), addr)
	node.start()
	node.allowVote = s.allowVote(node)
	node.online = true

	s.nodeManager.addNode(node)
	s.Info("add node", zap.Uint64("newID", id), zap.String("addr", addr))

}

func (s *Server) allowVote(n *node) bool {
	_, ok := s.opts.InitNodes[n.id]
	return ok
}

func (s *Server) removeNode(id uint64) {
	node := s.nodeManager.getNode(id)
	if node == nil {
		return
	}
	s.nodeManager.removeNode(id)
	node.stop()
}

func (s *Server) loopTick() {
	tick := time.NewTicker(s.opts.Heartbeat)
	for {
		select {
		case <-tick.C:
			s.tick()
		case <-s.stopper.ShouldStop():
			return
		}
	}
}

func (s *Server) tick() {
	switch s.role {
	case ServerRoleFollow:
		s.stepSlave()
	case ServerRoleLeader:
		s.stepMaster()
	}
}

func (s *Server) stepSlave() {
	if s.tickCount.Load() > s.getRandElectionTick() { // 超时，开始选举
		s.stepElection() // 选举
		return
	}
	s.tickCount.Inc()

	if s.leaderID.Load() != 0 {
		if s.leaderClusterConfigVersion.Load() > s.clusterEventManager.GetClusterConfigVersion() { // 领导者的版本要大于当前节点，则此节点应该去同步最新的配置
			s.Debug("节点集群配置太旧，去同步领导的最新配置。。。")
			clusterConfig, err := s.nodeManager.requestClusterConfig(s.cancelCtx, s.leaderID.Load())
			if err != nil {
				s.Error("request cluster config failed", zap.Error(err))
				return
			}
			s.clusterEventManager.UpdateClusterConfig(clusterConfig)
		}
	}

}

func (s *Server) stepMaster() {
	s.sendPingToAll()
}

func (s *Server) sendPingToAll() {
	s.clusterEventManager.SetNodeConfigVersion(s.opts.NodeID, s.clusterEventManager.GetClusterConfigVersion())
	for _, n := range s.nodeManager.getAllNode() {
		s.stopper.RunWorker(func(nodeID uint64) func() {

			return func() {
				s.Info("发送Ping", zap.Uint64("toNodeID", nodeID))
				err := s.nodeManager.sendPing(nodeID, &PingRequest{
					Epoch:                s.currentEpoch.Load(),
					ClusterConfigVersion: s.clusterEventManager.GetClusterConfigVersion(),
				})
				if err != nil {
					s.Warn("Send PingRequest cmd failed!", zap.Uint64("toNodeID", nodeID), zap.Error(err))
				}
			}
		}(n.id))
	}
}

func (s *Server) sendVoteRequestToAll() {
	for _, n := range s.nodeManager.getAllVoteNodes() {
		s.stopper.RunWorker(func(nodeID uint64) func() {
			return func() {
				err := s.nodeManager.sendVote(nodeID, &VoteRequest{
					Epoch:                s.currentEpoch.Load(),
					ClusterConfigVersion: s.clusterEventManager.GetClusterConfigVersion(),
				})
				if err != nil {
					s.Warn("Send VoteRequest cmd failed!", zap.Uint64("toNodeID", nodeID), zap.Error(err))
				}
			}
		}(n.id))
	}
}

// 选举
func (s *Server) stepElection() {

	s.electionLock.Lock()
	defer s.electionLock.Unlock()

	s.Debug("开始选举...", zap.Uint32("epoch", s.currentEpoch.Load()))

	s.currentEpoch.Inc() // 选举周期递增
	_, ok := s.voteTo[s.currentEpoch.Load()]
	if ok { // 如果已投票，说明此周期已经选举过了
		return
	}

	s.voteTo[s.currentEpoch.Load()] = s.opts.NodeID // 投票给自己

	s.sendVoteRequestToAll() // 发送投票请求
}

func (s *Server) handlePingRequest(fromNodeID uint64, msg *proto.Message) {
	req := &PingRequest{}
	err := req.Unmarshal(msg.Content)
	if err != nil {
		s.Error("Unmarshal PingRequest failed!", zap.Error(err))
	}

	if s.opts.NodeID == fromNodeID {
		s.Error("ping is self")
		return
	}
	s.leaderClusterConfigVersion.Store(req.ClusterConfigVersion)
	s.tickCount.Store(0)
	if s.role == ServerRoleLeader {
		if req.ClusterConfigVersion > s.clusterEventManager.GetClusterConfigVersion() { // 配置版本新的为主节点
			s.becomeFollow(req.Epoch)
		} else if req.ClusterConfigVersion == s.clusterEventManager.GetClusterConfigVersion() {
			if req.Epoch > s.currentEpoch.Load() { // 版本系统 周期大的成为领导节点
				s.becomeFollow(req.Epoch)
			} else if req.Epoch == s.currentEpoch.Load() && s.opts.NodeID > fromNodeID { // 版本相同 周期相同，nodeID小的成为主节点
				s.becomeFollow(req.Epoch)
			}
		}
	}
	if s.leaderID.Load() != fromNodeID {
		s.leaderChangeIfNeed(fromNodeID)
	}
	err = s.nodeManager.sendPong(fromNodeID, &PongResponse{
		ClusterConfigVersion: uint32(s.clusterEventManager.GetClusterConfig().Version),
	})
	if err != nil {
		s.Warn("send pong failed", zap.Error(err))
	}
}

func (s *Server) handlePongResponse(fromNodeID uint64, msg *proto.Message) {
	req := &PongResponse{}
	err := req.Unmarshal(msg.Content)
	if err != nil {
		s.Error("Unmarshal PingRequest failed!", zap.Error(err))
	}

	// s.Debug("收到Pong", zap.Uint32("clusterConfigVersion", req.ClusterConfigVersion), zap.Uint64("fromNodeID", fromNodeID))

	s.clusterEventManager.SetNodeConfigVersion(fromNodeID, req.ClusterConfigVersion)
}

func (s *Server) leaderChangeIfNeed(newLeaderID uint64) {
	if s.leaderID.Load() == newLeaderID {
		return
	}
	s.leaderChange(newLeaderID)
}

func (s *Server) leaderChange(newLeaderID uint64) {
	if s.opts.OnLeaderChange != nil {
		s.opts.OnLeaderChange(newLeaderID)
	}
	s.Debug("新的领导", zap.Uint64("newLeaderID", newLeaderID))
	s.leaderID.Store(newLeaderID)
	s.clusterEventManager.SetNodeLeaderID(newLeaderID)
}

func (s *Server) handleVoteRequest(fromNodeID uint64, msg *proto.Message) {
	req := &VoteRequest{}
	err := req.Unmarshal(msg.Content)
	if err != nil {
		s.Error("Unmarshal VoteRequest failed!", zap.Error(err))
		return
	}
	s.Debug("收到投票请求", zap.Uint64("fromNodeID", fromNodeID), zap.Uint32("epoch", req.Epoch))

	// -------------------- 判断是否存在master --------------------
	// 如果master没有失联，忽略该请求，如果过半的节点master都没有失联，说明这个选举无效，
	// 投票节点要么等待存在的master的心跳让其变成slave
	// 要么等待选举超时重新发起投票，往次循环
	if !s.masterIsLost() {
		s.Warn("master is not lost")
		return
	}
	// -------------------- 判断当前周期是否已投票 --------------------
	// 判断请求投票的周期内当前节点是否已投票，如果没有投票则执行投票逻辑
	// 如果已经投票则忽略此次投票
	s.electionLock.RLock()
	_, ok := s.voteTo[req.Epoch]
	s.electionLock.RUnlock()
	if !ok { // 还没有投票
		voteResp := &VoteResponse{}
		voteResp.Epoch = req.Epoch
		if req.ClusterConfigVersion < s.clusterEventManager.GetClusterConfigVersion() { // 如果参选人的配置版本还没当前节点的新，则拒绝选举
			s.Warn("current node config version  > vote request node", zap.Uint32("currentNodeConfigVersion", s.clusterEventManager.GetClusterConfigVersion()), zap.Uint32("requestNodeConfigVersion", req.ClusterConfigVersion))
			voteResp.Reject = true
			s.currentEpoch.Add(2) // 并且当前节点选举周期加2，为了防止周期内的选票其他节点已经投完了。
		} else {
			s.electionLock.Lock()
			s.voteTo[req.Epoch] = fromNodeID
			s.electionLock.Unlock()
		}

		err = s.nodeManager.sendVoteResp(fromNodeID, voteResp)
		if err != nil {
			s.Warn("send vote respo failed", zap.Error(err))
		}
	}
}

func (s *Server) handleVoteResponse(fromNodeID uint64, msg *proto.Message) {
	resp := &VoteResponse{}
	err := resp.Unmarshal(msg.Content)
	if err != nil {
		s.Error("Unmarshal VoteResponse failed!", zap.Error(err))
	}
	if resp.Reject {
		s.becomeFollow(resp.Epoch) // 选举被拒绝，成为追随者
		return
	}
	s.electionLock.Lock()
	nodeIDs := s.votesFrom[resp.Epoch]
	if nodeIDs == nil {
		nodeIDs = make([]uint64, 0)
	}

	existNode := false
	for _, nodeID := range nodeIDs {
		if nodeID == fromNodeID {
			existNode = true
			break
		}
	}
	if !existNode {
		nodeIDs = append(nodeIDs, fromNodeID)
		s.votesFrom[resp.Epoch] = nodeIDs
	}
	s.votesFrom[resp.Epoch] = append(s.votesFrom[resp.Epoch], fromNodeID)
	voteCount := len(s.votesFrom[resp.Epoch])

	s.electionLock.Unlock()
	if voteCount > len(s.nodeManager.getAllVoteNodes())/2 {
		s.becomeLeader()
	}
}

func (s *Server) becomeFollow(epoch uint32) {
	s.electionLock.Lock()
	defer s.electionLock.Unlock()

	if s.role == ServerRoleFollow {
		return
	}

	s.Debug("成为追随者", zap.Uint32("epoch", s.currentEpoch.Load()))

	s.role = ServerRoleFollow
	s.currentEpoch.Store(epoch)
}

func (s *Server) BecomeLeader() {
	s.becomeLeader()
}

func (s *Server) becomeLeader() {
	s.electionLock.Lock()
	defer s.electionLock.Unlock()

	if s.role == ServerRoleLeader {
		return
	}

	s.Debug("成为领导", zap.Uint32("epoch", s.currentEpoch.Load()))

	s.role = ServerRoleLeader
	s.leaderChangeIfNeed(s.opts.NodeID)
}

func (s *Server) getRandElectionTick() uint32 {
	randElectionTick := s.opts.ElectionTimeoutTick + rand.Uint32()%s.opts.ElectionTimeoutTick
	return randElectionTick
}

// master是否失联
// 认为只要 tickCount > ElectionTimeoutTick/2 则认为master可能失联了
func (s *Server) masterIsLost() bool {
	return s.tickCount.Load() > s.opts.ElectionTimeoutTick/2
}

// split listenAddr to ip and port
func splitAddr(listenAddr string) (string, int) {
	addrs := strings.Split(listenAddr, ":")
	if len(addrs) == 2 {
		port, _ := strconv.Atoi(addrs[1])
		return addrs[0], port
	}
	return "", 0
}

func (s *Server) newSlot(slot *pb.Slot) (*Slot, error) {

	return s.slotManager.NewSlot(slot)
}
