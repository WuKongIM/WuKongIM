package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
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

	//################### 黑明单 ###################// 删除频道
	r.POST("/channel/blacklist_add", ch.blacklistAdd)       // 添加黑明单
	r.POST("/channel/blacklist_set", ch.blacklistSet)       // 设置黑明单（覆盖原来的黑名单数据）
	r.POST("/channel/blacklist_remove", ch.blacklistRemove) // 移除黑名单

	//################### 白名单 ###################
	r.POST("/channel/whitelist_add", ch.whitelistAdd) // 添加白名单
	r.POST("/channel/whitelist_set", ch.whitelistSet) // 设置白明单（覆盖
	r.POST("/channel/whitelist_remove", ch.whitelistRemove)
	r.GET("/channel/whitelist", func(c *wkhttp.Context) {
		channelID := c.Query("channel_id")
		channel, err := ch.s.channelManager.GetChannel(channelID, wkproto.ChannelTypeGroup)
		if err != nil {
			c.ResponseError(err)
			return
		}
		if channel == nil {
			c.ResponseError(errors.New("频道不存在！"))
			return
		}
		whitelist := make([]string, 0)
		channel.whitelist.Range(func(key, value interface{}) bool {
			whitelist = append(whitelist, key.(string))
			return true
		})
		c.JSON(http.StatusOK, whitelist)
	})
	//################### 频道消息 ###################
	// 同步频道消息
	r.POST("/channel/messagesync", ch.syncMessages)

}

func (ch *ChannelAPI) channelCreateOrUpdate(c *wkhttp.Context) {
	var req ChannelCreateReq
	if err := c.BindJSON(&req); err != nil {
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
	// channelInfo := wkstore.NewChannelInfo(req.ChannelID, req.ChannelType)
	channelInfo := req.ToChannelInfo()

	err := ch.s.store.AddOrUpdateChannel(channelInfo)
	if err != nil {
		c.ResponseError(err)
		ch.Error("创建频道失败！", zap.Error(err))
		return
	}
	err = ch.s.store.RemoveAllSubscriber(req.ChannelID, req.ChannelType)
	if err != nil {
		ch.Error("移除所有订阅者失败！", zap.Error(err))
		c.ResponseError(errors.New("移除所有订阅者失败！"))
		return
	}
	if len(req.Subscribers) > 0 {
		err = ch.s.store.AddSubscribers(req.ChannelID, req.ChannelType, req.Subscribers)
		if err != nil {
			ch.Error("添加订阅者失败！", zap.Error(err))
			c.ResponseError(err)
			return
		}
	}
	ch.s.channelManager.DeleteChannelFromCache(req.ChannelID, req.ChannelType)
	c.ResponseOK()
}

// 更新或添加频道信息
func (ch *ChannelAPI) updateOrAddChannelInfo(c *wkhttp.Context) {
	var req ChannelInfoReq
	if err := c.BindJSON(&req); err != nil {
		ch.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(errors.New("数据格式有误！"))
		return
	}
	channelInfo := req.ToChannelInfo()
	err := ch.s.store.AddOrUpdateChannel(channelInfo)
	if err != nil {
		ch.Error("添加或更新频道信息失败！", zap.Error(err))
		c.ResponseError(errors.New("添加或更新频道信息失败！"))
		return
	}
	// 如果有缓存则更新缓存
	channel := ch.s.channelManager.getChannelFromCache(req.ChannelID, req.ChannelType)
	if channel != nil {
		channel.ChannelInfo = channelInfo
	}
	c.ResponseOK()
}

func (ch *ChannelAPI) addSubscriber(c *wkhttp.Context) {
	var req subscriberAddReq
	if err := c.BindJSON(&req); err != nil {
		ch.Error("数据格式有误！", zap.Error(err))
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
	if req.ChannelType == 0 {
		req.ChannelType = wkproto.ChannelTypeGroup //默认为群
	}
	channel, err := ch.s.channelManager.GetChannel(req.ChannelID, req.ChannelType)
	if err != nil {
		ch.Error("获取频道失败！", zap.String("channel", req.ChannelID), zap.Error(err))
		c.ResponseError(errors.Wrap(err, "获取频道失败！"))
		return
	}
	if channel == nil {
		ch.Error("频道不存在！", zap.String("channel_id", req.ChannelID), zap.Uint8("channel_type", req.ChannelType))
		c.ResponseError(errors.New("频道并不存在！"))
		return
	}
	if req.TempSubscriber == 1 {
		err = ch.addTmpSubscriberWithReq(req, channel)
		if err != nil {
			ch.Error("添加临时频道失败！", zap.Error(err))
			c.ResponseError(errors.New("添加临时频道失败！"))
			return
		}
	} else {
		err = ch.addSubscriberWithReq(req, channel)
		if err != nil {
			ch.Error("添加频道失败！", zap.Error(err))
			c.ResponseError(errors.New("添加频道失败！"))
			return
		}
	}
	c.ResponseOK()
}

func (ch *ChannelAPI) addTmpSubscriberWithReq(req subscriberAddReq, channel *Channel) error {
	if req.Reset == 1 {
		channel.RemoveAllTmpSubscriber()
	}

	channel.AddTmpSubscribers(req.Subscribers)

	return nil
}

func (ch *ChannelAPI) addSubscriberWithReq(req subscriberAddReq, channel *Channel) error {
	var err error
	existSubscribers := make([]string, 0)
	if req.Reset == 1 {
		err = ch.s.store.RemoveAllSubscriber(req.ChannelID, req.ChannelType)
		if err != nil {
			ch.Error("移除所有订阅者失败！", zap.Error(err))
			return err
		}
		channel.RemoveAllSubscriber()
	} else {

		existSubscribers, err = ch.s.store.GetSubscribers(req.ChannelID, req.ChannelType)
		if err != nil {
			ch.Error("获取所有订阅者失败！", zap.Error(err))
			return err
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
		err = ch.s.store.AddSubscribers(req.ChannelID, req.ChannelType, newSubscribers)
		if err != nil {
			ch.Error("添加订阅者失败！", zap.Error(err))
			return err
		}
		channel.AddSubscribers(newSubscribers)
	}

	return nil
}

func (ch *ChannelAPI) removeSubscriber(c *wkhttp.Context) {
	var req subscriberRemoveReq
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(errors.Wrap(err, "数据格式有误！"))
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}
	channel, err := ch.s.channelManager.GetChannel(req.ChannelID, req.ChannelType)
	if err != nil {
		ch.Error("获取频道失败！", zap.Error(err), zap.String("channelId", req.ChannelID))
		c.ResponseError(errors.Wrap(err, "获取频道失败！"))
		return
	}
	if channel == nil {
		ch.Error("频道不存在！", zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("频道不存在！"))
		return
	}

	if req.TempSubscriber == 1 {
		channel.RemoveTmpSubscribers(req.Subscribers)
	} else {
		err = ch.s.store.RemoveSubscribers(req.ChannelID, req.ChannelType, req.Subscribers)
		if err != nil {
			ch.Error("移除订阅者失败！", zap.Error(err))
			c.ResponseError(err)
			return
		}
		channel.RemoveSubscribers(req.Subscribers)
		err = ch.s.conversationManager.DeleteConversation(req.Subscribers, req.ChannelID, req.ChannelType)
		if err != nil {
			ch.Error("删除最近会话失败！", zap.Error(err))
			c.ResponseError(err)
			return
		}
	}

	c.ResponseOK()
}

func (ch *ChannelAPI) blacklistAdd(c *wkhttp.Context) {
	var req blacklistReq
	if err := c.BindJSON(&req); err != nil {
		ch.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}
	err := ch.s.store.AddDenylist(req.ChannelID, req.ChannelType, req.UIDs)
	if err != nil {
		ch.Error("添加黑名单失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	// 增加到缓存中
	channelObj, err := ch.s.channelManager.GetChannel(req.ChannelID, req.ChannelType)
	if err != nil {
		c.ResponseError(err)
		return
	}
	channelObj.AddDenylist(req.UIDs)

	c.ResponseOK()
}

func (ch *ChannelAPI) blacklistSet(c *wkhttp.Context) {
	var req blacklistReq
	if err := c.BindJSON(&req); err != nil {
		ch.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if strings.TrimSpace(req.ChannelID) == "" {
		c.ResponseError(errors.New("频道ID不能为空！"))
		return
	}
	err := ch.s.store.RemoveAllDenylist(req.ChannelID, req.ChannelType)
	if err != nil {
		ch.Error("移除所有黑明单失败！", zap.Error(err))
		c.ResponseError(errors.New("移除所有黑明单失败！"))
		return
	}
	if len(req.UIDs) > 0 {
		err := ch.s.store.AddDenylist(req.ChannelID, req.ChannelType, req.UIDs)
		if err != nil {
			ch.Error("添加黑名单失败！", zap.Error(err))
			c.ResponseError(err)
			return
		}
	}
	// 增加到缓存中
	channelObj, err := ch.s.channelManager.GetChannel(req.ChannelID, req.ChannelType)
	if err != nil {
		c.ResponseError(err)
		return
	}
	channelObj.SetDenylist(req.UIDs)

	c.ResponseOK()
}

func (ch *ChannelAPI) blacklistRemove(c *wkhttp.Context) {
	var req blacklistReq
	if err := c.BindJSON(&req); err != nil {
		ch.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}
	err := ch.s.store.RemoveDenylist(req.ChannelID, req.ChannelType, req.UIDs)
	if err != nil {
		ch.Error("移除黑名单失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	// 缓存中移除
	channelObj, err := ch.s.channelManager.GetChannel(req.ChannelID, req.ChannelType)
	if err != nil {
		c.ResponseError(err)
		return
	}
	channelObj.RemoveDenylist(req.UIDs)
	c.ResponseOK()
}

// 删除频道
func (ch *ChannelAPI) channelDelete(c *wkhttp.Context) {
	var req ChannelDeleteReq
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(errors.Wrap(err, "数据格式有误！"))
		return
	}

	err := ch.s.store.DeleteChannelAndClearMessages(req.ChannelID, req.ChannelType)
	if err != nil {
		c.ResponseError(err)
		return
	}

	ch.s.channelManager.DeleteChannel(req.ChannelID, req.ChannelType)
	c.ResponseOK()
}

// ----------- 白名单 -----------

// 添加白名单
func (ch *ChannelAPI) whitelistAdd(c *wkhttp.Context) {
	var req whitelistReq
	if err := c.BindJSON(&req); err != nil {
		ch.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}
	err := ch.s.store.AddAllowlist(req.ChannelID, req.ChannelType, req.UIDs)
	if err != nil {
		ch.Error("添加白名单失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	// 增加到缓存中
	channelObj, err := ch.s.channelManager.GetChannel(req.ChannelID, req.ChannelType)
	if err != nil {
		c.ResponseError(err)
		return
	}
	channelObj.AddAllowlist(req.UIDs)

	c.ResponseOK()
}
func (ch *ChannelAPI) whitelistSet(c *wkhttp.Context) {
	var req whitelistReq
	if err := c.BindJSON(&req); err != nil {
		ch.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if strings.TrimSpace(req.ChannelID) == "" {
		c.ResponseError(errors.New("频道ID不能为空！"))
		return
	}
	err := ch.s.store.RemoveAllAllowlist(req.ChannelID, req.ChannelType)
	if err != nil {
		ch.Error("移除所有白明单失败！", zap.Error(err))
		c.ResponseError(errors.New("移除所有白明单失败！"))
		return
	}
	if len(req.UIDs) > 0 {
		err := ch.s.store.AddAllowlist(req.ChannelID, req.ChannelType, req.UIDs)
		if err != nil {
			ch.Error("添加白名单失败！", zap.Error(err))
			c.ResponseError(err)
			return
		}
	}
	// 增加到缓存中
	channelObj, err := ch.s.channelManager.GetChannel(req.ChannelID, req.ChannelType)
	if err != nil {
		c.ResponseError(err)
		return
	}
	channelObj.SetAllowlist(req.UIDs)

	c.ResponseOK()
}

// 移除白名单
func (ch *ChannelAPI) whitelistRemove(c *wkhttp.Context) {
	var req whitelistReq
	if err := c.BindJSON(&req); err != nil {
		ch.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}
	err := ch.s.store.RemoveAllowlist(req.ChannelID, req.ChannelType, req.UIDs)
	if err != nil {
		ch.Error("移除白名单失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	// 缓存中移除
	channelObj, err := ch.s.channelManager.GetChannel(req.ChannelID, req.ChannelType)
	if err != nil {
		c.ResponseError(err)
		return
	}
	channelObj.RemoveAllowlist(req.UIDs)
	c.ResponseOK()
}

type PullMode int // 拉取模式

const (
	PullModeDown PullMode = iota // 向下拉取
	PullModeUp                   // 向上拉取
)

// 同步频道内的消息
func (ch *ChannelAPI) syncMessages(c *wkhttp.Context) {
	var req struct {
		LoginUID        string   `json:"login_uid"` // 当前登录用户的uid
		ChannelID       string   `json:"channel_id"`
		ChannelType     uint8    `json:"channel_type"`
		StartMessageSeq uint32   `json:"start_message_seq"` //开始消息列号（结果包含start_message_seq的消息）
		EndMessageSeq   uint32   `json:"end_message_seq"`   // 结束消息列号（结果不包含end_message_seq的消息）
		Limit           int      `json:"limit"`             // 每次同步数量限制
		PullMode        PullMode `json:"pull_mode"`         // 拉取模式 0:向下拉取 1:向上拉取
	}
	if err := c.BindJSON(&req); err != nil {
		ch.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(errors.New("数据格式有误！"))
		return
	}

	var (
		limit         = req.Limit
		fakeChannelID = req.ChannelID
		messages      []wkstore.Message
		err           error
	)

	if limit > 10000 {
		limit = 10000
	}

	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelID = GetFakeChannelIDWith(req.LoginUID, req.ChannelID)
	}

	fmt.Println("fakeChannelID:", fakeChannelID, "req.ChannelType:", req.ChannelType, "req.StartMessageSeq:", req.StartMessageSeq, "req.EndMessageSeq:", req.EndMessageSeq, "limit:", limit)

	if req.StartMessageSeq == 0 && req.EndMessageSeq == 0 {
		messages, err = ch.s.store.LoadLastMsgs(fakeChannelID, req.ChannelType, limit)
	} else if req.PullMode == PullModeUp { // 向上拉取
		messages, err = ch.s.store.LoadNextRangeMsgs(fakeChannelID, req.ChannelType, req.StartMessageSeq, req.EndMessageSeq, limit)
	} else {
		messages, err = ch.s.store.LoadPrevRangeMsgs(fakeChannelID, req.ChannelType, req.StartMessageSeq, req.EndMessageSeq, limit)
	}
	if err != nil {
		c.ResponseError(err)
		return
	}
	messageResps := make([]*MessageResp, 0, len(messages))
	if len(messages) > 0 {
		for _, message := range messages {
			messageResp := &MessageResp{}
			messageResp.from(message.(*Message), ch.s.store)
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
