package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/auth"
	"github.com/WuKongIM/WuKongIM/pkg/auth/resource"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/replica"
	"github.com/WuKongIM/WuKongIM/pkg/network"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func (s *Server) ServerAPI(route *wkhttp.WKHttp, prefix string) {
	s.apiPrefix = prefix

	route.GET(s.formatPath("/nodes"), s.nodesGet)                     // 获取所有节点
	route.GET(s.formatPath("/node"), s.nodeGet)                       // 获取当前节点信息
	route.GET(s.formatPath("/simpleNodes"), s.simpleNodesGet)         // 获取简单节点信息
	route.GET(s.formatPath("/nodes/:id/channels"), s.nodeChannelsGet) // 获取节点的所有频道信息

	// route.GET(s.formatPath("/channels/:channel_id/:channel_type/config"), s.channelClusterConfigGet) // 获取频道分布式配置
	route.GET(s.formatPath("/slots"), s.slotsGet)                                                      // 获取指定的槽信息
	route.GET(s.formatPath("/allslot"), s.allSlotsGet)                                                 // 获取所有槽信息
	route.GET(s.formatPath("/slots/:id/config"), s.slotClusterConfigGet)                               // 槽分布式配置
	route.GET(s.formatPath("/slots/:id/channels"), s.slotChannelsGet)                                  // 获取某个槽的所有频道信息
	route.POST(s.formatPath("/slots/:id/migrate"), s.slotMigrate)                                      // 迁移槽
	route.GET(s.formatPath("/info"), s.clusterInfoGet)                                                 // 获取集群信息
	route.GET(s.formatPath("/messages"), s.messageSearch)                                              // 搜索消息
	route.GET(s.formatPath("/channels"), s.channelSearch)                                              // 频道搜索
	route.GET(s.formatPath("/channels/:channel_id/:channel_type/subscribers"), s.subscribersGet)       // 获取频道的订阅者列表
	route.GET(s.formatPath("/channels/:channel_id/:channel_type/denylist"), s.denylistGet)             // 获取黑名单列表
	route.GET(s.formatPath("/channels/:channel_id/:channel_type/allowlist"), s.allowlistGet)           // 获取白名单列表
	route.GET(s.formatPath("/users"), s.userSearch)                                                    // 用户搜索
	route.GET(s.formatPath("/devices"), s.deviceSearch)                                                // 设备搜索
	route.GET(s.formatPath("/conversations"), s.conversationSearch)                                    // 搜索最近会话消息
	route.POST(s.formatPath("/channels/:channel_id/:channel_type/migrate"), s.channelMigrate)          // 迁移频道
	route.GET(s.formatPath("/channels/:channel_id/:channel_type/config"), s.channelClusterConfig)      // 获取频道的分布式配置
	route.POST(s.formatPath("/channels/:channel_id/:channel_type/start"), s.channelStart)              // 开始频道
	route.POST(s.formatPath("/channels/:channel_id/:channel_type/stop"), s.channelStop)                // 停止频道
	route.POST(s.formatPath("/channel/status"), s.channelStatus)                                       // 获取频道状态
	route.GET(s.formatPath("/channels/:channel_id/:channel_type/replicas"), s.channelReplicas)         // 获取频道副本信息
	route.GET(s.formatPath("/channels/:channel_id/:channel_type/localReplica"), s.channelLocalReplica) // 获取频道在本节点的副本信息

	route.GET(s.formatPath("/logs"), s.clusterLogs) // 获取节点日志

}

func (s *Server) nodesGet(c *wkhttp.Context) {

	leaderId := s.clusterEventServer.LeaderId()

	nodeCfgs := make([]*NodeConfig, 0, len(s.clusterEventServer.Nodes()))

	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, time.Second*10)
	defer cancel()
	requestGroup, _ := errgroup.WithContext(timeoutCtx)

	cfg := s.clusterEventServer.Config()

	for _, node := range s.clusterEventServer.Nodes() {
		if node.Id == s.opts.NodeId {
			nodeCfg := s.getLocalNodeInfo()
			nodeCfgs = append(nodeCfgs, nodeCfg)
			continue
		}
		if !node.Online {
			nodeCfg := NewNodeConfigFromNode(node)
			nodeCfg.IsLeader = wkutil.BoolToInt(leaderId == node.Id)
			nodeCfg.Term = cfg.Term
			nodeCfg.SlotCount = s.getNodeSlotCount(node.Id, cfg)
			nodeCfg.SlotLeaderCount = s.getNodeSlotLeaderCount(node.Id, cfg)
			nodeCfgs = append(nodeCfgs, nodeCfg)
			continue
		}
		requestGroup.Go(func(nId uint64) func() error {
			return func() error {
				nodeCfg, err := s.requestNodeInfo(nId, c.CopyRequestHeader(c.Request))
				if err != nil {
					return err
				}
				nodeCfgs = append(nodeCfgs, nodeCfg)
				return nil
			}
		}(node.Id))
	}
	err := requestGroup.Wait()
	if err != nil {
		c.ResponseError(err)
		return
	}
	c.JSON(http.StatusOK, NodeConfigTotal{
		Total: len(nodeCfgs),
		Data:  nodeCfgs,
	})
}

func (s *Server) nodeGet(c *wkhttp.Context) {
	nodeCfg := s.getLocalNodeInfo()
	c.JSON(http.StatusOK, nodeCfg)
}

func (s *Server) simpleNodesGet(c *wkhttp.Context) {
	cfgServer := s.clusterEventServer
	cfg := cfgServer.Config()

	leaderId := cfgServer.LeaderId()

	nodeCfgs := make([]*NodeConfig, 0, len(cfg.Nodes))

	for _, node := range cfg.Nodes {
		nodeCfg := NewNodeConfigFromNode(node)
		nodeCfg.IsLeader = wkutil.BoolToInt(leaderId == node.Id)
		nodeCfg.SlotCount = s.getNodeSlotCount(node.Id, cfg)
		nodeCfg.SlotLeaderCount = s.getNodeSlotLeaderCount(node.Id, cfg)
		nodeCfgs = append(nodeCfgs, nodeCfg)
	}
	c.JSON(http.StatusOK, NodeConfigTotal{
		Total: len(nodeCfgs),
		Data:  nodeCfgs,
	})
}

func (s *Server) nodeChannelsGet(c *wkhttp.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		s.Error("id parse error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	limit := wkutil.ParseInt(c.Query("limit"))
	currentPage := wkutil.ParseInt(c.Query("current_page")) // 页码
	channelId := strings.TrimSpace(c.Query("channel_id"))
	channelType := wkutil.ParseUint8(c.Query("channel_type"))

	// running := wkutil.ParseBool(c.Query("running")) // 是否只查询运行中的频道

	if limit <= 0 {
		limit = s.opts.PageSize
	}

	if currentPage <= 0 {
		currentPage = 1
	}

	node := s.clusterEventServer.Node(id)
	if node == nil {
		s.Error("node not found", zap.Uint64("nodeId", id))
		c.ResponseError(errors.New("node not found"))
		return
	}
	if !node.Online {
		s.Error("node offline", zap.Uint64("nodeId", id))
		c.ResponseError(errors.New("node offline"))
		return
	}

	if node.Id != s.opts.NodeId {
		c.Forward(fmt.Sprintf("%s%s", node.ApiServerAddr, c.Request.URL.Path))
		return
	}

	channelClusterConfigs, err := s.opts.DB.SearchChannelClusterConfig(wkdb.ChannelClusterConfigSearchReq{
		ChannelId:   channelId,
		ChannelType: channelType,
		CurrentPage: currentPage,
		Limit:       limit,
	}, func(cfg wkdb.ChannelClusterConfig) bool {
		slotId := s.getSlotId(cfg.ChannelId)
		slot := s.clusterEventServer.Slot(slotId)
		if slot == nil {
			return false
		}

		if slot.Leader != node.Id {
			return false
		}
		return true
	})
	if err != nil {
		s.Error("SearchChannelClusterConfig error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	channelClusterConfigResps := make([]*ChannelClusterConfigResp, 0, len(channelClusterConfigs))

	leaderCfgMap := make(map[uint64][]*channelBase)

	for _, cfg := range channelClusterConfigs {
		slotId := s.getSlotId(cfg.ChannelId)
		slot := s.clusterEventServer.Slot(slotId)
		if slot == nil {
			s.Error("slot not found", zap.Uint32("slotId", slotId))
			c.ResponseError(err)
			return
		}

		resp := NewChannelClusterConfigRespFromClusterConfig(slot.Leader, slot.Id, cfg)

		if cfg.LeaderId == s.opts.NodeId {
			if s.channelManager.exist(cfg.ChannelId, cfg.ChannelType) {
				resp.Active = 1
				resp.ActiveFormat = "运行中"
			} else {
				resp.Active = 0
				resp.ActiveFormat = "未运行"
			}
			shardNo := wkutil.ChannelToKey(cfg.ChannelId, cfg.ChannelType)
			lastMsgSeq, lastAppendTime, err := s.opts.MessageLogStorage.LastIndexAndAppendTime(shardNo)
			if err != nil {
				s.Error("LastIndexAndAppendTime error", zap.Error(err))
				c.ResponseError(err)
				return
			}
			resp.LastMessageSeq = lastMsgSeq
			if lastAppendTime > 0 {
				resp.LastAppendTime = myUptime(time.Since(time.Unix(int64(lastAppendTime/1e9), 0)))
			}
		} else {
			leaderCfgMap[cfg.LeaderId] = append(leaderCfgMap[cfg.LeaderId], &channelBase{
				ChannelId:   cfg.ChannelId,
				ChannelType: cfg.ChannelType,
			})
		}
		channelClusterConfigResps = append(channelClusterConfigResps, resp)

	}

	statusResps := make([]*channelStatusResp, 0, len(leaderCfgMap))

	if len(leaderCfgMap) > 0 {
		timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ReqTimeout)
		defer cancel()
		requestGroup, _ := errgroup.WithContext(timeoutCtx)

		statusRespLock := sync.Mutex{}

		for nodeId, channels := range leaderCfgMap {
			if !s.NodeIsOnline(nodeId) {
				continue
			}
			requestGroup.Go(func(nId uint64, chs []*channelBase) func() error {

				return func() error {

					results, err := s.requestChannelStatus(nId, chs, c.CopyRequestHeader(c.Request))
					if err != nil {
						return err
					}
					statusRespLock.Lock()
					statusResps = append(statusResps, results...)
					statusRespLock.Unlock()

					return nil
				}
			}(nodeId, channels))
		}

		err = requestGroup.Wait()
		if err != nil {
			s.Error("requestChannelStatus error", zap.Error(err))
			c.ResponseError(err)
			return
		}

	}

	for _, channelClusterConfigResp := range channelClusterConfigResps {
		for _, statusResp := range statusResps {
			if channelClusterConfigResp.ChannelId == statusResp.ChannelId && channelClusterConfigResp.ChannelType == statusResp.ChannelType {
				channelClusterConfigResp.Active = statusResp.Running

				if statusResp.Running == 1 {
					channelClusterConfigResp.ActiveFormat = "运行中"
				} else {
					channelClusterConfigResp.ActiveFormat = "未运行"
				}
				channelClusterConfigResp.LastMessageSeq = statusResp.LastMsgSeq
				channelClusterConfigResp.LastAppendTime = myUptime(time.Since(time.Unix(int64(statusResp.LastMsgTime/1e9), 0)))
				break
			}
		}
	}

	// fmt.Println("start---------->3-->", len(channelClusterConfigs))
	// total, err := s.opts.DB.GetTotalChannelClusterConfigCount()
	// if err != nil {
	// 	s.Error("GetTotalChannelClusterConfigCount error", zap.Error(err))
	// 	c.ResponseError(err)
	// 	return

	// }

	c.JSON(http.StatusOK, ChannelClusterConfigRespTotal{
		Running: s.channelManager.channelCount(),
		Data:    channelClusterConfigResps,
	})
}

func (s *Server) requestChannelStatus(nodeId uint64, channels []*channelBase, headers map[string]string) ([]*channelStatusResp, error) {
	node := s.clusterEventServer.Node(nodeId)
	if node == nil {
		return nil, fmt.Errorf("node not found, nodeId:%d", nodeId)
	}
	fullUrl := fmt.Sprintf("%s%s", node.ApiServerAddr, s.formatPath("/channel/status"))
	resp, err := network.Post(fullUrl, []byte(wkutil.ToJSON(map[string]interface{}{
		"channels": channels,
	})), headers)
	if err != nil {
		s.Error("requestChannelStatus error", zap.Error(err))
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		s.Error("requestChannelStatus failed", zap.Int("statusCode", resp.StatusCode))
		return nil, errors.New("requestChannelStatus failed")
	}

	var statusResp []*channelStatusResp
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &statusResp)
	if err != nil {
		return nil, err
	}
	return statusResp, nil
}

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
	shardNo := SlotIdToKey(slotId)

	slot := s.clusterEventServer.Slot(slotId)
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
	appliedIdx, err := s.opts.SlotLogStorage.AppliedIndex(shardNo)
	if err != nil {
		s.Error("getAppliedIndex error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	lastIdx, err := s.opts.SlotLogStorage.LastIndex(shardNo)
	if err != nil {
		s.Error("LastIndex error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	cfg := NewSlotClusterConfigRespFromClusterConfig(appliedIdx, lastIdx, leaderLogMaxIndex, slot)
	c.JSON(http.StatusOK, cfg)

}

func (s *Server) slotChannelsGet(c *wkhttp.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		s.Error("id parse error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	slotId := uint32(id)

	channels, err := s.opts.ChannelClusterStorage.GetWithSlotId(slotId)
	if err != nil {
		s.Error("GetChannels error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	channelClusterConfigResps := make([]*ChannelClusterConfigResp, 0, len(channels))
	for _, cfg := range channels {
		if !wkutil.ArrayContainsUint64(cfg.Replicas, s.opts.NodeId) {
			continue
		}
		slot := s.clusterEventServer.Slot(slotId)
		if slot == nil {
			s.Error("slot not found", zap.Uint32("slotId", slotId))
			c.ResponseError(err)
			return
		}
		shardNo := wkutil.ChannelToKey(cfg.ChannelId, cfg.ChannelType)
		lastMsgSeq, lastAppendTime, err := s.opts.MessageLogStorage.LastIndexAndAppendTime(shardNo)
		if err != nil {
			s.Error("LastIndexAndAppendTime error", zap.Error(err))
			c.ResponseError(err)
			return
		}

		resp := NewChannelClusterConfigRespFromClusterConfig(slot.Leader, slot.Id, cfg)
		resp.LastMessageSeq = lastMsgSeq
		if lastAppendTime > 0 {
			resp.LastAppendTime = wkutil.ToyyyyMMddHHmm(time.Unix(int64(lastAppendTime/1e9), 0))
		}
		channelClusterConfigResps = append(channelClusterConfigResps, resp)
	}

	c.JSON(http.StatusOK, ChannelClusterConfigRespTotal{
		Total: len(channelClusterConfigResps),
		Data:  channelClusterConfigResps,
	})
}

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

	slot := s.clusterEventServer.Slot(id)
	if slot == nil {
		s.Error("slot not found", zap.Uint32("slotId", id))
		c.ResponseError(errors.New("slot not found"))
		return
	}

	node := s.clusterEventServer.Node(slot.Leader)
	if node == nil {
		s.Error("leader not found", zap.Uint64("leaderId", slot.Leader))
		c.ResponseError(errors.New("leader not found"))
		return
	}

	if slot.Leader != s.opts.NodeId {
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

	err = s.clusterEventServer.ProposeMigrateSlot(id, req.MigrateFrom, req.MigrateTo)
	if err != nil {
		s.Error("slotMigrate: ProposeMigrateSlot error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.ResponseOK()

}

func (s *Server) clusterInfoGet(c *wkhttp.Context) {

	leaderId := s.clusterEventServer.LeaderId()
	if leaderId == 0 {
		c.ResponseError(errors.New("leader not found"))
		return
	}
	if leaderId != s.opts.NodeId {
		leaderNode := s.clusterEventServer.Node(leaderId)
		c.Forward(fmt.Sprintf("%s%s", leaderNode.ApiServerAddr, c.Request.URL.Path))
		return
	}
	cfg := s.clusterEventServer.Config()
	c.JSON(http.StatusOK, cfg)
}

func (s *Server) allSlotsGet(c *wkhttp.Context) {
	leaderId := s.clusterEventServer.LeaderId()
	if leaderId == 0 {
		c.ResponseError(errors.New("leader not found"))
		return
	}
	if leaderId != s.opts.NodeId {
		leaderNode := s.clusterEventServer.Node(leaderId)
		c.Forward(fmt.Sprintf("%s%s", leaderNode.ApiServerAddr, c.Request.URL.Path))
		return
	}

	clusterCfg := s.clusterEventServer.Config()
	resps := make([]*SlotResp, 0, len(clusterCfg.Slots))

	nodeSlotsMap := make(map[uint64][]uint32)
	for _, st := range clusterCfg.Slots {
		if st.Leader == s.opts.NodeId {
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

		if !s.clusterEventServer.NodeOnline(nodeId) {
			slotResps, err := s.getSlotInfoForLeaderOffline(nodeId, slotIds)
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
	slot := s.clusterEventServer.Slot(slotId)
	if slot == nil {
		return nil, errors.New("slot not found")
	}
	count, err := s.opts.ChannelClusterStorage.GetCountWithSlotId(slotId)
	if err != nil {
		return nil, err
	}
	shardNo := SlotIdToKey(slotId)
	lastIdx, err := s.opts.SlotLogStorage.LastIndex(shardNo)
	if err != nil {
		return nil, err
	}
	resp := NewSlotResp(slot, count)
	resp.LogIndex = lastIdx
	return resp, nil
}

func (s *Server) channelClusterConfigGet(c *wkhttp.Context) {
	channelId := c.Param("channel_id")
	channelTypeStr := c.Param("channel_type")

	channelTypeI64, err := strconv.ParseUint(channelTypeStr, 10, 8)
	if err != nil {
		s.Error("channelTypeStr parse error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	channelType := uint8(channelTypeI64)
	cfg, err := s.loadOnlyChannelClusterConfig(channelId, channelType)
	if err != nil {
		s.Error("loadChannelClusterConfig error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	slotId := s.getSlotId(channelId)
	slot := s.clusterEventServer.Slot(slotId)
	if slot == nil {
		s.Error("slot not found", zap.Uint32("slotId", slotId))
		c.ResponseError(err)
		return

	}
	resp := NewChannelClusterConfigRespFromClusterConfig(slot.Leader, slotId, cfg)
	shardNo := wkutil.ChannelToKey(channelId, channelType)
	lastMsgSeq, lastAppendTime, err := s.opts.MessageLogStorage.LastIndexAndAppendTime(shardNo)
	if err != nil {
		s.Error("LastIndexAndAppendTime error", zap.Error(err))
		c.ResponseError(err)
		return

	}
	resp.LastMessageSeq = lastMsgSeq
	if lastAppendTime > 0 {
		resp.LastAppendTime = wkutil.ToyyyyMMddHHmm(time.Unix(int64(lastAppendTime/1e9), 0))
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) messageSearch(c *wkhttp.Context) {

	// 搜索条件
	limit := wkutil.ParseInt(c.Query("limit"))
	nodeId := wkutil.ParseUint64(c.Query("node_id"))
	fromUid := strings.TrimSpace(c.Query("from_uid"))
	channelId := strings.TrimSpace(c.Query("channel_id"))
	channelType := wkutil.ParseUint8(c.Query("channel_type"))
	offsetMessageId := wkutil.ParseInt64(c.Query("offset_message_id"))    // 偏移的messageId
	offsetMessageSeq := wkutil.ParseUint64(c.Query("offset_message_seq")) // 偏移的messageSeq（通过频道筛选并分页的时候需要传此值）
	pre := wkutil.ParseInt(c.Query("pre"))                                // 是否向前搜索
	payloadStr := strings.TrimSpace(c.Query("payload"))                   // base64编码的消息内容
	messageId := wkutil.ParseInt64(c.Query("message_id"))
	clientMsgNo := strings.TrimSpace(c.Query("client_msg_no"))

	// 解密payload
	var payload []byte
	var err error
	// base64解码payloadStr
	if strings.TrimSpace(payloadStr) != "" {
		payload, err = wkutil.Base64Decode(payloadStr)
		if err != nil {
			s.Error("base64 decode error", zap.Error(err), zap.String("payloadStr", payloadStr))
			c.ResponseError(err)
			return
		}
	}

	if nodeId > 0 && nodeId != s.opts.NodeId {
		node := s.clusterEventServer.Node(nodeId)
		c.Forward(fmt.Sprintf("%s%s", node.ApiServerAddr, c.Request.URL.Path))
		return
	}

	if limit <= 0 {
		limit = s.opts.PageSize
	}

	if nodeId > 0 && nodeId != s.opts.NodeId {
		s.Error("nodeId not is self", zap.Uint64("nodeId", nodeId))
		c.ResponseError(errors.New("nodeId not is self"))
		return
	}

	// 搜索本地消息
	var searchLocalMessage = func() ([]*messageResp, error) {
		messages, err := s.opts.DB.SearchMessages(wkdb.MessageSearchReq{
			MessageId:        messageId,
			FromUid:          fromUid,
			Limit:            limit,
			ChannelId:        channelId,
			ChannelType:      channelType,
			OffsetMessageId:  offsetMessageId,
			OffsetMessageSeq: offsetMessageSeq,
			Pre:              pre == 1,
			Payload:          payload,
			ClientMsgNo:      clientMsgNo,
		})
		if err != nil {
			s.Error("查询消息失败！", zap.Error(err))
			return nil, err
		}

		resps := make([]*messageResp, 0, len(messages))
		for _, message := range messages {
			resps = append(resps, newMessageResp(message))
		}
		return resps, nil
	}

	if nodeId == s.opts.NodeId {
		messages, err := searchLocalMessage()
		if err != nil {
			s.Error("search local message failed", zap.Error(err))
			c.ResponseError(err)
			return
		}
		c.JSON(http.StatusOK, messageRespTotal{
			Data: messages,
		})
		return
	}

	nodes := s.clusterEventServer.Nodes()

	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ReqTimeout)
	defer cancel()

	var messages = make([]*messageResp, 0)
	var messageLock sync.Mutex
	requestGroup, _ := errgroup.WithContext(timeoutCtx)
	for _, node := range nodes {
		if node.Id == s.opts.NodeId {
			localMessages, err := searchLocalMessage()
			if err != nil {
				s.Error("search local message failed", zap.Error(err))
				c.ResponseError(err)
				return
			}
			for _, localMsg := range localMessages {
				exist := false
				for _, msg := range messages {
					if localMsg.MessageId == msg.MessageId {
						exist = true
						break
					}
				}
				if !exist {
					messages = append(messages, localMsg)
				}
			}
			continue
		}
		if !node.Online {
			continue
		}

		requestGroup.Go(func(nId uint64, queryValues url.Values) func() error {
			return func() error {
				queryMap := map[string]string{}
				for key, values := range queryValues {
					if len(values) > 0 {
						queryMap[key] = values[0]
					}
				}
				result, err := s.requestNodeMessageSearch(c.Request.URL.Path, nId, queryMap, c.CopyRequestHeader(c.Request))
				if err != nil {
					return err
				}
				messageLock.Lock()
				for _, resultMsg := range result.Data {
					exist := false
					for _, msg := range messages {
						if resultMsg.MessageId == msg.MessageId {
							exist = true
							break
						}
					}
					if !exist {
						messages = append(messages, resultMsg)
					}
				}
				messageLock.Unlock()
				return nil
			}
		}(node.Id, c.Request.URL.Query()))
	}

	err = requestGroup.Wait()
	if err != nil {
		s.Error("search message failed", zap.Error(err))
		c.ResponseError(err)
		return
	}

	sort.Slice(messages, func(i, j int) bool {

		return messages[i].MessageId > messages[j].MessageId
	})

	if len(messages) > limit {
		if pre == 1 {
			messages = messages[len(messages)-limit:]
		} else {
			messages = messages[:limit]
		}
	}
	count, err := s.opts.DB.GetTotalMessageCount()
	if err != nil {
		s.Error("GetTotalMessageCount error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	c.JSON(http.StatusOK, messageRespTotal{
		Data:  messages,
		Total: count,
	})
}

func (s *Server) requestNodeMessageSearch(path string, nodeId uint64, queryMap map[string]string, headers map[string]string) (*messageRespTotal, error) {
	node := s.clusterEventServer.Node(nodeId)
	if node == nil {
		s.Error("requestNodeMessageSearch failed, node not found", zap.Uint64("nodeId", nodeId))
		return nil, errors.New("node not found")
	}
	fullUrl := fmt.Sprintf("%s%s", node.ApiServerAddr, path)
	queryMap["node_id"] = fmt.Sprintf("%d", nodeId)
	resp, err := network.Get(fullUrl, queryMap, headers)
	if err != nil {
		return nil, err
	}
	err = handlerIMError(resp)
	if err != nil {
		return nil, err
	}

	var msgResp *messageRespTotal
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &msgResp)
	if err != nil {
		return nil, err
	}
	return msgResp, nil
}

func (s *Server) getSlotMaxLogIndex(slotId uint32) (uint64, error) {

	slot := s.clusterEventServer.Slot(slotId)
	if slot == nil {
		return 0, errors.New("slot not found")
	}

	if slot.Leader == s.opts.NodeId {
		shardNo := SlotIdToKey(slotId)
		lastIdx, err := s.opts.SlotLogStorage.LastIndex(shardNo)
		if err != nil {
			return 0, err
		}
		return lastIdx, nil
	}

	slotLogResp, err := s.nodeManager.requestSlotLogInfo(s.cancelCtx, slot.Leader, &SlotLogInfoReq{
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

func (s *Server) channelSearch(c *wkhttp.Context) {
	// 搜索条件
	limit := wkutil.ParseInt(c.Query("limit"))
	nodeId := wkutil.ParseUint64(c.Query("node_id"))
	channelId := strings.TrimSpace(c.Query("channel_id"))
	channelType := wkutil.ParseUint8(c.Query("channel_type"))
	currentPage := wkutil.ParseInt(c.Query("current_page")) // 页码

	if limit <= 0 {
		limit = s.opts.PageSize
	}

	var ban *bool
	if strings.TrimSpace(c.Query("ban")) != "" {
		banI := wkutil.ParseInt(c.Query("ban")) // 是否禁用
		*ban = wkutil.IntToBool(banI)
	}

	var disband *bool
	if strings.TrimSpace(c.Query("disband")) != "" {
		disbandI := wkutil.ParseInt(c.Query("disband")) // 是否解散
		*disband = wkutil.IntToBool(disbandI)
	}

	var subscriberCountGte *int
	if strings.TrimSpace(c.Query("subscriber_count_gte")) != "" {
		subscriberCountGteI := wkutil.ParseInt(c.Query("subscriber_count_gte")) // 订阅者数量大于等于
		*subscriberCountGte = subscriberCountGteI
	}

	var subscriberCountLte *int
	if strings.TrimSpace(c.Query("subscriber_count_lte")) != "" {
		subscriberCountLteI := wkutil.ParseInt(c.Query("subscriber_count_lte")) // 订阅者数量小于等于
		*subscriberCountLte = subscriberCountLteI
	}

	var denylistCountGte *int
	if strings.TrimSpace(c.Query("denylist_count_gte")) != "" {
		denylistCountGteI := wkutil.ParseInt(c.Query("denylist_count_gte")) // 黑名单数量大于等于
		*denylistCountGte = denylistCountGteI
	}

	var denylistCountLte *int
	if strings.TrimSpace(c.Query("denylist_count_lte")) != "" {
		denylistCountLteI := wkutil.ParseInt(c.Query("denylist_count_lte")) // 黑名单数量小于等于
		*denylistCountLte = denylistCountLteI
	}

	var allowlistCountGte *int
	if strings.TrimSpace(c.Query("allowlist_count_gte")) != "" {
		allowlistCountGteI := wkutil.ParseInt(c.Query("allowlist_count_gte")) // 白名单数量大于等于
		*allowlistCountGte = allowlistCountGteI
	}

	var allowlistCountLte *int
	if strings.TrimSpace(c.Query("allowlist_count_lte")) != "" {
		allowlistCountLteI := wkutil.ParseInt(c.Query("allowlist_count_lte")) // 白名单数量小于等于
		*allowlistCountLte = allowlistCountLteI
	}

	if currentPage <= 0 {
		currentPage = 1
	}

	searchLocalChannelInfos := func() ([]*channelInfoResp, error) {
		channelInfos, err := s.opts.DB.SearchChannels(wkdb.ChannelSearchReq{
			ChannelId:          channelId,
			ChannelType:        channelType,
			Ban:                ban,
			Disband:            disband,
			SubscriberCountGte: subscriberCountGte,
			SubscriberCountLte: subscriberCountLte,
			DenylistCountGte:   denylistCountGte,
			DenylistCountLte:   denylistCountLte,
			AllowlistCountGte:  allowlistCountGte,
			AllowlistCountLte:  allowlistCountLte,
			CurrentPage:        currentPage,
			Limit:              limit,
		})
		if err != nil {
			s.Error("search channel failed", zap.Error(err))
			return nil, err
		}
		resps := make([]*channelInfoResp, 0, len(channelInfos))
		for _, channelInfo := range channelInfos {
			slotId := s.getSlotId(channelInfo.ChannelId)
			resps = append(resps, newChannelInfoResp(channelInfo, slotId))
		}
		return resps, nil
	}

	if nodeId == s.opts.NodeId {
		channelInfoResps, err := searchLocalChannelInfos()
		if err != nil {
			s.Error("search local channel info failed", zap.Error(err))
			c.ResponseError(err)
			return
		}
		c.JSON(http.StatusOK, channelInfoRespTotal{
			Data: channelInfoResps,
		})
		return
	}

	nodes := s.clusterEventServer.Nodes()

	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ReqTimeout)
	defer cancel()

	var channelInfoResps = make([]*channelInfoResp, 0)
	var channelInfoRespLock sync.Mutex
	requestGroup, _ := errgroup.WithContext(timeoutCtx)
	for _, node := range nodes {
		if node.Id == s.opts.NodeId {
			localChannelInfoResps, err := searchLocalChannelInfos()
			if err != nil {
				s.Error("search local channel info failed", zap.Error(err))
				c.ResponseError(err)
				return
			}
			for _, localChannelInfoResp := range localChannelInfoResps {
				exist := false
				for _, channelInfoResp := range channelInfoResps {
					if localChannelInfoResp.ChannelId == channelInfoResp.ChannelId && localChannelInfoResp.ChannelType == channelInfoResp.ChannelType {
						exist = true
						break
					}
				}
				if !exist {
					channelInfoResps = append(channelInfoResps, localChannelInfoResp)
				}
			}
			continue
		}
		if !node.Online {
			continue
		}
		requestGroup.Go(func(nId uint64, queryValues url.Values) func() error {
			return func() error {
				queryMap := map[string]string{}
				for key, values := range queryValues {
					if len(values) > 0 {
						queryMap[key] = values[0]
					}
				}
				result, err := s.requestNodeChannelSearch(c.Request.URL.Path, nId, queryMap, c.CopyRequestHeader(c.Request))
				if err != nil {
					return err
				}
				channelInfoRespLock.Lock()
				for _, resultChannelInfo := range result.Data {
					exist := false
					for _, channelInfoResp := range channelInfoResps {
						if resultChannelInfo.ChannelId == channelInfoResp.ChannelId && resultChannelInfo.ChannelType == channelInfoResp.ChannelType {
							exist = true
							break
						}
					}
					if !exist {
						channelInfoResps = append(channelInfoResps, resultChannelInfo)
					}
				}
				channelInfoRespLock.Unlock()
				return nil
			}
		}(node.Id, c.Request.URL.Query()))
	}

	err := requestGroup.Wait()
	if err != nil {
		s.Error("search message failed", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if len(channelInfoResps) > limit {
		channelInfoResps = channelInfoResps[:limit]
	}

	count, err := s.opts.DB.GetTotalChannelCount()
	if err != nil {
		s.Error("GetTotalChannelCount error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.JSON(http.StatusOK, channelInfoRespTotal{
		Data:  channelInfoResps,
		Total: count,
	})

}

func (s *Server) requestNodeChannelSearch(path string, nodeId uint64, queryMap map[string]string, headers map[string]string) (*channelInfoRespTotal, error) {
	node := s.clusterEventServer.Node(nodeId)
	if node == nil {
		s.Error("requestNodeChannelSearch failed, node not found", zap.Uint64("nodeId", nodeId))
		return nil, errors.New("node not found")
	}
	fullUrl := fmt.Sprintf("%s%s", node.ApiServerAddr, path)
	queryMap["node_id"] = fmt.Sprintf("%d", nodeId)
	resp, err := network.Get(fullUrl, queryMap, headers)
	if err != nil {
		return nil, err
	}
	err = handlerIMError(resp)
	if err != nil {
		return nil, err
	}

	var channelInfoResp *channelInfoRespTotal
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &channelInfoResp)
	if err != nil {
		return nil, err
	}
	return channelInfoResp, nil
}

func (s *Server) subscribersGet(c *wkhttp.Context) {
	channelId := c.Param("channel_id")
	channelType := wkutil.ParseUint8(c.Param("channel_type"))

	leaderNode, err := s.LeaderOfChannelForRead(channelId, channelType)
	if err != nil {
		s.Error("LeaderOfChannelForRead error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if leaderNode.Id != s.opts.NodeId {
		c.Forward(fmt.Sprintf("%s%s", leaderNode.ApiServerAddr, c.Request.URL.Path))
		return
	}
	subscribers, err := s.opts.DB.GetSubscribers(channelId, channelType)
	if err != nil {
		s.Error("GetSubscribers error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	c.JSON(http.StatusOK, subscribers)
}

func (s *Server) denylistGet(c *wkhttp.Context) {
	channelId := c.Param("channel_id")
	channelType := wkutil.ParseUint8(c.Param("channel_type"))

	leaderNode, err := s.LeaderOfChannelForRead(channelId, channelType)
	if err != nil {
		s.Error("LeaderOfChannelForRead error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if leaderNode.Id != s.opts.NodeId {
		c.Forward(fmt.Sprintf("%s%s", leaderNode.ApiServerAddr, c.Request.URL.Path))
		return
	}
	denylist, err := s.opts.DB.GetDenylist(channelId, channelType)
	if err != nil {
		s.Error("GetDenylist error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	c.JSON(http.StatusOK, denylist)
}

func (s *Server) allowlistGet(c *wkhttp.Context) {
	channelId := c.Param("channel_id")
	channelType := wkutil.ParseUint8(c.Param("channel_type"))

	leaderNode, err := s.LeaderOfChannelForRead(channelId, channelType)
	if err != nil {
		s.Error("LeaderOfChannelForRead error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if leaderNode.Id != s.opts.NodeId {
		c.Forward(fmt.Sprintf("%s%s", leaderNode.ApiServerAddr, c.Request.URL.Path))
		return
	}
	allowlist, err := s.opts.DB.GetAllowlist(channelId, channelType)
	if err != nil {
		s.Error("GetAllowlist error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	c.JSON(http.StatusOK, allowlist)
}

func (s *Server) userSearch(c *wkhttp.Context) {
	// 搜索条件
	limit := wkutil.ParseInt(c.Query("limit"))
	offsetId := wkutil.ParseUint64(c.Query("offset_id")) // 偏移的id
	uid := strings.TrimSpace(c.Query("uid"))
	nodeId := wkutil.ParseUint64(c.Query("node_id"))
	pre := wkutil.ParseInt(c.Query("pre")) // 是否向前搜索

	if limit <= 0 {
		limit = s.opts.PageSize
	}

	var searchLocalUsers = func() (userRespTotal, error) {
		users, err := s.opts.DB.SearchUser(wkdb.UserSearchReq{
			Uid:      uid,
			Limit:    limit,
			OffsetId: offsetId,
			Pre:      pre == 1,
		})
		if err != nil {
			s.Error("search user failed", zap.Error(err))
			return userRespTotal{}, err
		}

		userResps := make([]*userResp, 0, len(users))
		for _, user := range users {
			userResps = append(userResps, newUserResp(user))
		}

		count, err := s.opts.DB.GetTotalUserCount()
		if err != nil {
			s.Error("GetTotalUserCount error", zap.Error(err))
			return userRespTotal{}, err
		}
		return userRespTotal{
			Data:  userResps,
			Total: count,
		}, nil
	}

	if nodeId == s.opts.NodeId {
		result, err := searchLocalUsers()
		if err != nil {
			s.Error("search local users  failed", zap.Error(err))
			c.ResponseError(err)
			return
		}
		c.JSON(http.StatusOK, result)
		return
	}

	nodes := s.clusterEventServer.Nodes()
	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ReqTimeout)
	defer cancel()

	requestGroup, _ := errgroup.WithContext(timeoutCtx)

	userResps := make([]*userResp, 0)
	for _, node := range nodes {

		if node.Id == s.opts.NodeId {
			result, err := searchLocalUsers()
			if err != nil {
				s.Error("search local users  failed", zap.Error(err))
				c.ResponseError(err)
				return
			}
			userResps = append(userResps, result.Data...)
			continue
		}

		if !s.NodeIsOnline(node.Id) {
			continue
		}
		requestGroup.Go(func(nId uint64, queryValues url.Values) func() error {
			return func() error {
				queryMap := map[string]string{}
				for key, values := range queryValues {
					if len(values) > 0 {
						queryMap[key] = values[0]
					}
				}
				result, err := s.requestUserSearch(c.Request.URL.Path, nId, queryMap, c.CopyRequestHeader(c.Request))
				if err != nil {
					return err
				}
				userResps = append(userResps, result.Data...)
				return nil
			}
		}(node.Id, c.Request.URL.Query()))

	}

	err := requestGroup.Wait()
	if err != nil {
		s.Error("search user request failed", zap.Error(err))
		c.ResponseError(err)
		return
	}

	// 移除userResps中重复的数据
	userRespMap := make(map[string]*userResp)
	for _, userResp := range userResps {
		userRespMap[userResp.Uid] = userResp
	}

	userResps = make([]*userResp, 0, len(userRespMap))
	for _, userResp := range userRespMap {
		userResps = append(userResps, userResp)
	}

	sort.Slice(userResps, func(i, j int) bool {
		return userResps[i].Id > userResps[j].Id
	})

	if len(userResps) > limit {
		if pre == 1 {
			userResps = userResps[len(userResps)-limit:]
		} else {
			userResps = userResps[:limit]
		}
	}

	nextId := ""
	preId := ""
	if len(userResps) > 0 {
		nextId = wkutil.Uint64ToString(userResps[len(userResps)-1].Id)
		preId = wkutil.Uint64ToString(userResps[0].Id)
	}

	userCount, err := s.opts.DB.GetTotalUserCount()
	if err != nil {
		s.Error("GetTotalUserCount error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.JSON(http.StatusOK, userRespTotal{
		Data:  userResps,
		Total: userCount,
		Next:  nextId,
		Pre:   preId,
	})
}

func (s *Server) requestUserSearch(path string, nodeId uint64, queryMap map[string]string, headers map[string]string) (*userRespTotal, error) {
	node := s.clusterEventServer.Node(nodeId)
	if node == nil {
		s.Error("requestUserSearch failed, node not found", zap.Uint64("nodeId", nodeId))
		return nil, errors.New("node not found")
	}
	fullUrl := fmt.Sprintf("%s%s", node.ApiServerAddr, path)
	queryMap["node_id"] = fmt.Sprintf("%d", nodeId)
	resp, err := network.Get(fullUrl, queryMap, headers)
	if err != nil {
		return nil, err
	}
	err = handlerIMError(resp)
	if err != nil {
		return nil, err
	}

	var userRespTotal *userRespTotal
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &userRespTotal)
	if err != nil {
		return nil, err
	}
	return userRespTotal, nil
}

func (s *Server) deviceSearch(c *wkhttp.Context) {
	// 搜索条件
	limit := wkutil.ParseInt(c.Query("limit"))
	currentPage := wkutil.ParseInt(c.Query("current_page")) // 页码
	uid := strings.TrimSpace(c.Query("uid"))
	deviceFlag := wkutil.ParseUint64(strings.TrimSpace(c.Query("device_flag")))
	nodeId := wkutil.ParseUint64(c.Query("node_id"))

	if currentPage <= 0 {
		currentPage = 1
	}
	if limit <= 0 {
		limit = s.opts.PageSize
	}

	var searchLocalDevice = func() (*deviceRespTotal, error) {
		devices, err := s.opts.DB.SearchDevice(wkdb.DeviceSearchReq{
			Uid:         uid,
			DeviceFlag:  deviceFlag,
			Limit:       limit,
			CurrentPage: currentPage,
		})
		if err != nil {
			s.Error("search device failed", zap.Error(err))
			return nil, err
		}
		deviceResps := make([]*deviceResp, 0, len(devices))
		for _, device := range devices {
			deviceResps = append(deviceResps, newDeviceResp(device))
		}
		count, err := s.opts.DB.GetTotalDeviceCount()
		if err != nil {
			s.Error("GetTotalDeviceCount error", zap.Error(err))
			return nil, err
		}

		return &deviceRespTotal{
			Total: count,
			Data:  deviceResps,
		}, nil

	}

	if nodeId == s.opts.NodeId {
		result, err := searchLocalDevice()
		if err != nil {
			s.Error("search local device  failed", zap.Error(err))
			c.ResponseError(err)
			return
		}
		c.JSON(http.StatusOK, result)
		return
	}

	nodes := s.clusterEventServer.Nodes()
	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ReqTimeout)
	defer cancel()

	requestGroup, _ := errgroup.WithContext(timeoutCtx)

	deviceResps := make([]*deviceResp, 0)
	for _, node := range nodes {
		if node.Id == s.opts.NodeId {
			result, err := searchLocalDevice()
			if err != nil {
				s.Error("search local users  failed", zap.Error(err))
				c.ResponseError(err)
				return
			}
			deviceResps = append(deviceResps, result.Data...)
			continue
		}

		if !s.NodeIsOnline(node.Id) {
			continue
		}

		requestGroup.Go(func(nId uint64, queryValues url.Values) func() error {
			return func() error {
				queryMap := map[string]string{}
				for key, values := range queryValues {
					if len(values) > 0 {
						queryMap[key] = values[0]
					}
				}
				result, err := s.requestDeviceSearch(c.Request.URL.Path, nId, queryMap, c.CopyRequestHeader(c.Request))
				if err != nil {
					return err
				}
				deviceResps = append(deviceResps, result.Data...)
				return nil
			}
		}(node.Id, c.Request.URL.Query()))

	}

	err := requestGroup.Wait()
	if err != nil {
		s.Error("search device request failed", zap.Error(err))
		c.ResponseError(err)
		return
	}

	// 移除deviceResps中重复的数据
	deviceRespMap := make(map[string]*deviceResp)
	for _, deviceResp := range deviceResps {
		deviceRespMap[fmt.Sprintf("%s-%d", deviceResp.Uid, deviceResp.DeviceFlag)] = deviceResp
	}

	deviceResps = make([]*deviceResp, 0, len(deviceRespMap))
	for _, deviceResp := range deviceRespMap {
		deviceResps = append(deviceResps, deviceResp)
	}

	c.JSON(http.StatusOK, deviceRespTotal{
		Data:  deviceResps,
		Total: 0,
	})

}

func (s *Server) requestDeviceSearch(path string, nodeId uint64, queryMap map[string]string, headers map[string]string) (*deviceRespTotal, error) {
	node := s.clusterEventServer.Node(nodeId)
	if node == nil {
		s.Error("requestDeviceSearch failed, node not found", zap.Uint64("nodeId", nodeId))
		return nil, errors.New("node not found")
	}
	fullUrl := fmt.Sprintf("%s%s", node.ApiServerAddr, path)
	queryMap["node_id"] = fmt.Sprintf("%d", nodeId)
	resp, err := network.Get(fullUrl, queryMap, headers)
	if err != nil {
		return nil, err
	}
	err = handlerIMError(resp)
	if err != nil {
		return nil, err
	}

	var deviceRespTotal *deviceRespTotal
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &deviceRespTotal)
	if err != nil {
		return nil, err
	}
	return deviceRespTotal, nil
}

func (s *Server) conversationSearch(c *wkhttp.Context) {
	// 搜索条件
	limit := wkutil.ParseInt(c.Query("limit"))
	currentPage := wkutil.ParseInt(c.Query("current_page")) // 页码
	uid := strings.TrimSpace(c.Query("uid"))
	nodeId := wkutil.ParseUint64(c.Query("node_id"))

	if currentPage <= 0 {
		currentPage = 1
	}
	if limit <= 0 {
		limit = s.opts.PageSize
	}

	var searchLocalConversations = func() (*conversationRespTotal, error) {
		conversations, err := s.opts.DB.SearchConversation(wkdb.ConversationSearchReq{
			Uid:         uid,
			Limit:       limit,
			CurrentPage: currentPage,
		})
		if err != nil {
			s.Error("search conversation failed", zap.Error(err))
			return nil, err
		}

		conversationResps := make([]*conversationResp, 0, len(conversations))

		for _, conversation := range conversations {
			lastMsgSeq, _, err := s.opts.DB.GetChannelLastMessageSeq(conversation.ChannelId, conversation.ChannelType)
			if err != nil {
				s.Error("GetChannelLastMessageSeq error", zap.Error(err))
				return nil, err
			}
			resp := newConversationResp(conversation)
			resp.LastMsgSeq = lastMsgSeq
			conversationResps = append(conversationResps, resp)
		}
		count, err := s.opts.DB.GetTotalSessionCount()
		if err != nil {
			s.Error("GetTotalConversationCount error", zap.Error(err))
			return nil, err
		}
		return &conversationRespTotal{
			Data:  conversationResps,
			Total: count,
		}, nil
	}

	if nodeId == s.opts.NodeId {
		result, err := searchLocalConversations()
		if err != nil {
			s.Error("search local conversation  failed", zap.Error(err))
			c.ResponseError(err)
			return
		}
		c.JSON(http.StatusOK, result)
		return
	}

	nodes := s.clusterEventServer.Nodes()
	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ReqTimeout)
	defer cancel()

	requestGroup, _ := errgroup.WithContext(timeoutCtx)

	conversationResps := make([]*conversationResp, 0)
	for _, node := range nodes {
		if node.Id == s.opts.NodeId {
			result, err := searchLocalConversations()
			if err != nil {
				s.Error("search local conversation  failed", zap.Error(err))
				c.ResponseError(err)
				return
			}
			conversationResps = append(conversationResps, result.Data...)
			continue
		}

		if !s.NodeIsOnline(node.Id) {
			continue
		}

		requestGroup.Go(func(nId uint64, queryValues url.Values) func() error {
			return func() error {
				queryMap := map[string]string{}
				for key, values := range queryValues {
					if len(values) > 0 {
						queryMap[key] = values[0]
					}
				}
				result, err := s.requestConversationSearch(c.Request.URL.Path, nId, queryMap, c.CopyRequestHeader(c.Request))
				if err != nil {
					return err
				}
				conversationResps = append(conversationResps, result.Data...)
				return nil
			}
		}(node.Id, c.Request.URL.Query()))

	}

	err := requestGroup.Wait()
	if err != nil {
		s.Error("search conversation request failed", zap.Error(err))
		c.ResponseError(err)
		return
	}

	// 移除conversationResps中重复的数据
	conversationRespMap := make(map[string]*conversationResp)
	for _, conversationResp := range conversationResps {
		conversationRespMap[fmt.Sprintf("%s-%s-%d", conversationResp.Uid, conversationResp.ChannelId, conversationResp.ChannelType)] = conversationResp
	}

	conversationResps = make([]*conversationResp, 0, len(conversationRespMap))
	for _, conversationResp := range conversationRespMap {
		conversationResps = append(conversationResps, conversationResp)
	}

	c.JSON(http.StatusOK, conversationRespTotal{
		Data:  conversationResps,
		Total: 0,
	})

}

func (s *Server) requestConversationSearch(path string, nodeId uint64, queryMap map[string]string, headers map[string]string) (*conversationRespTotal, error) {
	node := s.clusterEventServer.Node(nodeId)
	if node == nil {
		s.Error("requestConversationSearch failed, node not found", zap.Uint64("nodeId", nodeId))
		return nil, errors.New("node not found")
	}
	fullUrl := fmt.Sprintf("%s%s", node.ApiServerAddr, path)
	queryMap["node_id"] = fmt.Sprintf("%d", nodeId)
	resp, err := network.Get(fullUrl, queryMap, headers)
	if err != nil {
		return nil, err
	}
	err = handlerIMError(resp)
	if err != nil {
		return nil, err
	}

	var conversationRespTotal *conversationRespTotal
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &conversationRespTotal)
	if err != nil {
		return nil, err
	}
	return conversationRespTotal, nil
}

func (s *Server) channelMigrate(c *wkhttp.Context) {

	var req struct {
		MigrateFrom uint64 `json:"migrate_from"` // 迁移的原节点
		MigrateTo   uint64 `json:"migrate_to"`   // 迁移的目标节点
	}
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		s.Error("BindJSON error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	channelId := c.Param("channel_id")
	channelType := wkutil.ParseUint8(c.Param("channel_type"))

	// 获取频道所属槽领导的id
	nodeId, err := s.SlotLeaderIdOfChannel(channelId, channelType)
	if err != nil {
		s.Error("channelMigrate: LeaderIdOfChannel error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if nodeId != s.opts.NodeId {
		c.ForwardWithBody(fmt.Sprintf("%s%s", s.clusterEventServer.Node(nodeId).ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	// 获取频道的分布式配置
	clusterConfig, err := s.getChannelClusterConfig(channelId, channelType)
	if err != nil {
		s.Error("channelMigrate: getChannelClusterConfig error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if !wkutil.ArrayContainsUint64(clusterConfig.Replicas, req.MigrateFrom) {
		c.ResponseError(errors.New("MigrateFrom not in replicas"))
		return
	}

	if wkutil.ArrayContainsUint64(clusterConfig.Replicas, req.MigrateTo) && req.MigrateFrom != clusterConfig.LeaderId {
		c.ResponseError(errors.New("transition between followers is not supported"))
		return
	}

	newClusterConfig := clusterConfig.Clone()
	if newClusterConfig.MigrateFrom != 0 || newClusterConfig.MigrateTo != 0 {
		c.ResponseError(errors.New("migrate is in progress"))
		return
	}

	// 保存配置
	newClusterConfig.MigrateFrom = req.MigrateFrom
	newClusterConfig.MigrateTo = req.MigrateTo
	newClusterConfig.ConfVersion = uint64(time.Now().UnixNano())

	if !wkutil.ArrayContainsUint64(clusterConfig.Replicas, req.MigrateTo) {
		// 将要目标节点加入学习者中
		newClusterConfig.Learners = append(newClusterConfig.Learners, req.MigrateTo)
	}

	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ReqTimeout)
	defer cancel()

	// 提案保存配置
	err = s.opts.ChannelClusterStorage.Propose(timeoutCtx, newClusterConfig)
	if err != nil {
		s.Error("channelMigrate: Save error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	s.clusterCfgCache.Add(wkutil.ChannelToKey(channelId, channelType), newClusterConfig)

	// 如果频道领导不是当前节点，则发送最新配置给频道领导 （这里就算发送失败也没问题，因为频道领导会间隔比对自己与槽领导的配置）
	if newClusterConfig.LeaderId != s.opts.NodeId {
		err = s.SendChannelClusterConfigUpdate(channelId, channelType, newClusterConfig.LeaderId)
		if err != nil {
			s.Error("channelMigrate: sendChannelClusterConfigUpdate error", zap.Error(err))
			c.ResponseError(err)
			return
		}
	} else {
		s.UpdateChannelClusterConfig(newClusterConfig)
	}

	// 如果目标节点不是当前节点，则发送最新配置给目标节点
	if req.MigrateTo != s.opts.NodeId {
		err = s.SendChannelClusterConfigUpdate(channelId, channelType, req.MigrateTo)
		if err != nil {
			s.Error("channelMigrate: sendChannelClusterConfigUpdate error", zap.Error(err))
			c.ResponseError(err)
			return
		}
	}
	c.ResponseOK()

}

func (s *Server) channelClusterConfig(c *wkhttp.Context) {

	start := time.Now()
	defer func() {
		end := time.Since(start)
		if end > time.Millisecond*200 {
			s.Warn("channelClusterConfig slow", zap.Duration("cost", end))
		}
	}()

	nodeId := wkutil.ParseUint64(c.Query("node_id"))

	if nodeId > 0 && nodeId != s.opts.NodeId {
		c.Forward(fmt.Sprintf("%s%s", s.clusterEventServer.Node(nodeId).ApiServerAddr, c.Request.URL.Path))
		return
	}

	channelId := c.Param("channel_id")
	channelType := wkutil.ParseUint8(c.Param("channel_type"))

	slotId := s.getSlotId(channelId)
	st := s.clusterEventServer.Slot(slotId)
	if st == nil {
		s.Error("slot not found", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Uint32("slotId", slotId))
		c.ResponseError(errors.New("slot not found"))
		return
	}

	if st.Leader != s.opts.NodeId {
		s.Error("slot leader is not current node", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Uint32("slotId", slotId), zap.Uint64("slotLeader", st.Leader))
		c.ResponseError(errors.New("slot leader is not current node"))
		return
	}

	clusterConfig, err := s.getChannelClusterConfig(channelId, channelType)
	if err != nil {
		s.Error("getChannelClusterConfig error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.JSON(http.StatusOK, clusterConfig)
}

func (s *Server) channelStart(c *wkhttp.Context) {

	if !s.opts.Auth.HasPermissionWithContext(c, resource.ClusterChannel.Start, auth.ActionWrite) {
		c.ResponseStatus(http.StatusUnauthorized)
		return
	}

	channelId := c.Param("channel_id")
	channelType := wkutil.ParseUint8(c.Param("channel_type"))

	cfg, err := s.loadOnlyChannelClusterConfig(channelId, channelType)
	if err != nil {
		s.Error("loadOnlyChannelClusterConfig error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if cfg.LeaderId == 0 {
		s.Error("leader not found", zap.String("cfg", cfg.String()))
		c.ResponseError(errors.New("leader not found"))
		return
	}

	if cfg.LeaderId != s.opts.NodeId {
		c.Forward(fmt.Sprintf("%s%s", s.clusterEventServer.Node(cfg.LeaderId).ApiServerAddr, c.Request.URL.Path))
		return
	}

	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ReqTimeout)
	defer cancel()
	_, err = s.loadOrCreateChannel(timeoutCtx, channelId, channelType)
	if err != nil {
		s.Error("loadOrCreateChannel error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	c.ResponseOK()
}

func (s *Server) channelStop(c *wkhttp.Context) {

	if !s.opts.Auth.HasPermissionWithContext(c, resource.ClusterChannel.Stop, auth.ActionWrite) {
		c.ResponseStatus(http.StatusUnauthorized)
		return
	}

	channelId := c.Param("channel_id")
	channelType := wkutil.ParseUint8(c.Param("channel_type"))

	cfg, err := s.loadOnlyChannelClusterConfig(channelId, channelType)
	if err != nil {
		s.Error("loadOnlyChannelClusterConfig error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if cfg.LeaderId == 0 {
		s.Error("leader not found", zap.String("cfg", cfg.String()))
		c.ResponseError(errors.New("leader not found"))
		return
	}

	if cfg.LeaderId != s.opts.NodeId {
		c.Forward(fmt.Sprintf("%s%s", s.clusterEventServer.Node(cfg.LeaderId).ApiServerAddr, c.Request.URL.Path))
		return
	}

	handler := s.channelManager.get(channelId, channelType)
	if handler != nil {
		s.channelManager.remove(handler.(*channel))
	}
	c.ResponseOK()
}

func (s *Server) channelStatus(c *wkhttp.Context) {
	var req struct {
		Channels []channelBase `json:"channels"`
	}
	if err := c.BindJSON(&req); err != nil {
		s.Error("BindJSON error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	resps := make([]*channelStatusResp, 0, len(req.Channels))
	for _, ch := range req.Channels {
		lastMsgSeq, lastAppendTime, err := s.opts.MessageLogStorage.LastIndexAndAppendTime(wkutil.ChannelToKey(ch.ChannelId, ch.ChannelType))
		if err != nil {
			s.Error("LastIndexAndAppendTime error", zap.Error(err))
			c.ResponseError(err)
			return
		}
		handler := s.channelManager.get(ch.ChannelId, ch.ChannelType)
		if handler != nil {
			resps = append(resps, &channelStatusResp{
				channelBase: ch,
				Running:     1,
				LastMsgSeq:  lastMsgSeq,
				LastMsgTime: lastAppendTime,
			})
		} else {
			resps = append(resps, &channelStatusResp{
				channelBase: ch,
				Running:     0,
				LastMsgSeq:  lastMsgSeq,
				LastMsgTime: lastAppendTime,
			})

		}
	}

	c.JSON(http.StatusOK, resps)

}

func (s *Server) channelReplicas(c *wkhttp.Context) {
	channelId := c.Param("channel_id")
	channelType := wkutil.ParseUint8(c.Param("channel_type"))

	slotId := s.getSlotId(channelId)

	st := s.clusterEventServer.Slot(slotId)
	if st == nil {
		s.Error("slot not found", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Uint32("slotId", slotId))
		c.ResponseError(errors.New("slot not found"))
		return
	}

	if st.Leader == 0 {
		s.Error("slot leader is 0", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Uint32("slotId", slotId))
		c.ResponseError(errors.New("slot leader is 0"))
		return
	}

	slotLeaderNode := s.clusterEventServer.Node(st.Leader)
	if slotLeaderNode == nil {
		s.Error("slot leader node not found", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.Uint32("slotId", slotId), zap.Uint64("slotLeader", st.Leader))
		c.ResponseError(errors.New("slot leader node not found"))
		return

	}

	if st.Leader != s.opts.NodeId {
		c.Forward(fmt.Sprintf("%s%s", slotLeaderNode.ApiServerAddr, c.Request.URL.Path))
		return
	}

	channelClusterConfig, err := s.getChannelClusterConfig(channelId, channelType)
	if err != nil {
		s.Error("getChannelClusterConfig error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ReqTimeout)
	defer cancel()

	replicas := make([]*channelReplicaDetailResp, 0, len(channelClusterConfig.Replicas))

	replicaIds := make([]uint64, 0, len(channelClusterConfig.Replicas)+len(channelClusterConfig.Learners))
	replicaIds = append(replicaIds, channelClusterConfig.Replicas...)
	replicaIds = append(replicaIds, channelClusterConfig.Learners...)

	requestGroup, _ := errgroup.WithContext(timeoutCtx)
	for _, replicaId := range replicaIds {
		replicaId := replicaId

		if !s.NodeIsOnline(replicaId) {
			continue
		}

		if replicaId == s.opts.NodeId {
			running := s.channelManager.exist(channelId, channelType)
			lastMsgSeq, lastTime, err := s.opts.DB.GetChannelLastMessageSeq(channelId, channelType)
			if err != nil {
				s.Error("GetChannelLastMessageSeq error", zap.Error(err))
				c.ResponseError(err)
				return
			}
			replicaResp := &channelReplicaResp{
				ReplicaId:   s.opts.NodeId,
				Running:     wkutil.BoolToInt(running),
				LastMsgSeq:  lastMsgSeq,
				LastMsgTime: lastTime,
			}
			lastMsgTimeFormat := ""
			if replicaResp.LastMsgTime != 0 {
				lastMsgTimeFormat = myUptime(time.Since(time.Unix(int64(replicaResp.LastMsgTime/1e9), 0)))
			}
			replicas = append(replicas, &channelReplicaDetailResp{
				channelReplicaResp: *replicaResp,
				Role:               s.getReplicaRole(channelClusterConfig, replicaId),
				RoleFormat:         s.getReplicaRoleFormat(channelClusterConfig, replicaId),
				LastMsgTimeFormat:  lastMsgTimeFormat,
			})
			continue

		}

		requestGroup.Go(func() error {
			replicaResp, err := s.requestChannelLocalReplica(replicaId, channelId, channelType, c.CopyRequestHeader(c.Request))
			if err != nil {
				return err
			}

			lastMsgTimeFormat := ""
			if replicaResp.LastMsgTime != 0 {
				lastMsgTimeFormat = myUptime(time.Since(time.Unix(int64(replicaResp.LastMsgTime/1e9), 0)))
			}

			replicas = append(replicas, &channelReplicaDetailResp{
				channelReplicaResp: *replicaResp,
				Role:               s.getReplicaRole(channelClusterConfig, replicaId),
				RoleFormat:         s.getReplicaRoleFormat(channelClusterConfig, replicaId),
				LastMsgTimeFormat:  lastMsgTimeFormat,
			})
			return nil
		})

	}

	err = requestGroup.Wait()
	if err != nil {
		s.Error("channelReplicas requestGroup error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.JSON(http.StatusOK, replicas)

}

func (s *Server) getReplicaRoleFormat(clusterConfig wkdb.ChannelClusterConfig, replicaId uint64) string {
	if replicaId == clusterConfig.LeaderId {
		return "leader"
	}
	if wkutil.ArrayContainsUint64(clusterConfig.Learners, replicaId) {
		return "learner"
	}
	return "follower"
}

func (s *Server) getReplicaRole(clusterConfig wkdb.ChannelClusterConfig, replicaId uint64) int {
	if replicaId == clusterConfig.LeaderId {
		return int(replica.RoleLeader)
	}
	if wkutil.ArrayContainsUint64(clusterConfig.Learners, replicaId) {
		return int(replica.RoleLearner)
	}
	return int(replica.RoleFollower)

}

func (s *Server) requestChannelLocalReplica(nodeId uint64, channelId string, channelType uint8, headers map[string]string) (*channelReplicaResp, error) {
	node := s.clusterEventServer.Node(nodeId)
	if node == nil {
		s.Error("requestChannelLocalReplica failed, node not found", zap.Uint64("nodeId", nodeId))
		return nil, errors.New("node not found")
	}
	fullUrl := fmt.Sprintf("%s%s", node.ApiServerAddr, s.formatPath(fmt.Sprintf("/channels/%s/%d/localReplica", channelId, channelType)))
	resp, err := network.Get(fullUrl, nil, headers)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("requestChannelLocalReplica failed, status code: %d", resp.StatusCode)
	}

	var replicaResp *channelReplicaResp
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &replicaResp)
	if err != nil {
		return nil, err
	}
	return replicaResp, nil
}

func (s *Server) channelLocalReplica(c *wkhttp.Context) {
	channelId := c.Param("channel_id")
	channelType := wkutil.ParseUint8(c.Param("channel_type"))

	running := s.channelManager.exist(channelId, channelType)

	lastMsgSeq, lastTime, err := s.opts.DB.GetChannelLastMessageSeq(channelId, channelType)
	if err != nil {
		s.Error("GetChannelLastMessageSeq error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	resp := &channelReplicaResp{
		ReplicaId:   s.opts.NodeId,
		Running:     wkutil.BoolToInt(running),
		LastMsgSeq:  lastMsgSeq,
		LastMsgTime: lastTime,
	}

	c.JSON(http.StatusOK, resp)
}

type channelBase struct {
	ChannelId   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
}

type channelStatusResp struct {
	channelBase
	Running     int    `json:"running"`       // 是否运行中
	LastMsgSeq  uint64 `json:"last_msg_seq"`  // 最新消息序号
	LastMsgTime uint64 `json:"last_msg_time"` // 最新消息时间
}

type channelReplicaResp struct {
	ReplicaId   uint64 `json:"replica_id"`    // 副本节点id
	Running     int    `json:"running"`       // 是否运行中
	LastMsgSeq  uint64 `json:"last_msg_seq"`  // 最新消息序号
	LastMsgTime uint64 `json:"last_msg_time"` // 最新消息时间
}

type channelReplicaDetailResp struct {
	channelReplicaResp
	Role              int    `json:"role"`                 // 角色
	RoleFormat        string `json:"role_format"`          // 角色格式化
	LastMsgTimeFormat string `json:"last_msg_time_format"` // 最新消息时间格式化
}

func (s *Server) clusterLogs(c *wkhttp.Context) {
	nodeId := wkutil.ParseUint64(c.Query("node_id"))
	if nodeId != s.opts.NodeId {
		c.Forward(fmt.Sprintf("%s%s", s.clusterEventServer.Node(nodeId).ApiServerAddr, c.Request.URL.Path))
		return
	}

	slotId := wkutil.ParseUint32(c.Query("slot")) // slot id

	// 日志类型
	logType := LogType(wkutil.ParseInt(c.Query("log_type")))
	if logType == LogTypeUnknown {
		logType = LogTypeConfig
	}

	pre := wkutil.ParseUint64(c.Query("pre"))
	next := wkutil.ParseUint64(c.Query("next"))
	limit := wkutil.ParseInt(c.Query("limit"))

	if limit <= 0 {
		limit = s.opts.PageSize
	}

	var (
		start        uint64
		end          uint64
		logs         []replica.Log
		err          error
		appliedIndex uint64
		lastLogIndex uint64
	)

	if next > 0 {
		end = next
		if next > uint64(limit) {
			start = next - uint64(limit) - 1
		} else {
			start = 0
		}
	} else if pre > 0 {
		start = pre + 1
		end = pre + uint64(limit) + 1
	}

	if logType == LogTypeConfig {
		logs, err = s.clusterEventServer.GetLogsInReverseOrder(start, end, limit)
		if err != nil {
			s.Error("config: GetLogsInReverseOrder error", zap.Error(err))
			c.ResponseError(err)
			return
		}
		appliedIndex, err = s.clusterEventServer.AppliedLogIndex()
		if err != nil {
			s.Error("AppliedLogIndex error", zap.Error(err))
			c.ResponseError(err)
			return
		}

		lastLogIndex, err = s.clusterEventServer.LastLogIndex()
		if err != nil {
			s.Error("LastLogIndex error", zap.Error(err))
			c.ResponseError(err)
			return
		}

	} else if logType == LogTypeSlot {
		shardNo := SlotIdToKey(slotId)
		logs, err = s.slotStorage.GetLogsInReverseOrder(shardNo, start, end, limit)
		if err != nil {
			s.Error("slot: GetLogsInReverseOrder error", zap.Error(err))
			c.ResponseError(err)
			return

		}
		appliedIndex, err = s.slotStorage.AppliedIndex(shardNo)
		if err != nil {
			s.Error("slot: AppliedLogIndex error", zap.Error(err))
			c.ResponseError(err)
			return
		}
		lastLogIndex, err = s.slotStorage.LastIndex(shardNo)
		if err != nil {
			s.Error("slot: LastLogIndex error", zap.Error(err))
			c.ResponseError(err)
			return
		}
	}

	resps := make([]*LogResp, 0, len(logs))

	for _, log := range logs {
		resp, err := NewLogRespFromLog(log, logType)
		if err != nil {
			s.Error("NewLogRespFromLog error", zap.Error(err), zap.Uint64("index", log.Index), zap.Uint32("term", log.Term))
			c.ResponseError(err)
			return
		}
		resps = append(resps, resp)
	}

	var (
		newNext uint64
		newPre  uint64
		more    int = 1
	)
	if len(logs) > 0 {
		newNext = logs[len(logs)-1].Index
		newPre = logs[0].Index
	}
	if newPre == 1 {
		more = 0
	}

	c.JSON(http.StatusOK, LogRespTotal{
		Next:    newNext,
		Pre:     newPre,
		More:    more,
		Applied: appliedIndex,
		Last:    lastLogIndex,
		Logs:    resps,
	})
}
