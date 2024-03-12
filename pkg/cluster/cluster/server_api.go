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

	"github.com/WuKongIM/WuKongIM/pkg/cluster/cluster/clusterconfig/pb"
	"github.com/WuKongIM/WuKongIM/pkg/network"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func (s *Server) ServerAPI(route *wkhttp.WKHttp, prefix string) {

	s.apiPrefix = prefix

	route.GET(s.formatPath("/nodes"), s.nodesGet)                     // 获取所有节点
	route.GET(s.formatPath("/simpleNodes"), s.simpleNodesGet)         // 获取简单节点信息
	route.GET(s.formatPath("/node"), s.nodeGet)                       // 获取当前节点信息
	route.GET(s.formatPath("/nodes/:id/channels"), s.nodeChannelsGet) // 获取节点的所有频道信息

	route.GET(s.formatPath("/channels/:channel_id/:channel_type/config"), s.channelClusterConfigGet) // 获取频道分布式配置
	route.GET(s.formatPath("/slots"), s.slotsGet)                                                    // 获取指定的槽信息
	route.GET(s.formatPath("/allslot"), s.allSlotsGet)                                               // 获取所有槽信息
	route.GET(s.formatPath("/slots/:id/config"), s.slotClusterConfigGet)                             // 槽分布式配置
	route.GET(s.formatPath("/slots/:id/channels"), s.slotChannelsGet)                                // 获取某个槽的所有频道信息
	route.GET(s.formatPath("/info"), s.clusterInfoGet)                                               // 获取集群信息
}

func (s *Server) formatPath(path string) string {
	var prefix = s.apiPrefix
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	prefix = strings.TrimSuffix(prefix, "/")
	path = strings.TrimPrefix(path, "/")

	return fmt.Sprintf("%s/%s", prefix, path)
}

func (s *Server) nodeGet(c *wkhttp.Context) {
	nodeCfg := s.getLocalNodeInfo()
	c.JSON(http.StatusOK, nodeCfg)
}

func (s *Server) nodesGet(c *wkhttp.Context) {
	cfgServer := s.getClusterConfigManager().clusterconfigServer
	cfg := cfgServer.ConfigManager().GetConfig()

	leaderId := cfgServer.Leader()

	nodeCfgs := make([]*NodeConfig, 0, len(cfg.Nodes))

	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, time.Second*10)
	defer cancel()
	requestGroup, _ := errgroup.WithContext(timeoutCtx)

	for _, node := range cfg.Nodes {
		if node.Id == s.opts.NodeID {
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

func (s *Server) simpleNodesGet(c *wkhttp.Context) {
	cfgServer := s.getClusterConfigManager().clusterconfigServer
	cfg := cfgServer.ConfigManager().GetConfig()

	leaderId := cfgServer.Leader()

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

// 请求指定节点的配置信息
func (s *Server) requestNodeInfo(nodeId uint64) (*NodeConfig, error) {
	node := s.getClusterConfigManager().node(nodeId)
	if node == nil {
		return nil, errors.New("node not found")
	}
	resp, err := network.Get(fmt.Sprintf("%s%s", node.ApiServerAddr, s.formatPath("/node")), nil, nil)
	if err != nil {
		return nil, err
	}
	nodeCfg := &NodeConfig{}
	err = wkutil.ReadJSONByByte([]byte(resp.Body), nodeCfg)
	if err != nil {
		return nil, err
	}
	return nodeCfg, nil
}

func (s *Server) getLocalNodeInfo() *NodeConfig {
	cfgServer := s.getClusterConfigManager().clusterconfigServer
	cfg := cfgServer.ConfigManager().GetConfig()
	leaderId := cfgServer.Leader()
	node := cfgServer.ConfigManager().Node(s.opts.NodeID)
	nodeCfg := NewNodeConfigFromNode(node)
	nodeCfg.IsLeader = wkutil.BoolToInt(leaderId == node.Id)
	nodeCfg.SlotCount = s.getNodeSlotCount(node.Id, cfg)
	nodeCfg.SlotLeaderCount = s.getNodeSlotLeaderCount(node.Id, cfg)
	nodeCfg.Uptime = myUptime(time.Since(s.uptime))
	nodeCfg.AppVersion = s.opts.AppVersion
	nodeCfg.ConfigVersion = cfg.Version
	return nodeCfg
}

func (s *Server) clusterInfoGet(c *wkhttp.Context) {

	leaderId := s.getClusterConfigManager().clusterconfigServer.Leader()
	if leaderId == 0 {
		c.ResponseError(errors.New("leader not found"))
		return
	}
	if leaderId != s.opts.NodeID {
		leaderNode := s.getClusterConfigManager().node(leaderId)
		c.Forward(fmt.Sprintf("%s%s", leaderNode.ApiServerAddr, c.Request.URL.Path))
		return
	}

	cfg := s.getClusterConfig()

	c.JSON(http.StatusOK, cfg)
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

	ch, err := s.channelGroupManager.fetchChannel(channelId, channelType)
	if err != nil {
		s.Error("fetchChannel error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	cfg := ch.getClusterConfig()
	slotId := s.getChannelSlotId(channelId)
	slot := s.clusterEventListener.clusterconfigManager.slot(slotId)
	if slot == nil {
		s.Error("slot not found", zap.Uint32("slotId", slotId))
		c.ResponseError(err)
		return

	}
	resp := NewChannelClusterConfigRespFromClusterConfig(slot.Leader, slotId, cfg)
	shardNo := ChannelKey(channelId, channelType)
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

func (s *Server) slotClusterConfigGet(c *wkhttp.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		s.Error("id parse error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	slotId := uint32(id)
	shardNo := GetSlotShardNo(slotId)

	slot := s.clusterEventListener.clusterconfigManager.slot(slotId)
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
	appliedIdx, err := s.localStorage.getAppliedIndex(shardNo)
	if err != nil {
		s.Error("getAppliedIndex error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	lastIdx, err := s.opts.ShardLogStorage.LastIndex(shardNo)
	if err != nil {
		s.Error("LastIndex error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	cfg := NewSlotClusterConfigRespFromClusterConfig(appliedIdx, lastIdx, leaderLogMaxIndex, slot)
	c.JSON(http.StatusOK, cfg)

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

func (s *Server) allSlotsGet(c *wkhttp.Context) {

	leaderId := s.getClusterConfigManager().leaderId()
	if leaderId == 0 {
		c.ResponseError(errors.New("leader not found"))
		return
	}
	if leaderId != s.opts.NodeID {
		leaderNode := s.getClusterConfigManager().node(leaderId)
		c.Forward(fmt.Sprintf("%s%s", leaderNode.ApiServerAddr, c.Request.URL.Path))
		return
	}

	clusterCfg := s.getClusterConfig()
	resps := make([]*SlotResp, 0, len(clusterCfg.Slots))

	nodeSlotsMap := make(map[uint64][]uint32)
	for _, st := range clusterCfg.Slots {
		if st.Leader == s.opts.NodeID {
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

func (s *Server) requestSlotInfo(nodeId uint64, slotIds []uint32) ([]*SlotResp, error) {
	node := s.getClusterConfigManager().node(nodeId)
	if node == nil {
		return nil, errors.New("node not found")
	}
	resp, err := network.Get(fmt.Sprintf("%s%s", node.ApiServerAddr, s.formatPath("/slots")), map[string]string{
		"ids": strings.Join(wkutil.Uint32ArrayToStringArray(slotIds), ","),
	}, nil)
	if err != nil {
		return nil, err
	}
	slotResps := make([]*SlotResp, 0)
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &slotResps)
	return slotResps, err
}

func (s *Server) getSlotInfo(slotId uint32) (*SlotResp, error) {
	slot := s.clusterEventListener.clusterconfigManager.slot(slotId)
	if slot == nil {
		return nil, errors.New("slot not found")
	}
	count, err := s.opts.ChannelClusterStorage.GetCountWithSlotId(slotId)
	if err != nil {
		return nil, err
	}
	shardNo := GetSlotShardNo(slotId)
	lastIdx, err := s.localStorage.opts.ShardLogStorage.LastIndex(shardNo)
	if err != nil {
		return nil, err
	}
	resp := NewSlotResp(slot, count)
	resp.LogIndex = lastIdx
	return resp, nil
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
		if !wkutil.ArrayContainsUint64(cfg.Replicas, s.opts.NodeID) {
			continue
		}
		slot := s.clusterEventListener.clusterconfigManager.slot(slotId)
		if slot == nil {
			s.Error("slot not found", zap.Uint32("slotId", slotId))
			c.ResponseError(err)
			return
		}
		shardNo := ChannelKey(cfg.ChannelID, cfg.ChannelType)
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

func (s *Server) nodeChannelsGet(c *wkhttp.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		s.Error("id parse error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	node := s.getClusterConfigManager().node(id)
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

	if node.Id != s.opts.NodeID {
		c.Forward(fmt.Sprintf("%s%s", node.ApiServerAddr, c.Request.URL.Path))
		return
	}

	channelClusterConfigs, err := s.opts.ChannelClusterStorage.GetWithAllSlot()
	if err != nil {
		s.Error("GetWithAllSlot error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	channelClusterConfigResps := make([]*ChannelClusterConfigResp, 0, len(channelClusterConfigs))

	for _, cfg := range channelClusterConfigs {
		slotId := s.getChannelSlotId(cfg.ChannelID)
		slot := s.clusterEventListener.clusterconfigManager.slot(slotId)
		if slot == nil {
			s.Error("slot not found", zap.Uint32("slotId", slotId))
			c.ResponseError(err)
			return
		}
		if !wkutil.ArrayContainsUint64(cfg.Replicas, s.opts.NodeID) {
			continue
		}
		shardNo := ChannelKey(cfg.ChannelID, cfg.ChannelType)
		lastMsgSeq, lastAppendTime, err := s.opts.MessageLogStorage.LastIndexAndAppendTime(shardNo)
		if err != nil {
			s.Error("LastIndexAndAppendTime error", zap.Error(err))
			c.ResponseError(err)
			return
		}

		resp := NewChannelClusterConfigRespFromClusterConfig(slot.Leader, slot.Id, cfg)
		resp.MaxMessageSeq = lastMsgSeq
		if lastAppendTime > 0 {
			resp.LastAppendTime = myUptime(time.Since(time.Unix(int64(lastAppendTime/1e9), 0)))
		}
		channelClusterConfigResps = append(channelClusterConfigResps, resp)

		exist := s.channelGroupManager.existChannel(cfg.ChannelID, cfg.ChannelType)
		if exist {
			resp.Active = 1
			resp.ActiveFormat = "已激活"
		} else {
			resp.ActiveFormat = "未激活"
			resp.Active = 0
		}
	}

	c.JSON(http.StatusOK, ChannelClusterConfigRespTotal{
		Total: len(channelClusterConfigResps),
		Data:  channelClusterConfigResps,
	})
}

func (s *Server) getSlotMaxLogIndex(slotId uint32) (uint64, error) {

	slot := s.clusterEventListener.clusterconfigManager.slot(slotId)
	if slot == nil {
		return 0, errors.New("slot not found")
	}

	if slot.Leader == s.opts.NodeID {
		shardNo := GetSlotShardNo(slotId)
		lastIdx, err := s.opts.ShardLogStorage.LastIndex(shardNo)
		if err != nil {
			return 0, err
		}
		return lastIdx, nil
	}

	slotLogResp, err := s.nodeManager.requestSlotLogInfo(slot.Leader, &SlotLogInfoReq{
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

func (s *Server) getNodeSlotCount(nodeId uint64, cfg *pb.Config) int {
	count := 0
	for _, st := range cfg.Slots {
		if wkutil.ArrayContainsUint64(st.Replicas, nodeId) {
			count++
		}
	}
	return count
}

func (s *Server) getNodeSlotLeaderCount(nodeId uint64, cfg *pb.Config) int {
	count := 0
	for _, st := range cfg.Slots {
		if st.Leader == nodeId {
			count++
		}
	}
	return count
}

func (s *Server) getClusterConfig() *pb.Config {
	return s.getClusterConfigManager().clusterconfigServer.ConfigManager().GetConfig()
}

func (s *Server) getClusterConfigManager() *clusterconfigManager {
	return s.clusterEventListener.clusterconfigManager
}

type NodeConfigTotal struct {
	Total int           `json:"total"` // 总数
	Data  []*NodeConfig `json:"data"`
}

type NodeConfig struct {
	Id              uint64         `json:"id"`                          // 节点ID
	IsLeader        int            `json:"is_leader,omitempty"`         // 是否是leader
	Role            pb.NodeRole    `json:"role"`                        // 节点角色
	ClusterAddr     string         `json:"cluster_addr"`                // 集群地址
	ApiServerAddr   string         `json:"api_server_addr,omitempty"`   // API服务地址
	Online          int            `json:"online,omitempty"`            // 是否在线
	OfflineCount    int            `json:"offline_count,omitempty"`     // 下线次数
	LastOffline     string         `json:"last_offline,omitempty"`      // 最后一次下线时间
	AllowVote       int            `json:"allow_vote"`                  // 是否允许投票
	SlotCount       int            `json:"slot_count,omitempty"`        // 槽位数量
	SlotLeaderCount int            `json:"slot_leader_count,omitempty"` // 槽位领导者数量
	ExportCount     int            `json:"export_count,omitempty"`      // 迁出槽位数量
	Exports         []*SlotMigrate `json:"exports,omitempty"`           // 迁移槽位
	ImportCount     int            `json:"import_count,omitempty"`      // 迁入槽位数量
	Imports         []*SlotMigrate `json:"imports,omitempty"`           // 迁入槽位
	Uptime          string         `json:"uptime,omitempty"`            // 运行时间
	AppVersion      string         `json:"app_version,omitempty"`       // 应用版本
	ConfigVersion   uint64         `json:"config_version,omitempty"`    // 配置版本
	Status          pb.NodeStatus  `json:"status,omitempty"`            // 状态
	StatusFormat    string         `json:"status_format,omitempty"`     // 状态格式化
}

func NewNodeConfigFromNode(n *pb.Node) *NodeConfig {
	// lastOffline format string
	lastOffline := ""
	if n.LastOffline != 0 {
		lastOffline = wkutil.ToyyyyMMddHHmm(time.Unix(n.LastOffline, 0))
	}
	status := ""
	if n.Status == pb.NodeStatus_NodeStatusDone {
		status = "已加入"
	} else if n.Status == pb.NodeStatus_NodeStatusWillJoin {
		status = "将加入"
	} else if n.Status == pb.NodeStatus_NodeStatusJoining {
		status = "加入中"
	}
	return &NodeConfig{
		Id:            n.Id,
		Role:          n.Role,
		ClusterAddr:   n.ClusterAddr,
		ApiServerAddr: n.ApiServerAddr,
		Online:        wkutil.BoolToInt(n.Online),
		OfflineCount:  int(n.OfflineCount),
		LastOffline:   lastOffline,
		AllowVote:     wkutil.BoolToInt(n.AllowVote),
		Exports:       convertSlotMigrate(n.Exports),
		ExportCount:   len(n.Exports),
		Imports:       convertSlotMigrate(n.Imports),
		ImportCount:   len(n.Imports),
		Status:        n.Status,
		StatusFormat:  status,
	}
}

type ChannelClusterConfigRespTotal struct {
	Total int                         `json:"total"` // 总数
	Data  []*ChannelClusterConfigResp `json:"data"`
}

type ChannelClusterConfigResp struct {
	ChannelID         string   `json:"channel_id"`          // 频道ID
	ChannelType       uint8    `json:"channel_type"`        // 频道类型
	ChannelTypeFormat string   `json:"channel_type_format"` // 频道类型格式化
	ReplicaCount      uint16   `json:"replica_count"`       // 副本数量
	Replicas          []uint64 `json:"replicas"`            // 副本节点ID集合
	LeaderId          uint64   `json:"leader_id"`           // 领导者ID
	Term              uint32   `json:"term"`                // 任期
	SlotId            uint32   `json:"slot_id"`             // 槽位ID
	SlotLeaderId      uint64   `json:"slot_leader_id"`      // 槽位领导者ID
	MaxMessageSeq     uint64   `json:"max_message_seq"`     // 最大消息序号
	LastAppendTime    string   `json:"last_append_time"`    // 最后一次追加时间
	Active            int      `json:"active"`              // 是否激活
	ActiveFormat      string   `json:"active_format"`       // 状态格式化
}

func NewChannelClusterConfigRespFromClusterConfig(slotLeaderId uint64, slotId uint32, cfg *wkstore.ChannelClusterConfig) *ChannelClusterConfigResp {

	channelTypeFormat := ""

	switch cfg.ChannelType {
	case wkproto.ChannelTypeGroup:
		channelTypeFormat = "群组"
	case wkproto.ChannelTypePerson:
		channelTypeFormat = "个人"
	case wkproto.ChannelTypeCommunity:
		channelTypeFormat = "社区"
	case wkproto.ChannelTypeCustomerService:
		channelTypeFormat = "客服"
	case wkproto.ChannelTypeInfo:
		channelTypeFormat = "资讯"
	case wkproto.ChannelTypeData:
		channelTypeFormat = "数据"
	default:
		channelTypeFormat = fmt.Sprintf("未知(%d)", cfg.ChannelType)
	}
	return &ChannelClusterConfigResp{
		ChannelID:         cfg.ChannelID,
		ChannelType:       cfg.ChannelType,
		ChannelTypeFormat: channelTypeFormat,
		ReplicaCount:      cfg.ReplicaCount,
		Replicas:          cfg.Replicas,
		LeaderId:          cfg.LeaderId,
		Term:              cfg.Term,
		SlotId:            slotId,
		SlotLeaderId:      slotLeaderId,
	}
}

type SlotClusterConfigResp struct {
	Id                uint32   `json:"id"`                   // 槽位ID
	LeaderId          uint64   `json:"leader_id"`            // 领导者ID
	Term              uint32   `json:"term"`                 // 任期
	Replicas          []uint64 `json:"replicas"`             // 副本节点ID集合
	ReplicaCount      uint32   `json:"replica_count"`        // 副本数量
	LogMaxIndex       uint64   `json:"log_max_index"`        // 本地日志最大索引
	LeaderLogMaxIndex uint64   `json:"leader_log_max_index"` // 领导者日志最大索引
	AppliedIndex      uint64   `json:"applied_index"`        // 已应用索引
}

func NewSlotClusterConfigRespFromClusterConfig(appliedIdx, logMaxIndex uint64, leaderLogMaxIndex uint64, slot *pb.Slot) *SlotClusterConfigResp {
	return &SlotClusterConfigResp{
		Id:                slot.Id,
		LeaderId:          slot.Leader,
		Term:              slot.Term,
		Replicas:          slot.Replicas,
		ReplicaCount:      slot.ReplicaCount,
		LogMaxIndex:       logMaxIndex,
		LeaderLogMaxIndex: leaderLogMaxIndex,
		AppliedIndex:      appliedIdx,
	}
}

type SlotRespTotal struct {
	Total int         `json:"total"` // 总数
	Data  []*SlotResp `json:"data"`  // 槽位信息
}

type SlotResp struct {
	Id           uint32   `json:"id"`
	LeaderId     uint64   `json:"leader_id"`
	Term         uint32   `json:"term"`
	Replicas     []uint64 `json:"replicas"`
	ReplicaCount uint32   `json:"replica_count"`
	ChannelCount int      `json:"channel_count"`
	LogIndex     uint64   `json:"log_index"`
}

func NewSlotResp(st *pb.Slot, channelCount int) *SlotResp {
	return &SlotResp{
		Id:           st.Id,
		LeaderId:     st.Leader,
		Term:         st.Term,
		Replicas:     st.Replicas,
		ReplicaCount: st.ReplicaCount,
		ChannelCount: channelCount,
	}
}

type SlotMigrate struct {
	Slot   uint32           `json:"slot_id"`
	From   uint64           `json:"from"`
	To     uint64           `json:"to"`
	Status pb.MigrateStatus `json:"status"`
}

func convertSlotMigrate(mg []*pb.SlotMigrate) []*SlotMigrate {
	res := make([]*SlotMigrate, 0, len(mg))
	for _, m := range mg {
		res = append(res, &SlotMigrate{
			Slot: m.Slot,
			From: m.From,
			To:   m.To,
		})
	}
	return res
}
