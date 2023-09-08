package clusterset

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type Server struct {
	s          *wkserver.Server
	clusterset *ClusterSet
	wklog.Log
	nodeManager  *nodeManager
	stopChan     chan struct{}
	doneChan     chan struct{}
	stopPingChan chan struct{}
	leaderCli    *client.Client

	bootstrapStatus bootstrapStatus
	opts            *options.Options

	leaderTimeoutTick *time.Ticker

	isElectionInProgress atomic.Bool      // 是否选举中
	electionManager      *electionManager // 选举管理
}

func NewServer(opts *options.Options) *Server {
	s := &Server{
		s:                 wkserver.New(opts.Cluster.Addr),
		clusterset:        newClusterSet(opts),
		Log:               wklog.NewWKLog("clusterset.server"),
		nodeManager:       newNodeManager(),
		stopChan:          make(chan struct{}),
		doneChan:          make(chan struct{}),
		bootstrapStatus:   bootstrapStatusInit,
		opts:              opts,
		leaderTimeoutTick: time.NewTicker(opts.Cluster.LeaderTimeout),
	}
	s.electionManager = newElectionManager(s)
	s.resetLeaderTimeout()
	return s
}

func (s *Server) Start() error {
	s.initServerRoute()
	s.initClientRoute()

	go s.loopReady()
	err := s.s.Start()
	if err != nil {
		return err
	}
	if strings.TrimSpace(s.opts.Cluster.Join) != "" {
		s.bootstrap()
	}

	return nil
}

func (s *Server) Stop() {
	s.stopChan <- struct{}{}
	<-s.doneChan
}

func (s *Server) onStop() {
	s.s.Stop()
	close(s.doneChan)
}

func (s *Server) Addr() net.Addr {
	return s.s.Addr()
}

func (s *Server) loopReady() {
	defer s.onStop()
	for {
		select {
		case ready := <-s.clusterset.readyChan:
			if s.isElectionInProgress.Load() { // 选举中
				continue
			}
			s.handleClusterReady(ready)
		case <-s.leaderTimeoutTick.C: // 领导超时
			s.Info("leader timeout start election")
			s.handleLeaderTimeout()
		case <-s.stopChan:
			return
		}
	}
}

func (s *Server) handleClusterReady(ready *ClusterReady) {
	if len(ready.updateNodes) > 0 {
		for _, nodeID := range ready.updateNodes {
			go s.requestToSyncClusterset(nodeID)
		}
	}
}

func (s *Server) requestToSyncClusterset(nodeID uint64) {
	r := &pb.SyncClustersetConfigReq{
		Version: s.clusterset.clusterConfigSet.Version,
	}
	data, _ := r.Marshal()
	resp, err := s.s.Request(fmt.Sprintf("%d", nodeID), "/toSyncClusterset", data)
	if err != nil {
		s.Error("request sync clusterset is error", zap.Error(err))
		return
	}
	if resp.Status != proto.Status_OK {
		s.Error("request sync clusterset is error", zap.Int32("status", int32(resp.Status)))
		return
	}
	s.clusterset.Lock()
	s.clusterset.nodeConfigVersionMap[nodeID] = r.Version
	s.clusterset.Unlock()
}
