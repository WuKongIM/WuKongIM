package api

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/ingress"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type channel struct {
	wklog.Log
	s *Server
}

// NewChannel 创建API
func newChannel(s *Server) *channel {
	return &channel{
		s:   s,
		Log: wklog.NewWKLog("Channel"),
	}
}

// Route Route
func (ch *channel) route(r *wkhttp.WKHttp) {
	//################### 频道 ###################
	r.POST("/channel", ch.channelCreateOrUpdate)       // 创建或修改频道
	r.POST("/channel/info", ch.updateOrAddChannelInfo) // 更新或添加频道基础信息
	r.POST("/channel/delete", ch.channelDelete)        // 删除频道

	//################### 订阅者 ###################// 删除频道
	r.POST("/channel/subscriber_add", ch.addSubscriber)       // 添加订阅者
	r.POST("/channel/subscriber_remove", ch.removeSubscriber) // 移除订阅者

	r.POST("/tmpchannel/subscriber_set", ch.setTmpSubscriber) // 临时频道设置订阅者(节点内部调用)

	//################### 黑名单 ###################// 删除频道
	r.POST("/channel/blacklist_add", ch.blacklistAdd)       // 添加黑名单
	r.POST("/channel/blacklist_set", ch.blacklistSet)       // 设置黑名单（覆盖原来的黑名单数据）
	r.POST("/channel/blacklist_remove", ch.blacklistRemove) // 移除黑名单

	//################### 白名单 ###################
	r.POST("/channel/whitelist_add", ch.whitelistAdd) // 添加白名单
	r.POST("/channel/whitelist_set", ch.whitelistSet) // 设置白明单（覆盖
	r.POST("/channel/whitelist_remove", ch.whitelistRemove)
	r.GET("/channel/whitelist", ch.whitelistGet) // 获取白名单

}

func (ch *channel) channelCreateOrUpdate(c *wkhttp.Context) {
	var req channelCreateReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		c.ResponseError(errors.Wrap(err, "数据格式有误！"))
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}

	if req.ChannelType == wkproto.ChannelTypePerson && len(req.Subscribers) > 0 {
		c.ResponseError(errors.New("不支持个人频道添加订阅者！"))
		return
	}

	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(req.ChannelID, req.ChannelType) // 获取频道的槽领导节点
	if err != nil {
		ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
	if !leaderIsSelf {
		ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	// channelInfo := wkstore.NewChannelInfo(req.ChannelID, req.ChannelType)
	channelInfo := req.ToChannelInfo()
	err = ch.addOrUpdateChannel(channelInfo)
	if err != nil && err != wkdb.ErrNotFound {
		ch.Error("创建或更新频道失败", zap.Error(err), zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("创建或更新频道失败"))
		return
	}

	// 添加订阅者
	err = ch.addSubscriberWithReq(subscriberAddReq{
		ChannelId:   req.ChannelID,
		ChannelType: req.ChannelType,
		Subscribers: req.Subscribers,
		Reset:       req.Reset,
	})
	if err != nil {
		ch.Error("添加订阅者失败！", zap.Error(err))
		c.ResponseError(errors.New("添加订阅者失败！"))
		return
	}

	c.ResponseOK()
}

// 更新或添加频道信息
func (ch *channel) updateOrAddChannelInfo(c *wkhttp.Context) {
	var req channelInfoReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		ch.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(errors.New("数据格式有误！"))
		return
	}

	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(req.ChannelID, req.ChannelType) // 获取频道的领导节点
	if err != nil {
		ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
	if !leaderIsSelf {
		ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	channelInfo := req.ToChannelInfo()
	err = ch.addOrUpdateChannel(channelInfo)
	if err != nil {
		ch.Error("添加或更新频道信息失败！", zap.Error(err))
		c.ResponseError(errors.New("添加或更新频道信息失败！"))
		return
	}
	c.ResponseOK()
}

func (ch *channel) addSubscriber(c *wkhttp.Context) {

	var req subscriberAddReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		c.ResponseError(errors.Wrap(err, "数据格式有误！"))
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}
	if req.ChannelType == wkproto.ChannelTypePerson {
		c.ResponseError(errors.New("个人频道不支持添加订阅者！"))
		return
	}

	if req.TempSubscriber == 1 {
		c.ResponseError(errors.New("新版本临时订阅者已不支持！"))
		return
	}

	if req.ChannelType == 0 {
		req.ChannelType = wkproto.ChannelTypeGroup //默认为群
	}
	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(req.ChannelId, req.ChannelType) // 获取频道的领导节点
	if err != nil {
		ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
	if !leaderIsSelf {
		ch.Info("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)), zap.Uint64("leaderId ", leaderInfo.Id))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	exist, err := service.Store.ExistChannel(req.ChannelId, req.ChannelType)
	if err != nil {
		ch.Error("查询频道失败！", zap.Error(err))
		c.ResponseError(errors.New("查询频道失败！"))
		return
	}
	if !exist { // 如果没有频道则创建

		channelInfo := wkdb.NewChannelInfo(req.ChannelId, req.ChannelType)
		err = service.Store.AddChannelInfo(channelInfo)
		if err != nil {
			ch.Error("创建频道失败！", zap.Error(err))
			c.ResponseError(errors.New("创建频道失败！"))
			return
		}
	}

	err = ch.addSubscriberWithReq(req)
	if err != nil {
		ch.Error("添加频道失败！", zap.Error(err))
		c.ResponseError(errors.New("添加频道失败！"))
		return
	}
	c.ResponseOK()
}

func (ch *channel) addSubscriberWithReq(req subscriberAddReq) error {
	var err error
	if req.Reset == 1 {
		err = service.Store.RemoveAllSubscriber(req.ChannelId, req.ChannelType)
		if err != nil {
			ch.Error("移除所有订阅者失败！", zap.Error(err), zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
			return err
		}
		tagKey := service.TagManager.GetChannelTag(req.ChannelId, req.ChannelType)
		if tagKey != "" {
			service.TagManager.RemoveTag(tagKey)
		}
	}

	newSubscribers := req.Subscribers

	if len(newSubscribers) > 0 {

		// TODO: 消息应该去频道的领导节点获取
		lastMsgSeq, err := service.Store.GetLastMsgSeq(req.ChannelId, req.ChannelType)
		if err != nil {
			ch.Error("获取最大消息序号失败！", zap.Error(err), zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
			return err
		}
		// 添加订阅者

		members := make([]wkdb.Member, 0, len(newSubscribers))
		createdAt := time.Now()
		updatedAt := time.Now()
		for _, subscriber := range newSubscribers {
			members = append(members, wkdb.Member{
				Uid:       subscriber,
				CreatedAt: &createdAt,
				UpdatedAt: &updatedAt,
			})
		}
		err = ch.addSubscribers(req.ChannelId, req.ChannelType, members)
		if err != nil {
			ch.Error("添加订阅者失败！", zap.Error(err), zap.Int("members", len(members)), zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
			return err
		}

		if req.ChannelType != wkproto.ChannelTypeLive { // 直播频道不添加会话
			conversations := make([]wkdb.Conversation, 0, len(newSubscribers))
			for _, subscriber := range newSubscribers {
				createdAt := time.Now()
				updatedAt := time.Now()
				conversations = append(conversations, wkdb.Conversation{
					Id:           service.Store.NextPrimaryKey(),
					Uid:          subscriber,
					ChannelId:    req.ChannelId,
					ChannelType:  req.ChannelType,
					Type:         wkdb.ConversationTypeChat,
					UnreadCount:  0,
					ReadToMsgSeq: lastMsgSeq,
					CreatedAt:    &createdAt,
					UpdatedAt:    &updatedAt,
				})
			}
			err = service.Store.AddOrUpdateConversations(conversations)
			if err != nil {
				ch.Error("添加或更新会话失败！", zap.Error(err), zap.Int("conversations", len(conversations)), zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
				return err
			}
		}

		err = ch.updateTagBySubscribers(req.ChannelId, req.ChannelType, newSubscribers, false)
		if err != nil {
			ch.Error("更新tag失败！", zap.Error(err))
			return err
		}
	}

	return nil
}

func (ch *channel) addSubscribers(channelId string, channelType uint8, members []wkdb.Member) error {
	err := service.Store.AddSubscribers(channelId, channelType, members)
	if err != nil {
		ch.Error("添加订阅者失败！", zap.Error(err), zap.Int("members", len(members)), zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
		return err
	}
	return nil
}

// // cmd和普通频道
func (ch *channel) updateTagBySubscribers(channelId string, channelType uint8, subscribers []string, remove bool) error {

	updateTag := func(chId string, chType uint8) {
		tagKey := service.TagManager.GetChannelTag(chId, chType)
		if tagKey != "" {
			if service.TagManager.Exist(tagKey) {
				newTagKey := wkutil.GenUUID()
				if remove {
					err := service.TagManager.RemoveUsers(tagKey, subscribers)
					if err != nil {
						ch.Error("updateTagByAddSubscribers: removeUsers failed", zap.Error(err))
						return
					}
				} else {
					err := service.TagManager.AddUsers(tagKey, subscribers)
					if err != nil {
						ch.Error("updateTagByAddSubscribers: addUsers failed", zap.Error(err))
						return
					}
				}

				err := service.TagManager.RenameTag(tagKey, newTagKey)
				if err != nil {
					ch.Error("updateTagByAddSubscribers: renameTag failed", zap.Error(err))
					return
				}
				service.TagManager.SetChannelTag(chId, chType, newTagKey)
			}
		}
	}

	// 获取频道的领导节点
	leaderId, err := service.Cluster.LeaderIdOfChannel(channelId, channelType)
	if err != nil {
		ch.Error("updateTagByAddSubscribers: get leader id failed", zap.Error(err))
		return err
	}
	if leaderId == 0 {
		ch.Error("updateTagByAddSubscribers: leader id is 0")
		return nil
	}

	if options.G.IsLocalNode(leaderId) {
		updateTag(channelId, channelType)
	} else {
		err = ch.s.client.UpdateTag(leaderId, &ingress.TagUpdateReq{
			ChannelId:   channelId,
			ChannelType: channelType,
			Uids:        subscribers,
			Remove:      remove,
			ChannelTag:  true,
		})
		if err != nil {
			ch.Error("updateTagByAddSubscribers: updateOrMakeTag failed", zap.Error(err))
			return err
		}
	}

	// 更新cmd频道的tag
	cmdChannelId := options.G.OrginalConvertCmdChannel(channelId)
	// 获取或请求cmd频道的分布式配置
	cfg, err := service.Cluster.LoadOnlyChannelClusterConfig(cmdChannelId, channelType)
	if err != nil && err != wkdb.ErrNotFound {
		ch.Info("updateTagByAddSubscribers: loadOnlyChannelClusterConfig failed", zap.Error(err))
		return nil
	}
	if cfg.LeaderId == 0 { // 说明频道还没选举过，不存在被激活，这里无需去创建tag了
		return nil
	}
	if options.G.IsLocalNode(cfg.LeaderId) {
		updateTag(cmdChannelId, channelType)
	} else {
		err = ch.s.client.UpdateTag(cfg.LeaderId, &ingress.TagUpdateReq{
			ChannelId:   cmdChannelId,
			ChannelType: channelType,
			Uids:        subscribers,
			Remove:      remove,
			ChannelTag:  true,
		})
		if err != nil {
			ch.Error("updateTagByAddSubscribers: updateOrMakeTag failed", zap.Error(err))
			return err
		}
	}

	return nil
}

// func (ch *Channel) makeReceiverTag(channelId string, channelType uint8) error {
// 	cfg, err := service.Cluster.LoadOnlyChannelClusterConfig(channelId, channelType)
// 	if err != nil && err != cluster.ErrChannelClusterConfigNotFound {
// 		ch.Info("makeLocalReceiverTag: loadOnlyChannelClusterConfig failed")
// 		return nil
// 	}
// 	if err == cluster.ErrChannelClusterConfigNotFound { // 说明频道还没选举过，不存在被激活，这里无需去创建tag了
// 		return nil
// 	}

// 	if cfg.LeaderId == 0 { // 说明频道还没选举过，不存在被激活，这里无需去创建tag了
// 		return nil
// 	}

// 	// 如果在本节点，则重新make tag
// 	if ch.s.opts.IsLocalNode(cfg.LeaderId) {
// 		channelKey := wkutil.ChannelToKey(channelId, channelType)
// 		channel := ch.s.channelReactor.reactorSub(channelKey).channel(channelKey)
// 		if channel != nil {
// 			// 重新生成接收者标签
// 			_, err := channel.makeReceiverTag()
// 			if err != nil {
// 				ch.Error("创建接收者标签失败！", zap.Error(err))
// 				return err
// 			}
// 		}
// 		return nil
// 	}

// 	return ch.requestReceiverTag(channelId, channelType, cfg.LeaderId)

// }

// // cmd和普通频道
// func (ch *Channel) makeAndCmdReceiverTag(channelId string, channelType uint8) error {

// 	// 普通频道的tag
// 	err := ch.makeReceiverTag(channelId, channelType)
// 	if err != nil {
// 		return err
// 	}

// 	// 普通频道对应的cmd频道的tag
// 	cmdChannelId := ch.s.opts.OrginalConvertCmdChannel(channelId)

// 	return ch.makeReceiverTag(cmdChannelId, channelType)

// }

func (ch *channel) removeSubscriber(c *wkhttp.Context) {
	var req subscriberRemoveReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		c.ResponseError(errors.Wrap(err, "数据格式有误！"))
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}
	if req.ChannelType == wkproto.ChannelTypePerson {
		c.ResponseError(errors.New("个人频道不支持添加订阅者！"))
		return
	}
	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(req.ChannelId, req.ChannelType) // 获取频道的领导节点
	if err != nil {
		ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
	if !leaderIsSelf {
		ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}
	err = service.Store.RemoveSubscribers(req.ChannelId, req.ChannelType, req.Subscribers)
	if err != nil {
		ch.Error("移除订阅者失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	// 删除订阅者的会话缓存
	if req.ChannelType != wkproto.ChannelTypeLive { // 直播频道不处理最近会话
		for _, subscriber := range req.Subscribers {
			err = service.ConversationManager.DeleteFromCache(subscriber, req.ChannelId, req.ChannelType)
			if err != nil {
				ch.Error("删除订阅者的会话失败！", zap.Error(err))
				c.ResponseError(err)
				return
			}

			// 删除订阅者的会话
			err = service.Store.DeleteConversation(subscriber, req.ChannelId, req.ChannelType)
			if err != nil {
				ch.Error("删除订阅者的会话失败！", zap.Error(err))
				c.ResponseError(err)
				return
			}
		}
	}

	err = ch.updateTagBySubscribers(req.ChannelId, req.ChannelType, req.Subscribers, true)
	if err != nil {
		ch.Error("removeSubscriber: update tag failed", zap.Error(err))
		c.ResponseError(errors.New("更新tag失败！"))
		return
	}
	c.ResponseOK()
}

func (ch *channel) setTmpSubscriber(c *wkhttp.Context) {
	var req tmpSubscriberSetReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		c.ResponseError(errors.Wrap(err, "数据格式有误！"))
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}

	leaderInfo, err := service.Cluster.LeaderOfChannel(req.ChannelId, wkproto.ChannelTypeTemp) // 获取频道的领导节点
	if err != nil {
		ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelId), zap.Uint8("channelType", wkproto.ChannelTypeTemp))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
	if !leaderIsSelf {
		ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	err = setTmpSubscriberWithReq(req)
	if err != nil {
		c.ResponseError(err)
		return
	}

	c.ResponseOK()
}

func setTmpSubscriberWithReq(req tmpSubscriberSetReq) error {
	tag, err := service.TagManager.MakeTag(req.Uids)
	if err != nil {
		return err
	}
	service.TagManager.SetChannelTag(req.ChannelId, wkproto.ChannelTypeTemp, tag.Key)
	return nil
}

func (ch *channel) blacklistAdd(c *wkhttp.Context) {
	var req blacklistReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		ch.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}
	if len(req.UIDs) == 0 {
		c.ResponseError(errors.New("uids不能为空！"))
		return
	}
	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(req.ChannelId, req.ChannelType) // 获取频道的领导节点
	if err != nil {
		ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
	if !leaderIsSelf {
		ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	members := make([]wkdb.Member, 0, len(req.UIDs))
	createdAt := time.Now()
	updatedAt := time.Now()
	for _, uid := range req.UIDs {
		members = append(members, wkdb.Member{
			Uid:       uid,
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		})
	}

	err = service.Store.AddDenylist(req.ChannelId, req.ChannelType, members)
	if err != nil {
		ch.Error("添加黑名单失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	// 删除黑名单的会话缓存
	if req.ChannelType != wkproto.ChannelTypeLive { // 直播频道不处理最近会话
		for _, uid := range req.UIDs {
			err = service.ConversationManager.DeleteFromCache(uid, req.ChannelId, req.ChannelType)
			if err != nil {
				ch.Error("删除订阅者的会话失败！", zap.Error(err))
				c.ResponseError(err)
				return
			}
			// 删除订阅者的会话
			err = service.Store.DeleteConversation(uid, req.ChannelId, req.ChannelType)
			if err != nil {
				ch.Error("删除订阅者的会话失败！", zap.Error(err))
				c.ResponseError(err)
				return
			}
		}
	}

	c.ResponseOK()
}

func (ch *channel) blacklistSet(c *wkhttp.Context) {
	var req blacklistReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		ch.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if strings.TrimSpace(req.ChannelId) == "" {
		c.ResponseError(errors.New("频道ID不能为空！"))
		return
	}
	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(req.ChannelId, req.ChannelType) // 获取频道的领导节点
	if err != nil {
		ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
	if !leaderIsSelf {
		ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	err = service.Store.RemoveAllDenylist(req.ChannelId, req.ChannelType)
	if err != nil {
		ch.Error("移除所有黑名单失败！", zap.Error(err))
		c.ResponseError(errors.New("移除所有黑名单失败！"))
		return
	}
	if len(req.UIDs) > 0 {

		members := make([]wkdb.Member, 0, len(req.UIDs))
		createdAt := time.Now()
		updatedAt := time.Now()
		for _, uid := range req.UIDs {
			members = append(members, wkdb.Member{
				Uid:       uid,
				CreatedAt: &createdAt,
				UpdatedAt: &updatedAt,
			})
		}

		err := service.Store.AddDenylist(req.ChannelId, req.ChannelType, members)
		if err != nil {
			ch.Error("添加黑名单失败！", zap.Error(err))
			c.ResponseError(err)
			return
		}

		// 删除黑名单的会话缓存
		if req.ChannelType != wkproto.ChannelTypeLive { // 直播频道不处理最近会话
			for _, uid := range req.UIDs {
				err = service.ConversationManager.DeleteFromCache(uid, req.ChannelId, req.ChannelType)
				if err != nil {
					ch.Error("删除订阅者的会话失败！", zap.Error(err))
					c.ResponseError(err)
					return
				}
				// 删除订阅者的会话
				err = service.Store.DeleteConversation(uid, req.ChannelId, req.ChannelType)
				if err != nil {
					ch.Error("删除订阅者的会话失败！", zap.Error(err))
					c.ResponseError(err)
					return
				}
			}
		}
	}

	c.ResponseOK()
}

func (ch *channel) blacklistRemove(c *wkhttp.Context) {
	var req blacklistReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		ch.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}
	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(req.ChannelId, req.ChannelType) // 获取频道的领导节点
	if err != nil {
		ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId

	if !leaderIsSelf {
		ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}
	err = service.Store.RemoveDenylist(req.ChannelId, req.ChannelType, req.UIDs)
	if err != nil {
		ch.Error("移除黑名单失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	// 增加黑频道的会话
	if req.ChannelType != wkproto.ChannelTypeLive { // 直播频道不处理最近会话
		createdAt := time.Now()
		updatedAt := time.Now()
		for _, uid := range req.UIDs {
			err = service.Store.AddConversationsIfNotExist([]wkdb.Conversation{
				{
					Uid:         uid,
					Type:        wkdb.ConversationTypeChat,
					ChannelId:   req.ChannelId,
					ChannelType: req.ChannelType,
					CreatedAt:   &createdAt,
					UpdatedAt:   &updatedAt,
				},
			})
			if err != nil {
				ch.Error("添加会话失败！", zap.Error(err))
				c.ResponseError(err)
				return
			}
		}
	}

	c.ResponseOK()
}

// 删除频道
func (ch *channel) channelDelete(c *wkhttp.Context) {
	var req channelDeleteReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		c.ResponseError(errors.Wrap(err, "数据格式有误！"))
		return
	}
	if req.ChannelType == wkproto.ChannelTypePerson {
		c.ResponseError(errors.New("个人频道不支持添加订阅者！"))
		return
	}
	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(req.ChannelId, req.ChannelType) // 获取频道的领导节点
	if err != nil {
		ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
	if !leaderIsSelf {
		ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	channelInfo, err := service.Store.GetChannel(req.ChannelId, req.ChannelType)
	if err != nil {
		ch.Error("查询频道信息失败！", zap.Error(err))
		c.ResponseError(errors.New("查询频道信息失败！"))
		return
	}
	if wkdb.IsEmptyChannelInfo(channelInfo) {
		ch.Warn("频道不存在！", zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.ResponseOK()
		return
	}

	// 解散频道
	channelInfo.Disband = true

	// 更新频道资料
	err = service.Store.UpdateChannelInfo(channelInfo)
	if err != nil {
		ch.Error("更新频道信息失败！", zap.Error(err), zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("更新频道信息失败！"))
		return
	}

	err = service.Store.DeleteChannelAndClearMessages(req.ChannelId, req.ChannelType)
	if err != nil {
		c.ResponseError(err)
		return
	}

	c.ResponseOK()
}

// ----------- 白名单 -----------

// 添加白名单
func (ch *channel) whitelistAdd(c *wkhttp.Context) {
	var req whitelistReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		ch.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}
	if len(req.UIDs) == 0 {
		c.ResponseError(errors.New("uids不能为空！"))
		return
	}

	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(req.ChannelId, req.ChannelType) // 获取频道的领导节点
	if err != nil {
		ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
	if !leaderIsSelf {
		ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	members := make([]wkdb.Member, 0, len(req.UIDs))
	createdAt := time.Now()
	updatedAt := time.Now()
	for _, uid := range req.UIDs {
		members = append(members, wkdb.Member{
			Uid:       uid,
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		})
	}

	err = service.Store.AddAllowlist(req.ChannelId, req.ChannelType, members)
	if err != nil {
		ch.Error("添加白名单失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if req.ChannelType == wkproto.ChannelTypePerson {
		for _, uid := range req.UIDs {
			if uid == req.ChannelId {
				continue
			}
			fakeChannelId := options.GetFakeChannelIDWith(uid, req.ChannelId)
			err = service.Store.AddConversationsIfNotExist([]wkdb.Conversation{
				{
					Uid:         uid,
					Type:        wkdb.ConversationTypeChat,
					ChannelId:   fakeChannelId,
					ChannelType: req.ChannelType,
					CreatedAt:   &createdAt,
					UpdatedAt:   &updatedAt,
				},
			})
			if err != nil {
				ch.Error("添加会话失败！", zap.Error(err))
				c.ResponseError(err)
				return
			}
		}
	}

	c.ResponseOK()
}
func (ch *channel) whitelistSet(c *wkhttp.Context) {
	var req whitelistReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		ch.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if strings.TrimSpace(req.ChannelId) == "" {
		c.ResponseError(errors.New("频道ID不能为空！"))
		return
	}

	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(req.ChannelId, req.ChannelType) // 获取频道的领导节点
	if err != nil {
		ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
	if !leaderIsSelf {
		ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	err = service.Store.RemoveAllAllowlist(req.ChannelId, req.ChannelType)
	if err != nil {
		ch.Error("移除所有白明单失败！", zap.Error(err))
		c.ResponseError(errors.New("移除所有白明单失败！"))
		return
	}
	if len(req.UIDs) > 0 {
		members := make([]wkdb.Member, 0, len(req.UIDs))
		createdAt := time.Now()
		updatedAt := time.Now()
		for _, uid := range req.UIDs {
			members = append(members, wkdb.Member{
				Uid:       uid,
				CreatedAt: &createdAt,
				UpdatedAt: &updatedAt,
			})
		}
		err := service.Store.AddAllowlist(req.ChannelId, req.ChannelType, members)
		if err != nil {
			ch.Error("添加白名单失败！", zap.Error(err))
			c.ResponseError(err)
			return
		}
	}

	c.ResponseOK()
}

// 移除白名单
func (ch *channel) whitelistRemove(c *wkhttp.Context) {
	var req whitelistReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		ch.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}
	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(req.ChannelId, req.ChannelType) // 获取频道的领导节点
	if err != nil {
		ch.Error("获取频道所在节点失败！", zap.Error(err), zap.Error(err), zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
	if !leaderIsSelf {
		ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	err = service.Store.RemoveAllowlist(req.ChannelId, req.ChannelType, req.UIDs)
	if err != nil {
		ch.Error("移除白名单失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.ResponseOK()
}

func (ch *channel) whitelistGet(c *wkhttp.Context) {
	channelId := c.Query("channel_id")
	channelType := wkutil.ParseUint8(c.Query("channel_type"))

	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(channelId, channelType) // 获取频道的领导节点
	if err != nil {
		ch.Error("获取频道所在节点失败！", zap.Error(err), zap.Error(err), zap.String("channelID", channelId), zap.Uint8("channelType", channelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
	if !leaderIsSelf {
		ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), nil)
		return
	}

	whitelist, err := service.Store.GetAllowlist(channelId, channelType)
	if err != nil {
		ch.Error("获取白名单失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.JSON(http.StatusOK, whitelist)
}

type PullMode int // 拉取模式

const (
	PullModeDown PullMode = iota // 向下拉取
	PullModeUp                   // 向上拉取
)

func BindJSON(obj any, c *wkhttp.Context) ([]byte, error) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, err
	}
	if err := wkutil.ReadJSONByByte(bodyBytes, obj); err != nil {
		return nil, err
	}
	return bodyBytes, nil
}

func (ch *channel) addOrUpdateChannel(channelInfo wkdb.ChannelInfo) error {
	existChannel, err := service.Store.GetChannel(channelInfo.ChannelId, channelInfo.ChannelType)
	if err != nil && err != wkdb.ErrNotFound {
		return err
	}
	if wkdb.IsEmptyChannelInfo(existChannel) {
		err = service.Store.AddChannelInfo(channelInfo)
		if err != nil {
			return err
		}
	} else {
		err = service.Store.UpdateChannelInfo(channelInfo)
		if err != nil {
			return err
		}
	}
	return nil
}
