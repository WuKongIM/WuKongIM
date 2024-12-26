package eventbus

var Channel *channelPlus

type IChannel interface {
	// AddEvent 添加事件
	AddEvent(channelId string, channelType uint8, event *Event)
	// Advance 推进事件（让事件池不需要等待直接执行下一轮事件）
	Advance(channelId string, channelType uint8)
}

type channelPlus struct {
	channel IChannel
}

func newChannelPlus(channel IChannel) *channelPlus {
	return &channelPlus{
		channel: channel,
	}
}

func (c *channelPlus) AddEvent(channelId string, channelType uint8, event *Event) {
	c.channel.AddEvent(channelId, channelType, event)
}

func (c *channelPlus) AddEvents(channelId string, channelType uint8, events []*Event) {
	for _, event := range events {
		c.channel.AddEvent(channelId, channelType, event)
	}
}

func (c *channelPlus) Advance(channelId string, channelType uint8) {
	c.channel.Advance(channelId, channelType)
}

// SendMessage 发送消息
func (c *channelPlus) SendMessage(channelId string, channelType uint8, event *Event) {
	c.channel.AddEvent(channelId, channelType, event)
}
