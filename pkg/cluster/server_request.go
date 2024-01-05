package cluster

import (
	"errors"
	"strconv"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"go.uber.org/zap"
)

func (s *Server) setRoutes() {

	// 同步集群配置
	s.clusterServer.Route("/syncClusterConfig", s.handleSyncClusterConfig)
	// 获取指定的slot的信息
	s.clusterServer.Route("/slot/infos", s.handleSlotInfos)
	// 同步槽的日志
	s.clusterServer.Route("/slot/syncLog", s.handleSlotSyncLog)
	// 远程提案
	s.clusterServer.Route("/slot/propose", s.handlePropose)
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

func (s *Server) handleSlotInfos(c *wkserver.Context) {
	req := &SlotInfoReportRequest{}
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
	resp := &SlotInfoReportResponse{
		NodeID:    s.opts.NodeID,
		SlotInfos: slotInfos,
	}

	respData, err := resp.Marshal()
	if err != nil {
		s.Error("marshal SlotInfoReportResponse failed", zap.Error(err))
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
	logs, err := slot.replicaServer.SyncLogs(nodeID, req.StartLogIndex, req.Limit)
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
		return
	}

	c.Write(respData)

	err = s.slotManager.slotStateMachine.saveSlotSyncInfo(slot.slotID, &replica.SyncInfo{
		NodeID:       nodeID,
		LastLogIndex: req.StartLogIndex,
		LastSyncTime: uint64(time.Now().Unix()),
	})
	if err != nil {
		s.Warn("save slot sync info failed", zap.Error(err), zap.Uint32("slotID", slot.slotID))
		return
	}

}

func (s *Server) handlePropose(c *wkserver.Context) {
	req := &ProposeRequest{}
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
	err = slot.Propose(req.Data)
	if err != nil {
		c.WriteErr(err)
		return
	}
	c.WriteOk()
}
