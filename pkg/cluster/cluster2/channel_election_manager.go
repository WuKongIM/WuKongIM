package cluster

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type channelElectionManager struct {
	electionC chan electionReq
	stopper   *syncutil.Stopper
	opts      *Options
	s         *Server
	wklog.Log
}

func newChannelElectionManager(s *Server) *channelElectionManager {
	return &channelElectionManager{
		electionC: make(chan electionReq, 1000),
		stopper:   syncutil.NewStopper(),
		opts:      s.opts,
		s:         s,
		Log:       wklog.NewWKLog("channelElectionManager"),
	}
}

func (c *channelElectionManager) start() error {
	c.stopper.RunWorker(c.loop)
	return nil
}

func (c *channelElectionManager) stop() {
	c.stopper.Stop()
}

func (c *channelElectionManager) loop() {
	electionReqs := make([]electionReq, 0, 100)
	reqLen := 0
	var err error
	for {
		select {
		case req := <-c.electionC:
			reqLen++
			electionReqs = append(electionReqs, req)
			// 取出所有electionReq

			for done := false; !done && reqLen < c.opts.MaxChannelElectionBatchLen; {
				select {
				case r := <-c.electionC:
					reqLen++
					electionReqs = append(electionReqs, r)
				case <-c.stopper.ShouldStop():
					return
				default:
					done = true
				}
			}

			// 提交选举任务
			err = c.s.channelElectionPool.Submit(func(reqs []electionReq) func() {
				return func() {
					c.election(reqs)
				}
			}(electionReqs))
			if err != nil {
				c.Error("submit election failed", zap.Error(err))
			}
			electionReqs = electionReqs[:0]

		case <-c.stopper.ShouldStop():
			return
		}
	}
}

// 添加选举请求
func (c *channelElectionManager) addElectionReq(req electionReq) error {
	select {
	case c.electionC <- req:
	case <-c.stopper.ShouldStop():
		return ErrStopped
	default: // 频道选举通道已满
		return ErrChannelElectionCIsFull
	}
	return nil
}

func (c *channelElectionManager) election(reqs []electionReq) {
	channelLastLogInfoMap, err := c.requestChannelLastLogInfos(reqs)
	if err != nil {
		c.Error("requestChannelLastLogInfos failed", zap.Error(err))
		for _, req := range reqs {
			req.resultC <- electionResp{
				err: err,
			}
		}
		return
	}
	fmt.Println("channelLastLogInfoMap--->", channelLastLogInfoMap)

	for _, req := range reqs {
		lastInfoResps := make([]*replicaChannelLastLogInfoResponse, 0, len(req.cfg.Replicas))
		for replicaId, resps := range channelLastLogInfoMap {
			for _, resp := range resps {
				if req.ch.channelId == resp.ChannelId && req.ch.channelType == resp.ChannelType {
					lastInfoResps = append(lastInfoResps, &replicaChannelLastLogInfoResponse{
						replicaId:                  replicaId,
						ChannelLastLogInfoResponse: resp,
					})
				}
			}
		}
		if len(lastInfoResps) < c.quorum() { // 如果参与选举的节点数小于法定数量，则直接返回错误
			select {
			case req.resultC <- electionResp{
				err: ErrNotEnoughReplicas,
			}:
			case <-c.stopper.ShouldStop():
				return
			}
			continue
		}
		newLeaderId := c.channelLeaderIDByLogInfo(lastInfoResps) // 通过日志信息选举频道领导
		if newLeaderId == 0 {
			select {
			case req.resultC <- electionResp{
				err: ErrNoLeader,
			}:
			case <-c.stopper.ShouldStop():
				return
			}
			continue
		}
		cfg := req.cfg
		cfg.LeaderId = newLeaderId
		select {
		case req.resultC <- electionResp{
			cfg: cfg,
		}:
		case <-c.stopper.ShouldStop():
			return
		}
	}
}

// 通过日志高度选举频道领导
func (c *channelElectionManager) channelLeaderIDByLogInfo(resps []*replicaChannelLastLogInfoResponse) uint64 {

	// 选出resps中最大的日志下标和任期的节点
	var leaderID uint64
	var maxIndex uint64
	var maxTerm uint32
	for _, resp := range resps {
		if resp.Term > maxTerm {
			maxIndex = resp.LogIndex
			maxTerm = resp.Term
			leaderID = resp.replicaId
		} else if resp.Term == maxTerm {
			if resp.LogIndex > maxIndex {
				maxIndex = resp.LogIndex
				leaderID = resp.replicaId
			}
		}
	}

	return leaderID
}

func (c *channelElectionManager) quorum() int {

	return int(c.s.opts.ChannelMaxReplicaCount/2) + 1
}

func (c *channelElectionManager) requestChannelLastLogInfos(reqs []electionReq) (map[uint64][]*ChannelLastLogInfoResponse, error) {
	replicaReqMap := make(map[uint64][]electionReq) // 按照节点Id分组
	for _, req := range reqs {
		for _, replicaId := range req.cfg.Replicas {
			replicaReqMap[replicaId] = append(replicaReqMap[replicaId], req)
		}
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	requestGroup, ctx := errgroup.WithContext(timeoutCtx)
	channelLogInfoMapLock := new(sync.Mutex)
	channelLastLogInfosMap := make(map[uint64][]*ChannelLastLogInfoResponse)
	for replicaId, reqs := range replicaReqMap {
		if !c.s.clusterEventServer.NodeOnline(replicaId) { // 如果节点不在线，则忽略对此节点的请求
			continue
		}
		if replicaId == c.opts.NodeId { // 如果是自己，则直接返回
			for _, req := range reqs {
				lastIndex, lastTerm, err := c.s.opts.MessageLogStorage.LastIndexAndTerm(req.ch.key)
				if err != nil {
					return nil, err
				}
				channelLastLogInfosMap[replicaId] = append(channelLastLogInfosMap[replicaId], &ChannelLastLogInfoResponse{
					ChannelId:   req.ch.channelId,
					ChannelType: req.ch.channelType,
					LogIndex:    lastIndex,
					Term:        lastTerm,
				})
			}
			continue
		}
		channelLastLogInfoReqs := make([]*ChannelLastLogInfoReq, 0, len(reqs))
		for _, req := range reqs {
			channelLastLogInfoReqs = append(channelLastLogInfoReqs, &ChannelLastLogInfoReq{
				ChannelId:   req.ch.channelId,
				ChannelType: req.ch.channelType,
			})
		}
		requestGroup.Go(func(rcId uint64) func() error {
			node := c.s.nodeManager.node(rcId)
			if node == nil {
				c.Warn("node is not found", zap.Uint64("nodeID", rcId))
				return nil
			}
			resps, err := node.requestChannelLastLogInfo(ctx, ChannelLastLogInfoReqSet(channelLastLogInfoReqs))
			if err != nil {
				c.Warn("requestChannelLastLogInfo failed", zap.Error(err))
				return nil
			}
			channelLogInfoMapLock.Lock()
			channelLastLogInfosMap[rcId] = []*ChannelLastLogInfoResponse(resps)
			channelLogInfoMapLock.Unlock()
			return nil
		}(replicaId))
	}
	_ = requestGroup.Wait()
	return channelLastLogInfosMap, nil
}

type electionReq struct {
	ch      *channel
	cfg     wkdb.ChannelClusterConfig
	resultC chan electionResp
}

type electionResp struct {
	cfg wkdb.ChannelClusterConfig
	err error
}

type replicaChannelLastLogInfoResponse struct {
	replicaId uint64
	*ChannelLastLogInfoResponse
}
