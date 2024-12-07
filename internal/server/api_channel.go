package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	cluster "github.com/WuKongIM/WuKongIM/pkg/cluster/clusterserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

// ChannelAPI ChannelAPI
type ChannelAPI struct {
	s *Server
	wklog.Log
}

// NewChannelAPI 创建API
func NewChannelAPI(s *Server) *ChannelAPI {
	return &ChannelAPI{
		Log: wklog.NewWKLog("ChannelAPI"),
		s:   s,
	}
}

// Route Route
func (ch *ChannelAPI) Route(r *wkhttp.WKHttp) {
	//################### 频道 ###################
	r.POST("/channel", ch.channelCreateOrUpdate)       // 创建或修改频道
	r.POST("/channel/info", ch.updateOrAddChannelInfo) // 更新或添加频道基础信息
	r.POST("/channel/delete", ch.channelDelete)        // 删除频道

	//################### 订阅者 ###################// 删除频道
	r.POST("/channel/subscriber_add", ch.addSubscriber)       // 添加订阅者
	r.POST("/channel/subscriber_remove", ch.removeSubscriber) // 移除订阅者

	r.POST("/tmpchannel/subscriber_set", ch.setTmpSubscriber) // 临时频道设置订阅者

	//################### 黑名单 ###################// 删除频道
	r.POST("/channel/blacklist_add", ch.blacklistAdd)       // 添加黑名单
	r.POST("/channel/blacklist_set", ch.blacklistSet)       // 设置黑名单（覆盖原来的黑名单数据）
	r.POST("/channel/blacklist_remove", ch.blacklistRemove) // 移除黑名单

	//################### 白名单 ###################
	r.POST("/channel/whitelist_add", ch.whitelistAdd) // 添加白名单
	r.POST("/channel/whitelist_set", ch.whitelistSet) // 设置白明单（覆盖
	r.POST("/channel/whitelist_remove", ch.whitelistRemove)
	r.GET("/channel/whitelist", ch.whitelistGet) // 获取白名单
	//################### 频道消息 ###################
	// 同步频道消息
	r.POST("/channel/messagesync", ch.syncMessages)
	//	获取某个频道最大的消息序号
	r.GET("/channel/max_message_seq", ch.getChannelMaxMessageSeq)

}

func (ch *ChannelAPI) channelCreateOrUpdate(c *wkhttp.Context) {
	var req ChannelCreateReq
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
		c.ResponseError(errors.New("暂不支持个人频道！"))
		return
	}

	if ch.s.opts.ClusterOn() {
		leaderInfo, err := ch.s.cluster.SlotLeaderOfChannel(req.ChannelID, req.ChannelType) // 获取频道的槽领导节点
		if err != nil {
			ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
			c.ResponseError(errors.New("获取频道所在节点失败！"))
			return
		}
		leaderIsSelf := leaderInfo.Id == ch.s.opts.Cluster.NodeId
		if !leaderIsSelf {
			ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
			c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
			return
		}
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

	channelKey := wkutil.ChannelToKey(req.ChannelID, req.ChannelType)
	cacheChannel := ch.s.channelReactor.reactorSub(channelKey).channel(channelKey)
	if cacheChannel != nil {
		cacheChannel.info = channelInfo
	}

	c.ResponseOK()
}

// 更新或添加频道信息
func (ch *ChannelAPI) updateOrAddChannelInfo(c *wkhttp.Context) {
	var req ChannelInfoReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		ch.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(errors.New("数据格式有误！"))
		return
	}

	if ch.s.opts.ClusterOn() {
		leaderInfo, err := ch.s.cluster.SlotLeaderOfChannel(req.ChannelID, req.ChannelType) // 获取频道的领导节点
		if err != nil {
			ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
			c.ResponseError(errors.New("获取频道所在节点失败！"))
			return
		}
		leaderIsSelf := leaderInfo.Id == ch.s.opts.Cluster.NodeId
		if !leaderIsSelf {
			ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
			c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
			return
		}
	}

	channelInfo := req.ToChannelInfo()
	err = ch.addOrUpdateChannel(channelInfo)
	if err != nil {
		ch.Error("添加或更新频道信息失败！", zap.Error(err))
		c.ResponseError(errors.New("添加或更新频道信息失败！"))
		return
	}
	channelKey := wkutil.ChannelToKey(req.ChannelID, req.ChannelType)
	cacheChannel := ch.s.channelReactor.reactorSub(channelKey).channel(channelKey)
	if cacheChannel != nil {
		cacheChannel.info = channelInfo
	}
	c.ResponseOK()
}

func (ch *ChannelAPI) addSubscriber(c *wkhttp.Context) {

	fmt.Println("addSubscriber---->start...")

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
	leaderInfo, err := ch.s.cluster.SlotLeaderOfChannel(req.ChannelId, req.ChannelType) // 获取频道的领导节点
	if err != nil {
		ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == ch.s.opts.Cluster.NodeId
	if !leaderIsSelf {
		ch.Info("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)), zap.Uint64("leaderId ", leaderInfo.Id))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	exist, err := ch.s.store.ExistChannel(req.ChannelId, req.ChannelType)
	if err != nil {
		ch.Error("查询频道失败！", zap.Error(err))
		c.ResponseError(errors.New("查询频道失败！"))
		return
	}
	if !exist { // 如果没有频道则创建

		fmt.Println("AddChannelInfo....")

		channelInfo := wkdb.NewChannelInfo(req.ChannelId, req.ChannelType)
		err = ch.s.store.AddChannelInfo(channelInfo)
		if err != nil {
			ch.Error("创建频道失败！", zap.Error(err))
			c.ResponseError(errors.New("创建频道失败！"))
			return
		}
		fmt.Println("AddChannelInfo..end..")
	}

	err = ch.addSubscriberWithReq(req)
	if err != nil {
		ch.Error("添加频道失败！", zap.Error(err))
		c.ResponseError(errors.New("添加频道失败！"))
		return
	}
	c.ResponseOK()
}

func (ch *ChannelAPI) addSubscriberWithReq(req subscriberAddReq) error {
	var err error
	existSubscribers := make([]string, 0)
	if req.Reset == 1 {
		err = ch.s.store.RemoveAllSubscriber(req.ChannelId, req.ChannelType)
		if err != nil {
			ch.Error("移除所有订阅者失败！", zap.Error(err), zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
			return err
		}
	} else {
		members, err := ch.s.store.GetSubscribers(req.ChannelId, req.ChannelType)
		if err != nil {
			ch.Error("获取所有订阅者失败！", zap.Error(err), zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
			return err
		}
		for _, member := range members {
			existSubscribers = append(existSubscribers, member.Uid)
		}
	}
	newSubscribers := make([]string, 0, len(req.Subscribers))
	for _, subscriber := range req.Subscribers {
		if strings.TrimSpace(subscriber) == "" {
			continue
		}
		if !wkutil.ArrayContains(existSubscribers, subscriber) {
			newSubscribers = append(newSubscribers, subscriber)
		}
	}
	if len(newSubscribers) > 0 {
		lastMsgSeq, err := ch.s.store.GetLastMsgSeq(req.ChannelId, req.ChannelType)
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
		err = ch.s.store.AddSubscribers(req.ChannelId, req.ChannelType, members)
		if err != nil {
			ch.Error("添加订阅者失败！", zap.Error(err), zap.Int("members", len(members)), zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
			return err
		}

		conversations := make([]wkdb.Conversation, 0, len(newSubscribers))
		for _, subscriber := range newSubscribers {
			createdAt := time.Now()
			updatedAt := time.Now()
			conversations = append(conversations, wkdb.Conversation{
				Id:           ch.s.store.NextPrimaryKey(),
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
		err = ch.s.store.AddOrUpdateConversations(conversations)
		if err != nil {
			ch.Error("添加或更新会话失败！", zap.Error(err), zap.Int("conversations", len(conversations)), zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
			return err
		}

	}

	// 重新制止tag
	err = ch.makeAndCmdReceiverTag(req.ChannelId, req.ChannelType)
	if err != nil {
		ch.Error("创建接收者标签失败！", zap.Error(err))
		return err
	}
	return nil
}

func (ch *ChannelAPI) makeReceiverTag(channelId string, channelType uint8) error {
	cfg, err := ch.s.cluster.LoadOnlyChannelClusterConfig(channelId, channelType)
	if err != nil && err != cluster.ErrChannelClusterConfigNotFound {
		ch.Info("makeLocalReceiverTag: loadOnlyChannelClusterConfig failed")
		return nil
	}
	if err == cluster.ErrChannelClusterConfigNotFound { // 说明频道还没选举过，不存在被激活，这里无需去创建tag了
		return nil
	}

	if cfg.LeaderId == 0 { // 说明频道还没选举过，不存在被激活，这里无需去创建tag了
		return nil
	}

	// 如果在本节点，则重新make tag
	if ch.s.opts.IsLocalNode(cfg.LeaderId) {
		channelKey := wkutil.ChannelToKey(channelId, channelType)
		channel := ch.s.channelReactor.reactorSub(channelKey).channel(channelKey)
		if channel != nil {
			// 重新生成接收者标签
			_, err := channel.makeReceiverTag()
			if err != nil {
				ch.Error("创建接收者标签失败！", zap.Error(err))
				return err
			}
		}
		return nil
	}

	return ch.requestReceiverTag(channelId, channelType, cfg.LeaderId)

}

// cmd和普通频道
func (ch *ChannelAPI) makeAndCmdReceiverTag(channelId string, channelType uint8) error {

	// 普通频道的tag
	err := ch.makeReceiverTag(channelId, channelType)
	if err != nil {
		return err
	}

	// 普通频道对应的cmd频道的tag
	cmdChannelId := ch.s.opts.OrginalConvertCmdChannel(channelId)

	return ch.makeReceiverTag(cmdChannelId, channelType)

}

func (ch *ChannelAPI) requestReceiverTag(channelId string, channelType uint8, nodeId uint64) error {

	timeoutCtx, cancel := ch.s.WithRequestTimeout()
	defer cancel()

	req := channelReq{
		ChannelId:   channelId,
		ChannelType: channelType,
	}
	resp, err := ch.s.cluster.RequestWithContext(timeoutCtx, nodeId, "/wk/makeReceiverTag", req.Marshal())
	if err != nil {
		return err
	}

	if resp.Status != proto.StatusOK {
		return fmt.Errorf("requestReceiverTag: respose error status:%d", resp.Status)
	}
	return nil
}

func (ch *ChannelAPI) removeSubscriber(c *wkhttp.Context) {
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
	if ch.s.opts.ClusterOn() {
		leaderInfo, err := ch.s.cluster.SlotLeaderOfChannel(req.ChannelId, req.ChannelType) // 获取频道的领导节点
		if err != nil {
			ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
			c.ResponseError(errors.New("获取频道所在节点失败！"))
			return
		}
		leaderIsSelf := leaderInfo.Id == ch.s.opts.Cluster.NodeId
		if !leaderIsSelf {
			ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
			c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
			return
		}
	}

	err = ch.s.store.RemoveSubscribers(req.ChannelId, req.ChannelType, req.Subscribers)
	if err != nil {
		ch.Error("移除订阅者失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	err = ch.makeAndCmdReceiverTag(req.ChannelId, req.ChannelType)
	if err != nil {
		ch.Error("创建接收者标签失败！", zap.Error(err), zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(err)
		return
	}

	c.ResponseOK()
}

func (ch *ChannelAPI) setTmpSubscriber(c *wkhttp.Context) {
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

	if ch.s.opts.ClusterOn() {
		timeoutCtx, cancel := context.WithTimeout(ch.s.ctx, time.Second*5)
		leaderInfo, err := ch.s.cluster.LeaderOfChannel(timeoutCtx, req.ChannelId, wkproto.ChannelTypeTemp) // 获取频道的领导节点
		cancel()
		if err != nil {
			ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelId), zap.Uint8("channelType", wkproto.ChannelTypeTemp))
			c.ResponseError(errors.New("获取频道所在节点失败！"))
			return
		}
		leaderIsSelf := leaderInfo.Id == ch.s.opts.Cluster.NodeId
		if !leaderIsSelf {
			ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
			c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
			return
		}
	}

	setTmpSubscriberWithReq(ch.s, req)

	c.ResponseOK()
}

func setTmpSubscriberWithReq(s *Server, req tmpSubscriberSetReq) {
	channel := s.channelReactor.loadOrCreateChannel(req.ChannelId, wkproto.ChannelTypeTemp)
	channel.setTmpSubscribers(req.Uids)
}

func (ch *ChannelAPI) blacklistAdd(c *wkhttp.Context) {
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

	if ch.s.opts.ClusterOn() {
		leaderInfo, err := ch.s.cluster.SlotLeaderOfChannel(req.ChannelId, req.ChannelType) // 获取频道的领导节点
		if err != nil {
			ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
			c.ResponseError(errors.New("获取频道所在节点失败！"))
			return
		}
		leaderIsSelf := leaderInfo.Id == ch.s.opts.Cluster.NodeId
		if !leaderIsSelf {
			ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
			c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
			return
		}
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

	err = ch.s.store.AddDenylist(req.ChannelId, req.ChannelType, members)
	if err != nil {
		ch.Error("添加黑名单失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.ResponseOK()
}

func (ch *ChannelAPI) blacklistSet(c *wkhttp.Context) {
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

	if ch.s.opts.ClusterOn() {
		leaderInfo, err := ch.s.cluster.SlotLeaderOfChannel(req.ChannelId, req.ChannelType) // 获取频道的领导节点
		if err != nil {
			ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
			c.ResponseError(errors.New("获取频道所在节点失败！"))
			return
		}
		leaderIsSelf := leaderInfo.Id == ch.s.opts.Cluster.NodeId
		if !leaderIsSelf {
			ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
			c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
			return
		}
	}

	err = ch.s.store.RemoveAllDenylist(req.ChannelId, req.ChannelType)
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

		err := ch.s.store.AddDenylist(req.ChannelId, req.ChannelType, members)
		if err != nil {
			ch.Error("添加黑名单失败！", zap.Error(err))
			c.ResponseError(err)
			return
		}
	}

	c.ResponseOK()
}

func (ch *ChannelAPI) blacklistRemove(c *wkhttp.Context) {
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
	if ch.s.opts.ClusterOn() {
		leaderInfo, err := ch.s.cluster.SlotLeaderOfChannel(req.ChannelId, req.ChannelType) // 获取频道的领导节点
		if err != nil {
			ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
			c.ResponseError(errors.New("获取频道所在节点失败！"))
			return
		}
		leaderIsSelf := leaderInfo.Id == ch.s.opts.Cluster.NodeId

		if !leaderIsSelf {
			ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
			c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
			return
		}
	}
	err = ch.s.store.RemoveDenylist(req.ChannelId, req.ChannelType, req.UIDs)
	if err != nil {
		ch.Error("移除黑名单失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.ResponseOK()
}

// 删除频道
func (ch *ChannelAPI) channelDelete(c *wkhttp.Context) {
	var req ChannelDeleteReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		c.ResponseError(errors.Wrap(err, "数据格式有误！"))
		return
	}
	if req.ChannelType == wkproto.ChannelTypePerson {
		c.ResponseError(errors.New("个人频道不支持添加订阅者！"))
		return
	}
	if ch.s.opts.ClusterOn() {
		leaderInfo, err := ch.s.cluster.SlotLeaderOfChannel(req.ChannelId, req.ChannelType) // 获取频道的领导节点
		if err != nil {
			ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
			c.ResponseError(errors.New("获取频道所在节点失败！"))
			return
		}
		leaderIsSelf := leaderInfo.Id == ch.s.opts.Cluster.NodeId
		if !leaderIsSelf {
			ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
			c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
			return
		}
	}

	channelInfo, err := ch.s.store.GetChannel(req.ChannelId, req.ChannelType)
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
	err = ch.s.store.UpdateChannelInfo(channelInfo)
	if err != nil {
		ch.Error("更新频道信息失败！", zap.Error(err), zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("更新频道信息失败！"))
		return
	}

	err = ch.s.store.DeleteChannelAndClearMessages(req.ChannelId, req.ChannelType)
	if err != nil {
		c.ResponseError(err)
		return
	}

	c.ResponseOK()
}

// ----------- 白名单 -----------

// 添加白名单
func (ch *ChannelAPI) whitelistAdd(c *wkhttp.Context) {
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

	if ch.s.opts.ClusterOn() {
		leaderInfo, err := ch.s.cluster.SlotLeaderOfChannel(req.ChannelId, req.ChannelType) // 获取频道的领导节点
		if err != nil {
			ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
			c.ResponseError(errors.New("获取频道所在节点失败！"))
			return
		}
		leaderIsSelf := leaderInfo.Id == ch.s.opts.Cluster.NodeId
		if !leaderIsSelf {
			ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
			c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
			return
		}
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

	err = ch.s.store.AddAllowlist(req.ChannelId, req.ChannelType, members)
	if err != nil {
		ch.Error("添加白名单失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.ResponseOK()
}
func (ch *ChannelAPI) whitelistSet(c *wkhttp.Context) {
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

	if ch.s.opts.ClusterOn() {
		leaderInfo, err := ch.s.cluster.SlotLeaderOfChannel(req.ChannelId, req.ChannelType) // 获取频道的领导节点
		if err != nil {
			ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
			c.ResponseError(errors.New("获取频道所在节点失败！"))
			return
		}
		leaderIsSelf := leaderInfo.Id == ch.s.opts.Cluster.NodeId
		if !leaderIsSelf {
			ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
			c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
			return
		}
	}

	err = ch.s.store.RemoveAllAllowlist(req.ChannelId, req.ChannelType)
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
		err := ch.s.store.AddAllowlist(req.ChannelId, req.ChannelType, members)
		if err != nil {
			ch.Error("添加白名单失败！", zap.Error(err))
			c.ResponseError(err)
			return
		}
	}

	c.ResponseOK()
}

// 移除白名单
func (ch *ChannelAPI) whitelistRemove(c *wkhttp.Context) {
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
	if ch.s.opts.ClusterOn() {
		leaderInfo, err := ch.s.cluster.SlotLeaderOfChannel(req.ChannelId, req.ChannelType) // 获取频道的领导节点
		if err != nil {
			ch.Error("获取频道所在节点失败！", zap.Error(err), zap.Error(err), zap.String("channelId", req.ChannelId), zap.Uint8("channelType", req.ChannelType))
			c.ResponseError(errors.New("获取频道所在节点失败！"))
			return
		}
		leaderIsSelf := leaderInfo.Id == ch.s.opts.Cluster.NodeId
		if !leaderIsSelf {
			ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
			c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
			return
		}
	}

	err = ch.s.store.RemoveAllowlist(req.ChannelId, req.ChannelType, req.UIDs)
	if err != nil {
		ch.Error("移除白名单失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.ResponseOK()
}

func (ch *ChannelAPI) whitelistGet(c *wkhttp.Context) {
	channelId := c.Query("channel_id")
	channelType := wkutil.ParseUint8(c.Query("channel_type"))

	if ch.s.opts.ClusterOn() {
		leaderInfo, err := ch.s.cluster.SlotLeaderOfChannel(channelId, channelType) // 获取频道的领导节点
		if err != nil {
			ch.Error("获取频道所在节点失败！", zap.Error(err), zap.Error(err), zap.String("channelID", channelId), zap.Uint8("channelType", channelType))
			c.ResponseError(errors.New("获取频道所在节点失败！"))
			return
		}
		leaderIsSelf := leaderInfo.Id == ch.s.opts.Cluster.NodeId
		if !leaderIsSelf {
			ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
			c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), nil)
			return
		}
	}

	whitelist, err := ch.s.store.GetAllowlist(channelId, channelType)
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

// 同步频道内的消息
func (ch *ChannelAPI) syncMessages(c *wkhttp.Context) {

	var req struct {
		LoginUID        string   `json:"login_uid"` // 当前登录用户的uid
		ChannelID       string   `json:"channel_id"`
		ChannelType     uint8    `json:"channel_type"`
		StartMessageSeq uint64   `json:"start_message_seq"` //开始消息列号（结果包含start_message_seq的消息）
		EndMessageSeq   uint64   `json:"end_message_seq"`   // 结束消息列号（结果不包含end_message_seq的消息）
		Limit           int      `json:"limit"`             // 每次同步数量限制
		PullMode        PullMode `json:"pull_mode"`         // 拉取模式 0:向下拉取 1:向上拉取
	}
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		ch.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if strings.TrimSpace(req.ChannelID) == "" {
		ch.Error("channel_id不能为空！", zap.Any("req", req))
		c.ResponseError(errors.New("channel_id不能为空！"))
		return
	}
	if strings.TrimSpace(req.LoginUID) == "" {
		ch.Error("login_uid不能为空！", zap.Any("req", req))
		c.ResponseError(errors.New("login_uid不能为空！"))
		return
	}

	var (
		limit         = req.Limit
		fakeChannelID = req.ChannelID
		messages      []wkdb.Message
	)

	if limit > 10000 {
		limit = 10000
	}

	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelID = GetFakeChannelIDWith(req.LoginUID, req.ChannelID)
	}
	if ch.s.opts.ClusterOn() {
		leaderInfo, err := ch.s.cluster.LeaderOfChannelForRead(fakeChannelID, req.ChannelType) // 获取频道的领导节点
		if errors.Is(err, cluster.ErrChannelClusterConfigNotFound) {
			ch.Info("空频道，返回空消息.", zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
			c.JSON(http.StatusOK, emptySyncMessageResp)
			return
		}
		if err != nil {
			ch.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
			c.ResponseError(errors.New("获取频道所在节点失败！"))
			return
		}
		leaderIsSelf := leaderInfo.Id == ch.s.opts.Cluster.NodeId

		if !leaderIsSelf {
			ch.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
			c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
			return
		}
	}
	if req.StartMessageSeq == 0 && req.EndMessageSeq == 0 {
		messages, err = ch.s.store.LoadLastMsgs(fakeChannelID, req.ChannelType, limit)
	} else if req.PullMode == PullModeUp { // 向上拉取
		messages, err = ch.s.store.LoadNextRangeMsgs(fakeChannelID, req.ChannelType, req.StartMessageSeq, req.EndMessageSeq, limit)
	} else {
		messages, err = ch.s.store.LoadPrevRangeMsgs(fakeChannelID, req.ChannelType, req.StartMessageSeq, req.EndMessageSeq, limit)
	}
	if err != nil {
		ch.Error("获取消息失败！", zap.Error(err), zap.Any("req", req))
		c.ResponseError(err)
		return
	}
	messageResps := make([]*MessageResp, 0, len(messages))
	if len(messages) > 0 {
		for _, message := range messages {
			messageResp := &MessageResp{}
			messageResp.from(message, ch.s)
			messageResps = append(messageResps, messageResp)
		}
	}
	var more bool = true // 是否有更多数据
	if len(messageResps) < limit {
		more = false
	}
	if len(messageResps) > 0 {

		if req.PullMode == PullModeDown {
			if req.EndMessageSeq != 0 {
				messageSeq := messageResps[0].MessageSeq
				if req.EndMessageSeq == messageSeq {
					more = false
				}
			}
		} else {
			if req.EndMessageSeq != 0 {
				messageSeq := messageResps[len(messageResps)-1].MessageSeq
				if req.EndMessageSeq == messageSeq {
					more = false
				}
			}
		}
	}
	c.JSON(http.StatusOK, syncMessageResp{
		StartMessageSeq: req.StartMessageSeq,
		EndMessageSeq:   req.EndMessageSeq,
		More:            wkutil.BoolToInt(more),
		Messages:        messageResps,
	})
}

func (ch *ChannelAPI) getChannelMaxMessageSeq(c *wkhttp.Context) {
	channelId := c.Query("channel_id")
	channelType := wkutil.StringToUint8(c.Query("channel_type"))

	if channelId == "" {
		c.ResponseError(errors.New("channel_id不能为空"))
		return
	}

	leaderInfo, err := ch.s.cluster.LeaderOfChannelForRead(channelId, channelType)
	if err != nil && errors.Is(err, cluster.ErrChannelClusterConfigNotFound) {
		c.JSON(http.StatusOK, gin.H{
			"message_seq": 0,
		})
		return
	}
	if err != nil {
		c.ResponseError(err)
		return
	}

	if leaderInfo.Id != ch.s.opts.Cluster.NodeId {
		c.Forward(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path))
		return
	}

	msgSeq, err := ch.s.store.GetLastMsgSeq(channelId, channelType)
	if err != nil {
		c.ResponseError(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message_seq": msgSeq,
	})
}

func (ch *ChannelAPI) addOrUpdateChannel(channelInfo wkdb.ChannelInfo) error {
	existChannel, err := ch.s.store.GetChannel(channelInfo.ChannelId, channelInfo.ChannelType)
	if err != nil && err != wkdb.ErrNotFound {
		return err
	}

	if wkdb.IsEmptyChannelInfo(existChannel) {
		err = ch.s.store.AddChannelInfo(channelInfo)
		if err != nil {
			return err
		}
	} else {
		err = ch.s.store.UpdateChannelInfo(channelInfo)
		if err != nil {
			return err
		}
	}
	return nil
}
