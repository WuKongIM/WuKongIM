package cluster

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/clusterevent/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

func (s *Server) setRoutes() {

	// 同步集群配置
	s.clusterServer.Route("/syncClusterConfig", s.handleSyncClusterConfig)
	// 获取槽的日志信息
	s.clusterServer.Route("/slot/loginfo", s.handleSlotLogInfo)
	// 同步槽的日志
	s.clusterServer.Route("/slot/syncLog", s.handleSlotSyncLog)
	// slot远程提案
	s.clusterServer.Route("/slot/propose", s.handleSlotPropose)
	// 获取频道日志信息
	s.clusterServer.Route("/channel/loginfo", s.handleChannelLogInfo)
	// channel远程提案（元数据）
	s.clusterServer.Route("/channel/meta/propose", s.handleChannelMetaPropose)
	// channel远程提案（消息数据）
	s.clusterServer.Route("/channel/message/propose", s.handleChannelMessagePropose)
	// channel远程提案（消息数据，批量消息提案）
	s.clusterServer.Route("/channel/messages/propose", s.handleChannelMessagesPropose)
	// 同步频道的元数据日志
	s.clusterServer.Route("/channel/meta/syncLog", s.handleChannelMetaSyncLog)
	// 同步频道的消息数据日志
	s.clusterServer.Route("/channel/message/syncLog", s.handleChannelMessageSyncLog)
	// 获取频道集群信息
	s.clusterServer.Route("/channel/getclusterinfo", s.handleGetClusterInfo)
	// 处理节点更新
	s.clusterServer.Route("/node/update", s.handleNodeUpdate)
	// 频道领导请求副本应用频道集群配置
	s.clusterServer.Route("/channel/applyClusterInfo", s.handleChannelApplyClusterInfo)
	// 获取频道最新日志信息
	s.clusterServer.Route("/channel/clusterdetail", s.handleGetClusterDetail)
}

func (s *Server) handleSyncClusterConfig(c *wkserver.Context) {
	clusterCfg := s.clusterEventManager.GetClusterConfig()
	data, err := clusterCfg.Marshal()
	if err != nil {
		c.WriteErr(err)
		return
	}
	c.Write(data)
}

func (s *Server) handleSlotLogInfo(c *wkserver.Context) {
	req := &SlotLogInfoReportRequest{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		c.WriteErr(err)
		return
	}
	slotInfos, err := s.getSlotInfosFromLocalNode(req.SlotIDs)
	if err != nil {
		c.WriteErr(err)
		return
	}
	resp := &SlotLogInfoReportResponse{
		NodeID:    s.opts.NodeID,
		SlotInfos: slotInfos,
	}

	respData, err := resp.Marshal()
	if err != nil {
		s.Error("marshal SlotInfoReportResponse failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.Write(respData)
}

func (s *Server) handleChannelLogInfo(c *wkserver.Context) {
	req := &ChannelLogInfoReportRequest{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		c.WriteErr(err)
		return
	}
	shardNo := GetChannelKey(req.ChannelID, req.ChannelType)
	lastLogIndex, err := s.channelManager.pebbleStorage.LastIndex(shardNo)
	if err != nil {
		c.WriteErr(err)
		return
	}
	resp := &ChannelLogInfoReportResponse{
		LogIndex: lastLogIndex,
	}
	respData, err := resp.Marshal()
	if err != nil {
		s.Error("marshal ChannelLogInfoReportResponse failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.Write(respData)
}

func (s *Server) handleSlotSyncLog(c *wkserver.Context) {
	req := &replica.SyncReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		c.WriteErr(err)
		return
	}
	s.Debug("副本来同步槽日志", zap.String("slot", req.ShardNo), zap.String("replicaNodeID", c.Conn().UID()), zap.Uint64("startLogIndex", req.StartLogIndex), zap.Uint32("limit", req.Limit))

	slotID, _ := strconv.ParseUint(req.ShardNo, 10, 32)
	slot := s.slotManager.GetSlot(uint32(slotID))
	if slot == nil {
		s.Error("slot not found handleLogSyncNotify failed", zap.Uint32("slotID", uint32(slotID)))
		c.WriteErr(errors.New("slot not found"))
		return
	}
	if !slot.IsLeader() {
		s.Error("the node not is leader of slot", zap.Uint32("slotID", slot.slotID))
		c.WriteErr(errors.New("the node not is leader of slot"))
		return
	}

	nodeID, err := strconv.ParseUint(c.Conn().UID(), 10, 64)
	if err != nil {
		c.WriteErr(err)
		return
	}
	logs, err := slot.SyncLogs(nodeID, req.StartLogIndex, req.Limit)
	if err != nil {
		s.Error("sync logs failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	resp := &replica.SyncRsp{
		Logs: logs,
	}
	respData, err := resp.Marshal()
	if err != nil {
		s.Error("marshal SyncRsp failed", zap.Error(err))
		return
	}

	c.Write(respData)

	err = s.stateMachine.saveSlotSyncInfo(slot.slotID, &replica.SyncInfo{
		NodeID:           nodeID,
		LastSyncLogIndex: req.StartLogIndex,
		LastSyncTime:     uint64(time.Now().UnixNano()),
	})
	if err != nil {
		s.Warn("save slot sync info failed", zap.Error(err), zap.Uint32("slotID", slot.slotID))
		return
	}

}

func (s *Server) handleSlotPropose(c *wkserver.Context) {
	req := &SlotProposeRequest{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		c.WriteErr(err)
		return
	}
	slot := s.slotManager.GetSlot(req.SlotID)
	if slot == nil {
		s.Error("slot not found handleAppendLog failed", zap.Uint32("slotID", req.SlotID))
		c.WriteErr(errors.New("slot not found"))
		return
	}
	if !slot.IsLeader() {
		s.Error("the node is not leader of slot, propose failed", zap.Uint32("slotID", req.SlotID))
		c.WriteErr(errors.New("the node is not leader of slot, propose failed"))
		return
	}
	err = slot.Propose(req.Data)
	if err != nil {
		c.WriteErr(err)
		return
	}
	c.WriteOk()
}

func (s *Server) handleChannelMetaPropose(c *wkserver.Context) {
	req := &ChannelProposeRequest{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		c.WriteErr(err)
		return
	}
	s.Debug("收到远程提案频道元数据", zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))

	// 获取channel对象
	channel, err := s.channelManager.GetChannel(req.ChannelID, req.ChannelType)
	if err != nil {
		s.Error("handleChannelMetaPropose: get channel failed", zap.Error(err), zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(err)
		return
	}
	if channel == nil {
		s.Error("handleChannelMetaPropose: channel not found", zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(fmt.Errorf("channel[%s:%d] not found", req.ChannelID, req.ChannelType))
		return
	}
	if channel.LeaderID() != s.opts.NodeID { // 如果当前节点不是领导节点
		s.Error("the node is not leader, failed to propose channel", zap.Uint64("nodeID", s.opts.NodeID), zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(fmt.Errorf("the node[%d] is not leader", s.opts.NodeID))
		return
	}
	err = channel.ProposeMeta(req.Data)
	if err != nil {
		s.Error("propose channel failed", zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(err)
		return
	}
	c.WriteOk()
}

func (s *Server) handleChannelMessagePropose(c *wkserver.Context) {
	req := &ChannelProposeRequest{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		c.WriteErr(err)
		return
	}
	s.Debug("收到远程提案消息数据", zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))

	// 获取channel对象
	channel, err := s.channelManager.GetChannel(req.ChannelID, req.ChannelType)
	if err != nil {
		s.Error("handleChannelMessagePropose: get channel failed", zap.Error(err), zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(err)
		return
	}
	if channel == nil {
		s.Error("handleChannelMessagePropose: channel not found", zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(fmt.Errorf("channel[%s:%d] not found", req.ChannelID, req.ChannelType))
		return
	}
	if channel.LeaderID() != s.opts.NodeID { // 如果当前节点不是领导节点
		s.Error("the node is not leader, failed to propose channel", zap.Uint64("nodeID", s.opts.NodeID), zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(fmt.Errorf("the node[%d] is not leader", s.opts.NodeID))
		return
	}
	_, err = channel.ProposeMessage(req.Data)
	if err != nil {
		s.Error("propose message to channel failed", zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(err)
		return
	}
	c.WriteOk()
}

func (s *Server) handleChannelMessagesPropose(c *wkserver.Context) {
	req := &ChannelProposesRequest{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		c.WriteErr(err)
		return
	}
	s.Debug("收到远程提案消息数据", zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))

	// 获取channel对象
	channel, err := s.channelManager.GetChannel(req.ChannelID, req.ChannelType)
	if err != nil {
		s.Error("handleChannelMessagePropose: get channel failed", zap.Error(err), zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(err)
		return
	}
	if channel == nil {
		s.Error("handleChannelMessagePropose: channel not found", zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(fmt.Errorf("channel[%s:%d] not found", req.ChannelID, req.ChannelType))
		return
	}
	if channel.LeaderID() != s.opts.NodeID { // 如果当前节点不是领导节点
		s.Error("the node is not leader, failed to propose channel", zap.Uint64("nodeID", s.opts.NodeID), zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(fmt.Errorf("the node[%d] is not leader", s.opts.NodeID))
		return
	}
	_, err = channel.ProposeMessages(req.Data)
	if err != nil {
		s.Error("propose message to channel failed", zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(err)
		return
	}
	c.WriteOk()
}

func (s *Server) handleGetClusterInfo(c *wkserver.Context) {
	req := &ChannelClusterInfoRequest{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		c.WriteErr(err)
		return
	}
	slotID := s.GetSlotID(req.ChannelID)

	slot := s.slotManager.GetSlot(slotID)
	if slot == nil {
		s.Error("slot not found handleGetClusterinfo failed", zap.Uint32("slotID", slotID))
		c.WriteErr(errors.New("slot not found"))
		return
	}
	if !slot.IsLeader() { // 不是槽领导者，不能获取集群信息
		s.Error("the node not is leader of slot", zap.Uint32("slotID", slotID))
		c.WriteErr(errors.New("the node not is leader of slot"))
		return
	}
	channel, err := s.channelManager.GetChannel(req.ChannelID, req.ChannelType)
	if err != nil {
		s.Error("get channel failed", zap.Error(err), zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(err)
		return
	}
	if channel == nil {
		s.Error("handleGetClusterinfo: channel not found", zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(fmt.Errorf("channel[%s:%d] not found", req.ChannelID, req.ChannelType))
		return
	}

	clusterInfo := channel.GetClusterInfo()
	resultData, err := clusterInfo.Marshal()
	if err != nil {
		s.Error("marshal clusterInfo failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.Write(resultData)
}

func (s *Server) handleChannelMetaSyncLog(c *wkserver.Context) {

	req := &replica.SyncReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		c.WriteErr(err)
		return
	}
	channelID, channelType := GetChannelFromChannelKey(req.ShardNo)

	startTime := time.Now().UnixNano()
	s.Debug("副本过来同步频道元数据日志", zap.String("replicaNodeID", c.Conn().UID()), zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.Uint64("startLogIndex", req.StartLogIndex), zap.Uint32("limit", req.Limit))

	channel, err := s.channelManager.GetChannel(channelID, channelType)
	if err != nil {
		s.Error("get channel failed", zap.Error(err), zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
		c.WriteErr(err)
		return
	}
	if channel == nil {
		s.Error("handleChannelSyncLog: channel not found", zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
		c.WriteErr(errors.New("handleChannelSyncLog: channel not found"))
		return
	}
	if !channel.IsLeader() {
		s.Error("the node not is leader of channel", zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
		c.WriteErr(errors.New("the node not is leader of channel"))
		return
	}

	nodeID, err := strconv.ParseUint(c.Conn().UID(), 10, 64)
	if err != nil {
		c.WriteErr(err)
		return
	}
	logs, err := channel.SyncMetaLogs(nodeID, req.StartLogIndex, req.Limit)
	if err != nil {
		c.WriteErr(err)
		return
	}
	resp := &replica.SyncRsp{
		Logs: logs,
	}
	respData, err := resp.Marshal()
	if err != nil {
		s.Error("marshal SyncRsp failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	c.Write(respData)

	err = s.stateMachine.saveChannelSyncInfo(channelID, channelType, LogKindMeta, &replica.SyncInfo{
		NodeID:           nodeID,
		LastSyncLogIndex: req.StartLogIndex,
		LastSyncTime:     uint64(time.Now().UnixNano()),
	})
	if err != nil {
		s.Warn("save channel sync info failed", zap.Error(err), zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
		return
	}
	s.Debug("副本过来同步频道元数据日志结束", zap.Int64("cost", (time.Now().UnixNano()-startTime)/1000000), zap.String("replicaNodeID", c.Conn().UID()), zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.Uint64("startLogIndex", req.StartLogIndex), zap.Uint32("limit", req.Limit))
}

func (s *Server) handleChannelMessageSyncLog(c *wkserver.Context) {
	req := &replica.SyncReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		c.WriteErr(err)
		return
	}
	channelID, channelType := GetChannelFromChannelKey(req.ShardNo)

	startTime := time.Now().UnixMilli()
	s.Debug("有副本来同步频道消息日志", zap.Uint64("startLogIndex", req.StartLogIndex), zap.String("replicaNodeID", c.Conn().UID()), zap.String("channelID", channelID), zap.Uint8("channelType", channelType))

	channel, err := s.channelManager.GetChannel(channelID, channelType)
	if err != nil {
		s.Error("get channel failed", zap.Error(err), zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
		c.WriteErr(err)
		return
	}
	if channel == nil {
		s.Error("handleChannelMessageSyncLog: channel not found", zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
		c.WriteErr(errors.New("handleChannelMessageSyncLog: channel not found"))
		return
	}
	if !channel.IsLeader() {
		s.Error("the node not is leader of channel", zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
		c.WriteErr(errors.New("the node not is leader of channel"))
		return
	}

	nodeID, err := strconv.ParseUint(c.Conn().UID(), 10, 64)
	if err != nil {
		c.WriteErr(err)
		return
	}
	logs, err := channel.SyncMessageLogs(nodeID, req.StartLogIndex, req.Limit)
	if err != nil {
		c.WriteErr(err)
		return
	}
	resp := &replica.SyncRsp{
		Logs: logs,
	}
	respData, err := resp.Marshal()
	if err != nil {
		s.Error("marshal SyncRsp failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	c.Write(respData)

	err = s.stateMachine.saveChannelSyncInfo(channelID, channelType, LogKindMessage, &replica.SyncInfo{
		NodeID:           nodeID,
		LastSyncLogIndex: req.StartLogIndex,
		LastSyncTime:     uint64(time.Now().UnixNano()),
	})
	if err != nil {
		s.Warn("save channel sync info failed", zap.Error(err), zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
		return
	}

	s.Debug("有副本来同步频道消息日志结束", zap.Int64("cost", (time.Now().UnixMilli()-startTime)), zap.String("replicaNodeID", c.Conn().UID()), zap.String("channelID", channelID), zap.Uint8("channelType", channelType), zap.Uint64("startLogIndex", req.StartLogIndex), zap.Uint32("limit", req.Limit))
}

func (s *Server) handleNodeUpdate(c *wkserver.Context) {
	nodeUpdate := &pb.Node{}
	err := nodeUpdate.Unmarshal(c.Body())
	if err != nil {
		c.WriteErr(err)
		return
	}
	s.Debug("收到节点更新", zap.Uint64("nodeID", nodeUpdate.Id), zap.String("apiAddr", nodeUpdate.ApiAddr))

	s.clusterEventManager.UpdateNode(nodeUpdate)

	c.WriteOk()
}

func (s *Server) handleChannelApplyClusterInfo(c *wkserver.Context) {
	req := &ChannelClusterInfo{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		s.Error("unmarshal ChannelClusterInfo failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	oldChannelClusterInfo, err := s.stateMachine.getChannelClusterInfo(req.ChannelID, req.ChannelType)
	if err != nil {
		s.Error("query channel cluster info failed", zap.Error(err), zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.WriteErr(err)
		return
	}
	apply := false
	if oldChannelClusterInfo == nil || oldChannelClusterInfo.ConfigVersion < req.ConfigVersion {
		apply = true
	} else {
		s.Warn("本地频道集群配置比请求的新，所以不进行应用", zap.String("fromNodeID", c.Conn().UID()), zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType), zap.Uint64("localConfigVersion", oldChannelClusterInfo.ConfigVersion), zap.Uint64("requestConfigVersion", req.ConfigVersion))
	}

	if apply {
		var oldVersion uint64 = 0
		if oldChannelClusterInfo != nil {
			oldVersion = oldChannelClusterInfo.ConfigVersion
		}
		s.Debug("应用频道集群配置", zap.String("fromNodeID", c.Conn().UID()), zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType), zap.Uint64("localConfigVersion", oldVersion), zap.Uint64("requestConfigVersion", req.ConfigVersion))
		err = s.stateMachine.saveChannelClusterInfo(req)
		if err != nil {
			s.Error("apply channel cluster info failed", zap.Error(err), zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
			c.WriteErr(err)
			return
		}
		s.channelManager.UpdateChannelCacheIfNeed(req)
	}

	c.WriteOk()
}

func (s *Server) handleGetClusterDetail(c *wkserver.Context) {
	var reqs []*channelClusterDetailoReq
	if err := wkutil.ReadJSONByByte(c.Body(), &reqs); err != nil {
		s.Error("unmarshal channelLastLogInfoReq failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	var results []*channelClusterDetailInfo
	for _, req := range reqs {
		resp, err := s.getChannelClusterDetail(req)
		if err != nil {
			s.Error("get channel last log info failed", zap.Error(err))
			c.WriteErr(err)
			return
		}
		results = append(results, resp)
	}
	c.Write([]byte(wkutil.ToJSON(results)))
}

func (s *Server) getChannelClusterDetails(reqs []*channelClusterDetailoReq) ([]*channelClusterDetailInfo, error) {
	var results []*channelClusterDetailInfo
	for _, req := range reqs {
		resp, err := s.getChannelClusterDetail(req)
		if err != nil {
			s.Error("get channel last log info failed", zap.Error(err))
			return nil, err
		}
		results = append(results, resp)
	}
	return results, nil
}

func (s *Server) getChannelClusterDetail(req *channelClusterDetailoReq) (*channelClusterDetailInfo, error) {

	detail := &channelClusterDetailInfo{
		ChannelID:   req.ChannelID,
		ChannelType: req.ChannelType,
		LeaderID:    s.opts.NodeID,
	}

	shardNo := GetChannelKey(req.ChannelID, req.ChannelType)

	// 获取元数据日志最后一条日志的索引和追加时间
	lastLogIndex, lastLogAppendTime, err := s.channelManager.pebbleStorage.LastIndexAndAppendTime(shardNo)
	if err != nil {
		s.Error("get channel last log info failed", zap.Error(err))
		return nil, err
	}
	detail.LeaderLastMetaLogIndex = lastLogIndex
	detail.LeaderLastMetaAppendTime = lastLogAppendTime

	// 获取消息日志最后一条日志的索引和追加时间
	lastMsgLogIndex, lastMsgLogAppendTime, err := s.opts.MessageLogStorage.LastIndexAndAppendTime(shardNo)
	if err != nil {
		s.Error("get channel message last log info failed", zap.Error(err))
		return nil, err
	}
	detail.LeaderLastMsgLogIndex = lastMsgLogIndex
	detail.LeaderLastMsgAppendTime = lastMsgLogAppendTime

	// 获取集群配置
	channelClusterInfo, err := s.stateMachine.getChannelClusterInfo(req.ChannelID, req.ChannelType)
	if err != nil {
		s.Error("get channel cluster info failed", zap.Error(err))
		return nil, err
	}

	if channelClusterInfo != nil {
		detail.Replicas = channelClusterInfo.Replicas
		detail.LeaderID = channelClusterInfo.LeaderID
		detail.ApplyReplicas = channelClusterInfo.Replicas

		// 获取副本同步元数据的信息
		metaSyncInfos, err := s.stateMachine.getChannelSyncInfos(channelClusterInfo.ChannelID, channelClusterInfo.ChannelType, LogKindMeta)
		if err != nil {
			s.Error("getChannelSyncInfos error", zap.Error(err))
			return nil, err
		}
		metaSyncInfoResps := make([]*channelSyncInfo, 0, len(metaSyncInfos))
		for _, metaSyncInfo := range metaSyncInfos {
			channelSyncInfo := newChannelSyncInfo(metaSyncInfo.NodeID, metaSyncInfo.LastSyncLogIndex, metaSyncInfo.LastSyncTime)
			metaSyncInfoResps = append(metaSyncInfoResps, channelSyncInfo)
			if channelSyncInfo.NextLogIndex == 0 {
				channelSyncInfo.LaggingLogCount = detail.LeaderLastMetaLogIndex
			} else {
				channelSyncInfo.LaggingLogCount = detail.LeaderLastMetaLogIndex - (channelSyncInfo.NextLogIndex - 1)
			}

			channelSyncInfo.ReplicaSyncDelay = channelSyncInfo.LastSyncTime/1000/1000 - detail.LeaderLastMetaAppendTime/1000/1000
		}
		detail.MetaSyncInfo = metaSyncInfoResps

		// 获取副本同步消息数据的信息
		msgSyncInfos, err := s.stateMachine.getChannelSyncInfos(channelClusterInfo.ChannelID, channelClusterInfo.ChannelType, LogKindMessage)
		if err != nil {
			s.Error("getChannelSyncInfos error", zap.Error(err))
			return nil, err
		}
		msgSyncInfoResps := make([]*channelSyncInfo, 0, len(msgSyncInfos))
		for _, msgSyncInfo := range msgSyncInfos {
			syncInfo := newChannelSyncInfo(msgSyncInfo.NodeID, msgSyncInfo.LastSyncLogIndex, msgSyncInfo.LastSyncTime)
			msgSyncInfoResps = append(msgSyncInfoResps, syncInfo)
			if syncInfo.NextLogIndex == 0 {
				syncInfo.LaggingLogCount = detail.LeaderLastMsgLogIndex
			} else {
				syncInfo.LaggingLogCount = detail.LeaderLastMsgLogIndex - (syncInfo.NextLogIndex - 1)
			}
			syncInfo.ReplicaSyncDelay = syncInfo.LastSyncTime/1000/1000 - detail.LeaderLastMsgAppendTime/1000/1000
		}
		detail.MsgSyncInfo = msgSyncInfoResps
	}

	return detail, nil
}
