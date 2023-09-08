package clusterset

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
)

func (s *Server) initClientRoute() {
	s.s.Route("/toSyncClusterset", s.toSyncClusterset) // 去同步集群配置
	s.s.Route("/requestVote", s.handleRequestVote)     // 处理投票
	s.s.Route("/ping", s.handlePing)                   // 领导心跳
}

func (s *Server) bootstrap() {

	if s.clusterset.existNode(s.opts.Cluster.NodeID) { // 如果存在节点，说明已经加入集群
		s.bootstrapStatus = bootstrapStatusReady
		return
	}

	var (
		bootstrapcli *client.Client
		errorSleep   = time.Millisecond * 500
		err          error
	)
	for {
		switch s.bootstrapStatus {
		case bootstrapStatusInit:
			s.bootstrapStatus = bootstrapStatusWaitConnect
		case bootstrapStatusWaitConnect:
			bootstrapcli, err = s.connectBootstrapdNode()
			if err != nil {
				s.Error("connect bootstrap node is error", zap.Error(err))
				time.Sleep(errorSleep)
				continue
			}
			s.bootstrapStatus = bootstrapStatusWaitJoin
		case bootstrapStatusWaitJoin:
			err = s.requestJoinCluster(bootstrapcli)
			if err != nil {
				s.Error("request join cluster is error", zap.Error(err))
				time.Sleep(errorSleep)
				continue
			}
			s.bootstrapStatus = bootstrapStatusReady
		case bootstrapStatusReady:
			return
		}
	}
}

func (s *Server) connectBootstrapdNode() (*client.Client, error) {
	if strings.TrimSpace(s.opts.Addr) == "" {
		return nil, errors.New("bootstrap node addr is empty")
	}
	bootstrapcli := client.New(s.opts.Cluster.Join, client.WithUID(GetFakeNodeID(s.opts.Cluster.NodeID)))
	err := bootstrapcli.Connect()
	if err != nil {
		return nil, err
	}
	return bootstrapcli, nil
}

func (s *Server) requestJoinCluster(cli *client.Client) error {
	req := &pb.JoinReq{
		NodeID:     s.opts.Cluster.NodeID,
		ServerAddr: s.opts.Addr,
	}
	data, _ := req.Marshal()
	resp, err := cli.Request("/join", data)
	if err != nil {
		s.Error("request join cluster is error", zap.Error(err))
		return err
	}
	if resp.Status != proto.Status_OK {
		s.Error("request join cluster is error", zap.Error(err))
		return errors.New("request join cluster is error")
	}
	clusterConfigSet := &pb.ClusterConfigSet{}
	err = clusterConfigSet.Unmarshal(resp.Body)
	if err != nil {
		s.Error("unmarshal is error", zap.Error(err))
		return err
	}
	s.clusterset.clusterConfigSet = clusterConfigSet
	return nil
}

func (s *Server) toSyncClusterset(c *wkserver.Context) {
	if s.leaderCli == nil {
		s.Debug("leader is nil")
		return
	}
	req := &pb.SyncClustersetConfigReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		c.WriteErr(err)
		return
	}
	if s.clusterset.clusterConfigSet.Version >= req.Version {
		c.WriteErrorAndStatus(fmt.Errorf("version is latest"), StatusClustersetconfigIsLatest)
		return
	}
	c.WriteOk()

	go func() {
		err = s.syncClusterset()
		if err != nil {
			s.Error("sync clusterset is error", zap.Error(err))
		}
	}()
}

func (s *Server) syncClusterset() error {
	resp, err := s.leaderCli.Request("/syncClusterset", []byte{})
	if err != nil {
		return err
	}
	clusterConfigSet := &pb.ClusterConfigSet{}
	err = clusterConfigSet.Unmarshal(resp.Body)
	if err != nil {
		return err
	}
	if s.clusterset.clusterConfigSet.Version >= clusterConfigSet.Version {
		return nil
	}
	return s.clusterset.updateClustersetConfig(clusterConfigSet)
}

func (s *Server) handlePing(c *wkserver.Context) {
	p := &pb.Ping{}
	err := p.Unmarshal(c.Body())
	if err != nil {
		s.Error("ping unmarshal is error", zap.Error(err))
		c.WriteErr(err)
		return
	}
	s.resetLeaderTimeout()
	c.WriteOk()

	if p.Version > s.clusterset.version() {
		go func() {
			err = s.syncClusterset()
			if err != nil {
				s.Error("sync clusterset is error", zap.Error(err))
			}
		}()
	}

}

func (s *Server) handleRequestVote(c *wkserver.Context) {
	req := &pb.VoteReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		s.Error("request vote unmarshal is error", zap.Error(err))
		c.WriteErr(err)
		return
	}
	voteResp := &pb.VoteResp{
		PartitionID: s.clusterset.leaderID(),
	}
	if s.electionManager.voteForPartition != 0 {
		voteResp.VoteGranted = false
		voteResp.Reason = "already vote"
	} else {
		if req.ConfigVersion >= s.clusterset.clusterConfigSet.Version { // 如果请求的配置版本大于等于当前配置版本，才会投票
			voteResp.VoteGranted = true
		} else {
			voteResp.VoteGranted = false
			voteResp.Reason = "config version is old"
		}
	}
	c.WriteMessage(voteResp)

}

func (s *Server) handleLeaderTimeout() {
	fmt.Println("handleLeaderTimeout---->1")
	if s.isElectionInProgress.Load() { // 正在选举，不需要处理
		return
	}
	fmt.Println("handleLeaderTimeout---->2")
	if s.clusterset.isLeader() { // 本身就是领导者，不需要处理
		return
	}
	s.triggerElection()
}

func (s *Server) resetLeaderTimeout() {
	leaderTimeout := time.Duration(rand.Intn(150))*time.Millisecond + s.opts.Cluster.LeaderTimeout
	s.leaderTimeoutTick.Reset(leaderTimeout)
}

// triggerElection 触发选举
func (s *Server) triggerElection() {
	fmt.Println("triggerElection---->1")
	if s.isElectionInProgress.Load() {
		return
	}
	s.isElectionInProgress.Store(true)
	defer s.isElectionInProgress.Store(false)
	s.leaderTimeoutTick.Stop()
	s.Debug("trigger election")

	// 选举过程就是摇骰子，谁的骰子点数大，谁就是领导者

	electionResult := s.electionManager.run() // 运行选举
	if electionResult.leader {                // 当选领导
		s.Info("become leader")
		s.stopPingChan = make(chan struct{})
		go s.loopSendPing() // 开始发送心跳
	} else {
		s.Info("become follower")
		if s.stopPingChan != nil {
			close(s.stopPingChan)
			s.stopPingChan = nil
		}
		fmt.Println("zzz--->")
		s.resetLeaderTimeout()
		fmt.Println("zzz--->2")
	}
	s.Info("election is over", zap.Bool("leader", electionResult.leader))

}

func (s *Server) loopSendPing() {
	pingTick := time.NewTicker(s.opts.Cluster.LeaderTimeout / 2)
	for {
		select {
		case <-s.stopChan:
			return
		case <-pingTick.C:
			s.sendPing()
		case <-s.stopPingChan:
			return

		}
	}
}

func (s *Server) sendPing() {
	nodes := s.nodeManager.getAllNode()
	if len(nodes) == 0 {
		return
	}
	for _, n := range nodes {
		pingR := &pb.Ping{
			NodeID:  n.nodeID,
			Version: s.clusterset.version(),
		}
		data, _ := pingR.Marshal()
		err := s.s.RequestAsync(GetFakeNodeID(n.nodeID), "/ping", data)
		if err != nil {
			s.Error("send ping is error", zap.Error(err))
		}
	}
}
