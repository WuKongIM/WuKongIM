package server

import (
	"fmt"
	"sync"

	"github.com/WuKongIM/WuKongIM/internal/server/cluster/rpc"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type Channel struct {
	wkdb.ChannelInfo
	blacklist     sync.Map // 黑名单
	whitelist     sync.Map // 白名单
	subscriberMap sync.Map // 订阅者
	s             *Server
	wklog.Log
	tmpSubscriberMap sync.Map // 临时订阅者
}

// NewChannel NewChannel
func NewChannel(channelInfo wkdb.ChannelInfo, s *Server) *Channel {

	return &Channel{
		ChannelInfo:      channelInfo,
		blacklist:        sync.Map{},
		whitelist:        sync.Map{},
		subscriberMap:    sync.Map{},
		tmpSubscriberMap: sync.Map{},
		s:                s,
		Log:              wklog.NewWKLog(fmt.Sprintf("channel[%s-%d]", channelInfo.ChannelId, channelInfo.ChannelType)),
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
	if c.ChannelInfo.ChannelType == wkproto.ChannelTypePerson {
		return nil
	}
	channelInfo, err := c.s.datasource.GetChannelInfo(c.ChannelInfo.ChannelId, c.ChannelInfo.ChannelType)
	if err != nil {
		c.Error("从数据获取频道信息失败！", zap.Error(err))
		return err
	}
	if !wkdb.IsEmptyChannelInfo(channelInfo) {
		c.ChannelInfo = channelInfo
		return nil
	}
	return nil
}

// 初始化订阅者
func (c *Channel) initSubscribers() error {
	channelID := c.ChannelInfo.ChannelId
	channelType := c.ChannelInfo.ChannelType
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
		blacklists, err = c.s.datasource.GetBlacklist(c.ChannelInfo.ChannelId, c.ChannelInfo.ChannelType)
		if err != nil {
			c.Error("从数据源获取黑名单失败！", zap.Error(err))
			return err
		}
	} else {
		blacklists, err = c.s.store.GetDenylist(c.ChannelInfo.ChannelId, c.ChannelInfo.ChannelType)
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
		whitelists, err = c.s.datasource.GetWhitelist(c.ChannelInfo.ChannelId, c.ChannelInfo.ChannelType)
		if err != nil {
			c.Error("从数据源获取白名单失败！", zap.Error(err))
			return err
		}
	} else {
		whitelists, err = c.s.store.GetAllowlist(c.ChannelInfo.ChannelId, c.ChannelInfo.ChannelType)
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
	if c.ChannelInfo.ChannelType == wkproto.ChannelTypeCommunityTopic {
		parentChannelID := GetCommunityTopicParentChannelID(c.ChannelInfo.ChannelId)
		if parentChannelID != "" {
			var err error
			parentChannel, err = c.s.channelManager.GetChannel(parentChannelID, wkproto.ChannelTypeCommunity)
			if err != nil {
				return nil, err
			}
			c.Debug("获取父类频道", zap.Any("parentChannel", parentChannel))
		} else {
			c.Warn("不符合的社区话题频道ID", zap.String("channelID", c.ChannelId))
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

// ---------- 黑名单  ----------

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

	if c.ChannelInfo.ChannelType == wkproto.ChannelTypeInfo { // 资讯频道都可以发消息
		return true, wkproto.ReasonSuccess
	}

	systemUID := c.s.systemUIDManager.SystemUID(uid) // 系统账号允许发消息
	if systemUID {
		c.Debug("system account is allowed to send messages", zap.String("uid", uid))
		return true, wkproto.ReasonSuccess
	}

	if c.ChannelInfo.Ban { // 频道被封
		c.Debug("channel is banned", zap.String("uid", uid))
		return false, wkproto.ReasonBan
	}
	if c.Disband { // 频道已解散
		c.Debug("channel is disband", zap.String("uid", uid))
		return false, wkproto.ReasonDisband
	}

	if c.IsDenylist(uid) { // 黑名单判断
		c.Debug("in blacklist", zap.String("uid", uid))
		return false, wkproto.ReasonInBlacklist
	}

	// if c.ChannelType == wkproto.ChannelTypePerson && c.s.opts.IsFakeChannel(c.ChannelID) {
	// 	if c.IsDenylist(uid) {
	// 		return false, wkproto.ReasonInBlacklist
	// 	}
	// 	return true, wkproto.ReasonSuccess
	// }
	if c.ChannelInfo.ChannelType != wkproto.ChannelTypePerson || !c.s.opts.WhitelistOffOfPerson {
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
			c.Debug("not in whitelist", zap.String("uid", uid))
			return ok, wkproto.ReasonNotInWhitelist
		}
		if c.ChannelInfo.ChannelType == wkproto.ChannelTypePerson { // 个人频道强制验证白名单，除非WhitelistOffOfPerson==true
			if whitelistLength == 0 {
				c.Debug("whitelist is empty", zap.String("uid", uid))
				return false, wkproto.ReasonNotInWhitelist
			}
		}
	}

	return true, wkproto.ReasonSuccess
}

// real subscribers
func (c *Channel) RealSubscribers(customSubscribers []string) ([]string, error) {

	subscribers := make([]string, 0)                                    // TODO: 此处可以用对象pool来管理
	if c.ChannelInfo.ChannelType == wkproto.ChannelTypeCommunityTopic { // 社区话题频道
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
	} else if c.ChannelInfo.ChannelType == wkproto.ChannelTypeInfo {
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
	//########## 获取订阅者 ##########
	subscribers, err := c.RealSubscribers(customSubscribers) // get subscribers
	if err != nil {
		c.Error("获取频道失败！", zap.Error(err))
		return err
	}
	// c.Debug("订阅者数量", zap.Any("subscribers", len(subscribers)))
	if len(subscribers) == 0 {
		return nil
	}
	channel := &wkproto.Channel{
		ChannelID:   c.ChannelInfo.ChannelId,
		ChannelType: c.ChannelInfo.ChannelType,
	}
	if c.s.opts.ClusterOn() {
		nodeIDSubscribersMap, err := c.s.calcNodeSubscribers(subscribers)
		if err != nil {
			c.Error("计算订阅者所在节点失败！", zap.Error(err))
			return err
		}
		for nodeID, subscribers := range nodeIDSubscribersMap {
			if nodeID == c.s.opts.Cluster.NodeId {
				err = c.s.dispatch.processor.handleLocalSubscribersMessages(messages, c.ChannelInfo.Large, subscribers, fromUID, fromDeviceFlag, fromDeviceID, channel)
				if err != nil {
					c.Error("处理本地订阅者消息失败！", zap.Error(err))
					return err
				}
			} else {
				err = c.forwardToOtherPeerSubscribers(messages, c.ChannelInfo.Large, nodeID, subscribers, fromUID, fromDeviceFlag, fromDeviceID)
				if err != nil {
					c.Error("转发消息失败！", zap.Error(err))
					return err
				}
			}
		}
	} else {
		return c.s.dispatch.processor.handleLocalSubscribersMessages(messages, c.ChannelInfo.Large, subscribers, fromUID, fromDeviceFlag, fromDeviceID, channel)
	}

	return nil
}

// forward to other node subscribers
func (c *Channel) forwardToOtherPeerSubscribers(messages []*Message, large bool, nodeId uint64, subscribers []string, fromUID string, fromDeviceFlag wkproto.DeviceFlag, fromDeviceID string) error {
	recvPacketDatas := make([]byte, 0)
	for _, m := range messages {
		recvPacket := m.RecvPacket
		data, err := c.s.opts.Proto.EncodeFrame(&recvPacket, wkproto.LatestVersion)
		if err != nil {
			c.Error("encode recvPacket err", zap.Error(err))
			return err
		}
		recvPacketDatas = append(recvPacketDatas, data...)
	}
	req := &rpc.ForwardRecvPacketReq{
		No:             wkutil.GenUUID(),
		Subscribers:    subscribers,
		Messages:       recvPacketDatas,
		FromUID:        fromUID,
		FromDeviceFlag: uint32(fromDeviceFlag),
		Large:          large,
		FromDeviceID:   fromDeviceID,
		ProtoVersion:   wkproto.LatestVersion,
	}
	reqData, _ := req.Marshal()
	c.s.startDeliveryPeerData(&PeerInFlightData{
		PeerInFlightDataModel: PeerInFlightDataModel{
			No:     req.No,
			PeerID: nodeId,
			Data:   reqData,
		},
	})
	return nil
}
