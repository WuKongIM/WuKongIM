package event

import (
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/clusterconfig"
	"github.com/WuKongIM/WuKongIM/pkg/raft/types"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type Server struct {
	handler    *handler
	stopper    *syncutil.Stopper
	advanceC   chan struct{}
	cfgServer  *clusterconfig.Server
	cfgOptions *clusterconfig.Options
	wklog.Log
	pongC chan uint64

	pong struct {
		sync.RWMutex
		tickMap map[uint64]int
	}
	event IEvent
}

func NewServer(event IEvent, cfgOptions *clusterconfig.Options, cfgServer *clusterconfig.Server) *Server {
	s := &Server{
		stopper:    syncutil.NewStopper(),
		advanceC:   make(chan struct{}, 1),
		cfgServer:  cfgServer,
		cfgOptions: cfgOptions,
		Log:        wklog.NewWKLog("event"),
		pongC:      make(chan uint64, 1),
		event:      event,
	}
	s.pong.tickMap = make(map[uint64]int)
	s.handler = newHandler(s, cfgOptions)
	return s
}

func (s *Server) Start() error {
	s.stopper.RunWorker(s.loopEvent)
	return nil
}

func (s *Server) Stop() {
	s.stopper.Stop()
}

func (s *Server) Step(event types.Event) {
	s.cfgServer.StepRaftEvent(event)

	if s.cfgServer.IsLeader() && event.Type == types.SyncReq {
		s.pong.Lock()
		s.pong.tickMap[event.From] = 0
		s.pong.Unlock()
	}

}

func (s *Server) loopEvent() {
	tk := time.NewTicker(time.Millisecond * 200)
	defer tk.Stop()
	for {
		s.handleEvents()
		select {
		case <-tk.C:
			s.tick()
		case <-s.advanceC:
		case <-s.stopper.ShouldStop():
			return
		}
	}
}

func (s *Server) handleEvents() {

	// 如果没有领导节点，则不处理事件
	if s.cfgServer.LeaderId() == 0 {
		return
	}

	if s.cfgServer.IsLeader() {

		// 分布式初始化
		if !s.cfgServer.IsInitialized() {
			s.handler.handleClusterInit()
			return
		}
	}
	// 比较新旧配置
	s.handler.handleCompare()

	if s.cfgServer.IsLeader() {

		// 处理节点加入
		err := s.handleNodeJoining()
		if err != nil {
			s.Error("handleJoinNode failed", zap.Error(err))
			return
		}

		// 检查和均衡槽领导
		err = s.handleSlotLeaderAutoBalance()
		if err != nil {
			s.Error("handleSlotLeaderAutoBalance failed", zap.Error(err))
			return
		}
		// 处理槽选举
		s.handler.handleSlotLeaderElection()
	}

}

func (s *Server) tick() {
	if s.cfgServer.IsLeader() {
		for _, node := range s.cfgServer.Nodes() {
			if node.Id == s.cfgOptions.NodeId {
				continue
			}

			s.pong.Lock()
			tk := s.pong.tickMap[node.Id]
			tk++
			s.pong.tickMap[node.Id] = tk
			s.pong.Unlock()

			if tk >= s.cfgOptions.PongMaxTick && node.Online { // 超过最大pong tick数，认为节点离线
				// 节点离线
				s.handler.handleOnlineStatus(node.Id, false)
			} else if tk < s.cfgOptions.PongMaxTick && !node.Online {
				// 节点在线
				s.handler.handleOnlineStatus(node.Id, true)
			}

		}
	}

}
