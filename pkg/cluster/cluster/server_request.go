package cluster

import (
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

func (s *Server) setRoutes() {
	// 获取频道最新日志信息
	s.server.Route("/channel/lastloginfo", s.handleChannelLastLogInfo)
	// 任命频道leader
	s.server.Route("/channel/appointleader", s.handleChannelAppointleader)
	// 获取频道分布式配置
	s.server.Route("/channel/clusterconfig", s.handleClusterconfig)
	//	向频道提案消息
	s.server.Route("/channel/proposeMessage", s.handleProposeMessage)
}

func (s *Server) handleChannelLastLogInfo(c *wkserver.Context) {
	req := &ChannelLastLogInfoReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		c.WriteErr(err)
		return
	}
	shardNo := ChannelKey(req.ChannelID, req.ChannelType)

	lastIndex, err := s.opts.MessageLogStorage.LastIndex(shardNo)
	if err != nil {
		c.WriteErr(err)
		return
	}
	resp := &ChannelLastLogInfoResponse{
		LogIndex: lastIndex,
	}
	respData, err := resp.Marshal()
	if err != nil {
		s.Error("marshal ChannelLastLogInfoResponse failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.Write(respData)
}

func (s *Server) handleChannelAppointleader(c *wkserver.Context) {

	s.Info("handleChannelAppointleader.....")
	req := &AppointLeaderReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		s.Error("unmarshal AppointLeaderReq failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	if req.Term == 0 {
		s.Error("term is zero,appoint leader failed", zap.Uint64("leader", req.LeaderId), zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(ErrTermZero)
		return
	}
	s.Info("handleChannelAppointleader.....222")
	ch, err := s.channelGroupManager.fetchChannel(req.ChannelId, req.ChannelType)
	if err != nil {
		c.WriteErr(err)
		return
	}

	s.Info("handleChannelAppointleader.....333")
	err = ch.appointLeaderTo(req.Term, req.LeaderId)
	if err != nil {
		s.Error("appoint leader failed", zap.Uint64("leader", req.LeaderId), zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(err)
		return
	}

	if !ch.isLeader() {
		err = ch.appointLeader(req.Term)
		if err != nil {
			s.Error("appoint leader failed", zap.Uint64("leader", req.LeaderId), zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
			c.WriteErr(err)
			return
		}
		clusterconfig := ch.clusterConfig
		clusterconfig.LeaderId = req.LeaderId
		clusterconfig.Term = req.Term
		ch.updateClusterConfig(clusterconfig)
		err = s.localStorage.saveChannelClusterConfig(req.ChannelId, req.ChannelType, clusterconfig)
		if err != nil {
			s.Error("saveChannelClusterConfig failed", zap.Error(err))
			c.WriteErr(err)
			return
		}
	}
	s.Info("handleChannelAppointleader.....444")
	c.WriteOk()

}

func (s *Server) handleClusterconfig(c *wkserver.Context) {
	req := &ChannelClusterConfigReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		s.Error("unmarshal ChannelClusterConfigReq failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	slotId := s.getChannelSlotId(req.ChannelID)
	slot := s.clusterEventListener.clusterconfigManager.slot(slotId)
	if slot == nil {
		s.Error("slot not found", zap.Uint32("slotId", slotId))
		c.WriteErr(ErrSlotNotFound)
		return
	}

	if slot.Leader != s.opts.NodeID {
		s.Error("not leader,handleClusterconfig failed", zap.Uint64("leader", slot.Leader), zap.String("channelId", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(ErrNotIsLeader)
		return
	}
	channel, err := s.channelGroupManager.fetchChannel(req.ChannelID, req.ChannelType)
	if err != nil {
		s.Error("get channel failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	if channel == nil {
		s.Error("channel not found", zap.String("channelId", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(ErrChannelNotFound)
		return
	}
	clusterConfig := channel.clusterConfig
	if clusterConfig == nil {
		s.Error("clusterConfig not found", zap.String("channelId", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
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

	ch, err := s.channelGroupManager.fetchChannel(req.ChannelId, req.ChannelType)
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
		s.Error("not leader,handleProposeMessage failed", zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(ErrNotIsLeader)
		return
	}
	logIndex, err := ch.proposeAndWaitCommit(req.Data, s.opts.ProposeTimeout)
	if err != nil {
		s.Error("proposeAndWaitCommit failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	resp := &ChannelProposeResp{
		Index: logIndex,
	}
	data, err := resp.Marshal()
	if err != nil {
		s.Error("marshal ChannelProposeResp failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.Write(data)

}

// 获取频道所在的slotId
func (s *Server) getChannelSlotId(channelId string) uint32 {
	return wkutil.GetSlotNum(int(s.opts.SlotCount), channelId)
}
