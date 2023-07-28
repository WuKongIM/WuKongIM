package server

import (
	"fmt"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type Channel struct {
	*wkstore.ChannelInfo
	blacklist     sync.Map // 黑名单
	whitelist     sync.Map // 白名单
	subscriberMap sync.Map // 订阅者
	s             *Server
	wklog.Log
	tmpSubscriberMap sync.Map // 临时订阅者
}

// NewChannel NewChannel
func NewChannel(channelInfo *wkstore.ChannelInfo, s *Server) *Channel {

	return &Channel{
		ChannelInfo:      channelInfo,
		blacklist:        sync.Map{},
		whitelist:        sync.Map{},
		subscriberMap:    sync.Map{},
		tmpSubscriberMap: sync.Map{},
		s:                s,
		Log:              wklog.NewWKLog(fmt.Sprintf("channel[%s-%d]", channelInfo.ChannelID, channelInfo.ChannelType)),
	}
}

// LoadData load data
func (c *Channel) LoadData() error {
	if err := c.s.systemUIDManager.LoadIfNeed(); err != nil { // 加载系统账号
		return err
	}
	if err := c.initChannelInfo(); err != nil {
		return err
	}
	if err := c.initSubscribers(); err != nil { // 初始化订阅者
		return err
	}
	if err := c.initBlacklist(); err != nil { // 初始化黑名单
		return err
	}
	if err := c.initWhitelist(); err != nil { // 初始化白名单
		return err
	}
	return nil
}

func (c *Channel) initChannelInfo() error {
	if !c.s.opts.Datasource.ChannelInfoOn {
		return nil
	}
	if !c.s.opts.HasDatasource() {
		return nil
	}
	if c.ChannelType == wkproto.ChannelTypePerson {
		return nil
	}
	channelInfo, err := c.s.datasource.GetChannelInfo(c.ChannelID, c.ChannelType)
	if err != nil {
		c.Error("从数据获取频道信息失败！", zap.Error(err))
		return err
	}
	if channelInfo != nil {
		c.ChannelInfo = channelInfo
		return nil
	}
	return nil
}

// 初始化订阅者
func (c *Channel) initSubscribers() error {
	channelID := c.ChannelID
	channelType := c.ChannelType
	if c.s.opts.HasDatasource() && channelType != wkproto.ChannelTypePerson {
		subscribers, err := c.s.datasource.GetSubscribers(channelID, channelType)
		if err != nil {
			c.Error("从数据源获取频道订阅者失败！", zap.Error(err))
			return err
		}
		if len(subscribers) > 0 {
			for _, subscriber := range subscribers {
				c.AddSubscriber(subscriber)
			}
		}
	} else {
		// ---------- 订阅者  ----------
		subscribers, err := c.s.store.GetSubscribers(channelID, channelType)
		if err != nil {
			c.Error("获取频道订阅者失败！", zap.Error(err))
			return err
		}
		if len(subscribers) > 0 {
			for _, subscriber := range subscribers {
				c.AddSubscriber(subscriber)
			}
		}
	}
	if channelType == wkproto.ChannelTypeCustomerService {
		visitorID, _ := c.s.opts.GetCustomerServiceVisitorUID(channelID)
		if visitorID != "" {
			c.AddSubscriber(visitorID)
		}
	}

	return nil
}

// 初始化黑名单
func (c *Channel) initBlacklist() error {
	var blacklists []string
	var err error
	if c.s.opts.HasDatasource() {
		blacklists, err = c.s.datasource.GetBlacklist(c.ChannelID, c.ChannelType)
		if err != nil {
			c.Error("从数据源获取黑名单失败！", zap.Error(err))
			return err
		}
	} else {
		blacklists, err = c.s.store.GetDenylist(c.ChannelID, c.ChannelType)
		if err != nil {
			c.Error("获取黑名单失败！", zap.Error(err))
			return err
		}
	}
	if len(blacklists) > 0 {
		for _, uid := range blacklists {
			c.blacklist.Store(uid, true)
		}
	}
	return nil
}

// 初始化黑名单
func (c *Channel) initWhitelist() error {
	var whitelists []string
	var err error
	if c.s.opts.HasDatasource() {
		whitelists, err = c.s.datasource.GetWhitelist(c.ChannelID, c.ChannelType)
		if err != nil {
			c.Error("从数据源获取白名单失败！", zap.Error(err))
			return err
		}
	} else {
		whitelists, err = c.s.store.GetAllowlist(c.ChannelID, c.ChannelType)
		if err != nil {
			c.Error("获取白名单失败！", zap.Error(err))
			return err
		}
	}
	if len(whitelists) > 0 {
		for _, uid := range whitelists {
			c.whitelist.Store(uid, true)
		}
	}
	return nil
}

// 获取父频道
func (c *Channel) parentChannel() (*Channel, error) {
	var parentChannel *Channel
	if c.ChannelType == wkproto.ChannelTypeCommunityTopic {
		parentChannelID := GetCommunityTopicParentChannelID(c.ChannelID)
		if parentChannelID != "" {
			var err error
			parentChannel, err = c.s.channelManager.GetChannel(parentChannelID, wkproto.ChannelTypeCommunity)
			if err != nil {
				return nil, err
			}
			c.Debug("获取父类频道", zap.Any("parentChannel", parentChannel))
		} else {
			c.Warn("不符合的社区话题频道ID", zap.String("channelID", c.ChannelID))
		}
	}
	return parentChannel, nil
}

// ---------- 订阅者 ----------

// IsSubscriber 是否已订阅
func (c *Channel) IsSubscriber(uid string) bool {

	parent, err := c.parentChannel()
	if err != nil {
		c.Error("获取父类频道失败！", zap.Error(err))
	}
	if parent != nil {
		ok := parent.IsSubscriber(uid)
		if ok {
			return ok
		}
	}
	_, ok := c.subscriberMap.Load(uid)
	if ok {
		return ok
	}
	return false
}

// IsTmpSubscriber 是否是临时订阅者
func (c *Channel) IsTmpSubscriber(uid string) bool {
	_, ok := c.tmpSubscriberMap.Load(uid)
	return ok
}

// ---------- 黑名单 (怕怕😱) ----------

// IsDenylist 是否在黑名单内
func (c *Channel) IsDenylist(uid string) bool {
	_, ok := c.blacklist.Load(uid)
	return ok
}

// AddSubscriber Add subscribers
func (c *Channel) AddSubscriber(uid string) {
	c.subscriberMap.Store(uid, true)
}

func (c *Channel) AddSubscribers(uids []string) {
	if len(uids) > 0 {
		for _, uid := range uids {
			c.subscriberMap.Store(uid, true)
		}
	}
}

func (c *Channel) AddTmpSubscriber(uid string) {
	c.tmpSubscriberMap.Store(uid, true)
}

func (c *Channel) AddTmpSubscribers(uids []string) {
	if len(uids) > 0 {
		for _, uid := range uids {
			c.tmpSubscriberMap.Store(uid, true)
		}
	}
}

// Allow Whether to allow sending of messages If it is in the white list or not in the black list, it is allowed to send
func (c *Channel) Allow(uid string) (bool, wkproto.ReasonCode) {

	if c.ChannelType == wkproto.ChannelTypeInfo { // 资讯频道都可以发消息
		return true, wkproto.ReasonSuccess
	}

	systemUID := c.s.systemUIDManager.SystemUID(uid) // 系统账号允许发消息
	if systemUID {
		return true, wkproto.ReasonSuccess
	}

	if c.Ban { // 频道被封
		return false, wkproto.ReasonBan
	}

	if c.ChannelType == wkproto.ChannelTypePerson && c.s.opts.IsFakeChannel(c.ChannelID) {
		if c.IsDenylist(uid) {
			return false, wkproto.ReasonInBlacklist
		}
		return true, wkproto.ReasonSuccess
	}
	if c.ChannelType != wkproto.ChannelTypePerson || c.s.opts.WhitelistOffOfPerson == 0 {
		whitelistLength := 0
		c.whitelist.Range(func(_, _ interface{}) bool {
			whitelistLength++
			return false
		})
		if whitelistLength > 0 { // 如果白名单有内容，则只判断白名单
			_, ok := c.whitelist.Load(uid)
			if ok {
				return ok, wkproto.ReasonSuccess
			}
			return ok, wkproto.ReasonNotInWhitelist
		}
		if c.ChannelType == wkproto.ChannelTypePerson { // 个人频道强制验证白名单，除非WhitelistOffOfPerson==1
			if whitelistLength == 0 {
				return false, wkproto.ReasonNotInWhitelist
			}
		}
	}

	if c.IsDenylist(uid) {
		return false, wkproto.ReasonInBlacklist
	}
	return true, wkproto.ReasonSuccess
}

// real subscribers
func (c *Channel) RealSubscribers(customSubscribers []string) ([]string, error) {

	subscribers := make([]string, 0)                        // TODO: 此处可以用对象pool来管理
	if c.ChannelType == wkproto.ChannelTypeCommunityTopic { // 社区话题频道
		channelSubscribers := c.GetAllSubscribers()
		if len(channelSubscribers) == 0 { // 如果频道无订阅者，则获取父频道的订阅者
			parentChannel, err := c.parentChannel()
			if err != nil {
				c.Error("获取父类频道失败！", zap.Error(err))
				return nil, err
			}
			if parentChannel == nil {
				return nil, errors.New("父类频道不存在！")
			}
			return parentChannel.RealSubscribers(customSubscribers)
		}
	} else if c.ChannelType == wkproto.ChannelTypeInfo {
		subscribers = append(subscribers, c.GetAllTmpSubscribers()...)
	}
	// 组合订阅者

	if len(customSubscribers) > 0 { // 如果指定了订阅者则消息只发给指定的订阅者，不将发送给其他订阅者
		subscribers = append(subscribers, customSubscribers...)
	} else { // 默认将消息发送给频道的订阅者
		subscribers = append(subscribers, c.GetAllSubscribers()...)
	}
	return wkutil.RemoveRepeatedElement(subscribers), nil
}

// GetAllSubscribers 获取所有订阅者
func (c *Channel) GetAllSubscribers() []string {
	subscribers := make([]string, 0)
	c.subscriberMap.Range(func(key, value interface{}) bool {
		subscribers = append(subscribers, key.(string))
		return true
	})
	return subscribers
}

func (c *Channel) GetAllTmpSubscribers() []string {
	subscribers := make([]string, 0)
	c.tmpSubscriberMap.Range(func(key, value interface{}) bool {
		subscribers = append(subscribers, key.(string))
		return true
	})
	return subscribers
}

// RemoveAllSubscriber 移除所有订阅者
func (c *Channel) RemoveAllSubscriber() {
	c.subscriberMap.Range(func(key, value interface{}) bool {
		c.subscriberMap.Delete(key)
		return true
	})
}

// RemoveAllTmpSubscriber 移除所有临时订阅者
func (c *Channel) RemoveAllTmpSubscriber() {
	c.tmpSubscriberMap.Range(func(key, value interface{}) bool {
		c.tmpSubscriberMap.Delete(key)
		return true
	})
}

// RemoveSubscriber  移除订阅者
func (c *Channel) RemoveSubscriber(uid string) {
	c.subscriberMap.Delete(uid)
}

func (c *Channel) RemoveSubscribers(uids []string) {
	if len(uids) > 0 {
		for _, uid := range uids {
			c.subscriberMap.Delete(uid)
		}
	}
}
func (c *Channel) RemoveTmSubscriber(uid string) {
	c.tmpSubscriberMap.Delete(uid)
}

func (c *Channel) RemoveTmpSubscribers(uids []string) {
	if len(uids) > 0 {
		for _, uid := range uids {
			c.tmpSubscriberMap.Delete(uid)
		}
	}
}

// AddDenylist 添加黑名单
func (c *Channel) AddDenylist(uids []string) {
	if len(uids) == 0 {
		return
	}
	for _, uid := range uids {
		c.blacklist.Store(uid, true)
	}
}

// SetDenylist SetDenylist
func (c *Channel) SetDenylist(uids []string) {
	c.blacklist.Range(func(key interface{}, value interface{}) bool {
		c.blacklist.Delete(key)
		return true
	})
	c.AddDenylist(uids)
}

// RemoveDenylist 移除黑名单
func (c *Channel) RemoveDenylist(uids []string) {
	if len(uids) == 0 {
		return
	}
	for _, uid := range uids {
		c.blacklist.Delete(uid)
	}
}

// ---------- 白名单 ----------

// AddAllowlist 添加白名单
func (c *Channel) AddAllowlist(uids []string) {
	if len(uids) == 0 {
		return
	}
	for _, uid := range uids {
		c.whitelist.Store(uid, true)
	}

}

// SetAllowlist SetAllowlist
func (c *Channel) SetAllowlist(uids []string) {
	c.whitelist.Range(func(key interface{}, value interface{}) bool {
		c.whitelist.Delete(key)
		return true
	})
	c.AddAllowlist(uids)
}

// RemoveAllowlist 移除白名单
func (c *Channel) RemoveAllowlist(uids []string) {
	if len(uids) == 0 {
		return
	}
	for _, uid := range uids {
		c.whitelist.Delete(uid)
	}
}

func (c *Channel) Put(messages []*Message, customSubscribers []string, fromUID string, fromDeviceFlag wkproto.DeviceFlag, fromDeviceID string) error {
	//########## get subscribers ##########
	subscribers, err := c.RealSubscribers(customSubscribers) // get subscribers
	if err != nil {
		c.Error("获取频道失败！", zap.Error(err))
		return err
	}

	//########## store messages in user queue ##########
	var messageSeqMap map[int64]uint32
	if len(messages) > 0 {
		messageSeqMap, err = c.storeMessageToUserQueueIfNeed(messages, subscribers)
		if err != nil {
			return err
		}
	}

	// ########## update conversation ##########
	if !c.Large && c.ChannelType != wkproto.ChannelTypeInfo { // 如果是大群 则不维护最近会话 几万人的大群，更新最近会话也太耗性能
		var lastMsg *Message
		for i := len(messages) - 1; i >= 0; i-- {
			m := messages[i]
			if !m.NoPersist && !m.SyncOnce {
				lastMsg = messages[i]
				break
			}
		}
		if lastMsg != nil {
			c.updateConversations(lastMsg, subscribers)
		}
	}

	//########## delivery messages ##########
	c.s.deliveryManager.startDeliveryMessages(messages, c.Large, messageSeqMap, subscribers, fromUID, fromDeviceFlag, fromDeviceID)

	return nil
}

// store message to user queue if need
func (c *Channel) storeMessageToUserQueueIfNeed(messages []*Message, subscribers []string) (map[int64]uint32, error) {
	if len(messages) == 0 {
		return nil, nil
	}

	messageSeqMap := make(map[int64]uint32, len(messages))

	for _, subscriber := range subscribers {
		storeMessages := make([]wkstore.Message, 0, len(messages))
		for _, m := range messages {

			if m.NoPersist || !m.SyncOnce {
				continue
			}

			cloneMsg, err := m.DeepCopy()
			if err != nil {
				return nil, err
			}
			cloneMsg.ToUID = subscriber
			cloneMsg.large = c.Large
			if m.ChannelType == wkproto.ChannelTypePerson && m.ChannelID == subscriber {
				cloneMsg.ChannelID = m.FromUID
			}
			storeMessages = append(storeMessages, cloneMsg)
		}
		if len(storeMessages) > 0 {
			_, err := c.s.store.AppendMessagesOfUser(subscriber, storeMessages) // will fill messageSeq after store messages
			if err != nil {
				return nil, err
			}
			for _, storeMessage := range storeMessages {
				messageSeqMap[storeMessage.GetMessageID()] = storeMessage.GetSeq()
			}
		}
	}
	return messageSeqMap, nil
}

func (c *Channel) updateConversations(m *Message, subscribers []string) {
	c.s.conversationManager.PushMessage(m, subscribers)

}
