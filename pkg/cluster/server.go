package cluster

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/gossip"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/atomic"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Server struct {
	gossipServer  *gossip.Server // gossip协议服务，主要用于发现新节点
	opts          *Options
	nodeManager   *nodeManager     // 节点管理
	clusterServer *wkserver.Server // 集群通讯服务
	stopper       syncutil.Stopper
	role          ServerRole    // 当前server的角色
	currentEpoch  atomic.Uint32 // 当前选举周期
	wklog.Log
	tickCount    atomic.Uint32 // 心跳计数
	electionLock sync.RWMutex

	leaderID atomic.Uint64 // leader节点id

	voteTo    map[uint32]uint64   // 投票情况 key为epoch value为投票给谁的节点
	votesFrom map[uint32][]uint64 // 投票情况 key为epoch value为投票的节点
}

func NewServer(nodeID uint64, opts ...Option) *Server {
	defaultOpts := NewOptions()
	defaultOpts.NodeID = nodeID
	for _, opt := range opts {
		opt(defaultOpts)
	}
	ip, port := splitAddr(defaultOpts.ListenAddr)
	defaultOpts.clusterAddr = ip + ":" + strconv.Itoa(port+defaultOpts.offsetPort)

	s := &Server{
		opts:          defaultOpts,
		nodeManager:   newNodeManager(),
		clusterServer: wkserver.New(fmt.Sprintf("tcp://%s", defaultOpts.clusterAddr)),
		stopper:       *syncutil.NewStopper(),
		role:          ServerRoleSlave,
		voteTo:        make(map[uint32]uint64),
		votesFrom:     make(map[uint32][]uint64),
		Log:           wklog.NewWKLog(fmt.Sprintf("cluster.Server[%d]", defaultOpts.NodeID)),
	}
	wklog.Configure(&wklog.Options{
		Level: zapcore.Level(defaultOpts.LogLevel),
	})

	if len(defaultOpts.Seed) == 0 { // 如果没有种子节点 说明是单节点，则直接设置自己为master
		s.role = ServerRoleMaster
		s.leaderID.Store(nodeID)
	}

	s.gossipServer = gossip.NewServer(nodeID, defaultOpts.ListenAddr, gossip.WithSeed(defaultOpts.Seed), gossip.WithEpoch(1), gossip.WithOnNodeEvent(func(event gossip.NodeEvent) {
		ip, port := splitAddr(event.Addr)

		fmt.Println("nodeEvent...")
		switch event.EventType {
		case gossip.NodeEventJoin:
			s.addNode(event.NodeID, ip+":"+strconv.Itoa(port+defaultOpts.offsetPort))
		case gossip.NodeEventLeave:
			// s.removeNode(event.NodeID)

		}
	}))
	return s
}

func (s *Server) Start() error {

	s.stopper.RunWorker(s.loopTick)

	err := s.gossipServer.Start()
	if err != nil {
		return err
	}

	s.clusterServer.OnMessage(func(conn wknet.Conn, msg *proto.Message) {
		switch msg.MsgType {
		case MessageTypePing.Uint32():
			fromNodeID, _ := strconv.ParseUint(conn.UID(), 10, 64)
			s.handlePingRequest(fromNodeID, msg)
		case MessageTypeVoteRequest.Uint32():
			fromNodeID, _ := strconv.ParseUint(conn.UID(), 10, 64)
			s.handleVoteRequest(fromNodeID, msg)
		case MessageTypeVoteResponse.Uint32():
			fromNodeID, _ := strconv.ParseUint(conn.UID(), 10, 64)
			s.handleVoteResponse(fromNodeID, msg)
		}
	})
	err = s.clusterServer.Start()
	if err != nil {
		return err
	}

	return nil
}

func (s *Server) Stop() {

	s.stopper.Stop()

	for _, node := range s.nodeManager.getAllNode() {
		node.stop()
	}
	s.gossipServer.Stop()
	s.clusterServer.Stop()
}

func (s *Server) MustWaitLeader(timeout time.Duration) {
	err := s.WaitLeader(timeout)
	if err != nil {
		s.Panic("WaitLeader failed!", zap.Error(err))
	}
}

func (s *Server) WaitLeader(timeout time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	tick := time.NewTicker(time.Millisecond * 200)

	for {
		select {
		case <-timeoutCtx.Done():
			return timeoutCtx.Err()
		case <-tick.C:
			if s.leaderID.Load() != 0 {
				return nil
			}
		case <-s.stopper.ShouldStop():
			return errors.New("server stopped")
		}
	}
}

func (s *Server) addNode(id uint64, addr string) {
	if s.opts.NodeID == id {
		return
	}
	node := newNode(id, fmt.Sprintf("%d", s.opts.NodeID), addr)
	node.start()
	s.nodeManager.addNode(node)
	s.Info("add node", zap.Uint64("newID", id), zap.String("addr", addr))

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
	case ServerRoleSlave:
		s.stepSlave()
	case ServerRoleMaster:
		s.stepMaster()
	}
}

func (s *Server) stepSlave() {
	if s.tickCount.Load() > s.getRandElectionTick() { // 超时，开始选举
		s.stepElection() // 选举
		return
	}
	s.tickCount.Inc()
}

func (s *Server) stepMaster() {
	s.sendPingToAll()
}

func (s *Server) sendPingToAll() {
	for _, node := range s.nodeManager.getAllNode() {
		s.stopper.RunWorker(func() {
			s.Info("发送Ping", zap.Uint64("toNodeID", node.id))
			err := node.sendPing(&PingRequest{
				Epoch: s.currentEpoch.Load(),
			})
			if err != nil {
				s.Warn("Send PingRequest cmd failed!", zap.Uint64("toNodeID", node.id), zap.Error(err))
			}
		})
	}
}

func (s *Server) sendVoteRequestToAll() {
	for _, node := range s.nodeManager.getAllNode() {
		s.stopper.RunWorker(func() {
			err := node.sendVote(&VoteRequest{
				Epoch: s.currentEpoch.Load(),
			})
			if err != nil {
				s.Warn("Send VoteRequest cmd failed!", zap.Uint64("toNodeID", node.id), zap.Error(err))
			}
		})
	}
}

// 选举
func (s *Server) stepElection() {

	s.electionLock.Lock()
	defer s.electionLock.Unlock()

	s.Debug("开始选举...")

	s.currentEpoch.Inc() // 选举周期递增
	_, ok := s.voteTo[s.currentEpoch.Load()]
	if ok { // 如果已投票，说明此周期已经选举过了
		return
	}

	s.voteTo[s.currentEpoch.Load()] = s.opts.NodeID // 投票给自己

	s.sendVoteRequestToAll() // 发送投票请求
}

func (s *Server) handlePingRequest(fromNodeID uint64, msg *proto.Message) {
	s.Debug("收到PingRequest", zap.Uint64("fromNodeID", fromNodeID))
	req := &PingRequest{}
	err := req.Unmarshal(msg.Content)
	if err != nil {
		s.Error("Unmarshal PingRequest failed!", zap.Error(err))
	}
	if req.Epoch < s.currentEpoch.Load() {
		s.Debug("epoch is old, ignore!", zap.Uint32("epoch", req.Epoch))
		return
	}
	s.tickCount.Store(0)
	if s.role == ServerRoleMaster {
		if req.Epoch > s.currentEpoch.Load() { // epoch 大的为主节点
			s.changeToSlave(req.Epoch)
		} else if req.Epoch == s.currentEpoch.Load() && fromNodeID > s.opts.NodeID { // 如果epoch相同，节点ID小的为主节点
			s.changeToSlave(req.Epoch)
		}
	}
	if s.leaderID.Load() != fromNodeID {
		if s.opts.OnLeaderChange != nil {
			s.opts.OnLeaderChange(fromNodeID)
		}
		s.leaderID.Store(fromNodeID)
	}
}

func (s *Server) handleVoteRequest(fromNodeID uint64, msg *proto.Message) {
	req := &VoteRequest{}
	err := req.Unmarshal(msg.Content)
	if err != nil {
		s.Error("Unmarshal VoteRequest failed!", zap.Error(err))
	}

	// -------------------- 判断是否存在master --------------------
	// 如果master没有失联，忽略该请求，如果过半的节点master都没有失联，说明这个选举无效，
	// 投票节点要么等待存在的master的心跳让其变成slave
	// 要么等待选举超时重新发起投票，往次循环
	if !s.masterIsLost() {
		return
	}
	// -------------------- 判断当前周期是否已投票 --------------------
	// 判断请求投票的周期内当前节点是否已投票，如果没有投票则执行投票逻辑
	// 如果已经投票则忽略此次投票
	s.electionLock.RLock()
	_, ok := s.voteTo[req.Epoch]
	s.electionLock.RUnlock()
	if !ok { // 还没有投票
		voteResp := &VoteRespose{}
		voteResp.Epoch = req.Epoch

		s.electionLock.Lock()
		s.voteTo[req.Epoch] = fromNodeID
		s.electionLock.Unlock()
	}
}

func (s *Server) handleVoteResponse(fromNodeID uint64, msg *proto.Message) {
	resp := &VoteRespose{}
	err := resp.Unmarshal(msg.Content)
	if err != nil {
		s.Error("Unmarshal VoteRespose failed!", zap.Error(err))
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
	if voteCount > len(s.nodeManager.getAllNode())/2 {
		s.changeToMaster()
	}
}

func (s *Server) changeToSlave(epoch uint32) {
	s.electionLock.Lock()
	defer s.electionLock.Unlock()
	if s.role == ServerRoleSlave {
		return
	}
	s.role = ServerRoleSlave
	s.currentEpoch.Store(epoch)
}

func (s *Server) changeToMaster() {
	s.electionLock.Lock()
	defer s.electionLock.Unlock()

	if s.role == ServerRoleMaster {
		return
	}
	s.role = ServerRoleMaster
	if s.opts.OnLeaderChange != nil {
		s.opts.OnLeaderChange(s.opts.NodeID)
	}
	s.leaderID.Store(s.opts.NodeID)
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
