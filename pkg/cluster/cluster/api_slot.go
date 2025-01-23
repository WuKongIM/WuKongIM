package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/auth"
	"github.com/WuKongIM/WuKongIM/pkg/auth/resource"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func (s *Server) slotsGet(c *wkhttp.Context) {
	slotIdStrs := c.Query("ids")
	slotIds := make([]uint32, 0)
	if slotIdStrs != "" {
		slotIdStrArray := strings.Split(slotIdStrs, ",")
		for _, slotIdStr := range slotIdStrArray {
			slotId, err := strconv.ParseUint(slotIdStr, 10, 32)
			if err != nil {
				s.Error("slotId parse error", zap.Error(err))
				c.ResponseError(err)
				return
			}
			slotIds = append(slotIds, uint32(slotId))
		}
	}
	slotInfos := make([]*SlotResp, 0, len(slotIds))

	for _, slotId := range slotIds {
		slotInfo, err := s.getSlotInfo(slotId)
		if err != nil {
			s.Error("getSlotInfo error", zap.Error(err))
			c.ResponseError(err)
			return
		}
		slotInfos = append(slotInfos, slotInfo)
	}
	c.JSON(http.StatusOK, slotInfos)
}

func (s *Server) slotClusterConfigGet(c *wkhttp.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		s.Error("id parse error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	slotId := uint32(id)

	slot := s.cfgServer.Slot(slotId)
	if slot == nil {
		s.Error("slot not found", zap.Uint32("slotId", slotId))
		c.ResponseError(err)
		return
	}

	leaderLogMaxIndex, err := s.getSlotMaxLogIndex(slotId)
	if err != nil {
		s.Error("getSlotMaxLogIndex error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	appliedIdx, err := s.slotServer.AppliedIndex(slotId)
	if err != nil {
		s.Error("getAppliedIndex error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	lastIdx, err := s.slotServer.LastIndex(slotId)
	if err != nil {
		s.Error("LastIndex error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	cfg := NewSlotClusterConfigRespFromClusterConfig(appliedIdx, lastIdx, leaderLogMaxIndex, slot)
	c.JSON(http.StatusOK, cfg)

}

// func (s *Server) slotChannelsGet(c *wkhttp.Context) {
// 	idStr := c.Param("id")
// 	id, err := strconv.ParseUint(idStr, 10, 32)
// 	if err != nil {
// 		s.Error("id parse error", zap.Error(err))
// 		c.ResponseError(err)
// 		return
// 	}
// 	slotId := uint32(id)

// 	channels, err := s.opts.ChannelClusterStorage.GetWithSlotId(slotId)
// 	if err != nil {
// 		s.Error("GetChannels error", zap.Error(err))
// 		c.ResponseError(err)
// 		return
// 	}
// 	channelClusterConfigResps := make([]*ChannelClusterConfigResp, 0, len(channels))
// 	for _, cfg := range channels {
// 		if !wkutil.ArrayContainsUint64(cfg.Replicas, s.opts.NodeId) {
// 			continue
// 		}
// 		slot := s.clusterEventServer.Slot(slotId)
// 		if slot == nil {
// 			s.Error("slot not found", zap.Uint32("slotId", slotId))
// 			c.ResponseError(err)
// 			return
// 		}
// 		shardNo := wkutil.ChannelToKey(cfg.ChannelId, cfg.ChannelType)
// 		lastMsgSeq, lastAppendTime, err := s.opts.MessageLogStorage.LastIndexAndAppendTime(shardNo)
// 		if err != nil {
// 			s.Error("LastIndexAndAppendTime error", zap.Error(err))
// 			c.ResponseError(err)
// 			return
// 		}

// 		resp := NewChannelClusterConfigRespFromClusterConfig(slot.Leader, slot.Id, cfg)
// 		resp.LastMessageSeq = lastMsgSeq
// 		if lastAppendTime > 0 {
// 			resp.LastAppendTime = wkutil.ToyyyyMMddHHmm(time.Unix(int64(lastAppendTime/1e9), 0))
// 		}
// 		channelClusterConfigResps = append(channelClusterConfigResps, resp)
// 	}

// 	c.JSON(http.StatusOK, ChannelClusterConfigRespTotal{
// 		Total: len(channelClusterConfigResps),
// 		Data:  channelClusterConfigResps,
// 	})
// }

func (s *Server) slotMigrate(c *wkhttp.Context) {
	var req struct {
		MigrateFrom uint64 `json:"migrate_from"` // 迁移的原节点
		MigrateTo   uint64 `json:"migrate_to"`   // 迁移的目标节点
	}

	if !s.opts.Auth.HasPermissionWithContext(c, resource.Slot.Migrate, auth.ActionWrite) {
		c.ResponseStatus(http.StatusUnauthorized)
		return
	}

	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		s.Error("bind json error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	idStr := c.Param("id")
	id := wkutil.ParseUint32(idStr)

	slot := s.cfgServer.Slot(id)
	if slot == nil {
		s.Error("slot not found", zap.Uint32("slotId", id))
		c.ResponseError(errors.New("slot not found"))
		return
	}

	node := s.cfgServer.Node(slot.Leader)
	if node == nil {
		s.Error("leader not found", zap.Uint64("leaderId", slot.Leader))
		c.ResponseError(errors.New("leader not found"))
		return
	}

	if slot.Leader != s.opts.ConfigOptions.NodeId {
		c.ForwardWithBody(fmt.Sprintf("%s%s", node.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	if !wkutil.ArrayContainsUint64(slot.Replicas, req.MigrateFrom) {
		c.ResponseError(errors.New("MigrateFrom not in replicas"))
		return
	}
	if wkutil.ArrayContainsUint64(slot.Replicas, req.MigrateTo) && req.MigrateFrom != slot.Leader {
		c.ResponseError(errors.New("transition between followers is not supported"))
		return
	}

	if req.MigrateFrom == 0 || req.MigrateTo == 0 {
		c.ResponseError(errors.New("migrateFrom or migrateTo is 0"))
		return
	}

	if req.MigrateFrom == req.MigrateTo {
		c.ResponseError(errors.New("migrateFrom is equal to migrateTo"))
		return
	}

	// err = s.clusterEventServer.ProposeMigrateSlot(id, req.MigrateFrom, req.MigrateTo)
	// if err != nil {
	// 	s.Error("slotMigrate: ProposeMigrateSlot error", zap.Error(err))
	// 	c.ResponseError(err)
	// 	return
	// }

	c.ResponseOK()

}

func (s *Server) allSlotsGet(c *wkhttp.Context) {
	leaderId := s.cfgServer.LeaderId()
	if leaderId == 0 {
		c.ResponseError(errors.New("leader not found"))
		return
	}
	if leaderId != s.opts.ConfigOptions.NodeId {
		leaderNode := s.cfgServer.Node(leaderId)
		c.Forward(fmt.Sprintf("%s%s", leaderNode.ApiServerAddr, c.Request.URL.Path))
		return
	}

	clusterCfg := s.cfgServer.GetClusterConfig()
	resps := make([]*SlotResp, 0, len(clusterCfg.Slots))

	nodeSlotsMap := make(map[uint64][]uint32)
	for _, st := range clusterCfg.Slots {
		if st.Leader == s.opts.ConfigOptions.NodeId {
			slotInfo, err := s.getSlotInfo(st.Id)
			if err != nil {
				s.Error("getSlotInfo error", zap.Error(err))
				c.ResponseError(err)
				return
			}
			resps = append(resps, slotInfo)
			continue
		}
		nodeSlotsMap[st.Leader] = append(nodeSlotsMap[st.Leader], st.Id)
	}

	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, time.Second*10)
	defer cancel()
	requestGroup, _ := errgroup.WithContext(timeoutCtx)

	for nodeId, slotIds := range nodeSlotsMap {

		if !s.cfgServer.NodeIsOnline(nodeId) {
			slotResps, err := s.getSlotInfoForLeaderOffline(slotIds)
			if err != nil {
				s.Error("getSlotInfoForLeaderOffline error", zap.Error(err))
				c.ResponseError(err)
				return
			}
			resps = append(resps, slotResps...)
			continue
		}

		requestGroup.Go(func(nId uint64, sIds []uint32) func() error {
			return func() error {
				slotResps, err := s.requestSlotInfo(nId, sIds, c.CopyRequestHeader(c.Request))
				if err != nil {
					return err
				}
				resps = append(resps, slotResps...)
				return nil
			}
		}(nodeId, slotIds))
	}

	err := requestGroup.Wait()
	if err != nil {
		s.Error("requestSlotInfo error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	sort.Slice(resps, func(i, j int) bool {
		return resps[i].Id < resps[j].Id
	})
	c.JSON(http.StatusOK, SlotRespTotal{
		Total: len(resps),
		Data:  resps,
	})
}

func (s *Server) getSlotInfo(slotId uint32) (*SlotResp, error) {
	slot := s.cfgServer.Slot(slotId)
	if slot == nil {
		return nil, errors.New("slot not found")
	}
	// count, err := s.opts.ChannelClusterStorage.GetCountWithSlotId(slotId)
	// if err != nil {
	// 	return nil, err
	// }
	lastIdx, err := s.slotServer.LastIndex(slotId)
	if err != nil {
		return nil, err
	}
	resp := NewSlotResp(slot, 0)
	resp.LogIndex = lastIdx
	return resp, nil
}

func (s *Server) getSlotMaxLogIndex(slotId uint32) (uint64, error) {

	slot := s.cfgServer.Slot(slotId)
	if slot == nil {
		return 0, errors.New("slot not found")
	}

	if slot.Leader == s.opts.ConfigOptions.NodeId {
		lastIdx, err := s.slotServer.LastIndex(slotId)
		if err != nil {
			return 0, err
		}
		return lastIdx, nil
	}

	slotLogResp, err := s.rpcClient.RequestSlotLastLogInfo(slot.Leader, &SlotLogInfoReq{
		SlotIds: []uint32{slotId},
	})
	if err != nil {
		s.Error("requestSlotLogInfo error", zap.Error(err))
		return 0, err
	}
	if len(slotLogResp.Slots) > 0 {
		return slotLogResp.Slots[0].LogIndex, nil
	}
	return 0, nil
}
