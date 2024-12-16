package reactor

import "github.com/WuKongIM/WuKongIM/pkg/wkdb"

type IChannel interface {
	// AddAction 添加action，返回是否添加成功
	AddAction(action ChannelAction) bool

	// WakeIfNeed 唤醒频道，如果频道不存在
	WakeIfNeed(channelId string, channelType uint8)

	// Advance 推进，让频道立即执行下一个动作
	Advance(channelId string, channelType uint8)
}

type ChannelPlus struct {
	ch IChannel
}

func newChannelPlus(ch IChannel) *ChannelPlus {
	return &ChannelPlus{
		ch: ch,
	}
}

func (c *ChannelPlus) WakeIfNeed(channelId string, channelType uint8) {
	c.ch.WakeIfNeed(channelId, channelType)
}

// SendMessage 发送消息
func (c *ChannelPlus) SendMessage(message *ChannelMessage) bool {
	added := c.ch.AddAction(ChannelAction{
		FakeChannelId: message.FakeChannelId,
		ChannelType:   message.ChannelType,
		Type:          ChannelActionInboundAdd,
		Messages: []*ChannelMessage{
			message,
		},
	})
	c.ch.Advance(message.FakeChannelId, message.ChannelType)
	return added
}

func (c *ChannelPlus) AddMessage(message *ChannelMessage) bool {
	added := c.ch.AddAction(ChannelAction{
		FakeChannelId: message.FakeChannelId,
		ChannelType:   message.ChannelType,
		Type:          ChannelActionInboundAdd,
		Messages: []*ChannelMessage{
			message,
		},
	})
	c.ch.Advance(message.FakeChannelId, message.ChannelType)
	return added

}

func (c *ChannelPlus) AddMessages(fakeChannelId string, channelType uint8, messages []*ChannelMessage) bool {
	added := c.ch.AddAction(ChannelAction{
		FakeChannelId: fakeChannelId,
		ChannelType:   channelType,
		Type:          ChannelActionInboundAdd,
		Messages:      messages,
	})
	c.ch.Advance(fakeChannelId, channelType)
	return added

}

func (c *ChannelPlus) AddMessageToOutbound(message *ChannelMessage) bool {
	added := c.ch.AddAction(ChannelAction{
		FakeChannelId: message.FakeChannelId,
		ChannelType:   message.ChannelType,
		Type:          ChannelActionOutboundAdd,
		Messages: []*ChannelMessage{
			message,
		},
	})
	c.ch.Advance(message.FakeChannelId, message.ChannelType)
	return added
}

// UpdateConfig 更新配置
func (c *ChannelPlus) UpdateConfig(channelId string, channelType uint8, cfg ChannelConfig) {
	c.ch.AddAction(ChannelAction{
		Type:          ChannelActionConfigUpdate,
		FakeChannelId: channelId,
		ChannelType:   channelType,
		Cfg:           cfg,
	})
}

// UpdateChannelInfo 更新缓存中频道的基础信息
func (c *ChannelPlus) UpdateChannelInfo(channelInfo wkdb.ChannelInfo) {

}

// AddSubscribers 添加订阅者
func (c *ChannelPlus) AddSubscribers(channelId string, channelType uint8, subscribers []string) {

}

// RemoveSubscribers 移除订阅者
func (c *ChannelPlus) RemoveSubscribers(channelId string, channelType uint8, subscribers []string) {

}

// AddTempSubscribers 添加临时订阅者
func (c *ChannelPlus) AddTempSubscribers(channelId string, channelType uint8, subscribers []string) {

}
