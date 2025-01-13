package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func (s *Server) nodesGet(c *wkhttp.Context) {

	leaderId := s.cfgServer.LeaderId()

	nodeCfgs := make([]*NodeConfig, 0, len(s.cfgServer.Nodes()))

	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, time.Second*10)
	defer cancel()
	requestGroup, _ := errgroup.WithContext(timeoutCtx)

	cfg := s.cfgServer.GetClusterConfig()
	nodeCfgLock := sync.Mutex{}

	for _, node := range s.cfgServer.Nodes() {
		if node.Id == s.opts.ConfigOptions.NodeId {
			nodeCfg := s.getLocalNodeInfo()
			nodeCfgLock.Lock()
			nodeCfgs = append(nodeCfgs, nodeCfg)
			nodeCfgLock.Unlock()
			continue
		}
		if !node.Online {
			nodeCfg := NewNodeConfigFromNode(node)
			nodeCfg.IsLeader = wkutil.BoolToInt(leaderId == node.Id)
			nodeCfg.Term = cfg.Term
			nodeCfg.SlotCount = s.getNodeSlotCount(node.Id, cfg)
			nodeCfg.SlotLeaderCount = s.getNodeSlotLeaderCount(node.Id, cfg)
			nodeCfgLock.Lock()
			nodeCfgs = append(nodeCfgs, nodeCfg)
			nodeCfgLock.Unlock()
			continue
		}
		requestGroup.Go(func(nId uint64) func() error {
			return func() error {
				nodeCfg, err := s.requestNodeInfo(nId, c.CopyRequestHeader(c.Request))
				if err != nil {
					return err
				}
				nodeCfgLock.Lock()
				nodeCfgs = append(nodeCfgs, nodeCfg)
				nodeCfgLock.Unlock()
				return nil
			}
		}(node.Id))
	}
	err := requestGroup.Wait()
	if err != nil {
		c.ResponseError(err)
		return
	}

	sort.Slice(nodeCfgs, func(i, j int) bool {
		return nodeCfgs[i].Id < nodeCfgs[j].Id
	})

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
	cfg := s.cfgServer.GetClusterConfig()

	leaderId := s.cfgServer.LeaderId()

	nodeCfgs := make([]*NodeConfig, 0, len(cfg.Nodes))

	for _, node := range cfg.Nodes {
		nodeCfg := NewNodeConfigFromNode(node)
		nodeCfg.IsLeader = wkutil.BoolToInt(leaderId == node.Id)
		nodeCfg.SlotCount = s.getNodeSlotCount(node.Id, cfg)
		nodeCfg.SlotLeaderCount = s.getNodeSlotLeaderCount(node.Id, cfg)
		nodeCfgs = append(nodeCfgs, nodeCfg)
	}

	sort.Slice(nodeCfgs, func(i, j int) bool {
		return nodeCfgs[i].Id < nodeCfgs[j].Id
	})

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
	channelId := strings.TrimSpace(c.Query("channel_id"))
	channelType := wkutil.ParseUint8(c.Query("channel_type"))
	offsetCreatedAt := wkutil.ParseInt64(c.Query("offset_created_at"))
	pre := wkutil.ParseInt(c.Query("pre"))

	// running := wkutil.ParseBool(c.Query("running")) // 是否只查询运行中的频道

	if limit <= 0 {
		limit = s.opts.PageSize
	}

	node := s.cfgServer.Node(id)
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

	if node.Id != s.opts.ConfigOptions.NodeId {
		c.Forward(fmt.Sprintf("%s%s", node.ApiServerAddr, c.Request.URL.Path))
		return
	}

	channelClusterConfigs, err := s.db.SearchChannelClusterConfig(wkdb.ChannelClusterConfigSearchReq{
		ChannelId:       channelId,
		ChannelType:     channelType,
		OffsetCreatedAt: offsetCreatedAt,
		Pre:             pre == 1,
		Limit:           limit + 1, // 多查一个，用于判断是否有下一页
	}, func(cfg wkdb.ChannelClusterConfig) bool {
		slotId := s.getSlotId(cfg.ChannelId)
		slot := s.cfgServer.Slot(slotId)
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
		slot := s.cfgServer.Slot(slotId)
		if slot == nil {
			s.Error("slot not found", zap.Uint32("slotId", slotId))
			c.ResponseError(err)
			return
		}

		resp := NewChannelClusterConfigRespFromClusterConfig(slot.Leader, slot.Id, cfg)

		if cfg.LeaderId == s.opts.ConfigOptions.NodeId {
			channel := s.channelServer.Channel(cfg.ChannelId, cfg.ChannelType)
			if channel != nil {
				resp.Active = 1
				resp.ActiveFormat = "运行中"
				if channel.Suspend() {
					resp.ActiveFormat = "挂起"
				}
			} else {
				resp.Active = 0
				resp.ActiveFormat = "未运行"
			}
			lastMsgSeq, lastAppendTime, err := s.channelServer.LastIndexAndAppendTime(cfg.ChannelId, cfg.ChannelType)
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

	hasMore := false
	if len(channelClusterConfigResps) > limit {
		hasMore = true
		if pre == 1 {
			channelClusterConfigResps = channelClusterConfigResps[len(channelClusterConfigResps)-limit:]
		} else {
			channelClusterConfigResps = channelClusterConfigResps[:limit]
		}
	}

	c.JSON(http.StatusOK, ChannelClusterConfigRespTotal{
		Running: s.channelServer.ChannelCount(),
		More:    wkutil.BoolToInt(hasMore),
		Data:    channelClusterConfigResps,
	})
}
