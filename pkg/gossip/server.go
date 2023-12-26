package gossip

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/hashicorp/memberlist"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type Server struct {
	gossipServer *memberlist.Memberlist
	wklog.Log
	opts    *Options
	stopper *syncutil.Stopper

	nodeMeta     *nodeMeta  // 节点元数据
	role         ServerRole // 当前节点角色
	leaderID     atomic.Uint64
	currentEpoch atomic.Uint32

	electionLock sync.RWMutex
	voteTo       map[uint32]uint64   // 投票情况 key为epoch value为投票给谁的节点
	votesFrom    map[uint32][]uint64 // 投票情况 key为epoch value为投票的节点

	tickCount atomic.Uint32 // 心跳计数
}

func NewServer(nodeID uint64, listenAddr string, opts ...Option) *Server {
	lg := wklog.NewWKLog("gossip.Server")

	defaultOpts := NewOptions()
	defaultOpts.NodeID = nodeID
	defaultOpts.ListenAddr = listenAddr
	for _, opt := range opts {
		opt(defaultOpts)
	}

	s := &Server{
		Log:     lg,
		opts:    defaultOpts,
		stopper: syncutil.NewStopper(),
		nodeMeta: &nodeMeta{
			role:         ServerRoleSlave,
			currentEpoch: defaultOpts.Epoch,
		},
		role:      ServerRoleSlave,
		voteTo:    make(map[uint32]uint64),
		votesFrom: make(map[uint32][]uint64),
	}

	s.gossipServer = createMemberlist(s) // 创建gossip服务

	return s
}

func (s *Server) Start() error {

	// 加入存在的节点
	s.stopper.RunWorker(func() {
		s.loopJoin()
	})

	return nil
}

func (s *Server) Stop() {
	s.stopper.Stop()
	err := s.gossipServer.Shutdown()
	if err != nil {
		s.Warn("Shutdown gossip server failed!", zap.Error(err))
	}
}

func (s *Server) WaitJoin(timeout time.Duration) error {
	if len(s.opts.Seed) == 0 {
		return nil
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	tick := time.NewTicker(time.Millisecond * 200)

	for {
		select {
		case <-timeoutCtx.Done():
			return timeoutCtx.Err()
		case <-tick.C:
			if s.getNotJoinNum() == 0 {
				return nil
			}
		case <-s.stopper.ShouldStop():
			return errors.New("server stopped")
		}
	}

}

func (s *Server) MustWaitJoin(timeout time.Duration) {
	err := s.WaitJoin(timeout)
	if err != nil {
		s.Panic("WaitJoin failed!", zap.Error(err))
	}
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

func (s *Server) SendCMD(toNodeID uint64, cmd *CMD) error {
	toNode := s.getNode(toNodeID)
	if toNode == nil {
		return errors.New("node not found")
	}
	data, err := cmd.Marshal()
	if err != nil {
		return err
	}
	return s.gossipServer.SendReliable(toNode, data)
}

func (s *Server) SendCMDToUDP(toNodeID uint64, cmd *CMD) error {
	toNode := s.getNode(toNodeID)
	if toNode == nil {
		return errors.New("node not found")
	}
	data, err := cmd.Marshal()
	if err != nil {
		return err
	}
	return s.gossipServer.SendBestEffort(toNode, data)
}

func (s *Server) UpdateRole(role ServerRole, timeout time.Duration) error {
	s.nodeMeta.role = role
	return s.gossipServer.UpdateNode(timeout)
}

func (s *Server) UpdateNodeMeta() error {
	return s.gossipServer.UpdateNode(time.Second * 10)
}

func (s *Server) loopJoin() {

	needJoins := s.getNeedJoin()
	if len(needJoins) > 0 {
		s.join(needJoins)
	}

	tick := time.NewTicker(time.Second * 5)
	for {
		select {
		case <-tick.C:
			needJoins := s.getNeedJoin()
			if len(needJoins) > 0 {
				s.join(needJoins)
			} else {
				return
			}
		case <-s.stopper.ShouldStop():
			return
		}
	}
}

func (s *Server) join(nodes []string) {
	if len(nodes) > 0 {
		fmt.Println("节点数量--->", len(nodes))
		_, err := s.gossipServer.Join(nodes)
		if err != nil {
			s.Error("Join gossip server failed!", zap.Error(err))
		}
	}
}

func createMemberlist(s *Server) *memberlist.Memberlist {
	memberlistCfg := memberlist.DefaultLocalConfig()
	memberlistCfg.Delegate = s
	memberlistCfg.Events = s
	memberlistCfg.Name = strconv.FormatUint(s.opts.NodeID, 10)
	// memberlistCfg.RequireNodeNames = true
	memberlistCfg.BindAddr, memberlistCfg.BindPort = splitAddr(s.opts.ListenAddr)
	if s.opts.AdvertiseAddr != "" {
		memberlistCfg.AdvertiseAddr, memberlistCfg.AdvertisePort = splitAddr(s.opts.AdvertiseAddr)
	} else {
		memberlistCfg.AdvertisePort = memberlistCfg.BindPort
	}
	gossipServer, err := memberlist.Create(memberlistCfg)
	if err != nil {
		s.Panic("Create gossip server failed!", zap.Error(err))
		return nil
	}
	return gossipServer
}

func (s *Server) getNode(nodeID uint64) *memberlist.Node {
	for _, member := range s.gossipServer.Members() {
		if member.Name == strconv.FormatUint(nodeID, 10) {
			return member
		}
	}
	return nil
}

func (s *Server) allNodes() []*memberlist.Node {
	return s.gossipServer.Members()
}

// 存活的节点
func (s *Server) aliveNodes() []*memberlist.Node {
	nodes := make([]*memberlist.Node, 0)
	for _, node := range s.gossipServer.Members() {
		if node.State == memberlist.StateAlive {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func (s *Server) getNeedJoin() []string {
	needJoins := make([]string, 0)
	if len(s.opts.Seed) > 0 {
		for _, seed := range s.opts.Seed {
			joined := false
			for _, member := range s.gossipServer.Members() {
				if member.Address() == seed {
					joined = true
					break
				}
			}
			if !joined {
				needJoins = append(needJoins, seed)
			}
		}
	}
	return needJoins
}

// 未加入节点数量
func (s *Server) getNotJoinNum() int {
	needJoins := s.getNeedJoin()
	return len(needJoins)
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
