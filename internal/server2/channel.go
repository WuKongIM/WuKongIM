package server

import (
	"fmt"
	"strings"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

type channel struct {
	key         string
	channelId   string
	channelType uint8

	info wkdb.ChannelInfo

	msgQueue *channelMsgQueue

	actions []*ChannelAction

	// 缓存的订阅者 （不是全部的频道订阅者，是比较活跃的订阅者）
	cacheSubscribers map[string]struct{}

	// options
	stroageMaxSize uint64 // 每次存储的最大字节数量
	deliverMaxSize uint64 // 每次投递的最大字节数量

	r   *channelReactor
	sub *channelReactorSub

	mu sync.Mutex

	wklog.Log
}

func newChannel(sub *channelReactorSub, channelId string, channelType uint8) *channel {
	key := ChannelToKey(channelId, channelType)
	return &channel{
		key:              key,
		channelId:        channelId,
		channelType:      channelType,
		msgQueue:         newChannelMsgQueue(channelId),
		cacheSubscribers: make(map[string]struct{}),
		stroageMaxSize:   1024 * 1024 * 2,
		deliverMaxSize:   1024 * 1024 * 2,
		Log:              wklog.NewWKLog(fmt.Sprintf("channel[%s]", key)),
		r:                sub.r,
		sub:              sub,
	}

}

func (c *channel) hasReady() bool {

	if c.hasUnstorage() {
		return true
	}

	if c.hasUnDeliver() {
		return true
	}

	return len(c.actions) > 0
}

func (c *channel) ready() ready {
	// 如果有未存储的消息，则继续存储
	if c.hasUnstorage() {
		msgs := c.msgQueue.sliceWithSize(c.msgQueue.storagedIndex+1, c.msgQueue.lastIndex+1, c.stroageMaxSize)
		if len(msgs) > 0 {
			lastMsg := msgs[len(msgs)-1]
			c.msgQueue.storagingIndex = lastMsg.Index
			c.exec(&ChannelAction{ActionType: ChannelActionStorage, Messages: msgs})
		}

	}

	// 投递消息
	if c.hasUnDeliver() {
		msgs := c.msgQueue.sliceWithSize(c.msgQueue.deliveringIndex+1, c.msgQueue.storagedIndex+1, c.deliverMaxSize)
		if len(msgs) > 0 {
			c.exec(&ChannelAction{ActionType: ChannelActionDeliver, Messages: msgs})
		}
		c.msgQueue.deliveringIndex = c.msgQueue.storagedIndex

	}
	actions := c.actions
	c.actions = nil
	return ready{
		actions: actions,
	}
}

// 有未存储的消息
func (c *channel) hasUnstorage() bool {
	if c.msgQueue.storagingIndex > c.msgQueue.storagedIndex { // 如果正在存储的下标大于已存储的下标，则说明有消息正在存储，等待存储完毕
		return false
	}
	return c.msgQueue.lastIndex > c.msgQueue.storagingIndex
}

// 有未投递的消息
func (c *channel) hasUnDeliver() bool {
	return c.msgQueue.deliveringIndex < c.msgQueue.storagedIndex
}

func (c *channel) tick() {

}

func (c *channel) proposeSend(fromUid string, deviceId string, sendPacket *wkproto.SendPacket) error {

	messageId := c.r.messageIDGen.Generate().Int64() // 生成唯一消息ID
	message := &ReactorChannelMessage{
		FromUid:      fromUid,
		FromDeviceId: deviceId,
		SendPacket:   sendPacket,
		MessageId:    messageId,
	}

	err := c.sub.stepWait(c, &ChannelAction{
		ActionType: ChannelActionSend,
		Messages:   []*ReactorChannelMessage{message},
	})
	if err != nil {
		return err
	}
	return nil
}

func (c *channel) advance() {
	c.sub.advance()
}

// 是否是缓存中的订阅者
func (c *channel) isCacheSubscriber(uid string) bool {
	_, ok := c.cacheSubscribers[uid]
	return ok
}

// 设置为缓存订阅者
func (c *channel) setCacheSubscriber(uid string) {
	c.cacheSubscribers[uid] = struct{}{}
}

type ready struct {
	actions []*ChannelAction
}

func (c *channel) makeReceiverTag() (*tag, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var subscribers []string
	var err error
	if c.channelType == wkproto.ChannelTypePerson {
		if c.r.s.opts.IsFakeChannel(c.channelId) { // fake个人频道
			subscribers = strings.Split(c.channelId, "@")
		}
	} else {
		subscribers, err = c.r.s.store.GetSubscribers(c.channelId, c.channelType)
		if err != nil {
			return nil, err
		}
	}

	var nodeUserList = make([]*nodeUsers, 0, 20)
	for _, subscriber := range subscribers {
		leaderInfo, err := c.r.s.cluster.SlotLeaderOfChannel(subscriber, wkproto.ChannelTypePerson) // 获取频道的槽领导节点
		if err != nil {
			c.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", subscriber), zap.Uint8("channelType", wkproto.ChannelTypePerson))
			return nil, err
		}
		exist := false
		for _, nodeUser := range nodeUserList {
			if nodeUser.nodeId == leaderInfo.Id {
				nodeUser.uids = append(nodeUser.uids, subscriber)
				exist = true
				break
			}
		}
		if !exist {
			nodeUserList = append(nodeUserList, &nodeUsers{
				nodeId: leaderInfo.Id,
				uids:   []string{subscriber},
			})
		}
	}
	newTag := c.r.s.tagManager.addOrUpdateReceiverTag(c.channelId, c.channelType, nodeUserList)
	return newTag, nil
}
