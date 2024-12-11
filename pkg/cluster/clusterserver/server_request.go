package cluster

import (
	"context"
	"encoding/binary"
	"errors"
	"strconv"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/reactor"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

func (s *Server) setRoutes() {
	// 获取频道最新日志信息
	s.netServer.Route("/channel/lastloginfo", s.handleChannelLastLogInfo)
	// 获取频道分布式配置
	s.netServer.Route("/channel/clusterconfig", s.handleClusterconfig)
	//	向频道提案消息
	s.netServer.Route("/channel/proposeMessage", s.handleProposeMessage)
	// 向槽提案数据
	s.netServer.Route("/slot/propose", s.handleSlotPropose)

	// 节点加入集群
	s.netServer.Route("/cluster/join", s.handleClusterJoin)

	// 获取槽的leader term start index
	s.netServer.Route("/slot/leaderTermStartIndex", s.handleSlotLeaderTermStartIndex)
	// 获取频道的leader term start index，follower节点请求领导节点获取领导的term start index
	s.netServer.Route("/channel/leaderTermStartIndex", s.handleChannelLeaderTermStartIndex)

	// 获取槽日志信息
	s.netServer.Route("/slot/logInfo", s.handleSlotLogInfo)
}

func (s *Server) handleChannelLastLogInfo(c *wkserver.Context) {
	var reqs ChannelLastLogInfoReqSet
	err := reqs.Unmarshal(c.Body())
	if err != nil {
		c.Error("Unmarshal request failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	if len(reqs) == 0 {
		c.WriteErr(ErrEmptyRequest)
		return
	}

	resps := make([]*ChannelLastLogInfoResponse, 0, len(reqs))
	for _, req := range reqs {
		shardNo := wkutil.ChannelToKey(req.ChannelId, req.ChannelType)
		lastIndex, lastTerm, err := s.opts.MessageLogStorage.LastIndexAndTerm(shardNo)
		if err != nil {
			c.Error("Get last log info failed", zap.Error(err))
			c.WriteErr(err)
			return
		}

		term := lastTerm // 当前频道的任期
		handler := s.channelManager.get(req.ChannelId, req.ChannelType)
		if handler != nil {
			ch := handler.(*channel)
			if ch.term() > lastTerm {
				term = ch.term()
			}
		}

		resps = append(resps, &ChannelLastLogInfoResponse{
			ChannelId:   req.ChannelId,
			ChannelType: req.ChannelType,
			LogIndex:    lastIndex,
			LogTerm:     lastTerm,
			Term:        term,
		})
	}

	set := ChannelLastLogInfoResponseSet(resps)
	data, err := set.Marshal()
	if err != nil {
		c.Error("Marshal response failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.Write(data)
}

func (s *Server) handleClusterconfig(c *wkserver.Context) {

	req := &ChannelClusterConfigReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		s.Error("unmarshal ChannelClusterConfigReq failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	slotId := s.getSlotId(req.ChannelId)
	slot := s.clusterEventServer.Slot(slotId)
	if slot == nil {
		s.Error("slot not found", zap.Uint32("slotId", slotId))
		c.WriteErr(ErrSlotNotFound)
		return
	}

	if req.From == 0 {
		s.Error("handleClusterconfig: from is 0", zap.Uint64("from", req.From))
		c.WriteErr(errors.New("handleClusterconfig: from is 0"))
		return
	}

	if s.opts.NodeId == req.From {
		s.Panic("from equal local nodeId, error", zap.Uint64("from", req.From), zap.Uint64("nodeId", s.opts.NodeId))
		c.WriteErr(errors.New("from equal local nodeId, error"))
		return
	}

	if slot.Leader != s.opts.NodeId {
		s.Error("not leader,handleClusterconfig failed", zap.Uint64("leader", slot.Leader), zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(ErrNotIsLeader)
		return
	}

	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ReqTimeout)
	defer cancel()
	clusterConfig, _, err := s.loadOrCreateChannelClusterConfig(timeoutCtx, req.ChannelId, req.ChannelType)
	if err != nil {
		s.Error("handleClusterconfig: loadOrCreateChannelClusterConfig failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	if clusterConfig.LeaderId == 0 {
		s.Error("clusterConfig leaderId is 0", zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(ErrNoLeader)
		return
	}

	data, err := clusterConfig.Marshal()
	if err != nil {
		s.Error("marshal clusterConfig failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.Write(data)
}

func (s *Server) handleProposeMessage(c *wkserver.Context) {
	req := &ChannelProposeReq{}
	if err := req.Unmarshal(c.Body()); err != nil {
		s.Error("unmarshal ChannelProposeReq failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	from, err := s.getFrom(c)
	if err != nil {
		s.Error("getFrom failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	// 获取频道集群
	ch, err := s.loadOrCreateChannel(s.cancelCtx, req.ChannelId, req.ChannelType)
	if err != nil {
		s.Error("fetchChannel failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	if ch == nil {
		s.Error("channel not found", zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(ErrChannelNotFound)
		return
	}

	if !ch.isLeader() {
		if ch.leaderId() == from {
			s.Error("leaderId is from,handleProposeMessage failed", zap.Uint64("leaderId", ch.leaderId()), zap.Uint64("from", from))
			c.WriteErr(errors.New("leaderId is from"))
			return
		}
		s.Error("not is leader,handleProposeMessage failed", zap.Uint64("leaderId", ch.leaderId()), zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(ErrOldChannelClusterConfig)
		return
	}
	results, err := s.channelManager.proposeAndWait(s.cancelCtx, req.ChannelId, req.ChannelType, req.Logs)
	if err != nil {
		s.Error("proposeAndWait failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	resp := &ChannelProposeResp{
		ProposeResults: results,
	}
	data, err := resp.Marshal()
	if err != nil {
		s.Error("marshal ChannelProposeResp failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.Write(data)
}

func (s *Server) getFrom(c *wkserver.Context) (uint64, error) {
	return strconv.ParseUint(wkserver.GetUidFromContext(c.Conn()), 10, 64)
}

func (s *Server) handleSlotPropose(c *wkserver.Context) {
	req := &SlotProposeReq{}
	if err := req.Unmarshal(c.Body()); err != nil {
		s.Error("unmarshal SlotProposeReq failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	slot := s.clusterEventServer.Slot(req.SlotId)
	if slot == nil {
		s.Error("slot not found", zap.Uint32("slotId", req.SlotId))
		c.WriteErr(ErrSlotNotFound)
		return
	}

	if slot.Leader != s.opts.NodeId {
		s.Error("not leader,handleSlotPropose failed", zap.Uint64("leader", slot.Leader), zap.Uint32("slotId", req.SlotId))
		c.WriteErr(ErrNotIsLeader)
		return
	}

	results, err := s.slotManager.proposeAndWait(s.cancelCtx, req.SlotId, req.Logs)
	if err != nil {
		s.Error("proposeAndWait failed", zap.Error(err))
		c.WriteErr(err)
		return

	}
	resp := &SlotProposeResp{
		ProposeResults: results,
	}
	data, err := resp.Marshal()
	if err != nil {
		s.Error("marshal ChannelProposeResp failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.Write(data)
}

func (s *Server) handleClusterJoin(c *wkserver.Context) {
	req := &ClusterJoinReq{}
	if err := req.Unmarshal(c.Body()); err != nil {
		s.Error("unmarshal ClusterJoinReq failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	if !s.clusterEventServer.IsLeader() {
		resp, err := s.nodeManager.requestClusterJoin(s.clusterEventServer.LeaderId(), req)
		if err != nil {
			s.Error("requestClusterJoin failed", zap.Error(err))
			c.WriteErr(err)
			return
		}
		data, err := resp.Marshal()
		if err != nil {
			s.Error("marshal ClusterJoinResp failed", zap.Error(err))
			c.WriteErr(err)
			return
		}
		c.Write(data)
		return
	}

	allowVote := false
	if req.Role == pb.NodeRole_NodeRoleReplica {
		allowVote = true
	}

	resp := ClusterJoinResp{}

	nodeInfos := make([]*NodeInfo, 0, len(s.clusterEventServer.Nodes()))
	for _, node := range s.clusterEventServer.Nodes() {
		nodeInfos = append(nodeInfos, &NodeInfo{
			NodeId:     node.Id,
			ServerAddr: node.ClusterAddr,
		})
	}
	resp.Nodes = nodeInfos

	err := s.clusterEventServer.ProposeJoin(&pb.Node{
		Id:          req.NodeId,
		ClusterAddr: req.ServerAddr,
		Join:        true,
		Online:      true,
		Role:        req.Role,
		AllowVote:   allowVote,
		CreatedAt:   time.Now().Unix(),
		Status:      pb.NodeStatus_NodeStatusWillJoin,
	})
	if err != nil {
		s.Error("proposeJoin failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	result, err := resp.Marshal()
	if err != nil {
		s.Error("marshal ClusterJoinResp failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.Write(result)
}

func (s *Server) handleSlotLeaderTermStartIndex(c *wkserver.Context) {
	req := &reactor.LeaderTermStartIndexReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		s.Error("unmarshal request error", zap.Error(err))
		c.WriteErr(err)
		return
	}

	start := time.Now()
	defer func() {
		cost := time.Since(start)
		s.Info("handleSlotLeaderTermStartIndex too cost", zap.Duration("cost", cost))
	}()

	resultBytes := make([]byte, 8)

	handler := s.slotManager.slotReactor.Handler(req.HandlerKey)
	if handler == nil {
		s.Error("handler not found", zap.String("handlerKey", req.HandlerKey))
		c.WriteErr(errors.New("handler not found"))
		return
	}

	lastIndex, term := handler.LastLogIndexAndTerm()

	if term == req.Term {
		binary.BigEndian.PutUint64(resultBytes, lastIndex+1)
	} else {
		syncTerm := req.Term + 1
		syncTerm, err = s.slotStorage.LeaderLastTermGreaterThan(req.HandlerKey, syncTerm)
		if err != nil {
			s.Error("get leader last term error", zap.Error(err))
			c.WriteErr(err)
			return
		}
		lastIndex, err = s.slotStorage.LeaderTermStartIndex(req.HandlerKey, syncTerm)
		if err != nil {
			s.Error("get leader term start index error", zap.Error(err))
			c.WriteErr(err)
			return
		}
		binary.BigEndian.PutUint64(resultBytes, lastIndex)
	}
	c.Write(resultBytes)
}

func (s *Server) handleChannelLeaderTermStartIndex(c *wkserver.Context) {
	req := &reactor.LeaderTermStartIndexReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		s.Error("unmarshal request error", zap.Error(err))
		c.WriteErr(err)
		return
	}

	resultBytes := make([]byte, 8)

	handler := s.channelManager.channelReactor.Handler(req.HandlerKey)
	if handler == nil {

		// 如果不存在，重新激活频道
		channelId, channelType := wkutil.ChannelFromlKey(req.HandlerKey)
		timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, time.Second*10)
		handler, err = s.loadOrCreateChannel(timeoutCtx, channelId, channelType)
		cancel()
		if err != nil {
			s.Error("handler not found", zap.String("handlerKey", req.HandlerKey))
			c.WriteErr(errors.New("handler not found"))
			return
		}
	}

	lastIndex, term := handler.LastLogIndexAndTerm()

	if term == req.Term {
		binary.BigEndian.PutUint64(resultBytes, lastIndex+1)
	} else {
		syncTerm := req.Term + 1
		syncTerm, err = s.opts.MessageLogStorage.LeaderLastTermGreaterThan(req.HandlerKey, syncTerm)
		if err != nil {
			s.Error("get leader last term error", zap.Error(err))
			c.WriteErr(err)
			return
		}
		lastIndex, err = s.opts.MessageLogStorage.LeaderTermStartIndex(req.HandlerKey, syncTerm)
		if err != nil {
			s.Error("get leader term start index error", zap.Error(err))
			c.WriteErr(err)
			return
		}
		binary.BigEndian.PutUint64(resultBytes, lastIndex)
	}
	c.Write(resultBytes)
}

func (s *Server) handleSlotLogInfo(c *wkserver.Context) {
	req := &SlotLogInfoReq{}
	if err := req.Unmarshal(c.Body()); err != nil {
		s.Error("unmarshal SlotLogInfoReq failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	slotInfos, err := s.slotInfos(req.SlotIds)
	if err != nil {
		s.Error("get slotInfos failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	resp := &SlotLogInfoResp{
		NodeId: s.opts.NodeId,
		Slots:  slotInfos,
	}
	data, err := resp.Marshal()
	if err != nil {
		s.Error("marshal SlotLogInfoResp failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.Write(data)
}
