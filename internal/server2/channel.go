package server

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type channel struct {
	key         string
	channelId   string
	channelType uint8

	msgQueue *channelMsgQueue

	actions []*ChannelAction

	// 热点发送者 （比较活跃有权限的发送者）
	hotspotSenders map[string]struct{}

	// options
	stroageMaxSize uint64 // 每次存储的最大字节数量
	deliverMaxSize uint64 // 每次投递的最大字节数量

	r   *channelReactor
	sub *channelReactorSub

	wklog.Log
}

func newChannel(sub *channelReactorSub, channelId string, channelType uint8) *channel {
	key := ChannelToKey(channelId, channelType)
	return &channel{
		key:            key,
		channelId:      channelId,
		channelType:    channelType,
		msgQueue:       newChannelMsgQueue(channelId),
		hotspotSenders: make(map[string]struct{}),
		stroageMaxSize: 1024 * 1024 * 2,
		deliverMaxSize: 1024 * 1024 * 2,
		Log:            wklog.NewWKLog(fmt.Sprintf("channel[%s]", key)),
		r:              sub.r,
		sub:            sub,
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
			// fmt.Println("存储消息--->", len(msgs))
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

// 是否是热点发送者
func (c *channel) hotspotSender(uid string) bool {
	_, ok := c.hotspotSenders[uid]
	return ok
}

// 设置为热点发送者
func (c *channel) setHotspotSender(uid string) {
	c.hotspotSenders[uid] = struct{}{}
}

type ready struct {
	actions []*ChannelAction
}
