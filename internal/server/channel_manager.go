package server

import (
	"fmt"
	"strings"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/pkg/errors"
)

// ---------- 频道管理 ----------

// ChannelManager 频道管理
type ChannelManager struct {
	s                *Server
	channelCache     *lru.Cache[string, *Channel]
	tmpChannelCache  *lru.Cache[string, *Channel] // 系统消息临时频道
	dataChannelCache sync.Map                     // 数据频道缓存，订阅者为0的时候应该删除频道本身
	wklog.Log
}

// NewChannelManager 创建一个频道管理者
func NewChannelManager(s *Server) *ChannelManager {
	cache, err := lru.NewWithEvict(s.opts.TmpChannel.CacheCount, func(key string, value *Channel) {
	})
	if err != nil {
		panic(err)
	}

	channelCache, err := lru.NewWithEvict(s.opts.Channel.CacheCount, func(key string, value *Channel) {
	})
	if err != nil {
		panic(err)
	}

	return &ChannelManager{
		channelCache:     channelCache,
		tmpChannelCache:  cache,
		dataChannelCache: sync.Map{},
		s:                s,
		Log:              wklog.NewWKLog("ChannelManager"),
	}
}

// GetChannel 获取频道
func (cm *ChannelManager) GetChannel(channelID string, channelType uint8) (*Channel, error) {

	if strings.HasSuffix(channelID, cm.s.opts.TmpChannel.Suffix) {
		return cm.GetTmpChannel(channelID, channelType)
	}
	if channelType == wkproto.ChannelTypePerson {
		return cm.GetPersonChannel(channelID, channelType)
	}
	if channelType == wkproto.ChannelTypeData {
		return cm.GetOrCreateDataChannel(channelID, channelType), nil
	}
	channel, err := cm.getChannelFromCacheOrStore(channelID, channelType)
	if err != nil {
		return nil, err
	}
	return channel, nil
}

func (cm *ChannelManager) getChannelFromCacheOrStore(channelID string, channelType uint8) (*Channel, error) {
	channelCache := cm.getChannelFromCache(channelID, channelType)
	if channelCache != nil {
		return channelCache, nil
	}
	channelInfo, err := cm.s.store.GetChannel(channelID, channelType)
	if err != nil {
		return nil, err
	}
	if channelInfo == nil {
		if cm.s.opts.Channel.CreateIfNoExist || channelType == wkproto.ChannelTypeCommunityTopic || channelType == wkproto.ChannelTypeInfo {
			channelInfo = wkstore.NewChannelInfo(channelID, channelType)
		} else {
			return nil, nil
		}
	}
	channel := NewChannel(channelInfo, cm.s)
	err = channel.LoadData()
	if err != nil {
		return nil, err
	}
	cm.setChannelFromCache(channel)
	return channel, nil
}

func (cm *ChannelManager) getChannelFromCache(channelID string, channelType uint8) *Channel {
	key := fmt.Sprintf("%s-%d", channelID, channelType)
	channelObj, _ := cm.channelCache.Get(key)
	if channelObj != nil {
		return channelObj
	}
	return nil
}
func (cm *ChannelManager) setChannelFromCache(channel *Channel) {
	key := fmt.Sprintf("%s-%d", channel.ChannelID, channel.ChannelType)
	cm.channelCache.Add(key, channel)
}

// CreateOrUpdatePersonChannel 创建或更新个人频道
func (cm *ChannelManager) CreateOrUpdatePersonChannel(uid string) error {
	exist, err := cm.s.store.ExistChannel(uid, wkproto.ChannelTypePerson)
	if err != nil {
		return errors.Wrap(err, "查询是否存在频道信息失败！")
	}
	if !exist {
		err = cm.s.store.AddOrUpdateChannel(&wkstore.ChannelInfo{
			ChannelID:   uid,
			ChannelType: wkproto.ChannelTypePerson,
		})
		if err != nil {
			return errors.Wrap(err, "创建个人频道失败！")
		}
	}
	subscribers, err := cm.s.store.GetSubscribers(uid, wkproto.ChannelTypePerson)
	if err != nil {
		return errors.Wrap(err, "获取频道订阅者失败！")
	}
	if len(subscribers) == 0 || !wkutil.ArrayContains(subscribers, uid) {
		err = cm.s.store.AddSubscribers(uid, wkproto.ChannelTypePerson, []string{uid})
		if err != nil {
			return errors.Wrap(err, "添加订阅者失败！")
		}
	}
	return nil
}

// CreateTmpChannel 创建临时频道
func (cm *ChannelManager) CreateTmpChannel(channelID string, channelType uint8, subscribers []string) error {
	channel := NewChannel(wkstore.NewChannelInfo(channelID, channelType), cm.s)
	if len(subscribers) > 0 {
		for _, subscriber := range subscribers {
			channel.AddSubscriber(subscriber)
		}
	}
	cm.s.monitor.TmpChannelCacheCountInc()
	cm.tmpChannelCache.Add(fmt.Sprintf("%s-%d", channelID, channelType), channel)
	return nil
}

// GetPersonChannel 创建临时频道
func (cm *ChannelManager) GetPersonChannel(channelID string, channelType uint8) (*Channel, error) {
	key := fmt.Sprintf("%s-%d", channelID, channelType)
	v, ok := cm.channelCache.Get(key)
	if ok {
		return v, nil
	}
	channel := NewChannel(wkstore.NewChannelInfo(channelID, channelType), cm.s)
	err := channel.LoadData()
	if err != nil {
		return nil, err
	}
	if cm.s.opts.IsFakeChannel(channelID) { // fake个人频道
		subscribers := strings.Split(channelID, "@")
		for _, subscriber := range subscribers {
			channel.AddSubscriber(subscriber)
		}
	} else {
		channel.AddSubscriber(channelID) // 将频道添加到订阅者里
	}
	cm.s.monitor.ChannelCacheCountInc()
	cm.channelCache.Add(key, channel)
	return channel, nil
}

// GetTmpChannel 获取临时频道
func (cm *ChannelManager) GetTmpChannel(channelID string, channelType uint8) (*Channel, error) {
	v, _ := cm.tmpChannelCache.Get(fmt.Sprintf("%s-%d", channelID, channelType)) // 临时频道可能会被挤掉

	return v, nil
}

func (cm *ChannelManager) GetOrCreateDataChannel(channelID string, channelType uint8) *Channel {
	channelObj, _ := cm.dataChannelCache.Load(fmt.Sprintf("%s-%d", channelID, channelType))
	var channel *Channel
	if channelObj == nil {
		channel = NewChannel(wkstore.NewChannelInfo(channelID, channelType), cm.s)
		cm.dataChannelCache.Store(fmt.Sprintf("%s-%d", channelID, channelType), channel)
	} else {
		channel = channelObj.(*Channel)
	}
	return channel
}

func (cm *ChannelManager) RemoveDataChannel(channelID string, channelType uint8) {
	cm.dataChannelCache.Delete(fmt.Sprintf("%s-%d", channelID, channelType))
}

// DeleteChannel 删除频道
func (cm *ChannelManager) DeleteChannel(channelID string, channelType uint8) error {
	err := cm.s.store.DeleteChannel(channelID, channelType)
	if err != nil {
		return err
	}
	cm.DeleteChannelFromCache(channelID, channelType)
	return nil
}

// DeleteChannelFromCache DeleteChannelFromCache
func (cm *ChannelManager) DeleteChannelFromCache(channelID string, channelType uint8) {
	cm.s.monitor.ChannelCacheCountDec()
	cm.channelCache.Remove(fmt.Sprintf("%s-%d", channelID, channelType))
}
