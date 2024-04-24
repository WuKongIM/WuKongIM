package cluster

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
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
	// 提案更新api服务地址
	s.netServer.Route("/config/proposeUpdateApiServerAddr", s.handleUpdateApiServerAddr)

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
		fmt.Println("request......", req.ChannelId, req.ChannelType)
		shardNo := ChannelToKey(req.ChannelId, req.ChannelType)
		lastIndex, term, err := s.opts.MessageLogStorage.LastIndexAndTerm(shardNo)
		if err != nil {
			c.Error("Get last log info failed", zap.Error(err))
			c.WriteErr(err)
			return
		}
		resps = append(resps, &ChannelLastLogInfoResponse{
			ChannelId:   req.ChannelId,
			ChannelType: req.ChannelType,
			LogIndex:    lastIndex,
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

	if slot.Leader != s.opts.NodeId {
		s.Error("not leader,handleClusterconfig failed", zap.Uint64("leader", slot.Leader), zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(ErrNotIsLeader)
		return
	}

	clusterConfig, err := s.opts.ChannelClusterStorage.Get(req.ChannelId, req.ChannelType)
	if err != nil {
		s.Error("get clusterConfig failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	if wkdb.IsEmptyChannelClusterConfig(clusterConfig) {
		s.Error("clusterConfig not found", zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(ErrClusterConfigNotFound)
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
	return strconv.ParseUint(c.Conn().UID(), 10, 64)
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

func (s *Server) handleUpdateApiServerAddr(c *wkserver.Context) {
	req := &UpdateApiServerAddrReq{}
	if err := req.Unmarshal(c.Body()); err != nil {
		s.Error("unmarshal UpdateApiServerAddrReq failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	if !s.clusterEventServer.IsLeader() {
		s.Error("not leader,handleUpdateApiServerAddr failed")
		c.WriteErr(ErrNotIsLeader)
		return
	}

	err := s.clusterEventServer.ProposeUpdateApiServerAddr(req.NodeId, req.ApiServerAddr)
	if err != nil {
		s.Error("proposeUpdateApiServerAddr failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.WriteOk()

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
