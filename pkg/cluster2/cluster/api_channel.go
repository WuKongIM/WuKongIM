package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/network"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func (s *Server) channelSearch(c *wkhttp.Context) {
	// 搜索条件
	limit := wkutil.ParseInt(c.Query("limit"))
	nodeId := wkutil.ParseUint64(c.Query("node_id"))
	channelId := strings.TrimSpace(c.Query("channel_id"))
	channelType := wkutil.ParseUint8(c.Query("channel_type"))
	offsetCreatedAt := wkutil.ParseInt64(c.Query("offset_created_at")) // 偏移的创建时间
	pre := wkutil.ParseInt(c.Query("pre"))                             // 是否向前搜索

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

	searchLocalChannelInfos := func() ([]*channelInfoResp, error) {
		channelInfos, err := s.db.SearchChannels(wkdb.ChannelSearchReq{
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
			OffsetCreatedAt:    offsetCreatedAt,
			Limit:              limit + 1, // 实际查询出来的数据比limit多1，用于判断是否有下一页
			Pre:                pre == 1,
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

	if nodeId == s.opts.ConfigOptions.NodeId {
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

	nodes := s.cfgServer.Nodes()

	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ReqTimeout)
	defer cancel()

	var channelInfoResps = make([]*channelInfoResp, 0)
	var channelInfoRespLock sync.Mutex
	requestGroup, _ := errgroup.WithContext(timeoutCtx)
	for _, node := range nodes {
		if node.Id == s.opts.ConfigOptions.NodeId {
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

	sort.Slice(channelInfoResps, func(i, j int) bool {

		return channelInfoResps[i].CreatedAt > channelInfoResps[j].CreatedAt
	})

	hasMore := false

	if len(channelInfoResps) > limit {
		hasMore = true
		if pre == 1 {
			channelInfoResps = channelInfoResps[len(channelInfoResps)-limit:]
		} else {
			channelInfoResps = channelInfoResps[:limit]
		}
	}

	c.JSON(http.StatusOK, channelInfoRespTotal{
		Data: channelInfoResps,
		More: wkutil.BoolToInt(hasMore),
	})

}

func (s *Server) requestNodeChannelSearch(path string, nodeId uint64, queryMap map[string]string, headers map[string]string) (*channelInfoRespTotal, error) {
	node := s.cfgServer.Node(nodeId)
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

	leaderNode, err := s.SlotLeaderOfChannel(channelId, channelType)
	if err != nil {
		s.Error("SlotLeaderOfChannel error", zap.Error(err), zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
		c.ResponseError(err)
		return
	}

	if leaderNode.Id != s.opts.ConfigOptions.NodeId {
		c.Forward(fmt.Sprintf("%s%s", leaderNode.ApiServerAddr, c.Request.URL.Path))
		return
	}
	subscribers, err := s.db.GetSubscribers(channelId, channelType)
	if err != nil {
		s.Error("GetSubscribers error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	uids := make([]string, 0, len(subscribers))
	for _, subscriber := range subscribers {
		uids = append(uids, subscriber.Uid)
	}
	c.JSON(http.StatusOK, uids)
}

func (s *Server) denylistGet(c *wkhttp.Context) {
	channelId := c.Param("channel_id")
	channelType := wkutil.ParseUint8(c.Param("channel_type"))

	leaderNode, err := s.SlotLeaderOfChannel(channelId, channelType)
	if err != nil {
		s.Error("SlotLeaderOfChannel error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if leaderNode.Id != s.opts.ConfigOptions.NodeId {
		c.Forward(fmt.Sprintf("%s%s", leaderNode.ApiServerAddr, c.Request.URL.Path))
		return
	}
	members, err := s.db.GetDenylist(channelId, channelType)
	if err != nil {
		s.Error("GetDenylist error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	uids := make([]string, 0, len(members))
	for _, member := range members {
		uids = append(uids, member.Uid)
	}
	c.JSON(http.StatusOK, uids)
}

func (s *Server) allowlistGet(c *wkhttp.Context) {
	channelId := c.Param("channel_id")
	channelType := wkutil.ParseUint8(c.Param("channel_type"))

	leaderNode, err := s.SlotLeaderOfChannel(channelId, channelType)
	if err != nil {
		s.Error("SlotLeaderOfChannel error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if leaderNode.Id != s.opts.ConfigOptions.NodeId {
		c.Forward(fmt.Sprintf("%s%s", leaderNode.ApiServerAddr, c.Request.URL.Path))
		return
	}
	allowlist, err := s.db.GetAllowlist(channelId, channelType)
	if err != nil {
		s.Error("GetAllowlist error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	uids := make([]string, 0, len(allowlist))
	for _, member := range allowlist {
		uids = append(uids, member.Uid)
	}
	c.JSON(http.StatusOK, uids)
}
