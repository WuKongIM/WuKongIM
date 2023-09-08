package clusterset

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type electionStatus int

const (
	electionStatusInit        electionStatus = iota
	electionStatusRequestVote                // 请求投票
	electionStatusCountVotes                 // 统计票数
)

type electionManager struct {
	diceFaces        int
	randObj          *rand.Rand
	status           electionStatus
	voteForPartition uint64                    // 投票给哪个分区
	voteMap          map[int32]map[uint64]bool // 每轮的投票结果
	voteMapLock      sync.Locker
	round            int32 // 第几轮投票

	wklog.Log
	s *Server
}

func newElectionManager(s *Server) *electionManager {
	// 设置随机数种子
	randObj := rand.New(rand.NewSource(time.Now().UnixNano()))
	return &electionManager{
		diceFaces: 1000,
		randObj:   randObj,
		s:         s,
		Log:       wklog.NewWKLog(fmt.Sprintf("electionManager[%d]", s.opts.Cluster.NodeID)),
	}
}

func (e *electionManager) run() *electionResult {
	e.status = electionStatusInit
	e.round = 1
	for {
		switch e.status {
		case electionStatusInit:
			e.status = electionStatusRequestVote
			e.Info("start election", zap.Int32("round", e.round))
		case electionStatusRequestVote:
			e.Info("request vote", zap.Int32("round", e.round))
			e.requestVoteForAll()
			e.status = electionStatusCountVotes
		case electionStatusCountVotes:
			e.Info("count votes", zap.Int32("round", e.round))
			eResult := e.countVotes()
			e.status = electionStatusInit
			return eResult
		}
	}
}

func (e *electionManager) countVotes() *electionResult {
	var (
		voteCount int
	)
	for _, voteGranted := range e.voteMap[e.round] {
		if voteGranted {
			voteCount++
		}
	}
	if voteCount > len(e.s.clusterset.partitionManagers)/2 {
		return &electionResult{
			leader: true,
		}
	}
	return &electionResult{
		leader: false,
	}
}

func (e *electionManager) requestVoteForAll() {
	currentPartition := e.s.clusterset.getCurrentPartition()
	if currentPartition == nil {
		e.Error("current node has no partition")
		return
	}
	var wg sync.WaitGroup
	for _, partitionM := range e.s.clusterset.partitionManagers {
		leaderCfg := partitionM.getLeadNodeConfig()
		if leaderCfg == nil { // 没有领导节点，说明分区内部还没有选出领导节点
			continue
		}
		if leaderCfg.NodeID == e.s.opts.Cluster.NodeID { // 当前节点不做连接
			e.voteForPartition = partitionM.cfg.PartitionID // 投票给自己
			continue
		}

		wg.Add(1)
		go func(pm *partitionManager) {
			voteResp, err := pm.requestVote(&pb.VoteReq{
				PartitionID:   currentPartition.PartitionID,
				NodeID:        e.s.opts.Cluster.NodeID,
				ConfigVersion: e.s.clusterset.version(),
				Round:         e.round,
			})
			if err != nil {
				e.Error("request vote is error", zap.Error(err))
				wg.Done()
				return
			}
			e.voteMapLock.Lock()
			e.voteMap[voteResp.Round][voteResp.PartitionID] = voteResp.VoteGranted
			e.voteMapLock.Unlock()
			wg.Done()
		}(partitionM)

	}
	wg.Wait()
}

// 选举结果
type electionResult struct {
	leader bool // 是否是领导节点
}
