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
	route.GET(s.formatPath("/slots"), s.slotsGet)                                                 // 获取指定的槽信息
	route.GET(s.formatPath("/allslot"), s.allSlotsGet)                                            // 获取所有槽信息
	route.GET(s.formatPath("/slots/:id/config"), s.slotClusterConfigGet)                          // 槽分布式配置
	route.GET(s.formatPath("/slots/:id/channels"), s.slotChannelsGet)                             // 获取某个槽的所有频道信息
	route.GET(s.formatPath("/info"), s.clusterInfoGet)                                            // 获取集群信息
	route.GET(s.formatPath("/messages"), s.messageSearch)                                         // 搜索消息
	route.GET(s.formatPath("/channels"), s.channelSearch)                                         // 频道搜索
	route.GET(s.formatPath("/channels/:channel_id/:channel_type/subscribers"), s.subscribersGet)  // 获取频道的订阅者列表
	route.GET(s.formatPath("/channels/:channel_id/:channel_type/denylist"), s.denylistGet)        // 获取黑名单列表
	route.GET(s.formatPath("/channels/:channel_id/:channel_type/allowlist"), s.allowlistGet)      // 获取白名单列表
	route.GET(s.formatPath("/users"), s.userSearch)                                               // 用户搜索
	route.GET(s.formatPath("/devices"), s.deviceSearch)                                           // 设备搜索
	route.GET(s.formatPath("/conversations"), s.conversationSearch)                               // 搜索最近会话消息
	route.POST(s.formatPath("/channels/:channel_id/:channel_type/migrate"), s.channelMigrate)     // 迁移频道
	route.GET(s.formatPath("/channels/:channel_id/:channel_type/config"), s.channelClusterConfig) // 获取频道的分布式配置
	route.POST(s.formatPath("/channels/:channel_id/:channel_type/start"), s.channelStart)         // 开始频道
	route.POST(s.formatPath("/channels/:channel_id/:channel_type/stop"), s.channelStop)           // 停止频道

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
			nodeCfg.SlotCount = s.getNodeSlotCount(node.Id, cfg)
			nodeCfg.SlotLeaderCount = s.getNodeSlotLeaderCount(node.Id, cfg)
			nodeCfgs = append(nodeCfgs, nodeCfg)
			continue
		}
		requestGroup.Go(func(nId uint64) func() error {
			return func() error {
				nodeCfg, err := s.requestNodeInfo(nId)
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

	channelClusterConfigs, err := s.opts.ChannelClusterStorage.GetAll(0, 10000)
	if err != nil {
		s.Error("GetWithAllSlot error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	channelClusterConfigResps := make([]*ChannelClusterConfigResp, 0, len(channelClusterConfigs))

	for _, cfg := range channelClusterConfigs {
		slotId := s.getSlotId(cfg.ChannelId)
		slot := s.clusterEventServer.Slot(slotId)
		if slot == nil {
			s.Error("slot not found", zap.Uint32("slotId", slotId))
			c.ResponseError(err)
			return
		}
		if !wkutil.ArrayContainsUint64(cfg.Replicas, s.opts.NodeId) {
			continue
		}
		shardNo := ChannelToKey(cfg.ChannelId, cfg.ChannelType)
		lastMsgSeq, lastAppendTime, err := s.opts.MessageLogStorage.LastIndexAndAppendTime(shardNo)
		if err != nil {
			s.Error("LastIndexAndAppendTime error", zap.Error(err))
			c.ResponseError(err)
			return
		}

		handler := s.channelManager.get(cfg.ChannelId, cfg.ChannelType)

		var activeChannel *channel
		if handler != nil {
			activeChannel = handler.(*channel)
			cfg = activeChannel.cfg
		}

		resp := NewChannelClusterConfigRespFromClusterConfig(slot.Leader, slot.Id, cfg)
		resp.MaxMessageSeq = lastMsgSeq
		if lastAppendTime > 0 {
			resp.LastAppendTime = myUptime(time.Since(time.Unix(int64(lastAppendTime/1e9), 0)))
		}
		channelClusterConfigResps = append(channelClusterConfigResps, resp)

		if activeChannel != nil {
			resp.Active = 1
			resp.ActiveFormat = "运行中"
		} else {
			resp.ActiveFormat = "未运行"
			resp.Active = 0
		}

	}

	c.JSON(http.StatusOK, ChannelClusterConfigRespTotal{
		Total: len(channelClusterConfigResps),
		Data:  channelClusterConfigResps,
	})
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
		shardNo := ChannelToKey(cfg.ChannelId, cfg.ChannelType)
		lastMsgSeq, lastAppendTime, err := s.opts.MessageLogStorage.LastIndexAndAppendTime(shardNo)
		if err != nil {
			s.Error("LastIndexAndAppendTime error", zap.Error(err))
			c.ResponseError(err)
			return
		}

		resp := NewChannelClusterConfigRespFromClusterConfig(slot.Leader, slot.Id, cfg)
		resp.MaxMessageSeq = lastMsgSeq
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
		requestGroup.Go(func(nId uint64, sIds []uint32) func() error {
			return func() error {
				slotResps, err := s.requestSlotInfo(nId, sIds)
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
	cfg, err := s.loadChannelClusterConfig(channelId, channelType)
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
	shardNo := ChannelToKey(channelId, channelType)
	lastMsgSeq, lastAppendTime, err := s.opts.MessageLogStorage.LastIndexAndAppendTime(shardNo)
	if err != nil {
		s.Error("LastIndexAndAppendTime error", zap.Error(err))
		c.ResponseError(err)
		return

	}
	resp.MaxMessageSeq = lastMsgSeq
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
	currentPage := wkutil.ParseInt(c.Query("current_page")) // 页码
	payloadStr := strings.TrimSpace(c.Query("payload"))     // base64编码的消息内容
	messageId := wkutil.ParseInt64(c.Query("message_id"))
	clientMsgNo := strings.TrimSpace(c.Query("client_msg_no"))

	if currentPage <= 0 {
		currentPage = 1
	}
	// 解密payload
	var payload []byte
	var err error
	// base64解码payloadStr
	if strings.TrimSpace(payloadStr) != "" {
		payload, err = wkutil.Base64Decode(payloadStr)
		if err != nil {
			s.Error("base64 decode error", zap.Error(err))
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
			MessageId:   messageId,
			FromUid:     fromUid,
			Limit:       limit,
			ChannelId:   channelId,
			ChannelType: channelType,
			CurrentPage: currentPage,
			Payload:     payload,
			ClientMsgNo: clientMsgNo,
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
		requestGroup.Go(func(nId uint64, queryValues url.Values) func() error {
			return func() error {
				queryMap := map[string]string{}
				for key, values := range queryValues {
					if len(values) > 0 {
						queryMap[key] = values[0]
					}
				}
				result, err := s.requestNodeMessageSearch(c.Request.URL.Path, nId, queryMap)
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
	if len(messages) > limit {
		messages = messages[:limit]
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

func (s *Server) requestNodeMessageSearch(path string, nodeId uint64, queryMap map[string]string) (*messageRespTotal, error) {
	node := s.clusterEventServer.Node(nodeId)
	if node == nil {
		s.Error("requestNodeMessageSearch failed, node not found", zap.Uint64("nodeId", nodeId))
		return nil, errors.New("node not found")
	}
	fullUrl := fmt.Sprintf("%s%s", node.ApiServerAddr, path)
	queryMap["node_id"] = fmt.Sprintf("%d", nodeId)
	resp, err := network.Get(fullUrl, queryMap, nil)
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
		requestGroup.Go(func(nId uint64, queryValues url.Values) func() error {
			return func() error {
				queryMap := map[string]string{}
				for key, values := range queryValues {
					if len(values) > 0 {
						queryMap[key] = values[0]
					}
				}
				result, err := s.requestNodeChannelSearch(c.Request.URL.Path, nId, queryMap)
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

func (s *Server) requestNodeChannelSearch(path string, nodeId uint64, queryMap map[string]string) (*channelInfoRespTotal, error) {
	node := s.clusterEventServer.Node(nodeId)
	if node == nil {
		s.Error("requestNodeChannelSearch failed, node not found", zap.Uint64("nodeId", nodeId))
		return nil, errors.New("node not found")
	}
	fullUrl := fmt.Sprintf("%s%s", node.ApiServerAddr, path)
	queryMap["node_id"] = fmt.Sprintf("%d", nodeId)
	resp, err := network.Get(fullUrl, queryMap, nil)
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
	currentPage := wkutil.ParseInt(c.Query("current_page")) // 页码
	uid := strings.TrimSpace(c.Query("uid"))

	if currentPage <= 0 {
		currentPage = 1
	}

	if limit <= 0 {
		limit = s.opts.PageSize
	}

	users, err := s.opts.DB.SearchUser(wkdb.UserSearchReq{
		Uid:         uid,
		Limit:       limit,
		CurrentPage: currentPage,
	})
	if err != nil {
		s.Error("search user failed", zap.Error(err))
		c.ResponseError(err)
		return
	}

	userResps := make([]*userResp, 0, len(users))
	for _, user := range users {
		userResps = append(userResps, newUserResp(user))
	}

	count, err := s.opts.DB.GetTotalUserCount()
	if err != nil {
		s.Error("GetTotalUserCount error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.JSON(http.StatusOK, userRespTotal{
		Data:  userResps,
		Total: count,
	})
}

func (s *Server) deviceSearch(c *wkhttp.Context) {
	// 搜索条件
	limit := wkutil.ParseInt(c.Query("limit"))
	currentPage := wkutil.ParseInt(c.Query("current_page")) // 页码
	uid := strings.TrimSpace(c.Query("uid"))
	deviceFlag := wkutil.ParseUint64(strings.TrimSpace(c.Query("device_flag")))

	if currentPage <= 0 {
		currentPage = 1
	}
	if limit <= 0 {
		limit = s.opts.PageSize
	}

	devices, err := s.opts.DB.SearchDevice(wkdb.DeviceSearchReq{
		Uid:         uid,
		DeviceFlag:  deviceFlag,
		Limit:       limit,
		CurrentPage: currentPage,
	})
	if err != nil {
		s.Error("search device failed", zap.Error(err))
		c.ResponseError(err)
		return
	}

	deviceResps := make([]*deviceResp, 0, len(devices))
	for _, device := range devices {
		deviceResps = append(deviceResps, newDeviceResp(device))
	}

	count, err := s.opts.DB.GetTotalDeviceCount()
	if err != nil {
		s.Error("GetTotalDeviceCount error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.JSON(http.StatusOK, deviceRespTotal{
		Data:  deviceResps,
		Total: count,
	})

}

func (s *Server) conversationSearch(c *wkhttp.Context) {
	// 搜索条件
	limit := wkutil.ParseInt(c.Query("limit"))
	currentPage := wkutil.ParseInt(c.Query("current_page")) // 页码
	uid := strings.TrimSpace(c.Query("uid"))

	if currentPage <= 0 {
		currentPage = 1
	}
	if limit <= 0 {
		limit = s.opts.PageSize
	}

	conversations, err := s.opts.DB.SearchConversation(wkdb.ConversationSearchReq{
		Uid:         uid,
		Limit:       limit,
		CurrentPage: currentPage,
	})
	if err != nil {
		s.Error("search conversation failed", zap.Error(err))
		c.ResponseError(err)
		return
	}

	conversationResps := make([]*conversationResp, 0, len(conversations))

	for _, conversation := range conversations {
		lastMsgSeq, _, err := s.opts.DB.GetChannelLastMessageSeq(conversation.ChannelId, conversation.ChannelType)
		if err != nil {
			s.Error("GetChannelLastMessageSeq error", zap.Error(err))
			c.ResponseError(err)
			return
		}
		resp := newConversationResp(conversation)
		resp.LastMsgSeq = lastMsgSeq
		conversationResps = append(conversationResps, resp)
	}

	count, err := s.opts.DB.GetTotalSessionCount()
	if err != nil {
		s.Error("GetTotalConversationCount error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.JSON(http.StatusOK, conversationRespTotal{
		Data:  conversationResps,
		Total: count,
	})

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
		c.ResponseError(errors.New("source node not in replicas"))
		return
	}
	if wkutil.ArrayContainsUint64(clusterConfig.Replicas, req.MigrateTo) {
		c.ResponseError(errors.New("target node already in replicas"))
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
	// 将要目标节点加入学习者中
	newClusterConfig.Learners = append(newClusterConfig.Learners, req.MigrateTo)

	// 如果迁移的是领导者,则将频道设置为选举状态，这样就不会再有新的消息日志写入，等待学习者学到与频道领导一样的日志。
	if req.MigrateFrom == clusterConfig.LeaderId {
		newClusterConfig.Status = wkdb.ChannelClusterStatusCandidate
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

	// 如果频道领导不是当前节点，则发送最新配置给频道领导 （这里就算发送失败也没问题，因为频道领导会间隔比对自己与槽领导的配置）
	if newClusterConfig.LeaderId != s.opts.NodeId {
		err = s.sendChannelClusterConfigUpdate(channelId, channelType, newClusterConfig.LeaderId)
		if err != nil {
			s.Error("channelMigrate: sendChannelClusterConfigUpdate error", zap.Error(err))
			c.ResponseError(err)
			return
		}
	} else {
		s.updateChannelClusterConfig(newClusterConfig)
	}

	// 如果目标节点不是当前节点，则发送最新配置给目标节点
	if req.MigrateTo != s.opts.NodeId {
		err = s.sendChannelClusterConfigUpdate(channelId, channelType, req.MigrateTo)
		if err != nil {
			s.Error("channelMigrate: sendChannelClusterConfigUpdate error", zap.Error(err))
			c.ResponseError(err)
			return
		}
	}
	c.ResponseOK()

}

func (s *Server) channelClusterConfig(c *wkhttp.Context) {
	nodeId := wkutil.ParseUint64(c.Query("node_id"))

	if nodeId > 0 && nodeId != s.opts.NodeId {
		c.Forward(fmt.Sprintf("%s%s", s.clusterEventServer.Node(nodeId).ApiServerAddr, c.Request.URL.Path))
		return
	}

	channelId := c.Param("channel_id")
	channelType := wkutil.ParseUint8(c.Param("channel_type"))

	clusterConfig, err := s.getChannelClusterConfig(channelId, channelType)
	if err != nil {
		s.Error("getChannelClusterConfig error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	c.JSON(http.StatusOK, clusterConfig)
}

func (s *Server) channelStart(c *wkhttp.Context) {

	var req struct {
		NodeId uint64 `json:"node_id"`
	}
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		s.Error("BindJSON error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if req.NodeId > 0 && req.NodeId != s.opts.NodeId {
		c.ForwardWithBody(fmt.Sprintf("%s%s", s.clusterEventServer.Node(req.NodeId).ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	channelId := c.Param("channel_id")
	channelType := wkutil.ParseUint8(c.Param("channel_type"))

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

	var req struct {
		NodeId uint64 `json:"node_id"`
	}
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		s.Error("BindJSON error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if req.NodeId > 0 && req.NodeId != s.opts.NodeId {
		c.ForwardWithBody(fmt.Sprintf("%s%s", s.clusterEventServer.Node(req.NodeId).ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	channelId := c.Param("channel_id")
	channelType := wkutil.ParseUint8(c.Param("channel_type"))

	handler := s.channelManager.get(channelId, channelType)
	if handler != nil {
		s.channelManager.remove(handler.(*channel))
	}
	c.ResponseOK()
}
